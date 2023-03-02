package routes

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pkg/errors"

	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
)

var (
	ErrConditionParameter = errors.New("error in condition parameter")
)

func (r *Routes) serverConditionUpdate(c *gin.Context) {
	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: err.Error()},
		)

		return
	}

	kind := ptypes.ConditionKind(c.Param("conditionKind"))
	if !ptypes.ConditionKindValid(kind) {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: "unsupported condition kind: " + string(kind)},
		)

		return
	}

	var conditionUpdate ConditionUpdate
	if err = c.ShouldBindJSON(&conditionUpdate); err != nil {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: "invalid ConditionUpdate payload " + err.Error()},
		)

		return
	}

	if conditionUpdate.ResourceVersion == 0 {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: "invalid ConditionUpdate payload, expected a valid resourceVersion"},
		)

		return
	}

	if conditionUpdate.State == "" && conditionUpdate.Status == nil {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: "invalid ConditionUpdate payload, either a State or a Status value"},
		)

		return
	}

	// query existing condition
	existing, err := r.repository.Get(c.Request.Context(), serverID, kind)
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: err.Error()},
		)

		return
	}

	// merge update with existing
	update, err := conditionUpdate.mergeExisting(existing)
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: err.Error()},
		)

		return
	}

	// update
	if err := r.repository.Update(c.Request.Context(), serverID, update); err != nil {
		c.JSON(
			http.StatusInternalServerError,
			&ServerResponse{Message: err.Error()},
		)

		return
	}

	c.JSON(http.StatusOK, &ServerResponse{Message: "condition updated"})
}

func (r *Routes) serverConditionCreate(c *gin.Context) {
	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: err.Error()},
		)

		return
	}

	kind := ptypes.ConditionKind(c.Param("conditionKind"))
	if !ptypes.ConditionKindValid(kind) {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: "unsupported condition kind: " + string(kind)},
		)

		return
	}

	var conditionCreate ConditionCreate
	if err = c.ShouldBindJSON(&conditionCreate); err != nil {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: "invalid ConditionCreate payload: " + err.Error()},
		)

		return
	}

	condition := conditionCreate.newCondition(kind)

	// TODO: check if condition already exists and is in a finalized state
	existing, err := r.repository.Get(c.Request.Context(), serverID, kind)
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: err.Error()},
		)

		return
	}

	if existing != nil && !ptypes.ConditionStateFinalized(existing.State) {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: "condition present non-finalized state: " + string(existing.State)},
		)

		return
	}

	// Create updates the resource version
	err = r.repository.Create(c.Request.Context(), serverID, condition)
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			&ServerResponse{Message: err.Error()},
		)

		return
	}

	c.JSON(http.StatusOK, &ServerResponse{Message: "condition set"})
}

func (r *Routes) serverConditionDelete(c *gin.Context) {
	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: err.Error()},
		)

		return
	}

	kind := ptypes.ConditionKind(c.Param("conditionKind"))
	if !ptypes.ConditionKindValid(kind) {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: "unsupported condition kind: " + string(kind)},
		)

		return
	}

	existing, err := r.repository.Get(c.Request.Context(), serverID, kind)
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: err.Error()},
		)

		return
	}

	if existing == nil {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: "no such condition found"},
		)

		return
	}

	if err := r.repository.Delete(c.Request.Context(), serverID, kind); err != nil {
		c.JSON(
			http.StatusInternalServerError,
			&ServerResponse{Message: err.Error()},
		)

		return
	}

	c.JSON(http.StatusOK, &ServerResponse{Message: "condition deleted"})
}

func (r *Routes) serverConditionList(c *gin.Context) {
	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: err.Error()},
		)

		return
	}

	state := ptypes.ConditionState(c.Param("conditionState"))
	if state != "" && !ptypes.ConditionStateValid(state) {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: "unsupported condition state: " + string(state)},
		)

		return
	}

	found, err := r.repository.List(c.Request.Context(), serverID, state)
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			&ServerResponse{Message: err.Error()},
		)

		return
	}

	if len(found) == 0 {
		c.JSON(http.StatusNotFound, &ServerResponse{Message: "no conditions in given state found on server"})

		return
	}

	data := ConditionsResponse{ServerID: serverID, Conditions: found}

	c.JSON(http.StatusOK, &ServerResponse{Records: data})
}

func (r *Routes) serverConditionGet(c *gin.Context) {
	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: err.Error()},
		)

		return
	}

	kind := ptypes.ConditionKind(c.Param("conditionKind"))
	if !ptypes.ConditionKindValid(kind) {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: "unsupported condition kind: " + string(kind)},
		)

		return
	}

	found, err := r.repository.Get(c.Request.Context(), serverID, kind)
	if err != nil {
		c.JSON(
			http.StatusInternalServerError,
			&ServerResponse{Message: err.Error()},
		)

		return
	}

	if found == nil {
		c.JSON(http.StatusNotFound, &ServerResponse{Message: "conditionKind not found on server"})

		return
	}

	data := ConditionResponse{ServerID: serverID, Condition: found}

	c.JSON(http.StatusOK, &ServerResponse{Record: data})
}
