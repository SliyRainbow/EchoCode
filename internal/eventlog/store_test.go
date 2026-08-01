package eventlog

import (
	"testing"
)

func TestAppendAndLoad(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(EventPayload{Role: "user", Content: "hello"}, EventUserMessage); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(EventPayload{Role: "assistant", Content: "hi"}, EventAssistantMessage); err != nil {
		t.Fatal(err)
	}
	cp, err := s.Append(EventPayload{Label: "turn 1", TurnIndex: 1}, EventCheckpoint)
	if err != nil {
		t.Fatal(err)
	}

	// 重新打开，验证持久化与 head 恢复。
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	events := s2.Events()
	if len(events) != 3 {
		t.Fatalf("重载事件数 = %d, want 3", len(events))
	}
	if s2.Head() != cp.ID {
		t.Fatalf("head = %q, want %q", s2.Head(), cp.ID)
	}
	if len(s2.Checkpoints()) != 1 {
		t.Fatalf("checkpoints = %d, want 1", len(s2.Checkpoints()))
	}
}

func TestSnapshotDedup(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h1, err := s.WriteSnapshot("内容A\r\n带BOM")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := s.WriteSnapshot("内容A\r\n带BOM")
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatal("相同内容应产生相同哈希（去重）")
	}
	back, err := s.ReadSnapshot(h1)
	if err != nil {
		t.Fatal(err)
	}
	if back != "内容A\r\n带BOM" {
		t.Fatalf("快照回读不一致: %q", back)
	}
	// 空哈希应返回空串。
	if v, _ := s.ReadSnapshot(""); v != "" {
		t.Fatal("空哈希应返回空内容")
	}
}

func TestRewindChainAndFork(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// turn1: user + assistant + checkpoint
	s.Append(EventPayload{Role: "user", Content: "u1"}, EventUserMessage)
	s.Append(EventPayload{Role: "assistant", Content: "a1"}, EventAssistantMessage)
	cp1, _ := s.Append(EventPayload{Label: "cp1"}, EventCheckpoint)
	// turn2
	s.Append(EventPayload{Role: "user", Content: "u2"}, EventUserMessage)
	s.Append(EventPayload{Role: "assistant", Content: "a2"}, EventAssistantMessage)
	cp2, _ := s.Append(EventPayload{Label: "cp2"}, EventCheckpoint)

	if len(s.Events()) != 6 {
		t.Fatalf("事件数 = %d, want 6", len(s.Events()))
	}

	// 计算 cp2 到 cp1 之间需回退的事件（不含 cp1）。
	undo := s.EventsToUndo(cp1.ID)
	if len(undo) != 3 { // cp2, a2, u2
		t.Fatalf("待回退事件数 = %d, want 3", len(undo))
	}
	if undo[0].ID != cp2.ID {
		t.Fatal("回退顺序应最新在前")
	}

	// Rewind: head 回到 cp1。
	if err := s.SetHead(cp1.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.Events()) != 3 {
		t.Fatalf("回退后当前线事件数 = %d, want 3", len(s.Events()))
	}

	// 分叉：从 cp1 追加新事件，形成新线，旧事件仍在磁盘。
	s.Append(EventPayload{Role: "user", Content: "u2-alt"}, EventUserMessage)
	if len(s.Events()) != 4 {
		t.Fatalf("分叉后当前线事件数 = %d, want 4", len(s.Events()))
	}
	// 当前线不应包含旧的 a2。
	for _, ev := range s.Events() {
		if ev.Payload.Content == "a2" {
			t.Fatal("分叉后当前线不应包含被回退的 a2")
		}
	}
}

func TestEmptyHeadPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(EventPayload{Role: "user", Content: "original"}, EventUserMessage); err != nil {
		t.Fatal(err)
	}
	if err := store.SetHead(""); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Head() != "" {
		t.Fatalf("重开后 head = %q, want empty", reopened.Head())
	}
	if len(reopened.Events()) != 0 {
		t.Fatalf("空 HEAD 不应恢复旧分支，events = %d", len(reopened.Events()))
	}
	alternative, err := reopened.Append(EventPayload{Label: "alternative"}, EventBranchMarker)
	if err != nil {
		t.Fatal(err)
	}
	if alternative.ParentID != "" {
		t.Fatalf("新根分支 parent = %q, want empty", alternative.ParentID)
	}
}
