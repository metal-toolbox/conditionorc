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

	var conditionUpdate v1types.ConditionUpdate
	if err = c.ShouldBindJSON(&conditionUpdate); err != nil {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "invalid ConditionUpdate payload " + err.Error()},
		)

		return
	}

	if conditionUpdate.ResourceVersion == 0 {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "invalid ConditionUpdate payload, expected a valid resourceVersion"},
		)

		return
	}

	if conditionUpdate.State == "" && conditionUpdate.Status == nil {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "invalid ConditionUpdate payload, either a state or a status attribute is expected"},
		)

		return
	}

	// query existing condition
	existing, err := r.repository.Get(c.Request.Context(), serverID, kind)
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: err.Error()},
		)

		return
	}

	if existing == nil {
		c.JSON(
			http.StatusNotFound,
			&v1types.ServerResponse{Message: "no existing condition found for update"},
		)

		return
	}

	// nothing to update
	if existing.State == conditionUpdate.State && bytes.Equal(existing.Status, conditionUpdate.Status) {
		c.JSON(http.StatusOK, &v1types.ServerResponse{Message: "no changes to be applied"})

		return
	}

	// merge update with existing
	update, err := conditionUpdate.MergeExisting(existing)
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: err.Error()},
		)

		return
	}

	// update
	if err := r.repository.Update(c.Request.Context(), serverID, update); err != nil {
		c.JSON(
			http.StatusInternalServerError,
			&v1types.ServerResponse{Message: err.Error()},
		)

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

	var conditionCreate v1types.ConditionCreate
	if err = c.ShouldBindJSON(&conditionCreate); err != nil {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "invalid ConditionCreate payload: " + err.Error()},
		)

		return
	}

	condition := conditionCreate.NewCondition(kind)

	// check the condition doesn't already exist in a non-finalized state
	existing, err := r.repository.Get(c.Request.Context(), serverID, kind)
	if err != nil && !errors.Is(err, store.ErrConditionNotFound) {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: err.Error()},
		)

		return
	}

	if existing != nil && !ptypes.ConditionStateFinalized(existing.State) {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "condition present non-finalized state: " + string(existing.State)},
		)

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

	// check if any condition with exclusive set is in non-finalized states
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

		return
	}

	// publish the condition
	r.publishCondition(c.Request.Context(), serverID, condition)

	c.JSON(http.StatusOK, &v1types.ServerResponse{Message: "condition set"})
}

func (r *Routes) exclusiveNonFinalConditionExists(ctx context.Context, serverID uuid.UUID) error {
	for _, state := range ptypes.ConditionStates() {
		if ptypes.ConditionStateFinalized(state) {
			continue
		}

		existing, err := r.repository.List(ctx, serverID, state)
		if err != nil && !errors.Is(err, store.ErrConditionNotFound) {
			return err
		}

		for _, condition := range existing {
			if condition.Exclusive && condition.State == state {
				return errors.Wrap(
					ErrConditionExclusive,
					fmt.Sprintf("%s condition exists in non-finalized state - %s", condition.Kind, string(condition.State)),
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

	existing, err := r.repository.Get(c.Request.Context(), serverID, kind)
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: err.Error()},
		)

		return
	}

	if existing == nil {
		c.JSON(
			http.StatusBadRequest,
			&v1types.ServerResponse{Message: "no such condition found"},
		)

		return
	}

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
	if state != "" && !ptypes.ConditionStateValid(state) {
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
			http.StatusInternalServerError,
			&v1types.ServerResponse{Message: err.Error()},
		)

		return
	}

	if found == nil {
		c.JSON(http.StatusNotFound, &v1types.ServerResponse{Message: "conditionKind not found on server"})

		return
	}

	data := v1types.ConditionResponse{ServerID: serverID, Condition: found}

	c.JSON(http.StatusOK, &v1types.ServerResponse{Record: &data})
}

func (r *Routes) publishCondition(ctx context.Context, serverID uuid.UUID, condition *ptypes.Condition) {
	if r.streamBroker == nil {
		r.logger.Warn("Event publish skipped, not connected to stream borker")
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
