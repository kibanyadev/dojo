package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/kibanyadev/dojo/internal/catalog"
)

type beltMark struct {
	Slug      string  `json:"slug"`
	Title     string  `json:"title"`
	Summary   string  `json:"summary"`
	Held      bool    `json:"held"`
	AwardedAt *string `json:"awarded_at"`
}

type beltResponse struct {
	Belt     string     `json:"belt"`
	Title    string     `json:"title"`
	Complete bool       `json:"complete"`
	Held     int        `json:"held"`
	Total    int        `json:"total"`
	NextBelt *string    `json:"next_belt"`
	Marks    []beltMark `json:"marks"`
}

// belt answers GET /v1/belt (design 3.10): the first belt with marks the learner does not hold yet,
// or the last belt with marks, complete, when every one is held. A belt is held when every mark of it
// is; nothing is stored. A catalog with no marks reads as the first belt with total 0.
func (a Auth) belt(w http.ResponseWriter, r *http.Request) {
	p, _ := principalOf(r.Context())
	rows, err := a.Catalog.ListLearnerBeltMarks(r.Context(), p.learner)
	if err != nil {
		readFailed(w, r, err)
		return
	}
	marks := make(map[string][]beltMark, len(catalog.Belts))
	held := make(map[string]int, len(catalog.Belts))
	for _, x := range rows {
		m := beltMark{Slug: x.Slug, Title: x.Title, Summary: x.Summary}
		if x.AwardedAt.Valid {
			m.Held = true
			at := x.AwardedAt.Time.UTC().Format(time.RFC3339)
			m.AwardedAt = &at
			held[x.Belt]++
		}
		marks[x.Belt] = append(marks[x.Belt], m)
	}
	idx, complete := 0, false
	last := -1
	found := false
	for i, b := range catalog.Belts {
		if len(marks[b]) == 0 {
			continue
		}
		last = i
		if held[b] < len(marks[b]) {
			idx, found = i, true
			break
		}
	}
	if !found && last >= 0 {
		idx, complete = last, true
	}
	name := catalog.Belts[idx]
	out := beltResponse{Belt: name, Title: strings.ToUpper(name[:1]) + name[1:] + " belt", Complete: complete,
		Held: held[name], Total: len(marks[name]), Marks: marks[name]}
	if out.Marks == nil {
		out.Marks = []beltMark{}
	}
	if !complete && idx+1 < len(catalog.Belts) {
		n := catalog.Belts[idx+1]
		out.NextBelt = &n
	}
	writeNoStore(w, out)
}
