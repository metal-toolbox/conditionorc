package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/metal-toolbox/conditionorc/internal/status"
	rctypes "github.com/metal-toolbox/rivets/condition"
	jsmapi "github.com/nats-io/jsm.go/api"
	jsadv "github.com/nats-io/jsm.go/api/jetstream/advisory"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
)

var (
	dlqOnce      sync.Once
	streamName   = "DLQ"
	consumerName = "DLQ-subs"
	//dlqSubject   = "$JS.EVENT.ADVISORY.CONSUMER.MAX_DELIVERIES.controllers.>"
)

// startDLQListener sets up a stream and a consumer for the NATS JetStream and
// then starts a go-routine to listen for messages on the "failed max deliveries"
// subject.
func (o *Orchestrator) startDLQListener(ctx context.Context) {
	dlqOnce.Do(func() {
		js, ok := o.streamBroker.(*events.NatsJetstream)
		if !ok {
			o.logger.Fatal("dlq functions are only supported on NATS")
		}

		le := o.logger.WithFields(logrus.Fields{
			"stream.name":   streamName,
			"consumer.name": consumerName,
			//"nats.subject":  dlqSubject,
		})

		// NATS names their "JetStream complete API interface" a "JetStreamContext" -- no
		// relation to context.Context
		jsCtx := events.AsNatsJetStreamContext(js)

		if err := createDLQStream(ctx, jsCtx); err != nil {
			le.WithError(err).Fatal("creating dlq stream")
			return
		}

		if err := createDLQConsumer(ctx, jsCtx); err != nil {
			le.WithError(err).Fatal("creating dlq consumer")
			return
		}

		/*sub, err := jsCtx.PullSubscribe(dlqSubject, consumerName, nats.AckExplicit())
		if err != nil {
			le.WithError(err).Fatal("subscribe to dlq")
			return
		}*/

		//go o.dlqSubscriberRoutine(ctx, jsCtx, sub)
	})

	return
}

// create a stream to read the NATS advisory subject when MAX_DELIVERIES is exceeded.
// Note that the nats.JetStreamManager is the same object as the nats.JetStreamContext
// in StartListener. The difference in terminology stems from NATS using a
// "sub-interface" to describe functions specific to managing streams and consumers.
func createDLQStream(ctx context.Context, mgr nats.JetStreamManager) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err := mgr.StreamInfo(streamName, nats.Context(queryCtx))
	switch err {
	case nil:
		return nil
	case nats.ErrStreamNotFound:
	default:
		return errors.Wrap(err, "NATS StreamInfo")
	}

	cfg := nats.StreamConfig{
		Name: streamName,
		Subjects: []string{
			"$JS.EVENT.ADVISORY.CONSUMER.MAX_DELIVERIES.controllers.>",
		},
		Description: "dead letter exchange stream for controllers",
		Retention:   nats.WorkQueuePolicy,
	}

	_, err = mgr.AddStream(&cfg)
	if err != nil {
		return errors.Wrap(err, "NATS AddStream")
	}

	return nil
}

// create a dedicated DLQ consumer
func createDLQConsumer(ctx context.Context, mgr nats.JetStreamManager) error {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err := mgr.ConsumerInfo(streamName, consumerName, nats.Context(queryCtx))
	switch err {
	case nil:
		return nil
	case nats.ErrConsumerNotFound:
	default:
		return errors.Wrap(err, "NATS ConsumerInfo")
	}

	cfg := nats.ConsumerConfig{
		Durable:       consumerName,
		Name:          consumerName,
		AckPolicy:     nats.AckExplicitPolicy,
		DeliverPolicy: nats.DeliverAllPolicy,
		FilterSubject: "$JS.EVENT.ADVISORY.CONSUMER.MAX_DELIVERIES.controllers.>",
	}
	_, err = mgr.AddConsumer(streamName, &cfg)
	if err != nil {
		return errors.Wrap(err, "NATS AddConsumer")
	}

	return nil
}

