package events

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/metal-toolbox/conditionorc/internal/store"
	storeTest "github.com/metal-toolbox/conditionorc/internal/store/test"
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"

	rctypes "github.com/metal-toolbox/rivets/condition"
	mock_events "go.hollow.sh/toolbox/events/mock"
)

func TestUpdateEvent(t *testing.T) {
	t.Parallel()
	t.Run("invalid update", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		repo := storeTest.NewMockRepository(ctrl)

		handler := NewHandler(repo, stream, logrus.New())

		arg := v1types.ConditionUpdateEvent{}
		err := handler.UpdateCondition(context.TODO(), &arg)
		require.ErrorIs(t, err, v1types.ErrUpdatePayload)
	})
	t.Run("read for existing condition fails", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		repo := storeTest.NewMockRepository(ctrl)

		handler := NewHandler(repo, stream, logrus.New())
		serverID := uuid.New()
		testKind := rctypes.Kind("test")
		conditionID := uuid.New()
		status := []byte(`{ "msg":"it's happening!" }`)
		gomock.InOrder(
			repo.EXPECT().GetActiveCondition(
				gomock.Any(),
				serverID,
			).Times(1).Return(nil, errors.New("not today")),
		)

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
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		repo := storeTest.NewMockRepository(ctrl)

		handler := NewHandler(repo, stream, logrus.New())
		serverID := uuid.New()
		testKind := rctypes.Kind("test")
		conditionID := uuid.New()
		status := []byte(`{ "msg":"it's happening!" }`)
		gomock.InOrder(
			repo.EXPECT().GetActiveCondition(
				gomock.Any(),
				serverID,
			).Times(1).Return(nil, store.ErrConditionNotFound),
		)

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
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		repo := storeTest.NewMockRepository(ctrl)

		handler := NewHandler(repo, stream, logrus.New())
		serverID := uuid.New()
		testKind := rctypes.Kind("test")
		conditionID := uuid.New()
		status := []byte(`{ "msg":"it's happening!" }`)
		gomock.InOrder(
			repo.EXPECT().GetActiveCondition(
				gomock.Any(),
				serverID,
			).Times(1).Return(&rctypes.Condition{
				ID:     conditionID,
				Kind:   testKind,
				State:  rctypes.Active,
				Status: status,
			}, nil),
		)

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
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		repo := storeTest.NewMockRepository(ctrl)

		handler := NewHandler(repo, stream, logrus.New())
		serverID := uuid.New()
		testKind := rctypes.Kind("test")
		conditionID := uuid.New()

		gomock.InOrder(
			repo.EXPECT().GetActiveCondition(
				gomock.Any(),
				serverID,
			).Times(1).Return(&rctypes.Condition{
				ID:     conditionID,
				Kind:   testKind,
				State:  rctypes.Active,
				Status: []byte(`{ "msg":"still waiting" }`),
			}, nil),
			repo.EXPECT().Update(
				gomock.Any(),
				serverID,
				gomock.Any(),
			).Times(1).Return(errors.New("not happening")),
		)

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
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		repo := storeTest.NewMockRepository(ctrl)

		handler := NewHandler(repo, stream, logrus.New())
		serverID := uuid.New()
		testKind := rctypes.Kind("test")
		conditionID := uuid.New()

		gomock.InOrder(
			repo.EXPECT().GetActiveCondition(
				gomock.Any(),
				serverID,
			).Times(1).Return(&rctypes.Condition{
				ID:     conditionID,
				Kind:   testKind,
				State:  rctypes.Active,
				Status: []byte(`{ "msg":"still waiting" }`),
			}, nil),
			repo.EXPECT().Update(
				gomock.Any(),
				serverID,
				gomock.Any(), // we test the merge function elsewhere, don't bother here
			).Times(1).Return(nil),
		)

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
