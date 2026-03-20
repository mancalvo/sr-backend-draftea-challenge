package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	platformdatabase "github.com/draftea/sr-backend-draftea-challenge/internal/platform/database"
	"github.com/draftea/sr-backend-draftea-challenge/internal/services/wallets/domain"
)

// Sentinel errors returned by repository operations.
var (
	ErrWalletNotFound    = errors.New("wallet not found")
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrDuplicateMovement = errors.New("duplicate movement for transaction and source step")
)

// DebitResult is returned by a successful debit operation.
type DebitResult struct {
	Movement *domain.WalletMovement
	Wallet   *domain.Wallet
}

// CreditResult is returned by a successful credit operation.
type CreditResult struct {
	Movement *domain.WalletMovement
	Wallet   *domain.Wallet
}

// Repository defines the persistence operations for the wallets domain.
type Repository interface {
	// GetWalletByUserID returns the wallet for a given user, or ErrWalletNotFound.
	GetWalletByUserID(ctx context.Context, userID string) (*domain.Wallet, error)

	// Debit atomically debits the wallet and records a movement in a single
	// DB transaction. Uses row-level locking and deduplication by (transaction_id, source_step).
	// Returns ErrWalletNotFound if the wallet does not exist,
	// ErrInsufficientFunds if the balance is too low,
	// or ErrDuplicateMovement if this operation was already applied.
	Debit(ctx context.Context, userID, transactionID, sourceStep string, amount int64) (*DebitResult, error)

	// Credit atomically credits the wallet and records a movement in a single
	// DB transaction. Uses row-level locking and deduplication by (transaction_id, source_step).
	// Returns ErrWalletNotFound if the wallet does not exist,
	// or ErrDuplicateMovement if this operation was already applied.
	Credit(ctx context.Context, userID, transactionID, sourceStep string, amount int64) (*CreditResult, error)
}

// PostgresRepository implements Repository against the wallets PostgreSQL schema.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository creates a new PostgresRepository.
func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) GetWalletByUserID(ctx context.Context, userID string) (*domain.Wallet, error) {
	const q = `SELECT id, user_id, balance, currency, created_at, updated_at
		FROM wallets.wallets WHERE user_id = $1`
	var w domain.Wallet
	err := r.db.QueryRowContext(ctx, q, userID).Scan(
		&w.ID, &w.UserID, &w.Balance, &w.Currency, &w.CreatedAt, &w.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWalletNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get wallet for user %s: %w", userID, err)
	}
	return &w, nil
}

func (r *PostgresRepository) Debit(ctx context.Context, userID, transactionID, sourceStep string, amount int64) (*DebitResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	existing, err := r.findExistingMovement(ctx, tx, transactionID, sourceStep)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		w, err := r.getWalletInTx(ctx, tx, userID)
		if err != nil {
			return nil, err
		}
		return &DebitResult{Movement: existing, Wallet: w}, nil
	}

	var w domain.Wallet
	const lockQ = `SELECT id, user_id, balance, currency, created_at, updated_at
		FROM wallets.wallets WHERE user_id = $1 FOR UPDATE`
	err = tx.QueryRowContext(ctx, lockQ, userID).Scan(
		&w.ID, &w.UserID, &w.Balance, &w.Currency, &w.CreatedAt, &w.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWalletNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock wallet: %w", err)
	}

	if w.Balance < amount {
		return nil, ErrInsufficientFunds
	}

	balanceBefore := w.Balance
	balanceAfter := w.Balance - amount

	const updateQ = `UPDATE wallets.wallets SET balance = $1, updated_at = now() WHERE id = $2`
	if _, err := tx.ExecContext(ctx, updateQ, balanceAfter, w.ID); err != nil {
		return nil, fmt.Errorf("update balance: %w", err)
	}

	var mv domain.WalletMovement
	const insertQ = `INSERT INTO wallets.wallet_movements
		(wallet_id, transaction_id, source_step, type, amount, balance_before, balance_after)
		VALUES ($1, $2, $3, 'debit', $4, $5, $6)
		RETURNING id, wallet_id, transaction_id, source_step, type, amount, balance_before, balance_after, created_at`
	err = tx.QueryRowContext(ctx, insertQ, w.ID, transactionID, sourceStep, amount, balanceBefore, balanceAfter).Scan(
		&mv.ID, &mv.WalletID, &mv.TransactionID, &mv.SourceStep,
		&mv.Type, &mv.Amount, &mv.BalanceBefore, &mv.BalanceAfter, &mv.CreatedAt,
	)
	if err != nil {
		if platformdatabase.IsUniqueViolation(err) {
			return nil, ErrDuplicateMovement
		}
		return nil, fmt.Errorf("insert movement: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit debit tx: %w", err)
	}

	w.Balance = balanceAfter
	return &DebitResult{Movement: &mv, Wallet: &w}, nil
}

