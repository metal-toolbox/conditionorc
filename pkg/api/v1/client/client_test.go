//go:build testtools
// +build testtools

// Note:
// The testtools build flag is defined on this file since its required for ginjwt helper methods.
// Make sure to include `-tags testtools` in the build flags to ensure the tests in this file are run.
//
// for example:
// /usr/local/bin/go test -timeout 10s -run ^TestIntegration_ConditionsGet$ \
//   -tags testtools github.com/metal-toolbox/conditionorc/pkg/api/v1/client -v

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/server"
	"github.com/metal-toolbox/conditionorc/internal/store"
	"github.com/stretchr/testify/assert"
	"go.hollow.sh/toolbox/ginjwt"
	"gopkg.in/square/go-jose.v2"
	"gopkg.in/square/go-jose.v2/jwt"

	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
	"github.com/sirupsen/logrus"
)

type integrationTester struct {
	t               *testing.T
	assertAuthToken bool
	handler         http.Handler
	client          *Client
	repository      *store.MockRepository
}

// Do implements the HTTPRequestDoer interface to swap the response writer
func (i *integrationTester) Do(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}

	w := httptest.NewRecorder()
	i.handler.ServeHTTP(w, req)

	if i.assertAuthToken {
		assert.NotEmpty(i.t, req.Header.Get("Authorization"))
	} else {
		assert.Empty(i.t, req.Header.Get("Authorization"))
	}

	return w.Result(), nil
}

func newTester(t *testing.T, enableAuth bool, authToken string) *integrationTester {
	t.Helper()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repository := store.NewMockRepository(ctrl)

	l := logrus.New()
	l.Level = logrus.Level(logrus.ErrorLevel)
	serverOptions := []server.Option{
		server.WithLogger(l),
		server.WithListenAddress("localhost:9999"),
		server.WithStore(repository),
		server.WithConditionDefinitions(
			[]*rctypes.Definition{
				{Kind: rctypes.FirmwareInstall},
			},
		),
	}

	// setup JWT auth middleware on router when a non-empty auth token was provided
	if enableAuth {
		jwksURI := ginjwt.TestHelperJWKSProvider(ginjwt.TestPrivRSAKey1ID, ginjwt.TestPrivRSAKey2ID)

		serverOptions = append(serverOptions,
			server.WithAuthMiddlewareConfig(&ginjwt.AuthConfig{
				Enabled:  true,
				Issuer:   "conditionorc.oidc.issuer",
				Audience: "conditionorc.client",
				JWKSURI:  jwksURI,
			},
			),
		)

	}

	gin.SetMode(gin.ReleaseMode)

	srv := server.New(serverOptions...)

	// setup test server httptest recorder
	tester := &integrationTester{
		t:          t,
		handler:    srv.Handler,
		repository: repository,
	}

	// setup test client
	clientOptions := []Option{WithHTTPClient(tester)}

	if enableAuth {
		// enable auth token assert on the server
		tester.assertAuthToken = true

		// client to include Authorization header
		clientOptions = append(clientOptions, WithAuthToken(authToken))
	}

	client, err := NewClient("http://localhost:9999", clientOptions...)
	if err != nil {
		t.Error(err)
	}

	tester.client = client

	return tester
}

// Firmware holds parameters for a firmware install and is part of FirmwareInstallParameters.
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
type FirmwareInstallParameters struct {
	InventoryAfterUpdate bool        `json:"inventoryAfterUpdate,omitempty"`
	ForceInstall         bool        `json:"forceInstall,omitempty"`
	FirmwareSetID        string      `json:"firmwareSetID,omitempty"`
	FirmwareList         []*Firmware `json:"firmwareList,omitempty"`
}

func testAuthToken(t *testing.T) string {
	t.Helper()

	claims := jwt.Claims{
		Subject:   "test-user",
		Issuer:    "conditionorc.oidc.issuer",
		NotBefore: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
		Expiry:    jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		Audience:  jwt.Audience{"conditionorc.client"},
	}
	signer := ginjwt.TestHelperMustMakeSigner(jose.RS256, ginjwt.TestPrivRSAKey1ID, ginjwt.TestPrivRSAKey1)

	token, err := jwt.Signed(signer).Claims(claims).CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}

	return token
}

