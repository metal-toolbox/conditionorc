package routes

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	rctypes "github.com/metal-toolbox/rivets/condition"
	"github.com/metal-toolbox/rivets/events"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

const (
	conditionPopTimeout = 5 * time.Second
)

var (
	errNoConditionInQueue = errors.New("no condition in queue")
	errQueueFetch         = errors.New("error in fetching condition from queue")
)

type conditionJetstream interface {
	pop(ctx context.Context, conditionKind rctypes.Kind, serverID uuid.UUID) (*rctypes.Condition, error)
}

// implements the conditionJetstream interface
type conditionRetriever struct {
	streamSubjectPrefix string
	facilityCode        string
	logger              *logrus.Logger
	stream              events.Stream
}

func initConditionJetstream(streamSubjectPrefix, facilityCode string, l *logrus.Logger, stream events.Stream) conditionJetstream {
	return &conditionRetriever{
		streamSubjectPrefix: streamSubjectPrefix,
		facilityCode:        facilityCode,
		logger:              l,
		stream:              stream,
	}
}

// pop registers the condition with the matching kind and serverID from the NATS Jetstream.
func (c *conditionRetriever) pop(ctx context.Context, conditionKind rctypes.Kind, serverID uuid.UUID) (*rctypes.Condition, error) {
	_, span := otel.Tracer(pkgName).Start(ctx, "js.pop")
	span.SetAttributes(
		attribute.KeyValue{Key: "serverId", Value: attribute.StringValue(serverID.String())},
		attribute.KeyValue{Key: "conditionKind", Value: attribute.StringValue(string(conditionKind))},
	)
	defer span.End()

	// TODO: At somepoint lets get rid of this ridiculously long stream subject prefix
	// com.hollow.sh.controllers.commands.sandbox.servers.firmwareInstallInband.ede81024-f62a-4288-8730-3fab8cceab78
	subject := strings.Join(
		[]string{
			c.streamSubjectPrefix,                                // com.hollow.sh.controllers.commands
			rctypes.StreamSubject(c.facilityCode, conditionKind), // sandbox.servers.firmwareInstallInband
			serverID.String(),                                    // ede81024-f62a-4288-8730-3fab8cceab7
		},
		".",
	)

	jctx := events.AsNatsJetStreamContext(c.stream.(*events.NatsJetstream))
	subscription, err := jctx.PullSubscribe(
		subject,
		"",
		nats.PullMaxWaiting(1),
	)
	if err != nil {
		return nil, errors.Wrap(errQueueFetch, err.Error()+": subscribe error")
	}

	// This subject with the serverID suffix is expected to hold a single message in queue.
	// If theres more, we abort, or we risk returning a condition queued previously.
	//
	// Theres no reliable way to determine how many messages are in queue for this subject.
	// I've tried subscription.ConsumerInfo.NumPending but the value indicates
	// all the messages queued so far and isn't reliable.
	//
	// As a workaround we attempt to fetch upto 2 items from the backlog
	// if theres more than one we return an error for the Operators to clean up.
	msgBatch, err := subscription.FetchBatch(2, nats.MaxWait(conditionPopTimeout))
	if err != nil {
		// other possible errors
		//	nats.ErrBadSubscription
		//	nats.ErrNoResponders
		//	nats.ErrConnectionClosed
		return nil, errors.Wrap(errQueueFetch, err.Error())
	}

	// TODO: select with context so this doesn't block
	msgs := []*nats.Msg{}
	for msg := range msgBatch.Messages() {
		if len(msg.Data) == 0 {
			continue
		}

		msgs = append(msgs, msg)
	}

	if len(msgs) == 0 {
		return nil, errNoConditionInQueue
	}

	if len(msgs) > 1 {
		errMsg := "found multiple messages in queue, expected one, manual cleanup required"
		c.logger.WithFields(logrus.Fields{"count": len(msgs), "subject": subject}).Error(errMsg)
		return nil, errors.Wrap(errQueueFetch, errMsg)
	}

	// ack message right away since we don't want redeliveries on failure
	if err := msgs[0].Ack(); err != nil {
		c.logger.WithError(err).Warn("msg ack returned error")
	}

	return conditionFromNatsMsg(msgs[0])
}

func conditionFromNatsMsg(m *nats.Msg) (*rctypes.Condition, error) {
	errConditionDeserialize := errors.New("unable to deserialize condition")
	if m.Data == nil {
		return nil, errors.Wrap(errConditionDeserialize, "data field empty")
	}

	cond := &rctypes.Condition{}
	if err := json.Unmarshal(m.Data, cond); err != nil {
		return nil, errors.Wrap(errConditionDeserialize, err.Error())
	}

	return cond, nil
}
