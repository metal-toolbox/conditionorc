package routes

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
	"go.opentelemetry.io/otel"

	"github.com/metal-toolbox/conditionorc/internal/store"
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
)

var (
	ErrConditionParameter = errors.New("error in condition parameter")
	ErrConditionExclusive = errors.New("exclusive condition present")
)

func (r *Routes) conditionKindValid(kind ptypes.ConditionKind) bool {
	found := r.conditionDefinitions.FindByKind(kind)
	return found != nil
}

func (r *Routes) serverConditionUpdate(c *gin.Context) (int, *v1types.ServerResponse) {
	otelCtx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.serverConditionUpdate")
	defer span.End()
	// XXX: should "uuid" be "server-uuid" or something?
	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		// XXX: do we have any way to correlate an API request id through this stack?
		r.logger.WithFields(logrus.Fields{
			"serverID": c.Param("uuid"),
		}).Info("bad serverID")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid ConditionUpdate payload " + err.Error(),
		}
	}

	kind := ptypes.ConditionKind(c.Param("conditionKind"))
	if !r.conditionKindValid(kind) {
		r.logger.WithFields(logrus.Fields{
			"kind": kind,
		}).Info("unsupported condition kind")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "unsupported condition kind: " + string(kind),
		}
	}

	var conditionUpdate v1types.ConditionUpdate
	if err = c.ShouldBindJSON(&conditionUpdate); err != nil {
		r.logger.WithFields(logrus.Fields{
			"error": err,
		}).Info("invalid ConditionUpdate")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid ConditionUpdate payload " + err.Error(),
		}
	}

	if errUpdate := conditionUpdate.Validate(); errUpdate != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid ConditionUpdate payload " + errUpdate.Error(),
		}
	}

	// query existing condition
	existing, err := r.repository.Get(otelCtx, serverID, kind)
	if err != nil {
		r.logger.WithFields(logrus.Fields{
			"error":    err,
			"serverID": serverID.String(),
			"kind":     kind,
		}).Info("condition lookup failed")
		return http.StatusServiceUnavailable, &v1types.ServerResponse{
			Message: err.Error(),
		}
	}

	if existing == nil {
		r.logger.WithFields(logrus.Fields{
			"serverID": serverID.String(),
			"kind":     kind,
		}).Info("condition not found")

		return http.StatusNotFound, &v1types.ServerResponse{
			Message: "no existing condition found for update",
		}
	}

	// nothing to update
	if existing.State == conditionUpdate.State && bytes.Equal(existing.Status, conditionUpdate.Status) {
		return http.StatusOK, &v1types.ServerResponse{
			Message: "no changes to be applied",
		}
	}

	// merge update with existing
	update, err := conditionUpdate.MergeExisting(existing, true)
	if err != nil {
		r.logger.WithFields(logrus.Fields{
			"error":            err,
			"incoming_state":   conditionUpdate.State,
			"incoming_version": conditionUpdate.ResourceVersion,
			"existing_state":   existing.State,
			"existing_version": existing.ResourceVersion,
		}).Info("condition merge failed")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid ConditionUpdate payload",
		}
	}

	// update
	if err := r.repository.Update(otelCtx, serverID, update); err != nil {
		r.logger.WithFields(logrus.Fields{
			"error":             err,
			"serverID":          serverID,
			"conditionID":       update.ID,
			"condition_version": update.ResourceVersion,
		}).Info("condition update failed")

		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: err.Error(),
		}
	}

	return http.StatusOK, &v1types.ServerResponse{Message: "condition updated"}
}

