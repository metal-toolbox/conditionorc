package apiv1

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang/mock/gomock"
	"github.com/metal-toolbox/conditionorc/internal/store"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	ginlogrus "github.com/toorop/gin-logrus"
)

func mockserver(t *testing.T, logger *logrus.Logger, repository store.Repository) (*gin.Engine, error) {
	t.Helper()

	g := gin.New()
	g.Use(ginlogrus.Logger(logger), gin.Recovery())

	options := []Option{
		WithLogger(logger),
		WithStore(repository),
		WithConditionDefs([]ptypes.ConditionDefinition{
			{
				Name:      "firmwareUpdate",
				Exclusive: true,
			},
			{
				Name:      "inventoryOutofband",
				Exclusive: false,
			},
		}),
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

func TestServerConditionList(t *testing.T) {
	testcases := []struct {
		name          string
		condition     ptypes.ConditionKind
		serverID      string
		mockStore     func(r *store.MockRepository)
		request       func(t *testing.T) *http.Request
		checkResponse func(t *testing.T, r *httptest.ResponseRecorder)
	}{
		{
			"invalid server id",
			"",
			"123",
			nil,
			func(t *testing.T) *http.Request {
				url := fmt.Sprintf("/api/v1/servers/%s/conditions/%s", "123", "asdasd")
				request, err := http.NewRequest(http.MethodGet, url, nil)
				if err != nil {
					t.Fatal(err)
				}

				return request
			},
			func(t *testing.T, r *httptest.ResponseRecorder) {
				assert.Equal(t, http.StatusBadRequest, r.Code)
			},
		},
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	repository := store.NewMockRepository(ctrl)

	server, err := mockserver(t, logrus.New(), repository)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mockStore != nil {

			}

			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, tc.request(t))

			tc.checkResponse(t, recorder)
		})
	}

	// var firmwareUpdate ptypes.ConditionKind = "firmwareUpdate"
	// serverID := uuid.New()
	//
	//	econdition := &ptypes.Condition{
	//		Kind:       firmwareUpdate,
	//		Parameters: []byte(`{"foo": "bar"}`),
	//	}
	//
	// ctrl := gomock.NewController(t)
	// defer ctrl.Finish()
	//
	// repository := store.NewMockRepository(ctrl)
	// repository.EXPECT().
	//
	//	Get(gomock.Any(), gomock.Eq(serverID), gomock.Eq(firmwareUpdate)).
	//	Times(1).
	//	Return(econdition, nil)
	//
	// server, err := mockserver(t, logrus.New(), repository)
	//
	//	if err != nil {
	//		t.Fatal(err)
	//	}
	//
	// recorder := httptest.NewRecorder()
	//
	// url := fmt.Sprintf("/api/v1/servers/%s/conditions/%s", serverID.String(), firmwareUpdate)
	// request, err := http.NewRequest(http.MethodGet, url, nil)
	// assert.Nil(t, err)
	//
	// server.ServeHTTP(recorder, request)
	//
	// b, err := io.ReadAll(recorder.Body)
	//
	//	if err != nil {
	//		t.Fatal(err)
	//	}
	//
	// fmt.Println(string(b))
	// assert.Equal(t, http.StatusOK, recorder.Code)
}
