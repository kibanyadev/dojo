package repo

import "testing"

func TestReviewURL(t *testing.T) {
	for _, tc := range []struct{ kind, id, want string }{
		{"writing", "support-l1/write-01", "/w/support-l1/write-01"},
		{"feedback", "support-l1/fb-02", "/story/support-l1/fb-02"},
		{"writing", "a/b c?x", "/w/a/b%20c%3Fx"},
	} {
		if got := reviewURL(tc.kind, tc.id); got != tc.want {
			t.Errorf("reviewURL(%q, %q) = %q, want %q", tc.kind, tc.id, got, tc.want)
		}
	}
}
