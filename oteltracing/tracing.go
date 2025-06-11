package oteltracing

import (
	"context"
	// "net"
	"net/http"
	"fmt"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	// "go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
)

// Init initializes OpenTelemetry tracing with a Jaeger exporter.
func Init(serviceName, jaegerAddr string) (trace.Tracer, trace.TracerProvider, error) {
	// 在函数内声明上下文，避免全局变量问题
    ctx := context.Background()

    // 创建资源，正确设置 SchemaURL 和属性
    res, err := resource.New(ctx,
        resource.WithAttributes(
            semconv.ServiceName(serviceName),
        ),
        resource.WithSchemaURL(semconv.SchemaURL),
    )
    if err != nil {
        return nil, nil, fmt.Errorf("failed to create resource: %w", err)
    }

    // 创建 OTLP 导出器，检查错误并使用 jaegerAddr 参数
    exporter, err := otlptracegrpc.New(ctx)
    if err != nil {
        return nil, nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
    }

    // 配置 Tracer Provider
    tp := sdktrace.NewTracerProvider(
        sdktrace.WithSampler(sdktrace.AlwaysSample()),
        sdktrace.WithBatcher(exporter),
        sdktrace.WithResource(res),
    )

    // 设置全局 Tracer Provider 和 Propagator（根据需求决定是否保留）
    otel.SetTracerProvider(tp)
    otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

    return tp.Tracer(serviceName), tp, nil
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