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
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"

	serversvc "go.hollow.sh/serverservice/pkg/api/v1"
	mock_events "go.hollow.sh/toolbox/events/mock"
)

func TestControllerEvent(t *testing.T) {
	t.Parallel()
	t.Run("bogus controller message", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		msg := mock_events.NewMockMessage(ctrl)
		repo := store.NewMockRepository(ctrl)

		handler := NewHandler(repo, stream, logrus.New())
		gomock.InOrder(
			msg.EXPECT().Subject().Times(1).Return("com.hollow.sh.controllers.responses.fc13.bogus"),
			msg.EXPECT().Ack().Times(1).Return(nil),
		)
		handler.ControllerEvent(context.TODO(), msg)
	})
	t.Run("bogus message contents", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		msg := mock_events.NewMockMessage(ctrl)
		repo := store.NewMockRepository(ctrl)

		handler := NewHandler(repo, stream, logrus.New())
		gomock.InOrder(
			msg.EXPECT().Subject().Times(1).Return("com.hollow.sh.controllers.responses.fc13.update"),
			msg.EXPECT().Data().Times(1).Return([]byte("garbage")),
			msg.EXPECT().Ack().Times(1).Return(nil),
		)
		handler.ControllerEvent(context.TODO(), msg)
	})
	t.Run("update message doesn't validate", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		msg := mock_events.NewMockMessage(ctrl)
		repo := store.NewMockRepository(ctrl)

		handler := NewHandler(repo, stream, logrus.New())
		gomock.InOrder(
			msg.EXPECT().Subject().Times(1).Return("com.hollow.sh.controllers.responses.fc13.update"),
			// This is a valid JSON message that results in a zero-value (and thus invalid) update message
			msg.EXPECT().Data().Times(1).Return([]byte(`{"garbage": "true"}`)),
			msg.EXPECT().Ack().Times(1).Return(nil),
		)
		handler.ControllerEvent(context.TODO(), msg)
	})
	t.Run("store is unavailable", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		msg := mock_events.NewMockMessage(ctrl)
		repo := store.NewMockRepository(ctrl)

		serverID := uuid.New()
		contents := &v1types.ConditionUpdateEvent{
			Kind: ptypes.InventoryOutofband,
			ConditionUpdate: v1types.ConditionUpdate{
				ConditionID:     uuid.New(),
				TargetID:        serverID,
				ResourceVersion: int64(1),
				State:           ptypes.Active,
				Status:          []byte(`{"some":"update"}`),
			},
		}
		contentsBytes, err := json.Marshal(contents)
		require.NoError(t, err)

		handler := NewHandler(repo, stream, logrus.New())
		gomock.InOrder(
			msg.EXPECT().Subject().Times(1).Return("com.hollow.sh.controllers.responses.fc13.update"),
			msg.EXPECT().Data().Times(1).Return(contentsBytes),
			repo.EXPECT().Get(gomock.Any(), serverID, ptypes.InventoryOutofband).
				Return(nil, errors.New("your princess is in another castle")),
			msg.EXPECT().Nak().Times(1).Return(nil),
		)
		handler.ControllerEvent(context.TODO(), msg)
	})
	t.Run("no condition with given id", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		msg := mock_events.NewMockMessage(ctrl)
		repo := store.NewMockRepository(ctrl)

		serverID := uuid.New()
		contents := &v1types.ConditionUpdateEvent{
			Kind: ptypes.InventoryOutofband,
			ConditionUpdate: v1types.ConditionUpdate{
				ConditionID:     uuid.New(),
				TargetID:        serverID,
				ResourceVersion: int64(1),
				State:           ptypes.Active,
				Status:          []byte(`{"some":"update"}`),
			},
		}
		contentsBytes, err := json.Marshal(contents)
		require.NoError(t, err)

		handler := NewHandler(repo, stream, logrus.New())
		gomock.InOrder(
			msg.EXPECT().Subject().Times(1).Return("com.hollow.sh.controllers.responses.fc13.update"),
			msg.EXPECT().Data().Times(1).Return(contentsBytes),
			repo.EXPECT().Get(gomock.Any(), serverID, ptypes.InventoryOutofband).
				Return(nil, store.ErrConditionNotFound),
			msg.EXPECT().Ack().Times(1).Return(nil),
		)
		handler.ControllerEvent(context.TODO(), msg)
	})
	t.Run("nil condition with given id", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		msg := mock_events.NewMockMessage(ctrl)
		repo := store.NewMockRepository(ctrl)

		serverID := uuid.New()
		contents := &v1types.ConditionUpdateEvent{
			Kind: ptypes.InventoryOutofband,
			ConditionUpdate: v1types.ConditionUpdate{
				ConditionID:     uuid.New(),
				TargetID:        serverID,
				ResourceVersion: int64(1),
				State:           ptypes.Active,
				Status:          []byte(`{"some":"update"}`),
			},
		}
		contentsBytes, err := json.Marshal(contents)
		require.NoError(t, err)

		handler := NewHandler(repo, stream, logrus.New())
		gomock.InOrder(
			msg.EXPECT().Subject().Times(1).Return("com.hollow.sh.controllers.responses.fc13.update"),
			msg.EXPECT().Data().Times(1).Return(contentsBytes),
			repo.EXPECT().Get(gomock.Any(), serverID, ptypes.InventoryOutofband).
				Return(nil, nil),
			msg.EXPECT().Ack().Times(1).Return(nil),
		)
		handler.ControllerEvent(context.TODO(), msg)
	})
	t.Run("no-op update", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		msg := mock_events.NewMockMessage(ctrl)
		repo := store.NewMockRepository(ctrl)

		serverID := uuid.New()
		contents := &v1types.ConditionUpdateEvent{
			Kind: ptypes.InventoryOutofband,
			ConditionUpdate: v1types.ConditionUpdate{
				ConditionID:     uuid.New(),
				TargetID:        serverID,
				ResourceVersion: int64(1),
				State:           ptypes.Active,
				Status:          []byte(`{"some":"update"}`),
			},
		}
		contentsBytes, err := json.Marshal(contents)
		require.NoError(t, err)

		existingCondition := &ptypes.Condition{
			State:  ptypes.Active,
			Status: []byte(`{"some":"update"}`),
		}

		handler := NewHandler(repo, stream, logrus.New())
		gomock.InOrder(
			msg.EXPECT().Subject().Times(1).Return("com.hollow.sh.controllers.responses.fc13.update"),
			msg.EXPECT().Data().Times(1).Return(contentsBytes),
			repo.EXPECT().Get(gomock.Any(), serverID, ptypes.InventoryOutofband).
				Return(existingCondition, nil),
			msg.EXPECT().Ack().Times(1).Return(nil),
		)
		handler.ControllerEvent(context.TODO(), msg)
	})
	t.Run("merge fails", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		msg := mock_events.NewMockMessage(ctrl)
		repo := store.NewMockRepository(ctrl)

		serverID := uuid.New()
		contents := &v1types.ConditionUpdateEvent{
			Kind: ptypes.InventoryOutofband,
			ConditionUpdate: v1types.ConditionUpdate{
				ConditionID:     uuid.New(),
				TargetID:        serverID,
				ResourceVersion: int64(1),
				State:           ptypes.Active,
				Status:          []byte(`{"some":"update"}`),
			},
		}
		contentsBytes, err := json.Marshal(contents)
		require.NoError(t, err)

		existingCondition := &ptypes.Condition{
			ID:    uuid.New(), // ids don't match
			State: ptypes.Pending,
		}

		handler := NewHandler(repo, stream, logrus.New())
		gomock.InOrder(
			msg.EXPECT().Subject().Times(1).Return("com.hollow.sh.controllers.responses.fc13.update"),
			msg.EXPECT().Data().Times(1).Return(contentsBytes),
			repo.EXPECT().Get(gomock.Any(), serverID, ptypes.InventoryOutofband).
				Return(existingCondition, nil),
			msg.EXPECT().Ack().Times(1).Return(nil),
		)
		handler.ControllerEvent(context.TODO(), msg)
	})
	t.Run("update fails", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		msg := mock_events.NewMockMessage(ctrl)
		repo := store.NewMockRepository(ctrl)

		serverID := uuid.New()
		conditionID := uuid.New()
		contents := &v1types.ConditionUpdateEvent{
			Kind: ptypes.InventoryOutofband,
			ConditionUpdate: v1types.ConditionUpdate{
				ConditionID:     conditionID,
				TargetID:        serverID,
				ResourceVersion: int64(1),
				State:           ptypes.Active,
				Status:          []byte(`{"some":"update"}`),
			},
		}
		contentsBytes, err := json.Marshal(contents)
		require.NoError(t, err)

		existingCondition := &ptypes.Condition{
			ID:              conditionID,
			State:           ptypes.Pending,
			ResourceVersion: int64(1),
		}

		handler := NewHandler(repo, stream, logrus.New())
		gomock.InOrder(
			msg.EXPECT().Subject().Times(1).Return("com.hollow.sh.controllers.responses.fc13.update"),
			msg.EXPECT().Data().Times(1).Return(contentsBytes),
			repo.EXPECT().Get(gomock.Any(), serverID, ptypes.InventoryOutofband).
				Times(1).Return(existingCondition, nil),
			repo.EXPECT().Update(gomock.Any(), conditionID, gomock.Any()).Times(1).Return(errors.New("pound sand")),
			msg.EXPECT().Nak().Times(1).Return(nil),
		)
		handler.ControllerEvent(context.TODO(), msg)
	})
	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		stream := mock_events.NewMockStream(ctrl)
		msg := mock_events.NewMockMessage(ctrl)
		repo := store.NewMockRepository(ctrl)

		serverID := uuid.New()
		conditionID := uuid.New()
		contents := &v1types.ConditionUpdateEvent{
			Kind: ptypes.InventoryOutofband,
			ConditionUpdate: v1types.ConditionUpdate{
				ConditionID:     conditionID,
				TargetID:        serverID,
				ResourceVersion: int64(1),
				State:           ptypes.Active,
				Status:          []byte(`{"some":"update"}`),
			},
		}
		contentsBytes, err := json.Marshal(contents)
		require.NoError(t, err)

		existingCondition := &ptypes.Condition{
			ID:              conditionID,
			State:           ptypes.Pending,
			ResourceVersion: int64(1),
		}

		handler := NewHandler(repo, stream, logrus.New())
		gomock.InOrder(
			msg.EXPECT().Subject().Times(1).Return("com.hollow.sh.controllers.responses.fc13.update"),
			msg.EXPECT().Data().Times(1).Return(contentsBytes),
			repo.EXPECT().Get(gomock.Any(), serverID, ptypes.InventoryOutofband).
				Times(1).Return(existingCondition, nil),
			repo.EXPECT().Update(gomock.Any(), conditionID, gomock.Any()).Times(1).Return(nil),
			msg.EXPECT().Ack().Times(1).Return(nil),
		)
		handler.ControllerEvent(context.TODO(), msg)
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
