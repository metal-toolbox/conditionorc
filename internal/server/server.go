package server

import (
	"net/http"
	"time"

	docs "github.com/metal-toolbox/conditionorc/docs"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/gin-gonic/gin"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/metal-toolbox/conditionorc/pkg/api/v1/routes"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
	"go.hollow.sh/toolbox/ginjwt"
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
	authMWConfig         *ginjwt.AuthConfig
	logger               *logrus.Logger
	streamBroker         events.Stream
	listenAddress        string
	conditionDefinitions ptypes.ConditionDefinitions
	repository           store.Repository
}

// Option type sets a parameter on the Server type.
type Option func(*Server)

// WithStore sets the storage repository on the Server type.
func WithStore(repository store.Repository) Option {
	return func(s *Server) {
		s.repository = repository
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
func WithConditionDefinitions(defs ptypes.ConditionDefinitions) Option {
	return func(s *Server) {
		s.conditionDefinitions = defs
	}
}

// WithAuthMiddlewareConfig sets the auth middleware configuration.
func WithAuthMiddlewareConfig(authMWConfig *ginjwt.AuthConfig) Option {
	return func(s *Server) {
		s.authMWConfig = authMWConfig
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
		routes.WithStreamBroker(s.streamBroker),
		routes.WithConditionDefinitions(s.conditionDefinitions),
	}

	// add auth middleware
	if s.authMWConfig != nil && s.authMWConfig.Enabled {
		authMW, err := ginjwt.NewAuthMiddleware(*s.authMWConfig)
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

// ping checks the server can reach its store repository
func (s *Server) ping(c *gin.Context) {
	if err := s.repository.Ping(c.Request.Context()); err != nil {
		s.logger.Errorf("storage repository ping check failed: %s", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "DOWN",
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "UP",
	})
}
