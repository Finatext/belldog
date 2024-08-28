package handler

import (
	"context"

	slackgo "github.com/slack-go/slack"
	"github.com/stretchr/testify/mock"

	"github.com/Finatext/belldog/internal/service"
	"github.com/Finatext/belldog/internal/slack"
	"github.com/Finatext/belldog/internal/storage"
)

type mockSlackClient struct {
	mock.Mock
}

func (m *mockSlackClient) PostMessage(ctx context.Context, channelID string, channelName string, payload map[string]interface{}) (slack.PostMessageResult, error) {
	args := m.Called(ctx, channelID, channelName, payload)
	return args.Get(0).(slack.PostMessageResult), args.Error(1)
}

func (m *mockSlackClient) GetAllChannels(ctx context.Context) ([]slackgo.Channel, error) {
	args := m.Called(ctx)
	return args.Get(0).([]slackgo.Channel), args.Error(1)
}

func (m *mockSlackClient) GetFullCommandRequest(ctx context.Context, body string) (slack.SlashCommandRequest, error) {
	args := m.Called(ctx, body)
	return args.Get(0).(slack.SlashCommandRequest), args.Error(1)
}

type mockTokenService struct {
	mock.Mock
}

func (m *mockTokenService) VerifyToken(ctx context.Context, channelName string, givenToken string) (service.VerifyResult, error) {
	args := m.Called(ctx, channelName, givenToken)
	return args.Get(0).(service.VerifyResult), args.Error(1)
}

func (m *mockTokenService) GenerateAndSaveToken(ctx context.Context, channelID string, channelName string) (service.GenerateResult, error) {
	args := m.Called(ctx, channelID, channelName)
	return args.Get(0).(service.GenerateResult), args.Error(1)
}

func (m *mockTokenService) RevokeToken(ctx context.Context, channelName string, givenToken string) (service.RevokeResult, error) {
	args := m.Called(ctx, channelName, givenToken)
	return args.Get(0).(service.RevokeResult), args.Error(1)
}

func (m *mockTokenService) RevokeRenamedToken(ctx context.Context, channelID string, givenChannelName string, givenToken string) (service.RevokeRenamedResult, error) {
	args := m.Called(ctx, channelID, givenChannelName, givenToken)
	return args.Get(0).(service.RevokeRenamedResult), args.Error(1)
}

func (m *mockTokenService) GetTokens(ctx context.Context, channelName string) ([]service.Entry, error) {
	args := m.Called(ctx, channelName)
	return args.Get(0).([]service.Entry), args.Error(1)
}

func (m *mockTokenService) RegenerateToken(ctx context.Context, channelID string, channelName string) (service.RegenerateResult, error) {
	args := m.Called(ctx, channelID, channelName)
	return args.Get(0).(service.RegenerateResult), args.Error(1)
}

type mockStorageDDB struct {
	mock.Mock
}

func (m *mockStorageDDB) Save(ctx context.Context, rec storage.Record) error {
	args := m.Called(ctx, rec)
	return args.Error(0)
}

func (m *mockStorageDDB) QueryByChannelName(ctx context.Context, channelName string) ([]storage.Record, error) {
	args := m.Called(ctx, channelName)
	return args.Get(0).([]storage.Record), args.Error(1)
}

func (m *mockStorageDDB) Delete(ctx context.Context, rec storage.Record) error {
	args := m.Called(ctx, rec)
	return args.Error(0)
}

func (m *mockStorageDDB) ScanAll(ctx context.Context) ([]storage.Record, error) {
	args := m.Called(ctx)
	return args.Get(0).([]storage.Record), args.Error(1)
}
