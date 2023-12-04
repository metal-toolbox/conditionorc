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
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/fleetdb"
	"github.com/metal-toolbox/conditionorc/internal/model"
	"github.com/metal-toolbox/conditionorc/internal/store"
	storeTest "github.com/metal-toolbox/conditionorc/internal/store/test"
	"github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.hollow.sh/toolbox/events"
	mockevents "go.hollow.sh/toolbox/events/mock"
)

func mockserver(t *testing.T, logger *logrus.Logger, fleetDBClient fleetdb.FleetDB, repository store.Repository, stream events.Stream) (*gin.Engine, error) {
	t.Helper()

	gin.SetMode(gin.ReleaseMode)
	g := gin.New()
	g.Use(gin.Recovery())

	options := []Option{
		WithLogger(logger),
		WithStore(repository),
		WithFleetDBClient(fleetDBClient),
		WithConditionDefinitions(
			[]*rctypes.Definition{
				{Kind: rctypes.FirmwareInstall},
			},
		),
	}

	if stream != nil {
		options = append(options, WithStreamBroker(stream))
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

func setupTestServer(t *testing.T) (*storeTest.MockRepository, *fleetdb.MockFleetDB, *mockevents.MockStream, *gin.Engine, error) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repository := storeTest.NewMockRepository(ctrl)

	fleetDBCtrl := gomock.NewController(t)
	defer fleetDBCtrl.Finish()

	fleetDBClient := fleetdb.NewMockFleetDB(fleetDBCtrl)

	streamCtrl := gomock.NewController(t)
	defer streamCtrl.Finish()

	stream := mockevents.NewMockStream(streamCtrl)

	server, err := mockserver(t, logrus.New(), fleetDBClient, repository, stream)

	return repository, fleetDBClient, stream, server, err
}

// nolint:gocyclo // cyclomatic tests are cyclomatic
func TestAddServer(t *testing.T) {
	repository, fleetDBClient, stream, server, err := setupTestServer(t)
	if err != nil {
		t.Fatal(err)
	}

	mockServerID := uuid.New()
	mockFacilityCode := "mock-facility-code"
	mockIP := "mock-ip"
	mockUser := "mock-user"
	mockPwd := "mock-pwd"
	validParams := types.AddServerParams{
		Facility: "mock-facility-code",
		IP:       "mock-ip",
		Username: "mock-user",
		Password: "mock-pwd",
	}
	// collect_bios_cfg is default to false since we don't set it in validParams.
	expectedInventoryParams := func(id string) string {
		return fmt.Sprintf(`{"collect_bios_cfg":true,"collect_firmware_status":true,"inventory_method":"outofband","asset_id":"%v"}`, id)
	}
	nopRollback := func() error {
		return nil
	}
	var generatedServerID uuid.UUID
	testcases := []struct {
		name              string
		mockStore         func(r *storeTest.MockRepository)
		mockFleetDBClient func(f *fleetdb.MockFleetDB)
		mockStream        func(r *mockevents.MockStream)
		request           func(t *testing.T) *http.Request
		assertResponse    func(t *testing.T, r *httptest.ResponseRecorder)
	}{
		{
			"add server success",
			// mock repository
			func(r *storeTest.MockRepository) {
				// create condition query
				r.EXPECT().
					Create(
						gomock.Any(),
						gomock.Eq(mockServerID),
						gomock.Any(),
					).
					DoAndReturn(func(_ context.Context, _ uuid.UUID, c *rctypes.Condition) error {
						assert.Equal(t, rctypes.ConditionStructVersion, c.Version, "condition version mismatch")
						assert.Equal(t, rctypes.Inventory, c.Kind, "condition kind mismatch")
						assert.Equal(t, json.RawMessage(expectedInventoryParams(mockServerID.String())), c.Parameters, "condition parameters mismatch")
						assert.Equal(t, rctypes.Pending, c.State, "condition state mismatch")
						return nil
					}).
					Times(1)
			},
			func(r *fleetdb.MockFleetDB) {
				// lookup for an existing condition
				r.EXPECT().
					AddServer(
						gomock.Any(),
						gomock.Eq(mockServerID),
						gomock.Eq(mockFacilityCode),
						gomock.Eq(mockIP),
						gomock.Eq(mockUser),
						gomock.Eq(mockPwd),
					).
					Return(nopRollback, nil). // no condition exists
					Times(1)
			},
			func(r *mockevents.MockStream) {
				r.EXPECT().
					Publish(
						gomock.Any(),
						gomock.Eq(fmt.Sprintf("%s.servers.%s", mockFacilityCode, rctypes.Inventory)),
						gomock.Any(),
					).
					Return(nil).
					Times(1)
			},
			func(t *testing.T) *http.Request {
				payload, err := json.Marshal(&v1types.ConditionCreate{Parameters: validParams.MustJSON()})
				if err != nil {
					t.Error()
				}

				url := fmt.Sprintf("/api/v1/serverEnroll/%v", mockServerID)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPost, url, bytes.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusOK, r.Code)
			},
		},
		{
			"add server invalid params",
			nil,
			nil,
			nil,
			func(t *testing.T) *http.Request {
				payload := []byte("invalid json")
				url := fmt.Sprintf("/api/v1/serverEnroll/%v", mockServerID)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPost, url, bytes.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "invalid ConditionCreate payload")
			},
		},
		{
			"no bmc user",
			nil,
			func(r *fleetdb.MockFleetDB) {
				// lookup for an existing condition
				r.EXPECT().
					AddServer(
						gomock.Any(),
						gomock.Eq(mockServerID),
						gomock.Eq(mockFacilityCode),
						gomock.Eq(mockIP),
						gomock.Eq(""),
						gomock.Eq(mockPwd),
					).
					Return(nopRollback, fleetdb.ErrBMCCredentials). // no condition exists
					Times(1)
			},
			nil,
			func(t *testing.T) *http.Request {
				noUserParams := types.AddServerParams{
					Facility: "mock-facility-code",
					IP:       "mock-ip",
					Password: "mock-pwd",
				}
				payload, err := json.Marshal(&v1types.ConditionCreate{Parameters: noUserParams.MustJSON()})
				if err != nil {
					t.Error()
				}

				url := fmt.Sprintf("/api/v1/serverEnroll/%v", mockServerID)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPost, url, bytes.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusInternalServerError, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), fleetdb.ErrBMCCredentials.Error())
			},
		},
		{
			"no bmc password",
			nil,
			func(r *fleetdb.MockFleetDB) {
				// lookup for an existing condition
				r.EXPECT().
					AddServer(
						gomock.Any(),
						gomock.Eq(mockServerID),
						gomock.Eq(mockFacilityCode),
						gomock.Eq(mockIP),
						gomock.Eq(mockUser),
						gomock.Eq(""),
					).
					Return(nopRollback, fleetdb.ErrBMCCredentials). // no condition exists
					Times(1)
			},
			nil,
			func(t *testing.T) *http.Request {
				noPwdParams := types.AddServerParams{
					Facility: "mock-facility-code",
					IP:       "mock-ip",
					Username: "mock-user",
				}
				payload, err := json.Marshal(&v1types.ConditionCreate{Parameters: noPwdParams.MustJSON()})
				if err != nil {
					t.Error()
				}

				url := fmt.Sprintf("/api/v1/serverEnroll/%v", mockServerID)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPost, url, bytes.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusInternalServerError, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), fleetdb.ErrBMCCredentials.Error())
			},
		},
		{
			"add server success no uuid param",
			// mock repository
			func(r *storeTest.MockRepository) {
				// create condition query
				r.EXPECT().
					Create(
						gomock.Any(),
						gomock.Any(),
						gomock.Any(),
					).
					DoAndReturn(func(_ context.Context, id uuid.UUID, c *rctypes.Condition) error {
						assert.Equal(t, generatedServerID, id, "server ID mismatch")
						assert.Equal(t, json.RawMessage(expectedInventoryParams(generatedServerID.String())), c.Parameters, "condition parameters mismatch")
						assert.Equal(t, rctypes.ConditionStructVersion, c.Version, "condition version mismatch")
						assert.Equal(t, rctypes.Inventory, c.Kind, "condition kind mismatch")
						assert.Equal(t, rctypes.Pending, c.State, "condition state mismatch")
						return nil
					}).
					Times(1)
			},
			func(r *fleetdb.MockFleetDB) {
				// lookup for an existing condition
				r.EXPECT().
					AddServer(
						gomock.Any(),
						gomock.Any(),
						gomock.Eq(mockFacilityCode),
						gomock.Eq(mockIP),
						gomock.Eq(mockUser),
						gomock.Eq(mockPwd),
					).
					DoAndReturn(func(ctx context.Context, serverID uuid.UUID, _, _, _, _ string) (func() error, error) {
						generatedServerID = serverID
						return nopRollback, nil
					}).
					Times(1)
			},
			func(r *mockevents.MockStream) {
				r.EXPECT().
					Publish(
						gomock.Any(),
						gomock.Eq(fmt.Sprintf("%s.servers.%s", mockFacilityCode, rctypes.Inventory)),
						gomock.Any(),
					).
					Return(nil).
					Times(1)
			},
			func(t *testing.T) *http.Request {
				payload, err := json.Marshal(&v1types.ConditionCreate{Parameters: validParams.MustJSON()})
				if err != nil {
					t.Error()
				}

				url := "/api/v1/serverEnroll/"
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPost, url, bytes.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusOK, r.Code)
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mockStore != nil {
				tc.mockStore(repository)
			}

			if tc.mockFleetDBClient != nil {
				tc.mockFleetDBClient(fleetDBClient)
			}

			if tc.mockStream != nil {
				tc.mockStream(stream)
			}

			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, tc.request(t))

			tc.assertResponse(t, recorder)
		})
	}
}

