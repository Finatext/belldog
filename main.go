package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/Finatext/belldog/slack"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	EnvConfig
	SlackSigningSecret string
	SlackToken         string
}

// This doesn't follow go naming convention because it's used in envconfig.
//
// RetryReadTimeoutDuration: This will set to HTTP client's timeout.
// Default HTTP client timeout covers from dialing (initiating TCP connection) to reading response body.
// https://blog.cloudflare.com/the-complete-guide-to-golang-net-http-timeouts
type EnvConfig struct {
	CustomDomainName                string        `split_words:"true"`
	DdbTableName                    string        `split_words:"true" required:"true"`
	LogLevel                        slog.Level    `split_words:"true" default:"info"`
	Mode                            string        `split_words:"true" required:"true"`
	OpsNotificationChannelName      string        `split_words:"true" required:"true"`
	ParameterNameSlackSigningSecret string        `split_words:"true" required:"true"`
	ParameterNameSlackToken         string        `split_words:"true" required:"true"`
	RetryMax                        int           `split_words:"true" default:"3"`
	RetryReadTimeoutDuration        time.Duration `split_words:"true" default:"5s"`
	RetryWaitMaxDuration            time.Duration `split_words:"true" default:"10s"`
	RetryWaitMinDuration            time.Duration `split_words:"true" default:"1s"`
}

var (
	config           Config
	slackRetryConfig slack.RetryConfig
)

func main() {
	ctx := context.Background()
	logLevel := new(slog.LevelVar)
	ops := slog.HandlerOptions{
		AddSource: true,
		Level:     logLevel,
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &ops))
	slog.SetDefault(logger)

	var c EnvConfig
	err := envconfig.Process("", &c)
	if err != nil {
		slog.Error("parse env failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	config.EnvConfig = c
	slackRetryConfig = toSlackRetryConfig(config)

	logLevel.Set(config.LogLevel)

	token, err := fetchParamter(ctx, config.ParameterNameSlackToken)
	if err != nil {
		slog.Error("fetchParamter failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	config.SlackToken = token

	secret, err := fetchParamter(ctx, config.ParameterNameSlackSigningSecret)
	if err != nil {
		slog.Error("fetchParamter failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	config.SlackSigningSecret = secret

	switch config.Mode {
	case "proxy":
		lambda.Start(handleRequestWithCacheControl)
	case "batch":
		lambda.Start(handleCloudWatchEvent)
	default:
		slog.Error("Unknown `mode` env given", slog.String("mode", config.Mode))
		os.Exit(1)
	}
}

func fetchParamter(ctx context.Context, paramName string) (string, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to load SDK config: %w", err)
	}
	client := ssm.NewFromConfig(cfg)
	t := true
	input := &ssm.GetParameterInput{
		Name:           &paramName,
		WithDecryption: &t,
	}
	res, err := client.GetParameter(ctx, input)
	if err != nil {
		return "", fmt.Errorf("unable to fetch SSM parameter: %w", err)
	}

	value := *res.Parameter.Value
	if value == "" {
		return "", fmt.Errorf("empty SSM parameter value found: %s", paramName)
	}
	return value, nil
}

func toSlackRetryConfig(config Config) slack.RetryConfig {
	return slack.RetryConfig{
		Max:         config.RetryMax,
		WaitMin:     config.RetryWaitMinDuration,
		WaitMax:     config.RetryWaitMaxDuration,
		ReadTimeout: config.RetryReadTimeoutDuration,
	}
}
