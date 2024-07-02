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
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/metal-toolbox/conditionorc/internal/metrics"
	"github.com/metal-toolbox/conditionorc/internal/store"
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/conditions/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
)

const (
	badRequestErrMsg = "response code: 400"
)

var (
	ErrConditionParameter = errors.New("error in condition parameter")
	ErrConditionExclusive = errors.New("exclusive condition present")
	failedPublishStatus   = json.RawMessage(`{ "msg": "failed to publish condition to controller" }`)
)

func (r *Routes) conditionKindValid(kind rctypes.Kind) bool {
	found := r.conditionDefinitions.FindByKind(kind)
	return found != nil
}

// XXX: This needs to be refactored. In order to specifically compute the NATS subject for
// the message we need to have some visibility into the actual actions the caller wants us to
// take. Trying to parse this out of a json.RawMessage in the ConditionCreate is too
// ambiguous, as RawBytes has no structure aside being well-formed json.
//
// nolint:gocyclo //TODO: break up this method
// @Summary Condition Create
// @Tag Conditions
// @Description Creates a condition on a server
// @Description Sample firmwareInstall payload, response: https://github.com/metal-toolbox/conditionorc/blob/main/sample/firmwareInstall.md
// @Param uuid path string true "Server ID"
// @Param conditionKind path string true "Condition Kind"
// @Accept json
// @Produce json
// @Success 200 {object} v1types.ServerResponse
// Failure 400 {object} v1types.ServerResponse
// Failure 500 {object} v1types.ServerResponse
// Failure 503 {object} v1types.ServerResponse
// @Router /servers/{uuid}/condition/{conditionKind} [post]
func (r *Routes) serverConditionCreate(c *gin.Context) (int, *v1types.ServerResponse) {
	otelCtx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.serverConditionCreate")
	span.SetAttributes(
		attribute.KeyValue{Key: "conditionKind", Value: attribute.StringValue(c.Param("conditionKind"))},
		attribute.KeyValue{Key: "serverId", Value: attribute.StringValue(c.Param("uuid"))})
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

	kind := rctypes.Kind(c.Param("conditionKind"))
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

	facilityCode, err := r.serverFacilityCode(otelCtx, serverID)
	if err != nil {
		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "facility lookup error: " + err.Error(),
		}
	}

	active, err := r.repository.GetActiveCondition(otelCtx, serverID)
	if err != nil && !errors.Is(err, store.ErrConditionNotFound) {
		return http.StatusServiceUnavailable, &v1types.ServerResponse{
			Message: "error checking server state: " + err.Error(),
		}
	}
	if active != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "server has an active condition",
		}
	}

	newCondition := conditionCreate.NewCondition(kind, serverID)

	return r.conditionCreate(otelCtx, newCondition, serverID, facilityCode)
}

// @Summary Server Delete
// @Tag Servers
// @Description Delete a server from FleetDB
// @Description Sample server delete request and response: https://github.com/metal-toolbox/conditionorc/blob/main/sample/serverenroll.md
// @Param uuid path string true "Server ID"
// @Success 200 {object} v1types.ServerResponse
// Failure 400 {object} v1types.ServerResponse
// Failure 500 {object} v1types.ServerResponse
// @Router /servers/{uuid} [delete]
func (r *Routes) serverDelete(c *gin.Context) (int, *v1types.ServerResponse) {
	otelCtx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.serverDelete")
	id := c.Param("uuid")
	span.SetAttributes(attribute.KeyValue{Key: "serverId", Value: attribute.StringValue(id)})
	defer span.End()

	if id == "" {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "empty server ID",
		}
	}

	serverID, err := uuid.Parse(id)
	if err != nil {
		r.logger.WithFields(logrus.Fields{
			"serverID": id,
		}).Info("bad serverID")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: err.Error(),
		}
	}

	active, err := r.repository.GetActiveCondition(otelCtx, serverID)
	if err != nil && !errors.Is(err, store.ErrConditionNotFound) {
		return http.StatusServiceUnavailable, &v1types.ServerResponse{
			Message: "error checking server state: " + err.Error(),
		}
	}
	if active != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "failed to delete server because it has an active condition",
		}
	}

	if err := r.fleetDBClient.DeleteServer(c.Request.Context(), serverID); err != nil {
		if !strings.Contains(err.Error(), "404") {
			return http.StatusInternalServerError, &v1types.ServerResponse{
				Message: err.Error(),
			}
		}
	}

	return http.StatusOK, &v1types.ServerResponse{
		Message: "server deleted",
		Records: &v1types.ConditionsResponse{ServerID: serverID},
	}
}

