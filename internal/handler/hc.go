package handler

import (
	"net/http"
	"os"

	"github.com/labstack/echo/v4"
)

func (h *ProxyHandler) HealthCheck(c echo.Context) error {
	resp := map[string]string{
		"message": "ok",
	}
	if os.Getenv("HEALTH_CHECK_OK") == "0" {
		resp["message"] = "ng"
		return c.JSON(http.StatusServiceUnavailable, resp)
	}
	return c.JSON(http.StatusOK, resp)
}
