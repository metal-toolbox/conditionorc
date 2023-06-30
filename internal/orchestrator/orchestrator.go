package orchestrator

import (
	"context"
	"strings"
	"sync"

	"github.com/metal-toolbox/conditionorc/internal/store"
	v1EventHandlers "github.com/metal-toolbox/conditionorc/pkg/api/v1/events"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
	"go.opentelemetry.io/otel"
)

var (
	concurrency         = 10
	ErrPublishEvent     = errors.New("error publishing event")
	ErrInvalidEvent     = errors.New("invalid event message")
	pkgName             = "internal/orchestrator"
	serverServiceOrigin = "serverservice"
	controllersOrigin   = "controllers"
	defaultOrigin       = "default"
)

// Orchestrator type holds attributes of the condition orchestrator service
type Orchestrator struct {
	// Logger is the app logger
	logger        *logrus.Logger
	syncWG        *sync.WaitGroup
	listenAddress string
	dispatched    int32
	concurrency   int
	repository    store.Repository
	streamBroker  events.Stream
	eventHandler  *v1EventHandlers.Handler
	statusKV      bool
	replicaCount  int
}

// Option type sets a parameter on the Orchestrator type.
type Option func(*Orchestrator)

// WithStore sets the storage repository on the Orchestrator type.
func WithStore(repository store.Repository) Option {
	return func(o *Orchestrator) {
		o.repository = repository
	}
}

// WithStreamBroker sets the event stream broker on the Orchestrator type.
func WithStreamBroker(broker events.Stream) Option {
	return func(o *Orchestrator) {
		o.streamBroker = broker
	}
}

// WithLogger sets the logger on the Orchestrator type.
func WithLogger(logger *logrus.Logger) Option {
	return func(o *Orchestrator) {
		o.logger = logger
	}
}

// WithListenAddress sets the Orchestrator listen address - for health checks.
func WithListenAddress(addr string) Option {
	return func(o *Orchestrator) {
		o.listenAddress = addr
	}
}

// WithConcurrency sets the Orchestrator event concurrency, defaults to 1.
func WithConcurrency(c int) Option {
	return func(o *Orchestrator) {
		o.concurrency = c
	}
}

// WithStatusKV sets the Orchestrator to use a KV status update mechanism instead of
// subscribing to update channels
func WithStatusKV() Option {
	return func(o *Orchestrator) {
		o.statusKV = true
	}
}

// WithReplicas sets the number of replicas we'll use when instaintiating the NATS
// liveness and status KV buckets. This is only used in the rare case when the buckets
// do no already exist (e.g. when operating in the sandbox environment).
func WithReplicas(c int) Option {
	return func(o *Orchestrator) {
		o.replicaCount = c
	}
}

// New returns a new orchestrator service with the given options set.
func New(opts ...Option) *Orchestrator {
	o := &Orchestrator{concurrency: concurrency, syncWG: &sync.WaitGroup{}}

	for _, opt := range opts {
		opt(o)
	}

	o.eventHandler = v1EventHandlers.NewHandler(
		o.repository,
		o.streamBroker,
		o.logger,
	)

	return o
}

// Run runs the orchestrator which listens for events to action.
func (o *Orchestrator) Run(ctx context.Context) {
	o.logger.Info("running orchestrator")
	o.startWorkerLivenessCheckin(ctx)
	o.startUpdateListener(ctx)
	o.startEventListener(ctx)

	<-ctx.Done()
	o.streamBroker.Close()
	o.logger.Info("orchestrator shut down")
}

//nolint:gomnd,gocritic // shut it
func findSubjectOrigin(subject string) string {
	// XXX: ContainerOrc subjects from Server-Service follow a subject convention of
	// "com.hollow.sh.serverservice.events.target.action". For example, a server
	// inventory request would be 'com.hollow.sh.serverservice.events.server.create'
	// XXX: ugh, why is inventory 'create'?
	// Incoming messages from controllers have a subject of 'com.hollow.sh.controllers.responses'

	subjectElements := strings.Split(subject, ".")
	origin := defaultOrigin
	if len(subjectElements) > 3 {
		origin = subjectElements[3]
	}
	return origin
}

func (o *Orchestrator) processEvent(ctx context.Context, event events.Message) {
	// extract parent trace context from the event if any.
	ctx = event.ExtractOtelTraceContext(ctx)
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "processEvent")
	defer span.End()

	// route the message processing based on the subject

	switch findSubjectOrigin(event.Subject()) {
	case serverServiceOrigin:
		o.eventHandler.ServerserviceEvent(otelCtx, event)
	case controllersOrigin:
		o.eventHandler.ControllerEvent(otelCtx, event)
	default:
		// how did we get a message delivered on a subject that we're not subscribed to?
		o.ackEvent(event, errors.Wrap(ErrInvalidEvent, "msg with unknown subject-pattern ignored"))
	}
}

func (o *Orchestrator) ackEvent(event events.Message, err error) {
	if err != nil {
		// attempt to format the event as JSON
		// so its easier to read in the logs.
		logFields := logrus.Fields{"event_subject": event.Subject()}

		// TODO: add metrics for dropped events
		o.logger.WithError(err).WithFields(logFields).Warn("event with error ack'ed")
	}

	if err := event.Ack(); err != nil {
		o.logger.WithError(err).Warn("error Ack'ing event")
	}
}
