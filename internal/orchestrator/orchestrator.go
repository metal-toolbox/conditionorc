package orchestrator

import (
	"context"

	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/metrics"
	"github.com/metal-toolbox/conditionorc/internal/store"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
	"go.infratographer.com/x/pubsubx"
	"go.infratographer.com/x/urnx"
	"go.opentelemetry.io/otel"
)

var (
	ErrPublishEvent = errors.New("error publishing event")
	pkgName         = "internal/orchestrator"
)

func eventsError() {
	metrics.DependencyError("events")
}

// Orchestrator type holds attributes of the condition orchestrator service
type Orchestrator struct {
	// Logger is the app logger
	logger        *logrus.Logger
	listenAddress string
	repository    store.Repository
	streamBroker  events.Stream
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

	switch urn.Namespace {
	case "hollow":
		o.handleHollowEvent(otelCtx, data, urn)
	case "hollow-controllers":
		o.handleHollowControllerEvent(otelCtx, data, urn)
	default:
		if errAck := msg.Ack(); errAck != nil {
			o.logger.Warn(errAck)
		}

		o.logger.WithFields(
			logrus.Fields{"err": err.Error(), "urn ns": urn.Namespace},
		).Error("msg with unknown URN namespace ignored")
	}
}

func (o *Orchestrator) handleHollowControllerEvent(ctx context.Context, event *pubsubx.Message, urn *urnx.URN) {
	_, span := otel.Tracer(pkgName).Start(ctx, "handleHollowControllerEvent")
	defer span.End()
	o.logger.WithFields(
		logrus.Fields{"source": event.Source, "resource": urn.ResourceType},
	).Debug("controller reply handler not implemented, event dropped")
}

func (o *Orchestrator) handleHollowEvent(ctx context.Context, event *pubsubx.Message, urn *urnx.URN) {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "handleHollowEvent")
	defer span.End()
	switch event.EventType {
	case string(events.Create):
		condition := &ptypes.Condition{
			Kind:      ptypes.InventoryOutofband, // TODO: change, once the condition types package is moved into a shared package
			State:     ptypes.Pending,
			Exclusive: false,
		}

		if err := o.repository.Create(otelCtx, urn.ResourceID, condition); err != nil {
			o.logger.WithFields(
				logrus.Fields{"err": err.Error()},
			).Error("error creating condition on server")
			eventsError()
			return
		}

		if errPublish := o.publishCondition(otelCtx, urn.ResourceID, condition); errPublish != nil {
			o.logger.WithFields(
				logrus.Fields{"err": errPublish.Error()},
			).Error("condition publish returned an error")
			eventsError()
			return
		}

		o.logger.WithFields(
			logrus.Fields{
				"condition": condition.Kind,
			},
		).Info("condition published")

	default:
		o.logger.WithFields(
			logrus.Fields{"eventType": event.EventType, "urn ns": urn.Namespace},
		).Error("msg with unknown eventType ignored")
	}
}

func (o *Orchestrator) publishCondition(ctx context.Context, serverID uuid.UUID, condition *ptypes.Condition) error {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "publishCondition")
	defer span.End()
	if o.streamBroker == nil {
		return errors.Wrap(ErrPublishEvent, "not connected to event stream")
	}

	if err := o.streamBroker.PublishAsyncWithContext(
		otelCtx,
		events.ResourceType(ptypes.ServerResourceType),
		events.EventType(ptypes.InventoryOutofband), // XXX: is this right?
		serverID.String(),
		condition,
	); err != nil {
		o.logger.WithFields(logrus.Fields{
			"error":       err,
			"conditionID": condition.ID.String(),
			"kind":        condition.Kind,
		}).Warn("error publishing condition")
		eventsError()
		return errors.Wrap(ErrPublishEvent, err.Error())
	}

	return nil
}
