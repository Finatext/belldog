package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

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
type EnvConfig struct {
	CustomDomainName                string
	DdbTableName                    string `required:"true"`
	Mode                            string `required:"true"`
	OpsNotificationChannelName      string `required:"true"`
	ParameterNameSlackSigningSecret string `required:"true"`
	ParameterNameSlackToken         string `required:"true"`
}

var (
	config Config
)

func main() {
	ctx := context.Background()
	// TODO: Set log level from env. Use UnmarshalText to parse string to Level.
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
