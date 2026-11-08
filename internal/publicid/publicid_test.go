package publicid

import (
	"testing"

	"github.com/google/uuid"
)

func TestRoundTrip(t *testing.T) {
	id := uuid.MustParse("0190a7e4-c1f0-7c3e-8d21-b6f4a9e05d13")
	public := Format(Note, id)
	if public != "note_0190a7e4c1f07c3e8d21b6f4a9e05d13" {
		t.Fatalf("public = %s", public)
	}
	if parsed, ok := Parse(Note, public); !ok || parsed != id {
		t.Fatalf("parsed = %v %v", parsed, ok)
	}
}

func TestParseRejects(t *testing.T) {
	for _, value := range []string{"usr_0190a7e4c1f07c3e8d21b6f4a9e05d13", "0190a7e4c1f07c3e8d21b6f4a9e05d13", "note_0190", "note_zzzza7e4c1f07c3e8d21b6f4a9e05d13", "note_0190A7E4C1F07C3E8D21B6F4A9E05D13", ""} {
		if _, ok := Parse(Note, value); ok {
			t.Errorf("accepted %q", value)
		}
	}
}
