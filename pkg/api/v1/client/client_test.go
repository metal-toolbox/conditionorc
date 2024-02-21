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

	"github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
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
					StatusCode: 409,
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

func TestServerEnroll(t *testing.T) {
	serverID := uuid.New()
	validParams := types.AddServerParams{
		Facility: "mock-facility-code",
		IP:       "mock-ip",
		Username: "mock-user",
		Password: "mock-pwd",
	}
	invalidParamsNoBMCUser := types.AddServerParams{
		Facility: "mock-facility-code",
		IP:       "mock-ip",
		Password: "mock-pwd",
	}

	expectedInventoryParams := func(id string) string {
		return fmt.Sprintf(`{"collect_bios_cfg":true,"collect_firmware_status":true,"inventory_method":"outofband","asset_id":"%v"}`, id)
	}

	rollbackCounter := 0
	rollback := func() error {
		rollbackCounter += 1
		return nil
	}

	testcases := []struct {
		name                  string
		payload               v1types.ConditionCreate
		mockFleetDBClient     func(f *fleetdb.MockFleetDB)
		mockStore             func(r *storeTest.MockRepository)
		expectedRollbackCount int
		expectResponse        func() *v1types.ServerResponse
		expectError           string
	}{
		{
			"valid payload sent",
			v1types.ConditionCreate{Parameters: validParams.MustJSON()},
			func(r *fleetdb.MockFleetDB) {
				// lookup for an existing condition
				r.EXPECT().
					AddServer(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq("mock-facility-code"),
						gomock.Eq("mock-ip"), gomock.Eq("mock-user"),
						gomock.Eq("mock-pwd"),
					).
					Return(nil, nil).
					Times(1)
			},
			func(r *storeTest.MockRepository) {
				// expect valid payload
				r.EXPECT().
					Create(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Any(),
					).
					DoAndReturn(func(_ context.Context, _ uuid.UUID, c *rctypes.Condition) error {
						require.Equal(t, rctypes.ConditionStructVersion, c.Version, "condition version mismatch")
						require.Equal(t, rctypes.Inventory, c.Kind, "condition kind mismatch")
						require.Equal(t, json.RawMessage(expectedInventoryParams(serverID.String())), c.Parameters, "condition parameters mismatch")
						require.Equal(t, rctypes.Pending, c.State, "condition state mismatch")
						return nil
					}).
					Times(1)
			},
			0,
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					StatusCode: 200,
					Message:    "condition set",
				}
			},
			"",
		},
		{
			"invalid payload - failed to send out",
			v1types.ConditionCreate{Parameters: []byte("not json")},
			func(r *fleetdb.MockFleetDB) {
				// lookup for an existing condition
				r.EXPECT().
					AddServer(
						gomock.Any(),
						gomock.Any(),
						gomock.Any(),
						gomock.Any(),
						gomock.Any(),
						gomock.Any(),
					).
					Times(0)
			},
			func(r *storeTest.MockRepository) {
				// expect valid payload
				r.EXPECT().
					Create(
						gomock.Any(),
						gomock.Any(),
						gomock.Any(),
					).
					Times(0)
			},
			0,
			func() *v1types.ServerResponse {
				return nil
			},
			"conditionorc client error - error in POST JSON payload",
		},
		{
			"no bmc user",
			v1types.ConditionCreate{Parameters: invalidParamsNoBMCUser.MustJSON()},
			func(r *fleetdb.MockFleetDB) {
				// lookup for an existing condition
				r.EXPECT().
					AddServer(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq("mock-facility-code"),
						gomock.Eq("mock-ip"), gomock.Eq(""),
						gomock.Eq("mock-pwd"),
					).
					Return(rollback, fleetdb.ErrBMCCredentials).
					Times(1)
			},
			func(r *storeTest.MockRepository) {
				// expect valid payload
				r.EXPECT().
					Create(
						gomock.Any(),
						gomock.Any(),
						gomock.Any(),
					).Times(0)
			},
			1,
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					StatusCode: 500,
					Message:    "add server: invalid bmc credentials. missing user or passwordserver rollback err: <nil>",
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

			if tc.mockFleetDBClient != nil {
				tc.mockFleetDBClient(tester.fleetDB)
			}

			tester.stream.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
				Return(nil)
			defer func() {
				require.Equal(t, tc.expectedRollbackCount, rollbackCounter, "rollback called incorrectly")
				rollbackCounter = 0
			}()

			got, err := tester.client.ServerEnroll(context.TODO(), serverID.String(), tc.payload)
			if tc.expectError != "" {
				require.Contains(t, err.Error(), tc.expectError, "request should not send out")
				if got != nil {
					t.Errorf("expect nil response, got %v", got)
				}
				return
			}

			if err != nil {
				t.Error(err)
			}

			if tc.expectResponse() != nil {
				require.Equal(t, tc.expectResponse().StatusCode, got.StatusCode, "bad status code")
				require.Equal(t, tc.expectResponse().Message, got.Message, "bad message")
			}
		})
	}
}

