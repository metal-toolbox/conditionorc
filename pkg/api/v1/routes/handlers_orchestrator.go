package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/metrics"
	"github.com/metal-toolbox/conditionorc/internal/status"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/nats-io/nats.go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	"github.com/metal-toolbox/rivets/condition"
	rctypes "github.com/metal-toolbox/rivets/condition"
	rkv "github.com/metal-toolbox/rivets/events/pkg/kv"
	"github.com/metal-toolbox/rivets/events/registry"
)

var (
	errNoConditionInQueue = errors.New("no condition in queue")
	errQueueFetch         = errors.New("error in fetching condition from queue")
	errPublishStatus      = errors.New("error in condition status publish")
	errRegistryCheckin    = errors.New("error in controller check-in")
	errPublishTask        = errors.New("error in condition task publish")
	errNoTask             = errors.New("no task available")
	errQueryTask          = errors.New("error in condition task query")
	errUnmarshalKey       = errors.New("error unmarshal key, value for update")
	errControllerMismatch = errors.New("condition controller mismatch error")
)

const (
	conditionPopTimeout = 5 * time.Second
)

// @Summary ConditionQueuePop
// @Tag Conditions
// @Description Pops a conditions from the NATS Jestream and registers the serverID as a controller.
// @ID conditionQueue
// @Param uuid path string true "Server ID"
// @Param conditionKind path string true "Condition Kind"
// @Accept json
// @Produce json
// @Success 200 {object} v1types.ServerResponse
// Failure 400 {object} v1types.ServerResponse
// Failure 404 {object} v1types.ServerResponse
// Failure 500 {object} v1types.ServerResponse
// @Router /servers/{uuid}/{conditionKind} [get]
func (r *Routes) conditionQueuePop(c *gin.Context) (int, *v1types.ServerResponse) {
	ctx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.conditionQueuePop")
	span.SetAttributes(
		attribute.KeyValue{Key: "serverId", Value: attribute.StringValue(c.Param("uuid"))},
		attribute.KeyValue{Key: "conditionKind", Value: attribute.StringValue(c.Param("conditionKind"))},
	)
	defer span.End()

	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid server id: " + err.Error(),
		}
	}

	conditionKind := rctypes.Kind(c.Param("conditionKind"))
	if !r.conditionKindValid(conditionKind) {
		r.logger.WithFields(logrus.Fields{
			"kind": conditionKind,
		}).Info("unsupported condition kind")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "unsupported condition kind: " + string(conditionKind),
		}
	}

	conditionRecord, err := r.repository.Get(ctx, serverID)
	if err != nil {
		if errors.Is(err, store.ErrConditionNotFound) {
			return http.StatusNotFound, &v1types.ServerResponse{
				Message: "condition not found for server",
			}
		}

		r.logger.WithError(err).Info("condition record query error")

		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "condition lookup: " + err.Error(),
		}
	}

	var activeConditionExists bool
	for _, cond := range conditionRecord.Conditions {
		if cond.Kind == conditionKind && !rctypes.StateIsComplete(cond.State) {
			activeConditionExists = true
		}
	}

	if !activeConditionExists {
		return http.StatusNotFound, &v1types.ServerResponse{
			Message: "no active condition found for server",
		}
	}

	// TODO: if a task exists for this condition in an active state, don't proceed

	// pop condition from the Jetstream queue
	cond, err := r.jsPopCondition(ctx, conditionKind, serverID)
	if err != nil {
		if errors.Is(err, errNoConditionInQueue) {
			return http.StatusNotFound, &v1types.ServerResponse{
				Message: "no condition in queue",
			}
		}

		r.logger.WithError(err).Info("condition queue fetch error")

		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "Condition Queue fetch error error: " + err.Error(),
		}
	}

	// register this worker
	controllerID, err := r.controllerRegister(serverID.String())
	if err != nil {
		r.logger.WithField("condition.id", cond.ID).WithError(err).Info("controller registration error")

		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "Controller registration error: " + err.Error(),
		}
	}

	st := rctypes.NewTaskStatusRecord("controller fetched condition using client IP: " + c.RemoteIP())
	sv := &rctypes.StatusValue{
		WorkerID: controllerID.String(),
		Target:   serverID.String(),
		TraceID:  trace.SpanFromContext(ctx).SpanContext().TraceID().String(),
		SpanID:   trace.SpanFromContext(ctx).SpanContext().SpanID().String(),
		State:    string(rctypes.Pending),
		Status:   st.MustMarshal(),
	}

	// create condition status value entry
	if err := r.kvPublishStatusValue(
		serverID,
		cond.ID,
		controllerID,
		cond.Kind,
		sv,
		true,
		false,
	); err != nil {
		r.logger.WithField("condition.id", cond.ID).WithError(err).Info("status KV publish error")

		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "Condition first status publish error: " + err.Error(),
		}
	}

	// setup task to be published
	task := condition.NewTaskFromCondition(cond)
	task.WorkerID = controllerID.String()
	task.TraceID = trace.SpanFromContext(ctx).SpanContext().TraceID().String()
	task.SpanID = trace.SpanFromContext(ctx).SpanContext().SpanID().String()

	if err := r.kvPublishTask(
		ctx,
		serverID.String(),
		cond.ID.String(),
		conditionKind,
		task,
		true,
		false,
	); err != nil {
		r.logger.WithField("condition.id", cond.ID).WithError(err).Info("task KV publish error")

		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "Condition Task publish error: " + err.Error(),
		}
	}

	return http.StatusOK, &v1types.ServerResponse{
		Records: &v1types.ConditionsResponse{
			ServerID:   serverID,
			State:      cond.State,
			Conditions: []*rctypes.Condition{cond},
		},
	}
}

