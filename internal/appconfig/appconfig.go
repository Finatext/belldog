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
	CustomDomainName           string        `env:"CUSTOM_DOMAIN_NAME"`
	DdbTableName               string        `env:"DDB_TABLE_NAME,required"`
	GoLog                      slog.Level    `env:"GO_LOG" envDefault:"info"`
	Mode                       string        `env:"MODE,required"`
	OpsNotificationChannelName string        `env:"OPS_NOTIFICATION_CHANNEL_NAME,required"`
	SlackSigningSecret         string        `env:"SLACK_SIGNING_SECRET,required"`
	SlackToken                 string        `env:"SLACK_TOKEN,required"`
	RetryMax                   int           `env:"RETRY_MAX" envDefault:"3"`
	RetryReadTimeoutDuration   time.Duration `env:"RETRY_READ_TIMEOUT_DURATION" envDefault:"5s"`
	RetryWaitMaxDuration       time.Duration `env:"RETRY_WAIT_MAX_DURATION" envDefault:"10s"`
	RetryWaitMinDuration       time.Duration `env:"RETRY_WAIT_MIN_DURATION" envDefault:"1s"`
	NoOTel                     bool          `env:"NO_OTEL" envDefault:"false"`
}
