package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/server/repo"
)

// CatalogStore is what GET /v1/catalog, /v1/path, /v1/today and /v1/activity read (design 3.8).
type CatalogStore interface {
	GetCatalogVersion(ctx context.Context) (string, error)
	GetLearnerChosenPathSlug(ctx context.Context, id pgtype.UUID) (string, error)
	ListCatalogPaths(ctx context.Context) ([]repo.ListCatalogPathsRow, error)
	ListCatalogRoles(ctx context.Context) ([]repo.ListCatalogRolesRow, error)
	ListCatalogPathRoles(ctx context.Context) ([]repo.ListCatalogPathRolesRow, error)
	ListCatalogItems(ctx context.Context) ([]repo.ListCatalogItemsRow, error)
	ListCatalogMarks(ctx context.Context) ([]repo.ListCatalogMarksRow, error)
	ListPracticeItems(ctx context.Context) ([]repo.ListPracticeItemsRow, error)
	ListLearnerTicketStates(ctx context.Context, learnerID pgtype.UUID) ([]repo.ListLearnerTicketStatesRow, error)
	ListLearnerCompletedItems(ctx context.Context, learnerID pgtype.UUID) ([]string, error)
	ListLearnerActivityDays(ctx context.Context, arg repo.ListLearnerActivityDaysParams) ([]repo.ListLearnerActivityDaysRow, error)
	GetLearnerFirstRun(ctx context.Context, learnerID pgtype.UUID) (repo.GetLearnerFirstRunRow, error)
	CountLearnerPasses(ctx context.Context, learnerID pgtype.UUID) (int32, error)
	ListLearnerRecentPasses(ctx context.Context, learnerID pgtype.UUID) ([]repo.ListLearnerRecentPassesRow, error)
}

type catalogPath struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Position int32  `json:"position"`
}

type catalogRole struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	Kind     string `json:"kind"`
	HasExam  bool   `json:"has_exam"`
	Advanced bool   `json:"advanced"`
}

type catalogPathRole struct {
	Path     string `json:"path"`
	Role     string `json:"role"`
	Position int32  `json:"position"`
}

type catalogItem struct {
	ID         string   `json:"id"`
	Role       string   `json:"role"`
	Order      int32    `json:"order"`
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	EstMinutes int32    `json:"est_minutes"`
	Tags       []string `json:"tags"`
}