func (o *Orchestrator) dlqSubscriberRoutine(ctx context.Context, jsCtx nats.JetStreamContext, sub *nats.Subscription) {
	loop := true
	for loop {
		select {
		case <-ctx.Done():
			o.logger.Info("DLQ subscriber signaled to exit")
			loop = false
			continue
		default:
		}
		fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		batch, err := sub.FetchBatch(10, nats.Context(fetchCtx))
		if err != nil {
			o.logger.WithError(err).Warn("retrieving messages from DLQ subscription")
		}
		for msg := range batch.Messages() {
			o.processDLQMessage(ctx, jsCtx, msg)
		}
		cancel()
	}
}

// processDLQMessage will Ack or Term the message parameter as needed.
// XXX: msg here is the JetStream system message on the DLQ, not the undeliverable
// condition message that the controller bobbled. It needs to be acknowledged
// separate from deleting the condition message.
func (o *Orchestrator) processDLQMessage(ctx context.Context, js nats.JetStreamContext, msg *nats.Msg) {
	if msg == nil {
		return
	}

	if err := ctx.Err(); err != nil {
		return
	}

	le := o.logger.WithField("subject", msg.Subject)

	schema, m, err := jsmapi.ParseMessage(msg.Data)
	if err != nil {
		le.WithError(err).Warn("parsing jetstream advisory")
		msg.Term()
		return
	}

	cde, ok := m.(*jsadv.ConsumerDeliveryExceededAdvisoryV1)
	if !ok {
		le.WithField("schema", schema).Warn("unexpected JetStream advisory message")
		msg.Term()
		return
	}

	// at this point, regardless of whether we can update this event successfully, we
	// must remove it from the NATS jetstream
	le = le.WithFields(logrus.Fields{
		"stream":   cde.Stream,
		"sequence": cde.StreamSeq,
	})

	defer func() {
		err := js.DeleteMsg(cde.Stream, cde.StreamSeq)
		if err != nil {
			le.WithError(err).Warn("delete undeliverable message")
		}
		return
	}()

	conditionMsg, err := js.GetMsg(cde.Stream, cde.StreamSeq)
	if err != nil {
		le.WithError(err).Warn("get undeliverable message from stream")
		msg.Nak() // ?? Could Term here as well, but this might be transient
		return
	}

	var cond rctypes.Condition
	err = json.Unmarshal(conditionMsg.Data, &cond)
	if err != nil {
		le.WithError(err).Warn("unmarshaling undeliverable payload")
		msg.Term()
		return
	}

	// update the condition as though we were a controller: via the status KV store.
	// This allows us to hook into notifications and any other side-effects that
	// are prompted by a state change.
	kvApi, err := status.GetConditionKV(cond.Kind)
	if err != nil {
		le.WithError(err).WithField("condition.kind", cond.Kind).Warn("unable to retrieve status KV bucket")
		msg.Term()
		return
	}

	kve, err := status.GetSingleCondition(cond.Kind, o.facility, cond.ID.String())
	if err != nil {
		// this might be as simple as: the controller crashed before it could even
		// write status
		le.WithError(err).Warn("retrieving condition status")
		msg.Term()
		return
	}

	sv, err := rctypes.UnmarshalStatusValue(kve.Value())
	sv.UpdatedAt = time.Now()
	sv.State = string(rctypes.Failed)
	sv.Status = json.RawMessage(`{ "msg": "failed by orchestrator due to unprocessable condition" }`)

	_, err = kvApi.Update(kve.Key(), sv.MustBytes(), kve.Revision())
	if err != nil {
		le.WithError(err).WithFields(logrus.Fields{
			"condition.id":   cond.ID.String(),
			"condition.kind": cond.Kind,
			"revision":       fmt.Sprintf("%d", kve.Revision()),
		}).Warn("writing failed status to status KV")
		msg.Term()
	}

	msg.Ack()
	return
}
