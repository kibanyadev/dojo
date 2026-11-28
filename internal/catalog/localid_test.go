package catalog_test

import (
	"strings"
	"testing"

	"github.com/kibanyadev/dojo/internal/catalog"
)

func TestValidLocalID(t *testing.T) {
	for id, want := range map[string]bool{
		"calm":                          true,
		"a-b-2":                         true,
		strings.Repeat("a", 32):         true,
		strings.Repeat("a", 33):         false,
		"":                              false,
		"Calm":                          false,
		"a_b":                           false,
		"-a":                            false,
		"a--b":                          false,
		"a b":                           false,
		"a'; DROP TABLE story_choices;": false,
	} {
		if got := catalog.ValidLocalID(id); got != want {
			t.Errorf("ValidLocalID(%q) = %v, want %v", id, got, want)
		}
	}
}
