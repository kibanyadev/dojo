package server

// betweenItem is a non-ticket item of a role as GET /v1/path lists it (design 3.10).
type betweenItem struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Order int32  `json:"order"`
	Done  bool   `json:"done"`

	unlocked bool
	est      int32
}

type todayBetween struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Role       string `json:"role"`
	Title      string `json:"title"`
	EstMinutes int32  `json:"est_minutes"`
}

// selectBetween is Today's between: the first unlocked, not-done item over the unlocked roles in
// path order (locked roles skipped), each in position order.
func selectBetween(roles []pathRole) *todayBetween {
	for _, role := range roles {
		if role.Locked {
			continue
		}
		for _, b := range role.BetweenItems {
			if b.unlocked && !b.Done {
				return &todayBetween{b.ID, b.Kind, role.Slug, b.Title, b.est}
			}
		}
	}
	return nil
}