// nolint:gocyclo // cyclomatic tests are cyclomatic
func TestDeleteServer(t *testing.T) {
	repository, fleetDBClient, _, server, err := setupTestServer(t)
	if err != nil {
		t.Fatal(err)
	}

	mockServerID := uuid.New()
	testcases := []struct {
		name              string
		mockStore         func(r *storeTest.MockRepository)
		mockFleetDBClient func(f *fleetdb.MockFleetDB)
		request           func(t *testing.T) *http.Request
		assertResponse    func(t *testing.T, r *httptest.ResponseRecorder)
	}{
		{
			"delete server success",
			// mock repository
			func(r *storeTest.MockRepository) {
				// create condition query
				r.EXPECT().
					GetActiveCondition(
						gomock.Any(),
						gomock.Eq(mockServerID),
					).
					Return(nil, nil).
					Times(1)
			},
			func(r *fleetdb.MockFleetDB) {
				// lookup for an existing condition
				r.EXPECT().
					DeleteServer(
						gomock.Any(),
						gomock.Eq(mockServerID),
					).
					Return(nil). // no condition exists
					Times(1)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%v", mockServerID)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodDelete, url, nil)
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusOK, r.Code)
			},
		},
		{
			"invalid ID",
			// mock repository
			func(r *storeTest.MockRepository) {
				// create condition query
				r.EXPECT().
					GetActiveCondition(
						gomock.Any(),
						gomock.Eq(mockServerID),
					).
					Return(&rctypes.Condition{}, nil).
					Times(0)
			},
			func(r *fleetdb.MockFleetDB) {
				// lookup for an existing condition
				r.EXPECT().
					DeleteServer(
						gomock.Any(),
						gomock.Eq(mockServerID),
					).
					Return(nil). // no condition exists
					Times(0)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%v", "invalidID")
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodDelete, url, nil)
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
			},
		},
		{
			"active condition",
			// mock repository
			func(r *storeTest.MockRepository) {
				// create condition query
				r.EXPECT().
					GetActiveCondition(
						gomock.Any(),
						gomock.Eq(mockServerID),
					).
					Return(&rctypes.Condition{}, nil).
					Times(1)
			},
			func(r *fleetdb.MockFleetDB) {
				// lookup for an existing condition
				r.EXPECT().
					DeleteServer(
						gomock.Any(),
						gomock.Eq(mockServerID),
					).
					Return(nil). // no condition exists
					Times(0)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%v", mockServerID)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodDelete, url, nil)
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
			},
		},
		{
			"check active condition error",
			// mock repository
			func(r *storeTest.MockRepository) {
				// create condition query
				r.EXPECT().
					GetActiveCondition(
						gomock.Any(),
						gomock.Eq(mockServerID),
					).
					Return(nil, fmt.Errorf("fake check condition error")).
					Times(1)
			},
			func(r *fleetdb.MockFleetDB) {
				// lookup for an existing condition
				r.EXPECT().
					DeleteServer(
						gomock.Any(),
						gomock.Eq(mockServerID),
					).
					Return(fmt.Errorf("fake delete error")). // no condition exists
					Times(0)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%v", mockServerID)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodDelete, url, nil)
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusServiceUnavailable, r.Code)
			},
		},
		{
			"delete error",
			// mock repository
			func(r *storeTest.MockRepository) {
				// create condition query
				r.EXPECT().
					GetActiveCondition(
						gomock.Any(),
						gomock.Eq(mockServerID),
					).
					Return(nil, nil).
					Times(1)
			},
			func(r *fleetdb.MockFleetDB) {
				// lookup for an existing condition
				r.EXPECT().
					DeleteServer(
						gomock.Any(),
						gomock.Eq(mockServerID),
					).
					Return(fmt.Errorf("fake delete error")). // no condition exists
					Times(1)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%v", mockServerID)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodDelete, url, nil)
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusInternalServerError, r.Code)
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mockStore != nil {
				tc.mockStore(repository)
			}

			if tc.mockFleetDBClient != nil {
				tc.mockFleetDBClient(fleetDBClient)
			}

			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, tc.request(t))

			tc.assertResponse(t, recorder)
		})
	}
}

