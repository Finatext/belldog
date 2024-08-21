package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/cockroachdb/errors"
	"github.com/sethvargo/go-envconfig"

	"github.com/Finatext/belldog/internal/appconfig"
	"github.com/Finatext/belldog/internal/handler"
	"github.com/Finatext/belldog/internal/lambdaurl"
	"github.com/Finatext/belldog/internal/service"
	"github.com/Finatext/belldog/internal/slack"
	"github.com/Finatext/belldog/internal/ssmenv"
	"github.com/Finatext/belldog/internal/storage"
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

	awsConfig, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to load AWS config")
	}
	ssmClient := ssm.NewFromConfig(awsConfig)
	replacedEnv, err := ssmenv.ReplacedEnv(ctx, ssmClient, os.Environ())
	if err != nil {
		return err
	}
	var config appconfig.Config
	envconfigConfig := envconfig.Config{
		Target:   &config,
		Lookuper: envconfig.MapLookuper(replacedEnv),
	}
	if err := envconfig.ProcessWith(ctx, &envconfigConfig); err != nil {
		return errors.Wrap(err, "failed to process envconfig")
	}

	logLevel.Set(config.GoLog)

	if config.ParameterNameSlackToken != "" {
		slog.Warn("PARAMETER_NAME_SLACK_TOKEN is deprecated, use SLACK_TOKEN instead")
		token, err := fetchParamter(ctx, config.ParameterNameSlackToken)
		if err != nil {
			return err
		}
		config.SlackToken = token
	}
	if config.ParameterNameSlackSigningSecret != "" {
		slog.Warn("PARAMETER_NAME_SLACK_SIGNING_SECRET is deprecated, use SLACK_SIGNING_SECRET instead")
		secret, err := fetchParamter(ctx, config.ParameterNameSlackSigningSecret)
		if err != nil {
			return err
		}
		config.SlackSigningSecret = secret
	}

	slackClient := slack.NewClient(config)
	ddb, err := storage.NewDDB(ctx, awsConfig, config.DdbTableName)
	if err != nil {
		return err
	}
	tokenSvc := service.NewTokenService(&ddb)

	switch config.Mode {
	case "proxy":
		e := handler.NewEchoHandler(config, &slackClient, &tokenSvc)
		lambda.Start(lambdaurl.Wrap(e))
	case "batch":
		h := handler.NewBatchHandler(config, &slackClient, &ddb)
		lambda.Start(h.HandleCloudWatchEvent)
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
		return "", errors.Newf("empty SSM parameter value found: %s", paramName)
	}
	return value, nil
}
