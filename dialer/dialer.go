package dialer

import (
	"fmt"
	"time"

	"hotelReservation/tls"
	consul "github.com/hashicorp/consul/api"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

// DialOption allows optional config for dialer
type DialOption func(name string) (grpc.DialOption, error)

// WithBalancer enables client side load balancing
func WithBalancer(registry *consul.Client) DialOption {
	return func(name string) (grpc.DialOption, error) {
		return grpc.WithDefaultServiceConfig(`{"loadBalancingConfig": [{"round_robin":{}}]}`), nil
	}
}

// Dial returns a load balanced grpc client conn with tracing interceptor
func Dial(name string, tp trace.TracerProvider, opts ...DialOption) (*grpc.ClientConn, error) {
	dialopts := []grpc.DialOption{
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Timeout:             120 * time.Second,
			PermitWithoutStream: true,
		}),
	}

	// Apply TLS or insecure option
	if tlsopt := tls.GetDialOpt(); tlsopt != nil {
		dialopts = append(dialopts, tlsopt)
	} else {
		dialopts = append(dialopts, grpc.WithInsecure())
	}

	// Apply user options (e.g. load balancing)
	for _, fn := range opts {
		opt, err := fn(name)
		if err != nil {
			return nil, fmt.Errorf("config error: %v", err)
		}
		dialopts = append(dialopts, opt)
	}

	dialopts = append(dialopts,
		grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor(
			otelgrpc.WithTracerProvider(tp),
		)),
	)

	conn, err := grpc.Dial(name, dialopts...)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %v", name, err)
	}
	return conn, nil
}
