package orchestrator

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
)

var (
	updOnce             sync.Once
	fetchEventsInterval = 1 * time.Second
)

func (o *Orchestrator) startUpdateListener(ctx context.Context) {
	updOnce.Do(func() {
		if err := ctx.Err(); err != nil {
			o.logger.WithError(err).Info("bypassing update listener start on context error")
			return
		}
		go o.updateListener(ctx)
	})
}

func (o *Orchestrator) updateListener(ctx context.Context) {
	o.logger.Info("listening for updates")
	tickerFetchEvents := time.NewTicker(fetchEventsInterval).C
	for {
		select {
		case <-ctx.Done():
			o.logger.Info("stopping update listener")
			return
		// retrieve and process events sent by controllers.
		case <-tickerFetchEvents:
			if o.concurrencyLimit() {
				continue
			}
			o.pullEvents(ctx)
		}
	}
}

func (o *Orchestrator) concurrencyLimit() bool {
	return int(o.dispatched) >= o.concurrency
}

func (o *Orchestrator) pullEvents(ctx context.Context) {
	if err := ctx.Err(); err != nil {
		o.logger.WithError(err).Info("exit on context error")
		return
	}
	// XXX: consider having a separate context for message retrieval
	msgs, err := o.streamBroker.PullMsg(ctx, 1)
	if err != nil {
		o.logger.WithFields(
			logrus.Fields{"err": err.Error()},
		).Trace("error fetching work")
	}

	for _, msg := range msgs {
		/*if ctx.Err() != nil || o.concurrencyLimit() {
			o.eventNak(msg)

			return
		}*/

		// spawn msg process handler
		o.syncWG.Add(1)

		go func(msg events.Message) {
			defer o.syncWG.Done()

			atomic.AddInt32(&o.dispatched, 1)
			defer atomic.AddInt32(&o.dispatched, -1)

			o.processEvent(ctx, msg)
		}(msg)
	}
}