// @Summary Server Enroll
// @Tag Servers
// @Description Creates a server record in FleetDB and schedules an inventory condition on the device.
// @Description It will create a new server ID if UUID is not provided.
// @Description Sample server enroll request and response: https://github.com/metal-toolbox/conditionorc/blob/main/sample/serverenroll.md
// @Param uuid path string true "Server ID"
// @Accept json
// @Produce json
// @Success 200 {object} v1types.ServerResponse
// Failure 400 {object} v1types.ServerResponse
// Failure 500 {object} v1types.ServerResponse
// @Router /serverEnroll/{uuid} [post]
func (r *Routes) serverEnroll(c *gin.Context) (int, *v1types.ServerResponse) {
	id := c.Param("uuid")
	otelCtx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.serverEnroll")
	defer span.End()

	var serverID uuid.UUID
	var err error
	if id != "" {
		serverID, err = uuid.Parse(id)
		if err != nil {
			r.logger.WithFields(logrus.Fields{
				"serverID": id,
			}).Info("bad serverID")

			return http.StatusBadRequest, &v1types.ServerResponse{
				Message: "server id: " + err.Error(),
			}
		}
	} else {
		serverID = uuid.New()
	}

	span.SetAttributes(attribute.KeyValue{Key: "serverId", Value: attribute.StringValue(id)})
	var conditionCreate v1types.ConditionCreate
	if err = c.ShouldBindJSON(&conditionCreate); err != nil {
		r.logger.WithFields(logrus.Fields{
			"error": err,
		}).Info("invalid ConditionCreate payload")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid ConditionCreate payload: " + err.Error(),
		}
	}

	var params v1types.AddServerParams
	if err = json.Unmarshal(conditionCreate.Parameters, &params); err != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid params: " + err.Error(),
		}
	}

	// Creates a server record in FleetDB.
	rollback, err := r.fleetDBClient.AddServer(c.Request.Context(), serverID, params.Facility, params.IP, params.Username, params.Password)
	if err != nil {
		rollbackErr := rollback()
		if strings.Contains(err.Error(), badRequestErrMsg) {
			return http.StatusBadRequest, &v1types.ServerResponse{
				Message: "add server: " + err.Error() + fmt.Sprintf("server rollback err: %v", rollbackErr),
			}
		}
		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "add server: " + err.Error() + fmt.Sprintf("server rollback err: %v", rollbackErr),
		}
	}

	inventoryArgs := &rctypes.InventoryTaskParameters{
		AssetID:               serverID,
		Method:                rctypes.OutofbandInventory,
		CollectBiosCfg:        true,
		CollectFirwmareStatus: true,
	}
	inventoryParams, err := json.Marshal(inventoryArgs)
	if err != nil {
		_ = rollback()
		r.logger.WithError(err).Warning("bad condition inventoryParams serialize")
		panic(err)
	}
	conditionCreate.Parameters = inventoryParams
	newCondition := conditionCreate.NewCondition(rctypes.Inventory, serverID)

	st, resp := r.conditionCreate(otelCtx, newCondition, serverID, params.Facility)
	if st != http.StatusOK {
		rollbackErr := rollback()
		resp.Message += fmt.Sprintf("server rollback err: %v", rollbackErr)
	}
	return st, resp
}

// @Summary Server Provision
// @Tag Servers
// @Description an API to perform the server provision.
// @Accept json
// @Produce json
// @Success 200 {object} v1types.ServerResponse
// @Router /serverProvision [post]
func (r *Routes) serverProvision(c *gin.Context) (int, *v1types.ServerResponse) {
	var sp v1types.ServerProvisionRequest
	if err := c.ShouldBindJSON(&sp); err != nil {
		r.logger.WithError(err).Warn("unmarshal server provision payload")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid server provision payload: " + err.Error(),
		}
	}

	return 501, &v1types.ServerResponse{Message: "unimplemented"}
}

