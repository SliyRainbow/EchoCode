package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ResourceMode describes the access a caller needs for a resource.
// Resource keys are intentionally opaque to the coordinator: tool adapters
// should canonicalize paths, URLs, or other identifiers before requesting one.
type ResourceMode string

const (
	ResourceRead  ResourceMode = "read"
	ResourceWrite ResourceMode = "write"
)

var (
	ErrNilResourceCoordinator = errors.New("resource coordinator is nil")
	ErrNilResourceContext     = errors.New("resource lock context must not be nil")
	ErrNoResourceRequests     = errors.New("resource lock requires at least one resource")
	ErrEmptyResourceKey       = errors.New("resource lock key must not be empty")
	ErrInvalidResourceMode    = errors.New("resource lock mode must be read or write")
)

// ResourceRequest declares one resource needed by an operation. Repeated keys
// are coalesced; a write request wins over a read request for the same key.
type ResourceRequest struct {
	Key  string
	Mode ResourceMode
}

// ResourceCoordinator grants cancellable read/write leases over arbitrary
// resource keys. Its zero value is ready to use. A Service can therefore own
// one coordinator without coupling construction to Service fields.
//
// Every acquisition is atomic across its full resource set. Requests are
// canonicalized into stable key order before scheduling, so callers that name
// the same resources in different orders cannot deadlock each other.
type ResourceCoordinator struct {
	mu        sync.Mutex
	resources map[string]*resourceLockState
	waiters   []*resourceWaiter
}

type resourceLockState struct {
	readers int
	writer  bool
}

type resourceWaiter struct {
	requests []ResourceRequest
	ready    chan struct{}
	granted  bool
}

// ResourceLease owns an acquired resource set until Release is called.
// Release is idempotent and safe to call from cleanup paths.
type ResourceLease struct {
	coordinator *ResourceCoordinator
	requests    []ResourceRequest
	once        sync.Once
}

// NewResourceCoordinator constructs a coordinator suitable for reuse by a
// Service, worker, team, or subagent. The returned coordinator has no hidden
// dependence on Service state or tool arguments.
func NewResourceCoordinator() *ResourceCoordinator {
	return &ResourceCoordinator{}
}