// nolint:gocyclo // cyclomatic tests are cyclomatic
func TestAddServerRollback(t *testing.T) {
	repository, fleetDBClient, stream, server, err := setupTestServer(t)
	if err != nil {
		t.Fatal(err)
	}

	rollbackCallCounter := 0
	rollback := func() error {
		rollbackCallCounter += 1
		return nil
	}

	mockServerID := uuid.New()
	validParams := fmt.Sprintf(`{"facility":"%v","ip":"192.168.0.1","user":"foo","pwd":"bar"}`, mockServerID)
	payload, err := json.Marshal(&v1types.ConditionCreate{Parameters: []byte(validParams)})
	if err != nil {
		t.Error()
	}

	requestFunc := func(t *testing.T) *http.Request {
		url := fmt.Sprintf("/api/v1/serverEnroll/%v", mockServerID)
		request, err := http.NewRequestWithContext(context.TODO(), http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		return request
	}

	type mockError struct {
		calledTime int
		err        error
	}

	testcases := []struct {
		name                 string
		mockStoreCreateErr   mockError
		mockFleetDBClientErr mockError
		mockStreamErr        mockError
		mockStoreUpdateErr   mockError
		request              func(t *testing.T) *http.Request
		assertResponse       func(t *testing.T, r *httptest.ResponseRecorder)
		expectRollback       int
	}{
		{
			"no error",
			mockError{1, nil},
			mockError{1, nil},
			mockError{1, nil},
			mockError{0, nil},
			requestFunc,
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusOK, r.Code)
			},
			0,
		},
		{
			"fleetdb error",
			mockError{0, nil},
			mockError{1, fmt.Errorf("fake fleetdb error")},
			mockError{0, nil},
			mockError{0, nil},
			requestFunc,
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusInternalServerError, r.Code)
			},
			1,
		},
		{
			"repository create error",
			mockError{1, fmt.Errorf("fake repository create error")},
			mockError{1, nil},
			mockError{0, nil},
			mockError{0, nil},
			requestFunc,
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusInternalServerError, r.Code)
			},
			1,
		},
		{
			"stream error",
			mockError{1, nil},
			mockError{1, nil},
			mockError{1, fmt.Errorf("fake stream error")},
			mockError{1, nil},
			requestFunc,
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusInternalServerError, r.Code)
			},
			1,
		},
		{
			"stream delete error",
			mockError{1, nil},
			mockError{1, nil},
			mockError{1, fmt.Errorf("fake stream error")},
			mockError{1, fmt.Errorf("fake repository delete error")},
			requestFunc,
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusInternalServerError, r.Code)
			},
			1,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			rollbackCallCounter = 0

			repository.EXPECT().
				Create(
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
				).
				DoAndReturn(func(_ context.Context, _ uuid.UUID, c *rctypes.Condition) error {
					return tc.mockStoreCreateErr.err
				}).
				Times(tc.mockStoreCreateErr.calledTime)

			fleetDBClient.EXPECT().
				AddServer(
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
				).
				Return(rollback, tc.mockFleetDBClientErr.err). // no condition exists
				Times(tc.mockFleetDBClientErr.calledTime)

			stream.EXPECT().
				Publish(
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
				).
				Return(tc.mockStreamErr.err).
				Times(tc.mockStreamErr.calledTime)

			repository.EXPECT().
				Update(
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
				).
				Return(tc.mockStoreUpdateErr.err).
				Times(tc.mockStoreUpdateErr.calledTime)

			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, tc.request(t))

			tc.assertResponse(t, recorder)
			if rollbackCallCounter != tc.expectRollback {
				t.Errorf("rollback called %v times, expect %v", rollbackCallCounter, tc.expectRollback)
			}
		})
	}
}

