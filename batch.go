package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Finatext/belldog/slack"
	"github.com/Finatext/belldog/storage"
	"github.com/aws/aws-lambda-go/events"
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

	st, err := storage.NewStorage(ctx, config.DdbTableName)
	if err != nil {
		return fmt.Errorf("storage.NewStorage failed: %w", err)
	}
	recs, err := st.ScanAll(ctx)
	if err != nil {
		return fmt.Errorf("Storage.ScanAll failed: %w", err)
	}

	kit := slack.NewKit(config.SlackToken)
	channels, err := kit.GetAllChannels(ctx)
	if err != nil {
		return fmt.Errorf("slack.GetAllChannels failed: %w", err)
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
			return fmt.Errorf("notify failed: %w", err)
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
			return fmt.Errorf("notify failed: %w", err)
		}
	}
	return nil
}

func notify(ctx context.Context, kit slack.Kit, channelID string, channelName string, msg string, msgOps string) error {
	payload := map[string]interface{}{"text": msg}
	if err := kit.PostMessage(ctx, channelID, channelName, payload); err != nil {
		return fmt.Errorf("kit.PostMessage failed: %w", err)
	}
	payloadOps := map[string]interface{}{"text": msgOps}
	// kit.PostMessage can accept channel name as channel id.
	if err := kit.PostMessage(ctx, config.OpsNotificationChannelName, config.OpsNotificationChannelName, payloadOps); err != nil {
		return fmt.Errorf("kit.PostMessage failed: %w", err)
	}
	return nil
}
