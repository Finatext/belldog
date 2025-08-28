package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/Finatext/ssmenv-go"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/caarlos0/env/v11"
	"github.com/cockroachdb/errors"
	"github.com/phsym/console-slog"

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
	slog.SetDefault(slog.New(console.NewHandler(os.Stderr, &console.HandlerOptions{Level: logLevel})))

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
	_, cleanup, err := telemetry.SetupOTel(ctx)
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

	e := handler.NewEchoHandler(config, &slackClient, &tokenSvc)
	e.Logger.Fatal(e.Start(":3000"))
	return nil
}
