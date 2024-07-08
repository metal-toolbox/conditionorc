package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/fleetdb"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/metal-toolbox/conditionorc/pkg/api/v1/orchestrator/routes"

	"github.com/metal-toolbox/rivets/events/registry"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/orchestrator/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
	eventsm "github.com/metal-toolbox/rivets/events"
)

const (
	facility = "foo13"
)

// tester holds all the mocks for easier passing around
type tester struct {
	mockStore              *store.MockRepository
	mockFleetDB            *fleetdb.MockFleetDB
	mockStream             *eventsm.MockStream
	mockStatusValueKV      *routes.MockstatusValueKV
	mockLivenessKV         *routes.MocklivenessKV
	mocktaskKV             *routes.MocktaskKV
	mockConditionJetstream *routes.MockconditionJetstream
}

func mockrouter(t *testing.T, mtester *tester) (*gin.Engine, error) {
	t.Helper()

	gin.SetMode(gin.ReleaseMode)
	g := gin.New()
	g.Use(gin.Recovery())

	options := []routes.Option{
		routes.WithLogger(logrus.New()),
		routes.WithStore(mtester.mockStore),
		routes.WithFleetDBClient(mtester.mockFleetDB),
		routes.WithStatusKVPublisher(mtester.mockStatusValueKV),
		routes.WithLivenessKV(mtester.mockLivenessKV),
		routes.WithTaskKV(mtester.mocktaskKV),
		routes.WithConditionJetstream(mtester.mockConditionJetstream),
		routes.WithConditionDefinitions(
			[]*rctypes.Definition{
				{Kind: rctypes.FirmwareInstall},
			},
		),
		routes.WithFacilityCode(facility),
	}

	if mtester.mockStream != nil {
		options = append(options, routes.WithStreamBroker(mtester.mockStream, "foo"))
	}

	v1Router, err := routes.NewRoutes(options...)
	if err != nil {
		return nil, err
	}

	v1Router.Routes(g.Group("/api/v1"))

	g.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"message": "invalid request - route not found"})
	})

	return g, nil
}

func setupMockRouter(t *testing.T) (*tester, *gin.Engine, error) {
	mtester := &tester{
		mockStore:              store.NewMockRepository(t),
		mockFleetDB:            fleetdb.NewMockFleetDB(t),
		mockStream:             eventsm.NewMockStream(t),
		mockStatusValueKV:      routes.NewMockstatusValueKV(t),
		mockLivenessKV:         routes.NewMocklivenessKV(t),
		mocktaskKV:             routes.NewMocktaskKV(t),
		mockConditionJetstream: routes.NewMockconditionJetstream(t),
	}

	r, err := mockrouter(t, mtester)
	return mtester, r, err
}

func TestConditionStatusUpdate(t *testing.T) {
	serverID := uuid.New()
	conditionID := uuid.New()
	controllerID := registry.GetID("test-controller")
	cond := &rctypes.Condition{
		ID:   conditionID,
		Kind: rctypes.FirmwareInstall,
	}

	mtester, server, err := setupMockRouter(t)
	if err != nil {
		t.Fatal(err)
	}

	testcases := []struct {
		name                string
		statusValue         *rctypes.StatusValue
		onlyUpdateTimestamp bool
		mockStore           func(r *store.MockRepository)
		mockKVPublisher     func(p *routes.MockstatusValueKV)
		expectResponse      func() *v1types.ServerResponse
	}{
		{
			name: "successful update",
			statusValue: &rctypes.StatusValue{
				State:  string(rctypes.Active),
				Status: json.RawMessage(`{"message":"Updating firmware"}`),
			},
			onlyUpdateTimestamp: false,
			mockStore: func(r *store.MockRepository) {
				r.On("GetActiveCondition", mock.Anything, serverID).
					Return(cond, nil).
					Once()
			},
			mockKVPublisher: func(p *routes.MockstatusValueKV) {
				p.On("publish", facility, cond.ID, controllerID, cond.Kind, mock.Anything, false, false).
					Return(nil).
					Once()
			},
			expectResponse: func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					Message:    "condition status update published",
					StatusCode: http.StatusOK,
				}
			},
		},
		{
			name:                "timestamp update only",
			statusValue:         nil,
			onlyUpdateTimestamp: true,
			mockStore: func(r *store.MockRepository) {
				r.On("GetActiveCondition", mock.Anything, serverID).
					Return(cond, nil).
					Once()
			},
			mockKVPublisher: func(p *routes.MockstatusValueKV) {
				p.On("publish", facility, cond.ID, controllerID, cond.Kind, mock.Anything, false, true).
					Return(nil).
					Once()
			},
			expectResponse: func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					Message:    "condition status update published",
					StatusCode: http.StatusOK,
				}
			},
		},
		{
			name:                "no active condition",
			statusValue:         nil,
			onlyUpdateTimestamp: false,
			mockStore: func(r *store.MockRepository) {
				r.On("GetActiveCondition", mock.Anything, serverID).
					Return(nil, nil).
					Once()
			},
			mockKVPublisher: nil,
			expectResponse: func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					Message:    "no active condition found for server",
					StatusCode: http.StatusBadRequest,
				}
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mockStore != nil {
				tc.mockStore(mtester.mockStore)
			}
			if tc.mockKVPublisher != nil {
				tc.mockKVPublisher(mtester.mockStatusValueKV)
			}

			testServer := httptest.NewServer(server)
			defer testServer.Close()

			client := &Client{
				serverAddress: testServer.URL,
				client:        http.DefaultClient,
			}

			got, err := client.ConditionStatusUpdate(
				context.TODO(),
				cond.Kind,
				serverID,
				conditionID,
				controllerID,
				tc.statusValue,
				tc.onlyUpdateTimestamp,
			)
			require.NoError(t, err)

			if tc.expectResponse != nil {
				assert.Equal(t, tc.expectResponse(), got)
			}
		})
	}
}

