package problem

import (
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestWriteSendsAnUnknownSlugAsInternalError(t *testing.T) {
	w := httptest.NewRecorder()
	Write(w, httptest.NewRequest("GET", "/v1/notes", nil), New("no-such-type", "Something broke."))
	if w.Code != 500 || !strings.Contains(w.Body.String(), `"type":"urn:problem:internal-error"`) || !strings.Contains(w.Body.String(), `"status":500`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if status := New("no-such-type", "x").Status(); status != 500 {
		t.Fatalf("Status() = %d", status)
	}
	if Known("no-such-type") || !Known(NotFound) {
		t.Fatal("Known is wrong")
	}
}

func TestWriteUsesAnOverriddenStatus(t *testing.T) {
	w := httptest.NewRecorder()
	Write(w, httptest.NewRequest("POST", "/v1/notes", nil), &Error{Slug: ValidationFailed, Detail: "x", HTTPStatus: 422})
	if w.Code != 422 || !strings.Contains(w.Body.String(), `"status":422`) || !strings.Contains(w.Body.String(), "urn:problem:validation-failed") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func slugsIn(t *testing.T, path string, pattern *regexp.Regexp) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var slugs []string
	for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
		slugs = append(slugs, match[1])
	}
	slices.Sort(slugs)
	return slices.Compact(slugs)
}

func catalogSlugs() []string {
	var slugs []string
	for slug := range catalog {
		slugs = append(slugs, string(slug))
	}
	slices.Sort(slugs)
	return slugs
}

func TestCatalogMatchesTheErrorReference(t *testing.T) {
	documented := slugsIn(t, "../../docs/errors.md", regexp.MustCompile("(?m)^\\| `urn:problem:([a-z-]+)`"))
	if !slices.Equal(documented, catalogSlugs()) {
		t.Fatalf("docs/errors.md lists %v, but the catalog has %v", documented, catalogSlugs())
	}
}

func TestSpecUsesOnlyCatalogSlugs(t *testing.T) {
	for _, slug := range slugsIn(t, "../../api/openapi.yaml", regexp.MustCompile(`urn:problem:([a-z-]+)`)) {
		if !Known(Slug(slug)) {
			t.Errorf("api/openapi.yaml uses urn:problem:%s, which isn't in the catalog", slug)
		}
	}
}
