# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Belldog is a Slack webhook proxy that clarifies webhook URLs to indicate target channel names and simplifies webhook management. It proxies webhook requests to Slack channels, handles token generation/revocation via slash commands, and detects channel renamings.

## Architecture

### Deployment Modes

Belldog runs in two distinct Lambda modes, controlled by the `MODE` environment variable:

- **proxy mode**: Handles Slack slash commands and proxies webhook requests
- **batch mode**: Detects token migrations and channel renamings, notifies users and ops

Both modes share core logic from `/internal` but have different entry points in `/cmd`.

### Entry Points

- `cmd/lambda/lambda.go`: Lambda deployment (both proxy and batch modes)
- `cmd/server/server.go`: Local development HTTP server (runs on :3000)
- `cmd/oneshot/oneshot.go`: One-time batch execution for testing

### Package Structure

- `internal/appconfig/`: Environment configuration using `caarlos0/env`
- `internal/handler/`: HTTP handlers for webhooks, slash commands, and batch processing
- `internal/service/`: Token management business logic (generate, verify, revoke)
- `internal/storage/`: DynamoDB persistence layer
- `internal/slack/`: Slack API client wrapper
- `internal/telemetry/`: OpenTelemetry setup for metrics
- `internal/middlewares/`: Echo middleware (request validation, etc.)

### Key Design Patterns

**Interface Definition at Use Site**: Interfaces are defined where they're used (e.g., `handler/handler.go` defines `slackClient`, `storageDDB`, `tokenService` interfaces), not where implementations live.

**Wrapper Pattern for External Types**: The codebase wraps external library types rather than using them directly. For example, `internal/slack/slack.go` wraps `github.com/slack-go/slack` client to provide domain-specific methods.

**Dependency Injection via Struct Fields**: Services hold dependencies as explicit struct fields (e.g., `TokenService` holds `ddb` field). Dependencies are passed at construction time via factory functions like `NewTokenService()`.

## Development Commands

### Build and Run

```bash
# Build all packages
go build -v ./...

# Run local development server (proxy mode)
go run ./cmd/server

# Run batch job locally
go run ./cmd/oneshot
```

### Testing

```bash
# Run all tests
go test -v ./...

# Run tests for a specific package
go test -v ./internal/service

# Run a specific test
go test -v ./internal/service -run TestTokenService_GenerateAndSaveToken
```

### Linting

```bash
# Run golangci-lint
golangci-lint run

# Run with auto-fix
golangci-lint run --fix
```

The project uses extensive linters configured in `.golangci.yaml`, including wrapcheck for error wrapping.

## Environment Variables

Required configuration (loaded via `caarlos0/env` into `appconfig.Config`):

- `MODE`: "proxy" or "batch"
- `DDB_TABLE_NAME`: DynamoDB table name
- `SLACK_TOKEN`: Slack Bot User OAuth Token
- `SLACK_SIGNING_SECRET`: Slack request signature verification
- `OPS_NOTIFICATION_CHANNEL_NAME`: Channel for operational notifications

Optional:
- `CUSTOM_DOMAIN_NAME`: Custom domain for webhook URLs
- `GO_LOG`: Log level (default: "info")
- `NO_OTEL`: Disable OpenTelemetry (default: false)
- `RETRY_MAX`, `RETRY_READ_TIMEOUT_DURATION`, etc.: HTTP client retry configuration

**SSM Parameter Store Integration**: Values prefixed with `ssm://` are automatically fetched from AWS SSM Parameter Store using `github.com/Finatext/ssmenv-go`.

## Error Handling

Uses `github.com/cockroachdb/errors` throughout:

- Wrap errors with context: `errors.Wrap(err, "description")`
- Create new errors: `errors.Newf("format %s", arg)`
- Error wrapping is enforced by wrapcheck linter (configured to ignore current package and test packages)

## Token Management Flow

1. User runs `/belldog-generate` in a Slack channel
2. Handler calls `tokenService.GenerateAndSaveToken()` which generates a 16-byte random hex token
3. Token is stored in DynamoDB with `channel_name` (partition key) and `version` (sort key)
4. Webhook URL is returned: `https://<domain>/p/<channel_name>/<token>/`
5. External services POST to this URL, proxy verifies token and forwards to Slack

**Token Migration**: Users can generate a second token with `/belldog-regenerate` (max 2 tokens per channel), allowing safe token rotation without downtime.

**Channel Renaming**: Batch job detects renamed channels and notifies ops. Old tokens remain functional (linked by channel ID) until explicitly revoked with `/belldog-revoke-renamed`.

## Testing Strategy

- Unit tests use interface mocking (e.g., `handler_test.go` mocks `slackClient` and `tokenService`)
- Tests are co-located with implementation files (e.g., `token.go` → `token_test.go`)
- Use `github.com/stretchr/testify` for assertions

## OpenTelemetry

Metrics are configured in `internal/telemetry/` with OTLP HTTP exporter. Local testing requires running an OTel collector (config in `otel-collector.yaml`).

Set `NO_OTEL=true` to disable telemetry during local development.
