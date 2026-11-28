package server

import (
	"errors"
	"net/http"

	"github.com/kibanyadev/dojo/internal/server/repo"
)

type practiceItem struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Role       *string  `json:"role"`
	Title      string   `json:"title"`
	EstMinutes int32    `json:"est_minutes"`
	Tags       []string `json:"tags"`
	Difficulty *string  `json:"difficulty"`
	State      *string  `json:"state"`
	LabState   *string  `json:"lab_state"`
}

// practice answers GET /v1/practice (design 3.10): every practice item, then every ticket of the
// roles that are not locked on the learner's path, each with the learner's state.
func (a Auth) practice(w http.ResponseWriter, r *http.Request) {
	path, _, err := a.loadPath(r)
	if err != nil {
		readFailed(w, r, err)
		return
	}
	p, _ := principalOf(r.Context())
	c, s := a.Catalog, r.Context()
	practice, err1 := c.ListPracticeItems(s)
	items, err2 := c.ListCatalogItems(s)
	prog, err3 := c.ListLearnerTicketStates(s, p.learner)
	if err := errors.Join(err1, err2, err3); err != nil {
		readFailed(w, r, err)
		return
	}
	state := make(map[string]repo.ListLearnerTicketStatesRow, len(prog))
	for _, x := range prog {
		state[x.ItemID] = x
	}
	withState := func(it practiceItem) practiceItem {
		if st, ok := state[it.ID]; ok {
			v := st.State
			it.State = &v
			if st.LabState.Valid {
				it.LabState = &st.LabState.String
			}
		}
		return it
	}
	tags := func(t []string) []string {
		if t == nil {
			return []string{}
		}
		return t
	}
	out := struct {
		Items []practiceItem `json:"items"`
	}{Items: []practiceItem{}}
	for _, x := range practice {
		out.Items = append(out.Items, withState(practiceItem{
			ID: x.ID, Kind: "practice", Title: x.Title, EstMinutes: x.EstMinutes, Tags: tags(x.Tags), Difficulty: textPtr(x.Difficulty),
		}))
	}
	byID := make(map[string]repo.ListCatalogItemsRow, len(items))
	for _, i := range items {
		byID[i.ID] = i
	}
	for _, role := range path.Roles {
		if role.Locked {
			continue
		}
		slug := role.Slug
		for _, pi := range role.Items {
			i := byID[pi.ID]
			out.Items = append(out.Items, withState(practiceItem{
				ID: i.ID, Kind: "ticket", Role: &slug, Title: i.Title, EstMinutes: i.EstMinutes, Tags: tags(i.Tags),
			}))
		}
	}
	writeNoStore(w, out)
}
