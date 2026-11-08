package app_test

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/opikdev/api-go-starter/internal/apiclient"
)

const missingNote = "note_00000000000070008000000000000000"

func ptr[T any](v T) *T { return &v }

func createNote(t *testing.T, c *apiclient.ClientWithResponses, title string) apiclient.Note {
	t.Helper()
	res, err := c.CreateNoteWithResponse(context.Background(), nil, apiclient.CreateNote{Title: title})
	if err != nil || res.JSON201 == nil {
		t.Fatalf("create note: %v %s", err, res.Body)
	}
	return *res.JSON201
}

func ifMatch(value string) apiclient.RequestEditorFn {
	return func(_ context.Context, r *http.Request) error {
		r.Header.Set("If-Match", value)
		return nil
	}
}

func TestCreatingNotes(t *testing.T) {
	s := newServer(t, nil)
	ctx := context.Background()

	t.Run("returns the note with ETag and Location", func(t *testing.T) {
		c := s.client(t, clientOptions{token: s.signUp(t).token})
		res, _ := c.CreateNoteWithResponse(ctx, nil, apiclient.CreateNote{Title: "Hello", Body: ptr("World")})
		if res.StatusCode() != 201 || res.JSON201.Title != "Hello" || res.JSON201.Body != "World" || res.JSON201.Version != 1 {
			t.Fatalf("%d %s", res.StatusCode(), res.Body)
		}
		if !regexp.MustCompile(`^note_[0-9a-f]{32}$`).MatchString(res.JSON201.Id) {
			t.Fatalf("id = %s", res.JSON201.Id)
		}
		if res.HTTPResponse.Header.Get("ETag") != `"1"` || res.HTTPResponse.Header.Get("Location") != "/v1/notes/"+res.JSON201.Id {
			t.Fatalf("headers = %v", res.HTTPResponse.Header)
		}
	})

	t.Run("requires authentication", func(t *testing.T) {
		res := s.raw(t, "POST", "/v1/notes", `{"title":"x"}`, nil)
		expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 401, "unauthorized")
	})

	cases := map[string]string{
		"a blank title":                `{"title":"   "}`,
		"a long title":                 `{"title":"` + strings.Repeat("x", 201) + `"}`,
		"a long body":                  `{"title":"x","body":"` + strings.Repeat("y", 10_001) + `"}`,
		"a missing title":              `{"body":"y"}`,
		"an unknown field":             `{"title":"x","ownerId":"usr_1"}`,
		"a NUL character in the title": `{"title":"a\u0000b"}`,
		"a NUL character in the body":  `{"title":"x","body":"a\u0000b"}`,
	}
	for label, body := range cases {
		t.Run("rejects "+label, func(t *testing.T) {
			user := s.signUp(t)
			res := s.raw(t, "POST", "/v1/notes", body, map[string]string{"Authorization": "Bearer " + user.token})
			expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 400, "validation-failed")
		})
	}

	t.Run("accepts values at the limits", func(t *testing.T) {
		c := s.client(t, clientOptions{token: s.signUp(t).token})
		res, _ := c.CreateNoteWithResponse(ctx, nil, apiclient.CreateNote{Title: strings.Repeat("x", 200), Body: ptr(strings.Repeat("y", 10_000))})
		if res.StatusCode() != 201 {
			t.Fatalf("%d %s", res.StatusCode(), res.Body)
		}
	})
}

