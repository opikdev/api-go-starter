// Package publicid converts database UUIDs to and from prefixed public IDs, such as note_0190....
package publicid

import (
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// Prefix names the kind of resource an ID belongs to.
type Prefix string

const (
	User Prefix = "usr"
	Note Prefix = "note"
)

var hexID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Format returns the public form of id.
func Format(prefix Prefix, id uuid.UUID) string {
	return string(prefix) + "_" + strings.ReplaceAll(id.String(), "-", "")
}

// Parse returns the UUID behind a public ID, and false when it isn't a valid ID of that kind.
func Parse(prefix Prefix, value string) (uuid.UUID, bool) {
	rest, found := strings.CutPrefix(value, string(prefix)+"_")
	if !found || !hexID.MatchString(rest) {
		return uuid.UUID{}, false
	}
	id, err := uuid.Parse(rest)
	return id, err == nil
}
