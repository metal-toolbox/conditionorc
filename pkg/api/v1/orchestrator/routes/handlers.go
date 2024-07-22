package routes

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/metal-toolbox/rivets/events/registry"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/metal-toolbox/conditionorc/internal/store"
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/orchestrator/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
)

var (
	errPublishStatus      = errors.New("error in condition status publish")
	errUnmarshalKey       = errors.New("error unmarshal key, value for update")
	errControllerMismatch = errors.New("condition controller mismatch error")
)

func (r *Routes) conditionKindValid(kind rctypes.Kind) bool {
	found := r.conditionDefinitions.FindByKind(kind)
	return found != nil
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
// @Router /servers/{uuid}/condition-status/{conditionKind}/{conditionID} [put]
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

	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid server id: " + err.Error(),
		}
	}

	paramControllerID := c.Request.URL.Query().Get("controller_id")
	if paramControllerID == "" {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "expected controller_id param",
		}
	}

	controllerID, err := registry.ControllerIDFromString(paramControllerID)
	if err != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: fmt.Sprintf("invalid controller_id: %s, err: %s", paramControllerID, err.Error()),
		}
	}

	var statusValue rctypes.StatusValue
	var onlyTimestampUpdate bool
	if c.Request.URL.Query().Get("ts_update") == "true" {
		onlyTimestampUpdate = true
	} else {
		if errBind := c.ShouldBindJSON(&statusValue); errBind != nil {
			r.logger.WithError(err).Warn("unmarshal StatusValue payload")

			return http.StatusBadRequest, &v1types.ServerResponse{
				Message: "invalid StatusValue payload: " + errBind.Error(),
			}
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
	if err := r.statusValueKV.publish(
		r.facilityCode,
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

	task, err := r.taskKV.get(c.Request.Context(), conditionKind, activeCond.ID, serverID)
	if err != nil {
		r.logger.WithField("condition.id", activeCond.ID).WithError(err).Info("task KV query error")

		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: err.Error(),
		}
	}

	// A stale task was not cleaned up and now we have an odd situation
	if activeCond.ID != task.ID {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: fmt.Sprintf("TaskID: %s does not match active ConditionID: %s", task.ID, activeCond.ID),
		}
	}

	return http.StatusOK, &v1types.ServerResponse{
		Message: "Task identified",
		Task:    task,
	}
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
// @Router /servers/{uuid}/condition-task/{conditionKind}/{conditionID} [post]
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

	// the controller retrieved the condition from the queue which created the Task entry
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

	if !onlyTimestampUpdate && activeCond.ID != task.ID {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: fmt.Sprintf("TaskID: %s does not match active ConditionID: %s", task.ID, activeCond.ID),
		}
	}

	// publish Task
	if err := r.taskKV.publish(
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

// @Summary ConditionPending
// @Tag Conditions
// @Description Returns the pending Condition based on the given parameters.
// @ID conditionPending
// @Param uuid path string true "Server ID"
// @Param conditionKind path string true "Condition Kind"
// @Accept json
// @Produce json
// @Success 200 {object} v1types.ServerResponse
// Failure 400 {object} v1types.ServerResponse
// Failure 404 {object} v1types.ServerResponse
// Failure 500 {object} v1types.ServerResponse
// @Router /servers/{uuid}/condition-pending/{conditionKind} [get]
func (r *Routes) conditionPending(c *gin.Context) (int, *v1types.ServerResponse) {
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

	var found *rctypes.Condition
	for _, cond := range conditionRecord.Conditions {
		if cond.Kind == conditionKind && cond.State == rctypes.Pending {
			found = cond
		}
	}

	if found == nil {
		return http.StatusNotFound, &v1types.ServerResponse{
			Message: "no pending condition found for server",
		}
	}

	return http.StatusOK, &v1types.ServerResponse{Condition: found}
}