func TestConditionQueuePop(t *testing.T) {
	serverID := uuid.New()
	conditionID := uuid.New()
	conditionKind := rctypes.FirmwareInstall
	cond := &rctypes.Condition{
		ID:   conditionID,
		Kind: conditionKind,
	}

	mtester, server, err := setupMockRouter(t)
	if err != nil {
		t.Fatal(err)
	}

	testcases := []struct {
		name                   string
		mockStore              func(r *store.MockRepository)
		mockConditionJetstream func(cj *routes.MockconditionJetstream)
		mockLivenessKV         func(l *routes.MocklivenessKV)
		mockStatusKVPublisher  func(p *routes.MockstatusValueKV)
		mockTaskKV             func(tk *routes.MocktaskKV)
		expectResponse         func() *v1types.ServerResponse
	}{
		{
			name: "successful pop",
			mockStore: func(r *store.MockRepository) {
				r.On("Get", mock.Anything, serverID).
					Return(&store.ConditionRecord{
						Conditions: []*rctypes.Condition{cond},
					}, nil).
					Once()
			},
			mockConditionJetstream: func(cj *routes.MockconditionJetstream) {
				cj.On("pop", mock.Anything, conditionKind, serverID).
					Return(cond, nil).
					Once()
			},
			mockLivenessKV: func(l *routes.MocklivenessKV) {
				l.On("register", serverID.String()).
					Return(registry.GetID("test-controller"), nil).
					Once()
			},
			mockStatusKVPublisher: func(p *routes.MockstatusValueKV) {
				p.On("publish", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, true, false).
					Return(nil).
					Once()
			},
			mockTaskKV: func(tk *routes.MocktaskKV) {
				tk.On("publish", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, true, false).
					Return(nil).
					Once()
			},
			expectResponse: func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					Condition:  cond,
					StatusCode: http.StatusOK,
				}
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			tc.mockStore(mtester.mockStore)
			tc.mockConditionJetstream(mtester.mockConditionJetstream)
			tc.mockLivenessKV(mtester.mockLivenessKV)
			tc.mockStatusKVPublisher(mtester.mockStatusValueKV)
			tc.mockTaskKV(mtester.mocktaskKV)

			testServer := httptest.NewServer(server)
			defer testServer.Close()

			client := &Client{
				serverAddress: testServer.URL,
				client:        http.DefaultClient,
			}

			got, err := client.ConditionQueuePop(context.TODO(), conditionKind, serverID)

			require.NoError(t, err)
			assert.Equal(t, tc.expectResponse(), got)
		})
	}
}

func TestControllerCheckin(t *testing.T) {
	serverID := uuid.New()
	conditionID := uuid.New()
	controllerID := registry.GetID("test-controller")

	mtester, server, err := setupMockRouter(t)
	if err != nil {
		t.Fatal(err)
	}

	testcases := []struct {
		name           string
		mockStore      func(r *store.MockRepository)
		mockLivenessKV func(l *routes.MocklivenessKV)
		expectResponse func() *v1types.ServerResponse
	}{
		{
			name: "successful checkin",
			mockStore: func(r *store.MockRepository) {
				r.On("GetActiveCondition", mock.Anything, serverID).
					Return(&rctypes.Condition{ID: conditionID}, nil).
					Once()
			},
			mockLivenessKV: func(l *routes.MocklivenessKV) {
				l.On("checkin", controllerID).
					Return(nil).
					Once()
			},
			expectResponse: func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					Message:    "check-in successful",
					StatusCode: http.StatusOK,
				}
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			tc.mockStore(mtester.mockStore)
			tc.mockLivenessKV(mtester.mockLivenessKV)

			testServer := httptest.NewServer(server)
			defer testServer.Close()

			client := &Client{
				serverAddress: testServer.URL,
				client:        http.DefaultClient,
			}

			got, err := client.ControllerCheckin(context.TODO(), serverID, conditionID, controllerID)

			require.NoError(t, err)
			assert.Equal(t, tc.expectResponse(), got)
		})
	}
}

