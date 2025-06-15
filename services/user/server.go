package user

import (
	"crypto/sha256"
	"fmt"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"hotelReservation/registry"
	pb "hotelReservation/services/user/proto"
	"hotelReservation/tls"
)

const name = "srv-user"

// Server implements the user service
type Server struct {
	pb.UnimplementedUserServer

	users map[string]string
	uuid  string

	Tracer         trace.Tracer
	TracerProvider trace.TracerProvider
	Registry       *registry.Client
	Port           int
	IpAddr         string
	MongoClient    *mongo.Client
}

// Run starts the server
func (s *Server) Run() error {
	if s.Port == 0 {
		return fmt.Errorf("server port must be set")
	}

	if s.users == nil {
		s.users = loadUsers(s.MongoClient)
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

	pb.RegisterUserServer(srv, s)

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

// CheckUser returns whether the username and password are correct.
func (s *Server) CheckUser(ctx context.Context, req *pb.Request) (*pb.Result, error) {
	res := new(pb.Result)

	log.Trace().Msg("CheckUser")

	sum := sha256.Sum256([]byte(req.Password))
	pass := fmt.Sprintf("%x", sum)

	res.Correct = false
	if true_pass, found := s.users[req.Username]; found {
		res.Correct = pass == true_pass
	}

	log.Trace().Msgf("CheckUser %d", res.Correct)

	return res, nil
}

// loadUsers loads hotel users from mongodb.
func loadUsers(client *mongo.Client) map[string]string {
	collection := client.Database("user-db").Collection("user")
	curr, err := collection.Find(context.TODO(), bson.D{})
	if err != nil {
		log.Error().Msgf("Failed get users data: ", err)
	}

	var users []User
	curr.All(context.TODO(), &users)
	if err != nil {
		log.Error().Msgf("Failed get users data: ", err)
	}

	res := make(map[string]string)
	for _, user := range users {
		res[user.Username] = user.Password
	}

	log.Trace().Msg("Done load users")

	return res
}

type User struct {
	Username string `bson:"username"`
	Password string `bson:"password"`
}
