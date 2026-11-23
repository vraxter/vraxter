// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
)

type traceIDCtxKey struct{}

// SpanRecord is the serialized JSONL structure emitted by the local zero-egress span exporter.
type SpanRecord struct {
	TraceID      string            `json:"trace_id"`
	SpanID       string            `json:"span_id"`
	ParentSpanID string            `json:"parent_span_id,omitempty"`
	Name         string            `json:"name"`
	StartTimeUTC string            `json:"start_time_utc"`
	EndTimeUTC   string            `json:"end_time_utc"`
	DurationMS   float64           `json:"duration_ms"`
	Status       string            `json:"status,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
}

// Config configures the local OpenTelemetry TracerProvider and enforces airgap zero-egress rules.
type Config struct {
	IsolationMode  string // "airgap", "strict_local", "local_lan", "hybrid"
	JSONLPath      string // Optional local JSONL file for span export
	Writer         io.Writer
	RemoteEndpoint string // Rejected in airgap / strict_local / local_lan
}

// LocalTracerProvider implements go.opentelemetry.io/otel/trace.TracerProvider
// purely in-process with optional local JSONL file / io.Writer output and strictly
// zero network sockets or outbound HTTP connections.
type LocalTracerProvider struct {
	embedded.TracerProvider
	mu                  sync.RWMutex
	isolationMode       string
	jsonlPath           string
	writer              io.Writer
	recordedSpans       []SpanRecord
	maxInMemorySpans    int
	networkEgressCount  atomic.Int64
	blockedEgressTarget string
}

var (
	globalProviderMu sync.RWMutex
	globalProvider   *LocalTracerProvider
	// Tracer is the global OpenTelemetry tracer for Vraxter.
	Tracer trace.Tracer
)

func init() {
	p := NewLocalTracerProvider(Config{IsolationMode: "airgap"})
	globalProvider = p
	otel.SetTracerProvider(p)
	Tracer = p.Tracer("vraxter/vraxter")
}

// IsRestrictedIsolation returns true if outbound telemetry network egress is forbidden.
func IsRestrictedIsolation(mode string) bool {
	m := strings.ToLower(strings.TrimSpace(mode))
	return m == "" || m == "airgap" || m == "strict_local" || m == "local_lan"
}

// NewLocalTracerProvider creates a local zero-egress OpenTelemetry TracerProvider.
func NewLocalTracerProvider(cfg Config) *LocalTracerProvider {
	mode := strings.ToLower(strings.TrimSpace(cfg.IsolationMode))
	if mode == "" {
		mode = "airgap"
	}
	p := &LocalTracerProvider{
		isolationMode:    mode,
		jsonlPath:        strings.TrimSpace(cfg.JSONLPath),
		writer:           cfg.Writer,
		maxInMemorySpans: 512,
	}
	if strings.TrimSpace(cfg.RemoteEndpoint) != "" && IsRestrictedIsolation(mode) {
		p.blockedEgressTarget = strings.TrimSpace(cfg.RemoteEndpoint)
	}
	return p
}

// Configure installs and configures the process-wide LocalTracerProvider.
// If a RemoteEndpoint is requested while IsolationMode is airgap, strict_local, or local_lan,
// Configure fails closed and keeps zero-egress local tracing active.
func Configure(cfg Config) (*LocalTracerProvider, error) {
	mode := strings.ToLower(strings.TrimSpace(cfg.IsolationMode))
	if mode == "" {
		mode = "airgap"
	}
	p := NewLocalTracerProvider(cfg)

	globalProviderMu.Lock()
	globalProvider = p
	otel.SetTracerProvider(p)
	Tracer = p.Tracer("vraxter/vraxter")
	globalProviderMu.Unlock()

	if strings.TrimSpace(cfg.RemoteEndpoint) != "" && IsRestrictedIsolation(mode) {
		return p, fmt.Errorf("VRAXTER SHIELD: remote OTel exporter %q blocked in %s isolation mode (zero network egress enforced)", cfg.RemoteEndpoint, mode)
	}
	return p, nil
}

// Provider returns the active LocalTracerProvider.
func Provider() *LocalTracerProvider {
	globalProviderMu.RLock()
	defer globalProviderMu.RUnlock()
	return globalProvider
}

// NetworkEgressCount returns the number of outbound network telemetry requests made (always 0).
func (p *LocalTracerProvider) NetworkEgressCount() int64 {
	return p.networkEgressCount.Load()
}

// BlockedEgressTarget returns the remote endpoint that was blocked by airgap/local_lan policy, if any.
func (p *LocalTracerProvider) BlockedEgressTarget() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.blockedEgressTarget
}

// RecordedSpans returns a copy of recent spans buffered in memory.
func (p *LocalTracerProvider) RecordedSpans() []SpanRecord {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]SpanRecord, len(p.recordedSpans))
	copy(out, p.recordedSpans)
	return out
}

// Tracer implements trace.TracerProvider.
func (p *LocalTracerProvider) Tracer(name string, _ ...trace.TracerOption) trace.Tracer {
	return &localTracer{
		provider: p,
		name:     name,
	}
}

func (p *LocalTracerProvider) exportSpan(rec SpanRecord) {
	p.mu.Lock()
	if p.maxInMemorySpans > 0 {
		if len(p.recordedSpans) >= p.maxInMemorySpans {
			p.recordedSpans = append(p.recordedSpans[1:], rec)
		} else {
			p.recordedSpans = append(p.recordedSpans, rec)
		}
	}
	jsonlPath := p.jsonlPath
	writer := p.writer
	p.mu.Unlock()

	if jsonlPath == "" && writer == nil {
		return
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rec); err != nil {
		return
	}

	if writer != nil {
		_, _ = writer.Write(buf.Bytes())
	}
	if jsonlPath != "" {
		_ = os.MkdirAll(filepath.Dir(jsonlPath), 0700)
		if f, err := os.OpenFile(jsonlPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); err == nil {
			_, _ = f.Write(buf.Bytes())
			_ = f.Sync()
			_ = f.Close()
		}
	}
}

type localTracer struct {
	embedded.Tracer
	provider *LocalTracerProvider
	name     string
}

func (t *localTracer) Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if ctx == nil {
		ctx = context.Background()
	}

	cfg := trace.NewSpanStartConfig(opts...)
	parentSC := trace.SpanContextFromContext(ctx)

	var traceID trace.TraceID
	var parentSpanID string

	if !cfg.NewRoot() && parentSC.IsValid() {
		traceID = parentSC.TraceID()
		parentSpanID = parentSC.SpanID().String()
	} else if explicitHex := TraceIDFromContext(ctx); len(explicitHex) == 32 {
		if decoded, err := hex.DecodeString(explicitHex); err == nil && len(decoded) == 16 {
			copy(traceID[:], decoded)
		}
	}
	if !traceID.IsValid() {
		traceID = newRandomTraceID()
	}

	spanID := newRandomSpanID()
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})

	startTime := cfg.Timestamp()
	if startTime.IsZero() {
		startTime = time.Now().UTC()
	} else {
		startTime = startTime.UTC()
	}

	attrs := make(map[string]string)
	for _, kv := range cfg.Attributes() {
		attrs[string(kv.Key)] = kv.Value.Emit()
	}

	sp := &localSpan{
		provider:     t.provider,
		spanContext:  sc,
		parentSpanID: parentSpanID,
		name:         spanName,
		startTime:    startTime,
		attrs:        attrs,
	}

	ctx = trace.ContextWithSpan(ctx, sp)
	ctx = WithTraceID(ctx, traceID.String())
	return ctx, sp
}

type localSpan struct {
	embedded.Span
	mu           sync.Mutex
	provider     *LocalTracerProvider
	spanContext  trace.SpanContext
	parentSpanID string
	name         string
	startTime    time.Time
	endTime      time.Time
	ended        bool
	status       string
	attrs        map[string]string
}

func (s *localSpan) End(options ...trace.SpanEndOption) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	endCfg := trace.NewSpanEndConfig(options...)
	if !endCfg.Timestamp().IsZero() {
		s.endTime = endCfg.Timestamp().UTC()
	} else {
		s.endTime = time.Now().UTC()
	}
	attrsCopy := make(map[string]string, len(s.attrs))
	for k, v := range s.attrs {
		attrsCopy[k] = v
	}
	rec := SpanRecord{
		TraceID:      s.spanContext.TraceID().String(),
		SpanID:       s.spanContext.SpanID().String(),
		ParentSpanID: s.parentSpanID,
		Name:         s.name,
		StartTimeUTC: s.startTime.Format(time.RFC3339Nano),
		EndTimeUTC:   s.endTime.Format(time.RFC3339Nano),
		DurationMS:   float64(s.endTime.Sub(s.startTime).Microseconds()) / 1000.0,
		Status:       s.status,
		Attributes:   attrsCopy,
	}
	s.mu.Unlock()

	if s.provider != nil {
		s.provider.exportSpan(rec)
	}
}

func (s *localSpan) AddEvent(_ string, _ ...trace.EventOption) {}
func (s *localSpan) AddLink(_ trace.Link)                      {}

func (s *localSpan) IsRecording() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.ended
}

func (s *localSpan) RecordError(err error, _ ...trace.EventOption) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.status = "ERROR: " + err.Error()
	s.attrs["error"] = err.Error()
}

func (s *localSpan) SpanContext() trace.SpanContext {
	return s.spanContext
}

func (s *localSpan) SetStatus(code codes.Code, description string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	if description != "" {
		s.status = code.String() + ": " + description
	} else {
		s.status = code.String()
	}
}

func (s *localSpan) SetName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ended {
		s.name = name
	}
}

func (s *localSpan) SetAttributes(kv ...attribute.KeyValue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	for _, attr := range kv {
		s.attrs[string(attr.Key)] = attr.Value.Emit()
	}
}

func (s *localSpan) TracerProvider() trace.TracerProvider {
	return s.provider
}

func newRandomTraceID() trace.TraceID {
	var tid trace.TraceID
	_, _ = rand.Read(tid[:])
	if !tid.IsValid() {
		tid[0] = 1
	}
	return tid
}

func newRandomSpanID() trace.SpanID {
	var sid trace.SpanID
	_, _ = rand.Read(sid[:])
	if !sid.IsValid() {
		sid[0] = 1
	}
	return sid
}

// WithTraceID attaches a deterministic or propagated trace_id to ctx.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	cleaned := strings.ToLower(strings.TrimSpace(traceID))
	if cleaned == "" {
		return ctx
	}
	return context.WithValue(ctx, traceIDCtxKey{}, cleaned)
}

// EnsureTraceID returns a context guaranteed to carry a valid trace_id and active span,
// creating a new root span if none exists yet.
func EnsureTraceID(ctx context.Context, spanName string) (context.Context, string, trace.Span) {
	if existing := TraceIDFromContext(ctx); existing != "" {
		ctx, sp := StartSpan(ctx, spanName)
		return ctx, TraceIDFromContext(ctx), sp
	}
	ctx, sp := StartSpan(ctx, spanName)
	return ctx, TraceIDFromContext(ctx), sp
}

// TraceIDFromContext extracts the active 32-hex-char trace_id from an OpenTelemetry span
// or explicit context value.
func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() && sc.TraceID().IsValid() {
		return sc.TraceID().String()
	}
	if v, ok := ctx.Value(traceIDCtxKey{}).(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return ""
}

// StartSpan starts a child or root span using Vraxter's active local zero-egress TracerProvider.
func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	globalProviderMu.RLock()
	tr := Tracer
	globalProviderMu.RUnlock()
	return tr.Start(ctx, name, opts...)
}
