package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/opikdev/api-go-starter/internal/app"
	"github.com/opikdev/api-go-starter/internal/publicid"
	"github.com/opikdev/api-go-starter/internal/store"
)

func TestDatabaseRoles(t *testing.T) {
	ctx := context.Background()
	for label, statement := range map[string]string{
		"DROP":     "drop table notes",
		"TRUNCATE": "truncate notes",
		"ALTER":    "alter table notes add column hacked text",
		"CREATE":   "create table hacked (id int)",
	} {
		t.Run("stops the app role from running "+label, func(t *testing.T) {
			if _, err := appPool.Exec(ctx, statement); err == nil {
				t.Fatal("the app role changed the schema")
			}
		})
	}
}

func TestConstraints(t *testing.T) {
	s := newServer(t, nil)
	ctx := context.Background()
	user := s.signUp(t)
	userID, _ := publicid.Parse(publicid.User, user.userID)

	for label, statement := range map[string]string{
		"an uppercase email": "insert into users (email, password_hash) values ('A@B.C', 'x')",
		"an orphaned note":   "insert into notes (user_id, title) values ('00000000-0000-7000-8000-000000000000', 'x')",
	} {
		t.Run("rejects "+label, func(t *testing.T) {
			if _, err := appPool.Exec(ctx, statement); err == nil {
				t.Fatal("the database accepted invalid data")
			}
		})
	}
	t.Run("rejects an invalid note even if the API were bypassed", func(t *testing.T) {
		if _, err := appPool.Exec(ctx, "insert into notes (user_id, title) values ($1, '')", userID); err == nil {
			t.Fatal("blank title accepted")
		}
		if _, err := appPool.Exec(ctx, "insert into notes (user_id, title, body) values ($1, 'x', $2)", userID, strings.Repeat("y", 10_001)); err == nil {
			t.Fatal("long body accepted")
		}
	})
	t.Run("removes a user's data with the user", func(t *testing.T) {
		createNote(t, s.client(t, clientOptions{token: user.token}), "Mine")
		if _, err := appPool.Exec(ctx, "delete from users where id = $1", userID); err != nil {
			t.Fatal(err)
		}
		var notes, sessions int
		_ = appPool.QueryRow(ctx, "select count(*) from notes where user_id = $1", userID).Scan(&notes)
		_ = appPool.QueryRow(ctx, "select count(*) from sessions where user_id = $1", userID).Scan(&sessions)
		if notes != 0 || sessions != 0 {
			t.Fatalf("left %d notes and %d sessions", notes, sessions)
		}
	})
}

func TestCleanup(t *testing.T) {
	s := newServer(t, nil)
	ctx := context.Background()
	expired, live := s.signUp(t), s.signUp(t)
	expiredID, _ := publicid.Parse(publicid.User, expired.userID)
	liveID, _ := publicid.Parse(publicid.User, live.userID)
	past, future := time.Now().Add(-time.Second), time.Now().Add(time.Minute)
	must := func(_ any, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(appPool.Exec(ctx, "update sessions set expires_at = $1 where user_id = $2", past, expiredID))
	must(appPool.Exec(ctx, "insert into idempotency_keys (user_id, key, request_hash, response_body, expires_at) values ($1, 'old', 'x', '{}', $2), ($3, 'new', 'x', '{}', $4)", expiredID, past, liveID, future))
	must(appPool.Exec(ctx, "insert into rate_limits (key, reset_at) values ('cleanup:old', $1), ('cleanup:new', $2)", past, future))

	if err := app.DeleteExpiredRows(ctx, store.New(appPool)); err != nil {
		t.Fatal(err)
	}
	count := func(query string, args ...any) int {
		var n int
		_ = appPool.QueryRow(ctx, query, args...).Scan(&n)
		return n
	}
	if count("select count(*) from sessions where user_id = $1", expiredID) != 0 || count("select count(*) from sessions where user_id = $1", liveID) == 0 {
		t.Fatal("sessions not cleaned correctly")
	}
	if count("select count(*) from idempotency_keys where key = 'old'") != 0 || count("select count(*) from idempotency_keys where key = 'new'") != 1 {
		t.Fatal("idempotency keys not cleaned correctly")
	}
	if count("select count(*) from rate_limits where key = 'cleanup:old'") != 0 || count("select count(*) from rate_limits where key = 'cleanup:new'") != 1 {
		t.Fatal("rate limits not cleaned correctly")
	}
}

func TestMigrations(t *testing.T) {
	ctx := context.Background()
	owner, err := pgx.Connect(ctx, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close(ctx) }()

	rows, _ := owner.Query(ctx, "select conname from pg_constraint where connamespace = 'public'::regnamespace and contype in ('p','u','f','c') and conrelid <> 'goose_db_version'::regclass")
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(names) < 10 {
		t.Fatalf("constraints: %v %v", names, err)
	}
	for _, name := range names {
		if !strings.HasPrefix(name, "pk_") && !strings.HasPrefix(name, "uq_") && !strings.HasPrefix(name, "fk_") && !strings.HasPrefix(name, "chk_") {
			t.Errorf("constraint %s lacks a pk_, uq_, fk_, or chk_ prefix", name)
		}
	}

	rows, _ = owner.Query(ctx, `select t.table_name, count(c.column_name) from information_schema.tables t
		left join information_schema.columns c on c.table_schema = t.table_schema and c.table_name = t.table_name and c.column_name in ('id','created_at','updated_at')
		where t.table_schema = 'public' and t.table_type = 'BASE TABLE' and t.table_name <> 'goose_db_version' group by t.table_name`)
	type tableColumns struct {
		Name  string
		Count int
	}
	tables, err := pgx.CollectRows(rows, pgx.RowToStructByPos[tableColumns])
	if err != nil || len(tables) < 5 {
		t.Fatalf("tables: %v %v", tables, err)
	}
	for _, table := range tables {
		if table.Count != 3 {
			t.Errorf("table %s needs id, created_at, and updated_at", table.Name)
		}
	}
}
