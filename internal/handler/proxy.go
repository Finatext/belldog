package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/cockroachdb/errors"

	"github.com/Finatext/belldog/internal/appconfig"
	"github.com/Finatext/belldog/internal/slack"
)

type (
	Request  = events.LambdaFunctionURLRequest
	Response = events.LambdaFunctionURLResponse
)

type ProxyHandler struct {
	cfg         appconfig.Config
	slackClient slackClient
	tokenSvc    tokenService
}

func NewProxyHandler(cfg appconfig.Config, slackClient slackClient, svc tokenService) *ProxyHandler {
	return &ProxyHandler{
		cfg:         cfg,
		slackClient: slackClient,
		tokenSvc:    svc,
	}
}

func (h *ProxyHandler) HandleRequestWithCacheControl(ctx context.Context, req Request) (Response, error) {
	res, err := h.handleRequestWithAccessLogging(ctx, req)
	if err != nil {
		slog.ErrorContext(ctx, "event handler failed", slog.String("error", fmt.Sprintf("%+v", err)))
		return res, err
	}
	if res.Headers == nil {
		res.Headers = make(map[string]string)
	}
	res.Headers["cache-control"] = "no-store, no-cache"
	return res, err
}

func (h *ProxyHandler) handleRequestWithAccessLogging(ctx context.Context, req Request) (Response, error) {
	res, err := h.handleRequest(ctx, req)
	if err != nil {
		slog.ErrorContext(ctx, "access_log",
			slog.String("request_id", req.RequestContext.RequestID),
			slog.String("method", req.RequestContext.HTTP.Method),
			slog.String("path", maskToken(req.RequestContext.HTTP.Path)),
			slog.String("raw_path", maskToken(req.RawPath)),
			slog.String("user_agent", req.RequestContext.HTTP.UserAgent),
			slog.String("source_ip", req.RequestContext.HTTP.SourceIP),
			slog.String("protocol", req.RequestContext.HTTP.Protocol),
			slog.Int("req_body_size", len(req.Body)),
		)
		slog.ErrorContext(ctx, "handleRequest failed", slog.String("error", err.Error()))
	} else {
		slog.InfoContext(
			ctx, "access_log",
			slog.String("request_id", req.RequestContext.RequestID),
			slog.String("method", req.RequestContext.HTTP.Method),
			slog.String("path", maskToken(req.RequestContext.HTTP.Path)),
			slog.String("raw_path", maskToken(req.RawPath)),
			slog.String("user_agent", req.RequestContext.HTTP.UserAgent),
			slog.String("source_ip", req.RequestContext.HTTP.SourceIP),
			slog.String("protocol", req.RequestContext.HTTP.Protocol),
			slog.Int("status_code", res.StatusCode),
			slog.Int("req_body_size", len(req.Body)),
		)
	}
	return res, err
}

func (h *ProxyHandler) handleRequest(ctx context.Context, req Request) (Response, error) {
	if req.RequestContext.HTTP.Method != "POST" {
		return Response{Body: "Only POST method is supported.\n", StatusCode: http.StatusNotFound}, nil
	}
	body, err := decodeBody(req)
	if err != nil {
		return Response{}, err
	}

	switch {
	case req.RawPath == "/slash/":
		return h.handleSlashCommand(ctx, req, body)
	case strings.HasPrefix(req.RawPath, "/p/"):
		return h.handleWebhook(ctx, req, body)
	default:
		return Response{Body: "Not found.\n", StatusCode: http.StatusNotFound}, nil
	}
}

// When caller doesn't set the content-type field to "application/json".
// https://docs.aws.amazon.com/apigateway/latest/developerguide/api-gateway-payload-encodings-workflow.html
func decodeBody(req Request) ([]byte, error) {
	if req.IsBase64Encoded {
		b, err := base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return []byte{}, errors.Wrap(err, "failed to decode base64")
		}
		return b, nil
	}
	return []byte(req.Body), nil
}

const correctMatchSize = 3

// Define here to run regexp.MustCompilePOSIX once.
var pathRe = regexp.MustCompilePOSIX(`^/p/([^/]+)/([^/]+)/?$`)

func parsePath(path string) (channelName string, token string, err error) {
	decodedPath, err := url.PathUnescape(path)
	if err != nil {
		err = fmt.Errorf("error decoding path `%s`: %w", path, err)
		return
	}
	res := pathRe.FindStringSubmatch(decodedPath)
	if len(res) != correctMatchSize {
		err = fmt.Errorf("channelName or token not found: %s", decodedPath)
		return
	}
	channelName, token = res[1], res[2]
	if token == "" || channelName == "" {
		err = fmt.Errorf("token or channelName is empty: `%s`, `%s`", token, channelName)
		return
	}
	return
}

func maskToken(path string) string {
	res := pathRe.FindStringSubmatch(path)
	if len(res) != correctMatchSize {
		return path
	}
	masked := strings.Repeat("*", len(res[2]))
	return fmt.Sprintf("/p/%s/%s/", res[1], masked)
}

// Marshal to json to use "in_channel" type response: https://api.slack.com/interactivity/slash-commands
func buildResponse(msg string) (Response, error) {
	payload := map[string]string{
		"text":          msg,
		"response_type": "in_channel",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Response{}, errors.Wrap(err, "failed to marshal response")
	}
	return Response{Body: string(body), StatusCode: http.StatusOK}, nil
}

func logCommandRequest(ctx context.Context, cmdReq slack.SlashCommandRequest) {
	slog.InfoContext(ctx, "command given",
		slog.String("command", cmdReq.Command),
		slog.String("channel_id", cmdReq.ChannelID),
		slog.String("channel_name", cmdReq.ChannelName),
		slog.String("original_channel_name", cmdReq.OriginalChannelName),
		slog.String("text", cmdReq.Text),
		slog.Bool("supported", cmdReq.Supported),
	)
}