// popCondition registers the serverID as a controller and returns the condition assigned to it from NATS Jetstream.
func (r *Routes) jsPopCondition(ctx context.Context, conditionKind rctypes.Kind, serverID uuid.UUID) (*rctypes.Condition, error) {
	// at somepoint we get rid of this ridiculously long stream subject prefix
	// com.hollow.sh.controllers.commands.sandbox.servers.firmwareInstallInband.ede81024-f62a-4288-8730-3fab8cceab78
	subject := strings.Join(
		[]string{
			r.streamSubjectPrefix,                          // com.hollow.sh.controllers.commands
			r.streamSubject(r.facilityCode, conditionKind), // sandbox.servers.firmwareInstallInband
			serverID.String(),                              // ede81024-f62a-4288-8730-3fab8cceab7
		},
		".",
	)

	jctx := events.AsNatsJetStreamContext(r.streamBroker.(*events.NatsJetstream))
	subscription, err := jctx.PullSubscribe(
		subject,
		"",
		nats.PullMaxWaiting(1),
	)
	if err != nil {
		return nil, errors.Wrap(errQueueFetch, err.Error()+": subscribe error")
	}

	// This subject with the serverID suffix is expected to have only a single message in queue.
	// If theres more, we abort, or we risk returning a condition queued previously.
	//
	// Theres no reliable way to determine how many messages are in queue for this subject.
	// I've tried subcription.ConsumerInfo.NumPending but the value indicates
	// all the messages queued so far and isn't reliable.
	//
	// As a workaround we attempt to fetch upto 5 items from the backlog
	// if theres more than one we return an error for the Operators to clean up.
	msgBatch, err := subscription.FetchBatch(5, nats.MaxWait(conditionPopTimeout))
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
		r.logger.WithFields(logrus.Fields{"count": len(msgs), "subject": subject}).Error(errMsg)
		return nil, errors.Wrap(errQueueFetch, errMsg)
	}

	// ack message right away since we don't want redeliveries on failure
	msgs[0].Ack()

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

