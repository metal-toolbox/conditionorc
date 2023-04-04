package orchestrator

import (
	"context"
	"fmt"

	"github.com/davecgh/go-spew/spew"
	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/store"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
	"go.infratographer.com/x/pubsubx"
	"go.infratographer.com/x/urnx"
)

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
		o.handleHollowEvent(ctx, data, urn)
	case "hollow-controllers":
		o.handleHollowControllerEvent(ctx, data, urn)
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
	fmt.Println("I don't handle replies from controllers as yet.")
	spew.Dump(urn)
}

func (o *Orchestrator) handleHollowEvent(ctx context.Context, event *pubsubx.Message, urn *urnx.URN) {
	switch event.EventType {
	case string(events.Create):
		condition := &ptypes.Condition{
			Kind:      ptypes.InventoryOutofband, // TODO: change, once the condition types package is moved into a shared package
			State:     ptypes.Pending,
			Exclusive: false,
		}

		if err := o.repository.Create(ctx, urn.ResourceID, condition); err != nil {
			o.logger.WithFields(
				logrus.Fields{"err": err.Error()},
			).Error("error creating condition on server")

			return
		}

		o.publishCondition(ctx, urn.ResourceID, condition)
	default:
		o.logger.WithFields(
			logrus.Fields{"eventType": event.EventType, "urn ns": urn.Namespace},
		).Error("msg with unknown eventType ignored")
	}
}

func (o *Orchestrator) publishCondition(ctx context.Context, serverID uuid.UUID, condition *ptypes.Condition) {
	if o.streamBroker == nil {
		o.logger.Warn("Event publish skipped, not connected to stream borker")
		return
	}

	if err := o.streamBroker.PublishAsyncWithContext(
		ctx,
		events.ResourceType(ptypes.ServerResourceType),
		events.EventType(ptypes.InventoryOutofband),
		serverID.String(),
		condition,
	); err != nil {
		o.logger.WithFields(
			logrus.Fields{"err": err.Error()},
		).Error("error publishing condition event")
	}

	o.logger.WithFields(
		logrus.Fields{
			"condition": condition.Kind,
		},
	).Info("published condition event")
}
