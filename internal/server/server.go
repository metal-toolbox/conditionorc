package server

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/metal-toolbox/conditionorc/internal/events"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/metal-toolbox/conditionorc/pkg/api/v1/routes"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	ginlogrus "github.com/toorop/gin-logrus"
)

var (
	// Request read timeout.
	readTimeout = 10 * time.Second
	// Request write timeout.
	writeTimeout = 20 * time.Second

	ErrRoutes = errors.New("error in routes")
)

type Server struct {
	// Logger is the app logger
	logger        *logrus.Logger
	listenAddress string
	repository    store.Repository
	streamBroker  events.StreamBroker
	engine        *gin.Engine
}

// Option type sets a parameter on the Server type.
type Option func(Server)

// WithStore sets the storage repository on the Server type.
func WithStore(repository store.Repository) Option {
	return func(s Server) {
		s.repository = repository
	}
}

// WithStreamBroker sets the event stream broker on the Server type.
func WithStreamBroker(broker events.StreamBroker) Option {
	return func(s Server) {
		s.streamBroker = broker
	}
}

// WithLogger sets the logger on the Server type.
func WithLogger(logger *logrus.Logger) Option {
	return func(s Server) {
		s.logger = logger
	}
}

// WithListenAddress sets the Server listen address.
func WithListenAddress(addr string) Option {
	return func(s Server) {
		s.listenAddress = addr
	}
}

func New(opts ...Option) *http.Server {
	s := Server{}

	for _, opt := range opts {
		opt(s)
	}
	//	authMW, err := ginjwt.NewAuthMiddleware(authConfig)
	//	if err != nil {
	//		s.logger.Fatal("failed to initialize auth middleware", "error", err)
	//	}

	g := gin.New()
	g.Use(ginlogrus.Logger(s.logger), gin.Recovery())

	g.GET("/healthz/readiness", s.ping)

	options := []routes.Option{
		//		apiv1.WithAuthMiddleware(authMW),
		routes.WithLogger(s.logger),
		routes.WithStore(s.repository),
		routes.WithStreamBroker(s.streamBroker),
	}

	v1Router, err := routes.NewRoutes(options...)
	if err != nil {
		s.logger.Fatal(errors.Wrap(err, ErrRoutes.Error()))
	}

	v1Router.Routes(g.Group("/api/v1"))

	g.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"message": "invalid request - route not found"})
	})

	return &http.Server{
		Addr:         s.listenAddress,
		Handler:      g,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}
}

// Run method here runs the http server, its mainly here for tests.
func (s *Server) Run(ctx context.Context, listenAddress string) {
	s.engine.Run(listenAddress)
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
