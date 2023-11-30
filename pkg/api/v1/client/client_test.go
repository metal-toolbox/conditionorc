package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/fleetdb"
	"github.com/metal-toolbox/conditionorc/internal/model"
	"github.com/metal-toolbox/conditionorc/internal/server"
	"github.com/metal-toolbox/conditionorc/internal/store"
	storeTest "github.com/metal-toolbox/conditionorc/internal/store/test"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	rctypes "github.com/metal-toolbox/rivets/condition"

	events "go.hollow.sh/toolbox/events/mock"
)

type integrationTester struct {
	t               *testing.T
	assertAuthToken bool
	handler         http.Handler
	client          *Client
	repository      *storeTest.MockRepository
	fleetDB         *fleetdb.MockFleetDB
	stream          *events.MockStream
}

// Do implements the HTTPRequestDoer interface to swap the response writer
func (i *integrationTester) Do(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}

	w := httptest.NewRecorder()
	i.handler.ServeHTTP(w, req)

	if i.assertAuthToken {
		require.NotEmpty(i.t, req.Header.Get("Authorization"))
	} else {
		require.Empty(i.t, req.Header.Get("Authorization"))
	}

	return w.Result(), nil
}

type finalizer func()

func newTester(t *testing.T) (*integrationTester, finalizer) {
	t.Helper()

	ctrl := gomock.NewController(t)

	repository := storeTest.NewMockRepository(ctrl)
	fleetDBClient := fleetdb.NewMockFleetDB(ctrl)
	stream := events.NewMockStream(ctrl)

	l := logrus.New()
	l.Level = logrus.Level(logrus.ErrorLevel)
	serverOptions := []server.Option{
		server.WithLogger(l),
		server.WithListenAddress("localhost:9999"),
		server.WithStore(repository),
		server.WithFleetDBClient(fleetDBClient),
		server.WithStreamBroker(stream),
		server.WithConditionDefinitions(
			[]*rctypes.Definition{
				{Kind: rctypes.FirmwareInstall},
			},
		),
	}

	gin.SetMode(gin.ReleaseMode)

	srv := server.New(serverOptions...)

	// setup test server httptest recorder
	tester := &integrationTester{
		t:          t,
		handler:    srv.Handler,
		repository: repository,
		fleetDB:    fleetDBClient,
		stream:     stream,
	}

	// setup test client
	clientOptions := []Option{WithHTTPClient(tester)}

	client, err := NewClient("http://localhost:9999", clientOptions...)
	if err != nil {
		t.Error(err)
	}

	tester.client = client

	return tester, ctrl.Finish
}

func TestConditionStatus(t *testing.T) {
	serverID := uuid.New()

	testcases := []struct {
		name                string
		mockStore           func(r *storeTest.MockRepository)
		expectResponse      func() *v1types.ServerResponse
		expectErrorContains string
	}{
		{
			"valid response",
			// mock repository
			func(r *storeTest.MockRepository) {
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(
						&store.ConditionRecord{
							State: rctypes.Pending,
							Conditions: []*rctypes.Condition{
								&rctypes.Condition{
									Kind:   rctypes.FirmwareInstall,
									State:  rctypes.Pending,
									Status: []byte(`{"hello":"world"}`),
								},
							},
						},
						nil).
					Times(1)
			},
			func() *v1types.ServerResponse {

				return &v1types.ServerResponse{
					StatusCode: 200,
					Records: &v1types.ConditionsResponse{
						ServerID: serverID,
						State:    rctypes.Pending,
						Conditions: []*rctypes.Condition{
							{
								Kind:   rctypes.FirmwareInstall,
								State:  rctypes.Pending,
								Status: []byte(`{"hello":"world"}`),
							},
						},
					},
				}
			},
			"",
		},
		{
			"404 response",
			// mock repository
			func(r *storeTest.MockRepository) {
				// lookup existing condition
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(
						nil,
						store.ErrConditionNotFound).
					Times(1)
			},
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					Message:    "condition not found for server",
					StatusCode: 404,
				}
			},
			"",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			tester, finish := newTester(t)
			defer finish()

			if tc.mockStore != nil {
				tc.mockStore(tester.repository)
			}

			got, err := tester.client.ServerConditionStatus(context.TODO(), serverID)
			if err != nil {
				t.Error(err)
			}

			if err != nil {
				require.Contains(t, err.Error(), tc.expectErrorContains)
			}

			if tc.expectErrorContains != "" && err == nil {
				t.Error("expected error, got nil")
			}

			require.Equal(
				t,
				tc.expectResponse(),
				got,
			)
		})
	}
}

