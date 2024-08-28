package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Finatext/belldog/internal/appconfig"
)

func TestHcOK(t *testing.T) {
	slackClient := &mockSlackClient{}
	svc := &mockTokenService{}
	h := ProxyHandler{
		cfg:         appconfig.Config{},
		slackClient: slackClient,
		tokenSvc:    svc,
	}

	req := httptest.NewRequest(http.MethodGet, "/hc", nil)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	err := h.HealthCheck(c)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, c.Response().Status)
}
