package storage

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/niksmi-lab/booking-inventory-service/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresRepoIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	defer pool.Close()
	if err := ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	repository := NewPostgresRepo(pool, 80*time.Millisecond, 3*time.Second)

	reset := func(t *testing.T) {
		t.Helper()
		if _, err := pool.Exec(ctx, "TRUNCATE reservations, inventory"); err != nil {
			t.Fatalf("truncate tables: %v", err)
		}
	}

	t.Run("reserve and cancel are idempotent", func(t *testing.T) {
		reset(t)
		productID, orderID := uuid.New(), uuid.New()
		items := []domain.CartItem{{ProductID: productID, Quantity: 3}}
		if err := repository.AddInventory(ctx, []domain.CartItem{{ProductID: productID, Quantity: 5}}); err != nil {
			t.Fatal(err)
		}
		if err := repository.ReserveCart(ctx, orderID, items); err != nil {
			t.Fatal(err)
		}
		if err := repository.ReserveCart(ctx, orderID, items); err != nil {
			t.Fatalf("idempotent reserve: %v", err)
		}
		assertQuantity(t, ctx, pool, productID, 2)

		if err := repository.CancelItems(ctx, orderID); err != nil {
			t.Fatal(err)
		}
		if err := repository.CancelItems(ctx, orderID); err != nil {
			t.Fatalf("idempotent cancel: %v", err)
		}
		assertQuantity(t, ctx, pool, productID, 5)
	})

	t.Run("concurrent reservations cannot oversell", func(t *testing.T) {
		reset(t)
		productID := uuid.New()
		if err := repository.AddInventory(ctx, []domain.CartItem{{ProductID: productID, Quantity: 1}}); err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		results := make(chan error, 2)
		var workers sync.WaitGroup
		for range 2 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				results <- repository.ReserveCart(ctx, uuid.New(), []domain.CartItem{{ProductID: productID, Quantity: 1}})
			}()
		}
		close(start)
		workers.Wait()
		close(results)

		var succeeded, insufficient int
		for err := range results {
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, domain.ErrInsufficientStock):
				insufficient++
			default:
				t.Fatalf("unexpected reserve error: %v", err)
			}
		}
		if succeeded != 1 || insufficient != 1 {
			t.Fatalf("succeeded=%d insufficient=%d, want 1 and 1", succeeded, insufficient)
		}
		assertQuantity(t, ctx, pool, productID, 0)
	})

	t.Run("cleanup aggregates the same product", func(t *testing.T) {
		reset(t)
		productID := uuid.New()
		if err := repository.AddInventory(ctx, []domain.CartItem{{ProductID: productID, Quantity: 10}}); err != nil {
			t.Fatal(err)
		}
		for _, quantity := range []int64{2, 3} {
			if err := repository.ReserveCart(ctx, uuid.New(), []domain.CartItem{{ProductID: productID, Quantity: quantity}}); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(120 * time.Millisecond)

		count, err := repository.ClearExpired(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if count != 2 {
			t.Fatalf("cleared count = %d, want 2", count)
		}
		assertQuantity(t, ctx, pool, productID, 10)
	})
}

func assertQuantity(t *testing.T, ctx context.Context, pool *pgxpool.Pool, productID uuid.UUID, want int64) {
	t.Helper()
	var got int64
	if err := pool.QueryRow(ctx, "SELECT available_qty FROM inventory WHERE product_id = $1", productID).Scan(&got); err != nil {
		t.Fatalf("read quantity: %v", err)
	}
	if got != want {
		t.Fatalf("available quantity = %d, want %d", got, want)
	}
}
