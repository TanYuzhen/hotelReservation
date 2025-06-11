package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"hotelReservation/registry"
	pb "hotelReservation/services/profile/proto"
	"hotelReservation/tls"
)

const name = "srv-profile"

// Server implements the profile service
type Server struct {
	pb.UnimplementedProfileServer

	uuid string

	Tracer         trace.Tracer
	TracerProvider trace.TracerProvider
	Port           int
	IpAddr         string
	MongoClient    *mongo.Client
	Registry       *registry.Client
	MemcClient     *memcache.Client
}

// Run starts the server
func (s *Server) Run() error {

	if s.Port == 0 {
		return fmt.Errorf("server port must be set")
	}

	s.uuid = uuid.New().String()

	log.Trace().Msgf("in run s.IpAddr = %s, port = %d", s.IpAddr, s.Port)

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

	pb.RegisterProfileServer(srv, s)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.Port))
	if err != nil {
		log.Fatal().Msgf("failed to configure listener: %v", err)
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

// GetProfiles returns hotel profiles for requested IDs
func (s *Server) GetProfiles(ctx context.Context, req *pb.Request) (*pb.Result, error) {
	log.Trace().Msgf("In GetProfiles")
	
	ctx, span := s.Tracer.Start(ctx, "searchHandler")
	defer span.End()
	
	var wg sync.WaitGroup
	var mutex sync.Mutex

	// one hotel should only have one profile
	hotelIds := make([]string, 0)
	profileMap := make(map[string]struct{})
	for _, hotelId := range req.HotelIds {
		hotelIds = append(hotelIds, hotelId)
		profileMap[hotelId] = struct{}{}
	}

	// ctx, span := s.Tracer.Start(ctx, "memcached_get_profile", trace.WithSpanKind(trace.SpanKindClient))
	resMap, err := s.MemcClient.GetMulti(hotelIds)
	// span.End()

	res := new(pb.Result)
	hotels := make([]*pb.Hotel, 0)

	if err != nil && err != memcache.ErrCacheMiss {
		log.Panic().Msgf("Tried to get hotelIds [%v], but got memmcached error = %s", hotelIds, err)
	} else {
		for hotelId, item := range resMap {
			profileStr := string(item.Value)
			log.Trace().Msgf("memc hit with %v", profileStr)

			hotelProf := new(pb.Hotel)
			json.Unmarshal(item.Value, hotelProf)
			hotels = append(hotels, hotelProf)
			delete(profileMap, hotelId)
		}

		wg.Add(len(profileMap))
		for hotelId := range profileMap {
			go func(hotelId string) {
				var hotelProf *pb.Hotel

				collection := s.MongoClient.Database("profile-db").Collection("hotels")

				// _, span := s.Tracer.Start(ctx, "mongo_profile", trace.WithSpanKind(trace.SpanKindClient))
				err := collection.FindOne(context.TODO(), bson.D{{"id", hotelId}}).Decode(&hotelProf)
				// span.End()

				if err != nil {
					log.Error().Msgf("Failed get hotels data: ", err)
				}

				mutex.Lock()
				hotels = append(hotels, hotelProf)
				mutex.Unlock()

				profJson, err := json.Marshal(hotelProf)
				if err != nil {
					log.Error().Msgf("Failed to marshal hotel [id: %v] with err:", hotelProf.Id, err)
				}
				memcStr := string(profJson)

				// write to memcached
				go s.MemcClient.Set(&memcache.Item{Key: hotelId, Value: []byte(memcStr)})
				defer wg.Done()
			}(hotelId)
		}
	}
	wg.Wait()

	res.Hotels = hotels
	log.Trace().Msgf("In GetProfiles after getting resp")
	return res, nil
}
