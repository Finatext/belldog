package middlewares

import (
	"fmt"
	"log/slog"

	"github.com/cockroachdb/errors"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func RequestLogger() echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogError:        true,
		HandleError:     true,
		LogMethod:       true,
		LogURIPath:      true,
		LogHost:         true,
		LogRequestID:    true,
		LogLatency:      true,
		LogResponseSize: true,
		LogUserAgent:    true,
		LogRemoteIP:     true,
		LogStatus:       true,
		LogValuesFunc:   requestLoggerFunc,
	})
}

func requestLoggerFunc(c echo.Context, v middleware.RequestLoggerValues) error {
	if v.Error != nil {
		var httpError *echo.HTTPError
		// Log only non-HTTP errors, so the "not operator" `!` is used here.
		if !errors.As(v.Error, &httpError) {
			slog.ErrorContext(c.Request().Context(), "failed to handle request", slog.String("err", fmt.Sprintf("%+v", v.Error)))
		}
	}

	slog.LogAttrs(c.Request().Context(), slog.LevelInfo, "REQUEST",
		slog.String("method", v.Method),
		slog.String("path", v.URIPath),
		slog.Int("status", v.Status),
		slog.String("authority", v.Host),
		slog.String("request_id", v.RequestID),
		slog.String("latency", fmt.Sprintf("%s", v.Latency)),
		slog.Int64("response_size", v.ResponseSize),
		slog.String("user_agent", v.UserAgent),
		slog.String("remote_ip", v.RemoteIP),
	)

	return nil
}
