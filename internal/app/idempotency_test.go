package app_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/opikdev/api-go-starter/internal/apiclient"
	"github.com/opikdev/api-go-starter/internal/publicid"
)

func withKey(key string) apiclient.RequestEditorFn {
	return func(_ context.Context, r *http.Request) error {
		r.Header.Set("Idempotency-Key", key)
		return nil
	}
}

func noteCount(t *testing.T, userID string) int {
	t.Helper()
	id, _ := publicid.Parse(publicid.User, userID)
	var count int
	if err := appPool.QueryRow(context.Background(), "select count(*) from notes where user_id = $1", id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestIdempotency(t *testing.T) {
	s := newServer(t, nil)
	ctx := context.Background()
	post := func(c *apiclient.ClientWithResponses, key, title string) *apiclient.CreateNoteResponse {
		var editors []apiclient.RequestEditorFn
		if key != "" {
			editors = append(editors, withKey(key))
		}
		res, err := c.CreateNoteWithResponse(ctx, nil, apiclient.CreateNote{Title: title}, editors...)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	t.Run("returns the original note on replay", func(t *testing.T) {
		user := s.signUp(t)
		c := s.client(t, clientOptions{token: user.token})
		first := post(c, "key-1", "Once")
		replay := post(c, "key-1", "Once")
		if replay.StatusCode() != 201 || replay.HTTPResponse.Header.Get("Idempotency-Replayed") != "true" || first.HTTPResponse.Header.Get("Idempotency-Replayed") != "" {
			t.Fatalf("replay header: %v / %v", first.HTTPResponse.Header, replay.HTTPResponse.Header)
		}
		if replay.JSON201.Id != first.JSON201.Id || replay.HTTPResponse.Header.Get("ETag") != first.HTTPResponse.Header.Get("ETag") {
			t.Fatal("replay differs from the original")
		}
		if noteCount(t, user.userID) != 1 {
			t.Fatal("replay created a second note")
		}
	})

	t.Run("creates one note from concurrent requests", func(t *testing.T) {
		user := s.signUp(t)
		c := s.client(t, clientOptions{token: user.token})
		ids := make([]string, 5)
		var wg sync.WaitGroup
		for i := range ids {
			wg.Go(func() {
				res := post(c, "race", "Once")
				if res.StatusCode() == 201 {
					ids[i] = res.JSON201.Id
				}
			})
		}
		wg.Wait()
		for _, id := range ids {
			if id == "" || id != ids[0] {
				t.Fatalf("ids = %v", ids)
			}
		}
		if noteCount(t, user.userID) != 1 {
			t.Fatal("concurrent requests created several notes")
		}
	})

	t.Run("rejects a reused key with a different body", func(t *testing.T) {
		user := s.signUp(t)
		c := s.client(t, clientOptions{token: user.token})
		post(c, "key-2", "First")
		res := post(c, "key-2", "Second")
		expectProblem(t, res.StatusCode(), res.HTTPResponse.Header.Get("Content-Type"), res.Body, 422, "idempotency-key-reused")
		if noteCount(t, user.userID) != 1 {
			t.Fatal("a second note was created")
		}
	})

	t.Run("never replays another user's note", func(t *testing.T) {
		first := s.client(t, clientOptions{token: s.signUp(t).token})
		second := s.client(t, clientOptions{token: s.signUp(t).token})
		a := post(first, "shared", "Same")
		b := post(second, "shared", "Same")
		if a.JSON201.Id == b.JSON201.Id {
			t.Fatal("users share a note")
		}
		if again := post(first, "shared", "Same"); again.JSON201.Id != a.JSON201.Id {
			t.Fatal("first user got someone else's note")
		}
		if again := post(second, "shared", "Same"); again.JSON201.Id != b.JSON201.Id {
			t.Fatal("second user got someone else's note")
		}
	})

	t.Run("creates a new note each time without a key", func(t *testing.T) {
		user := s.signUp(t)
		c := s.client(t, clientOptions{token: user.token})
		post(c, "", "x")
		post(c, "", "x")
		if noteCount(t, user.userID) != 2 {
			t.Fatal("expected two notes")
		}
	})
}
