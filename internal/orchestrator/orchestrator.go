package orchestrator

import (
	"context"
	"fmt"
	"time"

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
	streamBroker  events.StreamBroker
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
func WithStreamBroker(broker events.StreamBroker) Option {
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

func New(opts ...Option) *Orchestrator {
	o := &Orchestrator{}

	for _, opt := range opts {
		opt(o)
	}

	return o
}

func (o *Orchestrator) Run(ctx context.Context) {
	var subscribeFailures bool

	tickerFetchWork := time.NewTicker(15 * time.Second).C
	eventsCh, err := o.streamBroker.Subscribe(ctx)
	if err != nil {
		o.logger.Fatal(err)
	}

	pendingTick := time.NewTicker(10 * time.Second).C
	retrySubscribeTick := time.NewTicker(5 * time.Second).C

	o.logger.Info("listening for events ...")
	for {
		select {
		case <-pendingTick:
			o.publishPending(ctx)
		case <-retrySubscribeTick:
			if !subscribeFailures {
				continue
			}
		case <-tickerFetchWork:
			o.fetchWork(ctx, eventsCh)
		case <-ctx.Done():
			o.streamBroker.Close()
			return
		case msg := <-eventsCh:
			o.processMsg(ctx, msg)
		}
	}
}

func (c *Orchestrator) fetchWork(ctx context.Context, msgCh events.MsgCh) {
	msgs, err := c.streamBroker.FetchMsg(ctx, 1)
	if err != nil {
		c.logger.WithFields(
			logrus.Fields{"err": err.Error()},
		).Error("error fetching messages")
	}

	for _, msg := range msgs {
		msgCh <- msg
	}
}

func (o *Orchestrator) publishPending(ctx context.Context) {
	// TODO: list servers with condition with last updated timestamp older than X

	//	for _, kind := range ptypes.ConditionKinds() {
	//		o.repository.ListServersWithCondition(ctx, kind, ptypes.Pending)
	//	}
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

	switch urn.Namespace {
	case "hollow":
		o.handleHollowEvent(ctx, data, urn)
	case "hollow-controllers":
		o.handleHollowControllerEvent(ctx, data, urn)
	}
}

func (o *Orchestrator) handleHollowControllerEvent(ctx context.Context, event *pubsubx.Message, urn *urnx.URN) {
	fmt.Println("Im supposed to do something with this controller event, but not yet.")
	spew.Dump(event)
	spew.Dump(urn)
}

func (o *Orchestrator) handleHollowEvent(ctx context.Context, event *pubsubx.Message, urn *urnx.URN) {
	if urn.ResourceType != "servers" {
		return
	}

	switch event.EventType {
	case string(events.Create):
		condition := &ptypes.Condition{
			Kind:      ptypes.InventoryOutofband,
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
	}
}

func (o *Orchestrator) publishCondition(ctx context.Context, serverID uuid.UUID, condition *ptypes.Condition) {
	if o.streamBroker == nil {
		o.logger.Warn("Event publish skipped, not connected to stream borker")
		return
	}

	if err := o.streamBroker.PublishAsyncWithContext(
		ctx,
		"server",
		events.EventType(ptypes.InventoryOutofband),
		serverID.String(),
		condition,
	); err != nil {
		o.logger.WithFields(
			logrus.Fields{"err": err.Error()},
		).Error("error publishing condition event")
	}
}
