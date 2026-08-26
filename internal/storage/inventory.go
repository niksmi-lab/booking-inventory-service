package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/niksmi-lab/booking-inventory-service/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepo struct {
	pool           *pgxpool.Pool
	reservationTTL time.Duration
	queryTimeout   time.Duration
}

func NewPostgresRepo(pool *pgxpool.Pool, reservationTTL, queryTimeout time.Duration) *PostgresRepo {
	return &PostgresRepo{pool: pool, reservationTTL: reservationTTL, queryTimeout: queryTimeout}
}

func (r *PostgresRepo) AddInventory(ctx context.Context, items []domain.CartItem) error {
	ctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	productIDs, quantities := itemArrays(items)
	const query = `
		INSERT INTO inventory (product_id, available_qty)
		SELECT requested.product_id, requested.qty
		FROM UNNEST($1::uuid[], $2::bigint[]) AS requested(product_id, qty)
		ON CONFLICT (product_id) DO UPDATE
		SET available_qty = inventory.available_qty + EXCLUDED.available_qty,
			updated_at = NOW()`
	if _, err := r.pool.Exec(ctx, query, productIDs, quantities); err != nil {
		return fmt.Errorf("add inventory: %w", err)
	}
	return nil
}

func (r *PostgresRepo) ReserveCart(ctx context.Context, orderID uuid.UUID, items []domain.CartItem) error {
	ctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reservation: %w", err)
	}
	defer rollback(tx)

	if err := lockOrder(ctx, tx, orderID); err != nil {
		return err
	}

	existing, status, expired, err := loadReservationState(ctx, tx, orderID, false)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		if status == "pending" && expired {
			if err := restoreAndSetStatus(ctx, tx, orderID, "expired"); err != nil {
				return err
			}
			if err := commit(ctx, tx, "expire reservation"); err != nil {
				return err
			}
			return domain.ErrReservationExpired
		}
		if (status == "pending" || status == "confirmed") && sameItems(existing, items) {
			return commit(ctx, tx, "idempotent reservation")
		}
		if status == "expired" {
			return domain.ErrReservationExpired
		}
		return domain.ErrReservationConflict
	}

	productIDs, quantities := itemArrays(items)
	const lockInventory = `
		SELECT product_id, available_qty
		FROM inventory
		WHERE product_id = ANY($1::uuid[])
		ORDER BY product_id
		FOR UPDATE`
	rows, err := tx.Query(ctx, lockInventory, productIDs)
	if err != nil {
		return fmt.Errorf("lock inventory: %w", err)
	}
	available := make(map[uuid.UUID]int64, len(items))
	for rows.Next() {
		var productID uuid.UUID
		var quantity int64
		if err := rows.Scan(&productID, &quantity); err != nil {
			rows.Close()
			return fmt.Errorf("scan inventory: %w", err)
		}
		available[productID] = quantity
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read inventory: %w", err)
	}
	for _, item := range items {
		if available[item.ProductID] < item.Quantity {
			return domain.ErrInsufficientStock
		}
	}

	const decrement = `
		UPDATE inventory AS inventory
		SET available_qty = inventory.available_qty - requested.qty,
			updated_at = NOW()
		FROM UNNEST($1::uuid[], $2::bigint[]) AS requested(product_id, qty)
		WHERE inventory.product_id = requested.product_id`
	if _, err := tx.Exec(ctx, decrement, productIDs, quantities); err != nil {
		return fmt.Errorf("decrement inventory: %w", err)
	}

	const insertReservation = `
		INSERT INTO reservations (order_id, product_id, qty, status, expires_at)
		SELECT $1, requested.product_id, requested.qty, 'pending', NOW() + $4::interval
		FROM UNNEST($2::uuid[], $3::bigint[]) AS requested(product_id, qty)`
	if _, err := tx.Exec(ctx, insertReservation, orderID, productIDs, quantities, r.reservationTTL.String()); err != nil {
		return fmt.Errorf("insert reservation: %w", err)
	}
	return commit(ctx, tx, "reservation")
}

func (r *PostgresRepo) CancelItems(ctx context.Context, orderID uuid.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin cancellation: %w", err)
	}
	defer rollback(tx)

	if err := lockOrder(ctx, tx, orderID); err != nil {
		return err
	}
	items, status, _, err := loadReservationState(ctx, tx, orderID, true)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return domain.ErrReservationNotFound
	}
	if status == "cancelled" || status == "expired" {
		return commit(ctx, tx, "idempotent cancellation")
	}
	if status != "pending" {
		return domain.ErrReservationConflict
	}
	if err := restoreAndSetStatus(ctx, tx, orderID, "cancelled"); err != nil {
		return err
	}
	return commit(ctx, tx, "cancellation")
}