// nolint:gocyclo // cyclomatic tests are cyclomatic
func TestServerConditionCreate(t *testing.T) {
	serverID := uuid.New()
	facilityCode := "foo-42"

	// mock repository
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repository := storeTest.NewMockRepository(ctrl)

	fleetDBCtrl := gomock.NewController(t)
	defer fleetDBCtrl.Finish()

	fleetDBClient := fleetdb.NewMockFleetDB(fleetDBCtrl)

	streamCtrl := gomock.NewController(t)
	defer streamCtrl.Finish()

	stream := mockevents.NewMockStream(streamCtrl)

	server, err := mockserver(t, logrus.New(), fleetDBClient, repository, stream)
	if err != nil {
		t.Fatal(err)
	}

	testcases := []struct {
		name              string
		mockStore         func(r *storeTest.MockRepository)
		mockFleetDBClient func(f *fleetdb.MockFleetDB)
		mockStream        func(r *mockevents.MockStream)
		request           func(t *testing.T) *http.Request
		assertResponse    func(t *testing.T, r *httptest.ResponseRecorder)
	}{
		{
			"invalid server ID error",
			nil,
			nil,
			nil,
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", "123", "invalid")
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPost, url, http.NoBody)
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "invalid UUID")
			},
		},
		{
			"invalid server condition state",
			nil,
			nil,
			nil,
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", uuid.New().String(), "asdasd")
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPost, url, http.NoBody)
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "unsupported condition")
			},
		},
		{
			"invalid server condition payload returns error",
			nil,
			nil,
			nil,
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), rctypes.FirmwareInstall)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPost, url, bytes.NewReader([]byte(``)))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "invalid ConditionCreate payload")
			},
		},
		{
			"server with no facility code returns error",
			nil,
			func(m *fleetdb.MockFleetDB) {
				// facility code lookup
				m.EXPECT().
					GetServer(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(
						&model.Server{ID: serverID, FacilityCode: ""},
						nil,
					).
					Times(1)
			},
			nil,
			func(t *testing.T) *http.Request {
				payload, err := json.Marshal(&v1types.ConditionCreate{Parameters: []byte(`{"some param": "1"}`)})
				if err != nil {
					t.Error()
				}

				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), rctypes.FirmwareInstall)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPost, url, bytes.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusInternalServerError, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "no facility code")
			},
		},
		{
			"valid server condition created",
			// mock repository
			func(r *storeTest.MockRepository) {
				parametersJSON, _ := json.Marshal(json.RawMessage(`{"some param": "1"}`))

				// lookup for an existing condition
				r.EXPECT().
					GetActiveCondition(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(nil, nil). // no condition exists
					Times(1)
				// create condition query
				r.EXPECT().
					Create(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Any(),
					).
					DoAndReturn(func(_ context.Context, _ uuid.UUID, c *rctypes.Condition) error {
						assert.Equal(t, rctypes.ConditionStructVersion, c.Version, "condition version mismatch")
						assert.Equal(t, rctypes.FirmwareInstall, c.Kind, "condition kind mismatch")
						assert.Equal(t, json.RawMessage(parametersJSON), c.Parameters, "condition parameters mismatch")
						assert.Equal(t, rctypes.Pending, c.State, "condition state mismatch")
						return nil
					}).
					Times(1)
			},

			func(m *fleetdb.MockFleetDB) {
				// facility code lookup
				m.EXPECT().
					GetServer(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(
						&model.Server{ID: serverID, FacilityCode: facilityCode},
						nil,
					).
					Times(1)
			},

			func(r *mockevents.MockStream) {
				r.EXPECT().
					Publish(
						gomock.Any(),
						gomock.Eq(fmt.Sprintf("%s.servers.%s", facilityCode, rctypes.FirmwareInstall)),
						gomock.Any(),
					).
					Return(nil).
					Times(1)
			},
			func(t *testing.T) *http.Request {
				payload, err := json.Marshal(&v1types.ConditionCreate{Parameters: []byte(`{"some param": "1"}`)})
				if err != nil {
					t.Error()
				}

				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), rctypes.FirmwareInstall)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPost, url, bytes.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusOK, r.Code)
				var resp v1types.ServerResponse
				err := json.Unmarshal(r.Body.Bytes(), &resp)
				assert.NoError(t, err, "malformed response body")
				assert.Equal(t, "condition set", resp.Message)
				assert.Equal(t, 1, len(resp.Records.Conditions), "bad length of return conditions")
			},
		},
		{
			"condition with Fault created",
			// mock repository
			func(r *storeTest.MockRepository) {
				// lookup for an existing condition
				r.EXPECT().
					GetActiveCondition(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(nil, nil). // no condition exists
					Times(1)

				// create condition query
				r.EXPECT().
					Create(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Any(),
					).
					DoAndReturn(func(_ context.Context, _ uuid.UUID, c *rctypes.Condition) error {
						expect := &rctypes.Fault{Panic: true, DelayDuration: "10s", FailAt: "foobar"}
						assert.Equal(t, c.Fault, expect)
						return nil
					}).
					Times(1)
			},
			func(m *fleetdb.MockFleetDB) {
				// facility code lookup
				m.EXPECT().
					GetServer(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(
						&model.Server{ID: serverID, FacilityCode: facilityCode},
						nil,
					).
					Times(1)
			},

			func(r *mockevents.MockStream) {
				r.EXPECT().
					Publish(
						gomock.Any(),
						gomock.Eq(fmt.Sprintf("%s.servers.%s", facilityCode, rctypes.FirmwareInstall)),
						gomock.Any(),
					).
					Return(nil).
					Times(1)
			},
			func(t *testing.T) *http.Request {
				fault := rctypes.Fault{Panic: true, DelayDuration: "10s", FailAt: "foobar"}
				payload, err := json.Marshal(&v1types.ConditionCreate{Parameters: []byte(`{"some param": "1"}`), Fault: &fault})
				if err != nil {
					t.Error(err)
				}

				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), rctypes.FirmwareInstall)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPost, url, bytes.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusOK, r.Code)
				var resp v1types.ServerResponse
				err := json.Unmarshal(r.Body.Bytes(), &resp)
				assert.NoError(t, err, "malformed response body")
				assert.Equal(t, "condition set", resp.Message)
				assert.Equal(t, 1, len(resp.Records.Conditions), "bad length of return conditions")
			},
		},
		{
			"server condition exists in non-finalized state",
			// mock repository
			func(r *storeTest.MockRepository) {
				// lookup existing condition
				r.EXPECT().
					GetActiveCondition(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(&rctypes.Condition{ // condition present
						Kind:       rctypes.FirmwareInstall,
						State:      rctypes.Pending,
						Parameters: []byte(`{"hello":"world"}`),
					}, nil).
					Times(1)
			},
			func(m *fleetdb.MockFleetDB) {
				// facility code lookup
				m.EXPECT().
					GetServer(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(
						&model.Server{ID: serverID, FacilityCode: facilityCode},
						nil,
					).
					Times(1)
			},
			nil,
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), rctypes.FirmwareInstall)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPost, url, bytes.NewReader([]byte(`{"hello":"world"}`)))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "server has an active condition")
			},
		},
		{
			"server condition publish failure results in created condition deletion",
			// mock repository
			func(r *storeTest.MockRepository) {
				parametersJSON, _ := json.Marshal(json.RawMessage(`{"some param": "1"}`))

				// lookup for an existing condition
				r.EXPECT().
					GetActiveCondition(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(nil, nil). // no condition exists
					Times(1)
				// create condition query
				r.EXPECT().
					Create(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Any(),
					).
					DoAndReturn(func(_ context.Context, _ uuid.UUID, c *rctypes.Condition) error {
						assert.Equal(t, rctypes.ConditionStructVersion, c.Version, "condition version mismatch")
						assert.Equal(t, rctypes.FirmwareInstall, c.Kind, "condition kind mismatch")
						assert.Equal(t, json.RawMessage(parametersJSON), c.Parameters, "condition parameters mismatch")
						assert.Equal(t, rctypes.Pending, c.State, "condition state mismatch")
						return nil
					}).
					Times(1)

				// condition deletion due to publish failure
				r.EXPECT().
					Update(gomock.Any(), gomock.Eq(serverID), gomock.Any()).
					Times(1).DoAndReturn(func(_ context.Context, _ uuid.UUID, c *rctypes.Condition) error {
					require.Equal(t, rctypes.Failed, c.State)
					return nil
				})
			},

			func(m *fleetdb.MockFleetDB) {
				// facility code lookup
				m.EXPECT().
					GetServer(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(
						&model.Server{ID: serverID, FacilityCode: facilityCode},
						nil,
					).
					Times(1)
			},

			func(r *mockevents.MockStream) {
				r.EXPECT().
					Publish(
						gomock.Any(),
						gomock.Eq(fmt.Sprintf("%s.servers.%s", facilityCode, rctypes.FirmwareInstall)),
						gomock.Any(),
					).
					Return(errors.New("gremlins in the pipes")).
					Times(1)
			},
			func(t *testing.T) *http.Request {
				payload, err := json.Marshal(&v1types.ConditionCreate{Parameters: []byte(`{"some param": "1"}`)})
				if err != nil {
					t.Error()
				}

				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), rctypes.FirmwareInstall)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPost, url, bytes.NewReader(payload))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusInternalServerError, r.Code)
				var resp v1types.ServerResponse
				err = json.Unmarshal(r.Body.Bytes(), &resp)
				assert.Nil(t, err)
				assert.Contains(t, resp.Message, "gremlins")
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mockStore != nil {
				tc.mockStore(repository)
			}

			if tc.mockStream != nil {
				tc.mockStream(stream)
			}

			if tc.mockFleetDBClient != nil {
				tc.mockFleetDBClient(fleetDBClient)
			}

			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, tc.request(t))

			tc.assertResponse(t, recorder)
		})
	}
}

