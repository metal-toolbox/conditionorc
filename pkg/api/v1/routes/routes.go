package routes

import (
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/metal-toolbox/conditionorc/internal/fleetdb"
	"github.com/metal-toolbox/conditionorc/internal/metrics"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
	"go.hollow.sh/toolbox/ginauth"

	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
)

const (
	PathPrefix = "/api/v1"
)

var pkgName = "pkg/api/v1/routes"

var ginNoOp = func(_ *gin.Context) {
}

// Routes type sets up the conditionorc API  router routes.
type Routes struct {
	authMW               *ginauth.MultiTokenMiddleware
	fleetDBClient        fleetdb.FleetDB
	repository           store.Repository
	streamBroker         events.Stream
	conditionDefinitions rctypes.Definitions
	logger               *logrus.Logger
	enableServerEnroll   bool
}

// Option type sets a parameter on the Routes type.
type Option func(*Routes)

// WithStore sets the storage repository on the routes type.
func WithStore(repository store.Repository) Option {
	return func(r *Routes) {
		r.repository = repository
	}
}

// WithFleetDBClient sets the client communicating with fleet db.
func WithFleetDBClient(client fleetdb.FleetDB) Option {
	return func(r *Routes) {
		r.fleetDBClient = client
	}
}

// EnableServerEnroll enables server enroll API.
func EnableServerEnroll(enable bool) Option {
	return func(r *Routes) {
		r.enableServerEnroll = enable
	}
}

// WithStreamBroker sets the event stream broker.
func WithStreamBroker(broker events.Stream) Option {
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
func WithAuthMiddleware(authMW *ginauth.MultiTokenMiddleware) Option {
	return func(r *Routes) {
		r.authMW = authMW
	}
}

// WithConditionDefinitions sets the supported condition types.
func WithConditionDefinitions(defs rctypes.Definitions) Option {
	return func(r *Routes) {
		r.conditionDefinitions = defs
	}
}

// apiHandler is a function that performs real work for the ConditionOrc API
type apiHandler func(c *gin.Context) (int, *v1types.ServerResponse)

// wrapAPICall wraps a conditionorc routine that does work with some prometheus
// metrics collection and returns a gin.HandlerFunc so the middleware can execute
// directly
func wrapAPICall(fn apiHandler) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		start := time.Now()
		endpoint := ctx.FullPath()
		responseCode, obj := fn(ctx)
		ctx.JSON(responseCode, obj)
		metrics.APICallEpilog(start, endpoint, responseCode)
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

func (r *Routes) composeAuthHandler(scopes []string) gin.HandlerFunc {
	if r.authMW == nil {
		return ginNoOp
	}
	return r.authMW.AuthRequired(scopes)
}

func (r *Routes) Routes(g *gin.RouterGroup) {
	if r.enableServerEnroll {
		serverEnroll := g.Group("/serverEnroll")
		serverEnroll.POST("/:uuid", r.composeAuthHandler(createScopes("server")), wrapAPICall(r.serverEnroll))
		serverDelete := g.Group("/serverDelete")
		serverDelete.DELETE("/:uuid", r.composeAuthHandler(createScopes("server")), wrapAPICall(r.serverDelete))
		// Create a new server ID when uuid is not provided.
		serverEnroll.POST("/", r.composeAuthHandler(createScopes("server-enroll")), wrapAPICall(r.serverEnroll))
	}

	servers := g.Group("/servers/:uuid")
	{
		// /servers/:uuid/condition/:conditionKind
		serverConditionBySlug := servers.Group("/condition")

		// List condition on a server.
		serverConditionBySlug.GET("/:conditionKind",
			r.composeAuthHandler(readScopes("condition")),
			wrapAPICall(r.serverConditionGet))

		// Create a condition on a server.
		// XXX: refactor me! see comments
		serverConditionBySlug.POST("/:conditionKind",
			r.composeAuthHandler(createScopes("condition")),
			wrapAPICall(r.serverConditionCreate))
	}
}

func createScopes(items ...string) []string {
	s := []string{"write", "create"}
	for _, i := range items {
		s = append(s, fmt.Sprintf("create:%s", i))
	}

	return s
}

func readScopes(items ...string) []string {
	s := []string{"read"}
	for _, i := range items {
		s = append(s, fmt.Sprintf("read:%s", i))
	}

	return s
}
