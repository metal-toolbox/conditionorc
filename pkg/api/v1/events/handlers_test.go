package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/volatiletech/null/v8"

	"github.com/metal-toolbox/conditionorc/internal/store"
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"

	rctypes "github.com/metal-toolbox/rivets/condition"
	serversvc "go.hollow.sh/serverservice/pkg/api/v1"
	mock_events "go.hollow.sh/toolbox/events/mock"
)

func TestUpdateEvent(t *testing.T) {
	t.Parallel()
	t.Run("invalid update", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		repo := store.NewMockRepository(ctrl)

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
		repo := store.NewMockRepository(ctrl)

		handler := NewHandler(repo, stream, logrus.New())
		serverID := uuid.New()
		testKind := rctypes.Kind("test")
		conditionID := uuid.New()
		status := []byte(`{ "msg":"it's happening!" }`)
		gomock.InOrder(
			repo.EXPECT().Get(
				gomock.Any(),
				serverID,
				testKind,
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
		repo := store.NewMockRepository(ctrl)

		handler := NewHandler(repo, stream, logrus.New())
		serverID := uuid.New()
		testKind := rctypes.Kind("test")
		conditionID := uuid.New()
		status := []byte(`{ "msg":"it's happening!" }`)
		gomock.InOrder(
			repo.EXPECT().Get(
				gomock.Any(),
				serverID,
				testKind,
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
		repo := store.NewMockRepository(ctrl)

		handler := NewHandler(repo, stream, logrus.New())
		serverID := uuid.New()
		testKind := rctypes.Kind("test")
		conditionID := uuid.New()
		status := []byte(`{ "msg":"it's happening!" }`)
		gomock.InOrder(
			repo.EXPECT().Get(
				gomock.Any(),
				serverID,
				testKind,
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
		repo := store.NewMockRepository(ctrl)

		handler := NewHandler(repo, stream, logrus.New())
		serverID := uuid.New()
		testKind := rctypes.Kind("test")
		conditionID := uuid.New()

		gomock.InOrder(
			repo.EXPECT().Get(
				gomock.Any(),
				serverID,
				testKind,
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
		repo := store.NewMockRepository(ctrl)

		handler := NewHandler(repo, stream, logrus.New())
		serverID := uuid.New()
		testKind := rctypes.Kind("test")
		conditionID := uuid.New()

		gomock.InOrder(
			repo.EXPECT().Get(
				gomock.Any(),
				serverID,
				testKind,
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

func TestServerserviceEvent(t *testing.T) {
	t.Parallel()
	t.Run("bogus message subject", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		msg := mock_events.NewMockMessage(ctrl)
		repo := store.NewMockRepository(ctrl)

		handler := NewHandler(repo, stream, logrus.New())
		gomock.InOrder(
			msg.EXPECT().Subject().Times(1).Return("com.hollow.sh.serverservice.events.bogus"),
			msg.EXPECT().Ack().Times(1).Return(nil),
		)
		handler.ServerserviceEvent(context.TODO(), msg)
	})
	t.Run("bogus message content", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		msg := mock_events.NewMockMessage(ctrl)
		repo := store.NewMockRepository(ctrl)

		handler := NewHandler(repo, stream, logrus.New())
		gomock.InOrder(
			msg.EXPECT().Subject().Times(1).Return("com.hollow.sh.serverservice.events.server.create"),
			msg.EXPECT().Data().Times(1).Return([]byte("garbage")),
			msg.EXPECT().Ack().Times(1).Return(nil),
		)
		handler.ServerserviceEvent(context.TODO(), msg)
	})
	t.Run("null facility in message", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		msg := mock_events.NewMockMessage(ctrl)
		repo := store.NewMockRepository(ctrl)

		serverID := uuid.New()
		createServer := serversvc.CreateServer{
			Metadata: &serversvc.MsgMetadata{
				CreatedAt: time.Now(),
			},
			Name: null.StringFrom("server_name"),
			ID:   serverID.String(),
		}
		createServerBytes, err := json.Marshal(createServer)
		require.NoError(t, err)

		handler := NewHandler(repo, stream, logrus.New())
		gomock.InOrder(
			msg.EXPECT().Subject().Times(1).Return("com.hollow.sh.serverservice.events.server.create"),
			msg.EXPECT().Data().Times(1).Return(createServerBytes),
			msg.EXPECT().Ack().Times(1).Return(nil),
		)
		handler.ServerserviceEvent(context.TODO(), msg)
	})
	t.Run("condition create fails", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		msg := mock_events.NewMockMessage(ctrl)
		repo := store.NewMockRepository(ctrl)

		serverID := uuid.New()
		createServer := serversvc.CreateServer{
			Metadata: &serversvc.MsgMetadata{
				CreatedAt: time.Now(),
			},
			Name:         null.StringFrom("server_name"),
			FacilityCode: null.StringFrom("fc13"),
			ID:           serverID.String(),
		}
		createServerBytes, err := json.Marshal(createServer)
		require.NoError(t, err)

		handler := NewHandler(repo, stream, logrus.New())
		gomock.InOrder(
			msg.EXPECT().Subject().Times(1).Return("com.hollow.sh.serverservice.events.server.create"),
			msg.EXPECT().Data().Times(1).Return(createServerBytes),
			repo.EXPECT().Create(gomock.Any(), serverID, gomock.Any()).Times(1).Return(errors.New("pound sand")),
			msg.EXPECT().Nak().Times(1).Return(nil),
		)
		handler.ServerserviceEvent(context.TODO(), msg)
	})
	t.Run("inventory publish fails", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		msg := mock_events.NewMockMessage(ctrl)
		repo := store.NewMockRepository(ctrl)

		serverID := uuid.New()
		createServer := serversvc.CreateServer{
			Metadata: &serversvc.MsgMetadata{
				CreatedAt: time.Now(),
			},
			Name:         null.StringFrom("server_name"),
			FacilityCode: null.StringFrom("fc13"),
			ID:           serverID.String(),
		}
		createServerBytes, err := json.Marshal(createServer)
		require.NoError(t, err)

		handler := NewHandler(repo, stream, logrus.New())
		gomock.InOrder(
			msg.EXPECT().Subject().Times(1).Return("com.hollow.sh.serverservice.events.server.create"),
			msg.EXPECT().Data().Times(1).Return(createServerBytes),
			repo.EXPECT().Create(gomock.Any(), serverID, gomock.Any()).Times(1).Return(nil),
			stream.EXPECT().Publish(gomock.Any(), "servers.fc13.inventory.outofband", gomock.Any()).
				Return(errors.New("no publish for you")),
			msg.EXPECT().Nak().Times(1).Return(nil),
		)
		handler.ServerserviceEvent(context.TODO(), msg)
	})
	t.Run("inventory published", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		msg := mock_events.NewMockMessage(ctrl)
		repo := store.NewMockRepository(ctrl)

		serverID := uuid.New()
		createServer := serversvc.CreateServer{
			Metadata: &serversvc.MsgMetadata{
				CreatedAt: time.Now(),
			},
			Name:         null.StringFrom("server_name"),
			FacilityCode: null.StringFrom("fc13"),
			ID:           serverID.String(),
		}
		createServerBytes, err := json.Marshal(createServer)
		require.NoError(t, err)

		handler := NewHandler(repo, stream, logrus.New())
		gomock.InOrder(
			msg.EXPECT().Subject().Times(1).Return("com.hollow.sh.serverservice.events.server.create"),
			msg.EXPECT().Data().Times(1).Return(createServerBytes),
			repo.EXPECT().Create(gomock.Any(), serverID, gomock.Any()).Times(1).Return(nil),
			stream.EXPECT().Publish(gomock.Any(), "servers.fc13.inventory.outofband", gomock.Any()).
				Return(nil),
			msg.EXPECT().Ack().Times(1).Return(nil),
		)
		handler.ServerserviceEvent(context.TODO(), msg)
	})
}
