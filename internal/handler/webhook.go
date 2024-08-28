package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/cockroachdb/errors"
	"github.com/labstack/echo/v4"

	"github.com/Finatext/belldog/internal/slack"
)

func (h *ProxyHandler) Webhook(c echo.Context) error {
	ctx := c.Request().Context()
	channelName := c.Param("channel_name")
	token := c.Param("token")

	res, err := h.tokenSvc.VerifyToken(ctx, channelName, token)
	if err != nil {
		return err
	}

	if res.NotFound {
		slog.InfoContext(ctx, "No token generated, response not found", slog.String("channel_name", channelName))
		msg := fmt.Sprintf("No token generated for %s, generate token with `%s` slash command.\n", channelName, cmdGenerate)
		return c.String(http.StatusNotFound, msg)
	}
	if res.Unmatch {
		slog.InfoContext(ctx, "Invalid token given, response unauthorized", slog.String("channel_name", channelName), slog.String("token", token))
		return c.String(http.StatusUnauthorized, "Invalid token given. Check generated URL.\n")
	}

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return errors.Wrap(err, "failed to read request body")
	}
	payload, err := parseRequestBody(c.Request(), body)
	if err != nil {
		slog.InfoContext(ctx, "parseRequestBody failed, response bad request", slog.String("error", err.Error()), slog.String("body", string(body)))
		return c.String(http.StatusBadRequest, "Invalid body given. JSON Unmarshal failed.\n")
	}

	result, err := h.slackClient.PostMessage(ctx, res.ChannelID, res.ChannelName, payload)
	if err != nil {
		slog.ErrorContext(ctx, "PostMessage failed",
			slog.String("error", err.Error()),
			slog.String("channel_id", res.ChannelID),
			slog.String("channel_name", res.ChannelName),
			slog.Int("body size", len(body)),
		)
		slog.DebugContext(ctx, "failed PostMessage body", slog.String("body", string(body)))
		return err
	}

	switch result.Type {
	case slack.PostMessageResultOK:
		slog.InfoContext(ctx, "PostMessage succeeded",
			slog.String("channel_id", res.ChannelID),
			slog.String("channel_name", res.ChannelName),
		)
		return c.String(http.StatusOK, "ok.\n")
	case slack.PostMessageResultServerTimeoutFailure:
		slog.WarnContext(ctx, "PostMessage timeout",
			slog.String("channel_id", res.ChannelID),
			slog.String("channel_name", res.ChannelName),
		)
		return c.String(http.StatusGatewayTimeout, "Slack API timeout.\n")
	case slack.PostMessageResultServerFailure:
		msg := fmt.Sprintf("Slack API error: status=%d, body=%s\n", result.StatusCode, result.Body)
		if result.StatusCode >= 500 && result.StatusCode < 600 {
			slog.WarnContext(ctx, "PostMessage server error", slog.Int("status_code", result.StatusCode), slog.String("body", result.Body))
			return c.String(http.StatusBadGateway, msg)
		} else if result.StatusCode >= 400 && result.StatusCode < 500 {
			slog.InfoContext(ctx, "PostMessage client error", slog.Int("status_code", result.StatusCode), slog.String("body", result.Body))
			return c.String(result.StatusCode, msg)
		} else {
			return errors.Newf("unexpected status code from Slack API: code=%d, body=%s", result.StatusCode, result.Body)
		}
	case slack.PostMessageResultAPIFailure:
		if result.Reason == "channel_not_found" {
			msg := fmt.Sprintf("invite bot to the channel: channelName=%s, channelID=%s, reason=%s", result.ChannelName, result.ChannelID, result.Reason)
			return c.String(http.StatusBadRequest, msg)
		} else {
			slog.WarnContext(ctx, "PostMessage Slack API responses error response",
				slog.String("channel_id", res.ChannelID),
				slog.String("channel_name", res.ChannelName),
				slog.String("reason", result.Reason),
			)
			msg := fmt.Sprintf("Slack API responses error: reason=%s", result.Reason)
			return c.String(http.StatusBadRequest, msg)
		}
	default:
		return errors.Newf("unexpected PostMessageResult type: %v", result.Type)
	}
}

// Lagacy Slack webhook accepts both of "application/json" and "application/x-www-form-urlencoded" contents.
// Also accepts pure JSON request body regardless of content-type header field, so we must accept JSON payload,
// event when the content-type header filed value is "application/x-www-form-urlencoded". And if the content is
// encoded as form-data, the JSON payload will be at `payload` key.
//
// This behavior is not documented now. Some old clients needs this behavior.
func parseRequestBody(req *http.Request, body []byte) (map[string]interface{}, error) {
	contentType, ok := req.Header[http.CanonicalHeaderKey("content-type")]
	if ok && contains(contentType, "application/x-www-form-urlencoded") {
		b, err := extractPayloadValue(body)
		if err != nil {
			return nil, err
		}
		body = b
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal JSON")
	}
	return payload, nil
}

func extractPayloadValue(body []byte) ([]byte, error) {
	// Use url.ParseQuery like http package.
	// https://cs.opensource.google/go/go/+/refs/tags/go1.19.2:src/net/http/request.go;l=1246;drc=61f0409c31cad8729d7982425d353d7b2ea80534
	vs, err := url.ParseQuery(string(body))
	// The clients may send raw JSON, but url.ParseQuery doesn't fail if raw JSON was passed in most cases.
	//
	// >A setting without an equals sign is interpreted as a key set to an empty value.
	//
	// https://cs.opensource.google/go/go/+/refs/tags/go1.19.2:src/net/url/url.go;l=928
	//
	// url.ParseQuery fails when parsing invalid semicolon separators or escapes.
	if err != nil {
		// Fallback to parse as raw JSON.
		//nolint:nilerr
		return body, nil
	}
	v, ok := vs["payload"]
	if !ok {
		// The client may send raw JSON request body with form-data content-type header field, so continue
		// to parse as JSON.
		return body, nil
	}
	if len(v) != 1 {
		return nil, errors.Newf("the HTTP query `payload` value must be a single value: len=%d", len(v))
	}
	return []byte(v[0]), nil
}

func contains(slice []string, item string) bool {
	for _, v := range slice {
		if v == item {
			return true
		}
	}
	return false
}
