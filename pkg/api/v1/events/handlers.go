package events

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/metrics"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	serversvc "go.hollow.sh/serverservice/pkg/api/v1"
	"go.hollow.sh/toolbox/events"
	"go.opentelemetry.io/otel"

	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
)

const (
	pkgName = "pkg/api/v1/events"
)

var (
	errRetryThis = errors.New("retry this operation")

	serverServiceEventPrefix = "com.hollow.sh.serverservice.events."

	serverCreate = "server.create"
)

//nolint:unused // we'll use this in the very near future
func eventsError(operation string) {
	metrics.DependencyError("events", operation)
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

func (h *Handler) ackEvent(m events.Message) {
	if err := m.Ack(); err != nil {
		h.logger.WithError(err).Warn("event ack error")
	}
}

func (h *Handler) nakEvent(m events.Message) {
	if err := m.Nak(); err != nil {
		h.logger.WithError(err).Warn("event nak error")
	}
}

// UpdateCondition sanity checks the incoming condition update, merges it and applies the result to serverservice
func (h *Handler) UpdateCondition(ctx context.Context, updEvt *v1types.ConditionUpdateEvent) error {
	_, span := otel.Tracer(pkgName).Start(ctx, "events.UpdateCondition")
	defer span.End()

	metrics.RegisterSpanEvent(
		span,
		updEvt.ServerID.String(),
		updEvt.ConditionID.String(),
		string(updEvt.Kind),
		"updateCondition",
	)

	if err := updEvt.Validate(); err != nil {
		h.logger.WithError(err).WithFields(logrus.Fields{
			"server_id":      updEvt.ConditionUpdate.ServerID,
			"condition_kind": updEvt.Kind,
		}).Error("conditionUpdateEvent validate error")
		return err
	}

	// query existing condition
	existing, err := h.repository.GetActiveCondition(ctx, updEvt.ConditionUpdate.ServerID)
	if err != nil {
		if errors.Is(err, store.ErrConditionNotFound) {
			h.logger.WithFields(logrus.Fields{
				"serverID":      updEvt.ConditionUpdate.ServerID,
				"conditionKind": updEvt.Kind,
			}).Error("no existing condition found for update")
			return err
		}
		return errors.Wrap(errRetryThis, err.Error())
	}

	// nothing to update
	if existing.State == updEvt.ConditionUpdate.State &&
		bytes.Equal(existing.Status, updEvt.ConditionUpdate.Status) {
		return nil
	}

	// merge update with existing
	revisedCondition, err := updEvt.MergeExisting(existing)
	if err != nil {
		h.logger.WithError(err).WithFields(logrus.Fields{
			"serverID":         updEvt.ConditionUpdate.ServerID,
			"conditionKind":    updEvt.Kind,
			"incoming_state":   updEvt.State,
			"existing_state":   existing.State,
			"existing_version": existing.ResourceVersion,
		}).Warn("condition merge failed")
		return err
	}

	// update
	if err := h.repository.Update(ctx, updEvt.ServerID, revisedCondition); err != nil {
		h.logger.WithError(err).WithFields(logrus.Fields{
			"serverID":          updEvt.ConditionUpdate.ServerID,
			"conditionID":       revisedCondition.ID,
			"condition_version": revisedCondition.ResourceVersion,
		}).Info("condition update failed")
		return errors.Wrap(errRetryThis, err.Error())
	}
	return nil
}

// ServerserviceEvent handles serverservice events.
func (h *Handler) ServerserviceEvent(ctx context.Context, ev events.Message) {
	otelCtx, span := otel.Tracer(pkgName).Start(ctx, "ServerserviceEvent")
	defer span.End()

	subject := ev.Subject()
	act := strings.TrimPrefix(subject, serverServiceEventPrefix)

	h.logger.WithFields(
		logrus.Fields{
			"event_subject": subject,
			"act":           act,
		},
	).Debug("received serverservice event")

	switch act {
	case serverCreate:
		var incoming serversvc.CreateServer
		byt := ev.Data()
		if err := json.Unmarshal(byt, &incoming); err != nil {
			h.logger.WithError(err).Warn("bogus server create message")
			h.ackEvent(ev)
			return
		}

		facility, _ := incoming.FacilityCode.MarshalText()
		if len(facility) == 0 {
			h.logger.Warn("incoming server create has a null facility")
			h.ackEvent(ev)
			return
		}

		conditionFromEvent := &rctypes.Condition{
			ID:         uuid.New(),
			Kind:       rctypes.Inventory,
			State:      rctypes.Pending,
			Exclusive:  false,
			Parameters: byt, // pass the incoming message data to Alloy
		}

		if err := h.repository.Create(otelCtx, uuid.MustParse(incoming.ID), conditionFromEvent); err != nil {
			h.logger.WithError(err).Error("error creating condition on server")
			h.nakEvent(ev)
			return
		}

		inventorySubject := fmt.Sprintf("servers.%s.inventory.outofband", string(facility))
		if err := h.stream.Publish(otelCtx, inventorySubject, conditionFromEvent.MustBytes()); err != nil {
			h.logger.WithError(err).WithFields(
				logrus.Fields{"condition_id": conditionFromEvent.ID},
			).Error("condition publish returned an error")
			h.nakEvent(ev) // XXX: repeated Creates *must not* fail or this message will give us an explosion of errors
			return
		}

		h.logger.WithFields(
			logrus.Fields{
				"id":        conditionFromEvent.ID,
				"condition": conditionFromEvent.Kind,
			},
		).Info("condition published")
		h.ackEvent(ev)

	default:
		h.logger.WithFields(
			logrus.Fields{"eventType": act},
		).Error("msg with unknown eventType ignored")
		h.ackEvent(ev)
	}
}
