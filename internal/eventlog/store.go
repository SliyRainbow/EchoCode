package eventlog

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

// Store 是单个会话的文件式事件存储。并发安全。
//
// 核心模型：事件构成一棵以 ParentID 相连的树。
//   - head 指针指向「当前对话线」的最后一个事件。
//   - 当前对话 = 从 head 沿 ParentID 回溯到根，再反转（根→head）。
//   - Rewind = 把 head 移到某个 checkpoint；之后 Append 会以该 checkpoint 为 parent，
//     自然分叉出新线（append-only，不删除任何旧事件）。
type Store struct {
	mu      sync.Mutex
	dir     string
	eventsF string
	snapDir string
	seq     int64
	head    string // 当前对话线的最后事件 ID
	byID    map[string]Event
	order   []string // 按写入顺序的事件 ID（用于持久化 head 之外的遍历）
}

// Open 打开（或创建）会话目录下的事件存储并载入已有事件。
func Open(sessionDir string) (*Store, error) {
	s := &Store{
		dir:     sessionDir,
		eventsF: filepath.Join(sessionDir, "events.jsonl"),
		snapDir: filepath.Join(sessionDir, "snapshots"),
		byID:    make(map[string]Event),
	}
	if err := os.MkdirAll(s.snapDir, 0o755); err != nil {
		return nil, err
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	f, err := os.Open(s.eventsF)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return fmt.Errorf("解析事件失败: %w", err)
		}
		s.byID[ev.ID] = ev
		s.order = append(s.order, ev.ID)
		if ev.Seq > s.seq {
			s.seq = ev.Seq
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	// head 恢复策略：优先读持久化的 HEAD 文件；否则取 seq 最大的事件。
	s.head = s.loadHead()
	return nil
}

// headFile 记录当前 head（Rewind 后需要持久化，重启才能回到回退点）。
func (s *Store) headFilePath() string { return filepath.Join(s.dir, "HEAD") }

func (s *Store) loadHead() string {
	data, err := os.ReadFile(s.headFilePath())
	if err == nil {
		id := string(data)
		// 空 HEAD 是有效状态：表示当前分支位于首条事件之前。
		if id == "" {
			return ""
		}
		if _, ok := s.byID[id]; ok {
			return id
		}
	}
	// 回退：取 seq 最大的事件作为 head。
	var head string
	var maxSeq int64 = -1
	for _, ev := range s.byID {
		if ev.Seq > maxSeq {
			maxSeq = ev.Seq
			head = ev.ID
		}
	}
	return head
}

func (s *Store) persistHead() error {
	return tools.WriteBytesAtomic(s.headFilePath(), []byte(s.head), 0o600)
}

// Append 追加事件，以当前 head 为 parent，写盘并前移 head。
func (s *Store) Append(payload EventPayload, eventType EventType) (Event, error) {
	return s.AppendWithHeader(EventHeader{}, payload, eventType)
}

// AppendWithHeader appends an event with execution metadata. The existing
// Append API remains available for callers that do not yet have a run or tool
// context. Header fields are append-only metadata and do not affect branch
// ordering, parent selection, or sequence allocation.
func (s *Store) AppendWithHeader(header EventHeader, payload EventPayload, eventType EventType) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if header.BranchID == "" {
		header.BranchID = s.branchIDLocked()
	}
	s.seq++
	ev := Event{
		EventHeader: header.WithDefaultSchemaVersion(),
		ID:          fmt.Sprintf("ev-%d-%d", s.seq, time.Now().UnixNano()),
		ParentID:    s.head,
		Seq:         s.seq,
		Type:        eventType,
		TS:          time.Now().UTC(),
		Payload:     payload,
	}
	if eventType == EventBranchMarker || ev.BranchID == "" {
		ev.BranchID = ev.ID
	}
	if err := s.appendRaw(ev); err != nil {
		s.seq--
		return Event{}, err
	}
	s.byID[ev.ID] = ev
	s.order = append(s.order, ev.ID)
	s.head = ev.ID
	_ = s.persistHead()
	return ev, nil
}

// BranchID returns the stable identity of the selected event branch. The
// first event identifies the original branch; every branch_marker starts a new
// branch. Rewind only moves HEAD and therefore restores the corresponding ID.
func (s *Store) BranchID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.branchIDLocked()
}

func (s *Store) branchIDLocked() string {
	current := s.head
	root := ""
	for current != "" {
		event, ok := s.byID[current]
		if !ok {
			break
		}
		root = event.ID
		if event.Type == EventBranchMarker {
			return event.ID
		}
		current = event.ParentID
	}
	return root
}

func (s *Store) appendRaw(ev Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.eventsF, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// WriteSnapshot 内容寻址保存，返回 sha256；已存在则去重。
func (s *Store) WriteSnapshot(content string) (string, error) {
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])
	path := filepath.Join(s.snapDir, hash)
	if _, err := os.Stat(path); err == nil {
		return hash, nil
	}
	if err := tools.WriteBytesAtomic(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return hash, nil
}

// ReadSnapshot 按哈希读回内容。
func (s *Store) ReadSnapshot(hash string) (string, error) {
	if hash == "" {
		return "", nil
	}
	data, err := os.ReadFile(filepath.Join(s.snapDir, hash))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// chainFrom 从给定事件 ID 沿 ParentID 回溯到根，返回根→该事件的顺序切片。
func (s *Store) chainFrom(id string) []Event {
	var rev []Event
	for id != "" {
		ev, ok := s.byID[id]
		if !ok {
			break
		}
		rev = append(rev, ev)
		id = ev.ParentID
	}
	// 反转为根→head。
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// Head 返回当前 head 事件 ID。
func (s *Store) Head() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.head
}

// Event 按 ID 返回事件。
func (s *Store) Event(id string) (Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev, ok := s.byID[id]
	return ev, ok
}

// IsOnCurrentChain 判断事件是否位于当前根到 head 的路径上。
// 空 ID 表示所有分支共有的虚拟根节点。
func (s *Store) IsOnCurrentChain(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return true
	}
	for current := s.head; current != ""; {
		if current == id {
			return true
		}
		ev, ok := s.byID[current]
		if !ok {
			break
		}
		current = ev.ParentID
	}
	return false
}

// Events 返回当前对话线（根→head）的全部事件。
func (s *Store) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chainFrom(s.head)
}

// Checkpoints 返回当前对话线上的 checkpoint 事件（供 Timeline）。
func (s *Store) Checkpoints() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Event
	for _, ev := range s.chainFrom(s.head) {
		if ev.Type == EventCheckpoint {
			out = append(out, ev)
		}
	}
	return out
}

// EventsToUndo 返回当前 head 到目标事件（不含目标）之间、当前线上的事件，
// 按「从 head 往回」的顺序（最新的在前），用于 Rewind 时逆序还原文件。
func (s *Store) EventsToUndo(targetID string) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Event
	for id := s.head; id != "" && id != targetID; {
		ev, ok := s.byID[id]
		if !ok {
			break
		}
		out = append(out, ev)
		id = ev.ParentID
	}
	return out
}

// SetHead 把 head 移到目标事件（Rewind 的对话回退部分），并持久化。
func (s *Store) SetHead(targetID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if targetID != "" {
		if _, ok := s.byID[targetID]; !ok {
			return fmt.Errorf("找不到事件: %s", targetID)
		}
	}
	s.head = targetID
	return s.persistHead()
}
