package agent

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

type resourceObservation struct {
	readers int
	writer  bool
}

func TestResourceCoordinatorReadLocksShareAKey(t *testing.T) {
	coordinator := NewResourceCoordinator()
	first, err := coordinator.Acquire(context.Background(), ResourceRequest{Key: "shared", Mode: ResourceRead})
	if err != nil {
		t.Fatalf("acquire first read: %v", err)
	}
	defer first.Release()

	acquired := make(chan *ResourceLease, 1)
	errs := make(chan error, 1)
	go func() {
		lease, err := coordinator.Acquire(context.Background(), ResourceRequest{Key: "shared", Mode: ResourceRead})
		if err != nil {
			errs <- err
			return
		}
		acquired <- lease
	}()

	select {
	case err := <-errs:
		t.Fatalf("acquire second read: %v", err)
	case lease := <-acquired:
		lease.Release()
	case <-time.After(time.Second):
		t.Fatal("second read waited for an existing read lease")
	}
}

func TestResourceCoordinatorWriteExcludesSameKey(t *testing.T) {
	coordinator := NewResourceCoordinator()
	writer, err := coordinator.Acquire(context.Background(), ResourceRequest{Key: "shared", Mode: ResourceWrite})
	if err != nil {
		t.Fatalf("acquire writer: %v", err)
	}

	readAcquired := make(chan *ResourceLease, 1)
	go func() {
		lease, acquireErr := coordinator.Acquire(context.Background(), ResourceRequest{Key: "shared", Mode: ResourceRead})
		if acquireErr == nil {
			readAcquired <- lease
		}
	}()

	select {
	case lease := <-readAcquired:
		lease.Release()
		writer.Release()
		t.Fatal("read acquired while writer held the key")
	case <-time.After(40 * time.Millisecond):
	}

	writer.Release()
	select {
	case lease := <-readAcquired:
		lease.Release()
	case <-time.After(time.Second):
		t.Fatal("read did not acquire after writer release")
	}
}

func TestResourceCoordinatorDifferentKeysProceedIndependently(t *testing.T) {
	coordinator := NewResourceCoordinator()
	alpha, err := coordinator.Acquire(context.Background(), ResourceRequest{Key: "alpha", Mode: ResourceWrite})
	if err != nil {
		t.Fatalf("acquire alpha: %v", err)
	}
	defer alpha.Release()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	beta, err := coordinator.Acquire(ctx, ResourceRequest{Key: "beta", Mode: ResourceWrite})
	if err != nil {
		t.Fatalf("independent beta writer blocked by alpha: %v", err)
	}
	beta.Release()
}

