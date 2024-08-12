package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/cockroachdb/errors"
	"github.com/sethvargo/go-envconfig"

	"github.com/Finatext/belldog/internal/ssmenv"
	"github.com/Finatext/belldog/slack"
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
	CustomDomainName           string     `env:"CUSTOM_DOMAIN_NAME"`
	DdbTableName               string     `env:"DDB_TABLE_NAME, required"`
	LogLevel                   slog.Level `env:"LOG_LEVEL, default=info"`
	Mode                       string     `env:"MODE, required"`
	OpsNotificationChannelName string     `env:"OPS_NOTIFICATION_CHANNEL_NAME, required"`
	// For backward compatibility
	ParameterNameSlackSigningSecret string `env:"PARAMETER_NAME_SLACK_SIGNING_SECRET"`
	SlackSigningSecret              string `env:"SLACK_SIGNING_SECRET"`
	// For backward compatibility
	ParameterNameSlackToken  string        `env:"PARAMETER_NAME_SLACK_TOKEN, required"`
	SlackToken               string        `env:"SLACK_TOKEN"`
	RetryMax                 int           `env:"RETRY_MAX, default=3"`
	RetryReadTimeoutDuration time.Duration `env:"RETRY_READ_TIMEOUT_DURATION, default=5s"`
	RetryWaitMaxDuration     time.Duration `env:"RETRY_WAIT_MAX_DURATION, default=10s"`
	RetryWaitMinDuration     time.Duration `env:"RETRY_WAIT_MIN_DURATION, default=1s"`
}

var (
	config           Config
	slackRetryConfig slack.RetryConfig
)

func main() {
	if err := doMain(); err != nil {
		slog.Error("failed to run", slog.String("error", fmt.Sprintf("%+v", err)))
		os.Exit(1)
	}
}

func doMain() error {
	ctx := context.Background()
	logLevel := new(slog.LevelVar)
	ops := slog.HandlerOptions{
		AddSource: true,
		Level:     logLevel,
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &ops))
	slog.SetDefault(logger)

	var c EnvConfig
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to load AWS config")
	}
	ssmClient := ssm.NewFromConfig(awsConfig)
	replacedEnv, err := ssmenv.ReplacedEnv(ctx, ssmClient, os.Environ())
	if err != nil {
		return err
	}
	envconfigConfig := envconfig.Config{
		Target:   &c,
		Lookuper: envconfig.MapLookuper(replacedEnv),
	}
	if err := envconfig.Process(ctx, envconfigConfig); err != nil {
		return errors.Wrap(err, "failed to process envconfig")
	}
	config.EnvConfig = c
	slackRetryConfig = toSlackRetryConfig(config)

	logLevel.Set(config.LogLevel)

	if config.EnvConfig.ParameterNameSlackToken != "" {
		slog.Warn("PARAMETER_NAME_SLACK_TOKEN is deprecated, use SLACK_TOKEN instead")
		token, err := fetchParamter(ctx, config.ParameterNameSlackToken)
		if err != nil {
			return err
		}
		// For backward compatibility
		config.EnvConfig.SlackToken = token
		config.SlackToken = token
	}
	if config.EnvConfig.ParameterNameSlackSigningSecret == "" {
		slog.Warn("PARAMETER_NAME_SLACK_SIGNING_SECRET is deprecated, use SLACK_SIGNING_SECRET instead")
		secret, err := fetchParamter(ctx, config.ParameterNameSlackSigningSecret)
		if err != nil {
			return err
		}
		// For backward compatibility
		config.EnvConfig.SlackSigningSecret = secret
		config.SlackSigningSecret = secret
	}

	switch config.Mode {
	case "proxy":
		lambda.Start(handleRequestWithCacheControl)
	case "batch":
		lambda.Start(handleCloudWatchEvent)
	default:
		return errors.Newf("Unknown `mode` env given: %s", config.Mode)
	}
	return nil
}

func fetchParamter(ctx context.Context, paramName string) (string, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to load AWS config")
	}
	client := ssm.NewFromConfig(cfg)
	t := true
	input := &ssm.GetParameterInput{
		Name:           &paramName,
		WithDecryption: &t,
	}
	res, err := client.GetParameter(ctx, input)
	if err != nil {
		return "", errors.Wrap(err, "failed to get SSM parameter")
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
