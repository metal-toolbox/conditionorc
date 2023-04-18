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

func (r *Routes) serverConditionUpdate(c *gin.Context) {
	// XXX: should "uuid" be "server-uuid" or something?
	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "invalid ConditionUpdate payload " + err.Error()},
		)
		// XXX: do we have any way to correlate an API request id through this stack?
		r.logger.WithFields(logrus.Fields{
			"serverID": c.Param("uuid"),
		}).Info("bad serverID")
		return
	}

	kind := ptypes.ConditionKind(c.Param("conditionKind"))
	if !r.conditionKindValid(kind) {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "unsupported condition kind: " + string(kind)},
		)
		r.logger.WithFields(logrus.Fields{
			"kind": kind,
		}).Info("unsupported condition kind")

		return
	}

	var conditionUpdate v1types.ConditionUpdate
	if err = c.ShouldBindJSON(&conditionUpdate); err != nil {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "invalid ConditionUpdate payload " + err.Error()},
		)
		r.logger.WithFields(logrus.Fields{
			"error": err,
		}).Info("invalid ConditionUpdate")

		return
	}

	if conditionUpdate.ResourceVersion == 0 {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "invalid ConditionUpdate payload: resourceVersion not set"},
		)
		r.logger.Info("resource version not set")

		return
	}

	if conditionUpdate.State == "" || conditionUpdate.Status == nil {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "invalid ConditionUpdate payload: state and status attributes are expected"},
		)
		r.logger.Info("invalid state and status pair")

		return
	}

	// query existing condition
	existing, err := r.repository.Get(c.Request.Context(), serverID, kind)
	if err != nil {
		c.JSON(
			http.StatusServiceUnavailable, // HTTP 503 -- this should signal a retry from the client
			&v1types.ServerResponse{Message: err.Error()},
		)
		r.logger.WithFields(logrus.Fields{
			"error":    err,
			"serverID": serverID.String(),
			"kind":     kind,
		}).Info("condition lookup failed")
		return
	}

	if existing == nil {
		c.JSON(
			http.StatusNotFound,
			&v1types.ServerResponse{Message: "no existing condition found for update"},
		)
		r.logger.WithFields(logrus.Fields{
			"serverID": serverID.String(),
			"kind":     kind,
		}).Info("condition not found")

		return
	}

	// nothing to update
	// XXX: consider just doing the update unconditionally?
	if existing.State == conditionUpdate.State && bytes.Equal(existing.Status, conditionUpdate.Status) {
		c.JSON(http.StatusOK, &v1types.ServerResponse{Message: "no changes to be applied"})

		return
	}

	// merge update with existing
	update, err := conditionUpdate.MergeExisting(existing)
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "invalid ConditionUpdate payload"},
		)
		r.logger.WithFields(logrus.Fields{
			"error":            err,
			"incoming_state":   conditionUpdate.State,
			"incoming_version": conditionUpdate.ResourceVersion,
			"existing_state":   existing.State,
			"existing_version": existing.ResourceVersion,
		}).Info("condition merge failed")

		return
	}

	// update
	if err := r.repository.Update(c.Request.Context(), serverID, update); err != nil {
		c.JSON(
			http.StatusInternalServerError,
			&v1types.ServerResponse{Message: err.Error()},
		)
		r.logger.WithFields(logrus.Fields{
			"error":             err,
			"serverID":          serverID,
			"conditionID":       update.ID,
			"condition_version": update.ResourceVersion,
		}).Info("condition update failed")

		return
	}

	c.JSON(http.StatusOK, &v1types.ServerResponse{Message: "condition updated"})
}

