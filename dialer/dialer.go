package dialer

import (
	"context"
	// "fmt"
	// "time"

	// "hotelReservation/tls"
	consul "github.com/hashicorp/consul/api"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	// "google.golang.org/grpc/keepalive"
)

// DialOption allows optional config for dialer
type DialOption func(name string) (grpc.DialOption, error)

// WithTracerProvider configures tracing for grpc clients.
// func WithTracerProvider(tp trace.TracerProvider) DialOption {
// 	return func(name string) (grpc.DialOption, error) {
// 		return grpc.WithUnaryInterceptor(
// 			otelgrpc.UnaryClientInterceptor(
// 				otelgrpc.WithTracerProvider(s.TracerProvider),
// 				otelgrpc.WithPropagators(otel.GetTextMapPropagator()),
// 			)), nil
// 	}
// }

// WithBalancer enables client side load balancing
func WithBalancer(registry *consul.Client) DialOption {
	return func(name string) (grpc.DialOption, error) {
		return grpc.WithDefaultServiceConfig(`{"loadBalancingConfig": [{"round_robin":{}}]}`), nil
	}
}

// Dial returns a load balanced grpc client conn with tracing interceptor
func Dial(name string, ctx context.Context, tp trace.TracerProvider, opts ...DialOption) (*grpc.ClientConn, error) {
	dials := []grpc.DialOption{
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithChainUnaryInterceptor(
            otelgrpc.UnaryClientInterceptor(
                otelgrpc.WithTracerProvider(tp),
                otelgrpc.WithPropagators(otel.GetTextMapPropagator()),
            ),
        ),
        grpc.WithChainStreamInterceptor(
            otelgrpc.StreamClientInterceptor(
                otelgrpc.WithTracerProvider(tp),
                otelgrpc.WithPropagators(otel.GetTextMapPropagator()),
            ),
        ),
    }
    // 把可变参数拼进来
    for _, c := range opts {
        if opt, err := c(name); err != nil {
            return nil, err
        } else {
            dials = append(dials, opt)
        }
    }
    return grpc.DialContext(ctx, name, dials...)
}
