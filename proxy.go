package main

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
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/cockroachdb/errors"

	"github.com/Finatext/belldog/domain"
	"github.com/Finatext/belldog/slack"
	"github.com/Finatext/belldog/storage"
)

const (
	cmdShow          = "/belldog-show"
	cmdGenerate      = "/belldog-generate"
	cmdRegenerate    = "/belldog-regenerate"
	cmdRevoke        = "/belldog-revoke"
	cmdRevokeRenamed = "/belldog-revoke-renamed"
)

type (
	request  = events.LambdaFunctionURLRequest
	response = events.LambdaFunctionURLResponse
)

func handleRequestWithCacheControl(ctx context.Context, req request) (response, error) {
	res, err := handleRequestWithAccessLogging(ctx, req)
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

func handleRequestWithAccessLogging(ctx context.Context, req request) (response, error) {
	res, err := handleRequest(ctx, req)
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

func handleRequest(ctx context.Context, req request) (response, error) {
	if req.RequestContext.HTTP.Method != "POST" {
		return response{Body: "Only POST method is supported.\n", StatusCode: http.StatusNotFound}, nil
	}
	body, err := decodeBody(req)
	if err != nil {
		return response{}, err
	}

	switch {
	case req.RawPath == "/slash/":
		return handleSlashCommand(ctx, req, body)
	case strings.HasPrefix(req.RawPath, "/p/"):
		return handleWebhook(ctx, req, body)
	default:
		return response{Body: "Not found.\n", StatusCode: http.StatusNotFound}, nil
	}
}

// When caller doesn't set the content-type field to "application/json".
// https://docs.aws.amazon.com/apigateway/latest/developerguide/api-gateway-payload-encodings-workflow.html
func decodeBody(req request) ([]byte, error) {
	if req.IsBase64Encoded {
		b, err := base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return []byte{}, errors.Wrap(err, "failed to decode base64")
		}
		return b, nil
	}
	return []byte(req.Body), nil
}

func handleSlashCommand(ctx context.Context, req request, body []byte) (response, error) {
	if !slack.VerifySlackRequest(ctx, config.SlackSigningSecret, req.Headers, string(body)) {
		return response{Body: "Bad request.\n", StatusCode: http.StatusBadRequest}, nil
	}

	// XXX: create object in initializing phase. Use handler struct pattern.
	kit := slack.NewKit(config.SlackToken, slackRetryConfig)
	cmdReq, err := kit.GetFullCommandRequest(ctx, string(body))
	if err != nil {
		return response{}, err
	}
	logCommandRequest(ctx, cmdReq)
	if !cmdReq.Supported {
		return buildResponse("Belldog only supports public/private channels. If this is a private channel, invite Belldog.\n")
	}

	st, err := storage.NewStorage(ctx, config.DdbTableName)
	if err != nil {
		return response{}, err
	}

	svc := domain.NewDomain(st)

	// https://api.slack.com/interactivity/slash-commands#creating_commands
	switch cmdReq.Command {
	case cmdShow:
		return processCmdShow(ctx, svc, cmdReq, req)
	case cmdGenerate:
		return processCmdGenerate(ctx, svc, cmdReq, req)
	case cmdRegenerate:
		return processCmdRegenerate(ctx, svc, cmdReq, req)
	case cmdRevoke:
		return processCmdRevoke(ctx, svc, cmdReq)
	case cmdRevokeRenamed:
		return processCmdRevokeRenamed(ctx, svc, cmdReq)
	default:
		slog.InfoContext(ctx, "missing command given", slog.String("command", cmdReq.Command))
		return buildResponse("Missing command.\n")
	}
}

func handleWebhook(ctx context.Context, req request, body []byte) (response, error) {
	channelName, token, err := parsePath(req.RawPath)
	if err != nil {
		slog.InfoContext(ctx, "Invalid request path given, response bad request", slog.String("error", err.Error()))
		return response{Body: "Invalid request path\n", StatusCode: http.StatusBadRequest}, nil
	}

	st, err := storage.NewStorage(ctx, config.DdbTableName)
	if err != nil {
		return response{}, err
	}
	svc := domain.NewDomain(st)
	res, err := svc.VerifyToken(ctx, channelName, token)
	if err != nil {
		return response{}, err
	}
	if res.NotFound {
		slog.InfoContext(ctx, "No token generated, response not found", slog.String("channel_name", channelName))
		msg := fmt.Sprintf("No token generated for %s, generate token with `%s` slash command.\n", channelName, cmdGenerate)
		return response{Body: msg, StatusCode: http.StatusNotFound}, nil
	}
	if res.Unmatch {
		slog.InfoContext(ctx, "Invalid token given, response unauthorized", slog.String("channel_name", channelName), slog.String("token", token))
		return response{Body: "Invalid token given. Check generated URL.\n", StatusCode: http.StatusUnauthorized}, nil
	}

	payload, err := parseRequestBody(req, body)
	if err != nil {
		slog.InfoContext(ctx, "parseRequestBody failed, response bad request", slog.String("error", err.Error()), slog.String("body", string(body)))
		return response{Body: "Invalid body given. JSON Unmarshal failed.\n", StatusCode: http.StatusBadRequest}, nil
	}

	kit := slack.NewKit(config.SlackToken, slackRetryConfig)
	result, err := kit.PostMessage(ctx, res.ChannelID, res.ChannelName, payload)
	if err != nil {
		slog.ErrorContext(ctx, "PostMessage failed",
			slog.String("error", err.Error()),
			slog.String("channel_id", res.ChannelID),
			slog.String("channel_name", res.ChannelName),
			slog.Int("body size", len(body)),
		)
		slog.DebugContext(ctx, "debug PostMessage failed", slog.String("body", string(body)))
		return response{}, err
	}

	switch result.Type {
	case slack.PostMessageResultOK:
		slog.InfoContext(ctx, "PostMessage succeeded",
			slog.String("channel_id", res.ChannelID),
			slog.String("channel_name", res.ChannelName),
		)
		return response{Body: "ok.\n", StatusCode: http.StatusOK}, nil
	case slack.PostMessageResultServerTimeoutFailure:
		slog.WarnContext(ctx, "PostMessage timeout",
			slog.String("channel_id", res.ChannelID),
			slog.String("channel_name", res.ChannelName),
		)
		return response{Body: "Slack API timeout.\n", StatusCode: http.StatusGatewayTimeout}, nil
	case slack.PostMessageResultServerFailure:
		msg := fmt.Sprintf("Slack API error: status=%d, body=%s\n", result.StatusCode, result.Body)
		if result.StatusCode >= 500 && result.StatusCode < 600 {
			slog.WarnContext(ctx, "PostMessage server error", slog.Int("status_code", result.StatusCode), slog.String("body", result.Body))
			return response{Body: msg, StatusCode: http.StatusBadGateway}, nil
		} else if result.StatusCode >= 400 && result.StatusCode < 500 {
			slog.InfoContext(ctx, "PostMessage client error", slog.Int("status_code", result.StatusCode), slog.String("body", result.Body))
			return response{Body: msg, StatusCode: result.StatusCode}, nil
		} else {
			return response{}, fmt.Errorf("unexpected status code from Slack API: code=%d, body=%s", result.StatusCode, result.Body)
		}
	case slack.PostMessageResultDomainFailure:
		if result.Reason == "channel_not_found" {
			msg := fmt.Sprintf("invite bot to the channel: channelName=%s, channelID=%s, reason=%s", result.ChannelName, result.ChannelID, result.Reason)
			return response{Body: msg, StatusCode: http.StatusBadRequest}, nil
		} else {
			slog.WarnContext(ctx, "PostMessage Slack API responses error response",
				slog.String("channel_id", res.ChannelID),
				slog.String("channel_name", res.ChannelName),
				slog.String("reason", result.Reason),
			)
			msg := fmt.Sprintf("Slack API responses error: reason=%s", result.Reason)
			return response{Body: msg, StatusCode: http.StatusBadRequest}, nil
		}
	// Check this in lint phase, not in runtime.
	default:
		return response{}, fmt.Errorf("unexpected PostMessageResult type: %v", result.Type)
	}
}

func processCmdShow(ctx context.Context, svc domain.Domain, cmdReq slack.SlashCommandRequest, req request) (response, error) {
	entries, err := svc.GetTokens(ctx, cmdReq.ChannelName)
	if err != nil {
		return response{}, err
	}
	tokenURLList := make([]string, 0, len(entries))
	for _, entry := range entries {
		hookURL := buildWebhookURL(entry.Token, cmdReq.ChannelName, req.RequestContext.DomainName)
		tokenURLList = append(tokenURLList, fmt.Sprintf("- %s (v%v, %s): %s", entry.Token, entry.Version, entry.CreatedAt.Format(time.RFC3339), hookURL))
	}
	listStr := strings.Join(tokenURLList, "\n")
	var msg string
	if len(listStr) == 0 {
		msg = "No token and url generated for this channel.\n"
	} else {
		msg = fmt.Sprintf("Available tokens for this channel:\n%s\n", listStr)
	}
	return buildResponse(msg)
}

func processCmdGenerate(ctx context.Context, svc domain.Domain, cmdReq slack.SlashCommandRequest, req request) (response, error) {
	res, err := svc.GenerateAndSaveToken(ctx, cmdReq.ChannelID, cmdReq.ChannelName)
	if err != nil {
		return response{}, err
	}
	if !res.IsGenerated {
		msg := fmt.Sprintf("Token already generated. To check generated token, use `%s`. To generate another token, use `%s`.\n", cmdShow, cmdRegenerate)
		return buildResponse(msg)
	}

	hookURL := buildWebhookURL(res.Token, cmdReq.ChannelName, req.RequestContext.DomainName)
	return buildResponse(fmt.Sprintf("Token generated: %s, %s", res.Token, hookURL))
}

func processCmdRegenerate(ctx context.Context, svc domain.Domain, cmdReq slack.SlashCommandRequest, req request) (response, error) {
	res, err := svc.RegenerateToken(ctx, cmdReq.ChannelID, cmdReq.ChannelName)
	if err != nil {
		return response{}, err
	}
	if res.NoTokenFound {
		return buildResponse(fmt.Sprintf("No token have been generated for this channel. Use `%s` to generate token.\n", cmdGenerate))
	}
	if res.TooManyToken {
		return buildResponse(fmt.Sprintf("Two tokens have been generated for this channel. Ensure old token is not used, then revoke it with `%s`.\n", cmdRevoke))
	}

	token := res.Token
	hookURL := buildWebhookURL(token, cmdReq.ChannelName, req.RequestContext.DomainName)
	return buildResponse(fmt.Sprintf("Another token generated for this chennel: %s", hookURL))
}

func processCmdRevoke(ctx context.Context, svc domain.Domain, cmdReq slack.SlashCommandRequest) (response, error) {
	res, err := svc.RevokeToken(ctx, cmdReq.ChannelName, cmdReq.Text)
	if err != nil {
		return response{}, err
	}
	if res.NotFound {
		msg := fmt.Sprintf("No pair found, check the token: channel_name=%s, token=%s\n", cmdReq.ChannelName, cmdReq.Text)
		return buildResponse(msg)
	}
	msg := fmt.Sprintf("Token revoked: channel_name=%s, token=%s\n", cmdReq.ChannelName, cmdReq.Text)
	return buildResponse(msg)
}

const slashCommandArgSize = 2

func processCmdRevokeRenamed(ctx context.Context, svc domain.Domain, cmdReq slack.SlashCommandRequest) (response, error) {
	args := strings.Fields(cmdReq.Text)
	if len(args) != slashCommandArgSize {
		return buildResponse("Invalid arguments for the slash command. This command expects `<channel name> <token>` as arguments.\n")
	}

	channelName, token := args[0], args[1]
	res, err := svc.RevokeRenamedToken(ctx, cmdReq.ChannelID, channelName, token)
	if err != nil {
		return response{}, err
	}
	if res.NotFound {
		msg := fmt.Sprintf("No pair found, check the token: channel_name=%s, token=%s\n", channelName, token)
		return buildResponse(msg)
	}
	if res.ChannelIDUnmatch {
		msg := fmt.Sprintf("Found pair but this channel does not own the token: channel_name=%s, token=%s, linked_channel_id=%s, channel_id=%s\n", channelName, token, res.LinkedChannelID, cmdReq.ChannelID)
		return buildResponse(msg)
	}
	msg := fmt.Sprintf("Token revoked: old_channel_name=%s, token=%s\n", channelName, token)
	return buildResponse(msg)
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

// Lagacy Slack webhook accepts both of "application/json" and "application/x-www-form-urlencoded" contents.
// Also accepts pure JSON request body regardless of content-type header field, so we must accept JSON payload,
// event when the content-type header filed value is "application/x-www-form-urlencoded". And if the content is
// encoded as form-data, the JSON payload will be at `payload` key.
//
// This behavior is not documented now. Some old clients needs this behavior.
func parseRequestBody(req request, body []byte) (map[string]interface{}, error) {
	contentType, ok := req.Headers["content-type"]
	if ok && contentType == "application/x-www-form-urlencoded" {
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

func buildWebhookURL(token string, channelName string, domainName string) string {
	if config.CustomDomainName != "" {
		domainName = config.CustomDomainName
	}
	return fmt.Sprintf("https://%s/p/%s/%s/", domainName, channelName, token)
}

// Marshal to json to use "in_channel" type response: https://api.slack.com/interactivity/slash-commands
func buildResponse(msg string) (response, error) {
	payload := map[string]string{
		"text":          msg,
		"response_type": "in_channel",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return response{}, errors.Wrap(err, "failed to marshal response")
	}
	return response{Body: string(body), StatusCode: http.StatusOK}, nil
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