func (r *Routes) serverConditionCreate(c *gin.Context) {
	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: err.Error()},
		)
		r.logger.WithFields(logrus.Fields{
			"serverID": c.Param("uuid"),
		}).Info("bad serverID")

		return
	}

	kind := ptypes.ConditionKind(c.Param("conditionKind"))
	if !r.conditionKindValid(kind) {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "unsupported condition kind: " + string(kind)},
		)
		r.logger.WithFields(logrus.Fields{
			"kind": kind,
		}).Info("unsupported condition kind")

		return
	}

	var conditionCreate v1types.ConditionCreate
	if err = c.ShouldBindJSON(&conditionCreate); err != nil {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "invalid ConditionCreate payload: " + err.Error()},
		)
		r.logger.WithFields(logrus.Fields{
			"kind": kind,
		}).Info("unsupported condition kind")

		return
	}

	condition := conditionCreate.NewCondition(kind)

	// check the condition doesn't already exist in an incomplete state
	existing, err := r.repository.Get(c.Request.Context(), serverID, kind)
	if err != nil && !errors.Is(err, store.ErrConditionNotFound) {
		c.JSON(
			http.StatusServiceUnavailable,
			&v1types.ServerResponse{Message: err.Error()},
		)
		r.logger.WithFields(logrus.Fields{
			"error":    err,
			"serverID": serverID.String(),
			"kind":     kind,
		}).Info("condition lookup failed")

		return
	}

	if existing != nil && !existing.IsComplete() {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "condition present in an incomplete state: " + string(existing.State)},
		)
		r.logger.WithFields(logrus.Fields{
			"serverID":    serverID.String(),
			"conditionID": existing.ID.String(),
			"kind":        kind,
		}).Info("existing server condition found")

		return
	}

	// purge the existing condition
	if existing != nil {
		err = r.repository.Delete(c.Request.Context(), serverID, kind)
		if err != nil && !errors.Is(err, store.ErrConditionNotFound) {
			c.JSON(
				http.StatusInternalServerError,
				&v1types.ServerResponse{Message: err.Error()},
			)

			return
		}
	}

	// XXX: if this is a check for another condition holding a lock is there a way to do this without
	// iterating all conditions?
	// check if any condition with exclusive set is in incomplete states
	if errEx := r.exclusiveNonFinalConditionExists(c.Request.Context(), serverID); errEx != nil {
		if errors.Is(errEx, ErrConditionExclusive) {
			c.JSON(
				http.StatusBadRequest,
				&v1types.ServerResponse{Message: errEx.Error()},
			)

			return
		}

		c.JSON(
			http.StatusInternalServerError,
			&v1types.ServerResponse{Message: errEx.Error()},
		)

		return
	}

	// Create the new condition
	err = r.repository.Create(c.Request.Context(), serverID, condition)
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			&v1types.ServerResponse{Message: err.Error()},
		)
		r.logger.WithFields(logrus.Fields{
			"error": err,
		}).Info("condition create failed")

		return
	}

	// publish the condition
	r.publishCondition(c.Request.Context(), serverID, condition)

	c.JSON(http.StatusOK, &v1types.ServerResponse{Message: "condition set"})
}

func (r *Routes) exclusiveNonFinalConditionExists(ctx context.Context, serverID uuid.UUID) error {
	for _, state := range ptypes.ConditionStates() {
		if ptypes.ConditionStateIsComplete(state) {
			continue
		}

		existing, err := r.repository.List(ctx, serverID, state)
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

func (r *Routes) serverConditionDelete(c *gin.Context) {
	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: err.Error()},
		)
		r.logger.WithFields(logrus.Fields{
			"serverID": c.Param("uuid"),
		}).Info("bad serverID")

		return
	}

	kind := ptypes.ConditionKind(c.Param("conditionKind"))
	if !r.conditionKindValid(kind) {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "unsupported condition kind: " + string(kind)},
		)
		r.logger.WithFields(logrus.Fields{
			"kind": kind,
		}).Info("unsupported condition kind")

		return
	}

	// TODO (vc): the repository Delete route should take a condition id
	if err := r.repository.Delete(c.Request.Context(), serverID, kind); err != nil {
		c.JSON(
			http.StatusInternalServerError,
			&v1types.ServerResponse{Message: err.Error()},
		)

		return
	}

	c.JSON(http.StatusOK, &v1types.ServerResponse{Message: "condition deleted"})
}

func (r *Routes) serverConditionList(c *gin.Context) {
	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: err.Error()},
		)

		return
	}

	state := ptypes.ConditionState(c.Param("conditionState"))
	if state != "" && !ptypes.ConditionStateIsValid(state) {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "unsupported condition state: " + string(state)},
		)

		return
	}

	found, err := r.repository.List(c.Request.Context(), serverID, state)
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			&v1types.ServerResponse{Message: err.Error()},
		)

		return
	}

	if len(found) == 0 {
		c.JSON(http.StatusNotFound, &v1types.ServerResponse{Message: "no conditions in given state found on server"})

		return
	}

	data := v1types.ConditionsResponse{ServerID: serverID, Conditions: found}

	c.JSON(http.StatusOK, &v1types.ServerResponse{Records: &data})
}

func (r *Routes) serverConditionGet(c *gin.Context) {
	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: err.Error()},
		)

		return
	}

	kind := ptypes.ConditionKind(c.Param("conditionKind"))
	if !r.conditionKindValid(kind) {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "unsupported condition kind: " + string(kind)},
		)

		return
	}

	found, err := r.repository.Get(c.Request.Context(), serverID, kind)
	if err != nil {
		if errors.Is(err, store.ErrConditionNotFound) {
			c.JSON(http.StatusNotFound, &v1types.ServerResponse{Message: "conditionKind not found on server"})

			return
		}

		c.JSON(
			http.StatusServiceUnavailable,
			&v1types.ServerResponse{Message: err.Error()},
		)

		return
	}

	if found == nil {
		c.JSON(http.StatusNotFound, &v1types.ServerResponse{Message: "conditionKind not found on server"})

		return
	}

	c.JSON(http.StatusOK,
		&v1types.ServerResponse{
			Records: &v1types.ConditionsResponse{
				ServerID: serverID,
				Conditions: []*ptypes.Condition{
					found,
				},
			},
		})
}

func (r *Routes) publishCondition(ctx context.Context, serverID uuid.UUID, condition *ptypes.Condition) {
	if r.streamBroker == nil {
		r.logger.Warn("Event publish skipped, not connected to stream broker")
		return
	}

	if err := r.streamBroker.PublishAsyncWithContext(
		ctx,
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
