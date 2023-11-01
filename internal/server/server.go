package server

import (
	"net/http"
	"time"

	docs "github.com/metal-toolbox/conditionorc/docs"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/gin-gonic/gin"
	"github.com/metal-toolbox/conditionorc/internal/fleetdb"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/metal-toolbox/conditionorc/pkg/api/v1/routes"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
	"go.hollow.sh/toolbox/ginjwt"

	rctypes "github.com/metal-toolbox/rivets/condition"
)

var (
	// Request read timeout.
	readTimeout = 10 * time.Second
	// Request write timeout.
	writeTimeout = 20 * time.Second

	ErrRoutes = errors.New("error in routes")
)

// Server type holds attributes of the condition orc server
type Server struct {
	// Logger is the app logger
	authMWConfigs        []ginjwt.AuthConfig
	logger               *logrus.Logger
	streamBroker         events.Stream
	listenAddress        string
	conditionDefinitions rctypes.Definitions
	repository           store.Repository
	fleetDBClient        fleetdb.FleetDB
}

// Option type sets a parameter on the Server type.
type Option func(*Server)

// WithStore sets the storage repository on the Server type.
func WithStore(repository store.Repository) Option {
	return func(s *Server) {
		s.repository = repository
	}
}

// WithFleetDBClient sets the client communicating with the fleet db.
func WithFleetDBClient(client fleetdb.FleetDB) Option {
	return func(s *Server) {
		s.fleetDBClient = client
	}
}

// WithLogger sets the logger on the Server type.
func WithLogger(logger *logrus.Logger) Option {
	return func(s *Server) {
		s.logger = logger
	}
}

// WithListenAddress sets the Server listen address.
func WithListenAddress(addr string) Option {
	return func(s *Server) {
		s.listenAddress = addr
	}
}

// WithStreamBroker sets the event stream broker.
func WithStreamBroker(broker events.Stream) Option {
	return func(s *Server) {
		s.streamBroker = broker
	}
}

// WithConditionDefinitions sets the supported condition types.
func WithConditionDefinitions(defs rctypes.Definitions) Option {
	return func(s *Server) {
		s.conditionDefinitions = defs
	}
}

// WithAuthMiddlewareConfig sets the auth middleware configuration.
func WithAuthMiddlewareConfig(authMWConfigs []ginjwt.AuthConfig) Option {
	return func(s *Server) {
		s.authMWConfigs = authMWConfigs
	}
}

func New(opts ...Option) *http.Server {
	s := &Server{}

	for _, opt := range opts {
		opt(s)
	}

	g := gin.New()
	g.Use(loggerMiddleware(s.logger), gin.Recovery())

	g.GET("/healthz/readiness", s.ping)

	options := []routes.Option{
		routes.WithLogger(s.logger),
		routes.WithStore(s.repository),
		routes.WithFleetDBClient(s.fleetDBClient),
		routes.WithStreamBroker(s.streamBroker),
		routes.WithConditionDefinitions(s.conditionDefinitions),
	}

	// add auth middleware
	if s.authMWConfigs != nil {
		authMW, err := ginjwt.NewMultiTokenMiddlewareFromConfigs(s.authMWConfigs...)
		if err != nil {
			s.logger.Fatal("failed to initialize auth middleware: ", "error", err)
		}

		options = append(options, routes.WithAuthMiddleware(authMW))
	}

	v1Router, err := routes.NewRoutes(options...)
	if err != nil {
		s.logger.Fatal(errors.Wrap(err, ErrRoutes.Error()))
	}

	v1Router.Routes(g.Group(routes.PathPrefix))

	g.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"message": "invalid request - route not found"})
	})

	// Swagger Doc API Endpoint. <IP:Port>/api/v1/docs/index.html is the URL you want
	docs.SwaggerInfo.BasePath = "/api/v1"
	g.GET("/api/v1/docs/*any", ginSwagger.WrapHandler(swaggerfiles.Handler))

	return &http.Server{
		Addr:         s.listenAddress,
		Handler:      g,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}
}

// this is a placeholder for a more comprehensive readiness check
func (s *Server) ping(c *gin.Context) {
	// XXX: the repository Ping method was removed because it returns a nil error unconditionally
	c.JSON(http.StatusOK, gin.H{
		"status": "UP",
	})
}
