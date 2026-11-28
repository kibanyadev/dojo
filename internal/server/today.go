package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

type todayTicket struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	State    *string `json:"state"`
	LabState *string `json:"lab_state"`
}

type todayRole struct {
	Slug          string `json:"slug"`
	Title         string `json:"title"`
	TicketsPassed int    `json:"tickets_passed"`
	TicketsTotal  int    `json:"tickets_total"`
}

type todayResponse struct {
	Current   *todayTicket   `json:"current"`
	Next      *todayTicket   `json:"next"`
	Role      *todayRole     `json:"role"`
	AllPassed bool           `json:"all_passed"`
	FirstRun  *todayFirstRun `json:"first_run"`
	Between   *todayBetween  `json:"between"`
}

// todayFirstRun is the learner's earliest first pass while they have not left "day one done" (design 3.9).
type todayFirstRun struct {
	ID                string    `json:"id"`
	Title             string    `json:"title"`
	FirstPassedAt     time.Time `json:"first_passed_at"`
	PassedCommitState *string   `json:"passed_commit_state"`
}

// today answers what L1 draws: the ticket in progress, the next one and the role that holds them
// (design 3.8), plus the first run when there is one.
func (a Auth) today(w http.ResponseWriter, r *http.Request) {
	p, seen, err := a.loadPath(r)
	if err != nil {
		readFailed(w, r, err)
		return
	}
	out := selectToday(p.Roles, seen)
	out.Between = selectBetween(p.Roles)
	fr, err := a.Catalog.GetLearnerFirstRun(r.Context(), principalLearner(r))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		readFailed(w, r, err)
		return
	default:
		out.FirstRun = &todayFirstRun{ID: fr.ItemID, Title: fr.Title, FirstPassedAt: fr.FirstPassedAt.Time}
		if fr.CommitState.Valid {
			out.FirstRun.PassedCommitState = &fr.CommitState.String
		}
	}
	writeNoStore(w, out)
}

func isOpen(state *string) bool { return state == nil || *state == "started" }

// selectToday picks current, next and role from the roles in path order. Locked roles have no
// tickets and are skipped.
func selectToday(roles []pathRole, lastSeen map[string]time.Time) todayResponse {
	type pos struct{ role, item int }
	var cur *pos
	var last *pos // last open role with tickets
	total := 0
	for ri, role := range roles {
		if role.Locked {
			continue
		}
		last = &pos{ri, 0}
		for ii, it := range role.Items {
			total++
			if it.State == nil || *it.State != "started" {
				continue
			}
			c := roles[ri].Items[ii]
			if cur == nil || lastSeen[c.ID].After(lastSeen[roles[cur.role].Items[cur.item].ID]) {
				cur = &pos{ri, ii}
			}
		}
	}
	if last == nil || total == 0 {
		return todayResponse{}
	}
	ticket := func(p pos) *todayTicket {
		it := roles[p.role].Items[p.item]
		return &todayTicket{it.ID, it.Title, it.State, it.LabState}
	}
	var next *pos
	for ri, role := range roles {
		if role.Locked {
			continue
		}
		for ii, it := range role.Items {
			if isOpen(it.State) && (cur == nil || cur.role != ri || cur.item != ii) {
				next = &pos{ri, ii}
				break
			}
		}
		if next != nil {
			break
		}
	}
	out := todayResponse{}
	holder := last.role
	switch {
	case cur != nil:
		out.Current = ticket(*cur)
		holder = cur.role
		if next != nil {
			out.Next = ticket(*next)
		}
	case next != nil:
		out.Next = ticket(*next)
		holder = next.role
	default:
		out.AllPassed = true
	}
	h := roles[holder]
	out.Role = &todayRole{h.Slug, h.Title, h.TicketsPassed, h.TicketsTotal}
	return out
}
