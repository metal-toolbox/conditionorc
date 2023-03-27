package routes

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/metal-toolbox/conditionorc/internal/store"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
	"go.hollow.sh/toolbox/ginjwt"
)

const (
	PathPrefix = "/api/v1"
)

// Routes type sets up the conditionorc API  router routes.
type Routes struct {
	authMW               *ginjwt.Middleware
	repository           store.Repository
	streamBroker         events.StreamBroker
	conditionDefinitions ptypes.ConditionDefinitions
	logger               *logrus.Logger
}

// Option type sets a parameter on the Routes type.
type Option func(*Routes)

// WithStore sets the storage repository on the routes type.
func WithStore(repository store.Repository) Option {
	return func(r *Routes) {
		r.repository = repository
	}
}

// WithStreamBroker sets the event stream broker.
func WithStreamBroker(broker events.StreamBroker) Option {
	return func(r *Routes) {
		r.streamBroker = broker
	}
}

// WithLogger sets the logger on the routes type.
func WithLogger(logger *logrus.Logger) Option {
	return func(r *Routes) {
		r.logger = logger
	}
}

// WithAuthMiddleware sets the auth middleware on the routes type.
func WithAuthMiddleware(authMW *ginjwt.Middleware) Option {
	return func(r *Routes) {
		r.authMW = authMW
	}
}

// WithConditionDefinitions sets the supported condition types.
func WithConditionDefinitions(defs ptypes.ConditionDefinitions) Option {
	return func(r *Routes) {
		r.conditionDefinitions = defs
	}
}

// NewRoutes returns a new conditionorc API routes with handlers registered.
func NewRoutes(options ...Option) (*Routes, error) {
	routes := &Routes{}

	for _, opt := range options {
		opt(routes)
	}

	supported := []string{}

	if routes.repository == nil {
		return nil, errors.Wrap(ErrStore, "no store repository defined")
	}

	routes.logger.Debug(
		"routes initialized with support for conditions: ",
		strings.Join(supported, ","),
	)

	return routes, nil
}

func (r *Routes) Routes(g *gin.RouterGroup) {
	// For now these don't have scopes, since @ozz suggests it'll be handled by the API gateway.
	servers := g.Group("/servers/:uuid")
	{
		// /servers/:uuid/state/:conditionState
		serverCondition := servers.Group("/state")

		serverCondition.GET("/:conditionState", r.serverConditionList)

		// /servers/:uuid/condition/:conditionKind
		serverConditionBySlug := servers.Group("/condition")

		// List condition on a server.
		serverConditionBySlug.GET("/:conditionKind", r.serverConditionGet)

		// Create a condition on a server.
		serverConditionBySlug.POST("/:conditionKind", r.serverConditionCreate)

		// Update an existing condition attributes on a server.
		serverConditionBySlug.PUT("/:conditionKind", r.serverConditionUpdate)

		// Remove a condition from a server.
		serverConditionBySlug.DELETE("/:conditionKind", r.serverConditionDelete)
	}
}
