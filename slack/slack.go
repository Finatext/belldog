package slack

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/slack-go/slack"
)

const (
	slackAPIPostMessageEndpoint = "https://slack.com/api/chat.postMessage"
	statusCodeSuccess           = 200
)

type SlashCommandRequest struct {
	OriginalSlashCommandRequest
	ChannelName string
	Supported   bool
}

type OriginalSlashCommandRequest struct {
	Command             string
	ChannelID           string
	OriginalChannelName string
	Text                string
}

// https://api.slack.com/methods/chat.postMessage#examples
type slackPostMessageResponse struct {
	Ok    bool
	Error string
	// Omit unnecessary fields
}

type Kit struct {
	token string
}

func NewKit(token string) Kit {
	return Kit{token: token}
}

// https://api.slack.com/methods/chat.postMessage
func (s Kit) PostMessage(ctx context.Context, channelID string, channelName string, payload map[string]interface{}) error {
	payload["channel"] = channelID
	jsonStr, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("json marshaling payload failed: %w", err)
	}
	body := strings.NewReader(string(jsonStr))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, slackAPIPostMessageEndpoint, body)
	if err != nil {
		return fmt.Errorf("http.NewRequestWithContext failed: %w", err)
	}
	req.Header.Add("authorization", fmt.Sprintf("Bearer %s", s.token))
	req.Header.Add("content-type", "application/json")

	// Default config values: https://github.com/hashicorp/go-retryablehttp/blob/v0.7.5/client.go#L429-L439
	retryClient := retryablehttp.NewClient()
	// Caller of Belldog expects to get response within about 5 seconds.
	retryClient.RetryMax = 3
	retryClient.RetryWaitMin = 500 * time.Millisecond
	retryClient.RetryWaitMax = 5 * time.Second
	// Default HTTP Client timeout covers from dialing (initiating TCP connection) to reading response body.
	// https://blog.cloudflare.com/the-complete-guide-to-golang-net-http-timeouts
	retryClient.HTTPClient.Timeout = 1 * time.Second
	retryClient.Logger = slog.Default()

	httpClient := retryClient.StandardClient()
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request to slack API failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != statusCodeSuccess {
		return fmt.Errorf("postMessage failed with status code=%v", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body in postMessage failed: %w", err)
	}
	res := slackPostMessageResponse{}
	if err := json.Unmarshal(b, &res); err != nil {
		return fmt.Errorf("unmarshalling Slack post messsage failed: %w", err)
	}

	if !res.Ok {
		if res.Error == "channel_not_found" {
			return fmt.Errorf("can not post messages in private channel in which the bot is not invited: channelName=%s, channelID=%s, reason=%s", channelName, channelID, res.Error)
		}
		return fmt.Errorf("slack PostMessage failed: channelName=%s, channelID=%s, reason=%s", channelName, channelID, res.Error)
	}

	return nil
}

const slackPaginationLimit = 200

// https://api.slack.com/docs/conversations-api
// https://api.slack.com/methods/conversations.list
//
// Required scopes:
//   - channels:read (public channels)
//   - groups:read (private channels)
func (s *Kit) GetAllChannels(ctx context.Context) ([]slack.Channel, error) {
	// XXX: If more actions are defined to Kit, move embed this to Kit struct value.
	client := slack.New(s.token)

	cursor := ""
	channels := []slack.Channel{}
	for {
		// https://api.slack.com/docs/pagination
		param := slack.GetConversationsParameters{
			Cursor:          cursor,
			ExcludeArchived: true,
			Limit:           slackPaginationLimit,
			Types:           []string{"public_channel", "private_channel"},
		}
		chans, next, err := client.GetConversationsContext(ctx, &param)
		if err != nil {
			return nil, fmt.Errorf("slack-go GetConversationsContext failed: %w", err)
		}

		channels = append(channels, chans...)

		cursor = next
		if cursor == "" {
			break
		}
	}

	return channels, nil
}

