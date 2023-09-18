package routes

import (
	"bytes"
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

	"github.com/metal-toolbox/conditionorc/internal/metrics"
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

// @Summary Condition Update
// @Tag Conditions
// @Description Updates a condition on a server
// @ID serverConditionUpdate
// @Param uuid path string true "Server ID"
// @Param conditionKind path string true "Condition Kind"
// @Accept json
// @Produce json
// @Success 200 {object} v1types.ServerResponse
// @Router /v1/servers/{uuid}/condition/{conditionKind} [put]
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

	if ptypes.ConditionStateIsComplete(update.State) {
		metrics.ConditionCompleted.With(
			prometheus.Labels{
				"conditionKind": string(update.Kind),
				"state":         string(update.State),
			},
		).Inc()
	}

	return http.StatusOK, &v1types.ServerResponse{Message: "condition updated"}
}

// XXX: This needs to be refactored. In order to specifically compute the NATS subject for
// the message we need to have some visibility into the actual actions the caller wants us to
// take. Trying to parse this out of a json.RawMessage in the ConditionCreate is too
// ambiguous, as RawBytes has no structure aside being well-formed json.
//
// nolint:gocyclo //TODO: break up this method
// @Summary Condition Create
// @Tag Conditions
// @Description Deletes a condition on a server
// @ID serverConditionCreate
// @Param uuid path string true "Server ID"
// @Param conditionKind path string true "Condition Kind"
// @Accept json
// @Produce json
// @Success 200 {object} v1types.ServerResponse
// @Router /v1/servers/{uuid}/condition/{conditionKind} [post]
func (r *Routes) serverConditionCreate(c *gin.Context) (int, *v1types.ServerResponse) {
	otelCtx, span := otel.Tracer(pkgName).Start(c.Request.Context(), "Routes.serverConditionCreate")
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

	facilityCode, err := r.serverFacilityCode(otelCtx, serverID)
	if err != nil {
		return http.StatusInternalServerError, &v1types.ServerResponse{
			Message: err.Error(),
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

	// publish the condition and in case of publish failure - revert.
	err = r.publishCondition(otelCtx, serverID, facilityCode, condition)
	if err != nil {
		r.logger.WithFields(logrus.Fields{
			"error": err,
		}).Info("condition create failed")

		metrics.PublishErrors.With(
			prometheus.Labels{"conditionKind": string(condition.Kind)},
		).Inc()

		deleteErr := r.repository.Delete(otelCtx, serverID, condition.Kind)
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
		prometheus.Labels{"conditionKind": string(condition.Kind)},
	)

	return http.StatusOK, &v1types.ServerResponse{
		Message: "condition set",
		Records: &v1types.ConditionsResponse{
			ServerID: serverID,
			Conditions: []*ptypes.Condition{
				condition,
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

func RegisterSpanEvent(span trace.Span, serverID, conditionID, conditionKind, event string) {
	span.AddEvent(event, trace.WithAttributes(
		attribute.String("serverID", serverID),
		attribute.String("conditionID", conditionID),
		attribute.String("conditionKind", conditionKind),
	))
}

func (r *Routes) publishCondition(ctx context.Context, serverID uuid.UUID, facilityCode string, condition *ptypes.Condition) error {
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
		condition.ID.String(),
		string(condition.Kind),
		"publishCondition",
	)

	if r.streamBroker == nil {
		return errors.Wrap(errPublish, "not connected to stream broker")
	}

	byt, err := json.Marshal(condition)
	if err != nil {
		return errors.Wrap(errPublish, "condition marshal error: "+err.Error())
	}

	subjectSuffix := fmt.Sprintf("%s.servers.%s", facilityCode, condition.Kind)
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
			"conditionID":  condition.ID,
		},
	).Trace("condition published")

	return nil
}

// @Summary Condition Delete
// @Tag Conditions
// @Description Deletes a condition on a server
// @ID serverConditionDelete
// @Param uuid path string true "Server ID"
// @Param conditionKind path string true "Condition Kind"
// @Accept json
// @Produce json
// @Success 200 {object} v1types.ServerResponse
// @Router /v1/servers/{uuid}/condition/{conditionKind} [delete]
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

// @Summary Condition List
// @Tag Conditions
// @Description Returns all conditions set on a server by the condition state.
// @ID serverConditionList
// @Param uuid path string true "Server ID"
// @Param conditionState path string true "Condition State"
// @Accept json
// @Produce json
// @Success 200 {object} v1types.ServerResponse
// @Router /v1/servers/{uuid}/state/{conditionState} [get]
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

// @Summary Condition Get
// @Tag Conditions
// @Description Returns condition of a server
// @ID serverConditionGet
// @Param uuid path string true "Server ID"
// @Param conditionKind path string true "Condition Kind"
// @Accept json
// @Produce json
// @Success 200 {object} v1types.ServerResponse
// @Router /v1/servers/{uuid}/condition/{conditionKind} [get]
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