func TestListingNotes(t *testing.T) {
	s := newServer(t, nil)
	ctx := context.Background()

	t.Run("pages through every note exactly once, newest first", func(t *testing.T) {
		c := s.client(t, clientOptions{token: s.signUp(t).token})
		var created []string
		for i := range 5 {
			created = append(created, createNote(t, c, "Note "+string(rune('0'+i))).Id)
		}
		var seen []string
		var cursor *string
		for {
			res, _ := c.ListNotesWithResponse(ctx, &apiclient.ListNotesParams{Limit: ptr(2), Cursor: cursor})
			if res.JSON200.Meta.Limit != 2 {
				t.Fatalf("limit = %d", res.JSON200.Meta.Limit)
			}
			for _, note := range res.JSON200.Data {
				seen = append(seen, note.Id)
			}
			if cursor = res.JSON200.Meta.NextCursor; cursor == nil {
				break
			}
		}
		slices.Reverse(created)
		if !slices.Equal(seen, created) {
			t.Fatalf("seen %v, want %v", seen, created)
		}
	})

	t.Run("has no next page when the results fit exactly", func(t *testing.T) {
		c := s.client(t, clientOptions{token: s.signUp(t).token})
		createNote(t, c, "a")
		createNote(t, c, "b")
		res, _ := c.ListNotesWithResponse(ctx, &apiclient.ListNotesParams{Limit: ptr(2)})
		if res.JSON200.Meta.NextCursor != nil {
			t.Fatal("expected no next cursor")
		}
	})

	t.Run("defaults to 20 and returns an empty list", func(t *testing.T) {
		res := s.raw(t, "GET", "/v1/notes", "", map[string]string{"Authorization": "Bearer " + s.signUp(t).token})
		if res.status != 200 || strings.TrimSpace(res.body) != `{"data":[],"meta":{"limit":20,"nextCursor":null}}` {
			t.Fatalf("%d %s", res.status, res.body)
		}
	})

	for query, want := range map[string]struct{ field, detail string }{
		"limit=101":  {"limit", "must be at most 100"},
		"limit=0":    {"limit", "must be at least 1"},
		"limit=abc":  {"limit", "is invalid"},
		"cursor=bad": {"cursor", "isn't a valid cursor. Use meta.nextCursor from a previous response"},
	} {
		t.Run("rejects "+query, func(t *testing.T) {
			res := s.raw(t, "GET", "/v1/notes?"+query, "", map[string]string{"Authorization": "Bearer " + s.signUp(t).token})
			expectProblem(t, res.status, res.headers.Get("Content-Type"), []byte(res.body), 400, "validation-failed")
			expectFieldError(t, res.body, want.field, want.detail)
		})
	}

	t.Run("accepts the maximum limit", func(t *testing.T) {
		res := s.raw(t, "GET", "/v1/notes?limit=100", "", map[string]string{"Authorization": "Bearer " + s.signUp(t).token})
		if res.status != 200 {
			t.Fatalf("%d %s", res.status, res.body)
		}
	})
}

func TestOwnership(t *testing.T) {
	s := newServer(t, nil)
	ctx := context.Background()
	owner := s.client(t, clientOptions{token: s.signUp(t).token})
	intruder := s.client(t, clientOptions{token: s.signUp(t).token})
	note := createNote(t, owner, "A note")

	get, _ := intruder.GetNoteWithResponse(ctx, note.Id)
	expectProblem(t, get.StatusCode(), get.HTTPResponse.Header.Get("Content-Type"), get.Body, 404, "not-found")
	update, _ := intruder.UpdateNoteWithResponse(ctx, note.Id, nil, apiclient.UpdateNote{Title: ptr("Hacked")}, ifMatch(`"1"`))
	expectProblem(t, update.StatusCode(), update.HTTPResponse.Header.Get("Content-Type"), update.Body, 404, "not-found")
	del, _ := intruder.DeleteNoteWithResponse(ctx, note.Id)
	expectProblem(t, del.StatusCode(), del.HTTPResponse.Header.Get("Content-Type"), del.Body, 404, "not-found")
	list, _ := intruder.ListNotesWithResponse(ctx, nil)
	if len(list.JSON200.Data) != 0 {
		t.Fatal("intruder sees notes")
	}
	unchanged, _ := owner.GetNoteWithResponse(ctx, note.Id)
	if unchanged.JSON200.Title != "A note" || unchanged.JSON200.Version != 1 {
		t.Fatalf("note changed: %+v", unchanged.JSON200)
	}
}

func TestReadingAndDeleting(t *testing.T) {
	s := newServer(t, nil)
	ctx := context.Background()
	c := s.client(t, clientOptions{token: s.signUp(t).token})

	for label, id := range map[string]string{"an unknown id": missingNote, "a malformed id": "nope", "an id of another type": "usr_00000000000070008000000000000000"} {
		t.Run("returns 404 for "+label, func(t *testing.T) {
			res, _ := c.GetNoteWithResponse(ctx, id)
			expectProblem(t, res.StatusCode(), res.HTTPResponse.Header.Get("Content-Type"), res.Body, 404, "not-found")
		})
	}

	t.Run("deletes a note once", func(t *testing.T) {
		note := createNote(t, c, "Delete me")
		if res, _ := c.DeleteNoteWithResponse(ctx, note.Id); res.StatusCode() != 204 {
			t.Fatalf("delete: %d", res.StatusCode())
		}
		get, _ := c.GetNoteWithResponse(ctx, note.Id)
		expectProblem(t, get.StatusCode(), get.HTTPResponse.Header.Get("Content-Type"), get.Body, 404, "not-found")
		again, _ := c.DeleteNoteWithResponse(ctx, note.Id)
		expectProblem(t, again.StatusCode(), again.HTTPResponse.Header.Get("Content-Type"), again.Body, 404, "not-found")
	})
}

