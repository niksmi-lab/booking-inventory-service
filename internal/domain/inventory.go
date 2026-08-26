package domain

import (
	"errors"

	"github.com/google/uuid"
)

var (
	ErrInvalidArgument     = errors.New("invalid argument")
	ErrInsufficientStock   = errors.New("insufficient stock")
	ErrReservationNotFound = errors.New("reservation not found")
	ErrReservationConflict = errors.New("reservation state conflict")
	ErrReservationExpired  = errors.New("reservation expired")
)

type CartItem struct {
	ProductID uuid.UUID
	Quantity  int64
}
