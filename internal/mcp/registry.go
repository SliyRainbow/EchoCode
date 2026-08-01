package mcp

type Registry struct {
	snapshots []ServerSnapshot
}

func NewRegistry(snapshots []ServerSnapshot) Registry {
	copied := make([]ServerSnapshot, len(snapshots))
	copy(copied, snapshots)
	return Registry{snapshots: copied}
}

func (r Registry) Snapshots() []ServerSnapshot {
	copied := make([]ServerSnapshot, len(r.snapshots))
	copy(copied, r.snapshots)
	return copied
}
