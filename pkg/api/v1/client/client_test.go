//go:build testtools

package client

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/stretchr/testify/assert"
	"go.hollow.sh/toolbox/ginjwt"
	"gopkg.in/square/go-jose.v2"
	"gopkg.in/square/go-jose.v2/jwt"
)

var (
	adminScopes = []string{"read", "write", "read:server:credentials", "write:server:credentials"}
)

func validToken(scopes []string) string {
	claims := jwt.Claims{
		Subject:   "test-user",
		Issuer:    "hollow.test.issuer",
		NotBefore: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
		Expiry:    jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		Audience:  jwt.Audience{"hollow.test", "another.test.service"},
	}
	signer := ginjwt.TestHelperMustMakeSigner(jose.RS256, ginjwt.TestPrivRSAKey1ID, ginjwt.TestPrivRSAKey1)

	return ginjwt.TestHelperGetToken(signer, claims, "userPerms", scopes)
}

func TestNewClientWithToken(t *testing.T) {
	var testCases = []struct {
		testName    string
		authToken   string
		url         string
		expectError bool
		errorMsg    string
	}{
		{
			"no authToken",
			"",
			"https://dcim.hollow.sh",
			true,
			"failed to initialize: no auth token provided",
		},
		{
			"no uri",
			"SuperSecret",
			"",
			true,
			"failed to initialize: no hollow api url provided",
		},
		{
			"happy path",
			"SuperSecret",
			"https://dcim.hollow.sh",
			false,
			"",
		},
	}

	for _, tt := range testCases {
		c, err := NewClientWithToken(tt.authToken, tt.url, nil)

		if tt.expectError {
			assert.Error(t, err, tt.testName)
			assert.Contains(t, err.Error(), tt.errorMsg)
		} else {
			assert.NoError(t, err, tt.testName)
			assert.NotNil(t, c, tt.testName)
		}
	}
}

func mockClientTests(t *testing.T, f func(ctx context.Context, respCode int, expectError bool) error) {
	ctx := context.Background()
	timeCtx, cancel := context.WithTimeout(ctx, 1*time.Nanosecond)

	defer cancel()

	var testCases = []struct {
		testName     string
		ctx          context.Context
		responseCode int
		expectError  bool
		errorMsg     string
	}{
		{
			"happy path",
			ctx,
			http.StatusOK,
			false,
			"",
		},
		{
			"server unauthorized",
			ctx,
			http.StatusUnauthorized,
			true,
			"server error - response code: 401, message:",
		},
		{
			"fake timeout",
			timeCtx,
			http.StatusOK,
			true,
			"context deadline exceeded",
		},
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			err := f(tt.ctx, tt.responseCode, tt.expectError)
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
