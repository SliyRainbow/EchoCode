package tools

import (
	"context"
	"testing"
)

func TestEmitProgressDropsLateUpdatesAfterCancellation(t *testing.T) {
	updates := 0
	ctx, cancel := context.WithCancel(context.Background())
	ctx = WithProgressSink(ctx, func(ResultPart) { updates++ })
	EmitProgress(ctx, ResultPart{Kind: PartToolCall, Status: "running"})
	cancel()
	EmitProgress(ctx, ResultPart{Kind: PartToolCall, Status: "running"})
	if updates != 1 {
		t.Fatalf("updates = %d, want 1", updates)
	}
}
