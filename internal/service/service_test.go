package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/niksmi-lab/booking-inventory-service/internal/domain"

	"github.com/google/uuid"
)

type repositoryStub struct {
	reservedOrder uuid.UUID
	reservedItems []domain.CartItem
	reserveErr    error
	calls         int
}

func (r *repositoryStub) AddInventory(context.Context, []domain.CartItem) error { return nil }
func (r *repositoryStub) ReserveCart(_ context.Context, orderID uuid.UUID, items []domain.CartItem) error {
	r.calls++
	r.reservedOrder = orderID
	r.reservedItems = items
	return r.reserveErr
}
func (r *repositoryStub) CancelItems(context.Context, uuid.UUID) error    { return nil }
func (r *repositoryStub) ConfirmPayment(context.Context, uuid.UUID) error { return nil }
func (r *repositoryStub) ClearExpired(context.Context) (int64, error)     { return 0, nil }

func TestReserveNormalizesDuplicateItems(t *testing.T) {
	repo := &repositoryStub{}
	svc := NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	orderID := uuid.New()
	first := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	second := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	err := svc.Reserve(context.Background(), orderID, []domain.CartItem{
		{ProductID: first, Quantity: 2},
		{ProductID: second, Quantity: 3},
		{ProductID: first, Quantity: 4},
	})
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}

	want := []domain.CartItem{
		{ProductID: second, Quantity: 3},
		{ProductID: first, Quantity: 6},
	}
	if len(repo.reservedItems) != len(want) {
		t.Fatalf("reserved items = %+v", repo.reservedItems)
	}
	for i := range want {
		if repo.reservedItems[i] != want[i] {
			t.Errorf("reservedItems[%d] = %+v, want %+v", i, repo.reservedItems[i], want[i])
		}
	}
}

func TestReserveRejectsInvalidInputBeforeRepository(t *testing.T) {
	repo := &repositoryStub{}
	svc := NewService(repo, nil)

	err := svc.Reserve(context.Background(), uuid.Nil, []domain.CartItem{{ProductID: uuid.New(), Quantity: 1}})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("Reserve() error = %v, want ErrInvalidArgument", err)
	}
	if repo.calls != 0 {
		t.Fatalf("repository calls = %d, want 0", repo.calls)
	}
}

func TestReservePreservesDomainError(t *testing.T) {
	repo := &repositoryStub{reserveErr: domain.ErrInsufficientStock}
	svc := NewService(repo, nil)

	err := svc.Reserve(context.Background(), uuid.New(), []domain.CartItem{{ProductID: uuid.New(), Quantity: 1}})
	if !errors.Is(err, domain.ErrInsufficientStock) {
		t.Fatalf("Reserve() error = %v, want ErrInsufficientStock", err)
	}
}

func TestNormalizeItemsRejectsAggregatedOverflow(t *testing.T) {
	productID := uuid.New()
	_, err := normalizeItems([]domain.CartItem{
		{ProductID: productID, Quantity: MaxItemQuantity},
		{ProductID: productID, Quantity: 1},
	})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("normalizeItems() error = %v, want ErrInvalidArgument", err)
	}
}