func TestIntegration_AuthToken(t *testing.T) {
	serverID := uuid.New()

	testcases := []struct {
		name                string
		conditionKind       rctypes.Kind
		tester              *integrationTester
		mockStore           func(r *store.MockRepository)
		expectResponse      func() *v1types.ServerResponse
		expectErrorContains string
	}{
		{
			"invalid auth token returns 401",
			rctypes.FirmwareInstall,
			newTester(t, true, "asdf"),
			// mock repository
			nil,
			func() *v1types.ServerResponse {
				return &v1types.ServerResponse{
					StatusCode: 401,
					Message:    "unable to parse auth token",
				}
			},
			"",
		},
		{
			"valid auth token works",
			rctypes.FirmwareInstall,
			newTester(t, true, testAuthToken(t)),
			// mock repository
			func(r *store.MockRepository) {
				parameters, err := json.Marshal(&FirmwareInstallParameters{
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
						gomock.Eq(rctypes.FirmwareInstall),
					).
					Return(
						&rctypes.Condition{
							Kind:       rctypes.FirmwareInstall,
							State:      rctypes.Pending,
							Status:     []byte(`{"hello":"world"}`),
							Parameters: parameters,
						},
						nil).
					Times(1)
			},
			func() *v1types.ServerResponse {
				parameters, err := json.Marshal(&FirmwareInstallParameters{
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
						Conditions: []*rctypes.Condition{
							{
								Kind:       rctypes.FirmwareInstall,
								State:      rctypes.Pending,
								Status:     []byte(`{"hello":"world"}`),
								Parameters: parameters,
							},
						},
					},
				}
			},
			"",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mockStore != nil {
				tc.mockStore(tc.tester.repository)
			}

			got, err := tc.tester.client.ServerConditionGet(context.TODO(), serverID, tc.conditionKind)
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

func TestIntegration_ConditionsGet(t *testing.T) {
	tester := newTester(t, false, "")

	serverID := uuid.New()

	testcases := []struct {
		name                string
		conditionKind       rctypes.Kind
		mockStore           func(r *store.MockRepository)
		expectResponse      func() *v1types.ServerResponse
		expectErrorContains string
	}{
		{
			"valid response",
			rctypes.FirmwareInstall,
			// mock repository
			func(r *store.MockRepository) {
				parameters, err := json.Marshal(&FirmwareInstallParameters{
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
						gomock.Eq(rctypes.FirmwareInstall),
					).
					Return(
						&rctypes.Condition{
							Kind:       rctypes.FirmwareInstall,
							State:      rctypes.Pending,
							Status:     []byte(`{"hello":"world"}`),
							Parameters: parameters,
						},
						nil).
					Times(1)
			},
			func() *v1types.ServerResponse {
				parameters, err := json.Marshal(&FirmwareInstallParameters{
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
						Conditions: []*rctypes.Condition{
							{
								Kind:       rctypes.FirmwareInstall,
								State:      rctypes.Pending,
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
			rctypes.FirmwareInstall,
			// mock repository
			func(r *store.MockRepository) {
				// lookup existing condition
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(rctypes.FirmwareInstall),
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
			rctypes.Kind("invalid"),
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
	tester := newTester(t, false, "")

	serverID := uuid.New()

	testcases := []struct {
		name                string
		conditionState      rctypes.State
		mockStore           func(r *store.MockRepository)
		expectResponse      func() *v1types.ServerResponse
		expectErrorContains string
	}{
		{
			"valid response",
			rctypes.Pending,
			// mock repository
			func(r *store.MockRepository) {
				parameters, err := json.Marshal(&FirmwareInstallParameters{
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
						gomock.Eq(rctypes.Pending),
					).
					Return(
						[]*rctypes.Condition{
							{
								Kind:       rctypes.FirmwareInstall,
								State:      rctypes.Pending,
								Status:     []byte(`{"hello":"world"}`),
								Parameters: parameters,
							},
							{
								Kind:       rctypes.Inventory,
								State:      rctypes.Pending,
								Status:     []byte(`{"hello":"world"}`),
								Parameters: nil,
							},
						},
						nil).
					Times(1)
			},
			func() *v1types.ServerResponse {
				parameters, err := json.Marshal(&FirmwareInstallParameters{
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
						Conditions: []*rctypes.Condition{
							{
								Kind:       rctypes.FirmwareInstall,
								State:      rctypes.Pending,
								Status:     []byte(`{"hello":"world"}`),
								Parameters: parameters,
							},
							{
								Kind:       rctypes.Inventory,
								State:      rctypes.Pending,
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
			rctypes.Active,
			// mock repository
			func(r *store.MockRepository) {
				// lookup existing condition
				r.EXPECT().
					List(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(rctypes.Active),
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
			rctypes.State("invalid"),
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
	tester := newTester(t, false, "")

	serverID := uuid.New()

	testcases := []struct {
		name                string
		conditionKind       rctypes.Kind
		payload             v1types.ConditionCreate
		mockStore           func(r *store.MockRepository)
		expectResponse      func() *v1types.ServerResponse
		expectErrorContains string
	}{
		{
			"valid payload sent",
			rctypes.FirmwareInstall,
			v1types.ConditionCreate{Parameters: []byte(`{"hello":"world"}`)},
			// mock repository
			func(r *store.MockRepository) {
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(rctypes.FirmwareInstall),
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
						gomock.Any(),
					).
					DoAndReturn(func(_ context.Context, _ uuid.UUID, c *rctypes.Condition) error {
						assert.Equal(t, rctypes.ConditionStructVersion, c.Version, "condition version mismatch")
						assert.Equal(t, rctypes.FirmwareInstall, c.Kind, "condition kind mismatch")
						assert.Equal(t, json.RawMessage(`{"hello":"world"}`), c.Parameters, "condition parameters mismatch")
						assert.Equal(t, rctypes.Pending, c.State, "condition state mismatch")
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
			rctypes.FirmwareInstall,
			v1types.ConditionCreate{Parameters: []byte(`{"hello":"world"}`)},
			// mock repository
			func(r *store.MockRepository) {
				// condition exists
				r.EXPECT().
					Get(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(rctypes.FirmwareInstall),
					).
					Return(
						&rctypes.Condition{
							Kind:  rctypes.FirmwareInstall,
							State: rctypes.Active,
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
	tester := newTester(t, false, "")

	serverID := uuid.New()

	testcases := []struct {
		name                string
		conditionKind       rctypes.Kind
		payload             v1types.ConditionUpdate
		mockStore           func(r *store.MockRepository)
		expectResponse      func() *v1types.ServerResponse
		expectErrorContains string
	}{
		{
			"valid payload sent",
			rctypes.FirmwareInstall,
			v1types.ConditionUpdate{
				State:           rctypes.Active,
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
						gomock.Eq(rctypes.FirmwareInstall),
					).
					Return(&rctypes.Condition{ // condition present
						Kind:            rctypes.FirmwareInstall,
						State:           rctypes.Pending,
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
			rctypes.FirmwareInstall,
			v1types.ConditionUpdate{
				State:           rctypes.Active,
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
						gomock.Eq(rctypes.FirmwareInstall),
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
	tester := newTester(t, false, "")

	serverID := uuid.New()

	testcases := []struct {
		name                string
		conditionKind       rctypes.Kind
		mockStore           func(r *store.MockRepository)
		expectResponse      func() *v1types.ServerResponse
		expectErrorContains string
	}{
		{
			"valid response",
			rctypes.FirmwareInstall,
			// mock repository
			func(r *store.MockRepository) {
				r.EXPECT().
					Delete(
						gomock.Any(),
						gomock.Eq(serverID),
						gomock.Eq(rctypes.FirmwareInstall),
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
