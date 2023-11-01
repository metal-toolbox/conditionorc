package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/metal-toolbox/conditionorc/internal/fleetdb"
	"github.com/metal-toolbox/conditionorc/internal/metrics"
	"github.com/metal-toolbox/conditionorc/internal/store"
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
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

	newCondition := conditionCreate.NewCondition(kind)

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

	facilityCode, err := r.serverFacilityCode(otelCtx, serverID)
	if err != nil {
		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: err.Error(),
		}
	}

	return r.conditionCreate(otelCtx, newCondition, serverID, facilityCode)
}

func (r *Routes) serverEnroll(c *gin.Context) (int, *v1types.ServerResponse) {
	id := c.Param("uuid")
	ip := c.Param("ip")
	username := c.Param("user")
	password := c.Param("pwd")
	facilityCode := c.Param("facility")

	otelCtx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.serverEnroll")
	span.SetAttributes(
		attribute.KeyValue{Key: "serverId", Value: attribute.StringValue(id)},
		attribute.KeyValue{Key: "IP", Value: attribute.StringValue(ip)},
		attribute.KeyValue{Key: "user", Value: attribute.StringValue(username)})
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

	// Creates a server record in FleetDB.
	if err = r.fleetDBClient.AddServer(c.Request.Context(), serverID, facilityCode, ip, username, password); err != nil {
		if errors.Is(err, fleetdb.ErrServerAlreadyExist) {
			return http.StatusBadRequest, &v1types.ServerResponse{
				Message: err.Error(),
			}
		}
		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: err.Error(),
		}
	}

	// Reuse the inventory for server enrollment.
	// May change in the future.
	kind := rctypes.Kind(rctypes.Inventory)

	var conditionCreate v1types.ConditionCreate
	if err := c.ShouldBindJSON(&conditionCreate); err != nil {
		r.logger.WithFields(logrus.Fields{
			"error": err,
		}).Info("invalid ConditionCreate payload")

		return http.StatusBadRequest, &v1types.ServerResponse{
			Message: "invalid ConditionCreate payload: " + err.Error(),
		}
	}

	newCondition := conditionCreate.NewCondition(kind)

	return r.conditionCreate(otelCtx, newCondition, serverID, facilityCode)
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
	server, err := r.repository.GetServer(ctx, serverID)
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

func (r *Routes) exclusiveNonFinalConditionExists(ctx context.Context, serverID uuid.UUID) error {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "Routes.exclusiveNonFinalConditionExists")
	defer span.End()
	// XXX: Clean me up! We're looking for any active condition here, so don't list, do a store lookup
	// here. Any active condition here is enough to block queuing a new one.
	for _, state := range rctypes.States() {
		if rctypes.StateIsComplete(state) {
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

		for _, existingCondition := range existing {
			if existingCondition.Exclusive && existingCondition.State == state {
				r.logger.WithFields(logrus.Fields{
					"serverID":    serverID.String(),
					"conditionID": existingCondition.ID.String(),
					"kind":        existingCondition.Kind,
					"state":       existingCondition.State,
				}).Info("existing exclusive server condition found")
				return errors.Wrap(
					ErrConditionExclusive,
					fmt.Sprintf("%s:%s condition exists in an incomplete state - %s",
						existingCondition.ID.String(), existingCondition.Kind, string(existingCondition.State)),
				)
			}
		}
	}

	return nil
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
