package orchestrator

import (
	"context"

	"github.com/metal-toolbox/conditionorc/internal/store"
	v1EventHandlers "github.com/metal-toolbox/conditionorc/pkg/api/v1/events"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
	"go.opentelemetry.io/otel"
)

var (
	ErrPublishEvent = errors.New("error publishing event")
	pkgName         = "internal/orchestrator"
)

// Orchestrator type holds attributes of the condition orchestrator service
type Orchestrator struct {
	// Logger is the app logger
	logger        *logrus.Logger
	listenAddress string
	repository    store.Repository
	streamBroker  events.Stream
	eventHandler  *v1EventHandlers.Handler
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

// New returns a new orchestrator service with the given options set.
func New(opts ...Option) *Orchestrator {
	o := &Orchestrator{}

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
	eventsCh, err := o.streamBroker.Subscribe(ctx)
	if err != nil {
		o.logger.Fatal(err)
	}

	o.logger.Info("listening for events ...")

	for {
		select {
		case <-ctx.Done():
			o.streamBroker.Close()
			return
		case msg := <-eventsCh:
			o.processMsg(ctx, msg)
		}
	}
}

func (o *Orchestrator) processMsg(ctx context.Context, msg events.Message) {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "processMsg")
	defer span.End()

	data, err := msg.Data()
	if err != nil {
		o.logger.WithFields(
			logrus.Fields{"err": err.Error(), "subject": msg.Subject()},
		).Error("data unpack error")

		return
	}

	urn, err := msg.SubjectURN(data)
	if err != nil {
		o.logger.WithFields(
			logrus.Fields{"err": err.Error(), "subject": msg.Subject()},
		).Error("error parsing subject URN in msg")

		return
	}

	if urn.ResourceType != ptypes.ServerResourceType {
		o.logger.WithFields(
			logrus.Fields{"urn ns": urn.Namespace, "resourceType": urn.ResourceType},
		).Error("msg with unknown ResourceType in URN")

		return
	}

	streamEvent := &ptypes.StreamEvent{URN: urn, Event: msg, Data: data}

	switch ptypes.EventUrnNamespace(urn.Namespace) {
	case ptypes.ServerserviceNamespace:
		o.eventHandler.ServerserviceEvent(otelCtx, streamEvent)
	case ptypes.ControllerUrnNamespace:
		o.eventHandler.ControllerEvent(otelCtx, streamEvent)
	default:
		if errAck := msg.Ack(); errAck != nil {
			o.logger.Warn(errAck)
		}

		o.logger.WithFields(
			logrus.Fields{"err": err.Error(), "urn ns": urn.Namespace},
		).Error("msg with unknown URN namespace ignored")
	}
}
