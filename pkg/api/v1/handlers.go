package apiv1

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
)

func (r *Routes) serverConditionList(c *gin.Context) {
	c.JSON(200, "ok")
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

	condition := ptypes.ConditionKind(c.Param("conditionslug"))
	if !r.conditionValid(condition) {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: "unsupported condition slug: " + string(condition)},
		)

		return
	}

	found, err := r.repository.Get(c.Request.Context(), serverID, condition)
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			&ServerResponse{Message: err.Error()},
		)

		return
	}

	c.JSON(http.StatusOK, &ServerResponse{Data: found})
}

func (r *Routes) serverConditionSet(c *gin.Context) {
	c.JSON(200, "ok")
}

func (r *Routes) serverConditionUpdate(c *gin.Context) {
	c.JSON(200, "ok")
}

func (r *Routes) serverConditionDelete(c *gin.Context) {
}

func (r *Routes) conditionValid(s ptypes.ConditionKind) bool {
	for _, c := range r.conditionDefs {
		if c.Name == s {
			return true
		}
	}

	return false
}
