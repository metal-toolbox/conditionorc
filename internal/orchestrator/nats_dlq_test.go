package orchestrator

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	rctypes "github.com/metal-toolbox/rivets/condition"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
)

// XXX: being in the orchestrator module, these tests use the infrastructure in
// updates_test.go for creating and using a test NATS instance.

func TestDLQ(t *testing.T) {
	t.Parallel()

	ctx := context.TODO()

	err := createDLQStream(ctx, js)
	require.NoError(t, err)

	// do it again, it's fine
	err = createDLQStream(ctx, js)
	require.NoError(t, err)

	si, err := js.StreamInfo("DLQ")
	require.NoError(t, err)
	require.NotNil(t, si)
	require.Equal(t, "DLQ", si.Config.Name)
	require.Equal(t, 1, len(si.Config.Subjects))
	require.Equal(t, "$JS.EVENT.ADVISORY.CONSUMER.MAX_DELIVERIES.controllers.>", si.Config.Subjects[0])

	// do it with the DLQ Consumer
	err = createDLQConsumer(ctx, js)
	require.NoError(t, err)

	err = createDLQConsumer(ctx, js)
	require.NoError(t, err)

	ci, err := js.ConsumerInfo("DLQ", "DLQ-subs")
	require.NoError(t, err)
	require.NotNil(t, ci)

	// the following is a little artificial but it serves to exercise some of the code

	// first create a stream and consumer for our controller
	streamCfg := nats.StreamConfig{
		Name: "controllers",
		Subjects: []string{
			"dlqtest.>",
		},
		Description: "dlq test stream",
		Retention:   nats.WorkQueuePolicy,
	}

	_, err = js.AddStream(&streamCfg)
	require.NoError(t, err, "setting up controller stream")

	consumerCfg := nats.ConsumerConfig{
		Durable:       "dlqtest-consumer",
		Name:          "dlqtest-consumer",
		FilterSubject: "dlqtest.controllers.>",
		AckPolicy:     nats.AckExplicitPolicy,
		MaxDeliver:    1,
	}

	_, err = js.AddConsumer("dlqtest-stream", &consumerCfg)
	require.NoError(t, err, "setting up controller consumer")

	// set up a subscriber for our DLQ and capture any message we get from it
	var dlqWg sync.WaitGroup
	dlqWg.Add(1)
	var dlqMsg *nats.Msg
	_, err = js.Subscribe("$JS.EVENT.ADVISORY.CONSUMER.MAX_DELIVERIES.controllers.>", func(m *nats.Msg) {
		dlqMsg = m
		dlqWg.Done()
	})
	require.NoError(t, err)

	// Subscribe as though we were a controller
	var conWg sync.WaitGroup
	conWg.Add(1)
	_, err = js.Subscribe("dlqtest.controllers.>", func(m *nats.Msg) {
		require.Equal(t, "dlqtest.controllers.ghost", m.Subject)
		err := m.Nak()
		require.NoError(t, err, "message NAK")
		conWg.Done()
	})
	require.NoError(t, err)

	condID := uuid.New()

	testCondition := rctypes.Condition{
		ID:    condID,
		State: rctypes.Pending,
		Kind:  rctypes.Kind("bogus"),
	}

	// send this message
	ack, err := js.Publish("dlqtest.controllers.ghost", testCondition.MustBytes())
	require.NoError(t, err, "publishing message")

	// this is an internal assumption-check
	si, err = js.StreamInfo("dlqtest-stream")
	require.NoError(t, err)
	require.Equal(t, uint64(1), si.State.Msgs)
	require.Equal(t, ack.Sequence, si.State.LastSeq)

	// wait for our asynchronous ops
	conWg.Wait()
	dlqWg.Wait()
	require.NotNil(t, dlqMsg)
}