// TODO:
// - Check remote IP matches the expected server remote IP?
//
// @Summary ConditionStatusUpdate
// @Tag Conditions
// @Description Publishes an update to the Condition StatusValue KV
// @ID conditionStatusUpdate
// @Param uuid path string true "Server ID"
// @Param conditionKind path string true "Condition Kind"
// @Param conditionID path string true "Condition ID"
// @Param ts_update query string false "Only update timestamp in the StatusValue entry"
// @Accept json
// @Produce json
// @Success 200 {object} v1types.ServerResponse
// Failure 400 {object} v1types.ServerResponse
// Failure 404 {object} v1types.ServerResponse
// Failure 500 {object} v1types.ServerResponse
// @Router /servers/{uuid}/{conditionKind}/{conditionID} [put]
func (r *Routes) conditionStatusUpdate(c *gin.Context) (int, *v1types.ServerResponse) {
	ctx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.conditionStatusUpdate")
	span.SetAttributes(
		attribute.KeyValue{Key: "serverId", Value: attribute.StringValue(c.Param("uuid"))},
		attribute.KeyValue{Key: "conditionKind", Value: attribute.StringValue(c.Param("conditionKind"))},
		attribute.KeyValue{Key: "conditionID", Value: attribute.StringValue(c.Param("conditionID"))},
		attribute.KeyValue{Key: "timestampUpdate", Value: attribute.StringValue(c.Request.URL.Query().Get("ts_update"))},
		attribute.KeyValue{Key: "controllerID", Value: attribute.StringValue(c.Request.URL.Query().Get("controller_id"))},
	)
	defer span.End()

	paramControllerID := c.Request.URL.Query().Get("controller_id")
	if paramControllerID == "" {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "expected controller_id param",
		}
	}

	controllerID, err := registry.ControllerIDFromString(paramControllerID)
	if err != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: fmt.Sprintf("invalid controller_id: %s, err:", paramControllerID, err.Error()),
		}
	}

	var statusValue rctypes.StatusValue
	var onlyTimestampUpdate bool
	if c.Request.URL.Query().Get("ts_update") == "true" {
		onlyTimestampUpdate = true
	} else {
		if err := c.ShouldBindJSON(&statusValue); err != nil {
			r.logger.WithError(err).Warn("unmarshal StatusValue payload")

			return http.StatusBadRequest, &v1types.ServerResponse{
				Message: "invalid StatusValue payload: " + err.Error(),
			}
		}
	}

	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid server id: " + err.Error(),
		}
	}

	conditionKind := rctypes.Kind(c.Param("conditionKind"))
	if !r.conditionKindValid(conditionKind) {
		r.logger.WithFields(logrus.Fields{
			"kind": conditionKind,
		}).Info("unsupported condition kind")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "unsupported condition kind: " + string(conditionKind),
		}
	}

	conditionID, err := uuid.Parse(c.Param("conditionID"))
	if err != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid conditionID: " + err.Error(),
		}
	}

	// the controller pop'ed the condition from the queue which created the StatusKV entry
	// we expect an active condition to allow this update
	activeCond, err := r.repository.GetActiveCondition(ctx, serverID)
	if err != nil {
		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "condition lookup: " + err.Error(),
		}
	}

	if activeCond == nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "no active condition found for server",
		}
	}

	// publish status value update
	if err := r.kvPublishStatusValue(
		serverID,
		conditionID,
		controllerID,
		conditionKind,
		&statusValue,
		false,
		onlyTimestampUpdate,
	); err != nil {
		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "status publish error: " + err.Error(),
		}
	}

	return http.StatusOK, &v1types.ServerResponse{
		Message: "condition status update published",
	}
}