func TestResourceCoordinatorCancellationAndTimeoutDoNotLeak(t *testing.T) {
	coordinator := NewResourceCoordinator()
	held, err := coordinator.Acquire(context.Background(), ResourceRequest{Key: "alpha", Mode: ResourceWrite})
	if err != nil {
		t.Fatalf("acquire holder: %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, acquireErr := coordinator.Acquire(cancelled, ResourceRequest{Key: "alpha", Mode: ResourceRead})
		result <- acquireErr
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		held.Release()
		t.Fatalf("cancelled wait error = %v, want context.Canceled", err)
	}

	timedOut, stopTimeout := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer stopTimeout()
	_, err = coordinator.Acquire(timedOut,
		ResourceRequest{Key: "alpha", Mode: ResourceRead},
		ResourceRequest{Key: "beta", Mode: ResourceWrite},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		held.Release()
		t.Fatalf("timed out wait error = %v, want context.DeadlineExceeded", err)
	}

	// beta was part of the timed-out bundle. It must not remain reserved.
	beta, err := coordinator.Acquire(context.Background(), ResourceRequest{Key: "beta", Mode: ResourceWrite})
	if err != nil {
		held.Release()
		t.Fatalf("beta leaked after timed-out multi-resource acquire: %v", err)
	}
	beta.Release()
	held.Release()

	alpha, err := coordinator.Acquire(context.Background(), ResourceRequest{Key: "alpha", Mode: ResourceWrite})
	if err != nil {
		t.Fatalf("alpha leaked after cancelled waits: %v", err)
	}
	alpha.Release()
}

func TestResourceCoordinatorCanonicalizesOrderAndStrength(t *testing.T) {
	coordinator := NewResourceCoordinator()
	lease, err := coordinator.Acquire(context.Background(),
		ResourceRequest{Key: " beta ", Mode: ResourceRead},
		ResourceRequest{Key: "alpha", Mode: ResourceRead},
		ResourceRequest{Key: "alpha", Mode: ResourceWrite},
	)
	if err != nil {
		t.Fatalf("acquire canonicalized set: %v", err)
	}
	defer lease.Release()

	requests := lease.Requests()
	if len(requests) != 2 ||
		requests[0] != (ResourceRequest{Key: "alpha", Mode: ResourceWrite}) ||
		requests[1] != (ResourceRequest{Key: "beta", Mode: ResourceRead}) {
		t.Fatalf("canonical requests = %#v", requests)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	_, err = coordinator.Acquire(ctx, ResourceRequest{Key: "alpha", Mode: ResourceRead})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read acquired despite coalesced writer, err = %v", err)
	}
}

func TestResourceCoordinatorHighConcurrencyRegression(t *testing.T) {
	coordinator := NewResourceCoordinator()
	const workers = 48
	const iterations = 80

	active := make(map[string]resourceObservation)
	var activeMu sync.Mutex
	var workersGroup sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, workers)

	for worker := 0; worker < workers; worker++ {
		workersGroup.Add(1)
		go func(worker int) {
			defer workersGroup.Done()
			<-start
			for iteration := 0; iteration < iterations; iteration++ {
				requests := resourceRequestsForConcurrency(worker, iteration)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				lease, err := coordinator.Acquire(ctx, requests...)
				cancel()
				if err != nil {
					errs <- fmt.Errorf("worker %d iteration %d acquire: %w", worker, iteration, err)
					return
				}

				activeMu.Lock()
				violation := observeResourceAcquire(active, requests)
				activeMu.Unlock()
				if violation != nil {
					lease.Release()
					errs <- fmt.Errorf("worker %d iteration %d: %w", worker, iteration, violation)
					return
				}

				runtime.Gosched()

				activeMu.Lock()
				observeResourceRelease(active, requests)
				activeMu.Unlock()
				lease.Release()
			}
		}(worker)
	}

	close(start)
	workersGroup.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if len(coordinator.waiters) != 0 {
		t.Fatalf("waiters left after concurrent work: %d", len(coordinator.waiters))
	}
	if len(coordinator.resources) != 0 {
		t.Fatalf("resource locks left after concurrent work: %#v", coordinator.resources)
	}
}

func resourceRequestsForConcurrency(worker, iteration int) []ResourceRequest {
	switch (worker + iteration) % 8 {
	case 0:
		return []ResourceRequest{
			{Key: "beta", Mode: ResourceWrite},
			{Key: "alpha", Mode: ResourceWrite},
		}
	case 1:
		return []ResourceRequest{
			{Key: "alpha", Mode: ResourceRead},
			{Key: "beta", Mode: ResourceRead},
		}
	default:
		keys := []string{"alpha", "beta", "gamma", "delta"}
		mode := ResourceRead
		if (worker*3+iteration)%5 == 0 {
			mode = ResourceWrite
		}
		return []ResourceRequest{{Key: keys[(worker+iteration)%len(keys)], Mode: mode}}
	}
}

func observeResourceAcquire(active map[string]resourceObservation, requests []ResourceRequest) error {
	for _, request := range requests {
		state := active[request.Key]
		if request.Mode == ResourceWrite {
			if state.writer || state.readers != 0 {
				return fmt.Errorf("write overlapped active state for %q: %#v", request.Key, state)
			}
			state.writer = true
		} else {
			if state.writer {
				return fmt.Errorf("read overlapped writer for %q", request.Key)
			}
			state.readers++
		}
		active[request.Key] = state
	}
	return nil
}

func observeResourceRelease(active map[string]resourceObservation, requests []ResourceRequest) {
	for _, request := range requests {
		state := active[request.Key]
		if request.Mode == ResourceWrite {
			state.writer = false
		} else {
			state.readers--
		}
		if !state.writer && state.readers == 0 {
			delete(active, request.Key)
			continue
		}
		active[request.Key] = state
	}
}
