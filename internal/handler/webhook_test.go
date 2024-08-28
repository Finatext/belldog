package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/Finatext/belldog/internal/appconfig"
	"github.com/Finatext/belldog/internal/service"
	"github.com/Finatext/belldog/internal/slack"
)

var defaultPayload = map[string]interface{}{
	"text": "hello",
}

func defaultPayloadJSON() string {
	ret, e := json.Marshal(defaultPayload)
	if e != nil {
		panic(e)
	}
	return string(ret)
}

func setupContext(payload *string) echo.Context {
	if payload == nil {
		payload = new(string)
		*payload = defaultPayloadJSON()
	}

	channelName := "test"
	token := "deadbeef"
	path := fmt.Sprintf("/p/%s/%s", channelName, token)
	req := httptest.NewRequest(http.MethodGet, path, strings.NewReader(*payload))
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetPath("/p/:channel_name/:token")
	c.SetParamNames("channel_name", "token")
	c.SetParamValues(channelName, token)
	return c
}

func TestWebhookOk(t *testing.T) {
	slackClient := &mockSlackClient{}
	svc := &mockTokenService{}
	svc.On("VerifyToken", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(service.VerifyResult{}, nil)
	slackClient.On("PostMessage", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), defaultPayload).Return(slack.PostMessageResult{
		Type: slack.PostMessageResultOK,
	}, nil)

	h := ProxyHandler{
		cfg:         appconfig.Config{},
		slackClient: slackClient,
		tokenSvc:    svc,
	}
	c := setupContext(nil)
	err := h.Webhook(c)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, c.Response().Status)
}

func TestWebhookFormData(t *testing.T) {
	slackClient := &mockSlackClient{}
	svc := &mockTokenService{}
	svc.On("VerifyToken", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(service.VerifyResult{}, nil)
	slackClient.On("PostMessage", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), defaultPayload).Return(slack.PostMessageResult{
		Type: slack.PostMessageResultOK,
	}, nil)

	h := ProxyHandler{
		cfg:         appconfig.Config{},
		slackClient: slackClient,
		tokenSvc:    svc,
	}
	f := make(url.Values)
	f.Set("payload", defaultPayloadJSON())
	payload := f.Encode()
	c := setupContext(&payload)
	c.Request().Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	err := h.Webhook(c)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, c.Response().Status)
}

func TestWebhookJSONWithFormContentType(t *testing.T) {
	slackClient := &mockSlackClient{}
	svc := &mockTokenService{}
	svc.On("VerifyToken", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(service.VerifyResult{}, nil)
	slackClient.On("PostMessage", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), defaultPayload).Return(slack.PostMessageResult{
		Type: slack.PostMessageResultOK,
	}, nil)

	h := ProxyHandler{
		cfg:         appconfig.Config{},
		slackClient: slackClient,
		tokenSvc:    svc,
	}
	c := setupContext(nil)
	c.Request().Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	err := h.Webhook(c)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, c.Response().Status)
}

func TestWebhookSlackTimeout(t *testing.T) {
	slackClient := &mockSlackClient{}
	svc := &mockTokenService{}
	svc.On("VerifyToken", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(service.VerifyResult{}, nil)
	slackClient.On("PostMessage", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), defaultPayload).Return(slack.PostMessageResult{
		Type: slack.PostMessageResultServerTimeoutFailure,
	}, nil)

	h := ProxyHandler{
		cfg:         appconfig.Config{},
		slackClient: slackClient,
		tokenSvc:    svc,
	}
	c := setupContext(nil)
	err := h.Webhook(c)

	require.NoError(t, err)
	assert.Equal(t, http.StatusGatewayTimeout, c.Response().Status)
}

func TestWebhookSlackServerFailure(t *testing.T) {
	slackClient := &mockSlackClient{}
	svc := &mockTokenService{}
	svc.On("VerifyToken", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(service.VerifyResult{}, nil)
	slackClient.On("PostMessage", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), defaultPayload).Return(slack.PostMessageResult{
		Type:       slack.PostMessageResultServerFailure,
		StatusCode: 500,
	}, nil)

	h := ProxyHandler{
		cfg:         appconfig.Config{},
		slackClient: slackClient,
		tokenSvc:    svc,
	}
	c := setupContext(nil)
	err := h.Webhook(c)

	require.NoError(t, err)
	assert.Equal(t, http.StatusBadGateway, c.Response().Status)
}

func TestWebhookSlackBadRequest(t *testing.T) {
	slackClient := &mockSlackClient{}
	svc := &mockTokenService{}
	svc.On("VerifyToken", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(service.VerifyResult{}, nil)
	slackClient.On("PostMessage", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), defaultPayload).Return(slack.PostMessageResult{
		Type:       slack.PostMessageResultServerFailure,
		StatusCode: 400,
	}, nil)

	h := ProxyHandler{
		cfg:         appconfig.Config{},
		slackClient: slackClient,
		tokenSvc:    svc,
	}
	c := setupContext(nil)
	err := h.Webhook(c)

	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, c.Response().Status)
}

func TestWebhookSlackUnexpectedResponse(t *testing.T) {
	slackClient := &mockSlackClient{}
	svc := &mockTokenService{}
	svc.On("VerifyToken", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(service.VerifyResult{}, nil)
	slackClient.On("PostMessage", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), defaultPayload).Return(slack.PostMessageResult{
		Type:       slack.PostMessageResultServerFailure,
		StatusCode: 301,
	}, nil)

	h := ProxyHandler{
		cfg:         appconfig.Config{},
		slackClient: slackClient,
		tokenSvc:    svc,
	}
	c := setupContext(nil)
	err := h.Webhook(c)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status code from Slack API")
}

func TestWebhookSlackAPIFailure(t *testing.T) {
	slackClient := &mockSlackClient{}
	svc := &mockTokenService{}
	svc.On("VerifyToken", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(service.VerifyResult{}, nil)
	slackClient.On("PostMessage", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), defaultPayload).Return(slack.PostMessageResult{
		Type:        slack.PostMessageResultAPIFailure,
		Reason:      "invalid_blocks",
		ChannelID:   "C123456",
		ChannelName: "test",
	}, nil)

	h := ProxyHandler{
		cfg:         appconfig.Config{},
		slackClient: slackClient,
		tokenSvc:    svc,
	}
	c := setupContext(nil)
	err := h.Webhook(c)

	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, c.Response().Status)
}
