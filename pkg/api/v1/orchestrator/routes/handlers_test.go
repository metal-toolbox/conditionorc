package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/fleetdb"
	"github.com/metal-toolbox/conditionorc/internal/store"
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/orchestrator/types"

	rctypes "github.com/metal-toolbox/rivets/condition"
	eventsm "github.com/metal-toolbox/rivets/events"
	"github.com/metal-toolbox/rivets/events/registry"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	mock "github.com/stretchr/testify/mock"
)

const (
	facility = "foo13"
)

// tester holds all the mocks for easier passing around
type tester struct {
	mockStore         *store.MockRepository
	mockFleetDB       *fleetdb.MockFleetDB
	mockStream        *eventsm.MockStream
	mockStatusValueKV *MockstatusValueKV
}

func mockserver(t *testing.T, mtester *tester) (*gin.Engine, error) {
	t.Helper()

	gin.SetMode(gin.ReleaseMode)
	g := gin.New()
	g.Use(gin.Recovery())

	options := []Option{
		WithLogger(logrus.New()),
		WithStore(mtester.mockStore),
		WithFleetDBClient(mtester.mockFleetDB),
		WithStatusKVPublisher(mtester.mockStatusValueKV),
		WithConditionDefinitions(
			[]*rctypes.Definition{
				{Kind: rctypes.FirmwareInstall},
			},
		),
		WithFacilityCode(facility),
	}

	if mtester.mockStream != nil {
		options = append(options, WithStreamBroker(mtester.mockStream, "foo"))
	}

	v1Router, err := NewRoutes(options...)
	if err != nil {
		return nil, err
	}

	v1Router.Routes(g.Group("/api/v1"))

	g.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"message": "invalid request - route not found"})
	})

	return g, nil
}

func asBytes(t *testing.T, b *bytes.Buffer) []byte {
	t.Helper()

	body, err := io.ReadAll(b)
	if err != nil {
		t.Error(err)
	}

	return body
}

func asJSONBytes(t *testing.T, s *v1types.ServerResponse) []byte {
	t.Helper()

	b, err := json.Marshal(s)
	if err != nil {
		t.Error(err)
	}

	return b
}

func setupTestServer(t *testing.T) (*tester, *gin.Engine, error) {
	mtester := &tester{
		mockStore:         store.NewMockRepository(t),
		mockFleetDB:       fleetdb.NewMockFleetDB(t),
		mockStream:        eventsm.NewMockStream(t),
		mockStatusValueKV: NewMockstatusValueKV(t),
	}

	server, err := mockserver(t, mtester)
	return mtester, server, err
}

func TestConditionStatusUpdate(t *testing.T) {
	serverID := uuid.New()
	conditionID := uuid.New()
	controllerID := registry.GetID("test-controller")
	cond := &rctypes.Condition{
		ID:   conditionID,
		Kind: rctypes.FirmwareInstall,
	}

	mtester, server, err := setupTestServer(t)
	if err != nil {
		t.Fatal(err)
	}

	surl := fmt.Sprintf("/api/v1/servers/%s/condition-status/%s/%s", serverID, rctypes.FirmwareInstall, conditionID)
	testcases := []struct {
		name            string
		mockStore       func(r *store.MockRepository)
		mockKVPublisher func(p *MockstatusValueKV)
		request         func(t *testing.T) *http.Request
		assertResponse  func(t *testing.T, r *httptest.ResponseRecorder)
	}{
		{
			name: "invalid server id",
			request: func(t *testing.T) *http.Request {
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPut, fmt.Sprintf("/api/v1/servers/%s/condition-status/%s/%s", "invalid_serverid", rctypes.FirmwareInstall, conditionID), http.NoBody)
				if err != nil {
					t.Fatal(err)
				}
				return request
			},
			assertResponse: func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "invalid server id")
			},
		},
		{
			name: "missing controller_id",
			request: func(t *testing.T) *http.Request {
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPut, surl, http.NoBody)
				if err != nil {
					t.Fatal(err)
				}
				return request
			},
			assertResponse: func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "expected controller_id param")
			},
		},
		{
			name: "invalid controller_id",
			request: func(t *testing.T) *http.Request {
				endpoint := surl + "?controller_id=invalid"
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPut, endpoint, http.NoBody)
				if err != nil {
					t.Fatal(err)
				}
				return request
			},
			assertResponse: func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "invalid controller_id")
			},
		},
		{
			name: "successful update",
			mockStore: func(r *store.MockRepository) {
				r.On("GetActiveCondition", mock.Anything, serverID).
					Return(cond, nil).
					Once()
			},
			mockKVPublisher: func(p *MockstatusValueKV) {
				p.On("publish", facility, cond.ID, controllerID, cond.Kind, mock.Anything, false, false).
					Return(nil).
					Once()
			},
			request: func(t *testing.T) *http.Request {
				endpoint := fmt.Sprintf("%s?controller_id=%s", surl, controllerID.String())
				payload := `{"status": "in_progress", "message": "Updating firmware"}`
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPut, endpoint, bytes.NewBufferString(payload))
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Content-Type", "application/json")
				return request
			},
			assertResponse: func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusOK, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "condition status update published")
			},
		},
		{
			name: "no active condition",
			mockStore: func(r *store.MockRepository) {
				r.On("GetActiveCondition", mock.Anything, serverID).
					Return(nil, nil).
					Once()
			},

			request: func(t *testing.T) *http.Request {
				endpoint := fmt.Sprintf("%s?controller_id=%s", surl, controllerID.String())
				payload := `{"status": "in_progress", "message": "Updating firmware"}`
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPut, endpoint, bytes.NewBufferString(payload))
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Content-Type", "application/json")
				return request
			},
			assertResponse: func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "no active condition found for server")
			},
		},
		{
			name: "timestamp update only",
			mockStore: func(r *store.MockRepository) {
				r.On("GetActiveCondition", mock.Anything, serverID).
					Return(cond, nil).
					Once()
			},
			mockKVPublisher: func(p *MockstatusValueKV) {
				p.On("publish", facility, cond.ID, controllerID, cond.Kind, mock.Anything, false, true).
					Return(nil).
					Once()
			},
			request: func(t *testing.T) *http.Request {
				endpoint := fmt.Sprintf("%s?controller_id=%s&ts_update=true", surl, controllerID.String())
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPut, endpoint, http.NoBody)
				if err != nil {
					t.Fatal(err)
				}
				return request
			},
			assertResponse: func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusOK, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "condition status update published")
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

			recorder := httptest.NewRecorder()

			server.ServeHTTP(recorder, tc.request(t))
			tc.assertResponse(t, recorder)
		})
	}
}
