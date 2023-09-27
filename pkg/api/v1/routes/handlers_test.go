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
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/model"
	"github.com/metal-toolbox/conditionorc/internal/store"
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	condition "github.com/metal-toolbox/rivets/condition"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"go.hollow.sh/toolbox/events"
	mockevents "go.hollow.sh/toolbox/events/mock"
)

func mockserver(t *testing.T, logger *logrus.Logger, repository store.Repository, stream events.Stream) (*gin.Engine, error) {
	t.Helper()

	gin.SetMode(gin.ReleaseMode)
	g := gin.New()
	g.Use(gin.Recovery())

	options := []Option{
		WithLogger(logger),
		WithStore(repository),
		WithConditionDefinitions(
			[]*condition.Definition{
				{Kind: condition.FirmwareInstall},
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

func TestServerConditionUpdate(t *testing.T) {
	serverID := uuid.New()

	payloadInvalid, err := json.Marshal(map[string]string{"foo": "bar"})
	if err != nil {
		t.Error(err)
	}

	updateNoState, err := json.Marshal(&v1types.ConditionUpdate{
		Status:          []byte(`{"hi": "there"}`),
		ResourceVersion: int64(1),
	})
	if err != nil {
		t.Error(err)
	}

	updateNoStatus, err := json.Marshal(&v1types.ConditionUpdate{
		State:           condition.Active,
		ResourceVersion: int64(1),
	})
	if err != nil {
		t.Error(err)
	}

	updateValid := v1types.ConditionUpdate{
		ConditionID:     uuid.New(),
		ServerID:        serverID,
		State:           condition.Active,
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

	server, err := mockserver(t, logrus.New(), repository, nil)
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
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPut, url, http.NoBody)
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
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPut, url, http.NoBody)
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
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), condition.FirmwareInstall)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPut, url, bytes.NewReader(payloadInvalid))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
				responseBody := string(asBytes(t, r.Body))
				assert.Contains(t, responseBody, "invalid ConditionUpdate payload")
			},
		},
		{
			"update with no state returns an error",
			nil,
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), condition.FirmwareInstall)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPut, url, bytes.NewReader(updateNoState))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
				responseBody := string(asBytes(t, r.Body))
				assert.Contains(t, responseBody, "invalid ConditionUpdate payload")
			},
		},
		{
			"update with no status returns an error",
			nil,
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), condition.FirmwareInstall)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPut, url, bytes.NewReader(updateNoStatus))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
				responseBody := string(asBytes(t, r.Body))
				assert.Contains(t, responseBody, "invalid ConditionUpdate payload")
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
						gomock.Eq(condition.FirmwareInstall),
					).
					Return(nil, nil).
					Times(1)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), condition.FirmwareInstall)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPut, url, bytes.NewReader(payloadValid))
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusNotFound, r.Code)
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
						gomock.Eq(condition.FirmwareInstall),
					).
					Return(&condition.Condition{ // condition present
						ID:              updateValid.ConditionID,
						Kind:            condition.FirmwareInstall,
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
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), condition.FirmwareInstall)
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodPut, url, bytes.NewReader(payloadValid))
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

	server, err := mockserver(t, logrus.New(), repository, stream)
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
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), condition.FirmwareInstall)
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
						gomock.Eq(condition.FirmwareInstall),
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

				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), condition.FirmwareInstall)
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
						gomock.Eq(condition.FirmwareInstall),
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
					DoAndReturn(func(_ context.Context, _ uuid.UUID, c *condition.Condition) error {
						assert.Equal(t, condition.ConditionStructVersion, c.Version, "condition version mismatch")
						assert.Equal(t, condition.FirmwareInstall, c.Kind, "condition kind mismatch")
						assert.Equal(t, json.RawMessage(parametersJSON), c.Parameters, "condition parameters mismatch")
						assert.Equal(t, condition.Pending, c.State, "condition state mismatch")
						return nil
					}).
					Times(1)
			},
			func(r *mockevents.MockStream) {
				r.EXPECT().
					Publish(
						gomock.Any(),
						gomock.Eq(fmt.Sprintf("%s.servers.%s", facilityCode, condition.FirmwareInstall)),
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

				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), condition.FirmwareInstall)
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
						gomock.Eq(condition.FirmwareInstall),
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
					DoAndReturn(func(_ context.Context, _ uuid.UUID, c *condition.Condition) error {
						expect := &condition.Fault{Panic: true, DelayDuration: "10s", FailAt: "foobar"}
						assert.Equal(t, c.Fault, expect)
						return nil
					}).
					Times(1)
			},
			func(r *mockevents.MockStream) {
				r.EXPECT().
					Publish(
						gomock.Any(),
						gomock.Eq(fmt.Sprintf("%s.servers.%s", facilityCode, condition.FirmwareInstall)),
						gomock.Any(),
					).
					Return(nil).
					Times(1)
			},
			func(t *testing.T) *http.Request {
				fault := condition.Fault{Panic: true, DelayDuration: "10s", FailAt: "foobar"}
				payload, err := json.Marshal(&v1types.ConditionCreate{Parameters: []byte(`{"some param": "1"}`), Fault: &fault})
				if err != nil {
					t.Error(err)
				}

				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), condition.FirmwareInstall)
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
						gomock.Eq(condition.FirmwareInstall),
					).
					Return(&condition.Condition{
						Kind:  condition.FirmwareInstall,
						State: condition.Succeeded,
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
						gomock.Eq(condition.FirmwareInstall),
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
					DoAndReturn(func(_ context.Context, _ uuid.UUID, c *condition.Condition) error {
						assert.Equal(t, condition.ConditionStructVersion, c.Version, "condition version mismatch")
						assert.Equal(t, condition.FirmwareInstall, c.Kind, "condition kind mismatch")
						assert.Equal(t, json.RawMessage(parametersJSON), c.Parameters, "condition parameters mismatch")
						assert.Equal(t, condition.Pending, c.State, "condition state mismatch")
						return nil
					}).
					Times(1)
			},
			func(r *mockevents.MockStream) {
				r.EXPECT().
					Publish(
						gomock.Any(),
						gomock.Eq(fmt.Sprintf("%s.servers.%s", facilityCode, condition.FirmwareInstall)),
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

				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), condition.FirmwareInstall)
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
						gomock.Eq(condition.FirmwareInstall),
					).
					Return(&condition.Condition{ // condition present
						Kind:       condition.FirmwareInstall,
						State:      condition.Pending,
						Parameters: []byte(`{"hello":"world"}`),
					}, nil).
					Times(1)
			},
			func(r *mockevents.MockStream) {
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), condition.FirmwareInstall)
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
						gomock.Eq(condition.FirmwareInstall),
					).
					Return(nil, nil).
					Times(1)

				r.EXPECT().
					List(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(condition.Active),
					).
					Return([]*condition.Condition{
						{
							Kind:      condition.Inventory,
							Exclusive: true,
							State:     condition.Active,
						},
					}, nil).
					Times(1)
			},
			func(r *mockevents.MockStream) {
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), condition.FirmwareInstall)
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
						gomock.Eq(condition.FirmwareInstall),
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
					DoAndReturn(func(_ context.Context, _ uuid.UUID, c *condition.Condition) error {
						assert.Equal(t, condition.ConditionStructVersion, c.Version, "condition version mismatch")
						assert.Equal(t, condition.FirmwareInstall, c.Kind, "condition kind mismatch")
						assert.Equal(t, json.RawMessage(parametersJSON), c.Parameters, "condition parameters mismatch")
						assert.Equal(t, condition.Pending, c.State, "condition state mismatch")
						return nil
					}).
					Times(1)

				// condition deletion due to publish failure
				r.EXPECT().
					Delete(gomock.Any(), gomock.Eq(serverID), gomock.Eq(condition.FirmwareInstall)).
					Times(1).
					Return(nil)
			},
			func(r *mockevents.MockStream) {
				r.EXPECT().
					Publish(
						gomock.Any(),
						gomock.Eq(fmt.Sprintf("%s.servers.%s", facilityCode, condition.FirmwareInstall)),
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

				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), condition.FirmwareInstall)
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

