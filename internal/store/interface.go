package store

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/app"
	"github.com/metal-toolbox/conditionorc/internal/model"
	rctypes "github.com/metal-toolbox/rivets/condition"
	"go.hollow.sh/toolbox/events"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// NOTE: when updating this interface, run make gen-store-mock to make sure the mocks are updated.
type Repository interface {
	// Get a condition set on a server.
	// @serverID: required
	// @conditionKind: required
	Get(ctx context.Context, serverID uuid.UUID, conditionKind rctypes.Kind) (*rctypes.Condition, error)

	// Get the currently active condition. If there is nothing active, return nil. Errors only
	// when the underlying storage is unavailable
	// @serverID: required
	GetActiveCondition(ctx context.Context, serverID uuid.UUID) (*rctypes.Condition, error)

	// @serverID: required
	// @condition: required
	Create(ctx context.Context, serverID uuid.UUID, condition *rctypes.Condition) error

	// Create a condition record that encapsulates a unit of work encompassing multiple conditions
	// If you create a condition record with 0 conditions, you don't actually create anything, but
	// no error is returned.
	CreateMultiple(ctx context.Context, serverID uuid.UUID, conditions ...*rctypes.Condition) error

	// Update a condition on a server
	// @serverID: required
	// @condition: required
	Update(ctx context.Context, serverID uuid.UUID, condition *rctypes.Condition) error
}

var (
	pkgName            = "internal/store"
	ErrRepository      = errors.New("storage repository error")
	ErrActiveCondition = errors.New("server has an active condition")
)

func NewStore(config *app.Configuration, logger *logrus.Logger, stream events.Stream) (Repository, error) {

	storeKind := strings.ToLower(string(config.StoreKind))

	switch model.StoreKind(storeKind) {
	case model.NATS:
		return newNatsRepository(logger, stream, config.NatsOptions.KVReplicationFactor)
	default:
		return nil, errors.Wrap(ErrRepository, "storage kind not implemented: "+string(config.StoreKind))
	}
}
