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

	serversvc "go.hollow.sh/serverservice/pkg/api/v1"
	mock_events "go.hollow.sh/toolbox/events/mock"
)

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
