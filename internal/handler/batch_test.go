package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/Finatext/belldog/internal/appconfig"
	"github.com/Finatext/belldog/internal/slack"
	"github.com/Finatext/belldog/internal/storage"
	"github.com/aws/aws-lambda-go/events"
	slackgo "github.com/slack-go/slack"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var defaultConfig = appconfig.Config{
	OpsNotificationChannelName: "ops",
}

func TestBatchOk(t *testing.T) {
	channelID := "C123456"
	channelName := "test"

	slackClient := &mockSlackClient{}
	ddb := &mockStorageDDB{}

	ddb.On("ScanAll", mock.Anything).Return([]storage.Record{
		{
			ChannelID:   channelID,
			ChannelName: channelName,
			Token:       "token_a",
		},
	}, nil)
	slackClient.On("GetAllChannels", mock.Anything).Return([]slackgo.Channel{
		{
			GroupConversation: slackgo.GroupConversation{
				Name: "test",
				Conversation: slackgo.Conversation{
					ID: channelID,
				},
			},
		},
	}, nil)

	h := NewBatchHandler(defaultConfig, slackClient, ddb)
	err := h.HandleCloudWatchEvent(context.Background(), events.CloudWatchEvent{})
	require.NoError(t, err)
}

func TestBatchMigration(t *testing.T) {
	channelID := "C123456"
	channelName := "test"

	cfg := defaultConfig
	slackClient := &mockSlackClient{}
	ddb := &mockStorageDDB{}

	ddb.On("ScanAll", mock.Anything).Return([]storage.Record{
		{
			ChannelID:   channelID,
			ChannelName: channelName,
			Token:       "token_a",
		},
		{
			ChannelID:   channelID,
			ChannelName: channelName,
			Token:       "token_b",
		},
	}, nil)
	slackClient.On("GetAllChannels", mock.Anything).Return([]slackgo.Channel{
		{
			GroupConversation: slackgo.GroupConversation{
				Name: channelName,
				Conversation: slackgo.Conversation{
					ID: channelID,
				},
			},
		},
	}, nil)

	messageMatcher := mock.MatchedBy(func(payload map[string]interface{}) bool {
		return payload["text"] == "Token is in migration: channel_name=test, channel_id=C123456\n"
	})
	slackClient.On("PostMessage", mock.Anything, channelID, channelName, mock.Anything).Return(slack.PostMessageResult{}, nil)
	slackClient.On("PostMessage", mock.Anything, cfg.OpsNotificationChannelName, cfg.OpsNotificationChannelName, messageMatcher).Return(slack.PostMessageResult{}, nil)

	h := NewBatchHandler(cfg, slackClient, ddb)
	err := h.HandleCloudWatchEvent(context.Background(), events.CloudWatchEvent{})
	require.NoError(t, err)
	slackClient.AssertExpectations(t)
}

func TestBatchRename(t *testing.T) {
	channelID := "C123456"
	channelName := "test"

	cfg := defaultConfig
	slackClient := &mockSlackClient{}
	ddb := &mockStorageDDB{}

	ddb.On("ScanAll", mock.Anything).Return([]storage.Record{
		{
			ChannelID:   channelID,
			ChannelName: channelName,
			Token:       "token_a",
		},
	}, nil)
	slackClient.On("GetAllChannels", mock.Anything).Return([]slackgo.Channel{
		{
			GroupConversation: slackgo.GroupConversation{
				Name: "renamed",
				Conversation: slackgo.Conversation{
					ID: channelID,
				},
			},
		},
	}, nil)

	messageMatcher := mock.MatchedBy(func(payload map[string]interface{}) bool {
		return strings.HasPrefix(payload["text"].(string), "Channel name and channel id pair updated: channel_id=C123456, old_channel_name=test, renamed_channel_name=renamed")
	})
	slackClient.On("PostMessage", mock.Anything, channelID, "renamed", mock.Anything).Return(slack.PostMessageResult{}, nil)
	slackClient.On("PostMessage", mock.Anything, cfg.OpsNotificationChannelName, cfg.OpsNotificationChannelName, messageMatcher).Return(slack.PostMessageResult{}, nil)

	h := NewBatchHandler(cfg, slackClient, ddb)
	err := h.HandleCloudWatchEvent(context.Background(), events.CloudWatchEvent{})
	require.NoError(t, err)
	slackClient.AssertExpectations(t)
}