func TestServerConditionList(t *testing.T) {
	var firmwareInstall condition.Kind = "firmwareInstall"

	var Inventory condition.Kind = "Inventory"

	serverID := uuid.New()

	conditions := []*condition.Condition{
		{
			Kind:       firmwareInstall,
			Parameters: []byte(`{"FirmwareSetID": "123"}`),
			State:      condition.Pending,
		},
		{
			Kind:       Inventory,
			Parameters: []byte(`{}`),
			State:      condition.Pending,
		},
	}

	// mock repository
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repository := store.NewMockRepository(ctrl)

	server, err := mockserver(t, logrus.New(), repository, nil)
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
			"invalid server condition state requested",
			nil,
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/state/%s", uuid.New().String(), "asdasd")
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
			"server condition records returned",
			// mock repository
			func(r *store.MockRepository) {
				r.EXPECT().
					List(gomock.Any(), gomock.Eq(serverID), gomock.Eq(condition.Pending)).
					Times(1).
					Return(conditions, nil)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/state/%s", serverID.String(), condition.Pending)

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
	testCondition := &condition.Condition{
		Kind:       condition.FirmwareInstall,
		Parameters: json.RawMessage{},
	}

	// mock repository
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repository := store.NewMockRepository(ctrl)

	server, err := mockserver(t, logrus.New(), repository, nil)
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
					Get(gomock.Any(), gomock.Eq(serverID), gomock.Eq(condition.FirmwareInstall)).
					Times(1).
					Return(testCondition, nil)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), condition.FirmwareInstall)

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
							Conditions: []*condition.Condition{
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

func TestServerConditionDelete(t *testing.T) {
	serverID := uuid.New()

	// mock repository
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repository := store.NewMockRepository(ctrl)

	server, err := mockserver(t, logrus.New(), repository, nil)
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
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodDelete, url, http.NoBody)
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
				request, err := http.NewRequestWithContext(context.TODO(), http.MethodDelete, url, http.NoBody)
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
			"condition deleted successfully",
			// mock repository
			func(r *store.MockRepository) {
				r.EXPECT().
					Delete(gomock.Any(), gomock.Eq(serverID), gomock.Eq(condition.FirmwareInstall)).
					Times(1).
					Return(nil)
			},
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/condition/%s", serverID.String(), condition.FirmwareInstall)

				request, err := http.NewRequestWithContext(context.TODO(), http.MethodDelete, url, http.NoBody)
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