// @Summary Firmware Install
// @Tag Conditions
// @Description Installs firmware on a device and validates with a subsequent inventory
// @Description Sample firmwareInstall payload, response: https://github.com/metal-toolbox/conditionorc/blob/main/sample/firmwareInstall.md
// @Param uuid path string true "Server ID"
// @Param data body rctypes.FirmwareInstallTaskParameters true "firmware install options"
// @Accept json
// @Produce json
// @Success 200 {object} v1types.ServerResponse
// Failure 400 {object} v1types.ServerResponse
// Failure 500 {object} v1types.ServerResponse
// Failure 503 {object} v1types.ServerResponse
// @Router /servers/{uuid}/firmwareInstall [post]
func (r *Routes) firmwareInstall(c *gin.Context) (int, *v1types.ServerResponse) {
	id := c.Param("uuid")
	otelCtx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.firmwareInstall")
	span.SetAttributes(attribute.KeyValue{Key: "serverId", Value: attribute.StringValue(id)})
	defer span.End()

	serverID, err := uuid.Parse(id)
	if err != nil {
		r.logger.WithError(err).WithField("serverID", id).Warn("bad serverID")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "server id: " + err.Error(),
		}
	}

	facilityCode, err := r.serverFacilityCode(otelCtx, serverID)
	if err != nil {
		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "server facility: " + err.Error(),
		}
	}

	var fw rctypes.FirmwareInstallTaskParameters
	if err = c.ShouldBindJSON(&fw); err != nil {
		r.logger.WithError(err).Warn("unmarshal firmwareInstall payload")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid firmware install payload: " + err.Error(),
		}
	}

	serverConditions := r.firmwareInstallComposite(serverID, fw)
<<<<<<< HEAD:pkg/api/v1/conditions/routes/handlers.go
	if err = r.repository.CreateMultiple(otelCtx, serverID, facilityCode, serverConditions.Conditions...); err != nil {
=======
	if err = r.repository.CreateMultiple(otelCtx, serverID, serverConditions.Conditions...); err != nil {
>>>>>>> c85f527 (api/v1: include acquire, release server conditions in composite):pkg/api/v1/routes/handlers.go
		if errors.Is(err, store.ErrActiveCondition) {
			return http.StatusConflict, &v1types.ServerResponse{
				Message: err.Error(),
			}
		}

		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "scheduling condition: " + err.Error(),
		}
	}

<<<<<<< HEAD:pkg/api/v1/conditions/routes/handlers.go
	if err = r.publishCondition(otelCtx, serverID, facilityCode, serverConditions.Conditions[0]); err != nil {
=======
	if err = r.publishCondition(otelCtx, serverID, facilityCode, serverConditions.Conditions[0], false); err != nil {
>>>>>>> c85f527 (api/v1: include acquire, release server conditions in composite):pkg/api/v1/routes/handlers.go
		r.logger.WithField("kind", serverConditions.Conditions[0].Kind).WithError(err).Warn("error publishing condition")
		// mark first condition as failed
		serverConditions.Conditions[0].State = rctypes.Failed
		serverConditions.Conditions[0].Status = failedPublishStatus

		if markErr := r.repository.Update(otelCtx, serverID, serverConditions.Conditions[0]); markErr != nil {
			// an operator is going to have to sort this out
			r.logger.WithError(err).Warn("marking unpublished condition failed")
		}
		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "publishing condition" + err.Error(),
		}
	}

	metrics.ConditionQueued.With(
		prometheus.Labels{"conditionKind": string(rctypes.FirmwareInstall)},
	).Inc()

	return http.StatusOK, &v1types.ServerResponse{
		Message: "firmware install scheduled",
		Records: &v1types.ConditionsResponse{
			ServerID:   serverID,
			State:      rctypes.Pending,
			Conditions: serverConditions.Conditions,
		},
	}
}

func (r *Routes) firmwareInstallComposite(serverID uuid.UUID, fwtp rctypes.FirmwareInstallTaskParameters) *rctypes.ServerConditions {
	createTime := time.Now()
	return &rctypes.ServerConditions{
		ServerID: serverID,
		Conditions: []*rctypes.Condition{
			{
				Kind:       rctypes.FirmwareInstall,
				Version:    rctypes.ConditionStructVersion,
				Parameters: fwtp.MustJSON(),
				State:      rctypes.Pending,
				CreatedAt:  createTime,
			},
			{
				Kind:       rctypes.Inventory,
				Version:    rctypes.ConditionStructVersion,
				Parameters: rctypes.MustDefaultInventoryJSON(serverID),
				State:      rctypes.Pending,
				CreatedAt:  createTime,
			},
		},
	}
}