func (r *Routes) kvPublishStatusValue(
	serverID,
	conditionID uuid.UUID,
	controllerID registry.ControllerID,
	conditionKind rctypes.Kind,
	newSV *rctypes.StatusValue,
	create,
	onlyTimestamp bool,
) error {
	statusKV, err := status.GetConditionKV(conditionKind)
	if err != nil {
		return errors.Wrap(errPublishStatus, err.Error())
	}

	key := rkv.StatusValueKVKey(r.facilityCode, conditionID.String())
	// create
	if create {
		newSV.CreatedAt = time.Now()
		if _, errCreate := statusKV.Create(key, newSV.MustBytes()); errCreate != nil {
			return errors.Wrap(errPublishStatus, errCreate.Error())
		}

		return nil
	}

	// update
	currEntry, err := statusKV.Get(key)
	if err != nil && !errors.Is(err, nats.ErrKeyNotFound) {
		return errors.Wrap(errPublishStatus, err.Error())
	}

	// only timestamp update takes the current value
	var publishSV *rctypes.StatusValue
	if onlyTimestamp {
		curSV := &rctypes.StatusValue{}
		if errJSON := json.Unmarshal(currEntry.Value(), &curSV); errJSON != nil {
			return errors.Wrap(errUnmarshalKey, errJSON.Error())
		}

		if curSV.WorkerID != controllerID.String() {
			return errors.Wrap(errControllerMismatch, curSV.WorkerID)
		}

		publishSV = curSV
	} else {
		if newSV == nil {
			return errors.Wrap(errPublishStatus, "expected a StatusValue param got nil")
		}

		publishSV = newSV
	}

	publishSV.UpdatedAt = time.Now()
	if _, err := statusKV.Update(key, publishSV.MustBytes(), currEntry.Revision()); err != nil {
		return errors.Wrap(errPublishStatus, err.Error())
	}

	return nil
}

// @Summary livenessCheckin
// @Tag Controllers
// @Description Check-in endpoint for controllers.
// @ID conditionQueue
// @Param uuid path string true "Server ID"
// @Param conditionKind path string true "Condition Kind"
// @Param conditionID path string true "Condition ID"
// @Accept json
// @Produce json
// @Success 200 {object} v1types.ServerResponse
// Failure 400 {object} v1types.ServerResponse
// Failure 500 {object} v1types.ServerResponse
// @Router /controller-checkin/:conditionID/controllerID [get]
func (r *Routes) livenessCheckin(c *gin.Context) (int, *v1types.ServerResponse) {
	ctx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.livenessCheckin")
	span.SetAttributes(
		attribute.KeyValue{Key: "serverId", Value: attribute.StringValue(c.Param("uuid"))},
		attribute.KeyValue{Key: "conditionID", Value: attribute.StringValue(c.Param("conditionID"))},
		attribute.KeyValue{Key: "controllerID", Value: attribute.StringValue(c.Request.URL.Query().Get("controller_id"))},
	)
	defer span.End()

	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid server id: " + err.Error(),
		}
	}

	paramControllerID := c.Request.URL.Query().Get("controller_id")
	if paramControllerID == "" {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid controller ID, none specified",
		}
	}

	controllerID, err := registry.ControllerIDFromString(paramControllerID)
	if err != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid controller ID: " + err.Error(),
		}
	}

	_, err = uuid.Parse(c.Param("conditionID"))
	if err != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid conditionID: " + err.Error(),
		}
	}

	// the controller pop'ed the condition from the queue
	// which set the condition state to be active, so we expect an active condition
	// to check in this controller
	activeCond, err := r.repository.GetActiveCondition(ctx, serverID)
	if err != nil {
		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "condition lookup: " + err.Error(),
		}
	}

	if activeCond == nil {
		return http.StatusNotFound, &v1types.ServerResponse{
			Message: "no active condition found for server",
		}
	}

	if err := r.kvControllerCheckin(controllerID); err != nil {
		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "check-in failed: " + err.Error(),
		}
	}

	return http.StatusOK, &v1types.ServerResponse{
		Message: "check-in successful",
	}
}

// controller registry helper methods
func (r *Routes) controllerRegister(controllerID string) (registry.ControllerID, error) {
	id := registry.GetID(controllerID)
	le := r.logger.WithField("id", id.String())

	// set up the active-controller registry handle
	err := r.registerRegistryKVHandle()
	if err != nil {
		le.WithError(err).Warn("unable to initialize controller registry KV handle")
		return nil, err
	}

	r.logger.WithField("id", id.String()).Info("registry controllerID assigned")
	if err := registry.RegisterController(id); err != nil {
		metrics.DependencyError("liveness", "register")
		le.WithError(err).Warn("initial controller liveness registration failed")

		return nil, err
	}

	return id, nil
}

