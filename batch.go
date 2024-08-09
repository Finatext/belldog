package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aws/aws-lambda-go/events"

	"github.com/Finatext/belldog/slack"
	"github.com/Finatext/belldog/storage"
)

type renameEvent struct {
	channelID  string
	oldName    string
	newName    string
	savedToken string
}

// Bypass domain layer because we don't have enough logic and tests yet for batch app code.
func handleCloudWatchEvent(event events.CloudWatchEvent) error {
	ctx := context.Background()
	if err := handleWithErrorLogging(ctx, event); err != nil {
		slog.ErrorContext(ctx, "failed to handle", slog.String("error", fmt.Sprintf("%+v", err)))
		return err
	}
	return nil
}

func handleWithErrorLogging(ctx context.Context, _ events.CloudWatchEvent) error {
	st, err := storage.NewStorage(ctx, config.DdbTableName)
	if err != nil {
		return err
	}
	recs, err := st.ScanAll(ctx)
	if err != nil {
		return err
	}

	kit := slack.NewKit(config.SlackToken, slackRetryConfig)
	channels, err := kit.GetAllChannels(ctx)
	if err != nil {
		return err
	}

	migrations := make(map[string]storage.Record)
	var renames []renameEvent

	slog.InfoContext(ctx, "target record size", slog.Int("size", len(recs)))
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

	for _, rec := range migrations {
		slog.InfoContext(ctx, "Token is in migration", slog.String("channel_name", rec.ChannelName), slog.String("channel_id", rec.ChannelID))
		msgOps := fmt.Sprintf("Token is in migration: channel_name=%s, channel_id=%s\n", rec.ChannelName, rec.ChannelID)
		msg := fmt.Sprintf("Token is in migration. Once all old webhook URLs are replaced, revoke old token: channel_name=%s, channel_id=%s\n", rec.ChannelName, rec.ChannelID)
		if err := notify(ctx, kit, rec.ChannelID, rec.ChannelName, msg, msgOps); err != nil {
			return err
		}
	}
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
		if err := notify(ctx, kit, evt.channelID, evt.newName, msg, msgOps); err != nil {
			return err
		}
	}
	return nil
}

func notify(ctx context.Context, kit slack.Kit, channelID string, channelName string, msg string, msgOps string) error {
	payload := map[string]interface{}{"text": msg}
	{
		result, err := kit.PostMessage(ctx, channelID, channelName, payload)
		if err != nil {
			return err
		}
		if e := handlePostMessageFailure(result); e != nil {
			return e
		}
	}
	payloadOps := map[string]interface{}{"text": msgOps}
	// kit.PostMessage can accept channel name as channel id.
	result, err := kit.PostMessage(ctx, config.OpsNotificationChannelName, config.OpsNotificationChannelName, payloadOps)
	if err != nil {
		return err
	}
	if e := handlePostMessageFailure(result); e != nil {
		return e
	}
	return nil
}

func handlePostMessageFailure(result slack.PostMessageResult) error {
	switch result.Type {
	case slack.PostMessageResultOK:
		return nil
	case slack.PostMessageResultServerTimeoutFailure:
		return fmt.Errorf("slack server timeout")
	case slack.PostMessageResultServerFailure:
		return fmt.Errorf("slack server error: code=%d, body=%s", result.StatusCode, result.Body)
	case slack.PostMessageResultDomainFailure:
		return fmt.Errorf("slack domain error: channelName=%s, channelID=%s, reason=%s", result.ChannelName, result.ChannelID, result.Reason)
	default:
		return fmt.Errorf("unknown PostMessageResult type: %d", result.Type)
	}
}