func (r *PostgresRepository) Credit(ctx context.Context, userID, transactionID, sourceStep string, amount int64) (*CreditResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	existing, err := r.findExistingMovement(ctx, tx, transactionID, sourceStep)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		w, err := r.getWalletInTx(ctx, tx, userID)
		if err != nil {
			return nil, err
		}
		return &CreditResult{Movement: existing, Wallet: w}, nil
	}

	var w domain.Wallet
	const lockQ = `SELECT id, user_id, balance, currency, created_at, updated_at
		FROM wallets.wallets WHERE user_id = $1 FOR UPDATE`
	err = tx.QueryRowContext(ctx, lockQ, userID).Scan(
		&w.ID, &w.UserID, &w.Balance, &w.Currency, &w.CreatedAt, &w.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWalletNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock wallet: %w", err)
	}

	balanceBefore := w.Balance
	balanceAfter := w.Balance + amount

	const updateQ = `UPDATE wallets.wallets SET balance = $1, updated_at = now() WHERE id = $2`
	if _, err := tx.ExecContext(ctx, updateQ, balanceAfter, w.ID); err != nil {
		return nil, fmt.Errorf("update balance: %w", err)
	}

	var mv domain.WalletMovement
	const insertQ = `INSERT INTO wallets.wallet_movements
		(wallet_id, transaction_id, source_step, type, amount, balance_before, balance_after)
		VALUES ($1, $2, $3, 'credit', $4, $5, $6)
		RETURNING id, wallet_id, transaction_id, source_step, type, amount, balance_before, balance_after, created_at`
	err = tx.QueryRowContext(ctx, insertQ, w.ID, transactionID, sourceStep, amount, balanceBefore, balanceAfter).Scan(
		&mv.ID, &mv.WalletID, &mv.TransactionID, &mv.SourceStep,
		&mv.Type, &mv.Amount, &mv.BalanceBefore, &mv.BalanceAfter, &mv.CreatedAt,
	)
	if err != nil {
		if platformdatabase.IsUniqueViolation(err) {
			return nil, ErrDuplicateMovement
		}
		return nil, fmt.Errorf("insert movement: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit credit tx: %w", err)
	}

	w.Balance = balanceAfter
	return &CreditResult{Movement: &mv, Wallet: &w}, nil
}

func (r *PostgresRepository) findExistingMovement(ctx context.Context, tx *sql.Tx, transactionID, sourceStep string) (*domain.WalletMovement, error) {
	const q = `SELECT id, wallet_id, transaction_id, source_step, type, amount, balance_before, balance_after, created_at
		FROM wallets.wallet_movements
		WHERE transaction_id = $1 AND source_step = $2`
	var mv domain.WalletMovement
	err := tx.QueryRowContext(ctx, q, transactionID, sourceStep).Scan(
		&mv.ID, &mv.WalletID, &mv.TransactionID, &mv.SourceStep,
		&mv.Type, &mv.Amount, &mv.BalanceBefore, &mv.BalanceAfter, &mv.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find existing movement: %w", err)
	}
	return &mv, nil
}