// Acquire waits until all requested resources can be acquired. Reads may share
// a key; a write excludes every other read or write for that key. Different
// keys can run concurrently. Context cancellation or deadline expiry removes
// a waiting request, and a cancellation race after a grant releases it before
// this method returns an error.
func (c *ResourceCoordinator) Acquire(ctx context.Context, requests ...ResourceRequest) (*ResourceLease, error) {
	if c == nil {
		return nil, ErrNilResourceCoordinator
	}
	if ctx == nil {
		return nil, ErrNilResourceContext
	}

	normalized, err := normalizeResourceRequests(requests)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	waiter := &resourceWaiter{
		requests: normalized,
		ready:    make(chan struct{}),
	}

	c.mu.Lock()
	c.ensureResourcesLocked()
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	c.waiters = append(c.waiters, waiter)
	c.scheduleLocked()
	c.mu.Unlock()

	select {
	case <-waiter.ready:
		// If cancellation won the race with a grant, return the resources here.
		// Once a lease has been returned, normal caller cleanup owns release.
		if err := ctx.Err(); err != nil {
			c.mu.Lock()
			if waiter.granted {
				c.releaseLocked(waiter.requests)
			}
			c.mu.Unlock()
			return nil, err
		}
		return c.newLease(normalized), nil
	case <-ctx.Done():
		c.mu.Lock()
		if waiter.granted {
			// The scheduler granted the set immediately before cancellation. It
			// has no caller-owned lease yet, so release it ourselves.
			c.releaseLocked(waiter.requests)
		} else if c.removeWaiterLocked(waiter) {
			c.scheduleLocked()
		}
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// AcquireAll is the slice-friendly form of Acquire.
func (c *ResourceCoordinator) AcquireAll(ctx context.Context, requests []ResourceRequest) (*ResourceLease, error) {
	return c.Acquire(ctx, requests...)
}

// Requests returns a copy of the canonical resource set held by this lease.
func (l *ResourceLease) Requests() []ResourceRequest {
	if l == nil {
		return nil
	}
	return append([]ResourceRequest(nil), l.requests...)
}

// Release returns the lease's resources and wakes compatible waiters.
func (l *ResourceLease) Release() {
	if l == nil || l.coordinator == nil {
		return
	}
	l.once.Do(func() {
		l.coordinator.release(l.requests)
	})
}

func (c *ResourceCoordinator) newLease(requests []ResourceRequest) *ResourceLease {
	return &ResourceLease{
		coordinator: c,
		requests:    append([]ResourceRequest(nil), requests...),
	}
}

func normalizeResourceRequests(requests []ResourceRequest) ([]ResourceRequest, error) {
	if len(requests) == 0 {
		return nil, ErrNoResourceRequests
	}

	coalesced := make(map[string]ResourceMode, len(requests))
	for _, request := range requests {
		key := strings.TrimSpace(request.Key)
		if key == "" {
			return nil, ErrEmptyResourceKey
		}
		if request.Mode != ResourceRead && request.Mode != ResourceWrite {
			return nil, fmt.Errorf("%w: %q", ErrInvalidResourceMode, request.Mode)
		}
		if previous, found := coalesced[key]; !found || previous != ResourceWrite {
			coalesced[key] = request.Mode
		}
	}

	normalized := make([]ResourceRequest, 0, len(coalesced))
	for key, mode := range coalesced {
		normalized = append(normalized, ResourceRequest{Key: key, Mode: mode})
	}
	sort.SliceStable(normalized, func(left, right int) bool {
		return normalized[left].Key < normalized[right].Key
	})
	return normalized, nil
}

func (c *ResourceCoordinator) ensureResourcesLocked() {
	if c.resources == nil {
		c.resources = make(map[string]*resourceLockState)
	}
}

func (c *ResourceCoordinator) scheduleLocked() {
	if len(c.waiters) == 0 {
		return
	}

	remaining := make([]*resourceWaiter, 0, len(c.waiters))
	blockedKeys := make(map[string]struct{})
	for _, waiter := range c.waiters {
		if !requestsIntersectKeys(waiter.requests, blockedKeys) && c.canGrantLocked(waiter.requests) {
			c.grantLocked(waiter.requests)
			waiter.granted = true
			close(waiter.ready)
			continue
		}

		// A later waiter cannot bypass an earlier incompatible waiter. This
		// prevents a steady stream of readers from starving a queued writer,
		// while unrelated resource keys continue through the same pass.
		remaining = append(remaining, waiter)
		for _, request := range waiter.requests {
			blockedKeys[request.Key] = struct{}{}
		}
	}
	c.waiters = remaining
}

func requestsIntersectKeys(requests []ResourceRequest, keys map[string]struct{}) bool {
	for _, request := range requests {
		for key := range keys {
			if resourceKeysConflict(request.Key, key) {
				return true
			}
		}
	}
	return false
}

func (c *ResourceCoordinator) canGrantLocked(requests []ResourceRequest) bool {
	for _, request := range requests {
		for key, state := range c.resources {
			if !resourceKeysConflict(request.Key, key) {
				continue
			}
			if request.Mode == ResourceWrite {
				if state.writer || state.readers != 0 {
					return false
				}
				continue
			}
			if state.writer {
				return false
			}
		}
	}
	return true
}

func (c *ResourceCoordinator) grantLocked(requests []ResourceRequest) {
	c.ensureResourcesLocked()
	for _, request := range requests {
		state := c.resources[request.Key]
		if state == nil {
			state = &resourceLockState{}
			c.resources[request.Key] = state
		}
		if request.Mode == ResourceWrite {
			state.writer = true
		} else {
			state.readers++
		}
	}
}

func (c *ResourceCoordinator) release(requests []ResourceRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.releaseLocked(requests)
}

func (c *ResourceCoordinator) releaseLocked(requests []ResourceRequest) {
	for _, request := range requests {
		for key, state := range c.resources {
			if key != request.Key {
				continue
			}
			if request.Mode == ResourceWrite {
				state.writer = false
			} else if state.readers > 0 {
				state.readers--
			}
			if !state.writer && state.readers == 0 {
				delete(c.resources, key)
			}
		}
	}
	c.scheduleLocked()
}

// resourceKeysConflict understands the small set of hierarchical keys used
// by tool adapters. Exact opaque keys remain exact-match resources, while a
// directory or workspace key also protects its descendants. This prevents a
// directory scan from racing a child file write without serializing unrelated
// files in the same workspace.
func resourceKeysConflict(left, right string) bool {
	if left == right {
		return true
	}
	leftKind, leftPath, leftPathKey := resourcePathKey(left)
	rightKind, rightPath, rightPathKey := resourcePathKey(right)
	if !leftPathKey || !rightPathKey {
		return false
	}
	if leftKind == "file" && rightKind == "file" {
		return false
	}
	if leftKind != "file" && rightKind != "file" {
		return resourcePathContains(leftPath, rightPath) || resourcePathContains(rightPath, leftPath)
	}
	if leftKind == "file" {
		return resourcePathContains(rightPath, leftPath)
	}
	return resourcePathContains(leftPath, rightPath)
}

func resourcePathKey(key string) (kind, value string, ok bool) {
	separator := strings.IndexByte(key, ':')
	if separator <= 0 || separator == len(key)-1 {
		return "", "", false
	}
	kind = key[:separator]
	switch kind {
	case "file", "dir", "workspace":
		return kind, key[separator+1:], true
	default:
		return "", "", false
	}
}

func resourcePathContains(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return true
	}
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != "."
}

func (c *ResourceCoordinator) removeWaiterLocked(target *resourceWaiter) bool {
	for index, waiter := range c.waiters {
		if waiter != target {
			continue
		}
		copy(c.waiters[index:], c.waiters[index+1:])
		c.waiters[len(c.waiters)-1] = nil
		c.waiters = c.waiters[:len(c.waiters)-1]
		return true
	}
	return false
}
