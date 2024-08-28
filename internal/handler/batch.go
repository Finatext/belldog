package handler

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aws/aws-lambda-go/events"
	"github.com/cockroachdb/errors"

	"github.com/Finatext/belldog/internal/appconfig"
	"github.com/Finatext/belldog/internal/slack"
	"github.com/Finatext/belldog/internal/storage"
)

type BatchHandler struct {
	cfg         appconfig.Config
	slackClient slackClient
	ddb         storageDDB
}

func NewBatchHandler(cfg appconfig.Config, slackClient slackClient, ddb storageDDB) BatchHandler {
	return BatchHandler{
		cfg:         cfg,
		slackClient: slackClient,
		ddb:         ddb,
	}
}

// Bypass domain layer because we don't have enough logic and tests yet for batch app code.
func (h *BatchHandler) HandleCloudWatchEvent(ctx context.Context, _ events.CloudWatchEvent) error {
	if err := h.handleWithErrorLogging(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to handle", slog.String("error", fmt.Sprintf("%+v", err)))
		return err
	}
	return nil
}

func (h *BatchHandler) handleWithErrorLogging(ctx context.Context) error {
	olds, err := h.ddb.ScanAll(ctx)
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "target record size", slog.Int("size", len(olds)))

	channels, err := h.slackClient.GetAllChannels(ctx)
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "target channel size", slog.Int("size", len(channels)))

	// Check channel is_archived.
	var archived []archiveEvent
	recs := make([]storage.Record, 0, len(olds))
	for _, rec := range olds {
		isArchived := false
		for _, channel := range channels {
			if rec.ChannelID == channel.ID {
				slog.DebugContext(ctx, "channel", slog.String("channel_id", rec.ChannelID), slog.String("channel_name", rec.ChannelName), slog.String("slack_channel_name", channel.Name))

				if channel.IsArchived {
					isArchived = true
					event := archiveEvent{record: rec, SlackChannelName: channel.Name}
					archived = append(archived, event) //nolint:staticcheck // false positive of append
				}
				break
			}
		}
		if !isArchived {
			recs = append(recs, rec)
		}
	}

	slog.InfoContext(ctx, "processing archived channels", slog.Int("size", len(archived)))
	for _, event := range archived {
		slog.InfoContext(ctx, "Channel is archived, deleting", slog.String("channel_id", event.record.ChannelID), slog.String("record_channel_name", event.record.ChannelName), slog.String("slack_channel_name", event.SlackChannelName))
		msg := fmt.Sprintf("Channel is archived, deleting record: channel_id=%s, record_channel_name=%s, slack_channel_name=%s\n", event.record.ChannelID, event.record.ChannelName, event.SlackChannelName)
		if err := h.notifyOps(ctx, msg); err != nil {
			return err
		}
		if err := h.ddb.Delete(ctx, event.record); err != nil {
			return err
		}
	}

	migrations := make(map[string]storage.Record)
	var renames []renameEvent

	for _, rec := range recs {
		name := rec.ChannelName
		// Check token is in migration.
		for _, other := range recs {
			if rec.ChannelID == other.ChannelID && name == other.ChannelName && rec.Token != other.Token {
				migrations[name] = rec
			}
		}
		// Check saved channel has been renamed.
		for _, channel := range channels {
			if rec.ChannelID == channel.ID && name != channel.Name {
				renames = append(renames, renameEvent{channelID: rec.ChannelID, oldName: name, newName: channel.Name, savedToken: rec.Token})
			}
		}
	}

	slog.InfoContext(ctx, "processing migrations", slog.Int("size", len(migrations)))
	for _, rec := range migrations {
		slog.InfoContext(ctx, "Token is in migration", slog.String("channel_name", rec.ChannelName), slog.String("channel_id", rec.ChannelID))
		msgOps := fmt.Sprintf("Token is in migration: channel_name=%s, channel_id=%s\n", rec.ChannelName, rec.ChannelID)
		msg := fmt.Sprintf("Token is in migration. Once all old webhook URLs are replaced, revoke old token: channel_name=%s, channel_id=%s\n", rec.ChannelName, rec.ChannelID)
		if err := h.notify(ctx, rec.ChannelID, rec.ChannelName, msg, msgOps); err != nil {
			return err
		}
	}

	slog.InfoContext(ctx, "processing renames", slog.Int("size", len(renames)))
	for _, evt := range renames {
		slog.InfoContext(ctx, "Channel name and channel id pair updated",
			slog.String("channel_id", evt.channelID),
			slog.String("old_channel_name", evt.oldName),
			slog.String("renamed_channel_name", evt.newName),
			slog.String("saved_token", evt.savedToken),
		)
		msgOps := fmt.Sprintf("Channel name and channel id pair updated: channel_id=%s, old_channel_name=%s, renamed_channel_name=%s\n", evt.channelID, evt.oldName, evt.newName)
		format := `
Detect channel renaming for this channel: channel_id=%s, old_channel_name=%s, renamed_channel_name=%s

1. Generate new token in this channel.
2. Replace old webhook URLs with new URLs.
3. When all old URLs are replaced, revoke old token with the "revoke renamed slash command" with channel_name=%s and token=%s
		`
		msg := fmt.Sprintf(format, evt.channelID, evt.oldName, evt.newName, evt.oldName, evt.savedToken)
		if err := h.notify(ctx, evt.channelID, evt.newName, msg, msgOps); err != nil {
			return err
		}
	}

	slog.InfoContext(ctx, "batch process completed")
	return nil
}

func (h *BatchHandler) notify(ctx context.Context, channelID string, channelName string, msg string, msgOps string) error {
	payload := map[string]interface{}{"text": msg}
	{
		result, err := h.slackClient.PostMessage(ctx, channelID, channelName, payload)
		if err != nil {
			return err
		}
		if e := handlePostMessageFailure(result); e != nil {
			return e
		}
	}
	return h.notifyOps(ctx, msgOps)
}

func (h *BatchHandler) notifyOps(ctx context.Context, msg string) error {
	result, err := h.slackClient.PostMessage(ctx, h.cfg.OpsNotificationChannelName, h.cfg.OpsNotificationChannelName, map[string]interface{}{"text": msg})
	if err != nil {
		return err
	}
	if e := handlePostMessageFailure(result); e != nil {
		return e
	}
	return nil
}

type renameEvent struct {
	channelID  string
	oldName    string
	newName    string
	savedToken string
}

type archiveEvent struct {
	record           storage.Record
	SlackChannelName string
}

func handlePostMessageFailure(result slack.PostMessageResult) error {
	switch result.Type {
	case slack.PostMessageResultOK:
		return nil
	case slack.PostMessageResultServerTimeoutFailure:
		return errors.New("slack server timeout")
	case slack.PostMessageResultServerFailure:
		return errors.Newf("slack server error: code=%d, body=%s", result.StatusCode, result.Body)
	case slack.PostMessageResultAPIFailure:
		return errors.Newf("slack API error: channelName=%s, channelID=%s, reason=%s", result.ChannelName, result.ChannelID, result.Reason)
	default:
		return errors.Newf("unknown PostMessageResult type: %d", result.Type)
	}
}
