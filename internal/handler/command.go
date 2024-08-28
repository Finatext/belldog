package handler

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/labstack/echo/v4"

	"github.com/Finatext/belldog/internal/slack"
)

const (
	cmdShow          = "/belldog-show"
	cmdGenerate      = "/belldog-generate"
	cmdRegenerate    = "/belldog-regenerate"
	cmdRevoke        = "/belldog-revoke"
	cmdRevokeRenamed = "/belldog-revoke-renamed"
)

func (h *ProxyHandler) SlashCommand(c echo.Context) error {
	ctx := c.Request().Context()
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return errors.Wrap(err, "failed to read request body")
	}
	if !slack.VerifySlackRequest(ctx, h.cfg.SlackSigningSecret, c.Request().Header, string(body)) {
		return c.String(http.StatusUnauthorized, "Invalid request signature.\n")
	}

	cmdReq, err := h.slackClient.GetFullCommandRequest(ctx, string(body))
	if err != nil {
		return err
	}
	logCommandRequest(ctx, cmdReq)
	if !cmdReq.Supported {
		return inChannelResponse(c, "Belldog only supports public/private channels. If this is a private channel, invite Belldog.\n")
	}

	// https://api.slack.com/interactivity/slash-commands#creating_commands
	switch cmdReq.Command {
	case cmdShow:
		return h.processCmdShow(c, cmdReq)
	case cmdGenerate:
		return h.processCmdGenerate(c, cmdReq)
	case cmdRegenerate:
		return h.processCmdRegenerate(c, cmdReq)
	case cmdRevoke:
		return h.processCmdRevoke(c, cmdReq)
	case cmdRevokeRenamed:
		return h.processCmdRevokeRenamed(c, cmdReq)
	default:
		slog.InfoContext(ctx, "missing command given", slog.String("command", cmdReq.Command))
		return inChannelResponse(c, "Missing command.\n")
	}
}

func (h *ProxyHandler) processCmdShow(c echo.Context, cmdReq slack.SlashCommandRequest) error {
	ctx := c.Request().Context()
	entries, err := h.tokenSvc.GetTokens(ctx, cmdReq.ChannelName)
	if err != nil {
		return err
	}
	tokenURLList := make([]string, 0, len(entries))
	for _, entry := range entries {
		hookURL := h.buildWebhookURL(entry.Token, cmdReq.ChannelName, c.Request().Host)
		tokenURLList = append(tokenURLList, fmt.Sprintf("- %s (v%v, %s): %s", entry.Token, entry.Version, entry.CreatedAt.Format(time.RFC3339), hookURL))
	}
	listStr := strings.Join(tokenURLList, "\n")
	var msg string
	if len(listStr) == 0 {
		msg = "No token and url generated for this channel.\n"
	} else {
		msg = fmt.Sprintf("Available tokens for this channel:\n%s\n", listStr)
	}
	return inChannelResponse(c, msg)
}

func (h *ProxyHandler) processCmdGenerate(c echo.Context, cmdReq slack.SlashCommandRequest) error {
	ctx := c.Request().Context()
	res, err := h.tokenSvc.GenerateAndSaveToken(ctx, cmdReq.ChannelID, cmdReq.ChannelName)
	if err != nil {
		return err
	}
	if !res.IsGenerated {
		msg := fmt.Sprintf("Token already generated. To check generated token, use `%s`. To generate another token, use `%s`.\n", cmdShow, cmdRegenerate)
		return inChannelResponse(c, msg)
	}

	hookURL := h.buildWebhookURL(res.Token, cmdReq.ChannelName, c.Request().Host)
	return inChannelResponse(c, fmt.Sprintf("Token generated: %s, %s", res.Token, hookURL))
}

func (h *ProxyHandler) processCmdRegenerate(c echo.Context, cmdReq slack.SlashCommandRequest) error {
	ctx := c.Request().Context()
	res, err := h.tokenSvc.RegenerateToken(ctx, cmdReq.ChannelID, cmdReq.ChannelName)
	if err != nil {
		return err
	}
	if res.NoTokenFound {
		return inChannelResponse(c, fmt.Sprintf("No token have been generated for this channel. Use `%s` to generate token.\n", cmdGenerate))
	}
	if res.TooManyToken {
		return inChannelResponse(c, fmt.Sprintf("Two tokens have been generated for this channel. Ensure old token is not used, then revoke it with `%s`.\n", cmdRevoke))
	}

	token := res.Token
	hookURL := h.buildWebhookURL(token, cmdReq.ChannelName, c.Request().Host)
	return inChannelResponse(c, fmt.Sprintf("Another token generated for this chennel: %s", hookURL))
}

func (h *ProxyHandler) processCmdRevoke(c echo.Context, cmdReq slack.SlashCommandRequest) error {
	ctx := c.Request().Context()
	res, err := h.tokenSvc.RevokeToken(ctx, cmdReq.ChannelName, cmdReq.Text)
	if err != nil {
		return err
	}
	if res.NotFound {
		msg := fmt.Sprintf("No pair found, check the token: channel_name=%s, token=%s\n", cmdReq.ChannelName, cmdReq.Text)
		return inChannelResponse(c, msg)
	}
	msg := fmt.Sprintf("Token revoked: channel_name=%s, token=%s\n", cmdReq.ChannelName, cmdReq.Text)
	return inChannelResponse(c, msg)
}

const slashCommandArgSize = 2

func (h *ProxyHandler) processCmdRevokeRenamed(c echo.Context, cmdReq slack.SlashCommandRequest) error {
	ctx := c.Request().Context()
	args := strings.Fields(cmdReq.Text)
	if len(args) != slashCommandArgSize {
		return inChannelResponse(c, "Invalid arguments for the slash command. This command expects `<channel name> <token>` as arguments.\n")
	}

	channelName, token := args[0], args[1]
	res, err := h.tokenSvc.RevokeRenamedToken(ctx, cmdReq.ChannelID, channelName, token)
	if err != nil {
		return err
	}
	if res.NotFound {
		msg := fmt.Sprintf("No pair found, check the token: channel_name=%s, token=%s\n", channelName, token)
		return inChannelResponse(c, msg)
	}
	if res.ChannelIDUnmatch {
		msg := fmt.Sprintf("Found pair but this channel does not own the token: channel_name=%s, token=%s, linked_channel_id=%s, channel_id=%s\n", channelName, token, res.LinkedChannelID, cmdReq.ChannelID)
		return inChannelResponse(c, msg)
	}
	msg := fmt.Sprintf("Token revoked: old_channel_name=%s, token=%s\n", channelName, token)
	return inChannelResponse(c, msg)
}

func (h *ProxyHandler) buildWebhookURL(token string, channelName string, domainName string) string {
	if h.cfg.CustomDomainName != "" {
		domainName = h.cfg.CustomDomainName
	}
	return fmt.Sprintf("https://%s/p/%s/%s/", domainName, channelName, token)
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

// Marshal to json to use "in_channel" type response: https://api.slack.com/interactivity/slash-commands
func inChannelResponse(c echo.Context, msg string) error {
	payload := map[string]string{
		"text":          msg,
		"response_type": "in_channel",
	}
	return c.JSON(http.StatusOK, payload)
}