func TestServerEnrollEmptyUUID(t *testing.T) {
	tester, finish := newTester(t)
	defer finish()

	var generatedServerID uuid.UUID
	validParams := types.AddServerParams{
		Facility: "mock-facility-code",
		IP:       "mock-ip",
		Username: "mock-user",
		Password: "mock-pwd",
	}
	expectedInventoryParams := func(id string) string {
		return fmt.Sprintf(`{"collect_bios_cfg":true,"collect_firmware_status":true,"inventory_method":"outofband","asset_id":"%v"}`, id)
	}

	rollbackCounter := 0
	rollback := func() error {
		rollbackCounter += 1
		return nil
	}

	tester.fleetDB.EXPECT().
		AddServer(gomock.Any(),
			gomock.Any(),
			gomock.Eq("mock-facility-code"),
			gomock.Eq("mock-ip"),
			gomock.Eq("mock-user"),
			gomock.Eq("mock-pwd"),
		).
		DoAndReturn(func(ctx context.Context, serverID uuid.UUID, _, _, _, _ string) (func() error, error) {
			generatedServerID = serverID
			return rollback, nil
		}).
		Times(1)

	tester.repository.EXPECT().
		Create(
			gomock.Any(),
			gomock.Any(),
			gomock.Any(),
		).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, c *rctypes.Condition) error {
			require.Equal(t, rctypes.ConditionStructVersion, c.Version, "condition version mismatch")
			require.Equal(t, rctypes.Inventory, c.Kind, "condition kind mismatch")
			require.Equal(t, json.RawMessage(expectedInventoryParams(generatedServerID.String())), c.Parameters, "condition parameters mismatch")
			require.Equal(t, rctypes.Pending, c.State, "condition state mismatch")
			return nil
		}).
		Times(1)

	tester.stream.EXPECT().Publish(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
		Return(nil)

	got, err := tester.client.ServerEnroll(context.TODO(), "", v1types.ConditionCreate{Parameters: validParams.MustJSON()})
	if err != nil {
		t.Error(err)
	}
	require.Equal(t, http.StatusOK, got.StatusCode, "bad status code")
	require.Equal(t, 0, rollbackCounter, "rollback called incorrectly")
}

func TestServerDelete(t *testing.T) {
	serverID := uuid.New()
	testcases := []struct {
		name              string
		mockFleetDBClient func(f *fleetdb.MockFleetDB)
		mockStore         func(r *storeTest.MockRepository)
		expectResponse    func() *v1types.ServerResponse
	}{
		{
			"delete server success",
			func(r *fleetdb.MockFleetDB) {
				r.EXPECT().
					DeleteServer(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(nil).
					Times(1)
			},
			func(r *storeTest.MockRepository) {
				// expect valid payload
				r.EXPECT().
					GetActiveCondition(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(nil, nil).
					Times(1)
			},
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					StatusCode: 200,
					Message:    "server deleted",
				}
			},
		},
		{
			"active condition",
			func(r *fleetdb.MockFleetDB) {
				r.EXPECT().
					DeleteServer(
						gomock.Any(),
						gomock.Any(),
					).
					Times(0)
			},
			func(r *storeTest.MockRepository) {
				// expect valid payload
				r.EXPECT().
					GetActiveCondition(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(&rctypes.Condition{}, nil).
					Times(1)
			},
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					StatusCode: 400,
					Message:    "failed to delete server because it has an active condition",
				}
			},
		},
		{
			"check active condition error",
			func(r *fleetdb.MockFleetDB) {
				r.EXPECT().
					DeleteServer(
						gomock.Any(),
						gomock.Any(),
					).
					Times(0)
			},
			func(r *storeTest.MockRepository) {
				// expect valid payload
				r.EXPECT().
					GetActiveCondition(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(nil, fmt.Errorf("fake check condition error")).
					Times(1)
			},
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					StatusCode: 503,
					Message:    "error checking server state: fake check condition error",
				}
			},
		},
		{
			"fleetdb delete error",
			func(r *fleetdb.MockFleetDB) {
				r.EXPECT().
					DeleteServer(
						gomock.Any(),
						gomock.Any(),
					).
					Return(fmt.Errorf("fake delete error")).
					Times(1)
			},
			func(r *storeTest.MockRepository) {
				// expect valid payload
				r.EXPECT().
					GetActiveCondition(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(nil, nil).
					Times(1)
			},
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					StatusCode: 500,
					Message:    "fake delete error",
				}
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			tester, finish := newTester(t)
			defer finish()

			if tc.mockStore != nil {
				tc.mockStore(tester.repository)
			}

			if tc.mockFleetDBClient != nil {
				tc.mockFleetDBClient(tester.fleetDB)
			}

			got, err := tester.client.ServerDelete(context.TODO(), serverID.String())
			if err != nil {
				t.Error(err)
			}
			require.Equal(t, tc.expectResponse().StatusCode, got.StatusCode, "bad status code")
			require.Equal(t, tc.expectResponse().Message, got.Message, "bad message")
		})
	}
}

func TestServerDeleteInvalidUUID(t *testing.T) {
	tester, finish := newTester(t)
	defer finish()
	fakeInvalidServerID := "fakeInvalidID"

	testcases := []struct {
		name           string
		serverID       string
		expectResponse func() *v1types.ServerResponse
	}{
		{
			"empty ID",
			"",
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					StatusCode: 404,
					Message:    "invalid request - route not found",
				}
			},
		},
		{
			"invalid ID",
			fakeInvalidServerID,
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					StatusCode: 400,
					Message:    fmt.Sprintf("invalid UUID length: %d", len(fakeInvalidServerID)),
				}
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			tester.fleetDB.EXPECT().
				DeleteServer(
					gomock.Any(),
					gomock.Any(),
				).
				Return(nil).
				Times(0)

			tester.repository.EXPECT().
				GetActiveCondition(
					gomock.Any(),
					gomock.Any(),
				).
				Return(nil, nil).
				Times(0)

			got, err := tester.client.ServerDelete(context.TODO(), tc.serverID)
			if err != nil {
				t.Error(err)
			}
			require.Equal(t, tc.expectResponse().StatusCode, got.StatusCode, "bad status code")
			require.Equal(t, tc.expectResponse().Message, got.Message, "bad message")
		})
	}
}
