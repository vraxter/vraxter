package telemetry

import (
	"context"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

var Tracer trace.Tracer

func init() {
	// Initialize a standard global tracer for Vraxter
	Tracer = otel.Tracer("patagonicrune/vraxter")
}

// StartSpan is a convenience wrapper for creating telemetry spans dynamically
func StartSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return Tracer.Start(ctx, name)
}
