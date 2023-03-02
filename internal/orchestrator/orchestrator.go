package orchestrator

import (
	"context"
	"time"

	"github.com/metal-toolbox/conditionorc/internal/events"
	"github.com/metal-toolbox/conditionorc/internal/store"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/sirupsen/logrus"
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
	tick := time.NewTicker(10 * time.Second).C

	for {
		select {
		case <-tick:
			o.publishPending(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (o *Orchestrator) publishPending(ctx context.Context) {
	for _, kind := range ptypes.ConditionKinds() {
		o.repository.ListServersWithCondition(ctx, kind, ptypes.Pending)
	}
}
