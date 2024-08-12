package handler

import (
	"context"

	slackgo "github.com/slack-go/slack"

	"github.com/Finatext/belldog/internal/service"
	"github.com/Finatext/belldog/internal/slack"
	"github.com/Finatext/belldog/internal/storage"
)

type slackClient interface {
	PostMessage(ctx context.Context, channelID string, channelName string, payload map[string]interface{}) (slack.PostMessageResult, error)
	GetAllChannels(ctx context.Context) ([]slackgo.Channel, error)
	GetFullCommandRequest(ctx context.Context, body string) (slack.SlashCommandRequest, error)
}

type storageDDB interface {
	Save(ctx context.Context, rec storage.Record) error
	QueryByChannelName(ctx context.Context, channelName string) ([]storage.Record, error)
	Delete(ctx context.Context, rec storage.Record) error
	ScanAll(ctx context.Context) ([]storage.Record, error)
}

type tokenService interface {
	GetTokens(ctx context.Context, channelName string) ([]service.Entry, error)
	VerifyToken(ctx context.Context, channelName string, givenToken string) (service.VerifyResult, error)
	GenerateAndSaveToken(ctx context.Context, channelID string, channelName string) (service.GenerateResult, error)
	RegenerateToken(ctx context.Context, channelID string, channelName string) (service.RegenerateResult, error)
	RevokeToken(ctx context.Context, channelName string, givenToken string) (service.RevokeResult, error)
	RevokeRenamedToken(ctx context.Context, channelID string, givenChannelName string, givenToken string) (service.RevokeRenamedResult, error)
}
