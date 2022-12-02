package main

import (
	"context"
	"fmt"
	"log"
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
	validateEnv()

	token, err := fetchParamter(paramSlackToken)
	if err != nil {
		log.Fatalf("fetchParamter failed: %s", err)
	}
	slackToken = token

	secret, err := fetchParamter(paramSlackSigningSecret)
	if err != nil {
		log.Fatalf("fetchParamter failed: %s", err)
	}
	slackSigningSecret = secret

	switch mode {
	case "proxy":
		lambda.Start(handleRequestWithCacheControl)
	case "batch":
		lambda.Start(handleCloudWatchEvent)
	default:
		log.Fatalf("Unknown `mode` env given: %s", mode)
	}
}

func validateEnv() {
	// customDomainName can be empty.

	if mode == "" {
		log.Fatalf("Missing `MODE` env")
	}
	if opsNotificationChannelName == "" {
		log.Fatalf("Missing `OPS_NOTIFICATION_CHANNEL_NAME` env")
	}
	if paramSlackSigningSecret == "" {
		log.Fatalf("Missing `PARAMETER_NAME_SLACK_SIGNING_SECRET` env")
	}
	if paramSlackToken == "" {
		log.Fatalf("Missing `PARAMETER_NAME_SLACK_TOKEN` env")
	}
	if tableName == "" {
		log.Fatalf("Missing `DDB_TABLE_NAME` env")
	}
}

func fetchParamter(paramName string) (string, error) {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to load SDK config: %w", err)
	}
	client := ssm.NewFromConfig(cfg)
	input := &ssm.GetParameterInput{
		Name:           &paramName,
		WithDecryption: true,
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
