package migrations_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "migrations", name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func repoWithMigration(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "migrations"), 0o750); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "--quiet", "--initial-branch=main")
	write(t, dir, "00001_init.sql", "-- +goose Up\nSELECT 1;\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "--quiet", "-m", "init")
	git(t, dir, "switch", "--quiet", "-c", "feature")
	return dir
}

func check(t *testing.T, dir string) (string, bool) {
	t.Helper()
	script, err := filepath.Abs("../scripts/check-migrations.sh")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(script, dir).CombinedOutput()
	return string(out), err == nil
}

func TestCheckMigrations(t *testing.T) {
	t.Run("passes a new migration", func(t *testing.T) {
		dir := repoWithMigration(t)
		write(t, dir, "00002_more.sql", "-- +goose Up\nSELECT 2;\n")
		git(t, dir, "add", ".")
		git(t, dir, "commit", "--quiet", "-m", "more")
		if out, ok := check(t, dir); !ok {
			t.Fatal(out)
		}
	})

	t.Run("fails an edited migration, committed or not", func(t *testing.T) {
		dir := repoWithMigration(t)
		write(t, dir, "00001_init.sql", "-- +goose Up\nSELECT 3;\n")
		if out, ok := check(t, dir); ok || !strings.Contains(out, "00001_init.sql was changed") {
			t.Fatalf("uncommitted edit passed: %s", out)
		}
		git(t, dir, "commit", "--quiet", "-am", "edit")
		if out, ok := check(t, dir); ok {
			t.Fatalf("committed edit passed: %s", out)
		}
	})

	t.Run("fails a deleted or renamed migration", func(t *testing.T) {
		for _, change := range [][]string{{"rm", "--quiet", "migrations/00001_init.sql"}, {"mv", "migrations/00001_init.sql", "migrations/00001_renamed.sql"}} {
			dir := repoWithMigration(t)
			git(t, dir, change...)
			if out, ok := check(t, dir); ok {
				t.Fatalf("git %v passed: %s", change, out)
			}
		}
	})

	t.Run("fails two migrations with one version", func(t *testing.T) {
		dir := repoWithMigration(t)
		write(t, dir, "1_other.sql", "-- +goose Up\nSELECT 4;\n")
		if out, ok := check(t, dir); ok || !strings.Contains(out, "version 1") {
			t.Fatalf("duplicate version passed: %s", out)
		}
	})

	t.Run("skips without a main branch", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "migrations"), 0o750); err != nil {
			t.Fatal(err)
		}
		if out, ok := check(t, dir); !ok || !strings.Contains(out, "skipped") {
			t.Fatalf("out = %s", out)
		}
	})
}
