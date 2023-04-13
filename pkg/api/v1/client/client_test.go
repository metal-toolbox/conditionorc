package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/server"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/stretchr/testify/assert"

	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/sirupsen/logrus"
)

type integrationTester struct {
	handler    http.Handler
	client     *Client
	repository *store.MockRepository
}

// Do implements the HTTPRequestDoer interface to swap the response writer
func (i *integrationTester) Do(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}

	w := httptest.NewRecorder()
	i.handler.ServeHTTP(w, req)

	return w.Result(), nil
}

func newTester(t *testing.T) *integrationTester {
	t.Helper()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repository := store.NewMockRepository(ctrl)

	l := logrus.New()
	l.Level = logrus.Level(logrus.ErrorLevel)
	options := []server.Option{
		server.WithLogger(l),
		server.WithListenAddress("localhost:9999"),
		server.WithStore(repository),
		server.WithConditionDefinitions(
			[]*ptypes.ConditionDefinition{
				{Kind: ptypes.FirmwareInstallOutofband},
			},
		),
	}

	gin.SetMode(gin.ReleaseMode)

	srv := server.New(options...)

	tester := &integrationTester{
		handler:    srv.Handler,
		repository: repository,
	}

	client, err := NewClient("http://localhost:9999", WithHTTPClient(tester))
	if err != nil {
		t.Error(err)
	}

	tester.client = client

	return tester
}

// Firmware holds parameters for a firmware install and is part of FirmwareInstallOutofbandParameters.
//
// defined here for tests, this will be made available in a shared package at some point.
type Firmware struct {
	Version       string `yaml:"version"`
	URL           string `yaml:"URL"`
	FileName      string `yaml:"filename"`
	Utility       string `yaml:"utility"`
	Model         string `yaml:"model"`
	Vendor        string `yaml:"vendor"`
	ComponentSlug string `yaml:"componentslug"`
	Checksum      string `yaml:"checksum"`
}

// firmwareInstallParameters define firmwareInstall condition parameters.
//
// defined here for tests, this will be made available in a shared package at some point.
type FirmwareInstallOutofbandParameters struct {
	InventoryAfterUpdate bool        `json:"inventoryAfterUpdate,omitempty"`
	ForceInstall         bool        `json:"forceInstall,omitempty"`
	FirmwareSetID        string      `json:"firmwareSetID,omitempty"`
	FirmwareList         []*Firmware `json:"firmwareList,omitempty"`
}

func TestIntegration_ConditionsGet(t *testing.T) {
	tester := newTester(t)

	serverID := uuid.New()

	testcases := []struct {
		name                string
		conditionKind       ptypes.ConditionKind
		mockStore           func(r *store.MockRepository)
		expectResponse      func() *v1types.ServerResponse
		expectErrorContains string
	}{
		{
			"valid response",
			ptypes.FirmwareInstallOutofband,
			// mock repository
			func(r *store.MockRepository) {
				parameters, err := json.Marshal(&FirmwareInstallOutofbandParameters{
					InventoryAfterUpdate: true,
					ForceInstall:         true,
					FirmwareSetID:        "fake",
				})

				if err != nil {
					t.Error(err)
				}

				// lookup existing condition
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(ptypes.FirmwareInstallOutofband),
					).
					Return(
						&ptypes.Condition{
							Kind:       ptypes.FirmwareInstallOutofband,
							State:      ptypes.Pending,
							Status:     []byte(`{"hello":"world"}`),
							Parameters: parameters,
						},
						nil).
					Times(1)
			},
			func() *v1types.ServerResponse {
				parameters, err := json.Marshal(&FirmwareInstallOutofbandParameters{
					InventoryAfterUpdate: true,
					ForceInstall:         true,
					FirmwareSetID:        "fake",
				})

				if err != nil {
					t.Error(err)
				}

				return &v1types.ServerResponse{
					StatusCode: 200,
					Records: &v1types.ConditionsResponse{
						ServerID: serverID,
						Conditions: []*ptypes.Condition{
							{
								Kind:       ptypes.FirmwareInstallOutofband,
								State:      ptypes.Pending,
								Status:     []byte(`{"hello":"world"}`),
								Parameters: parameters,
							},
						},
					},
				}
			},
			"",
		},
		{
			"404 response",
			ptypes.FirmwareInstallOutofband,
			// mock repository
			func(r *store.MockRepository) {
				// lookup existing condition
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(ptypes.FirmwareInstallOutofband),
					).
					Return(
						nil,
						nil).
					Times(1)
			},
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					Message:    "conditionKind not found on server",
					StatusCode: 404,
				}
			},
			"",
		},
		{
			"400 response",
			ptypes.ConditionKind("invalid"),
			nil,
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					Message:    "unsupported condition kind: invalid",
					StatusCode: 400,
				}
			},
			"",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mockStore != nil {
				tc.mockStore(tester.repository)
			}

			got, err := tester.client.ServerConditionGet(context.TODO(), serverID, tc.conditionKind)
			if err != nil {
				t.Error(err)
			}

			if err != nil {
				assert.Contains(t, err.Error(), tc.expectErrorContains)
			}

			if tc.expectErrorContains != "" && err == nil {
				t.Error("expected error, got nil")
			}

			assert.Equal(
				t,
				tc.expectResponse(),
				got,
			)
		})
	}
}

