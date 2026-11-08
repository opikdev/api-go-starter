// Package database opens the connection pool, applies migrations, and seeds demo data.
package database

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/opikdev/api-go-starter/internal/auth"
	"github.com/opikdev/api-go-starter/internal/store"
	"github.com/opikdev/api-go-starter/migrations"
)

// DemoEmail and DemoPassword are the fake seed user.
const (
	DemoEmail    = "demo@example.com"
	DemoPassword = "demo-password-123"
)

// Open connects a pool that traces queries and cancels slow statements.
func Open(ctx context.Context, url string, statementTimeoutMS int) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = 10
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = strconv.Itoa(statementTimeoutMS)
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	return pool, nil
}

// Migrate applies every pending migration as the schema owner.
func Migrate(ctx context.Context, url string) error {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return fmt.Errorf("parse MIGRATION_DATABASE_URL: %w", err)
	}
	db := stdlib.OpenDB(*cfg)
	defer func() { _ = db.Close() }()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// Seed adds the demo user and two fake notes, unless they already exist.
func Seed(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	queries := store.New(pool)
	if _, err := queries.FindUserByEmail(ctx, DemoEmail); err == nil {
		return false, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	hash, err := auth.HashPassword(DemoPassword)
	if err != nil {
		return false, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := queries.WithTx(tx)
	user, err := q.CreateUser(ctx, store.CreateUserParams{Email: DemoEmail, PasswordHash: hash})
	if err != nil {
		return false, fmt.Errorf("create demo user: %w", err)
	}
	for _, note := range []store.InsertNoteParams{
		{UserID: user.ID, Title: "Welcome", Body: "This is fake seed data."},
		{UserID: user.ID, Title: "Try the API", Body: "See /docs for every endpoint."},
	} {
		if _, err := q.InsertNote(ctx, note); err != nil {
			return false, fmt.Errorf("create demo note: %w", err)
		}
	}
	return true, tx.Commit(ctx)
}
