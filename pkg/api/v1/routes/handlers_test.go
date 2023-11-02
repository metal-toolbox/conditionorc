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
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
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

// nolint:gocyclo // cyclomatic tests are cyclomatic
func TestAddServer(t *testing.T) {
	// mock repository
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repository := store.NewMockRepository(ctrl)

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

	mockServerID := uuid.New()
	mockFacilityCode := "mock-facility-code"
	mockIP := "mock-ip"
	mockUser := "mock-user"
	mockPwd := "mock-pwd"
	validParams := fmt.Sprintf(`{"facility":"%v","ip":"%v","user":"%v","pwd":"%v","some param":"1"}`, mockFacilityCode, mockIP, mockUser, mockPwd)

	testcases := []struct {
		name              string
		mockStore         func(r *store.MockRepository)
		mockFleetDBClient func(f *fleetdb.MockFleetDB)
		mockStream        func(r *mockevents.MockStream)
		request           func(t *testing.T) *http.Request
		assertResponse    func(t *testing.T, r *httptest.ResponseRecorder)
	}{
		{
			"add server success",
			// mock repository
			func(r *store.MockRepository) {
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
						assert.Equal(t, json.RawMessage(validParams), c.Parameters, "condition parameters mismatch")
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
					Return(nil). // no condition exists
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
				payload, err := json.Marshal(&v1types.ConditionCreate{Parameters: []byte(validParams)})
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
					Return(fleetdb.ErrBMCCredentials). // no condition exists
					Times(1)
			},
			nil,
			func(t *testing.T) *http.Request {
				noUserParams := fmt.Sprintf(`{"facility":"%v","ip":"%v","pwd":"%v","some param":"1"}`, mockFacilityCode, mockIP, mockPwd)
				payload, err := json.Marshal(&v1types.ConditionCreate{Parameters: []byte(noUserParams)})
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
					Return(fleetdb.ErrBMCCredentials). // no condition exists
					Times(1)
			},
			nil,
			func(t *testing.T) *http.Request {
				noPwdParams := fmt.Sprintf(`{"facility":"%v","ip":"%v","user":"%v","some param":"1"}`, mockFacilityCode, mockIP, mockUser)
				payload, err := json.Marshal(&v1types.ConditionCreate{Parameters: []byte(noPwdParams)})
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
func TestServerConditionCreate(t *testing.T) {
	serverID := uuid.New()
	facilityCode := "foo-42"

	// mock repository
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repository := store.NewMockRepository(ctrl)

	streamCtrl := gomock.NewController(t)
	defer streamCtrl.Finish()

	stream := mockevents.NewMockStream(streamCtrl)

	server, err := mockserver(t, logrus.New(), nil, repository, stream)
	if err != nil {
		t.Fatal(err)
	}

	testcases := []struct {
		name           string
		mockStore      func(r *store.MockRepository)
		mockStream     func(r *mockevents.MockStream)
		request        func(t *testing.T) *http.Request
		assertResponse func(t *testing.T, r *httptest.ResponseRecorder)
	}{
		{
			"invalid server ID error",
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
			// mock repository
			func(r *store.MockRepository) {
				// lookup for an existing condition
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(rctypes.FirmwareInstall),
					).
					Return(nil, nil). // no condition exists
					Times(1)

				r.EXPECT().
					List(
						gomock.Any(),
						gomock.Any(),
						gomock.Any(),
					).
					Return(nil, nil).
					Times(2)

				// facility code lookup
				r.EXPECT().
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
			func(r *store.MockRepository) {
				parametersJSON, _ := json.Marshal(json.RawMessage(`{"some param": "1"}`))

				// lookup for an existing condition
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(rctypes.FirmwareInstall),
					).
					Return(nil, nil). // no condition exists
					Times(1)

				r.EXPECT().
					List(
						gomock.Any(),
						gomock.Any(),
						gomock.Any(),
					).
					Return(nil, nil).
					Times(2)

				// facility code lookup
				r.EXPECT().
					GetServer(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(
						&model.Server{ID: serverID, FacilityCode: facilityCode},
						nil,
					).
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
			func(r *store.MockRepository) {
				// lookup for an existing condition
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(rctypes.FirmwareInstall),
					).
					Return(nil, nil). // no condition exists
					Times(1)

				r.EXPECT().
					List(
						gomock.Any(),
						gomock.Any(),
						gomock.Any(),
					).
					Return(nil, nil).
					Times(2)

				// facility code lookup
				r.EXPECT().
					GetServer(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(
						&model.Server{ID: serverID, FacilityCode: facilityCode},
						nil,
					).
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
			"new condition replaces existing finalized condition",
			// mock repository
			func(r *store.MockRepository) {
				parametersJSON, err := json.Marshal(json.RawMessage(`{"some param": "1"}`))
				if err != nil {
					t.Error()
				}

				// lookup for an existing condition
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(rctypes.FirmwareInstall),
					).
					Return(&rctypes.Condition{
						Kind:  rctypes.FirmwareInstall,
						State: rctypes.Succeeded,
					}, nil). // no condition exists
					Times(1)

				r.EXPECT().
					List(
						gomock.Any(),
						gomock.Any(),
						gomock.Any(),
					).
					Return(nil, nil).
					Times(2)

				r.EXPECT().
					Delete(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(rctypes.FirmwareInstall),
					).
					Return(nil).
					Times(1)

				// facility code lookup
				r.EXPECT().
					GetServer(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(
						&model.Server{ID: serverID, FacilityCode: facilityCode},
						nil,
					).
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
			"server condition exists in non-finalized state",
			// mock repository
			func(r *store.MockRepository) {
				// lookup existing condition
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(rctypes.FirmwareInstall),
					).
					Return(&rctypes.Condition{ // condition present
						Kind:       rctypes.FirmwareInstall,
						State:      rctypes.Pending,
						Parameters: []byte(`{"hello":"world"}`),
					}, nil).
					Times(1)
			},
			func(r *mockevents.MockStream) {
			},
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
				assert.Contains(t, string(asBytes(t, r.Body)), "condition present in an incomplete state")
			},
		},
		{
			"exclusive condition exists in non-finalized state",
			// mock repository
			func(r *store.MockRepository) {
				// lookup existing condition
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(rctypes.FirmwareInstall),
					).
					Return(nil, nil).
					Times(1)

				r.EXPECT().
					List(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(rctypes.Active),
					).
					Return([]*rctypes.Condition{
						{
							Kind:      rctypes.Inventory,
							Exclusive: true,
							State:     rctypes.Active,
						},
					}, nil).
					Times(1)
			},
			func(r *mockevents.MockStream) {
			},
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
				assert.Contains(t, string(asBytes(t, r.Body)), "exclusive condition present")
			},
		},
		{
			"server condition publish failure results in created condition deletion",
			// mock repository
			func(r *store.MockRepository) {
				parametersJSON, _ := json.Marshal(json.RawMessage(`{"some param": "1"}`))

				// lookup for an existing condition
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(rctypes.FirmwareInstall),
					).
					Return(nil, nil). // no condition exists
					Times(1)

				r.EXPECT().
					List(
						gomock.Any(),
						gomock.Any(),
						gomock.Any(),
					).
					Return(nil, nil).
					Times(2)

				// facility code lookup
				r.EXPECT().
					GetServer(
						gomock.Any(),
						gomock.Eq(serverID),
					).
					Return(
						&model.Server{ID: serverID, FacilityCode: facilityCode},
						nil,
					).
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
					Delete(gomock.Any(), gomock.Eq(serverID), gomock.Eq(rctypes.FirmwareInstall)).
					Times(1).
					Return(nil)
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

			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, tc.request(t))

			tc.assertResponse(t, recorder)
		})
	}
}

func TestServerConditionGet(t *testing.T) {
	serverID := uuid.New()
	testCondition := &rctypes.Condition{
		Kind:       rctypes.FirmwareInstall,
		Parameters: json.RawMessage{},
	}

	// mock repository
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repository := store.NewMockRepository(ctrl)

	server, err := mockserver(t, logrus.New(), nil, repository, nil)
	if err != nil {
		t.Fatal(err)
	}

	testcases := []struct {
		name           string
		mockStore      func(r *store.MockRepository)
		request        func(t *testing.T) *http.Request
		assertResponse func(t *testing.T, r *httptest.ResponseRecorder)
	}{
		{
			"invalid server ID error",
			nil,
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", "123", "asdasd")
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
			"invalid condition requested",
			nil,
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", uuid.New().String(), "asdasd")
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodGet, url, http.NoBody)
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
			"server condition record returned",
			// mock repository
			func(r *store.MockRepository) {
				r.EXPECT().
					Get(gomock.Any(), gomock.Eq(serverID), gomock.Eq(rctypes.FirmwareInstall)).
					Times(1).
					Return(testCondition, nil)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), rctypes.FirmwareInstall)

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
							Conditions: []*rctypes.Condition{
								testCondition,
							},
						},
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
