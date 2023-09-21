package notify

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	ptypes "github.com/metal-toolbox/conditionorc/pkg/types"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	"github.com/stretchr/testify/require"
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
		t.Logf("request form: %s\n", r.Form)
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
			State:       ptypes.Pending,
			Status:      []byte(`{ "msg":"Hi Vince!" }`),
		},
		Kind: ptypes.FirmwareInstall,
	}

	err := notifier.Send(update)
	require.NoError(t, err)
	entry, ok := notifier.trk[condID]
	require.True(t, ok)
	require.Equal(t, string(ptypes.Pending), entry.ConditionState) // weak test >.>;

	update.State = ptypes.Failed // oh no! :(
	err = notifier.Send(update)
	require.NoError(t, err)
	entry, ok = notifier.trk[condID]
	require.False(t, ok)
}
