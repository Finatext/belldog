package handler

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/Finatext/belldog/internal/appconfig"
	"github.com/Finatext/belldog/internal/middlewares"
)

type ProxyHandler struct {
	cfg         appconfig.Config
	slackClient slackClient
	tokenSvc    tokenService
}

func NewEchoHandler(cfg appconfig.Config, slackClient slackClient, svc tokenService) *echo.Echo {
	h := ProxyHandler{
		cfg:         cfg,
		slackClient: slackClient,
		tokenSvc:    svc,
	}

	e := echo.New()
	e.GET("/hc", h.HealthCheck)
	e.POST("/p/:channel_name/:token", h.Webhook)
	e.POST("/slash", h.SlashCommand)

	e.Pre(middleware.RemoveTrailingSlash())
	e.Use(middleware.RequestID())
	e.Use(middlewares.RequestLogger())
	e.Use(addCacheControlHeader)

	return e
}

func addCacheControlHeader(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set("cache-control", "no-store, no-cache")
		return next(c)
	}
}