func TestBatchArchived(t *testing.T) {
	channelID := "C123456"
	channelName := "test"
	arcvhiedChannelID := "C789012"
	arcvhiedChannelName := "archived"

	cfg := defaultConfig
	slackClient := &mockSlackClient{}
	ddb := &mockStorageDDB{}

	rec := storage.Record{
		ChannelID:   arcvhiedChannelID,
		ChannelName: arcvhiedChannelName,
		Token:       "token_b",
	}
	ddb.On("ScanAll", mock.Anything).Return([]storage.Record{
		{
			ChannelID:   channelID,
			ChannelName: channelName,
			Token:       "token_a",
		},
		rec,
	}, nil)
	slackClient.On("GetAllChannels", mock.Anything).Return([]slackgo.Channel{
		{
			GroupConversation: slackgo.GroupConversation{
				Name: channelName,
				Conversation: slackgo.Conversation{
					ID: channelID,
				},
			},
		},
		{
			GroupConversation: slackgo.GroupConversation{
				IsArchived: true,
				Name:       arcvhiedChannelName,
				Conversation: slackgo.Conversation{
					ID: arcvhiedChannelID,
				},
			},
		},
	}, nil)
	ddb.On("Delete", mock.Anything, rec).Return(nil)

	messageMatcher := mock.MatchedBy(func(payload map[string]interface{}) bool {
		return payload["text"].(string) == "Channel is archived, deleting record: channel_id=C789012, record_channel_name=archived, slack_channel_name=archived\n"
	})
	slackClient.On("PostMessage", mock.Anything, cfg.OpsNotificationChannelName, cfg.OpsNotificationChannelName, messageMatcher).Return(slack.PostMessageResult{}, nil)

	h := NewBatchHandler(cfg, slackClient, ddb)
	err := h.HandleCloudWatchEvent(context.Background(), events.CloudWatchEvent{})
	require.NoError(t, err)
	slackClient.AssertExpectations(t)
	ddb.AssertExpectations(t)
}

func TestBatchRenameArchived(t *testing.T) {
	channelID := "C123456"
	channelName := "test"
	arcvhiedChannelID := "C789012"
	arcvhiedChannelName := "archived"

	cfg := defaultConfig
	slackClient := &mockSlackClient{}
	ddb := &mockStorageDDB{}

	rec := storage.Record{
		ChannelID:   arcvhiedChannelID,
		ChannelName: arcvhiedChannelName,
		Token:       "token_b",
	}
	ddb.On("ScanAll", mock.Anything).Return([]storage.Record{
		{
			ChannelID:   channelID,
			ChannelName: channelName,
			Token:       "token_a",
		},
		rec,
	}, nil)
	slackClient.On("GetAllChannels", mock.Anything).Return([]slackgo.Channel{
		{
			GroupConversation: slackgo.GroupConversation{
				Name: channelName,
				Conversation: slackgo.Conversation{
					ID: channelID,
				},
			},
		},
		{
			GroupConversation: slackgo.GroupConversation{
				IsArchived: true,
				Name:       "renamed_and_archived",
				Conversation: slackgo.Conversation{
					ID: arcvhiedChannelID,
				},
			},
		},
	}, nil)
	ddb.On("Delete", mock.Anything, rec).Return(nil)

	messageMatcher := mock.MatchedBy(func(payload map[string]interface{}) bool {
		return payload["text"].(string) == "Channel is archived, deleting record: channel_id=C789012, record_channel_name=archived, slack_channel_name=renamed_and_archived\n"
	})
	slackClient.On("PostMessage", mock.Anything, cfg.OpsNotificationChannelName, cfg.OpsNotificationChannelName, messageMatcher).Return(slack.PostMessageResult{}, nil)

	h := NewBatchHandler(cfg, slackClient, ddb)
	err := h.HandleCloudWatchEvent(context.Background(), events.CloudWatchEvent{})
	require.NoError(t, err)
	slackClient.AssertExpectations(t)
	ddb.AssertExpectations(t)
}
