package events

import (
	"bytes"
	"context"

	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/metrics"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.hollow.sh/toolbox/events"
	"go.opentelemetry.io/otel"

	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
)

const (
	pkgName = "pkg/api/v1/events"
)

var (
	errPublishEvent = errors.New("error publishing event")
)

func eventsError() {
	metrics.DependencyError("events")
}

// Handler holds methods to handle events.
type Handler struct {
	logger     *logrus.Logger
	repository store.Repository
	stream     events.Stream
}

func NewHandler(repository store.Repository, stream events.Stream, logger *logrus.Logger) *Handler {
	return &Handler{repository: repository, stream: stream, logger: logger}
}

func (h *Handler) ackEvent(e *ptypes.StreamEvent) {
	if err := e.Event.Ack(); err != nil {
		h.logger.WithError(err).Warn("event ack error")
	}
}

func (h *Handler) nakEvent(e *ptypes.StreamEvent) {
	if err := e.Event.Nak(); err != nil {
		h.logger.WithError(err).Warn("event nak error")
	}
}

// ControllerEvent handles events from controllers
func (h *Handler) ControllerEvent(ctx context.Context, streamEvent *ptypes.StreamEvent) {
	_, span := otel.Tracer(pkgName).Start(ctx, "ControllerEvent")
	defer span.End()

	h.logger.WithFields(
		logrus.Fields{
			"source":   streamEvent.Data.Source,
			"resource": streamEvent.URN.ResourceType,
		},
	).Trace("received controller event")

	switch streamEvent.Data.EventType {
	case string(ptypes.ConditionUpdateEvent):
		h.updateCondition(ctx, streamEvent)

	default:
		h.logger.WithFields(
			logrus.Fields{
				"source":    streamEvent.Data.Source,
				"resource":  streamEvent.URN.ResourceType,
				"serverID":  streamEvent.URN.ResourceID,
				"eventType": streamEvent.Data.EventType,
			},
		).Warn("msg with unsupported EventType ignored")

		return
	}
}

func (h *Handler) updateCondition(ctx context.Context, streamEvent *ptypes.StreamEvent) {
	var conditionUpdate v1types.ConditionUpdateEvent

	serverID := streamEvent.URN.ResourceID

	if err := streamEvent.UnmarshalAdditionalData(&conditionUpdate); err != nil {
		h.logger.WithFields(logrus.Fields{
			"err":      err,
			"source":   streamEvent.Data.Source,
			"urn":      streamEvent.URN.Namespace,
			"serverID": serverID,
		}).Error("unmarshal conditionUpdateEvent failed")

		h.ackEvent(streamEvent)

		return
	}

	if err := conditionUpdate.Validate(); err != nil {
		h.logger.WithFields(logrus.Fields{
			"err":           err,
			"source":        streamEvent.Data.Source,
			"urn":           streamEvent.URN.Namespace,
			"serverID":      serverID,
			"conditionKind": conditionUpdate.Kind,
		}).Error("conditionUpdateEvent validate error")

		h.ackEvent(streamEvent)

		return
	}

	// query existing condition
	existing, err := h.repository.Get(ctx, streamEvent.URN.ResourceID, conditionUpdate.Kind)
	if err != nil {
		if errors.Is(err, store.ErrConditionNotFound) {
			h.logger.WithFields(logrus.Fields{
				"source":        streamEvent.Data.Source,
				"urn":           streamEvent.URN.Namespace,
				"serverID":      serverID,
				"conditionKind": conditionUpdate.Kind,
			}).Error("no existing condition found for update")

			h.ackEvent(streamEvent)

			return
		}

		// nak event so its retried incase its a store repository issue.
		h.nakEvent(streamEvent)

		return
	}

	if existing == nil {
		h.logger.WithFields(logrus.Fields{
			"source":        streamEvent.Data.Source,
			"urn":           streamEvent.URN.Namespace,
			"serverID":      serverID,
			"conditionKind": conditionUpdate.Kind,
		}).Error("no existing condition found for update")

		h.ackEvent(streamEvent)

		return
	}

	// nothing to update
	// XXX: consider just doing the update unconditionally?
	if existing.State == conditionUpdate.State && bytes.Equal(existing.Status, conditionUpdate.Status) {
		return
	}

	// merge update with existing
	update, err := conditionUpdate.MergeExisting(existing, false)
	if err != nil {
		h.logger.WithFields(logrus.Fields{
			"error":            err,
			"serverID":         serverID,
			"incoming_state":   conditionUpdate.State,
			"incoming_version": conditionUpdate.ResourceVersion,
			"existing_state":   existing.State,
			"existing_version": existing.ResourceVersion,
		}).Info("condition merge failed")

		// XXX: should this be an nak, instead of a ack.
		h.ackEvent(streamEvent)

		return
	}

	// update
	if err := h.repository.Update(ctx, streamEvent.URN.ResourceID, update); err != nil {
		h.logger.WithFields(logrus.Fields{
			"error":             err,
			"serverID":          serverID,
			"conditionID":       update.ID,
			"condition_version": update.ResourceVersion,
		}).Info("condition update failed")

		// XXX: should this be an nak, instead of a ack.
		h.ackEvent(streamEvent)

		return
	}
}

// ServerserviceEvent handles serverservice events.
func (h *Handler) ServerserviceEvent(ctx context.Context, streamEvent *ptypes.StreamEvent) {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "ServerserviceEvent")
	defer span.End()

	h.logger.WithFields(
		logrus.Fields{
			"source":   streamEvent.Data.Source,
			"resource": streamEvent.URN.ResourceType,
		},
	).Trace("received serverservice event")

	switch streamEvent.Data.EventType {
	case string(events.Create):
		condition := &ptypes.Condition{
			Kind:      ptypes.InventoryOutofband, // TODO: change, once the condition types package is moved into a shared package
			State:     ptypes.Pending,
			Exclusive: false,
		}

		if err := h.repository.Create(otelCtx, streamEvent.URN.ResourceID, condition); err != nil {
			h.logger.WithFields(
				logrus.Fields{"err": err.Error()},
			).Error("error creating condition on server")

			return
		}

		if errPublish := h.publish(otelCtx, streamEvent.URN.ResourceID, ptypes.InventoryOutofband, condition); errPublish != nil {
			h.logger.WithFields(
				logrus.Fields{"err": errPublish.Error()},
				// TODO: log condition ID here
			).Error("condition publish returned an error")

			return
		}

		h.logger.WithFields(
			logrus.Fields{
				"condition": condition.Kind,
			},
		).Info("condition published")

	default:
		h.logger.WithFields(
			logrus.Fields{"eventType": streamEvent.Data.EventType, "urn ns": streamEvent.URN.Namespace},
		).Error("msg with unknown eventType ignored")
	}
}

func (h *Handler) publish(ctx context.Context, serverID uuid.UUID, conditionKind ptypes.ConditionKind, obj interface{}) error {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "publishCondition")
	defer span.End()

	if h.stream == nil {
		return errors.Wrap(errPublishEvent, "not connected to event stream")
	}

	if err := h.stream.PublishAsyncWithContext(
		otelCtx,
		events.ResourceType(ptypes.ServerResourceType),
		events.EventType(conditionKind),
		serverID.String(),
		obj,
	); err != nil {
		eventsError()

		return errors.Wrap(errPublishEvent, err.Error())
	}

	return nil
}
