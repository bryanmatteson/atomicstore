package store

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestMetricsCollector provides a test implementation of MetricsCollector
type TestMetricsCollector struct {
	durations     map[string]time.Duration
	sizes         map[string]int64
	counts        map[string]int64
	errors        map[string]error
	mu            sync.RWMutex
	recordedCalls int
}

func NewTestMetricsCollector() *TestMetricsCollector {
	return &TestMetricsCollector{
		durations: make(map[string]time.Duration),
		sizes:     make(map[string]int64),
		counts:    make(map[string]int64),
		errors:    make(map[string]error),
	}
}

func (m *TestMetricsCollector) RecordDuration(name string, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.durations[name] = duration
	m.recordedCalls++
}

func (m *TestMetricsCollector) RecordSize(name string, size int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sizes[name] = size
	m.recordedCalls++
}

func (m *TestMetricsCollector) RecordCount(name string, count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[name] = count
	m.recordedCalls++
}

func (m *TestMetricsCollector) RecordError(name string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors[name] = err
	m.recordedCalls++
}

func (m *TestMetricsCollector) GetRecordedCalls() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.recordedCalls
}

func (m *TestMetricsCollector) GetDuration(name string) (time.Duration, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	duration, ok := m.durations[name]
	return duration, ok
}

func (m *TestMetricsCollector) GetSize(name string) (int64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	size, ok := m.sizes[name]
	return size, ok
}

// Test implementation of TracingProvider
type TestTracingProvider struct {
	spans     map[string]*TestSpan
	mu        sync.RWMutex
	spanCount int
}

type TestSpan struct {
	name  string
	tags  map[string]string
	ended bool
}

func NewTestTracingProvider() *TestTracingProvider {
	return &TestTracingProvider{
		spans: make(map[string]*TestSpan),
	}
}

func (t *TestTracingProvider) StartSpan(ctx context.Context, name string) Span {
	t.mu.Lock()
	defer t.mu.Unlock()
	span := &TestSpan{
		name: name,
		tags: make(map[string]string),
	}
	t.spans[name] = span
	t.spanCount++
	return span
}

func (t *TestTracingProvider) GetSpanCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.spanCount
}

func (t *TestTracingProvider) GetSpan(name string) (*TestSpan, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	span, ok := t.spans[name]
	return span, ok
}

func (s *TestSpan) End() {
	s.ended = true
}

func (s *TestSpan) AddTag(key, value string) {
	s.tags[key] = value
}

func (s *TestSpan) IsEnded() bool {
	return s.ended
}

func (s *TestSpan) GetTag(key string) (string, bool) {
	value, ok := s.tags[key]
	return value, ok
}

// Test implementation of Logger
type TestLogger struct {
	messages []logMessage
	mu       sync.RWMutex
}

type logMessage struct {
	level  LogLevel
	msg    string
	fields map[string]any
}

func NewTestLogger() *TestLogger {
	return &TestLogger{
		messages: make([]logMessage, 0),
	}
}

func (l *TestLogger) Log(level LogLevel, msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, logMessage{
		level:  level,
		msg:    msg,
		fields: fields,
	})
}

func (l *TestLogger) GetMessages() []logMessage {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]logMessage{}, l.messages...)
}

func TestObservabilityOptions(t *testing.T) {
	t.Run("Basic Options", func(t *testing.T) {
		metrics := NewTestMetricsCollector()
		tracer := NewTestTracingProvider()
		logger := NewTestLogger()

		opts := &ObservabilityOptions{}
		WithMetrics(metrics).applyObservability(opts)
		WithTracer(tracer).applyObservability(opts)
		WithLogger(logger).applyObservability(opts)

		if opts.Metrics.Get() != metrics {
			t.Error("WithMetrics didn't set metrics correctly")
		}

		if opts.Tracer.Get() != tracer {
			t.Error("WithTracer didn't set tracer correctly")
		}

		if opts.Logger.Get() != logger {
			t.Error("WithLogger didn't set logger correctly")
		}
	})

	t.Run("Apply Multiple Options", func(t *testing.T) {
		metrics := NewTestMetricsCollector()
		tracer := NewTestTracingProvider()
		logger := NewTestLogger()

		opts := &ObservabilityOptions{}

		// Test applying multiple options at once
		applyObservabilityOptions(opts, []ObservabilityOption{
			WithMetrics(metrics),
			WithTracer(tracer),
			WithLogger(logger),
		})

		if opts.Metrics.Get() != metrics {
			t.Error("applyObservabilityOptions didn't set metrics correctly")
		}

		if opts.Tracer.Get() != tracer {
			t.Error("applyObservabilityOptions didn't set tracer correctly")
		}

		if opts.Logger.Get() != logger {
			t.Error("applyObservabilityOptions didn't set logger correctly")
		}
	})
}

