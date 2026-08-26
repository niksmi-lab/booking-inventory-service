package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/niksmi-lab/booking-inventory-service/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type stockServiceStub struct {
	reserveErr error
}

func (s *stockServiceStub) Restock(context.Context, []domain.CartItem) error { return nil }
func (s *stockServiceStub) Reserve(context.Context, uuid.UUID, []domain.CartItem) error {
	return s.reserveErr
}
func (s *stockServiceStub) Cancel(context.Context, uuid.UUID) error  { return nil }
func (s *stockServiceStub) Confirm(context.Context, uuid.UUID) error { return nil }

func TestReserveMapsDomainError(t *testing.T) {
	response := performReserve(t, domain.ErrInsufficientStock, validReserveBody(), "application/json")

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "insufficient_stock") {
		t.Fatalf("body = %s", response.Body.String())
	}
	if response.Header().Get(RequestIDHeader) == "" {
		t.Fatal("response has no request ID")
	}
}

func TestReserveDoesNotExposeInternalError(t *testing.T) {
	response := performReserve(t, errors.New("database diagnostic: sensitive-value"), validReserveBody(), "application/json")

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if strings.Contains(response.Body.String(), "sensitive-value") {
		t.Fatalf("internal error leaked in response: %s", response.Body.String())
	}
}

func TestReserveRejectsUnknownJSONField(t *testing.T) {
	body := strings.TrimSuffix(validReserveBody(), "}") + `,"unexpected":true}`
	response := performReserve(t, nil, body, "application/json")

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestReserveRequiresJSONContentType(t *testing.T) {
	response := performReserve(t, nil, validReserveBody(), "text/plain")

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestRequireBearerToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestIDMiddleware(), RequireBearerToken("expected"))
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for _, test := range []struct {
		name   string
		header string
		status int
	}{
		{name: "missing", status: http.StatusUnauthorized},
		{name: "wrong", header: "Bearer wrong", status: http.StatusUnauthorized},
		{name: "valid", header: "Bearer expected", status: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Authorization", test.header)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

func performReserve(t *testing.T, serviceErr error, body, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewStockHandler(&stockServiceStub{reserveErr: serviceErr}, logger)
	router := gin.New()
	router.Use(RequestIDMiddleware())
	router.POST("/reserve", handler.HandleReserve)

	request := httptest.NewRequest(http.MethodPost, "/reserve", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", contentType)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func validReserveBody() string {
	return `{"order_id":"` + uuid.NewString() + `","items":[{"item_id":"` + uuid.NewString() + `","quantity":1}]}`
}
