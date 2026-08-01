package terminal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/pathutil"
	"github.com/SliyRainbow/EchoCode/internal/sandboxexec"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const (
	maxSessionOutput = 1024 * 1024
	maxSessions      = 8
)

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*session
	starting int
	notify   func(SessionState)
}

type SessionState struct {
	ID                  string `json:"id"`
	Shell               string `json:"shell"`
	Workdir             string `json:"workdir"`
	Running             bool   `json:"running"`
	StartedAt           string `json:"startedAt"`
	ExitCode            int    `json:"exitCode"`
	Error               string `json:"error,omitempty"`
	Output              string `json:"output"`
	Sandboxed           bool   `json:"sandboxed"`
	SandboxBackend      string `json:"sandboxBackend,omitempty"`
	PrivilegeRestricted bool   `json:"privilegeRestricted"`
}

type session struct {
	mu                  sync.Mutex
	id                  string
	shell               string
	workdir             string
	startedAt           string
	cmd                 *exec.Cmd
	process             *sandboxexec.Process
	stdin               io.WriteCloser
	cancel              context.CancelFunc
	running             bool
	exitCode            int
	err                 string
	output              []byte
	sandboxed           bool
	sandboxBackend      string
	privilegeRestricted bool
	notify              func(SessionState)
	pending             bool
	done                chan struct{}
}

type sessionWriter struct{ session *session }

func NewManager() *Manager {
	return &Manager{sessions: map[string]*session{}}
}

// SetNotify receives throttled session snapshots whenever output or lifecycle state changes.
func (m *Manager) SetNotify(notify func(SessionState)) {
	m.mu.Lock()
	m.notify = notify
	sessions := make([]*session, 0, len(m.sessions))
	for _, item := range m.sessions {
		sessions = append(sessions, item)
	}
	m.mu.Unlock()
	for _, item := range sessions {
		item.mu.Lock()
		item.notify = notify
		item.mu.Unlock()
	}
}

func (m *Manager) Start(workdir string) (SessionState, error) {
	return m.start(workdir, false, sandboxexec.Limits{})
}

func (m *Manager) StartWithLimits(workdir string, limits sandboxexec.Limits) (SessionState, error) {
	return m.start(workdir, false, limits)
}

func (m *Manager) StartRestricted(workdir string, limits sandboxexec.Limits) (SessionState, error) {
	return m.start(workdir, true, limits)
}

func (m *Manager) start(workdir string, restricted bool, limits sandboxexec.Limits) (SessionState, error) {
	workdir = strings.TrimSpace(workdir)
	if workdir == "" {
		return SessionState{}, errors.New("terminal workdir is required")
	}
	abs, err := pathutil.Canonical(workdir)
	if err != nil {
		return SessionState{}, err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return SessionState{}, fmt.Errorf("terminal workdir is unavailable: %s", abs)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd, shell := shellCommand(ctx)
	cmd.Dir = abs
	cmd.Env = os.Environ()
	if restricted {
		cmd.Env = tools.SafeCommandEnvironment()
	}
	configureProcess(cmd)

	s := &session{
		id:        sessionID(),
		shell:     shell,
		workdir:   abs,
		startedAt: time.Now().Format(time.RFC3339),
		cmd:       cmd,
		cancel:    cancel,
		running:   true,
		exitCode:  -1,
		done:      make(chan struct{}),
	}
	m.mu.Lock()
	s.notify = m.notify
	m.mu.Unlock()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return SessionState{}, err
	}
	s.stdin = stdin
	writer := sessionWriter{session: s}
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err := m.reserveStart(); err != nil {
		cancel()
		_ = stdin.Close()
		return SessionState{}, err
	}
	process, err := sandboxexec.Start(cmd, limits)
	if err != nil {
		m.releaseStart()
		cancel()
		_ = stdin.Close()
		return SessionState{}, fmt.Errorf("start terminal: %w", err)
	}
	capabilities := sandboxexec.DetectCapabilities()
	s.process = process
	s.sandboxed = capabilities.ProcessTree
	s.sandboxBackend = capabilities.Backend
	s.privilegeRestricted = limits.RestrictPrivileges && capabilities.PrivilegeIsolation

	m.mu.Lock()
	m.starting--
	m.sessions[s.id] = s
	m.pruneLocked()
	m.mu.Unlock()
	go s.wait()
	state := s.state()
	s.emit(state)
	return state, nil
}

func (m *Manager) State(id string) (SessionState, error) {
	s, err := m.get(id)
	if err != nil {
		return SessionState{}, err
	}
	return s.state(), nil
}

