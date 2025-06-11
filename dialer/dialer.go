package dialer

import (
	"context"
	"fmt"
	"time"

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
// DialOption configures the gRPC dialer.
type DialOption func(cfg *dialOptions) error

type dialOptions struct {
	dialopts []grpc.DialOption
	unary    []grpc.UnaryClientInterceptor
	stream   []grpc.StreamClientInterceptor
}

// WithTracerProvider configures tracing for grpc clients.
func WithTracerProvider(tp trace.TracerProvider) DialOption {
	return func(cfg *dialOptions) error {
		cfg.unary = append(cfg.unary,
			otelgrpc.UnaryClientInterceptor(
				otelgrpc.WithTracerProvider(tp),
				otelgrpc.WithPropagators(otel.GetTextMapPropagator()),
			))
		cfg.stream = append(cfg.stream,
			otelgrpc.StreamClientInterceptor(
				otelgrpc.WithTracerProvider(tp),
				otelgrpc.WithPropagators(otel.GetTextMapPropagator()),
			))
		return nil
	}
}

// WithBalancer enables client side load balancing
func WithBalancer(registry *consul.Client) DialOption {
	return func(cfg *dialOptions) error {
		cfg.dialopts = append(cfg.dialopts,
			grpc.WithDefaultServiceConfig(`{"loadBalancingConfig": [{"round_robin":{}}]}`),
		)
		return nil
	}
}

// Dial returns a load balanced grpc client conn with tracing interceptor
func Dial(name string, ctx context.Context, opts ...DialOption) (*grpc.ClientConn, error) {

	// dialopts := []grpc.DialOption{
	// 	grpc.WithKeepaliveParams(keepalive.ClientParameters{
	// 		Timeout:             120 * time.Second,
	// 		PermitWithoutStream: true,
	// 	}),
	// }
	// if tlsopt := tls.GetDialOpt(); tlsopt != nil {
	// 	dialopts = append(dialopts, tlsopt)
	// } else {
	// 	dialopts = append(dialopts, grpc.WithInsecure())
	// }

	// for _, fn := range opts {
	// 	opt, err := fn(name)
	// 	if err != nil {
	// 		return nil, fmt.Errorf("config error: %v", err)
	// 	}
	// 	dialopts = append(dialopts, opt)
	// }

	// conn, err := grpc.Dial(name, dialopts...)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to dial %s: %v", name, err)
	// }

	// return conn, nil
	ctx, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()

	cfg := dialOptions{
		dialopts: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		},
	}

	for _, fn := range opts {
		if err := fn(&cfg); err != nil {
			return nil, fmt.Errorf("config error: %v", err)
		}
	}

	if len(cfg.unary) == 0 {
		cfg.unary = append(cfg.unary,
			otelgrpc.UnaryClientInterceptor(
				otelgrpc.WithTracerProvider(otel.GetTracerProvider()),
				otelgrpc.WithPropagators(otel.GetTextMapPropagator()),
			))
		cfg.stream = append(cfg.stream,
			otelgrpc.StreamClientInterceptor(
				otelgrpc.WithTracerProvider(otel.GetTracerProvider()),
				otelgrpc.WithPropagators(otel.GetTextMapPropagator()),
			))
	}

	cfg.dialopts = append(cfg.dialopts,
		grpc.WithChainUnaryInterceptor(cfg.unary...),
		grpc.WithChainStreamInterceptor(cfg.stream...),
	)

	dialopts := cfg.dialopts

	conn, err := grpc.DialContext(ctx, name, dialopts...)
	if err != nil {
		return nil, fmt.Errorf("config error: %v", err)
	}
	return conn, nil
}