func TestIntegration_ConditionsList(t *testing.T) {
	tester := newTester(t)

	serverID := uuid.New()

	testcases := []struct {
		name                string
		conditionState      ptypes.ConditionState
		mockStore           func(r *store.MockRepository)
		expectResponse      func() *v1types.ServerResponse
		expectErrorContains string
	}{
		{
			"valid response",
			ptypes.Pending,
			// mock repository
			func(r *store.MockRepository) {
				parameters, err := json.Marshal(&FirmwareInstallOutofbandParameters{
					InventoryAfterUpdate: true,
					ForceInstall:         true,
					FirmwareSetID:        "fake",
				})

				if err != nil {
					t.Error(err)
				}

				// lookup existing condition
				r.EXPECT().
					List(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(ptypes.Pending),
					).
					Return(
						[]*ptypes.Condition{
							{
								Kind:       ptypes.FirmwareInstallOutofband,
								State:      ptypes.Pending,
								Status:     []byte(`{"hello":"world"}`),
								Parameters: parameters,
							},
							{
								Kind:       ptypes.InventoryOutofband,
								State:      ptypes.Pending,
								Status:     []byte(`{"hello":"world"}`),
								Parameters: nil,
							},
						},
						nil).
					Times(1)
			},
			func() *v1types.ServerResponse {
				parameters, err := json.Marshal(&FirmwareInstallOutofbandParameters{
					InventoryAfterUpdate: true,
					ForceInstall:         true,
					FirmwareSetID:        "fake",
				})

				if err != nil {
					t.Error(err)
				}

				return &v1types.ServerResponse{
					StatusCode: 200,
					Records: &v1types.ConditionsResponse{
						ServerID: serverID,
						Conditions: []*ptypes.Condition{
							{
								Kind:       ptypes.FirmwareInstallOutofband,
								State:      ptypes.Pending,
								Status:     []byte(`{"hello":"world"}`),
								Parameters: parameters,
							},
							{
								Kind:       ptypes.InventoryOutofband,
								State:      ptypes.Pending,
								Status:     []byte(`{"hello":"world"}`),
								Parameters: nil,
							},
						},
					},
				}
			},
			"",
		},
		{
			"404 response",
			ptypes.Active,
			// mock repository
			func(r *store.MockRepository) {
				// lookup existing condition
				r.EXPECT().
					List(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(ptypes.Active),
					).
					Return(
						nil,
						nil).
					Times(1)
			},
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					Message:    "no conditions in given state found on server",
					StatusCode: 404,
				}
			},
			"",
		},
		{
			"400 response",
			ptypes.ConditionState("invalid"),
			nil,
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					Message:    "unsupported condition state: invalid",
					StatusCode: 400,
				}
			},
			"",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mockStore != nil {
				tc.mockStore(tester.repository)
			}

			got, err := tester.client.ServerConditionList(context.TODO(), serverID, tc.conditionState)
			if err != nil {
				t.Error(err)
			}

			if err != nil {
				assert.Contains(t, err.Error(), tc.expectErrorContains)
			}

			if tc.expectErrorContains != "" && err == nil {
				t.Error("expected error, got nil")
			}

			assert.Equal(
				t,
				tc.expectResponse(),
				got,
			)
		})
	}
}

