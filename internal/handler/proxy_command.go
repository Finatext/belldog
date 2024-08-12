package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Finatext/belldog/internal/slack"
)

const (
	cmdShow          = "/belldog-show"
	cmdGenerate      = "/belldog-generate"
	cmdRegenerate    = "/belldog-regenerate"
	cmdRevoke        = "/belldog-revoke"
	cmdRevokeRenamed = "/belldog-revoke-renamed"
)

func (h *ProxyHandler) handleSlashCommand(ctx context.Context, req Request, body []byte) (Response, error) {
	if !slack.VerifySlackRequest(ctx, h.cfg.SlackSigningSecret, req.Headers, string(body)) {
		return Response{Body: "Bad request.\n", StatusCode: http.StatusBadRequest}, nil
	}

	cmdReq, err := h.slackClient.GetFullCommandRequest(ctx, string(body))
	if err != nil {
		return Response{}, err
	}
	logCommandRequest(ctx, cmdReq)
	if !cmdReq.Supported {
		return buildResponse("Belldog only supports public/private channels. If this is a private channel, invite Belldog.\n")
	}

	// https://api.slack.com/interactivity/slash-commands#creating_commands
	switch cmdReq.Command {
	case cmdShow:
		return h.processCmdShow(ctx, cmdReq, req)
	case cmdGenerate:
		return h.processCmdGenerate(ctx, cmdReq, req)
	case cmdRegenerate:
		return h.processCmdRegenerate(ctx, cmdReq, req)
	case cmdRevoke:
		return h.processCmdRevoke(ctx, cmdReq)
	case cmdRevokeRenamed:
		return h.processCmdRevokeRenamed(ctx, cmdReq)
	default:
		slog.InfoContext(ctx, "missing command given", slog.String("command", cmdReq.Command))
		return buildResponse("Missing command.\n")
	}
}

func (h *ProxyHandler) processCmdShow(ctx context.Context, cmdReq slack.SlashCommandRequest, req Request) (Response, error) {
	entries, err := h.tokenSvc.GetTokens(ctx, cmdReq.ChannelName)
	if err != nil {
		return Response{}, err
	}
	tokenURLList := make([]string, 0, len(entries))
	for _, entry := range entries {
		hookURL := h.buildWebhookURL(entry.Token, cmdReq.ChannelName, req.RequestContext.DomainName)
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

func (h *ProxyHandler) processCmdGenerate(ctx context.Context, cmdReq slack.SlashCommandRequest, req Request) (Response, error) {
	res, err := h.tokenSvc.GenerateAndSaveToken(ctx, cmdReq.ChannelID, cmdReq.ChannelName)
	if err != nil {
		return Response{}, err
	}
	if !res.IsGenerated {
		msg := fmt.Sprintf("Token already generated. To check generated token, use `%s`. To generate another token, use `%s`.\n", cmdShow, cmdRegenerate)
		return buildResponse(msg)
	}

	hookURL := h.buildWebhookURL(res.Token, cmdReq.ChannelName, req.RequestContext.DomainName)
	return buildResponse(fmt.Sprintf("Token generated: %s, %s", res.Token, hookURL))
}

func (h *ProxyHandler) processCmdRegenerate(ctx context.Context, cmdReq slack.SlashCommandRequest, req Request) (Response, error) {
	res, err := h.tokenSvc.RegenerateToken(ctx, cmdReq.ChannelID, cmdReq.ChannelName)
	if err != nil {
		return Response{}, err
	}
	if res.NoTokenFound {
		return buildResponse(fmt.Sprintf("No token have been generated for this channel. Use `%s` to generate token.\n", cmdGenerate))
	}
	if res.TooManyToken {
		return buildResponse(fmt.Sprintf("Two tokens have been generated for this channel. Ensure old token is not used, then revoke it with `%s`.\n", cmdRevoke))
	}

	token := res.Token
	hookURL := h.buildWebhookURL(token, cmdReq.ChannelName, req.RequestContext.DomainName)
	return buildResponse(fmt.Sprintf("Another token generated for this chennel: %s", hookURL))
}

func (h *ProxyHandler) processCmdRevoke(ctx context.Context, cmdReq slack.SlashCommandRequest) (Response, error) {
	res, err := h.tokenSvc.RevokeToken(ctx, cmdReq.ChannelName, cmdReq.Text)
	if err != nil {
		return Response{}, err
	}
	if res.NotFound {
		msg := fmt.Sprintf("No pair found, check the token: channel_name=%s, token=%s\n", cmdReq.ChannelName, cmdReq.Text)
		return buildResponse(msg)
	}
	msg := fmt.Sprintf("Token revoked: channel_name=%s, token=%s\n", cmdReq.ChannelName, cmdReq.Text)
	return buildResponse(msg)
}

const slashCommandArgSize = 2

func (h *ProxyHandler) processCmdRevokeRenamed(ctx context.Context, cmdReq slack.SlashCommandRequest) (Response, error) {
	args := strings.Fields(cmdReq.Text)
	if len(args) != slashCommandArgSize {
		return buildResponse("Invalid arguments for the slash command. This command expects `<channel name> <token>` as arguments.\n")
	}

	channelName, token := args[0], args[1]
	res, err := h.tokenSvc.RevokeRenamedToken(ctx, cmdReq.ChannelID, channelName, token)
	if err != nil {
		return Response{}, err
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

func (h *ProxyHandler) buildWebhookURL(token string, channelName string, domainName string) string {
	if h.cfg.CustomDomainName != "" {
		domainName = h.cfg.CustomDomainName
	}
	return fmt.Sprintf("https://%s/p/%s/%s/", domainName, channelName, token)
}
