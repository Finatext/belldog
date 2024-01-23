package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

var (
	customDomainName           = os.Getenv("CUSTOM_DOMAIN_NAME")
	mode                       = os.Getenv("MODE")
	opsNotificationChannelName = os.Getenv("OPS_NOTIFICATION_CHANNEL_NAME")
	paramSlackSigningSecret    = os.Getenv("PARAMETER_NAME_SLACK_SIGNING_SECRET")
	paramSlackToken            = os.Getenv("PARAMETER_NAME_SLACK_TOKEN")
	slackSigningSecret         = "" // Setup later with SSM parameter store.
	slackToken                 = "" // Setup later with SSM parameter store.
	tableName                  = os.Getenv("DDB_TABLE_NAME")
)

func main() {
	ctx := context.Background()
	// TODO: Set log level from env.
	logLevel := new(slog.LevelVar)
	ops := slog.HandlerOptions{
		AddSource: true,
		Level:     logLevel,
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &ops))
	slog.SetDefault(logger)

	validateEnv()

	token, err := fetchParamter(ctx, paramSlackToken)
	if err != nil {
		slog.Error("fetchParamter failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	slackToken = token

	secret, err := fetchParamter(ctx, paramSlackSigningSecret)
	if err != nil {
		slog.Error("fetchParamter failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	slackSigningSecret = secret

	switch mode {
	case "proxy":
		lambda.Start(handleRequestWithCacheControl)
	case "batch":
		lambda.Start(handleCloudWatchEvent)
	default:
		slog.Error("Unknown `mode` env given", slog.String("mode", mode))
		os.Exit(1)
	}
}

func validateEnv() {
	// customDomainName can be empty.

	if mode == "" {
		slog.Error("Missing `MODE` env")
		os.Exit(1)
	}
	if opsNotificationChannelName == "" {
		slog.Error("Missing `OPS_NOTIFICATION_CHANNEL_NAME` env")
		os.Exit(1)
	}
	if paramSlackSigningSecret == "" {
		slog.Error("Missing `PARAMETER_NAME_SLACK_SIGNING_SECRET` env")
		os.Exit(1)
	}
	if paramSlackToken == "" {
		slog.Error("Missing `PARAMETER_NAME_SLACK_TOKEN` env")
		os.Exit(1)
	}
	if tableName == "" {
		slog.Error("Missing `DDB_TABLE_NAME` env")
		os.Exit(1)
	}
}

func fetchParamter(ctx context.Context, paramName string) (string, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
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