func TestIntegration_ConditionsCreate(t *testing.T) {
	tester := newTester(t)

	serverID := uuid.New()

	testcases := []struct {
		name                string
		conditionKind       ptypes.ConditionKind
		payload             v1types.ConditionCreate
		mockStore           func(r *store.MockRepository)
		expectResponse      func() *v1types.ServerResponse
		expectErrorContains string
	}{
		{
			"valid payload sent",
			ptypes.FirmwareInstallOutofband,
			v1types.ConditionCreate{Parameters: []byte(`{"hello":"world"}`)},
			// mock repository
			func(r *store.MockRepository) {
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(ptypes.FirmwareInstallOutofband),
					).
					Return(
						nil,
						nil).
					Times(1)

				r.EXPECT().
					List(
						gomock.Any(),
						gomock.Any(),
						gomock.Any(),
					).
					Return(nil, nil).
					Times(2)

				// expect valid payload
				r.EXPECT().
					Create(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(&ptypes.Condition{
							Version:    ptypes.ConditionStructVersion,
							Kind:       ptypes.FirmwareInstallOutofband,
							Parameters: []byte(`{"hello":"world"}`),
							State:      ptypes.Pending,
						}),
					).
					Return(nil).
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
			ptypes.FirmwareInstallOutofband,
			v1types.ConditionCreate{Parameters: []byte(`{"hello":"world"}`)},
			// mock repository
			func(r *store.MockRepository) {
				// condition exists
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(ptypes.FirmwareInstallOutofband),
					).
					Return(
						&ptypes.Condition{
							Kind:  ptypes.FirmwareInstallOutofband,
							State: ptypes.Active,
						},
						nil).
					Times(1)
			},
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					Message:    "condition present in an incomplete state: active",
					StatusCode: 400,
				}
			},
			"",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mockStore != nil {
				tc.mockStore(tester.repository)
			}

			got, err := tester.client.ServerConditionCreate(context.TODO(), serverID, tc.conditionKind, tc.payload)
			if err != nil {
				t.Error(err)
			}

			if err != nil {
				assert.Contains(t, err.Error(), tc.expectErrorContains)
			}

			if tc.expectErrorContains != "" && err == nil {
				t.Error("expected error, got nil")
			}

			assert.Equal(
				t,
				tc.expectResponse(),
				got,
			)
		})
	}
}

func TestIntegration_ConditionsUpdate(t *testing.T) {
	tester := newTester(t)

	serverID := uuid.New()

	testcases := []struct {
		name                string
		conditionKind       ptypes.ConditionKind
		payload             v1types.ConditionUpdate
		mockStore           func(r *store.MockRepository)
		expectResponse      func() *v1types.ServerResponse
		expectErrorContains string
	}{
		{
			"valid payload sent",
			ptypes.FirmwareInstallOutofband,
			v1types.ConditionUpdate{
				State:           ptypes.Active,
				Status:          []byte(`{"hello":"world"}`),
				ResourceVersion: 1,
			},
			// mock repository
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
						State:           ptypes.Pending,
						Status:          []byte(`{"hello":"world"}`),
						ResourceVersion: 1,
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
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					StatusCode: 200,
					Message:    "condition updated",
				}
			},
			"",
		},
		{
			"404 response",
			ptypes.FirmwareInstallOutofband,
			v1types.ConditionUpdate{
				State:           ptypes.Active,
				Status:          []byte(`{"hello":"world"}`),
				ResourceVersion: 1,
			},
			// mock repository
			// mock repository
			func(r *store.MockRepository) {
				// lookup for existing condition
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(ptypes.FirmwareInstallOutofband),
					).
					Return(nil, nil).
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
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					StatusCode: 404,
					Message:    "no existing condition found for update",
				}
			},
			"",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mockStore != nil {
				tc.mockStore(tester.repository)
			}

			got, err := tester.client.ServerConditionUpdate(context.TODO(), serverID, tc.conditionKind, tc.payload)
			if err != nil {
				t.Error(err)
			}

			if err != nil {
				assert.Contains(t, err.Error(), tc.expectErrorContains)
			}

			if tc.expectErrorContains != "" && err == nil {
				t.Error("expected error, got nil")
			}

			assert.Equal(
				t,
				tc.expectResponse(),
				got,
			)
		})
	}
}

func TestIntegration_ConditionsDelete(t *testing.T) {
	tester := newTester(t)

	serverID := uuid.New()

	testcases := []struct {
		name                string
		conditionKind       ptypes.ConditionKind
		mockStore           func(r *store.MockRepository)
		expectResponse      func() *v1types.ServerResponse
		expectErrorContains string
	}{
		{
			"valid response",
			ptypes.FirmwareInstallOutofband,
			// mock repository
			func(r *store.MockRepository) {
				r.EXPECT().
					Delete(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(ptypes.FirmwareInstallOutofband),
					).
					Return(nil).
					Times(1)
			},
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					StatusCode: 200,
					Message:    "condition deleted",
				}
			},
			"",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mockStore != nil {
				tc.mockStore(tester.repository)
			}

			got, err := tester.client.ServerConditionDelete(context.TODO(), serverID, tc.conditionKind)
			if err != nil {
				t.Error(err)
			}

			if err != nil {
				assert.Contains(t, err.Error(), tc.expectErrorContains)
			}

			if tc.expectErrorContains != "" && err == nil {
				t.Error("expected error, got nil")
			}

			assert.Equal(
				t,
				tc.expectResponse(),
				got,
			)
		})
	}
}
