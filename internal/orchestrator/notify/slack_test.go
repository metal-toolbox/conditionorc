package notify

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/require"

	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	rctypes "github.com/metal-toolbox/rivets/condition"
)

// cribbed this test infra from the slack API open source code
var (
	serverAddr string
	once       sync.Once
)

func startServer() {
	server := httptest.NewServer(nil)
	serverAddr = server.Listener.Addr().String()
}

func TestSlackSend(t *testing.T) {
	// var timestampExpected bool
	http.DefaultServeMux = new(http.ServeMux)
	http.HandleFunc("/chat.postMessage", func(rw http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		r.Body.Close()
		rw.Header().Set("Content-Type", "application/json")
		response := []byte("{\"ok\": true}")
		rw.Write(response)
	})

	once.Do(startServer)

	notifier := &slackSender{
		api: slack.New("totally-valid-token", slack.OptionAPIURL("http://"+serverAddr+"/")),
		log: &logrus.Logger{},
		trk: make(map[uuid.UUID]slackNotification),
		ch:  "#bogus",
	}

	condID := uuid.New()
	update := &v1types.ConditionUpdateEvent{
		ConditionUpdate: v1types.ConditionUpdate{
			ConditionID: condID,
			ServerID:    uuid.New(),
			State:       rctypes.Pending,
			Status:      []byte(`{ "msg":"Hi Vince!" }`),
		},
		Kind: rctypes.FirmwareInstall,
	}

	err := notifier.Send(update)
	require.NoError(t, err)
	entry, ok := notifier.trk[condID]
	require.True(t, ok)
	require.Equal(t, string(rctypes.Pending), entry.ConditionState) // weak test >.>;

	update.State = rctypes.Failed // oh no! :(
	err = notifier.Send(update)
	require.NoError(t, err)
	entry, ok = notifier.trk[condID]
	require.False(t, ok)
}
