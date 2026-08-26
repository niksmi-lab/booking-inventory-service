package handlers

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	requestIDKey    = "request_id"
	RequestIDHeader = "X-Request-ID"
)

func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := strings.TrimSpace(c.GetHeader(RequestIDHeader))
		if !validRequestID(requestID) {
			requestID = uuid.NewString()
		}
		c.Set(requestIDKey, requestID)
		c.Header(RequestIDHeader, requestID)
		c.Next()
	}
}

func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}

func RequireBearerToken(expected string) gin.HandlerFunc {
	expectedBytes := []byte(expected)
	return func(c *gin.Context) {
		provided := []byte(bearerToken(c.GetHeader("Authorization")))
		if len(provided) != len(expectedBytes) || subtle.ConstantTimeCompare(provided, expectedBytes) != 1 {
			c.Header("WWW-Authenticate", "Bearer")
			writeAPIError(c, http.StatusUnauthorized, "unauthorized", "valid bearer token is required")
			c.Abort()
			return
		}
		c.Next()
	}
}

func RequestID(c *gin.Context) string {
	value, _ := c.Get(requestIDKey)
	requestID, _ := value.(string)
	return requestID
}

func validRequestID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case character == '-', character == '_', character == '.':
		default:
			return false
		}
	}
	return true
}