func (r *Routes) kvControllerCheckin(id registry.ControllerID) error {
	le := r.logger.WithField("id", id.String())

	// set up the active-controller registry handle
	err := r.registerRegistryKVHandle()
	if err != nil {
		le.WithError(err).Warn("unable to initialize controller registry KV handle")
		return err
	}

	_, err = registry.LastContact(id)
	if err != nil {
		le.WithError(err).Warn("error identifying last contact from controller")
		return err
	}

	if errCheckin := registry.ControllerCheckin(id); errCheckin != nil {
		metrics.DependencyError("liveness", "check-in")
		le.WithError(errCheckin).Warn("controller checkin failed")
		// try to refresh our token, maybe this is a NATS hiccup
		//
		// TODO: get the client to retry
		if errRefresh := refreshControllerToken(id); errRefresh != nil {
			// couldn't refresh our liveness token, time to bail out
			le.WithError(errRefresh).Error("controller re-register required")
			return errors.Wrap(errRegistryCheckin, "check-in and refresh attempt failed, controller re-register required")
		}
	}

	return nil
}

// try to de-register/re-register this id.
func refreshControllerToken(id registry.ControllerID) error {
	err := registry.DeregisterController(id)
	if err != nil && !errors.Is(err, nats.ErrKeyNotFound) {
		metrics.DependencyError("liveness", "de-register")
		return err
	}
	err = registry.RegisterController(id)
	if err != nil {
		metrics.DependencyError("liveness", "register")
		return err
	}
	return nil
}

// @Summary taskQuery
// @Tag Conditions
// @Description Queries a *rivets.Task object from KV for a condition
// @ID taskQuery
// @Param uuid path string true "Server ID"
// @Param conditionKind path string true "Condition Kind"
// @Accept json
// @Produce json
// @Success 200 {object} v1types.ServerResponse
// Failure 400 {object} v1types.ServerResponse
// Failure 404 {object} v1types.ServerResponse
// Failure 500 {object} v1types.ServerResponse
// Failure 503 {object} v1types.ServerResponse
// @Router /servers/{uuid}/condition-task/{conditionKind} [get]
func (r *Routes) taskQuery(c *gin.Context) (int, *v1types.ServerResponse) {
	ctx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.taskQuery")
	span.SetAttributes(
		attribute.KeyValue{Key: "serverId", Value: attribute.StringValue(c.Param("uuid"))},
		attribute.KeyValue{Key: "conditionKind", Value: attribute.StringValue(c.Param("conditionKind"))},
	)
	defer span.End()

	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid server id: " + err.Error(),
		}
	}

	conditionKind := rctypes.Kind(c.Param("conditionKind"))
	if !r.conditionKindValid(conditionKind) {
		r.logger.WithFields(logrus.Fields{
			"kind": conditionKind,
		}).Info("unsupported condition kind")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "unsupported condition kind: " + string(conditionKind),
		}
	}

	// the controller pop'ed the condition from the queue which created the Task entry
	// we expect an active condition to allow this query
	activeCond, err := r.repository.GetActiveCondition(ctx, serverID)
	if err != nil {
		if errors.Is(err, store.ErrConditionNotFound) {
			return http.StatusBadRequest, &v1types.ServerResponse{
				Message: "no active condition found for server",
			}
		}

		r.logger.WithField("condition.kind", conditionKind).WithError(err).Info("active condition query error")

		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "condition lookup error: " + err.Error(),
		}
	}

	if activeCond.Kind != conditionKind {
		return http.StatusServiceUnavailable, &v1types.ServerResponse{
			Message: fmt.Sprintf("current active condition: %s, retry in a while", activeCond.Kind),
		}
	}

	task, err := r.kvTaskQuery(c.Request.Context(), conditionKind, activeCond.ID, serverID)
	if err != nil {
		r.logger.WithField("condition.id", activeCond.ID).WithError(err).Info("task KV query error")

		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: err.Error(),
		}
	}

	// A stale task was not cleaned up and now we have an odd situation
	if activeCond.ID != task.ID {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: fmt.Sprintf("TaskID: %s does not match ConditionID: %s", task.ID, activeCond.ID),
		}
	}

	return http.StatusOK, &v1types.ServerResponse{
		Message: "Task identified",
		Records: &v1types.ConditionsResponse{Tasks: []*rctypes.Task[any, any]{task}},
	}
}

