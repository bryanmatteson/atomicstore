package store

import (
	"context"
	"time"
)

// LogLevel defines the severity of a log message
type LogLevel int

// Log levels
const (
	LogLevelDebug LogLevel = iota
	LogLevelInfo
	LogLevelWarn
	LogLevelError
)

// MetricsCollector records operational metrics
type MetricsCollector interface {
	// RecordDuration records the duration of an operation
	RecordDuration(name string, duration time.Duration)

	// RecordSize records the size of data in bytes
	RecordSize(name string, size int64)

	// RecordCount increments a counter
	RecordCount(name string, count int64)

	// RecordError records an error
	RecordError(name string, err error)
}

// Span represents a tracing span
type Span interface {
	// End completes the span
	End()

	// AddTag adds metadata to the span
	AddTag(key string, value string)
}

// TracingProvider creates spans for tracing
type TracingProvider interface {
	// StartSpan begins a new span
	StartSpan(ctx context.Context, name string) Span
}

// Logger provides structured logging
type Logger interface {
	// Log records a log message with level and fields
	Log(level LogLevel, msg string, fields map[string]any)
}

// ObservabilityOptions contains configuration for observability
type ObservabilityOptions struct {
	// Metrics collector
	Metrics Field[MetricsCollector]

	// Tracing provider
	Tracer Field[TracingProvider]

	// Logger implementation
	Logger Field[Logger]
}

// ObservabilityOption is the interface for all observability options
type ObservabilityOption interface {
	applyObservability(*ObservabilityOptions)
}

// ObservabilityOptionFunc implements ObservabilityOption as a function
type ObservabilityOptionFunc func(*ObservabilityOptions)

func (f ObservabilityOptionFunc) applyObservability(opts *ObservabilityOptions) {
	f(opts)
}

// WithMetrics sets the metrics collector
func WithMetrics(metrics MetricsCollector) ObservabilityOption {
	return ObservabilityOptionFunc(func(opts *ObservabilityOptions) {
		opts.Metrics.Set(metrics)
	})
}

// WithTracer sets the tracing provider
func WithTracer(tracer TracingProvider) ObservabilityOption {
	return ObservabilityOptionFunc(func(opts *ObservabilityOptions) {
		opts.Tracer.Set(tracer)
	})
}

// WithLogger sets the logger implementation
func WithLogger(logger Logger) ObservabilityOption {
	return ObservabilityOptionFunc(func(opts *ObservabilityOptions) {
		opts.Logger.Set(logger)
	})
}

// applyObservabilityOptions applies a slice of observability options to the target options
func applyObservabilityOptions(opts *ObservabilityOptions, options []ObservabilityOption) {
	for _, opt := range options {
		opt.applyObservability(opts)
	}
}

// Allow ObservabilityOptions to be used as an ObservabilityOption
func (o ObservabilityOptions) applyObservability(opts *ObservabilityOptions) {
	opts.Metrics.SetDefaultFrom(o.Metrics.Get)
	opts.Tracer.SetDefaultFrom(o.Tracer.Get)
	opts.Logger.SetDefaultFrom(o.Logger.Get)
}

// NoOp Implementations

// NoOpMetrics provides a no-op metrics collector
type NoOpMetrics struct{}

func NewNoOpMetrics() *NoOpMetrics {
	return &NoOpMetrics{}
}

func (m *NoOpMetrics) RecordDuration(string, time.Duration) {}
func (m *NoOpMetrics) RecordSize(string, int64)             {}
func (m *NoOpMetrics) RecordCount(string, int64)            {}
func (m *NoOpMetrics) RecordError(string, error)            {}

// NoOpSpan provides a no-op tracing span
type NoOpSpan struct{}

func (s *NoOpSpan) End()                  {}
func (s *NoOpSpan) AddTag(string, string) {}

// NoOpTracer provides a no-op tracing provider
type NoOpTracer struct{}

func NewNoOpTracer() *NoOpTracer {
	return &NoOpTracer{}
}

func (t *NoOpTracer) StartSpan(context.Context, string) Span {
	return &NoOpSpan{}
}

// NoOpLogger provides a no-op logger
type NoOpLogger struct{}

func NewNoOpLogger() *NoOpLogger {
	return &NoOpLogger{}
}

func (l *NoOpLogger) Log(LogLevel, string, map[string]any) {}
