package repo

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func i4(n int32) pgtype.Int4 { return pgtype.Int4{Int32: n, Valid: true} }

func TestCheckInboxTexts(t *testing.T) {
	tests := []struct {
		name          string
		id            string
		ok            bool
		passed, total pgtype.Int4
		title, body   string
		url           string
	}{
		{"pass", "support-l1/perms-02", true, i4(2), i4(2), "Ticket passed · perms-02", "Your fix survived the reboot.", "/t/support-l1/perms-02"},
		{"pass without counts", "practice/disk-full", true, pgtype.Int4{}, pgtype.Int4{}, "Ticket passed · disk-full", "Your fix survived the reboot.", "/t/practice/disk-full"},
		{"fail with counts", "support-l1/perms-02", false, i4(1), i4(3), "Check failed · perms-02", "2 of 3 checks still red.", "/t/support-l1/perms-02"},
		{"fail counts unknown", "support-l1/perms-02", false, pgtype.Int4{}, pgtype.Int4{}, "Check failed · perms-02", "Open the ticket to see the output.", "/t/support-l1/perms-02"},
		{"fail with nothing red is unknown", "support-l1/perms-02", false, i4(3), i4(3), "Check failed · perms-02", "Open the ticket to see the output.", "/t/support-l1/perms-02"},
		{"url escapes each segment", "a b/c#d", true, pgtype.Int4{}, pgtype.Int4{}, "Ticket passed · c#d", "Your fix survived the reboot.", "/t/a%20b/c%23d"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			title, body, url := checkInboxText(tc.id, tc.ok, tc.passed, tc.total)
			if title != tc.title || body != tc.body || url != tc.url {
				t.Fatalf("got %q %q %q", title, body, url)
			}
		})
	}
}

func TestCheckInboxURLNeverBreaksTheCheck(t *testing.T) {
	_, _, url := checkInboxText("x/"+strings.Repeat("é\\\x00/", 20), true, pgtype.Int4{}, pgtype.Int4{})
	if !strings.HasPrefix(url, "/t/") || strings.ContainsAny(url, "\\\x00") || strings.Contains(url, "//") {
		t.Fatalf("url %q", url)
	}
}

func TestLastCheckApplies(t *testing.T) {
	cur := &TicketProgress{LastCheckAt: ts(t0)}
	if !lastCheckApplies(nil, t0) || !lastCheckApplies(&TicketProgress{}, t0) {
		t.Fatal("a ticket with no last check takes the first")
	}
	if lastCheckApplies(cur, t0.Add(-time.Second)) {
		t.Fatal("an older check must not apply")
	}
	if !lastCheckApplies(cur, t0) || !lastCheckApplies(cur, t0.Add(time.Second)) {
		t.Fatal("an equal or newer check applies")
	}
}