func (r *Routes) kvTaskQuery(ctx context.Context, conditionKind rctypes.Kind, conditionID, serverID uuid.UUID) (*rctypes.Task[any, any], error) {
	ctx, span := otel.Tracer(pkgName).Start(ctx, "Routes.taskQuery")
	defer span.End()

	// init task KV handle
	taskKV, err := r.conditionTaskKVHandle()
	if err != nil {
		return nil, errors.Wrap(errQueryTask, err.Error())
	}

	key := rctypes.TaskKVRepositoryKey(r.facilityCode, conditionKind, serverID.String())
	currEntry, err := taskKV.Get(key)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			r.logger.WithError(err).WithFields(logrus.Fields{
				"serverID":     serverID,
				"facilityCode": r.facilityCode,
				"conditionID":  conditionID,
				"controllerID": serverID,
				"key":          key,
			}).Info("Task key not found") // TODO: change to debug

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

		r.logger.WithError(err).WithFields(logrus.Fields{
			"serverID":     serverID,
			"facilityCode": r.facilityCode,
			"conditionID":  conditionID,
			"controllerID": serverID,
			"key":          key,
		}).Warn("Task query error")
		return nil, errors.Wrap(errQueryTask, err.Error())
	}

	return rctypes.TaskFromMessage(currEntry.Value())
}

// @Summary taskPublish
// @Tag Conditions
// @Description Publishes a *rivets.Task object to the KV for a condition
// @ID taskPublish
// @Param uuid path string true "Server ID"
// @Param conditionKind path string true "Condition Kind"
// @Param conditionID path string true "Condition ID"
// @Accept json
// @Produce json
// @Success 200 {object} v1types.ServerResponse
// Failure 400 {object} v1types.ServerResponse
// Failure 404 {object} v1types.ServerResponse
// Failure 503 {object} v1types.ServerResponse
// @Router /servers/{uuid}/{conditionKind}/{conditionID} [post]
func (r *Routes) taskPublish(c *gin.Context) (int, *v1types.ServerResponse) {
	ctx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.taskPublish")
	span.SetAttributes(
		attribute.KeyValue{Key: "serverId", Value: attribute.StringValue(c.Param("uuid"))},
		attribute.KeyValue{Key: "conditionKind", Value: attribute.StringValue(c.Param("conditionKind"))},
		attribute.KeyValue{Key: "conditionID", Value: attribute.StringValue(c.Param("conditionID"))},
		attribute.KeyValue{Key: "timestampUpdate", Value: attribute.StringValue(c.Request.URL.Query().Get("ts_update"))},
	)
	defer span.End()

	var task rctypes.Task[any, any]
	var onlyTimestampUpdate bool

	if c.Request.URL.Query().Get("ts_update") == "true" {
		onlyTimestampUpdate = true
	} else {
		if err := c.ShouldBindJSON(&task); err != nil {
			r.logger.WithError(err).Warn("unmarshal Task payload")

			return http.StatusBadRequest, &v1types.ServerResponse{
				Message: "invalid Task payload: " + err.Error(),
			}
		}
	}

	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid server id: " + err.Error(),
		}
	}

	conditionKind := rctypes.Kind(c.Param("conditionKind"))
	if !r.conditionKindValid(conditionKind) {
		r.logger.WithFields(logrus.Fields{
			"kind": conditionKind,
		}).Info("unsupported condition kind")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "unsupported condition kind: " + string(conditionKind),
		}
	}

	conditionID, err := uuid.Parse(c.Param("conditionID"))
	if err != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid conditionID: " + err.Error(),
		}
	}

	// the controller pop'ed the condition from the queue which created the Task entry
	// we expect an active condition to allow this publish
	activeCond, err := r.repository.GetActiveCondition(ctx, serverID)
	if err != nil {
		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "condition lookup: " + err.Error(),
		}
	}

	if activeCond == nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "no active condition found for server",
		}
	}

	if activeCond.ID != task.ID {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: fmt.Sprintf("TaskID: %s does not match ConditionID: %s", task.ID, activeCond.ID),
		}
	}

	// publish Task
	if err := r.kvPublishTask(
		ctx,
		serverID.String(),
		conditionID.String(),
		conditionKind,
		&task,
		false,
		onlyTimestampUpdate,
	); err != nil {
		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "Task publish error: " + err.Error(),
		}
	}

	return http.StatusOK, &v1types.ServerResponse{
		Message: "condition Task published",
	}
}

