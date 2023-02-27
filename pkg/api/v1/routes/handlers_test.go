package routes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/store"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func mockserver(t *testing.T, logger *logrus.Logger, repository store.Repository) (*gin.Engine, error) {
	t.Helper()

	gin.SetMode(gin.ReleaseMode)
	g := gin.New()
	g.Use(gin.Recovery())

	options := []Option{
		WithLogger(logger),
		WithStore(repository),
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

func asJSONBytes(t *testing.T, s *ServerResponse) []byte {
	t.Helper()

	b, err := json.Marshal(s)
	if err != nil {
		t.Error(err)
	}

	return b
}

func TestServerConditionUpdate(t *testing.T) {
	serverID := uuid.New()

	payloadInvalid, err := json.Marshal(map[string]string{"foo": "bar"})
	if err != nil {
		t.Error(err)
	}

	updateValid := ConditionUpdate{
		State:           ptypes.Active,
		Status:          []byte(`{"foo": "bar"}`),
		ResourceVersion: int64(time.Now().Nanosecond()),
	}

	payloadValid, err := json.Marshal(updateValid)
	if err != nil {
		t.Error(err)
	}

	// mock repository
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repository := store.NewMockRepository(ctrl)

	server, err := mockserver(t, logrus.New(), repository)
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
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", "123", "invalid")
				request, err := http.NewRequest(http.MethodPut, url, http.NoBody)
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
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", uuid.New().String(), "asdasd")
				request, err := http.NewRequest(http.MethodPut, url, http.NoBody)
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
			"invalid server condition update payload returns error",
			nil,
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), ptypes.FirmwareInstallOutofband)
				request, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(payloadInvalid))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "invalid ConditionUpdate payload")
			},
		},
		{
			"update on non existing server condition returns error",
			// mock repository
			func(r *store.MockRepository) {
				// lookup existing condition
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(ptypes.FirmwareInstallOutofband),
					).
					Return(nil, nil).
					Times(1)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), ptypes.FirmwareInstallOutofband)
				request, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(payloadValid))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "no existing condition found for update")
			},
		},
		{
			"update successful",
			// mock repository
			func(r *store.MockRepository) {
				// lookup for existing condition
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(ptypes.FirmwareInstallOutofband),
					).
					Return(&ptypes.Condition{ // condition present
						Kind:            ptypes.FirmwareInstallOutofband,
						State:           updateValid.State,
						Status:          updateValid.Status,
						ResourceVersion: updateValid.ResourceVersion,
					}, nil).
					Times(1)

				r.EXPECT().
					Update(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Any(),
					).
					Return(nil).
					Times(1)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), ptypes.FirmwareInstallOutofband)
				request, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(payloadValid))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusOK, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "condition updated")
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

func TestServerConditionCreate(t *testing.T) {
	serverID := uuid.New()

	// valid parameters for test
	parametersValid := &ptypes.FirmwareInstallOutofbandParameters{
		FirmwareSetID: uuid.NewString(),
	}

	paramsJSONValid, _ := json.Marshal(parametersValid)

	createValid := ConditionCreate{
		Parameters: paramsJSONValid,
	}

	payloadValid, err := json.Marshal(createValid)
	if err != nil {
		t.Error()
	}

	createInvalid := ConditionCreate{
		Parameters: []byte(`{"foo": "bar"}`),
	}

	payloadInvalid, err := json.Marshal(createInvalid)
	if err != nil {
		t.Error()
	}

	// mock repository
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repository := store.NewMockRepository(ctrl)

	server, err := mockserver(t, logrus.New(), repository)
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
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", "123", "invalid")
				request, err := http.NewRequest(http.MethodPost, url, http.NoBody)
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
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", uuid.New().String(), "asdasd")
				request, err := http.NewRequest(http.MethodPost, url, http.NoBody)
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
			"invalid server condition returns error",
			nil,
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), ptypes.FirmwareInstallOutofband)
				request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payloadInvalid))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "error in condition parameter")
			},
		},
		{
			"valid server condition created",
			// mock repository
			func(r *store.MockRepository) {
				// lookup for an existing condition
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(ptypes.FirmwareInstallOutofband),
					).
					Return(nil, nil). // no condition exists
					Times(1)

				// create condition query
				r.EXPECT().
					Create(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(
							&ptypes.Condition{
								Kind:       ptypes.FirmwareInstallOutofband,
								Parameters: parametersValid,
								State:      ptypes.Pending,
							},
						),
					).
					Times(1)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), ptypes.FirmwareInstallOutofband)
				request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payloadValid))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusOK, r.Code)
				assert.Equal(t, asBytes(t, r.Body), asJSONBytes(t, &ServerResponse{Message: "condition set"}))
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
						gomock.Eq(ptypes.FirmwareInstallOutofband),
					).
					Return(&ptypes.Condition{ // condition present
						Kind:       ptypes.FirmwareInstallOutofband,
						Parameters: parametersValid,
						State:      ptypes.Pending,
					}, nil).
					Times(1)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), ptypes.FirmwareInstallOutofband)
				request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payloadValid))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
				assert.Contains(t, string(asBytes(t, r.Body)), "condition present non-finalized state")
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