// GetFullCommandRequest to retrieve correct channel name for "private group"s. Before March 2021,
// a private channel was "private group" in Slack implementation. And slash command payloads which Slack
// sends to us, contains wrong channel name info for private groups. So we need retrieve the correct
// channel name via Slack API.
// https://api.slack.com/types/group
func (s *Kit) GetFullCommandRequest(ctx context.Context, body string) (SlashCommandRequest, error) {
	cmdReq, err := parseSlashCommandRequest(body)
	if err != nil {
		return SlashCommandRequest{}, err
	}
	channel, err := s.getChannelInfo(ctx, cmdReq.ChannelID)
	if err != nil {
		// Belldog doesn't have permissions to read the conversation info.
		if serr, ok := err.(slack.SlackErrorResponse); ok {
			if serr.Error() == "channel_not_found" {
				return SlashCommandRequest{
					OriginalSlashCommandRequest: cmdReq,
					ChannelName:                 cmdReq.OriginalChannelName,
					Supported:                   false,
				}, nil
			}
		}
		return SlashCommandRequest{}, fmt.Errorf("failed to call conversations.info API: %w", err)
	}
	return SlashCommandRequest{
		OriginalSlashCommandRequest: cmdReq,
		ChannelName:                 channel.Name,
		Supported:                   channel.IsChannel || channel.IsGroup,
	}, nil
}

// https://api.slack.com/methods/conversations.info
func (s *Kit) getChannelInfo(ctx context.Context, channelID string) (*slack.Channel, error) {
	client := slack.New(s.token)

	input := slack.GetConversationInfoInput{
		ChannelID:         channelID,
		IncludeLocale:     false,
		IncludeNumMembers: false,
	}
	channel, err := client.GetConversationInfoContext(ctx, &input)
	if err != nil {
		return nil, err
	}

	return channel, nil
}

const (
	currentVersionString = "v0"
	signaturePrefix      = "v0="
	base                 = 10
	bitSize              = 64
)

// https://api.slack.com/authentication/verifying-requests-from-slack
func VerifySlackRequest(ctx context.Context, key string, headers map[string]string, body string) bool {
	givenSig, ok := headers["x-slack-signature"]
	if !ok {
		slog.InfoContext(ctx, "missing x-slack-signature header")
		return false
	}

	timestampStr, ok := headers["x-slack-request-timestamp"]
	if !ok {
		slog.InfoContext(ctx, "missing x-slack-request-timestamp header")
		return false
	}
	timestamp, err := strconv.ParseInt(timestampStr, base, bitSize)
	if err != nil {
		slog.InfoContext(ctx, "failed to parse timestamp", slog.String("error", err.Error()), slog.String("timestamp", timestampStr))
		return false
	}
	now := time.Now().UTC().Unix()
	diff := abs(now - timestamp)
	if diff > 60*5 {
		slog.InfoContext(ctx, "expired timestamp given", slog.Int64("now", now), slog.Int64("timestamp", timestamp), slog.Int64("diff", diff))
		return false
	}

	baseString := fmt.Sprintf("%s:%d:%s", currentVersionString, timestamp, body)
	h := hmac.New(sha256.New, []byte(key))
	// This Write() never returns error. https://pkg.go.dev/hash#Hash
	h.Write([]byte(baseString))
	computed := hex.EncodeToString(h.Sum(nil))
	formatted := signaturePrefix + computed
	ret := hmac.Equal([]byte(givenSig), []byte(formatted))
	if !ret {
		slog.InfoContext(ctx, "verify failed", slog.String("givenSig", givenSig), slog.String("formatted", formatted))
		return false
	}
	return ret
}

func parseSlashCommandRequest(body string) (OriginalSlashCommandRequest, error) {
	query, err := url.ParseQuery(body)
	if err != nil {
		return OriginalSlashCommandRequest{}, fmt.Errorf("url.ParseQuery failed: %w", err)
	}

	req := OriginalSlashCommandRequest{
		Command:             query["command"][0],
		ChannelID:           query["channel_id"][0],
		OriginalChannelName: query["channel_name"][0],
		Text:                query["text"][0],
	}
	return req, nil
}

func abs(num int64) int64 {
	if num < 0 {
		return -num
	}
	if num == 0 {
		return 0 // return correctly abs(-0)
	}
	return num
}