func (r *Routes) conditionCreate(otelCtx context.Context, newCondition *rctypes.Condition, serverID uuid.UUID, facilityCode string) (int, *v1types.ServerResponse) {
	// Create the new condition
	err := r.repository.CreateMultiple(otelCtx, serverID, facilityCode, newCondition)
	if err != nil {
		r.logger.WithError(err).Info("condition create failed")

		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "condition create: " + err.Error(),
		}
	}

	// publish the condition and in case of publish failure - revert.
	err = r.publishCondition(otelCtx, serverID, facilityCode, newCondition)
	if err != nil {
		r.logger.WithError(err).Warn("condition create failed to publish")

		metrics.PublishErrors.With(
			prometheus.Labels{"conditionKind": string(newCondition.Kind)},
		).Inc()

		newCondition.State = rctypes.Failed
		newCondition.Status = failedPublishStatus

		updateErr := r.repository.Update(otelCtx, serverID, newCondition)
		if updateErr != nil {
			r.logger.WithError(updateErr).Warn("condition deletion failed")
		}

		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "condition create failed: " + err.Error(),
		}
	}

	metrics.ConditionQueued.With(
		prometheus.Labels{"conditionKind": string(newCondition.Kind)},
	).Inc()

	return http.StatusOK, &v1types.ServerResponse{
		Message: "condition set",
		Records: &v1types.ConditionsResponse{
			ServerID: serverID,
			Conditions: []*rctypes.Condition{
				newCondition,
			},
		},
	}
}

// look up server for facility code
func (r *Routes) serverFacilityCode(ctx context.Context, serverID uuid.UUID) (string, error) {
	server, err := r.fleetDBClient.GetServer(ctx, serverID)
	if err != nil {
		r.logger.WithFields(logrus.Fields{
			"error": err,
		}).Info("condition create failed, error in server lookup")

		return "", err
	}

	if server.FacilityCode == "" {
		msg := "condition create failed, Server has no facility code assigned"
		r.logger.Error(msg)

		return "", errors.New(msg)
	}

	return server.FacilityCode, nil
}

func RegisterSpanEvent(span trace.Span, serverID, conditionID, conditionKind, event string) {
	span.AddEvent(event, trace.WithAttributes(
		attribute.String("serverID", serverID),
		attribute.String("conditionID", conditionID),
		attribute.String("conditionKind", conditionKind),
	))
}

func (r *Routes) publishCondition(ctx context.Context, serverID uuid.UUID, facilityCode string, publishCondition *rctypes.Condition) error {
	errPublish := errors.New("error publishing condition")

	otelCtx, span := otel.Tracer(pkgName).Start(
		ctx,
		"Routes.publishCondition",
		trace.WithSpanKind(trace.SpanKindProducer),
	)
	defer span.End()

	metrics.RegisterSpanEvent(
		span,
		serverID.String(),
		publishCondition.ID.String(),
		string(publishCondition.Kind),
		"publishCondition",
	)

	if r.streamBroker == nil {
		return errors.Wrap(errPublish, "not connected to stream broker")
	}

	byt, err := json.Marshal(publishCondition)
	if err != nil {
		return errors.Wrap(errPublish, "condition marshal error: "+err.Error())
	}

	subjectSuffix := fmt.Sprintf("%s.servers.%s", facilityCode, publishCondition.Kind)
	if err := r.streamBroker.Publish(
		otelCtx,
		subjectSuffix,
		byt,
	); err != nil {
		return errors.Wrap(errPublish, err.Error())
	}

	r.logger.WithFields(
		logrus.Fields{
			"serverID":     serverID,
			"facilityCode": facilityCode,
			"conditionID":  publishCondition.ID,
		},
	).Trace("condition published")

	return nil
}

// @Summary Condition Status
// @Tag Conditions
// @Description Returns condition of a server
// @ID conditionStatus
// @Param uuid path string true "Server ID"
// @Accept json
// @Produce json
// @Success 200 {object} v1types.ServerResponse
// Failure 400 {object} v1types.ServerResponse
// Failure 404 {object} v1types.ServerResponse
// Failure 503 {object} v1types.ServerResponse
// @Router /servers/{uuid}/status [get]
func (r *Routes) conditionStatus(c *gin.Context) (int, *v1types.ServerResponse) {
	otelCtx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.conditionStatus")
	span.SetAttributes(
		attribute.KeyValue{
			Key:   "serverId",
			Value: attribute.StringValue(c.Param("uuid")),
		})
	defer span.End()
	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid server id: " + err.Error(),
		}
	}

	cr, err := r.repository.Get(otelCtx, serverID)
	if err != nil {
		if errors.Is(err, store.ErrConditionNotFound) {
			return http.StatusNotFound, &v1types.ServerResponse{
				Message: "condition not found for server",
			}
		}

		return http.StatusServiceUnavailable, &v1types.ServerResponse{
			Message: "condition lookup: " + err.Error(),
		}
	}

	// if we're here cr must be not-nil
	return http.StatusOK, &v1types.ServerResponse{
		Records: &v1types.ConditionsResponse{
			ServerID:   serverID,
			State:      cr.State,
			Conditions: cr.Conditions,
		},
	}
}
