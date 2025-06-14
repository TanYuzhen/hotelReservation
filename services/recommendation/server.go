package recommendation

import (
	"context"
	"fmt"
	"math"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/hailocab/go-geoindex"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"hotelReservation/registry"
	pb "hotelReservation/services/recommendation/proto"
	"hotelReservation/tls"
)

const name = "srv-recommendation"

// Server implements the recommendation service
type Server struct {
	pb.UnimplementedRecommendationServer

	hotels map[string]Hotel
	uuid   string

	Tracer         trace.Tracer
	TracerProvider trace.TracerProvider
	Port           int
	IpAddr         string
	MongoClient    *mongo.Client
	Registry       *registry.Client
}

// Run starts the server
func (s *Server) Run() error {
	if s.Port == 0 {
		return fmt.Errorf("server port must be set")
	}

	if s.hotels == nil {
		s.hotels = loadRecommendations(s.MongoClient)
	}

	s.uuid = uuid.New().String()

	opts := []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{ Timeout: 120 * time.Second }),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{ PermitWithoutStream: true }),
		grpc.ChainUnaryInterceptor(
			otelgrpc.UnaryServerInterceptor(
				otelgrpc.WithTracerProvider(s.TracerProvider),
				otelgrpc.WithPropagators(otel.GetTextMapPropagator()),
			),
		),
		grpc.ChainStreamInterceptor(
			otelgrpc.StreamServerInterceptor(
				otelgrpc.WithTracerProvider(s.TracerProvider),
				otelgrpc.WithPropagators(otel.GetTextMapPropagator()),
			),
		),
	}

	if tlsopt := tls.GetServerOpt(); tlsopt != nil {
		opts = append(opts, tlsopt)
	}
	
	srv := grpc.NewServer(opts...)  

	// srv := grpc.NewServer(
	// 	grpc.ChainUnaryInterceptor(
	// 		otelgrpc.UnaryServerInterceptor(
	// 			otelgrpc.WithTracerProvider(s.TracerProvider),
	// 			otelgrpc.WithPropagators(otel.GetTextMapPropagator()), 
	// 		),
	// 	),

	// 	grpc.ChainStreamInterceptor(
	// 		otelgrpc.StreamServerInterceptor(
	// 			otelgrpc.WithTracerProvider(s.TracerProvider),
	// 			otelgrpc.WithPropagators(otel.GetTextMapPropagator()), 
	// 		),
	// 	),
	// )

	pb.RegisterRecommendationServer(srv, s)

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

// GiveRecommendation returns recommendations within a given requirement.
func (s *Server) GetRecommendations(ctx context.Context, req *pb.Request) (*pb.Result, error) {
	res := new(pb.Result)
	log.Trace().Msgf("GetRecommendations")
	require := req.Require
	if require == "dis" {
		p1 := &geoindex.GeoPoint{
			Pid:  "",
			Plat: req.Lat,
			Plon: req.Lon,
		}
		min := math.MaxFloat64
		for _, hotel := range s.hotels {
			tmp := float64(geoindex.Distance(p1, &geoindex.GeoPoint{
				Pid:  "",
				Plat: hotel.HLat,
				Plon: hotel.HLon,
			})) / 1000
			if tmp < min {
				min = tmp
			}
		}
		for _, hotel := range s.hotels {
			tmp := float64(geoindex.Distance(p1, &geoindex.GeoPoint{
				Pid:  "",
				Plat: hotel.HLat,
				Plon: hotel.HLon,
			})) / 1000
			if tmp == min {
				res.HotelIds = append(res.HotelIds, hotel.HId)
			}
		}
	} else if require == "rate" {
		max := 0.0
		for _, hotel := range s.hotels {
			if hotel.HRate > max {
				max = hotel.HRate
			}
		}
		for _, hotel := range s.hotels {
			if hotel.HRate == max {
				res.HotelIds = append(res.HotelIds, hotel.HId)
			}
		}
	} else if require == "price" {
		min := math.MaxFloat64
		for _, hotel := range s.hotels {
			if hotel.HPrice < min {
				min = hotel.HPrice
			}
		}
		for _, hotel := range s.hotels {
			if hotel.HPrice == min {
				res.HotelIds = append(res.HotelIds, hotel.HId)
			}
		}
	} else {
		log.Warn().Msgf("Wrong require parameter: %v", require)
	}

	return res, nil
}

// loadRecommendations loads hotel recommendations from mongodb.
func loadRecommendations(client *mongo.Client) map[string]Hotel {
	collection := client.Database("recommendation-db").Collection("recommendation")
	curr, err := collection.Find(context.TODO(), bson.D{})
	if err != nil {
		log.Error().Msgf("Failed get hotels data: ", err)
	}

	var hotels []Hotel
	curr.All(context.TODO(), &hotels)
	if err != nil {
		log.Error().Msgf("Failed get hotels data: ", err)
	}

	profiles := make(map[string]Hotel)
	for _, hotel := range hotels {
		profiles[hotel.HId] = hotel
	}

	return profiles
}

type Hotel struct {
	HId    string  `bson:"hotelId"`
	HLat   float64 `bson:"lat"`
	HLon   float64 `bson:"lon"`
	HRate  float64 `bson:"rate"`
	HPrice float64 `bson:"price"`
}