func TestServerConditionList(t *testing.T) {
	var firmwareInstall ptypes.ConditionKind = "firmwareInstall"
	var inventoryOutofband ptypes.ConditionKind = "inventoryOutofband"

	serverID := uuid.New()
	conditions := []*ptypes.Condition{
		{
			Kind: firmwareInstall,
			Parameters: &ptypes.FirmwareInstallOutofbandParameters{
				FirmwareSetID: uuid.NewString(),
			},
			State: ptypes.Pending,
		},
		{
			Kind:       inventoryOutofband,
			Parameters: &ptypes.InventoryOutofbandParameters{},
			State:      ptypes.Pending,
		},
	}

	// mock repository
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repository := store.NewMockRepository(ctrl)

	server, err := mockserver(t, logrus.New(), repository)
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
				url := fmt.Sprintf("/api/v1/servers/%s/state/%s", "123", "invalid")
				request, err := http.NewRequest(http.MethodGet, url, http.NoBody)
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
			"invalid server condition state requested",
			nil,
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/state/%s", uuid.New().String(), "asdasd")
				request, err := http.NewRequest(http.MethodGet, url, http.NoBody)
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
			"server condition records returned",
			// mock repository
			func(r *store.MockRepository) {
				r.EXPECT().
					List(gomock.Any(), gomock.Eq(serverID), gomock.Eq(ptypes.Pending)).
					Times(1).
					Return(conditions, nil)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/state/%s", serverID.String(), ptypes.Pending)

				request, err := http.NewRequest(http.MethodGet, url, http.NoBody)
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusOK, r.Code)

				want := asJSONBytes(
					t,
					&ServerResponse{
						Records: ConditionsResponse{
							ServerID:   serverID,
							Conditions: conditions,
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

func TestServerConditionGet(t *testing.T) {
	serverID := uuid.New()
	condition := &ptypes.Condition{
		Kind:       ptypes.FirmwareInstallOutofband,
		Parameters: &ptypes.FirmwareInstallOutofbandParameters{},
	}

	// mock repository
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repository := store.NewMockRepository(ctrl)

	server, err := mockserver(t, logrus.New(), repository)
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
				request, err := http.NewRequest(http.MethodGet, url, http.NoBody)
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
				request, err := http.NewRequest(http.MethodGet, url, http.NoBody)
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
					Get(gomock.Any(), gomock.Eq(serverID), gomock.Eq(ptypes.FirmwareInstallOutofband)).
					Times(1).
					Return(condition, nil)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), ptypes.FirmwareInstallOutofband)

				request, err := http.NewRequest(http.MethodGet, url, http.NoBody)
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusOK, r.Code)

				want := asJSONBytes(
					t,
					&ServerResponse{
						Record: ConditionResponse{
							ServerID:  serverID,
							Condition: condition,
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

func TestServerConditionDelete(t *testing.T) {
	serverID := uuid.New()

	// mock repository
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repository := store.NewMockRepository(ctrl)

	server, err := mockserver(t, logrus.New(), repository)
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
				request, err := http.NewRequest(http.MethodDelete, url, http.NoBody)
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
				request, err := http.NewRequest(http.MethodDelete, url, http.NoBody)
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
			"no such condition on server returns error",
			// mock repository
			func(r *store.MockRepository) {
				r.EXPECT().
					Get(gomock.Any(), gomock.Eq(serverID), gomock.Eq(ptypes.FirmwareInstallOutofband)).
					Times(1).
					Return(nil, nil)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), ptypes.FirmwareInstallOutofband)

				request, err := http.NewRequest(http.MethodDelete, url, http.NoBody)
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)

				want := asJSONBytes(
					t,
					&ServerResponse{
						Message: "no such condition found",
					},
				)
				assert.Equal(t, asBytes(t, r.Body), want)
			},
		},
		{
			"condition deleted successfully",
			// mock repository
			func(r *store.MockRepository) {
				r.EXPECT().
					Get(gomock.Any(), gomock.Eq(serverID), gomock.Eq(ptypes.FirmwareInstallOutofband)).
					Times(1).
					Return(&ptypes.Condition{Kind: ptypes.FirmwareInstallOutofband}, nil)

				r.EXPECT().
					Delete(gomock.Any(), gomock.Eq(serverID), gomock.Eq(ptypes.FirmwareInstallOutofband)).
					Times(1).
					Return(nil)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), ptypes.FirmwareInstallOutofband)

				request, err := http.NewRequest(http.MethodDelete, url, http.NoBody)
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusOK, r.Code)

				want := asJSONBytes(
					t,
					&ServerResponse{
						Message: "condition deleted",
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
