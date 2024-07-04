package routes

import (
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/metal-toolbox/conditionorc/internal/fleetdb"
	"github.com/metal-toolbox/conditionorc/internal/metrics"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/metal-toolbox/rivets/events"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/ginauth"

	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/orchestrator/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
)

const (
	PathPrefix = "/api/v1"
)

var pkgName = "pkg/api/v1/orchestrator/routes"

var ginNoOp = func(_ *gin.Context) {
}

// Routes type sets up the conditionorc API  router routes.
type Routes struct {
	authMW               *ginauth.MultiTokenMiddleware
	fleetDBClient        fleetdb.FleetDB
	repository           store.Repository
	streamBroker         events.Stream
	facilityCode         string
	streamSubjectPrefix  string
	conditionDefinitions rctypes.Definitions
	logger               *logrus.Logger
	statusValueKV        statusValueKV
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

// WithStreamBroker sets the event stream broker.
func WithStreamBroker(broker events.Stream, streamSubjectPrefix string) Option {
	return func(r *Routes) {
		r.streamBroker = broker
		r.streamSubjectPrefix = streamSubjectPrefix
	}
}

func WithFacilityCode(fc string) Option {
	return func(r *Routes) {
		r.facilityCode = fc
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

func WithStatusKVPublisher(p statusValueKV) Option {
	return func(r *Routes) {
		r.statusValueKV = p
	}
}

// apiHandler is a function that performs real work for the Orchestrator API
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

// NewRoutes returns a new Orchestrator API routes with handlers registered.
func NewRoutes(options ...Option) (*Routes, error) {
	routes := &Routes{}

	for _, opt := range options {
		opt(routes)
	}

	supported := []string{}

	if routes.statusValueKV == nil {
		routes.statusValueKV = initStatusValueKV()
	}

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

// Routes returns routes for the Orchestrator API service.
func (r *Routes) Routes(g *gin.RouterGroup) {
	controller := g.Group("/servers/:uuid")

	controller.PUT(
		"/condition-status/:conditionKind/:conditionID",
		r.composeAuthHandler(createScopes("statusUpdate")),
		wrapAPICall(r.conditionStatusUpdate),
	)
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
