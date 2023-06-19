package orchestrator

import (
	"context"
	"sync"
	"time"

	"go.hollow.sh/toolbox/events"
	"go.hollow.sh/toolbox/events/registry"

	"github.com/nats-io/nats.go"
)

var (
	livOnce        sync.Once
	checkinCadence = 30 * time.Second
)

// This starts a go-routine to peridocally check in with the NATS kv
func (o *Orchestrator) startWorkerLivenessCheckin(ctx context.Context) {
	livOnce.Do(func() {
		natsJS, ok := o.streamBroker.(*events.NatsJetstream)
		if !ok {
			o.logger.Error("Non-NATS stores are not supported for worker-liveness")
			return
		}

		if err := registry.InitializeActiveControllerRegistry(natsJS); err != nil {
			o.logger.WithError(err).Error("unable to initialize active worker registry")
			return
		}

		go o.checkinRoutine(ctx)
	})
}

func (o *Orchestrator) checkinRoutine(ctx context.Context) {
	me := registry.GetID("condition-orchestrator")
	o.logger.WithField("id", me.String()).Info("worker id assigned")
	if err := registry.RegisterController(me); err != nil {
		o.logger.WithError(err).WithField("id", me.String()).
			Warn("unable to do initial worker liveness registration")
	}

	tick := time.NewTicker(checkinCadence)
	defer tick.Stop()

	var stop bool
	for !stop {
		select {
		case <-tick.C:
			err := registry.ControllerCheckin(me)
			switch err {
			case nil:
			case nats.ErrKeyNotFound: // generally means NATS reaped our entry on TTL
				if err = registry.RegisterController(me); err != nil {
					o.logger.WithError(err).
						WithField("id", me.String()).
						Warn("unable to re-register worker")
				}
			default:
				o.logger.WithError(err).
					WithField("id", me.String()).
					Warn("worker checkin failed")
			}
		case <-ctx.Done():
			o.logger.Info("liveness check-in stopping on done context")
			stop = true
		}
	}
}
