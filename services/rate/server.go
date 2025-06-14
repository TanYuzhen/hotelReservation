package rate

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
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
	pb "hotelReservation/services/rate/proto"
	"hotelReservation/tls"
)

const name = "srv-rate"

// Server implements the rate service
type Server struct {
	pb.UnimplementedRateServer

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

	pb.RegisterRateServer(srv, s)

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

// GetRates gets rates for hotels for specific date range.
func (s *Server) GetRates(ctx context.Context, req *pb.Request) (*pb.Result, error) {
	res := new(pb.Result)
	ratePlans := make(RatePlans, 0)
	hotelIds := []string{}
	rateMap := make(map[string]struct{})
	for _, hotelID := range req.HotelIds {
		hotelIds = append(hotelIds, hotelID)
		rateMap[hotelID] = struct{}{}
	}
	// first check memcached(get-multi)
	// ctx, span := s.Tracer.Start(ctx, "memcached_get_multi_rate", trace.WithSpanKind(trace.SpanKindClient))
	resMap, err := s.MemcClient.GetMulti(hotelIds)
	// span.End()

	var wg sync.WaitGroup
	var mutex sync.Mutex
	if err != nil && err != memcache.ErrCacheMiss {
		log.Panic().Msgf("Memmcached error while trying to get hotel [id: %v]= %s", hotelIds, err)
	} else {
		for hotelId, item := range resMap {
			rateStrs := strings.Split(string(item.Value), "\n")
			log.Trace().Msgf("memc hit, hotelId = %s,rate strings: %v", hotelId, rateStrs)

			for _, rateStr := range rateStrs {
				if len(rateStr) != 0 {
					rateP := new(pb.RatePlan)
					json.Unmarshal([]byte(rateStr), rateP)
					ratePlans = append(ratePlans, rateP)
				}
			}

			delete(rateMap, hotelId)
		}

		wg.Add(len(rateMap))
		for hotelId := range rateMap {
			go func(id string) {
				log.Trace().Msgf("memc miss, hotelId = %s", id)
				log.Trace().Msg("memcached miss, set up mongo connection")

				// _, span := s.Tracer.Start(ctx, "mongo_rate", trace.WithSpanKind(trace.SpanKindClient))

				// memcached miss, set up mongo connection
				collection := s.MongoClient.Database("rate-db").Collection("inventory")
				curr, err := collection.Find(context.TODO(), bson.D{})
				if err != nil {
					log.Error().Msgf("Failed get rate data: ", err)
				}

				tmpRatePlans := make(RatePlans, 0)
				curr.All(context.TODO(), &tmpRatePlans)
				if err != nil {
					log.Error().Msgf("Failed get rate data: ", err)
				}

				// span.End()

				memcStr := ""
				if err != nil {
					log.Panic().Msgf("Tried to find hotelId [%v], but got error", id, err.Error())
				} else {
					for _, r := range tmpRatePlans {
						mutex.Lock()
						ratePlans = append(ratePlans, r)
						mutex.Unlock()
						rateJson, err := json.Marshal(r)
						if err != nil {
							log.Error().Msgf("Failed to marshal plan [Code: %v] with error: %s", r.Code, err)
						}
						memcStr = memcStr + string(rateJson) + "\n"
					}
				}
				go s.MemcClient.Set(&memcache.Item{Key: id, Value: []byte(memcStr)})

				defer wg.Done()
			}(hotelId)
		}
	}
	wg.Wait()

	sort.Sort(ratePlans)
	res.RatePlans = ratePlans

	return res, nil
}

type RatePlans []*pb.RatePlan

func (r RatePlans) Len() int {
	return len(r)
}

func (r RatePlans) Swap(i, j int) {
	r[i], r[j] = r[j], r[i]
}

func (r RatePlans) Less(i, j int) bool {
	return r[i].RoomType.TotalRate > r[j].RoomType.TotalRate
}