func (r *PostgresRepository) getWalletInTx(ctx context.Context, tx *sql.Tx, userID string) (*domain.Wallet, error) {
	const q = `SELECT id, user_id, balance, currency, created_at, updated_at
		FROM wallets.wallets WHERE user_id = $1`
	var w domain.Wallet
	err := tx.QueryRowContext(ctx, q, userID).Scan(
		&w.ID, &w.UserID, &w.Balance, &w.Currency, &w.CreatedAt, &w.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWalletNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get wallet in tx: %w", err)
	}
	return &w, nil
}

// MemoryRepository is an in-memory implementation of Repository for unit tests.
// It simulates the atomic debit/credit behavior with mutex-based locking.
type MemoryRepository struct {
	mu        sync.Mutex
	Wallets   map[string]*domain.Wallet // keyed by user_id
	Movements []*domain.WalletMovement  // append-only journal
	movIndex  map[string]int            // dedupe index: "txn_id:source_step" -> movement index
	idCounter int
}

// NewMemoryRepository creates an empty in-memory repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		Wallets:  make(map[string]*domain.Wallet),
		movIndex: make(map[string]int),
	}
}

func (m *MemoryRepository) GetWalletByUserID(_ context.Context, userID string) (*domain.Wallet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	w, ok := m.Wallets[userID]
	if !ok {
		return nil, ErrWalletNotFound
	}
	result := *w
	return &result, nil
}

func (m *MemoryRepository) Debit(_ context.Context, userID, transactionID, sourceStep string, amount int64) (*DebitResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	dedupeKey := transactionID + ":" + sourceStep
	if idx, ok := m.movIndex[dedupeKey]; ok {
		mv := *m.Movements[idx]
		w := *m.Wallets[userID]
		return &DebitResult{Movement: &mv, Wallet: &w}, nil
	}

	w, ok := m.Wallets[userID]
	if !ok {
		return nil, ErrWalletNotFound
	}

	if w.Balance < amount {
		return nil, ErrInsufficientFunds
	}

	balanceBefore := w.Balance
	balanceAfter := w.Balance - amount

	w.Balance = balanceAfter
	w.UpdatedAt = time.Now().UTC()

	m.idCounter++
	mv := &domain.WalletMovement{
		ID:            fmt.Sprintf("mv-%d", m.idCounter),
		WalletID:      w.ID,
		TransactionID: transactionID,
		SourceStep:    sourceStep,
		Type:          domain.MovementTypeDebit,
		Amount:        amount,
		BalanceBefore: balanceBefore,
		BalanceAfter:  balanceAfter,
		CreatedAt:     time.Now().UTC(),
	}
	m.Movements = append(m.Movements, mv)
	m.movIndex[dedupeKey] = len(m.Movements) - 1

	resultWallet := *w
	resultMovement := *mv
	return &DebitResult{Movement: &resultMovement, Wallet: &resultWallet}, nil
}

func (m *MemoryRepository) Credit(_ context.Context, userID, transactionID, sourceStep string, amount int64) (*CreditResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	dedupeKey := transactionID + ":" + sourceStep
	if idx, ok := m.movIndex[dedupeKey]; ok {
		mv := *m.Movements[idx]
		w := *m.Wallets[userID]
		return &CreditResult{Movement: &mv, Wallet: &w}, nil
	}

	w, ok := m.Wallets[userID]
	if !ok {
		return nil, ErrWalletNotFound
	}

	balanceBefore := w.Balance
	balanceAfter := w.Balance + amount

	w.Balance = balanceAfter
	w.UpdatedAt = time.Now().UTC()

	m.idCounter++
	mv := &domain.WalletMovement{
		ID:            fmt.Sprintf("mv-%d", m.idCounter),
		WalletID:      w.ID,
		TransactionID: transactionID,
		SourceStep:    sourceStep,
		Type:          domain.MovementTypeCredit,
		Amount:        amount,
		BalanceBefore: balanceBefore,
		BalanceAfter:  balanceAfter,
		CreatedAt:     time.Now().UTC(),
	}
	m.Movements = append(m.Movements, mv)
	m.movIndex[dedupeKey] = len(m.Movements) - 1

	resultWallet := *w
	resultMovement := *mv
	return &CreditResult{Movement: &resultMovement, Wallet: &resultWallet}, nil
}
