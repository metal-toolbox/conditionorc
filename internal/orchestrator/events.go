package orchestrator

import (
	"context"
	"sync"
)

var evtOnce sync.Once

func (o *Orchestrator) startEventListener(ctx context.Context) {
	evtOnce.Do(func() {
		if err := ctx.Err(); err != nil {
			o.logger.WithError(err).Info("bypassing event listener start on context error")
			return
		}
		go o.eventListener(ctx)
	})
}

func (o *Orchestrator) eventListener(ctx context.Context) {
	o.logger.Info("listening for events")

	eventsCh, err := o.streamBroker.Subscribe(ctx)
	if err != nil {
		o.logger.Fatal(err)
	}

	for {
		select {
		case <-ctx.Done():
			o.logger.Info("stopping event listener")
			return
		// eventsCh receives events from push based subscriptions, in this case serverservice.
		case msg := <-eventsCh:
			o.processEvent(ctx, msg)
		}
	}
}