func TestConditionCreate(t *testing.T) {
	serverID := uuid.New()

	testcases := []struct {
		name                string
		payload             v1types.ConditionCreate
		mockStore           func(r *storeTest.MockRepository)
		expectResponse      func() *v1types.ServerResponse
		expectErrorContains string
	}{
		{
			"valid payload sent",
			v1types.ConditionCreate{Parameters: []byte(`{"hello":"world"}`)},
			// mock repository
			func(r *storeTest.MockRepository) {
				r.EXPECT().
					GetActiveCondition(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(
						nil,
						nil,
					).
					Times(1)

				// expect valid payload
				r.EXPECT().
					Create(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Any(),
					).
					DoAndReturn(func(_ context.Context, _ uuid.UUID, c *rctypes.Condition) error {
						require.Equal(t, rctypes.ConditionStructVersion, c.Version, "condition version mismatch")
						require.Equal(t, rctypes.FirmwareInstall, c.Kind, "condition kind mismatch")
						require.Equal(t, json.RawMessage(`{"hello":"world"}`), c.Parameters, "condition parameters mismatch")
						require.Equal(t, rctypes.Pending, c.State, "condition state mismatch")
						return nil
					}).
					Times(1)
			},
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					StatusCode: 200,
					Message:    "condition set",
				}
			},
			"",
		},
		{
			"400 response",
			v1types.ConditionCreate{Parameters: []byte(`{"hello":"world"}`)},
			// mock repository
			func(r *storeTest.MockRepository) {
				// condition exists
				r.EXPECT().
					GetActiveCondition(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(
						&rctypes.Condition{
							State: rctypes.Pending,
						},
						nil).
					Times(1)
			},
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					Message:    "server has an active condition",
					StatusCode: 400,
				}
			},
			"",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			tester, finish := newTester(t)
			defer finish()

			if tc.mockStore != nil {
				tc.mockStore(tester.repository)
			}

			tester.fleetDB.EXPECT().GetServer(gomock.Any(), gomock.Any()).AnyTimes().
				Return(&model.Server{FacilityCode: "facility"}, nil)

			tester.stream.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
				Return(nil)

			got, err := tester.client.ServerConditionCreate(context.TODO(), serverID, rctypes.FirmwareInstall, tc.payload)
			if err != nil {
				t.Error(err)
			}

			if err != nil {
				require.Contains(t, err.Error(), tc.expectErrorContains)
			}

			if tc.expectErrorContains != "" && err == nil {
				t.Error("expected error, got nil")
			}

			require.Equal(t, tc.expectResponse().StatusCode, got.StatusCode, "bad status code")
			require.Equal(t, tc.expectResponse().Message, got.Message, "bad message")
		})
	}
}

func TestFirmwareInstall(t *testing.T) {
	serverID := uuid.New()

	testcases := []struct {
		name                string
		payload             *rctypes.FirmwareInstallTaskParameters
		mockStore           func(r *storeTest.MockRepository)
		expectResponse      func() *v1types.ServerResponse
		expectErrorContains string
	}{
		{
			"success case",
			&rctypes.FirmwareInstallTaskParameters{
				AssetID: serverID,
			},
			// mock repository
			func(r *storeTest.MockRepository) {
				// CreateMultiple returns an error if there is an active condition
				r.EXPECT().
					CreateMultiple(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Any(),
						gomock.Any(),
					).Times(1).Return(nil)
			},
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					StatusCode: 200,
					Message:    "firmware install scheduled",
				}
			},
			"",
		},
		{
			"400 response",
			&rctypes.FirmwareInstallTaskParameters{
				AssetID: serverID,
			},
			// mock repository
			func(r *storeTest.MockRepository) {
				// condition exists
				r.EXPECT().
					CreateMultiple(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Any(),
						gomock.Any(),
					).
					Return(
						fmt.Errorf("%w:%s", store.ErrActiveCondition, "pound sand"),
					).
					Times(1)
			},
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					Message:    "server has an active condition",
					StatusCode: 400,
				}
			},
			"",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			tester, finish := newTester(t)
			defer finish()

			if tc.mockStore != nil {
				tc.mockStore(tester.repository)
			}

			tester.fleetDB.EXPECT().GetServer(gomock.Any(), gomock.Any()).AnyTimes().
				Return(&model.Server{FacilityCode: "facility"}, nil)

			tester.stream.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
				Return(nil)

			got, err := tester.client.ServerFirmwareInstall(context.TODO(), tc.payload)
			if err != nil {
				t.Error(err)
			}

			if err != nil {
				require.Contains(t, err.Error(), tc.expectErrorContains)
			}

			if tc.expectErrorContains != "" && err == nil {
				t.Error("expected error, got nil")
			}

			require.Equal(t, tc.expectResponse().StatusCode, got.StatusCode, "bad status code")
			require.Contains(t, got.Message, tc.expectResponse().Message, "bad message")
		})
	}
}
