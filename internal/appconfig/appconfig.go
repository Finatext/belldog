package appconfig

import (
	"log/slog"
	"time"
)

// This doesn't follow go naming convention because it's used in envconfig.
//
// RetryReadTimeoutDuration: This will set to HTTP client's timeout.
// Default HTTP client timeout covers from dialing (initiating TCP connection) to reading response body.
// https://blog.cloudflare.com/the-complete-guide-to-golang-net-http-timeouts
type Config struct {
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
