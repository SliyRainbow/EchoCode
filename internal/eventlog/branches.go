package eventlog

// 分支模型：一条「对话线」= 某个叶子事件（无子节点）到根的路径。
// Rewind 后继续对话会产生新叶子，旧叶子仍保留 → 叶子集合即分支集合。

// Chain 返回从根到指定事件的事件序列（导出版 chainFrom）。
func (s *Store) Chain(id string) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chainFrom(id)
}

// Leaves 返回所有叶子事件（不是任何事件 parent 的事件），按 seq 升序。
func (s *Store) Leaves() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	isParent := make(map[string]bool, len(s.byID))
	for _, ev := range s.byID {
		if ev.ParentID != "" {
			isParent[ev.ParentID] = true
		}
	}
	var leaves []Event
	for _, id := range s.order {
		ev := s.byID[id]
		if !isParent[ev.ID] {
			leaves = append(leaves, ev)
		}
	}
	return leaves
}

// CommonAncestor 返回两个事件在树中最深的公共祖先事件 ID（不存在返回空）。
func (s *Store) CommonAncestor(a, b string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	ancestorsOfA := make(map[string]bool)
	for id := a; id != ""; {
		ev, ok := s.byID[id]
		if !ok {
			break
		}
		ancestorsOfA[id] = true
		id = ev.ParentID
	}
	for id := b; id != ""; {
		if ancestorsOfA[id] {
			return id
		}
		ev, ok := s.byID[id]
		if !ok {
			break
		}
		id = ev.ParentID
	}
	return ""
}

// LastCheckpointOnChain 返回从根到 id 路径上最后一个 checkpoint（用于分支标签）。
func (s *Store) LastCheckpointOnChain(id string) (Event, bool) {
	chain := s.Chain(id)
	for i := len(chain) - 1; i >= 0; i-- {
		if chain[i].Type == EventCheckpoint {
			return chain[i], true
		}
	}
	return Event{}, false
}

// FileSnapshotsBetween 返回沿当前分支从 fromExclusive（不含）到 toInclusive（含）之间的
// file_snapshot 事件，按 seq 升序（用于「应用」目标分支的文件改动）。
// fromExclusive 为空表示从根开始。
func (s *Store) FileSnapshotsBetween(fromExclusive, toInclusive string) []Event {
	chain := s.Chain(toInclusive)
	var out []Event
	started := fromExclusive == ""
	for _, ev := range chain {
		if !started {
			if ev.ID == fromExclusive {
				started = true
			}
			continue
		}
		if ev.Type == EventFileSnapshot {
			out = append(out, ev)
		}
	}
	return out
}