func (r *Routes) conditionTaskKVHandle() (nats.KeyValue, error) {
	// Note: No CreateOrBindCall is made to the bucket here,
	// since we expect that the Orchestrator or other controllers will create this bucket
	// before any http based controller shows up.
	//
	// If this trips over  nats.ErrBucketNotFound, then lets add the CreateOrBind method
	njs := r.streamBroker.(*events.NatsJetstream)
	return events.AsNatsJetStreamContext(njs).KeyValue(rctypes.TaskKVRepositoryBucket)
}

// Sets up the registry KV handle in the registry package so callers can invoke methods on the active-controllers registry
func (r *Routes) registerRegistryKVHandle() error {
	// Note: No CreateOrBindCall is made to the bucket here,
	// since we expect that the Orchestrator or other controllers will create this bucket
	// before any http based controller shows up.
	//
	// If this trips over  nats.ErrBucketNotFound, then lets add the CreateOrBind method
	njs := r.streamBroker.(*events.NatsJetstream)

	handle, err := events.AsNatsJetStreamContext(njs).KeyValue(registry.RegistryName)
	if err != nil {
		return err
	}

	// assign the KV handle so we can invoke methods from the registry package
	if err := registry.SetHandle(handle); err != nil && !errors.Is(err, registry.ErrRegistryPreviouslyInitialized) {
		return err
	}

	return nil
}

// Publish implements the ConditionTaskRepository interface, it transparently publishes the given json.RawMessage to the KV
func (r *Routes) kvPublishTask(
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

	key := rctypes.TaskKVRepositoryKey(r.facilityCode, conditionKind, serverID)
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

		r.logger.WithError(err).WithFields(logrus.Fields{
			"serverID":     serverID,
			"facilityCode": r.facilityCode,
			"conditionID":  conditionID,
			"controllerID": serverID,
			"key":          key,
		}).Warn("Task publish error")

		return errors.Wrap(errPublishTask, err.Error())
	}

	// init task KV handle
	taskKV, err := r.conditionTaskKVHandle()
	if err != nil {
		return errors.Wrap(errPublishTask, err.Error())
	}

	// create
	if create {
		task.CreatedAt = time.Now()
		taskJSON, err := task.Marshal()
		if err != nil {
			return failed(err)
		}

		// nolint:ineffassign // false positive
		rev, err := taskKV.Create(key, taskJSON)
		if err != nil {
			return failed(err)
		}

		r.logger.WithFields(logrus.Fields{
			"serverID":     serverID,
			"facilityCode": r.facilityCode,
			"taskID":       conditionID,
			"rev":          rev,
			"key":          key,
			"create":       create,
		}).Trace("Task create published")

		return nil
	}

	// update
	curr, err := taskKV.Get(key)
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
		if err := currTask.Update(task); err != nil {
			return failed(err)
		}
	}

	currTaskJSON, err := currTask.Marshal()
	if err != nil {
		return failed(err)
	}

	if _, err := taskKV.Update(key, currTaskJSON, curr.Revision()); err != nil {
		return failed(err)
	}

	r.logger.WithFields(logrus.Fields{
		"serverID":     serverID,
		"facilityCode": r.facilityCode,
		"taskID":       conditionID,
		"rev":          curr.Revision(),
		"key":          key,
		"create":       create,
	}).Trace("Task update published")

	return nil
}
