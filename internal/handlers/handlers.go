package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/niksmi-lab/booking-inventory-service/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxRequestBodyBytes = 64 << 10

type StockService interface {
	Restock(ctx context.Context, items []domain.CartItem) error
	Reserve(ctx context.Context, orderID uuid.UUID, items []domain.CartItem) error
	Cancel(ctx context.Context, orderID uuid.UUID) error
	Confirm(ctx context.Context, orderID uuid.UUID) error
}

type StockHandler struct {
	service StockService
	logger  *slog.Logger
}

type cartItemDTO struct {
	ItemID   uuid.UUID `json:"item_id"`
	Quantity int64     `json:"quantity"`
}

type reserveRequest struct {
	OrderID uuid.UUID     `json:"order_id"`
	Items   []cartItemDTO `json:"items"`
}

type restockRequest struct {
	Items []cartItemDTO `json:"items"`
}

type orderRequest struct {
	OrderID uuid.UUID `json:"order_id"`
}

func NewStockHandler(service StockService, logger *slog.Logger) *StockHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &StockHandler{service: service, logger: logger}
}

func (h *StockHandler) HandleRestock(c *gin.Context) {
	var request restockRequest
	if err := decodeJSON(c, &request); err != nil {
		writeRequestError(c, err)
		return
	}
	if err := h.service.Restock(c.Request.Context(), toDomain(request.Items)); err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *StockHandler) HandleReserve(c *gin.Context) {
	var request reserveRequest
	if err := decodeJSON(c, &request); err != nil {
		writeRequestError(c, err)
		return
	}
	if err := h.service.Reserve(c.Request.Context(), request.OrderID, toDomain(request.Items)); err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *StockHandler) HandleCancel(c *gin.Context) {
	var request orderRequest
	if err := decodeJSON(c, &request); err != nil {
		writeRequestError(c, err)
		return
	}
	if err := h.service.Cancel(c.Request.Context(), request.OrderID); err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *StockHandler) HandleConfirm(c *gin.Context) {
	var request orderRequest
	if err := decodeJSON(c, &request); err != nil {
		writeRequestError(c, err)
		return
	}
	if err := h.service.Confirm(c.Request.Context(), request.OrderID); err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func toDomain(items []cartItemDTO) []domain.CartItem {
	result := make([]domain.CartItem, len(items))
	for i, item := range items {
		result[i] = domain.CartItem{ProductID: item.ItemID, Quantity: item.Quantity}
	}
	return result
}

func decodeJSON(c *gin.Context, destination any) error {
	contentType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || contentType != "application/json" {
		return errors.New("Content-Type must be application/json")
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodyBytes)

	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON object")
	}
	return nil
}

func writeRequestError(c *gin.Context, err error) {
	message := "request body is invalid"
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		message = "request body is too large"
	}
	writeAPIError(c, http.StatusBadRequest, "invalid_request", message)
}

func (h *StockHandler) writeServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidArgument):
		writeAPIError(c, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, domain.ErrInsufficientStock):
		writeAPIError(c, http.StatusConflict, "insufficient_stock", "one or more products are unavailable")
	case errors.Is(err, domain.ErrReservationNotFound):
		writeAPIError(c, http.StatusNotFound, "reservation_not_found", "reservation was not found")
	case errors.Is(err, domain.ErrReservationExpired):
		writeAPIError(c, http.StatusConflict, "reservation_expired", "reservation has expired")
	case errors.Is(err, domain.ErrReservationConflict):
		writeAPIError(c, http.StatusConflict, "reservation_conflict", "reservation is in an incompatible state")
	case errors.Is(err, context.DeadlineExceeded):
		writeAPIError(c, http.StatusGatewayTimeout, "dependency_timeout", "a dependency did not respond in time")
	case errors.Is(err, context.Canceled):
		writeAPIError(c, http.StatusRequestTimeout, "request_cancelled", "request was cancelled")
	default:
		h.logger.ErrorContext(c.Request.Context(), "request failed",
			slog.Any("error", err),
			slog.String("request_id", RequestID(c)),
			slog.String("method", c.Request.Method),
			slog.String("path", c.FullPath()),
		)
		writeAPIError(c, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

func writeAPIError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"code":       code,
			"message":    message,
			"request_id": RequestID(c),
		},
	})
}

func bearerToken(header string) string {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}
