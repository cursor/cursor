package copilot

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestGetTraceContextEmpty(t *testing.T) {
	// Without any propagator configured, should return empty strings
	tp, ts := getTraceContext(context.Background())
	if tp != "" || ts != "" {
		t.Errorf("expected empty trace context, got traceparent=%q tracestate=%q", tp, ts)
	}
}

func TestGetTraceContextWithPropagator(t *testing.T) {
	// Set up W3C propagator
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())

	// Inject known trace context
	carrier := propagation.MapCarrier{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}
	ctx := otel.GetTextMapPropagator().Extract(context.Background(), carrier)

	tp, ts := getTraceContext(ctx)
	if tp == "" {
		t.Error("expected non-empty traceparent")
	}
	_ = ts // tracestate may be empty
}

func TestContextWithTraceParentEmpty(t *testing.T) {
	ctx := contextWithTraceParent(context.Background(), "", "")
	if ctx == nil {
		t.Error("expected non-nil context")
	}
}

func TestContextWithTraceParentValid(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())

	ctx := contextWithTraceParent(context.Background(),
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "")

	// Verify the context has trace info by extracting it back
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	if carrier.Get("traceparent") == "" {
		t.Error("expected traceparent to be set in context")
	}
}

func TestToolInvocationTraceContext(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())

	traceparent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	ctx := contextWithTraceParent(context.Background(), traceparent, "")

	inv := ToolInvocation{
		SessionID:    "sess-1",
		ToolCallID:   "call-1",
		ToolName:     "my_tool",
		Arguments:    nil,
		TraceContext: ctx,
	}

	// The TraceContext should carry the remote span context
	sc := trace.SpanContextFromContext(inv.TraceContext)
	if !sc.IsValid() {
		t.Fatal("expected valid span context on ToolInvocation.TraceContext")
	}
	if sc.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("unexpected trace ID: %s", sc.TraceID())
	}
	if sc.SpanID().String() != "00f067aa0ba902b7" {
		t.Errorf("unexpected span ID: %s", sc.SpanID())
	}
}