func TestConditionStatus(t *testing.T) {
	serverID := uuid.New()
	condID := uuid.New()
	testCondition := &rctypes.Condition{
		ID:   condID,
		Kind: rctypes.FirmwareInstall,
	}
	conditionRecord := &store.ConditionRecord{
		ID:    condID,
		State: rctypes.Pending,
		Conditions: []*rctypes.Condition{
			testCondition,
		},
	}

	// mock repository
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repository := storeTest.NewMockRepository(ctrl)

	server, err := mockserver(t, logrus.New(), nil, repository, nil)
	if err != nil {
		t.Fatal(err)
	}

	testcases := []struct {
		name           string
		mockStore      func(r *storeTest.MockRepository)
		request        func(t *testing.T) *http.Request
		assertResponse func(t *testing.T, r *httptest.ResponseRecorder)
	}{
		{
			"invalid server ID error",
			nil,
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/status", "123")
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodGet, url, http.NoBody)
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "invalid UUID")
			},
		},
		{
			"server condition record returned",
			// mock repository
			func(r *storeTest.MockRepository) {
				r.EXPECT().
					Get(gomock.Any(), gomock.Eq(serverID)).
					Times(1).
					Return(conditionRecord, nil)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/status", serverID.String())

				request, err := http.NewRequestWithContext(context.TODO(), http.MethodGet, url, http.NoBody)
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusOK, r.Code)

				want := asJSONBytes(
					t,
					&v1types.ServerResponse{
						Records: &v1types.ConditionsResponse{
							ServerID: serverID,
							State:    rctypes.Pending,
							Conditions: []*rctypes.Condition{
								testCondition,
							},
						},
					},
				)

				assert.Equal(t, asBytes(t, r.Body), want)
			},
		},
		{
			"no server condition",
			// mock repository
			func(r *storeTest.MockRepository) {
				r.EXPECT().
					Get(gomock.Any(), gomock.Eq(serverID)).
					Times(1).
					Return(nil, store.ErrConditionNotFound)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/status", serverID.String())

				request, err := http.NewRequestWithContext(context.TODO(), http.MethodGet, url, http.NoBody)
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusNotFound, r.Code)

				want := asJSONBytes(
					t,
					&v1types.ServerResponse{
						Message: "condition not found for server",
					},
				)

				assert.Equal(t, asBytes(t, r.Body), want)
			},
		},
		{
			"lookup error",
			// mock repository
			func(r *storeTest.MockRepository) {
				r.EXPECT().
					Get(gomock.Any(), gomock.Eq(serverID)).
					Times(1).
					Return(nil, errors.New("bogus error"))
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/status", serverID.String())

				request, err := http.NewRequestWithContext(context.TODO(), http.MethodGet, url, http.NoBody)
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusServiceUnavailable, r.Code)

				want := asJSONBytes(
					t,
					&v1types.ServerResponse{
						Message: "condition lookup: bogus error",
					},
				)

				assert.Equal(t, asBytes(t, r.Body), want)
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mockStore != nil {
				tc.mockStore(repository)
			}

			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, tc.request(t))

			tc.assertResponse(t, recorder)
		})
	}
}