func TestUpdatingNotes(t *testing.T) {
	s := newServer(t, nil)
	ctx := context.Background()
	c := s.client(t, clientOptions{token: s.signUp(t).token})

	t.Run("updates only the given fields and bumps the version", func(t *testing.T) {
		created, _ := c.CreateNoteWithResponse(ctx, nil, apiclient.CreateNote{Title: "Old", Body: ptr("Keep me")})
		res, _ := c.UpdateNoteWithResponse(ctx, created.JSON201.Id, nil, apiclient.UpdateNote{Title: ptr("New")}, ifMatch(`"1"`))
		if res.StatusCode() != 200 || res.JSON200.Title != "New" || res.JSON200.Body != "Keep me" || res.JSON200.Version != 2 {
			t.Fatalf("%d %s", res.StatusCode(), res.Body)
		}
		if res.HTTPResponse.Header.Get("ETag") != `"2"` {
			t.Fatalf("ETag = %q", res.HTTPResponse.Header.Get("ETag"))
		}
	})

	t.Run("allows an empty body", func(t *testing.T) {
		note := createNote(t, c, "x")
		res, _ := c.UpdateNoteWithResponse(ctx, note.Id, nil, apiclient.UpdateNote{Body: ptr("")}, ifMatch(`"1"`))
		if res.StatusCode() != 200 {
			t.Fatalf("%d %s", res.StatusCode(), res.Body)
		}
	})

	t.Run("requires If-Match", func(t *testing.T) {
		note := createNote(t, c, "x")
		res, _ := c.UpdateNoteWithResponse(ctx, note.Id, nil, apiclient.UpdateNote{Title: ptr("New")})
		expectProblem(t, res.StatusCode(), res.HTTPResponse.Header.Get("Content-Type"), res.Body, 428, "precondition-required")
	})

	for _, value := range []string{`"9"`, "nonsense", "*", `"99999999999999999999"`} {
		t.Run("rejects If-Match "+value, func(t *testing.T) {
			note := createNote(t, c, "x")
			res, _ := c.UpdateNoteWithResponse(ctx, note.Id, nil, apiclient.UpdateNote{Title: ptr("New")}, ifMatch(value))
			expectProblem(t, res.StatusCode(), res.HTTPResponse.Header.Get("Content-Type"), res.Body, 412, "version-mismatch")
		})
	}

	t.Run("lets only one of two concurrent updates win", func(t *testing.T) {
		note := createNote(t, c, "x")
		statuses := make([]int, 2)
		var wg sync.WaitGroup
		for i, title := range []string{"A", "B"} {
			wg.Go(func() {
				res, _ := c.UpdateNoteWithResponse(ctx, note.Id, nil, apiclient.UpdateNote{Title: ptr(title)}, ifMatch(`"1"`))
				statuses[i] = res.StatusCode()
			})
		}
		wg.Wait()
		slices.Sort(statuses)
		if !slices.Equal(statuses, []int{200, 412}) {
			t.Fatalf("statuses = %v", statuses)
		}
	})

	t.Run("rejects an empty patch and a blank title", func(t *testing.T) {
		note := createNote(t, c, "x")
		for _, patch := range []apiclient.UpdateNote{{}, {Title: ptr("  ")}} {
			res, _ := c.UpdateNoteWithResponse(ctx, note.Id, nil, patch, ifMatch(`"1"`))
			expectProblem(t, res.StatusCode(), res.HTTPResponse.Header.Get("Content-Type"), res.Body, 400, "validation-failed")
		}
	})

	for label, id := range map[string]string{"an unknown id": missingNote, "a malformed id": "nope"} {
		t.Run("returns 404 for "+label, func(t *testing.T) {
			res, _ := c.UpdateNoteWithResponse(ctx, id, nil, apiclient.UpdateNote{Title: ptr("New")}, ifMatch(`"1"`))
			expectProblem(t, res.StatusCode(), res.HTTPResponse.Header.Get("Content-Type"), res.Body, 404, "not-found")
		})
	}
}