func (m *Manager) WriteLine(id, command string) error {
	command = strings.TrimRight(command, "\r\n")
	if strings.TrimSpace(command) == "" {
		return nil
	}
	s, err := m.get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running || s.stdin == nil {
		return errors.New("terminal session is not running")
	}
	_, err = io.WriteString(s.stdin, command+lineEnding())
	return err
}

func (m *Manager) Stop(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	cancel := s.cancel
	stdin := s.stdin
	process := s.process
	done := s.done
	s.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	var stopErr error
	if process != nil {
		stopErr = process.Terminate()
	}
	if cancel != nil {
		cancel()
	}
	if done == nil {
		return stopErr
	}
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		if stopErr != nil {
			return fmt.Errorf("stop terminal process tree: %w", stopErr)
		}
		return errors.New("terminal process did not stop within 5 seconds")
	}
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		_ = m.Stop(id)
	}
}

func (m *Manager) Close() { m.StopAll() }

func (m *Manager) get(id string) (*session, error) {
	m.mu.Lock()
	s := m.sessions[strings.TrimSpace(id)]
	m.mu.Unlock()
	if s == nil {
		return nil, errors.New("terminal session was not found")
	}
	return s, nil
}

func (m *Manager) pruneLocked() {
	m.pruneToLocked(maxSessions)
}

func (m *Manager) pruneToLocked(limit int) {
	if limit < 0 {
		limit = 0
	}
	if len(m.sessions) <= limit {
		return
	}
	type candidate struct {
		id      string
		started string
		running bool
	}
	candidates := make([]candidate, 0, len(m.sessions))
	for id, s := range m.sessions {
		s.mu.Lock()
		candidates = append(candidates, candidate{id: id, started: s.startedAt, running: s.running})
		s.mu.Unlock()
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].started < candidates[j].started })
	for _, candidate := range candidates {
		if len(m.sessions) <= limit {
			break
		}
		if !candidate.running {
			delete(m.sessions, candidate.id)
		}
	}
}

func (m *Manager) reserveStart() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneToLocked(maxSessions - m.starting - 1)
	if len(m.sessions)+m.starting >= maxSessions {
		return fmt.Errorf("terminal session limit reached (%d); stop a session before starting another", maxSessions)
	}
	m.starting++
	return nil
}

func (m *Manager) releaseStart() {
	m.mu.Lock()
	if m.starting > 0 {
		m.starting--
	}
	m.mu.Unlock()
}

func (s *session) wait() {
	var err error
	if s.process != nil {
		err = s.process.Wait()
	} else {
		err = s.cmd.Wait()
	}
	s.mu.Lock()
	s.running = false
	s.exitCode = 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			s.exitCode = exitErr.ExitCode()
		} else if !errors.Is(err, context.Canceled) {
			s.exitCode = -1
			s.err = err.Error()
		}
	}
	if s.stdin != nil {
		_ = s.stdin.Close()
		s.stdin = nil
	}
	state := s.stateLocked()
	notify := s.notify
	done := s.done
	s.mu.Unlock()
	if done != nil {
		close(done)
	}
	if notify != nil {
		notify(state)
	}
}

func (s *session) state() SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateLocked()
}

func (s *session) stateLocked() SessionState {
	return SessionState{
		ID:                  s.id,
		Shell:               s.shell,
		Workdir:             s.workdir,
		Running:             s.running,
		StartedAt:           s.startedAt,
		ExitCode:            s.exitCode,
		Error:               s.err,
		Output:              tools.DecodeCommandOutput(append([]byte(nil), s.output...)),
		Sandboxed:           s.sandboxed,
		SandboxBackend:      s.sandboxBackend,
		PrivilegeRestricted: s.privilegeRestricted,
	}
}

func (w sessionWriter) Write(p []byte) (int, error) {
	w.session.mu.Lock()
	w.session.output = append(w.session.output, p...)
	if len(w.session.output) > maxSessionOutput {
		start := len(w.session.output) - maxSessionOutput
		trimmed := append([]byte("[earlier terminal output truncated]\n"), w.session.output[start:]...)
		w.session.output = trimmed
	}
	w.session.queueNotifyLocked()
	w.session.mu.Unlock()
	return len(p), nil
}

func (s *session) queueNotifyLocked() {
	if s.notify == nil || s.pending {
		return
	}
	s.pending = true
	time.AfterFunc(45*time.Millisecond, func() {
		s.mu.Lock()
		s.pending = false
		state := s.stateLocked()
		notify := s.notify
		s.mu.Unlock()
		if notify != nil {
			notify(state)
		}
	})
}

func (s *session) emit(state SessionState) {
	s.mu.Lock()
	notify := s.notify
	s.mu.Unlock()
	if notify != nil {
		notify(state)
	}
}

func sessionID() string {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err == nil {
		return hex.EncodeToString(value)
	}
	return fmt.Sprintf("%x", time.Now().UnixNano())
}
