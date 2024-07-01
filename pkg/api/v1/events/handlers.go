package events

import (
	"bytes"
	"context"

	"github.com/metal-toolbox/conditionorc/internal/metrics"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/metal-toolbox/rivets/events"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"

	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
)

const (
	pkgName = "pkg/api/v1/events"
)

var (
	errRetryThis = errors.New("retry this operation")
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
				"server_id":      updEvt.ConditionUpdate.ServerID,
				"condition_id":   updEvt.ConditionUpdate.ConditionID,
				"condition_kind": updEvt.Kind,
			}).Error("no existing pending/active condition found for update")
			return err
		}
		return errors.Wrap(errRetryThis, err.Error())
	}

	// apply update on changes to one of these fields
	if existing.State == updEvt.ConditionUpdate.State &&
		// TODO: fix - if the JSON payload key/values are not in the same order, this will return true
		bytes.Equal(existing.Status, updEvt.ConditionUpdate.Status) &&
		existing.UpdatedAt == updEvt.UpdatedAt {
		return nil
	}

	// merge update with existing
	revisedCondition, err := updEvt.MergeExisting(existing)
	if err != nil {
		h.logger.WithError(err).WithFields(logrus.Fields{
			"serverID":       updEvt.ConditionUpdate.ServerID,
			"conditionKind":  updEvt.Kind,
			"incoming_state": updEvt.State,
			"existing_state": existing.State,
		}).Warn("condition merge failed")
		return err
	}

	// update
	if err := h.repository.Update(ctx, updEvt.ServerID, revisedCondition); err != nil {
		h.logger.WithError(err).WithFields(logrus.Fields{
			"serverID":    updEvt.ConditionUpdate.ServerID,
			"conditionID": revisedCondition.ID,
		}).Info("condition update failed")
		return errors.Wrap(errRetryThis, err.Error())
	}

	return nil
}
