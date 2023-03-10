package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/app"
	"github.com/metal-toolbox/conditionorc/internal/model"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type Repository interface {
	// Ping tests the repository is available.
	Ping(ctx context.Context) error

	// Get a condition set on a server.
	// @serverID: required
	// @conditionKind: required
	Get(ctx context.Context, serverID uuid.UUID, conditionKind ptypes.ConditionKind) (*ptypes.Condition, error)

	// List all conditions set on a server.
	// @serverID: required
	// @conditionState: optional
	List(ctx context.Context, serverID uuid.UUID, conditionState ptypes.ConditionState) ([]*ptypes.Condition, error)

	// Create a condition on a server.
	// @serverID: required
	// @condition: required
	Create(ctx context.Context, serverID uuid.UUID, condition *ptypes.Condition) error

	// Update a condition on a server
	// @serverID: required
	// @condition: required
	Update(ctx context.Context, serverID uuid.UUID, condition *ptypes.Condition) error

	// Delete a condition from a server.
	// @serverID: required
	// @conditionKind: required
	Delete(ctx context.Context, serverID uuid.UUID, conditionKind ptypes.ConditionKind) error
}

var (
	ErrRepository = errors.New("storage repository error")
)

func NewStore(ctx context.Context, config app.Configuration, logger *logrus.Logger) (Repository, error) {
	switch config.StoreKind {
	case model.ServerserviceStore:
		return newServerserviceStore(&config.ServerserviceOptions, logger)
	default:
		return nil, errors.Wrap(ErrRepository, "storage kind not implemented: "+string(config.StoreKind))
	}
}