func (r *Routes) serverConditionCreate(c *gin.Context) (int, *v1types.ServerResponse) {
	otelCtx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.serverConditionUpdate")
	defer span.End()
	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		r.logger.WithFields(logrus.Fields{
			"serverID": c.Param("uuid"),
		}).Info("bad serverID")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: err.Error(),
		}
	}

	kind := ptypes.ConditionKind(c.Param("conditionKind"))
	if !r.conditionKindValid(kind) {
		r.logger.WithFields(logrus.Fields{
			"kind": kind,
		}).Info("unsupported condition kind")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "unsupported condition kind: " + string(kind),
		}
	}

	var conditionCreate v1types.ConditionCreate
	if err = c.ShouldBindJSON(&conditionCreate); err != nil {
		r.logger.WithFields(logrus.Fields{
			"error": err,
		}).Info("invalid ConditionCreate payload")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid ConditionCreate payload: " + err.Error(),
		}
	}

	condition := conditionCreate.NewCondition(kind)

	// check the condition doesn't already exist in an incomplete state
	existing, err := r.repository.Get(otelCtx, serverID, kind)
	if err != nil && !errors.Is(err, store.ErrConditionNotFound) {
		r.logger.WithFields(logrus.Fields{
			"error":    err,
			"serverID": serverID.String(),
			"kind":     kind,
		}).Info("condition lookup failed")

		return http.StatusServiceUnavailable, &v1types.ServerResponse{
			Message: err.Error(),
		}
	}

	if existing != nil && !existing.IsComplete() {
		r.logger.WithFields(logrus.Fields{
			"serverID":    serverID.String(),
			"conditionID": existing.ID.String(),
			"kind":        kind,
		}).Info("existing server condition found")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "condition present in an incomplete state: " + string(existing.State),
		}
	}

	// purge the existing condition
	if existing != nil {
		err = r.repository.Delete(otelCtx, serverID, kind)
		if err != nil && !errors.Is(err, store.ErrConditionNotFound) {
			r.logger.WithFields(logrus.Fields{
				"error": err,
			}).Info("unable to remove existing, finalized condition")

			return http.StatusInternalServerError, &v1types.ServerResponse{
				Message: err.Error(),
			}
		}
	}

	// XXX: if this is a check for another condition holding a lock is there a way to do this without
	// iterating all conditions?
	// check if any condition with exclusive set is in incomplete states
	if errEx := r.exclusiveNonFinalConditionExists(otelCtx, serverID); errEx != nil {
		if errors.Is(errEx, ErrConditionExclusive) {
			r.logger.WithFields(logrus.Fields{
				"error": err,
				// XXX: would be nice to have the id of the exclusive condition here too
			}).Info("active, exclusive condition on this server")

			return http.StatusBadRequest, &v1types.ServerResponse{
				Message: errEx.Error(),
			}
		}

		r.logger.WithFields(logrus.Fields{
			"error": err,
		}).Info("error checking for exclusive conditions")

		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: errEx.Error(),
		}
	}

	// Create the new condition
	err = r.repository.Create(otelCtx, serverID, condition)
	if err != nil {
		r.logger.WithFields(logrus.Fields{
			"error": err,
		}).Info("condition create failed")

		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: err.Error(),
		}
	}

	// XXX: this operation can't ignore errors
	// publish the condition
	r.publishCondition(otelCtx, serverID, condition)

	return http.StatusOK, &v1types.ServerResponse{
		Message: "condition set",
	}
}

func (r *Routes) exclusiveNonFinalConditionExists(ctx context.Context, serverID uuid.UUID) error {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "Routes.exclusiveNonFinalConditionExists")
	defer span.End()
	for _, state := range ptypes.ConditionStates() {
		if ptypes.ConditionStateIsComplete(state) {
			continue
		}

		existing, err := r.repository.List(otelCtx, serverID, state)
		if err != nil && !errors.Is(err, store.ErrConditionNotFound) {
			r.logger.WithFields(logrus.Fields{
				"serverID": serverID.String(),
				"state":    state,
			}).Info("existing condition lookup failed")
			return err
		}

		for _, condition := range existing {
			if condition.Exclusive && condition.State == state {
				r.logger.WithFields(logrus.Fields{
					"serverID":    serverID.String(),
					"conditionID": condition.ID.String(),
					"kind":        condition.Kind,
					"state":       condition.State,
				}).Info("existing exclusive server condition found")
				return errors.Wrap(
					ErrConditionExclusive,
					fmt.Sprintf("%s:%s condition exists in an incomplete state - %s",
						condition.ID.String(), condition.Kind, string(condition.State)),
				)
			}
		}
	}

	return nil
}

