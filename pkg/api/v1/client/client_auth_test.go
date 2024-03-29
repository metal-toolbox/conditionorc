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
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/metal-toolbox/conditionorc/internal/server"
	"github.com/metal-toolbox/conditionorc/internal/store"
	storeTest "github.com/metal-toolbox/conditionorc/internal/store/test"
	"github.com/stretchr/testify/require"
	"go.hollow.sh/toolbox/ginjwt"
	"gopkg.in/square/go-jose.v2"
	"gopkg.in/square/go-jose.v2/jwt"

	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
	"github.com/sirupsen/logrus"
)

func newAuthTester(t *testing.T, authToken string) (*integrationTester, finalizer) {
	t.Helper()

	ctrl := gomock.NewController(t)

	repository := storeTest.NewMockRepository(ctrl)

	l := logrus.New()
	l.Level = logrus.Level(logrus.ErrorLevel)

	jwksURI := ginjwt.TestHelperJWKSProvider(ginjwt.TestPrivRSAKey1ID, ginjwt.TestPrivRSAKey2ID)

	serverOptions := []server.Option{
		server.WithLogger(l),
		server.WithListenAddress("localhost:9999"),
		server.WithStore(repository),
		server.WithConditionDefinitions(
			[]*rctypes.Definition{
				{Kind: rctypes.FirmwareInstall},
			},
		),
		server.WithAuthMiddlewareConfig(
			[]ginjwt.AuthConfig{
				ginjwt.AuthConfig{
					Enabled:  true,
					Issuer:   "conditionorc.oidc.issuer",
					Audience: "conditionorc.client",
					JWKSURI:  jwksURI,
				},
			}),
	}

	gin.SetMode(gin.ReleaseMode)

	srv := server.New(serverOptions...)

	// setup test server httptest recorder
	tester := &integrationTester{
		t:               t,
		handler:         srv.Handler,
		repository:      repository,
		assertAuthToken: true,
	}

	// setup test client
	clientOptions := []Option{
		WithHTTPClient(tester),
		WithAuthToken(authToken),
	}

	client, err := NewClient("http://localhost:9999", clientOptions...)
	if err != nil {
		t.Error(err)
	}

	tester.client = client

	return tester, ctrl.Finish
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

	scopes := map[string]interface{}{
		"scope": strings.Join(
			[]string{"write", "create", "create:condition", "read", "read:condition"}, " "),
	}

	signer := ginjwt.TestHelperMustMakeSigner(jose.RS256, ginjwt.TestPrivRSAKey1ID, ginjwt.TestPrivRSAKey1)

	token, err := jwt.Signed(signer).Claims(claims).Claims(scopes).CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}

	// XXX: uncomment and use jwt.io to examine the contents of the token -- t.Logf("JWT: %s\n", token)

	return token
}

func TestIntegration_AuthToken(t *testing.T) {
	serverID := uuid.New()

	testcases := []struct {
		name                string
		token               string
		mockStore           func(r *storeTest.MockRepository)
		expectResponse      func() *v1types.ServerResponse
		expectErrorContains string
	}{
		{
			"invalid auth token returns 401",
			"asdf",
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
			testAuthToken(t),
			// mock repository
			func(r *storeTest.MockRepository) {
				// lookup existing condition
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
								&rctypes.Condition{
									Kind:   rctypes.Inventory,
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
						State:    "pending",
						Conditions: []*rctypes.Condition{
							{
								Kind:   rctypes.FirmwareInstall,
								State:  rctypes.Pending,
								Status: []byte(`{"hello":"world"}`),
							},
							{
								Kind:   rctypes.Inventory,
								State:  rctypes.Pending,
								Status: []byte(`{"hello":"world"}`),
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
			tester, finish := newAuthTester(t, tc.token)
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