func (r *PostgresRepo) ConfirmPayment(ctx context.Context, orderID uuid.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin confirmation: %w", err)
	}
	defer rollback(tx)

	if err := lockOrder(ctx, tx, orderID); err != nil {
		return err
	}
	items, status, expired, err := loadReservationState(ctx, tx, orderID, true)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return domain.ErrReservationNotFound
	}
	if status == "confirmed" {
		return commit(ctx, tx, "idempotent confirmation")
	}
	if status == "expired" {
		return domain.ErrReservationExpired
	}
	if status != "pending" {
		return domain.ErrReservationConflict
	}
	if expired {
		if err := restoreAndSetStatus(ctx, tx, orderID, "expired"); err != nil {
			return err
		}
		if err := commit(ctx, tx, "expired confirmation"); err != nil {
			return err
		}
		return domain.ErrReservationExpired
	}
	if _, err := tx.Exec(ctx, `UPDATE reservations SET status = 'confirmed', updated_at = NOW() WHERE order_id = $1`, orderID); err != nil {
		return fmt.Errorf("confirm reservation: %w", err)
	}
	return commit(ctx, tx, "confirmation")
}

func (r *PostgresRepo) ClearExpired(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()

	const query = `
		WITH expired AS (
			UPDATE reservations
			SET status = 'expired', updated_at = NOW()
			WHERE status = 'pending' AND expires_at <= NOW()
			RETURNING product_id, qty
		), totals AS (
			SELECT product_id, SUM(qty) AS qty
			FROM expired
			GROUP BY product_id
		), restored AS (
			UPDATE inventory AS inventory
			SET available_qty = inventory.available_qty + totals.qty,
				updated_at = NOW()
			FROM totals
			WHERE inventory.product_id = totals.product_id
		)
		SELECT COUNT(*) FROM expired`
	var count int64
	if err := r.pool.QueryRow(ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("clear expired reservations: %w", err)
	}
	return count, nil
}

func lockOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))", orderID); err != nil {
		return fmt.Errorf("lock order: %w", err)
	}
	return nil
}

func loadReservationState(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, forUpdate bool) ([]domain.CartItem, string, bool, error) {
	query := `SELECT product_id, qty, status, expires_at <= NOW() FROM reservations WHERE order_id = $1 ORDER BY product_id`
	if forUpdate {
		query += " FOR UPDATE"
	}
	rows, err := tx.Query(ctx, query, orderID)
	if err != nil {
		return nil, "", false, fmt.Errorf("load reservation: %w", err)
	}
	defer rows.Close()

	var items []domain.CartItem
	var status string
	var expired bool
	for rows.Next() {
		var item domain.CartItem
		var rowStatus string
		var rowExpired bool
		if err := rows.Scan(&item.ProductID, &item.Quantity, &rowStatus, &rowExpired); err != nil {
			return nil, "", false, fmt.Errorf("scan reservation: %w", err)
		}
		if status != "" && status != rowStatus {
			return nil, "", false, fmt.Errorf("reservation %s has inconsistent states", orderID)
		}
		status = rowStatus
		expired = expired || rowExpired
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", false, fmt.Errorf("read reservation: %w", err)
	}
	return items, status, expired, nil
}

func restoreAndSetStatus(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, status string) error {
	const query = `
		WITH changed AS (
			UPDATE reservations
			SET status = $2, updated_at = NOW()
			WHERE order_id = $1 AND status = 'pending'
			RETURNING product_id, qty
		), totals AS (
			SELECT product_id, SUM(qty) AS qty FROM changed GROUP BY product_id
		)
		UPDATE inventory AS inventory
		SET available_qty = inventory.available_qty + totals.qty, updated_at = NOW()
		FROM totals
		WHERE inventory.product_id = totals.product_id`
	if _, err := tx.Exec(ctx, query, orderID, status); err != nil {
		return fmt.Errorf("set reservation status %s: %w", status, err)
	}
	return nil
}

func itemArrays(items []domain.CartItem) ([]uuid.UUID, []int64) {
	productIDs := make([]uuid.UUID, len(items))
	quantities := make([]int64, len(items))
	for i, item := range items {
		productIDs[i] = item.ProductID
		quantities[i] = item.Quantity
	}
	return productIDs, quantities
}

func sameItems(left, right []domain.CartItem) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func commit(ctx context.Context, tx pgx.Tx, operation string) error {
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", operation, err)
	}
	return nil
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
