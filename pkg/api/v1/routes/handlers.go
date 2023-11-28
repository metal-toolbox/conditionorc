package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
)

const (
	badRequestErrMsg = "response code: 400"
)

var (
	ErrConditionParameter = errors.New("error in condition parameter")
	ErrConditionExclusive = errors.New("exclusive condition present")
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
	if err != nil {
		return http.StatusServiceUnavailable, &v1types.ServerResponse{
			Message: "error checking server state: " + err.Error(),
		}
	}
	if active != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "server has an active condition",
		}
	}

	newCondition := conditionCreate.NewCondition(kind)

	return r.conditionCreate(otelCtx, newCondition, serverID, facilityCode)
}

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
	if err != nil {
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
		Message: "server detele",
		Records: &v1types.ConditionsResponse{ServerID: serverID},
	}
}

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
				Message: err.Error(),
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
				Message: err.Error() + fmt.Sprintf("server rollback err: %v", rollbackErr),
			}
		}
		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: err.Error() + fmt.Sprintf("server rollback err: %v", rollbackErr),
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
	newCondition := conditionCreate.NewCondition(rctypes.Inventory)

	st, resp := r.conditionCreate(otelCtx, newCondition, serverID, params.Facility)
	if st != http.StatusOK {
		rollbackErr := rollback()
		resp.Message += fmt.Sprintf("server rollback err: %v", rollbackErr)
	}
	return st, resp
}

func (r *Routes) conditionCreate(otelCtx context.Context, newCondition *rctypes.Condition, serverID uuid.UUID, facilityCode string) (int, *v1types.ServerResponse) {
	// Create the new condition
	err := r.repository.Create(otelCtx, serverID, newCondition)
	if err != nil {
		r.logger.WithFields(logrus.Fields{
			"error": err,
		}).Info("condition create failed")

		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: err.Error(),
		}
	}

	// publish the condition and in case of publish failure - revert.
	err = r.publishCondition(otelCtx, serverID, facilityCode, newCondition)
	if err != nil {
		r.logger.WithFields(logrus.Fields{
			"error": err,
		}).Info("condition create failed")

		metrics.PublishErrors.With(
			prometheus.Labels{"conditionKind": string(newCondition.Kind)},
		).Inc()

		deleteErr := r.repository.Delete(otelCtx, serverID, newCondition.Kind)
		if deleteErr != nil {
			r.logger.WithFields(logrus.Fields{
				"error": deleteErr,
			}).Info("condition deletion failed")

			return http.StatusInternalServerError, &v1types.ServerResponse{
				Message: deleteErr.Error(),
			}
		}

		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: "condition create failed: " + err.Error(),
		}
	}

	metrics.ConditionQueued.With(
		prometheus.Labels{"conditionKind": string(newCondition.Kind)},
	)

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

// @Summary Condition Get
// @Tag Conditions
// @Description Returns condition of a server
// @ID serverConditionGet
// @Param uuid path string true "Server ID"
// @Param conditionKind path string true "Condition Kind"
// @Accept json
// @Produce json
// @Success 200 {object} v1types.ServerResponse
// Failure 400 {object} v1types.ServerResponse
// Failure 404 {object} v1types.ServerResponse
// Failure 503 {object} v1types.ServerResponse
// @Router /servers/{uuid}/condition/{conditionKind} [get]
func (r *Routes) serverConditionGet(c *gin.Context) (int, *v1types.ServerResponse) {
	otelCtx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.serverConditionGet")
	span.SetAttributes(
		attribute.KeyValue{Key: "conditionKind", Value: attribute.StringValue(c.Param("conditionKind"))},
		attribute.KeyValue{Key: "serverId", Value: attribute.StringValue(c.Param("uuid"))})
	defer span.End()
	serverID, err := uuid.Parse(c.Param("uuid"))
	if err != nil {
		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: err.Error(),
		}
	}

	kind := rctypes.Kind(c.Param("conditionKind"))
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
			Conditions: []*rctypes.Condition{
				found,
			},
		},
	}
}