func (r *Routes) serverConditionDelete(c *gin.Context) (int, *v1types.ServerResponse) {
	otelCtx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.serverConditionDelete")
	defer span.End()
	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		r.logger.WithFields(logrus.Fields{
			"serverID": c.Param("uuid"),
		}).Info("bad serverID")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: err.Error(),
		}
	}

	kind := ptypes.ConditionKind(c.Param("conditionKind"))
	if !r.conditionKindValid(kind) {
		r.logger.WithFields(logrus.Fields{
			"kind": kind,
		}).Info("unsupported condition kind")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "unsupported condition kind: " + string(kind),
		}
	}

	if err := r.repository.Delete(otelCtx, serverID, kind); err != nil {
		r.logger.WithFields(logrus.Fields{
			"serverID": serverID.String(),
			"kind":     kind,
			"error":    err,
		}).Warn("unable to delete condition from repository")

		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: err.Error(),
		}
	}

	return http.StatusOK, &v1types.ServerResponse{
		Message: "condition deleted",
	}
}

func (r *Routes) serverConditionList(c *gin.Context) (int, *v1types.ServerResponse) {
	otelCtx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.serverConditionList")
	defer span.End()
	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{Message: err.Error()}
	}

	// XXX: should this be condition_state in the gin Context?
	state := ptypes.ConditionState(c.Param("conditionState"))
	if state != "" && !ptypes.ConditionStateIsValid(state) {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "unsupported condition state: " + string(state),
		}
	}

	found, err := r.repository.List(otelCtx, serverID, state)
	if err != nil {
		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: err.Error(),
		}
	}

	if len(found) == 0 {
		return http.StatusNotFound, &v1types.ServerResponse{
			Message: "no conditions in given state found on server",
		}
	}

	return http.StatusOK, &v1types.ServerResponse{
		Records: &v1types.ConditionsResponse{
			ServerID:   serverID,
			Conditions: found,
		},
	}
}

func (r *Routes) serverConditionGet(c *gin.Context) (int, *v1types.ServerResponse) {
	otelCtx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.serverConditionGet")
	defer span.End()
	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: err.Error(),
		}
	}

	kind := ptypes.ConditionKind(c.Param("conditionKind"))
	if !r.conditionKindValid(kind) {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "unsupported condition kind: " + string(kind),
		}
	}

	found, err := r.repository.Get(otelCtx, serverID, kind)
	if err != nil {
		if errors.Is(err, store.ErrConditionNotFound) {
			return http.StatusNotFound, &v1types.ServerResponse{
				Message: "conditionKind not found on server",
			}
		}

		return http.StatusServiceUnavailable, &v1types.ServerResponse{
			Message: err.Error(),
		}
	}

	if found == nil {
		return http.StatusNotFound, &v1types.ServerResponse{
			Message: "conditionKind not found on server",
		}
	}

	return http.StatusOK, &v1types.ServerResponse{
		Records: &v1types.ConditionsResponse{
			ServerID: serverID,
			Conditions: []*ptypes.Condition{
				found,
			},
		},
	}
}

func (r *Routes) publishCondition(ctx context.Context, serverID uuid.UUID, condition *ptypes.Condition) {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "Routes.publishCondition")
	defer span.End()
	if r.streamBroker == nil {
		r.logger.Warn("Event publish skipped, not connected to stream broker")
		return
	}

	if err := r.streamBroker.PublishAsyncWithContext(
		otelCtx,
		events.ResourceType(ptypes.ServerResourceType),
		events.EventType(condition.Kind),
		serverID.String(),
		condition,
	); err != nil {
		r.logger.WithFields(
			logrus.Fields{"err": err.Error()},
		).Error("error publishing condition event")
	}

	r.logger.WithFields(logrus.Fields{"kind": condition.Kind}).Info("published condition event")
}
