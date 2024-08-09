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

	"github.com/cockroachdb/errors"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/slack-go/slack"
)

const (
	slackAPIPostMessageEndpoint = "https://slack.com/api/chat.postMessage"
	statusCodeSuccess           = 200
)

type RetryConfig struct {
	Max     int
	WaitMin time.Duration
	WaitMax time.Duration
	// Includes TCP connection establishment, TLS handshake, send HTTP request, read response body.
	ReadTimeout time.Duration
}

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

// Pack all neccessary fields into one struct to work-around no enum.
type PostMessageResult struct {
	Type PostMessageResultType
	// Only when Type is ServerFailure
	StatusCode int
	Body       string
	// Only when Type is DomainFailure
	Reason      string
	ChannelID   string
	ChannelName string
}

type PostMessageResultType int

const (
	PostMessageResultOK PostMessageResultType = iota
	PostMessageResultServerTimeoutFailure
	PostMessageResultServerFailure
	PostMessageResultDomainFailure
)

type Kit struct {
	token      string
	httpClient *http.Client
}

func NewKit(token string, config RetryConfig) Kit {
	// Default config values: https://github.com/hashicorp/go-retryablehttp/blob/v0.7.5/client.go#L429-L439
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = config.Max
	retryClient.RetryWaitMin = config.WaitMin
	retryClient.RetryWaitMax = config.WaitMax
	retryClient.ErrorHandler = returnResponseHandler
	retryClient.HTTPClient.Timeout = config.ReadTimeout
	retryClient.Logger = slog.Default()

	httpClient := retryClient.StandardClient()
	return Kit{token: token, httpClient: httpClient}
}

// https://api.slack.com/methods/chat.postMessage#examples
type slackPostMessageResponse struct {
	Ok    bool   `json:"ok"`
	Error string `json:"error"`
	// Omit unnecessary fields
}

// https://api.slack.com/methods/chat.postMessage
func (s Kit) PostMessage(ctx context.Context, channelID string, channelName string, payload map[string]interface{}) (PostMessageResult, error) {
	payload["channel"] = channelID
	jsonStr, err := json.Marshal(payload)
	if err != nil {
		return PostMessageResult{}, errors.Wrap(err, "failed to marshal payload")
	}
	body := strings.NewReader(string(jsonStr))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, slackAPIPostMessageEndpoint, body)
	if err != nil {
		return PostMessageResult{}, errors.Wrap(err, "failed to create Slack API request")
	}
	req.Header.Add("authorization", fmt.Sprintf("Bearer %s", s.token))
	req.Header.Add("content-type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Timeout() {
			slog.InfoContext(ctx, "Slack API timeout", slog.String("error", err.Error()))
			return PostMessageResult{Type: PostMessageResultServerTimeoutFailure}, nil
		}
		// If err is not due to timeout, it's unexpected error.
		return PostMessageResult{}, errors.Wrap(err, "unexpected error from Slack API")
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return PostMessageResult{}, errors.Wrap(err, "failed to read Slack response body")
	}

	// After retrying, if response status code is not 200, it's server failure.
	if resp.StatusCode != statusCodeSuccess {
		return PostMessageResult{
			Type:       PostMessageResultServerFailure,
			StatusCode: resp.StatusCode,
			Body:       string(b),
		}, nil
	}

	res := slackPostMessageResponse{}
	if err := json.Unmarshal(b, &res); err != nil {
		return PostMessageResult{}, errors.Wrap(err, "failed to unmarshal Slack response")
	}

	if !res.Ok {
		return PostMessageResult{
			Type:        PostMessageResultDomainFailure,
			Reason:      res.Error,
			ChannelID:   channelID,
			ChannelName: channelName,
		}, nil
	}

	return PostMessageResult{Type: PostMessageResultOK}, nil
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
			var e *slack.RateLimitedError
			if errors.As(err, &e) && e.Retryable() {
				select {
				case <-ctx.Done():
					err = ctx.Err()
				case <-time.After(e.RetryAfter):
					err = nil
					continue
				}
			}
			return nil, errors.Wrap(err, "failed to get conversations")
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
		//
		// XXX: underlying func (*slack.Client).GetConversationInfoContext() returns error
		// as concrete struct type not as pointer. So non-pointer type sigunature is correct as of now.
		var serr slack.SlackErrorResponse
		if errors.As(err, &serr) && serr.Err == "channel_not_found" {
			return SlashCommandRequest{
				OriginalSlashCommandRequest: cmdReq,
				ChannelName:                 cmdReq.OriginalChannelName,
				Supported:                   false,
			}, nil
		}
		return SlashCommandRequest{}, err
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
		return nil, errors.Wrap(err, "failed to get conversation info")
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
		return OriginalSlashCommandRequest{}, errors.Wrapf(err, "failed to parse HTTP query: %s", body)
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

// To use go-retryable go-retryablehttp's error handler. Default error handling surpresses response of
// unexpected HTTP status. We want to propagate response to caller.
func returnResponseHandler(resp *http.Response, err error, numTries int) (*http.Response, error) {
	// Retry ends with unexpected HTTP status. This is normal situation so don't return error to caller.
	if err == nil {
		return resp, nil
	}
	// Else propagate error to caller with attempt information.
	return resp, errors.Wrapf(err, "giving up after %d attempt(s): %w", numTries)
}
