package apiv1

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/metal-toolbox/conditionorc/internal/events"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/ginjwt"

	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
)

// Routes type sets up the conditionorc API  router routes.
type Routes struct {
	authMW        *ginjwt.Middleware
	conditionDefs []ptypes.ConditionDefinition
	repository    store.Repository
	streamBroker  events.StreamBroker
	logger        *logrus.Logger
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

// WithConditionDefs sets the conditions this router supports.
func WithConditionDefs(defs []ptypes.ConditionDefinition) Option {
	return func(r *Routes) {
		r.conditionDefs = defs
	}
}

// WithAuthMiddleware sets the auth middleware on the routes type.
func WithAuthMiddleware(authMW *ginjwt.Middleware) Option {
	return func(r *Routes) {
		r.authMW = authMW
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
		return nil, errors.New("no store repository defined")
	}

	if len(routes.conditionDefs) == 0 {
		return nil, errors.New("no condition definitions defined")
	}

	for _, c := range routes.conditionDefs {
		supported = append(supported, string(c.Name))
	}

	routes.logger.Info(
		"routes initialized with support for conditions: ",
		strings.Join(supported, ","),
	)

	return routes, nil
}

func (r *Routes) Routes(g *gin.RouterGroup) {
	//authMiddleWare := r.authMW

	//g.Use(authMiddleWare)

	// For now these don't have scopes, since @ozz suggests it'll be handled by the API gateway.
	servers := g.Group("/servers/:uuid")
	{
		// /servers/:uuid/condition
		serverCondition := servers.Group("/conditions")
		{
			serverCondition.GET("", r.serverConditionList)
		}

		// /servers/:uuid/conditions/:conditionslug
		serverConditionBySlug := servers.Group("/conditions")
		{
			// List condition on a server.
			serverConditionBySlug.GET("/:conditionslug", r.serverConditionGet)

			// Set a condition on a server.
			serverConditionBySlug.POST("/:conditionslug", r.serverConditionSet)

			// Update an existing condition attributes on a server.
			serverConditionBySlug.PUT("/:conditionslug", r.serverConditionUpdate)

			// Remove a condition from a server.
			serverConditionBySlug.DELETE("/:conditionslug", r.serverConditionDelete)
		}
	}
}
