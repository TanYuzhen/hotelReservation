package search

import (
	"fmt"
	"net"
	"time"

	"github.com/google/uuid"
	_ "github.com/mbobakov/grpc-consul-resolver"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	context "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"hotelReservation/dialer"
	"hotelReservation/registry"
	geo "hotelReservation/services/geo/proto"
	rate "hotelReservation/services/rate/proto"
	pb "hotelReservation/services/search/proto"
	"hotelReservation/tls"
)

const name = "srv-search"

// Server implments the search service
type Server struct {
	pb.UnimplementedSearchServer

	geoClient  geo.GeoClient
	rateClient rate.RateClient
	uuid       string

	Tracer         trace.Tracer
	TracerProvider trace.TracerProvider
	Port           int
	IpAddr         string
	ConsulAddr     string
	KnativeDns     string
	Registry       *registry.Client
}

// Run starts the server
func (s *Server) Run() error {
	if s.Port == 0 {
		return fmt.Errorf("server port must be set")
	}

	s.uuid = uuid.New().String()

	opts := []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Timeout: 120 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			PermitWithoutStream: true,
		}),
		grpc.UnaryInterceptor(
			otelgrpc.UnaryServerInterceptor(
				otelgrpc.WithTracerProvider(s.TracerProvider),
				otelgrpc.WithPropagators(otel.GetTextMapPropagator()),
			),
		),
	}

	if tlsopt := tls.GetServerOpt(); tlsopt != nil {
		opts = append(opts, tlsopt)
	}

	srv := grpc.NewServer(opts...)
	pb.RegisterSearchServer(srv, s)

	ctx := context.Background()
	// init grpc clients
	if err := s.initGeoClient(ctx, "srv-geo"); err != nil {
		return err
	}
	if err := s.initRateClient(ctx, "srv-rate"); err != nil {
		return err
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.Port))
	if err != nil {
		log.Fatal().Msgf("failed to listen: %v", err)
	}

	err = s.Registry.Register(name, s.uuid, s.IpAddr, s.Port)
	if err != nil {
		return fmt.Errorf("failed register: %v", err)
	}
	log.Info().Msg("Successfully registered in consul")

	return srv.Serve(lis)
}

// Shutdown cleans up any processes
func (s *Server) Shutdown() {
	s.Registry.Deregister(s.uuid)
}

func (s *Server) initGeoClient(ctx context.Context, name string) error {
	conn, err := s.getGprcConn(ctx, name)
	if err != nil {
		return fmt.Errorf("dialer error: %v", err)
	}
	s.geoClient = geo.NewGeoClient(conn)
	return nil
}

func (s *Server) initRateClient(ctx context.Context, name string) error {
	conn, err := s.getGprcConn(ctx, name)
	if err != nil {
		return fmt.Errorf("dialer error: %v", err)
	}
	s.rateClient = rate.NewRateClient(conn)
	return nil
}

func (s *Server) getGprcConn(ctx context.Context, name string) (*grpc.ClientConn, error) {
	log.Info().Msg("get Grpc conn is :")
	log.Info().Msg(s.KnativeDns)
	log.Info().Msg(fmt.Sprintf("%s.%s", name, s.KnativeDns))

	if s.KnativeDns != "" {
		return dialer.Dial(
			fmt.Sprintf("consul://%s/%s.%s", s.ConsulAddr, name, s.KnativeDns),
			ctx,
			dialer.WithTracerProvider(s.TracerProvider))
	} else {
		return dialer.Dial(
			fmt.Sprintf("consul://%s/%s", s.ConsulAddr, name),
			ctx,
			dialer.WithTracerProvider(s.TracerProvider),
			dialer.WithBalancer(s.Registry.Client),
		)
	}
}

// Nearby returns ids of nearby hotels ordered by ranking algo
func (s *Server) Nearby(ctx context.Context, req *pb.NearbyRequest) (*pb.SearchResult, error) {
	// find nearby hotels
	log.Trace().Msg("in Search Nearby")

	log.Trace().Msgf("nearby lat = %f", req.Lat)
	log.Trace().Msgf("nearby lon = %f", req.Lon)

	ctx, span := s.Tracer.Start(ctx, "searchService")
	defer span.End()

	nearby, err := s.geoClient.Nearby(ctx, &geo.Request{
		Lat: req.Lat,
		Lon: req.Lon,
	})
	if err != nil {
		return nil, err
	}

	for _, hid := range nearby.HotelIds {
		log.Trace().Msgf("get Nearby hotelId = %s", hid)
	}

	// find rates for hotels
	rates, err := s.rateClient.GetRates(ctx, &rate.Request{
		HotelIds: nearby.HotelIds,
		InDate:   req.InDate,
		OutDate:  req.OutDate,
	})
	if err != nil {
		return nil, err
	}

	// TODO(hw): add simple ranking algo to order hotel ids:
	// * geo distance
	// * price (best discount?)
	// * reviews

	// build the response
	res := new(pb.SearchResult)
	for _, ratePlan := range rates.RatePlans {
		log.Trace().Msgf("get RatePlan HotelId = %s, Code = %s", ratePlan.HotelId, ratePlan.Code)
		res.HotelIds = append(res.HotelIds, ratePlan.HotelId)
	}
	return res, nil
}
