package events

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/metal-toolbox/conditionorc/internal/store"
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"

	rctypes "github.com/metal-toolbox/rivets/condition"
	eventsm "github.com/metal-toolbox/rivets/events"
)

func TestUpdateEvent(t *testing.T) {
	t.Parallel()
	t.Run("invalid update", func(t *testing.T) {
		t.Parallel()

		stream := eventsm.NewMockStream(t)
		repo := store.NewMockRepository(t)

		handler := NewHandler(repo, stream, logrus.New())

		arg := v1types.ConditionUpdateEvent{}
		err := handler.UpdateCondition(context.TODO(), &arg)
		require.ErrorIs(t, err, v1types.ErrUpdatePayload)
	})
	t.Run("read for existing condition fails", func(t *testing.T) {
		t.Parallel()

		stream := eventsm.NewMockStream(t)
		repo := store.NewMockRepository(t)

		handler := NewHandler(repo, stream, logrus.New())
		serverID := uuid.New()
		testKind := rctypes.Kind("test")
		conditionID := uuid.New()
		status := []byte(`{ "msg":"it's happening!" }`)

		repo.On("GetActiveCondition", mock.Anything, serverID).
			Return(nil, errors.New("not today")).Once()

		arg := v1types.ConditionUpdateEvent{
			Kind: testKind,
			ConditionUpdate: v1types.ConditionUpdate{
				ConditionID: conditionID,
				ServerID:    serverID,
				State:       rctypes.Active,
				Status:      status,
			},
		}
		err := handler.UpdateCondition(context.TODO(), &arg)
		require.ErrorIs(t, err, errRetryThis)
	})
	t.Run("no existing condition", func(t *testing.T) {
		t.Parallel()

		stream := eventsm.NewMockStream(t)
		repo := store.NewMockRepository(t)

		handler := NewHandler(repo, stream, logrus.New())
		serverID := uuid.New()
		testKind := rctypes.Kind("test")
		conditionID := uuid.New()
		status := []byte(`{ "msg":"it's happening!" }`)

		repo.On("GetActiveCondition", mock.Anything, serverID).
			Return(nil, store.ErrConditionNotFound).Once()

		arg := v1types.ConditionUpdateEvent{
			Kind: testKind,
			ConditionUpdate: v1types.ConditionUpdate{
				ConditionID: conditionID,
				ServerID:    serverID,
				State:       rctypes.Active,
				Status:      status,
			},
		}
		err := handler.UpdateCondition(context.TODO(), &arg)
		require.ErrorIs(t, err, store.ErrConditionNotFound)
	})
	t.Run("no-op update", func(t *testing.T) {
		t.Parallel()

		stream := eventsm.NewMockStream(t)
		repo := store.NewMockRepository(t)

		handler := NewHandler(repo, stream, logrus.New())
		serverID := uuid.New()
		testKind := rctypes.Kind("test")
		conditionID := uuid.New()
		status := []byte(`{ "msg":"it's happening!" }`)

		repo.On("GetActiveCondition", mock.Anything, serverID).
			Return(&rctypes.Condition{
				ID:     conditionID,
				Kind:   testKind,
				State:  rctypes.Active,
				Status: status,
			}, nil).Once()

		arg := v1types.ConditionUpdateEvent{
			Kind: testKind,
			ConditionUpdate: v1types.ConditionUpdate{
				ConditionID: conditionID,
				ServerID:    serverID,
				State:       rctypes.Active,
				Status:      status,
			},
		}
		err := handler.UpdateCondition(context.TODO(), &arg)
		require.NoError(t, err)
	})
	t.Run("update fails", func(t *testing.T) {
		t.Parallel()

		stream := eventsm.NewMockStream(t)
		repo := store.NewMockRepository(t)

		handler := NewHandler(repo, stream, logrus.New())
		serverID := uuid.New()
		testKind := rctypes.Kind("test")
		conditionID := uuid.New()

		get := repo.On("GetActiveCondition", mock.Anything, serverID).
			Return(&rctypes.Condition{
				ID:     conditionID,
				Kind:   testKind,
				Target: serverID,
				State:  rctypes.Active,
				Status: []byte(`{ "msg":"still waiting" }`),
			}, nil).Once()

		update := repo.On("Update", mock.Anything, serverID, mock.Anything).
			Return(errors.New("not happening")).Once()

		// get before update
		update.NotBefore(get)

		arg := v1types.ConditionUpdateEvent{
			Kind: testKind,
			ConditionUpdate: v1types.ConditionUpdate{
				ConditionID: conditionID,
				ServerID:    serverID,
				State:       rctypes.Active,
				Status:      []byte(`{ "msg":"it's happening!" }`),
			},
		}
		err := handler.UpdateCondition(context.TODO(), &arg)
		require.ErrorIs(t, err, errRetryThis)
	})
	t.Run("update succeeds", func(t *testing.T) {
		t.Parallel()

		stream := eventsm.NewMockStream(t)
		repo := store.NewMockRepository(t)

		handler := NewHandler(repo, stream, logrus.New())
		serverID := uuid.New()
		testKind := rctypes.Kind("test")
		conditionID := uuid.New()

		get := repo.On("GetActiveCondition", mock.Anything, serverID).
			Return(&rctypes.Condition{
				ID:     conditionID,
				Kind:   testKind,
				Target: serverID,
				State:  rctypes.Active,
				Status: []byte(`{ "msg":"still waiting" }`),
			}, nil).Once()

		update := repo.On("Update", mock.Anything, serverID, mock.Anything).
			Return(nil).Once()

		update.NotBefore(get)

		arg := v1types.ConditionUpdateEvent{
			Kind: testKind,
			ConditionUpdate: v1types.ConditionUpdate{
				ConditionID: conditionID,
				ServerID:    serverID,
				State:       rctypes.Active,
				Status:      []byte(`{ "msg":"it's happening!" }`),
			},
		}
		err := handler.UpdateCondition(context.TODO(), &arg)
		require.NoError(t, err)
	})
}
