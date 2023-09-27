package notify

// Slack notifications have a "best effort" SLA. Slack is not and should not
// be the authoritative source for task state. If we fail to post a message
// to Slack, it's not a crisis, just unfortunate.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	v1types "github.com/metal-toolbox/conditionorc/pkg/api/v1/types"
	condition "github.com/metal-toolbox/rivets/condition"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

var postTimeout = 800 * time.Millisecond

// this is the value associated with notificiations for a condition-execution
// the key is the tuple of facility and condition ID (just like the status KV)
// MsgTimestamp is returned by the Slack API after a successful
// send and is used for maintaining a thread of notifications.
type slackNotification struct {
	ConditionState  string
	ConditionStatus string
	MsgTimestamp    string
}

type slackSender struct {
	log *logrus.Logger
	api *slack.Client
	trk map[uuid.UUID]slackNotification
	ch  string
}

func (ss *slackSender) Send(upd *v1types.ConditionUpdateEvent) error {
	le := ss.log.WithFields(logrus.Fields{
		"channel":     ss.ch,
		"state":       upd.ConditionUpdate.State,
		"conditionID": upd.ConditionUpdate.ConditionID.String(),
	})

	le.Debug("sending slack notification")

	// cap the time we're willing to wait for Slack
	ctx, cancel := context.WithTimeout(context.Background(), postTimeout)
	defer cancel()

	entry := ss.trk[upd.ConditionID]

	if entry.ConditionState == string(upd.ConditionUpdate.State) &&
		entry.ConditionStatus == string(upd.ConditionUpdate.Status) {
		le.Info("skipping notification on duplicate state and status")
		return nil
	}

	msgOpts := ss.optionsFromUpdate(&upd.ConditionUpdate, string(upd.Kind))

	if entry.MsgTimestamp != "" {
		// this is not the first time we've sent a notification, add
		// the timestamp so we make a thread
		msgOpts = append(msgOpts, slack.MsgOptionTS(entry.MsgTimestamp))
	}

	_, ts, err := ss.api.PostMessageContext(ctx, ss.ch, msgOpts...)

	// special handling for the last update
	if condition.StateIsComplete(upd.State) {
		delete(ss.trk, upd.ConditionID)
		return err
	}

	entry.ConditionState = string(upd.State)
	entry.ConditionStatus = string(upd.Status)
	// we only need the timestamp for making threaded replies
	// from the initial message
	if entry.MsgTimestamp == "" {
		entry.MsgTimestamp = ts
	}
	ss.trk[upd.ConditionID] = entry

	return err
}

func (ss *slackSender) optionsFromUpdate(upd *v1types.ConditionUpdate, kind string) []slack.MsgOption {
	hdrStr := fmt.Sprintf("Condition: %s", upd.ConditionID.String())
	var emojiStr string
	switch upd.State {
	case condition.Succeeded:
		emojiStr = ":white_check_mark:"
	case condition.Failed:
		emojiStr = ":exclamation:"
	}
	stateStr := fmt.Sprintf("Type: _%s_\nState: *%s* %s", kind, string(upd.State), emojiStr)

	marshaledStatus, err := json.Marshal(upd.Status)
	if err != nil {
		ss.log.WithFields(logrus.Fields{
			"state":       upd.State,
			"conditionID": upd.ConditionID.String(),
		}).Warn("bad status payload")
		marshaledStatus = nil
	}

	statusString := fmt.Sprintf("Server: *%s*\nStatus: _%s_", upd.ServerID.String(),
		string(marshaledStatus))

	var blocks []slack.Block
	blocks = append(blocks,
		slack.NewHeaderBlock(
			slack.NewTextBlockObject(slack.PlainTextType, hdrStr, false, false)),
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, stateStr, false, false), nil, nil),
	)

	if marshaledStatus != nil {
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, statusString, false, false), nil, nil),
		)
	}

	return []slack.MsgOption{
		slack.MsgOptionBlocks(blocks...),
	}
}

func newSlackSender(l *logrus.Logger, cfg Configuration) *slackSender {
	return &slackSender{
		log: l,
		api: slack.New(cfg.Token),
		trk: make(map[uuid.UUID]slackNotification),
		ch:  cfg.Channel,
	}
}