func TestNoOpImplementations(t *testing.T) {
	t.Run("NoOpMetrics", func(t *testing.T) {
		metrics := NewNoOpMetrics()

		// These should all succeed without panicking
		metrics.RecordDuration("test", time.Second)
		metrics.RecordSize("test", 100)
		metrics.RecordCount("test", 5)
		metrics.RecordError("test", nil)
	})

	t.Run("NoOpTracer", func(t *testing.T) {
		tracer := NewNoOpTracer()
		ctx := context.Background()

		span := tracer.StartSpan(ctx, "test-span")
		if span == nil {
			t.Error("NoOpTracer.StartSpan returned nil")
		}

		// Should not panic
		span.End()
		span.AddTag("key", "value")
	})

	t.Run("NoOpLogger", func(t *testing.T) {
		logger := NewNoOpLogger()

		// Should not panic
		logger.Log(LogLevelInfo, "test message", map[string]any{
			"key": "value",
		})
	})
}

func TestObservabilityDefaultValues(t *testing.T) {
	t.Run("Default Values", func(t *testing.T) {
		opts := &ObservabilityOptions{}

		// Get the defaults - shouldn't panic and should return no-op implementations
		metrics := opts.Metrics.Or(&NoOpMetrics{})
		tracer := opts.Tracer.Or(&NoOpTracer{})
		logger := opts.Logger.Or(&NoOpLogger{})

		if metrics == nil {
			t.Error("Default metrics should not be nil")
		}

		if tracer == nil {
			t.Error("Default tracer should not be nil")
		}

		if logger == nil {
			t.Error("Default logger should not be nil")
		}
	})
}

func TestMetricsCollectorUsage(t *testing.T) {
	t.Run("Record Operations", func(t *testing.T) {
		metrics := NewTestMetricsCollector()

		// Record various metrics
		metrics.RecordDuration("op.duration", 100*time.Millisecond)
		metrics.RecordSize("op.size", 1024)
		metrics.RecordCount("op.count", 5)
		metrics.RecordError("op.error", NewStoreError(ErrCodeNotFound, "test", "key", "not found", nil, false))

		// Verify recordings
		if count := metrics.GetRecordedCalls(); count != 4 {
			t.Errorf("Expected 4 recorded calls, got %d", count)
		}

		if duration, ok := metrics.GetDuration("op.duration"); !ok || duration != 100*time.Millisecond {
			t.Errorf("Duration not recorded correctly, got %v", duration)
		}

		if size, ok := metrics.GetSize("op.size"); !ok || size != 1024 {
			t.Errorf("Size not recorded correctly, got %d", size)
		}
	})
}

func TestTracingProviderUsage(t *testing.T) {
	t.Run("Span Operations", func(t *testing.T) {
		tracer := NewTestTracingProvider()
		ctx := context.Background()

		// Start spans
		span1 := tracer.StartSpan(ctx, "span1")
		span2 := tracer.StartSpan(ctx, "span2")

		// Add tags
		span1.AddTag("operation", "test")
		span2.AddTag("priority", "high")

		// End one span
		span1.End()

		// Verify
		if count := tracer.GetSpanCount(); count != 2 {
			t.Errorf("Expected 2 spans, got %d", count)
		}

		if s1, ok := tracer.GetSpan("span1"); !ok || !s1.IsEnded() {
			t.Error("Span1 should be ended")
		}

		if s2, ok := tracer.GetSpan("span2"); !ok || s2.IsEnded() {
			t.Error("Span2 should not be ended")
		}

		if s1, ok := tracer.GetSpan("span1"); !ok {
			t.Error("Span1 not found")
		} else {
			if tag, ok := s1.GetTag("operation"); !ok || tag != "test" {
				t.Errorf("Span1 tag 'operation' not set correctly, got %s", tag)
			}
		}
	})
}

func TestLoggerUsage(t *testing.T) {
	t.Run("Log Messages", func(t *testing.T) {
		logger := NewTestLogger()

		// Log messages at different levels
		logger.Log(LogLevelDebug, "Debug message", nil)
		logger.Log(LogLevelInfo, "Info message", map[string]any{
			"operation": "test",
		})
		logger.Log(LogLevelError, "Error message", map[string]any{
			"code": 404,
			"err":  "not found",
		})

		// Verify
		messages := logger.GetMessages()
		if len(messages) != 3 {
			t.Errorf("Expected 3 log messages, got %d", len(messages))
		}

		if messages[0].level != LogLevelDebug || messages[0].msg != "Debug message" {
			t.Error("Debug message not logged correctly")
		}

		if messages[1].level != LogLevelInfo || messages[1].msg != "Info message" {
			t.Error("Info message not logged correctly")
		}

		if val, ok := messages[1].fields["operation"]; !ok || val != "test" {
			t.Errorf("Info message field not set correctly, got %v", val)
		}

		if messages[2].level != LogLevelError || messages[2].msg != "Error message" {
			t.Error("Error message not logged correctly")
		}

		if val, ok := messages[2].fields["code"]; !ok || val != 404 {
			t.Errorf("Error message field 'code' not set correctly, got %v", val)
		}
	})
}