type catalogMark struct {
	Slug     string `json:"slug"`
	Belt     string `json:"belt"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	Position int32  `json:"position"`
}

type catalogResponse struct {
	Version  *string           `json:"catalog_version"`
	Paths    []catalogPath     `json:"paths"`
	Roles    []catalogRole     `json:"roles"`
	PathRole []catalogPathRole `json:"path_roles"`
	Items    []catalogItem     `json:"items"`
	Marks    []catalogMark     `json:"marks"`
}

// catalog answers the public catalog. Before the first load there is no catalog_state row:
// the version is null and the lists are empty.
func (a Auth) catalog(w http.ResponseWriter, r *http.Request) {
	c, s := a.Catalog, r.Context()
	out := catalogResponse{Paths: []catalogPath{}, Roles: []catalogRole{}, PathRole: []catalogPathRole{}, Items: []catalogItem{}, Marks: []catalogMark{}}
	v, err := c.GetCatalogVersion(s)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		readFailed(w, r, err)
		return
	default:
		out.Version = &v
	}
	paths, err1 := c.ListCatalogPaths(s)
	roles, err2 := c.ListCatalogRoles(s)
	prs, err3 := c.ListCatalogPathRoles(s)
	items, err4 := c.ListCatalogItems(s)
	marks, err5 := c.ListCatalogMarks(s)
	if err := errors.Join(err1, err2, err3, err4, err5); err != nil {
		readFailed(w, r, err)
		return
	}
	for _, p := range paths {
		out.Paths = append(out.Paths, catalogPath{p.Slug, p.Title, p.Status, p.Position})
	}
	for _, x := range roles {
		out.Roles = append(out.Roles, catalogRole{x.Slug, x.Title, x.Summary, x.Kind, x.HasExam, x.Advanced})
	}
	for _, x := range prs {
		out.PathRole = append(out.PathRole, catalogPathRole{x.Path, x.Role, x.Position})
	}
	for _, i := range items {
		tags := i.Tags
		if tags == nil {
			tags = []string{}
		}
		out.Items = append(out.Items, catalogItem{i.ID, i.Role, i.Position, i.Kind, i.Title, i.EstMinutes, tags})
	}
	for _, m := range marks {
		out.Marks = append(out.Marks, catalogMark{m.Slug, m.Belt, m.Title, m.Summary, m.Position})
	}
	writeNoStore(w, out)
}

type pathItem struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Order    int32   `json:"order"`
	State    *string `json:"state"`
	LabState *string `json:"lab_state"`
	// CommitState is the passed commit's verification state; internal, not part of GET /v1/path.
	CommitState *string `json:"-"`
}

type pathRole struct {
	Slug          string        `json:"slug"`
	Title         string        `json:"title"`
	Summary       string        `json:"summary"`
	Locked        bool          `json:"locked"`
	Passed        bool          `json:"passed"`
	TicketsPassed int           `json:"tickets_passed"`
	TicketsTotal  int           `json:"tickets_total"`
	Items         []pathItem    `json:"items"`
	BetweenItems  []betweenItem `json:"between_items"`
}

type pathResponse struct {
	Path   *string    `json:"path"`
	Chosen bool       `json:"chosen"`
	Roles  []pathRole `json:"roles"`
}

// path answers the learner's path with their ticket state: the chosen path with chosen true, else
// the first available path with chosen false (design 2.1, decision h).
func (a Auth) path(w http.ResponseWriter, r *http.Request) {
	out, _, err := a.loadPath(r)
	if err != nil {
		readFailed(w, r, err)
		return
	}
	writeNoStore(w, out)
}

// loadPath reads the catalog and the caller's progress and returns the path, plus when each
// ticket was last seen.
func (a Auth) loadPath(r *http.Request) (pathResponse, map[string]time.Time, error) {
	p, _ := principalOf(r.Context())
	c, s := a.Catalog, r.Context()
	paths, err1 := c.ListCatalogPaths(s)
	roles, err2 := c.ListCatalogRoles(s)
	prs, err3 := c.ListCatalogPathRoles(s)
	items, err4 := c.ListCatalogItems(s)
	prog, err5 := c.ListLearnerTicketStates(s, p.learner)
	done, err7 := c.ListLearnerCompletedItems(s, p.learner)
	chosen, err6 := c.GetLearnerChosenPathSlug(s, p.learner)
	if errors.Is(err6, pgx.ErrNoRows) {
		chosen, err6 = "", nil
	}
	if err := errors.Join(err1, err2, err3, err4, err5, err6, err7); err != nil {
		return pathResponse{}, nil, err
	}
	seen := make(map[string]time.Time, len(prog))
	for _, x := range prog {
		seen[x.ItemID] = x.LastSeenAt.Time
	}
	return buildPath(paths, roles, prs, items, prog, done, chosen), seen, nil
}

// buildPath is the learner's path with their ticket state, shared by GET /v1/path and /v1/today.
// chosenSlug is the learner's available chosen path, empty when there is none.
func buildPath(paths []repo.ListCatalogPathsRow, roles []repo.ListCatalogRolesRow, prs []repo.ListCatalogPathRolesRow,
	items []repo.ListCatalogItemsRow, prog []repo.ListLearnerTicketStatesRow, done []string, chosenSlug string) pathResponse {
	out := pathResponse{Roles: []pathRole{}}
	var chosen *repo.ListCatalogPathsRow
	for i := range paths {
		if paths[i].Status != "available" {
			continue
		}
		if chosenSlug != "" && paths[i].Slug == chosenSlug {
			chosen, out.Chosen = &paths[i], true
			break
		}
		if chosen == nil {
			chosen = &paths[i]
		}
	}
	if chosen == nil {
		return out
	}
	out.Path = &chosen.Slug
	roleBySlug := make(map[string]repo.ListCatalogRolesRow, len(roles))
	for _, x := range roles {
		roleBySlug[x.Slug] = x
	}
	state := make(map[string]repo.ListLearnerTicketStatesRow, len(prog))
	for _, x := range prog {
		state[x.ItemID] = x
	}
	tickets := map[string][]repo.ListCatalogItemsRow{}
	byRole := map[string][]repo.ListCatalogItemsRow{}
	for _, i := range items {
		byRole[i.Role] = append(byRole[i.Role], i)
		if i.Kind == "ticket" {
			tickets[i.Role] = append(tickets[i.Role], i)
		}
	}
	isDone := make(map[string]bool, len(done))
	for _, id := range done {
		isDone[id] = true
	}
	passed := repo.PassedTickets(prog)
	for _, pr := range prs {
		if pr.Path != chosen.Slug {
			continue
		}
		role := roleBySlug[pr.Role]
		pr := pathRole{Slug: role.Slug, Title: role.Title, Summary: role.Summary, Items: []pathItem{}, BetweenItems: []betweenItem{}}
		unlocked := repo.UnlockedItems(byRole[role.Slug], passed)
		for _, i := range byRole[role.Slug] {
			if i.Kind != "ticket" {
				pr.BetweenItems = append(pr.BetweenItems, betweenItem{i.ID, i.Kind, i.Title, i.Position, isDone[i.ID], unlocked[i.ID], i.EstMinutes})
			}
		}
		for _, t := range tickets[role.Slug] {
			it := pathItem{ID: t.ID, Title: t.Title, Order: t.Position}
			if st, ok := state[t.ID]; ok {
				it.State = &st.State
				if st.LabState.Valid {
					it.LabState = &st.LabState.String
				}
				if st.CommitState.Valid {
					it.CommitState = &st.CommitState.String
				}
				if st.State == "passed" || st.State == "revisit" {
					pr.TicketsPassed++
				}
			}
			pr.Items = append(pr.Items, it)
		}
		pr.TicketsTotal = len(pr.Items)
		pr.Locked = chosen.Status == "planned" || pr.TicketsTotal == 0
		// Tickets alone pass only a role with no exam; an exam role waits for role_passes (M5).
		pr.Passed = !role.HasExam && pr.TicketsTotal > 0 && pr.TicketsPassed == pr.TicketsTotal
		out.Roles = append(out.Roles, pr)
	}
	return out
}

func readFailed(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return
	}
	slog.Error("catalog read failed", "path", r.Pattern, "error", err)
	writeError(w, http.StatusInternalServerError, "server_error")
}

func writeNoStore(w http.ResponseWriter, v any) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, v)
}
