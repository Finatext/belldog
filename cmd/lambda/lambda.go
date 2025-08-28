package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/Finatext/lambdaurl-buffered"
	"github.com/Finatext/ssmenv-go"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/caarlos0/env/v11"
	"github.com/cockroachdb/errors"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-lambda-go/otellambda"

	"github.com/Finatext/belldog/internal/appconfig"
	"github.com/Finatext/belldog/internal/handler"
	"github.com/Finatext/belldog/internal/service"
	"github.com/Finatext/belldog/internal/slack"
	"github.com/Finatext/belldog/internal/storage"
	"github.com/Finatext/belldog/internal/telemetry"
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
		AddSource: false,
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
		return errors.Wrap(err, "failed to replace env")
	}
	config, err := env.ParseAsWithOptions[appconfig.Config](env.Options{
		Environment: replacedEnv,
	})
	if err != nil {
		return errors.Wrap(err, "failed to process config from env")
	}

	logLevel.Set(config.GoLog)

	flusher, cleanup, err := telemetry.SetupOTel(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to setup OpenTelemetry")
	}
	defer func() {
		if err := cleanup(); err != nil {
			slog.Error("failed to cleanup telemetry", slog.String("error", fmt.Sprintf("%+v", err)))
		}
	}()

	slackClient := slack.NewClient(config)
	ddb, err := storage.NewDDB(ctx, awsConfig, config.DdbTableName)
	if err != nil {
		return err
	}
	tokenSvc := service.NewTokenService(&ddb)

	switch config.Mode {
	case "proxy":
		e := handler.NewEchoHandler(config, &slackClient, &tokenSvc)
		lambda.Start(otellambda.InstrumentHandler(lambdaurl.Wrap(e), otellambda.WithFlusher(flusher)))
	case "batch":
		h := handler.NewBatchHandler(config, &slackClient, &ddb)
		lambda.Start(otellambda.InstrumentHandler(h.HandleCloudWatchEvent, otellambda.WithFlusher(flusher)))
	default:
		return errors.Newf("Unknown `mode` env given: %s", config.Mode)
	}
	return nil
}
