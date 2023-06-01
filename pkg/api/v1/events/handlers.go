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
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
)

const (
	pkgName = "pkg/api/v1/events"
)

var (
	errRetryThis = errors.New("retry this operation")

	serverServiceEventPrefix = "com.hollow.sh.serverservice.events."
	controllerResponsePrefix = "com.hollow.sh.controllers.responses."

	serverCreate = "server.create"
)

//nolint:unused // we'll use this in the very near future
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

// ControllerEvent handles events from controllers
func (h *Handler) ControllerEvent(ctx context.Context, msg events.Message) {
	_, span := otel.Tracer(pkgName).Start(ctx, "ControllerEvent")
	defer span.End()

	subject := msg.Subject()
	fragment := strings.TrimPrefix(subject, controllerResponsePrefix)
	//nolint:gomnd // useless, generic optinion
	frags := strings.SplitN(fragment, ".", 2)

	facility := "no facility"
	act := "default"

	if len(frags) > 1 {
		facility = frags[0]
		act = frags[1]
	}

	h.logger.WithFields(
		logrus.Fields{
			"sub":      subject,
			"facility": facility,
			"act":      act,
		},
	).Debug("received controller event")

	switch act {
	case string(ptypes.ConditionUpdateEvent):
		var updateEvt v1types.ConditionUpdateEvent
		if err := json.Unmarshal(msg.Data(), &updateEvt); err != nil {
			h.logger.WithError(err).Warn("bogus condition update message")
			h.ackEvent(msg)
			return
		}
		err := h.updateCondition(ctx, &updateEvt)
		switch {
		case errors.Is(err, errRetryThis):
			h.nakEvent(msg)
		default:
			h.ackEvent(msg)
		}

	default:
		h.logger.WithFields(
			logrus.Fields{
				"subject":  subject,
				"facility": facility,
				"act":      act,
			},
		).Warn("msg with unsupported EventType ignored")
		h.ackEvent(msg)
		return
	}
}

func (h *Handler) updateCondition(ctx context.Context, updEvt *v1types.ConditionUpdateEvent) error {
	if err := updEvt.Validate(); err != nil {
		h.logger.WithError(err).WithFields(logrus.Fields{
			"server_id":      updEvt.ConditionUpdate.ServerID,
			"condition_kind": updEvt.Kind,
		}).Error("conditionUpdateEvent validate error")
		return err
	}

	// query existing condition
	existing, err := h.repository.Get(ctx, updEvt.ConditionUpdate.ServerID, updEvt.Kind)
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

	if existing == nil {
		h.logger.WithFields(logrus.Fields{
			"serverID":      updEvt.ConditionUpdate.ServerID,
			"conditionKind": updEvt.Kind,
		}).Error("no existing condition found for update")
		return errors.New("nil existing condition with no error on retrieve")
	}

	// nothing to update
	if existing.State == updEvt.ConditionUpdate.State &&
		bytes.Equal(existing.Status, updEvt.ConditionUpdate.Status) {
		return nil
	}

	// merge update with existing
	revisedCondition, err := updEvt.MergeExisting(existing, false)
	if err != nil {
		h.logger.WithError(err).WithFields(logrus.Fields{
			"serverID":         updEvt.ConditionUpdate.ServerID,
			"conditionKind":    updEvt.Kind,
			"incoming_state":   updEvt.State,
			"incoming_version": updEvt.ResourceVersion,
			"existing_state":   existing.State,
			"existing_version": existing.ResourceVersion,
		}).Warn("condition merge failed")
		return err
	}

	// update
	if err := h.repository.Update(ctx, updEvt.ConditionUpdate.ConditionID, revisedCondition); err != nil {
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

		condition := &ptypes.Condition{
			ID:         uuid.New(),
			Kind:       ptypes.InventoryOutofband, // TODO: change, once the condition types package is moved into a shared package
			State:      ptypes.Pending,
			Exclusive:  false,
			Parameters: byt, // pass the incoming message data to Alloy
		}

		if err := h.repository.Create(otelCtx, uuid.MustParse(incoming.ID), condition); err != nil {
			h.logger.WithError(err).Error("error creating condition on server")
			h.nakEvent(ev)
			return
		}

		inventorySubject := fmt.Sprintf("servers.%s.inventory.outofband", string(facility))
		if err := h.stream.Publish(otelCtx, inventorySubject, condition.MustBytes()); err != nil {
			h.logger.WithError(err).WithFields(
				logrus.Fields{"condition_id": condition.ID},
			).Error("condition publish returned an error")
			h.nakEvent(ev) // XXX: repeated Creates *must not* fail or this message will give us an explosion of errors
			return
		}

		h.logger.WithFields(
			logrus.Fields{
				"id":        condition.ID,
				"condition": condition.Kind,
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
