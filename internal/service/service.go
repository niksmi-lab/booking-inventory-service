package service

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/niksmi-lab/booking-inventory-service/internal/domain"

	"github.com/google/uuid"
)

const (
	MaxCartItems    = 500
	MaxItemQuantity = int64(1_000_000_000)
)

type Repository interface {
	AddInventory(ctx context.Context, items []domain.CartItem) error
	ReserveCart(ctx context.Context, orderID uuid.UUID, items []domain.CartItem) error
	CancelItems(ctx context.Context, orderID uuid.UUID) error
	ConfirmPayment(ctx context.Context, orderID uuid.UUID) error
	ClearExpired(ctx context.Context) (int64, error)
}

type Service struct {
	repo   Repository
	logger *slog.Logger
}

func NewService(repo Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, logger: logger}
}

func (s *Service) Restock(ctx context.Context, items []domain.CartItem) error {
	normalized, err := normalizeItems(items)
	if err != nil {
		return err
	}
	if err := s.repo.AddInventory(ctx, normalized); err != nil {
		return fmt.Errorf("restock inventory: %w", err)
	}
	s.logger.InfoContext(ctx, "inventory restocked", slog.Int("item_count", len(normalized)))
	return nil
}

func (s *Service) Reserve(ctx context.Context, orderID uuid.UUID, items []domain.CartItem) error {
	if err := validateOrderID(orderID); err != nil {
		return err
	}
	normalized, err := normalizeItems(items)
	if err != nil {
		return err
	}
	if err := s.repo.ReserveCart(ctx, orderID, normalized); err != nil {
		return fmt.Errorf("reserve inventory: %w", err)
	}
	s.logger.InfoContext(ctx, "inventory reserved",
		slog.String("order_id", orderID.String()),
		slog.Int("item_count", len(normalized)),
	)
	return nil
}

func (s *Service) Cancel(ctx context.Context, orderID uuid.UUID) error {
	if err := validateOrderID(orderID); err != nil {
		return err
	}
	if err := s.repo.CancelItems(ctx, orderID); err != nil {
		return fmt.Errorf("cancel reservation: %w", err)
	}
	s.logger.InfoContext(ctx, "reservation cancelled", slog.String("order_id", orderID.String()))
	return nil
}

func (s *Service) Confirm(ctx context.Context, orderID uuid.UUID) error {
	if err := validateOrderID(orderID); err != nil {
		return err
	}
	if err := s.repo.ConfirmPayment(ctx, orderID); err != nil {
		return fmt.Errorf("confirm reservation: %w", err)
	}
	s.logger.InfoContext(ctx, "reservation confirmed", slog.String("order_id", orderID.String()))
	return nil
}

func (s *Service) RunCleanupWorker(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.logger.InfoContext(ctx, "reservation cleanup worker started", slog.Duration("interval", interval))
	s.clearExpired(ctx)

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("reservation cleanup worker stopped")
			return
		case <-ticker.C:
			s.clearExpired(ctx)
		}
	}
}

func (s *Service) clearExpired(ctx context.Context) {
	count, err := s.repo.ClearExpired(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "failed to clear expired reservations", slog.Any("error", err))
		}
		return
	}
	if count > 0 {
		s.logger.InfoContext(ctx, "expired reservations cleared", slog.Int64("item_count", count))
	}
}

func normalizeItems(items []domain.CartItem) ([]domain.CartItem, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("%w: items must not be empty", domain.ErrInvalidArgument)
	}
	if len(items) > MaxCartItems {
		return nil, fmt.Errorf("%w: at most %d items are allowed", domain.ErrInvalidArgument, MaxCartItems)
	}

	quantities := make(map[uuid.UUID]int64, len(items))
	for i, item := range items {
		if item.ProductID == uuid.Nil {
			return nil, fmt.Errorf("%w: items[%d].item_id must be a non-zero UUID", domain.ErrInvalidArgument, i)
		}
		if item.Quantity <= 0 || item.Quantity > MaxItemQuantity {
			return nil, fmt.Errorf("%w: items[%d].quantity must be between 1 and %d", domain.ErrInvalidArgument, i, MaxItemQuantity)
		}
		if quantities[item.ProductID] > MaxItemQuantity-item.Quantity {
			return nil, fmt.Errorf("%w: total quantity for product %s exceeds %d", domain.ErrInvalidArgument, item.ProductID, MaxItemQuantity)
		}
		quantities[item.ProductID] += item.Quantity
	}

	normalized := make([]domain.CartItem, 0, len(quantities))
	for productID, quantity := range quantities {
		normalized = append(normalized, domain.CartItem{ProductID: productID, Quantity: quantity})
	}
	sort.Slice(normalized, func(i, j int) bool {
		return bytes.Compare(normalized[i].ProductID[:], normalized[j].ProductID[:]) < 0
	})
	return normalized, nil
}

func validateOrderID(orderID uuid.UUID) error {
	if orderID == uuid.Nil {
		return fmt.Errorf("%w: order_id must be a non-zero UUID", domain.ErrInvalidArgument)
	}
	return nil
}