func TestConditionTaskPublish(t *testing.T) {
	serverID := uuid.New()
	conditionID := uuid.New()
	conditionKind := rctypes.FirmwareInstall
	task := &rctypes.Task[any, any]{
		ID:   conditionID,
		Kind: rctypes.FirmwareInstall,
	}

	mtester, server, err := setupMockRouter(t)
	if err != nil {
		t.Fatal(err)
	}

	testcases := []struct {
		name                string
		task                *rctypes.Task[any, any]
		onlyUpdateTimestamp bool
		mockStore           func(r *store.MockRepository)
		mockTaskKV          func(tk *routes.MocktaskKV)
		expectResponse      func() *v1types.ServerResponse
	}{
		{
			name:                "successful task publish",
			task:                task,
			onlyUpdateTimestamp: false,
			mockStore: func(r *store.MockRepository) {
				r.On("GetActiveCondition", mock.Anything, serverID).
					Return(&rctypes.Condition{ID: conditionID, Kind: conditionKind}, nil).
					Once()
			},
			mockTaskKV: func(tk *routes.MocktaskKV) {
				tk.On("publish", mock.Anything, serverID.String(), conditionID.String(), conditionKind, mock.IsType(&rctypes.Task[any, any]{}), false, false).
					Return(nil).
					Once()
			},
			expectResponse: func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					Message:    "condition Task published",
					StatusCode: http.StatusOK,
				}
			},
		},
		{
			name:                "timestamp update only",
			task:                nil,
			onlyUpdateTimestamp: true,
			mockStore: func(r *store.MockRepository) {
				r.On("GetActiveCondition", mock.Anything, serverID).
					Return(&rctypes.Condition{ID: conditionID, Kind: conditionKind}, nil).
					Once()
			},
			mockTaskKV: func(tk *routes.MocktaskKV) {
				tk.On("publish", mock.Anything, serverID.String(), conditionID.String(), conditionKind, mock.IsType(&rctypes.Task[any, any]{}), false, true).
					Return(nil).
					Once()
			},
			expectResponse: func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					Message:    "condition Task published",
					StatusCode: http.StatusOK,
				}
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			tc.mockStore(mtester.mockStore)
			tc.mockTaskKV(mtester.mocktaskKV)

			testServer := httptest.NewServer(server)
			defer testServer.Close()

			client := &Client{
				serverAddress: testServer.URL,
				client:        http.DefaultClient,
			}

			got, err := client.ConditionTaskPublish(context.TODO(), conditionKind, serverID, conditionID, tc.task, tc.onlyUpdateTimestamp)

			require.NoError(t, err)
			assert.Equal(t, tc.expectResponse(), got)
		})
	}
}

func TestConditionTaskQuery(t *testing.T) {
	serverID := uuid.New()
	conditionID := uuid.New()
	conditionKind := rctypes.FirmwareInstall
	task := &rctypes.Task[any, any]{
		ID:   conditionID,
		Kind: conditionKind,
	}

	mtester, server, err := setupMockRouter(t)
	if err != nil {
		t.Fatal(err)
	}

	testcases := []struct {
		name           string
		mockStore      func(r *store.MockRepository)
		mockTaskKV     func(tk *routes.MocktaskKV)
		expectResponse func() *v1types.ServerResponse
	}{
		{
			name: "successful task query",
			mockStore: func(r *store.MockRepository) {
				r.On("GetActiveCondition", mock.Anything, serverID).
					Return(&rctypes.Condition{ID: conditionID, Kind: conditionKind}, nil).
					Once()
			},
			mockTaskKV: func(tk *routes.MocktaskKV) {
				tk.On("get", mock.Anything, conditionKind, conditionID, serverID).
					Return(task, nil).
					Once()
			},
			expectResponse: func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					Message:    "Task identified",
					Task:       task,
					StatusCode: http.StatusOK,
				}
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			tc.mockStore(mtester.mockStore)
			tc.mockTaskKV(mtester.mocktaskKV)

			testServer := httptest.NewServer(server)
			defer testServer.Close()

			client := &Client{
				serverAddress: testServer.URL,
				client:        http.DefaultClient,
			}

			got, err := client.ConditionTaskQuery(context.TODO(), conditionKind, serverID)

			require.NoError(t, err)
			assert.Equal(t, tc.expectResponse(), got)
		})
	}
}
