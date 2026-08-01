package tools

import "context"

type progressSinkKey struct{}

// ProgressSink receives transient tool output. Progress is intentionally kept
// out of Result so high-frequency updates are visible in the UI without being
// fed back into the model or appended to the durable event log.
type ProgressSink func(ResultPart)

// WithProgressSink attaches a transient progress receiver to a tool context.
func WithProgressSink(ctx context.Context, sink ProgressSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, progressSinkKey{}, sink)
}

// EmitProgress publishes a best-effort transient update for a running tool.
func EmitProgress(ctx context.Context, part ResultPart) {
	if ctx == nil {
		return
	}
	if ctx.Err() != nil {
		return
	}
	sink, _ := ctx.Value(progressSinkKey{}).(ProgressSink)
	if sink != nil {
		sink(part)
	}
}
