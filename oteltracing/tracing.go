package oteltracing

import (
	"context"
	"net"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
)

// Init initializes OpenTelemetry tracing with a Jaeger exporter.
func Init(serviceName, jaegerAddr string) (trace.Tracer, trace.TracerProvider, error) {
	host, port, err := net.SplitHostPort(jaegerAddr)
	if err != nil {
		host = jaegerAddr
		port = "6831"
	}

	exp, err := jaeger.New(jaeger.WithAgentEndpoint(
		jaeger.WithAgentHost(host),
		jaeger.WithAgentPort(port),
	))
	if err != nil {
		return nil, nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
		)),
	)

	otel.SetTracerProvider(tp)

	return tp.Tracer(serviceName), tp , nil
}

// Shutdown flushes and closes the tracer provider.
func Shutdown(ctx context.Context) error {
	if tp := otel.GetTracerProvider(); tp != nil {
		if sdk, ok := tp.(*sdktrace.TracerProvider); ok {
			return sdk.Shutdown(ctx)
		}
	}
	return nil
}

// NewServeMux returns a standard HTTP ServeMux.
// ServeMux instruments all registered handlers with OpenTelemetry.
type ServeMux struct {
	mux *http.ServeMux
}

// ServeHTTP implements http.Handler.
func (s *ServeMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// Handle registers the handler for the given pattern with tracing enabled.
func (s *ServeMux) Handle(pattern string, handler http.Handler) {
	s.mux.Handle(pattern, otelhttp.NewHandler(handler, pattern))
}

// HandleFunc registers the handler function for the given pattern with tracing enabled.
func (s *ServeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	s.Handle(pattern, http.HandlerFunc(handler))
}

// NewServeMux creates a ServeMux that instruments handlers with OpenTelemetry.
func NewServeMux() *ServeMux {
	return &ServeMux{mux: http.NewServeMux()}
}
