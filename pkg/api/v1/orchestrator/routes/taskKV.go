package routes

import (
	"context"
	"time"

	"github.com/google/uuid"
	rctypes "github.com/metal-toolbox/rivets/condition"
	"github.com/metal-toolbox/rivets/events"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	errPublishTask = errors.New("error in condition task publish")
	errNoTask      = errors.New("no task available")
	errQueryTask   = errors.New("error in condition task query")
)

type taskKV interface {
	get(
		ctx context.Context,
		conditionKind rctypes.Kind,
		conditionID,
		serverID uuid.UUID,
	) (*rctypes.Task[any, any], error)
	publish(
		ctx context.Context,
		serverID,
		conditionID string,
		conditionKind rctypes.Kind,
		task *rctypes.Task[any, any],
		create,
		onlyTimestamp bool,
	) error
}

// implements the taskKV interface
type taskKVImpl struct {
	logger       *logrus.Logger
	kv           nats.KeyValue
	facilityCode string
}

func initTaskKVImpl(facilityCode string, l *logrus.Logger, stream events.Stream) (taskKV, error) {
	// Note: No CreateOrBindCall is made to the bucket here,
	// since we expect that the Orchestrator or other controllers will create this bucket
	// before any http based controller shows up.
	//
	// If this trips over nats.ErrBucketNotFound, then lets add the CreateOrBind method
	njs := stream.(*events.NatsJetstream)

	handle, err := events.AsNatsJetStreamContext(njs).KeyValue(rctypes.TaskKVRepositoryBucket)
	if err != nil {
		return nil, err
	}

	return &taskKVImpl{
		logger:       l,
		kv:           handle,
		facilityCode: facilityCode,
	}, nil
}

func (t *taskKVImpl) get(ctx context.Context, conditionKind rctypes.Kind, conditionID, serverID uuid.UUID) (*rctypes.Task[any, any], error) {
	_, span := otel.Tracer(pkgName).Start(ctx, "taskKV.get")
	defer span.End()

	key := rctypes.TaskKVRepositoryKey(t.facilityCode, conditionKind, serverID.String())
	currEntry, err := t.kv.Get(key)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			t.logger.WithError(err).WithFields(logrus.Fields{
				"serverID":     serverID,
				"facilityCode": t.facilityCode,
				"conditionID":  conditionID,
				"controllerID": serverID,
				"key":          key,
			}).Debug("Task key not found")

			return nil, errors.Wrap(errNoTask, err.Error())
		}

		span.AddEvent("Task query error",
			trace.WithAttributes(
				attribute.String("controllerID", serverID.String()),
				attribute.String("serverID", serverID.String()),
				attribute.String("conditionID", conditionID.String()),
				attribute.String("error", err.Error()),
				attribute.String("key", key),
			),
		)

		t.logger.WithError(err).WithFields(logrus.Fields{
			"serverID":     serverID,
			"facilityCode": t.facilityCode,
			"conditionID":  conditionID,
			"controllerID": serverID,
			"key":          key,
		}).Warn("Task query error")
		return nil, errors.Wrap(errQueryTask, err.Error())
	}

	return rctypes.TaskFromMessage(currEntry.Value())
}

func (t *taskKVImpl) publish(
	ctx context.Context,
	serverID,
	conditionID string,
	conditionKind rctypes.Kind,
	task *rctypes.Task[any, any],
	create,
	onlyTimestamp bool,
) error {
	_, span := otel.Tracer(pkgName).Start(
		ctx,
		"controller.Publish.KV.Task",
		trace.WithSpanKind(trace.SpanKindConsumer),
	)
	defer span.End()

	key := rctypes.TaskKVRepositoryKey(t.facilityCode, conditionKind, serverID)
	failed := func(err error) error {
		span.AddEvent("Task publish error",
			trace.WithAttributes(
				attribute.String("controllerID", serverID),
				attribute.String("serverID", serverID),
				attribute.String("conditionID", conditionID),
				attribute.String("error", err.Error()),
				attribute.String("key", key),
			),
		)

		t.logger.WithError(err).WithFields(logrus.Fields{
			"serverID":     serverID,
			"facilityCode": t.facilityCode,
			"conditionID":  conditionID,
			"controllerID": serverID,
			"key":          key,
		}).Warn("Task publish error")

		return errors.Wrap(errPublishTask, err.Error())
	}

	// create
	if create {
		task.CreatedAt = time.Now()
		taskJSON, errMarshal := task.Marshal()
		if errMarshal != nil {
			return failed(errMarshal)
		}

		// nolint:ineffassign // false positive
		rev, errCreate := t.kv.Create(key, taskJSON)
		if errCreate != nil {
			return failed(errCreate)
		}

		t.logger.WithFields(logrus.Fields{
			"serverID":     serverID,
			"facilityCode": t.facilityCode,
			"taskID":       conditionID,
			"rev":          rev,
			"key":          key,
			"create":       create,
		}).Trace("Task create published")

		return nil
	}

	// update
	curr, err := t.kv.Get(key)
	if err != nil && !errors.Is(err, nats.ErrKeyNotFound) {
		return failed(err)
	}

	currTask, err := rctypes.TaskFromMessage(curr.Value())
	if err != nil {
		return failed(errors.Wrap(err, "Task object deserialize error"))
	}

	if onlyTimestamp {
		currTask.UpdatedAt = time.Now()
	} else {
		if errUpdate := currTask.Update(task); errUpdate != nil {
			return failed(errUpdate)
		}
	}

	currTaskJSON, err := currTask.Marshal()
	if err != nil {
		return failed(err)
	}

	if _, err := t.kv.Update(key, currTaskJSON, curr.Revision()); err != nil {
		return failed(err)
	}

	t.logger.WithFields(logrus.Fields{
		"serverID":     serverID,
		"facilityCode": t.facilityCode,
		"taskID":       conditionID,
		"rev":          curr.Revision(),
		"key":          key,
		"create":       create,
	}).Trace("Task update published")

	return nil
}
