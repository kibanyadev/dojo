package catalogload

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kibanyadev/dojo/internal/catalog"
)

// write upserts the catalog in tx, removes what the catalog dropped and refuses (an error, so the
// caller rolls back) when a dropped item has progress.
func write(ctx context.Context, tx pgx.Tx, cat *catalog.Catalog, bundles []*catalog.Bundle, version string) error {
	var org string
	if err := tx.QueryRow(ctx, `SELECT id FROM orgs WHERE slug = 'public'`).Scan(&org); err != nil {
		return fmt.Errorf("find org: %w", err)
	}
	exec := func(sql string, args ...any) error {
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			return fmt.Errorf("%s: %w", strings.Fields(sql)[0]+" "+strings.Fields(sql)[2], err)
		}
		return nil
	}

	rows, err := itemRows(cat, bundles)
	if err != nil {
		return err
	}
	ids, kinds, marks := make([]string, len(rows)), make([]string, len(rows)), make([]string, len(cat.Marks))
	for i, r := range rows {
		ids[i], kinds[i] = r.ID, r.Kind
	}
	// Marks first: items.awards_mark references them and the key is not deferrable.
	for i, m := range cat.Marks {
		marks[i] = m.Slug
		pos := 1
		for _, o := range cat.Marks[:i] {
			if o.Belt == m.Belt {
				pos++
			}
		}
		if err := exec(`INSERT INTO belt_marks (org_id, slug, belt, title, summary, position) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (slug) DO UPDATE SET belt = EXCLUDED.belt, title = EXCLUDED.title, summary = EXCLUDED.summary,
			position = EXCLUDED.position, updated_at = now()`, org, m.Slug, m.Belt, m.Title, m.Summary, pos); err != nil {
			return err
		}
	}
	// Lock the rows the catalog drops or changes kind, then look for learner rows on them: a first
	// start racing this load either lands before the lock (and is seen) or waits for the commit and
	// fails its foreign key.
	if err := refuseLearnerRows(ctx, tx, `
		SELECT c.id FROM catalog_items c
		WHERE NOT (c.id = ANY($1)) FOR UPDATE`, []any{ids}, ErrWouldDropProgress, "would disappear"); err != nil {
		return err
	}
	if err := refuseLearnerRows(ctx, tx, `
		SELECT c.id FROM catalog_items c JOIN unnest($1::text[], $2::text[]) AS n(id, kind) ON n.id = c.id
		WHERE c.kind <> n.kind FOR UPDATE OF c`, []any{ids, kinds}, ErrWouldChangeKind, "would change kind"); err != nil {
		return err
	}
	// Retired items go before retired marks, the foreign key of awards_mark.
	if err := exec(`DELETE FROM catalog_items WHERE NOT (id = ANY($1))`, ids); err != nil {
		return err
	}

	pathSlugs, roleSlugs := []string{}, []string{}
	for _, p := range cat.Paths {
		pathSlugs = append(pathSlugs, p.Slug)
		if err := exec(`INSERT INTO paths (org_id, slug, title, status, position) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (slug) DO UPDATE SET title = EXCLUDED.title, status = EXCLUDED.status,
			position = EXCLUDED.position, updated_at = now()`, org, p.Slug, p.Title, p.Status, p.Position); err != nil {
			return err
		}
	}
	for _, r := range cat.Roles {
		roleSlugs = append(roleSlugs, r.Slug)
		if err := exec(`INSERT INTO roles (org_id, slug, title, summary, kind, has_exam, advanced) VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (slug) DO UPDATE SET title = EXCLUDED.title, summary = EXCLUDED.summary, kind = EXCLUDED.kind,
			has_exam = EXCLUDED.has_exam, advanced = EXCLUDED.advanced, updated_at = now()`,
			org, r.Slug, r.Title, r.Summary, r.Kind, r.HasExam, r.Advanced); err != nil {
			return err
		}
	}
	for _, pr := range cat.PathRoles {
		if err := exec(`INSERT INTO path_roles (org_id, path_id, role_id, position)
			SELECT $1, p.id, r.id, $4 FROM paths p, roles r WHERE p.slug = $2 AND r.slug = $3
			ON CONFLICT (path_id, role_id) DO UPDATE SET position = EXCLUDED.position, updated_at = now()`,
			org, pr.Path, pr.Role, pr.Position); err != nil {
			return err
		}
	}
	for _, r := range rows {
		var key, sum *string
		var size *int
		if r.Bundle != nil {
			k, h, n := r.Bundle.Key(), r.Bundle.SHA256, len(r.Bundle.Data)
			key, sum, size = &k, &h, &n
		}
		if err := exec(`INSERT INTO catalog_items (id, org_id, role_id, kind, position, title, est_minutes, tags, brief, hints,
				payload, awards_mark, difficulty, catalog_version, bundle_key, bundle_sha256, bundle_bytes)
			VALUES ($1, $2, (SELECT id FROM roles WHERE slug = $3::text), $4, $5, $6, $7, $8, $9, $10, $11::text::jsonb, $12, $13, $14, $15, $16, $17)
			ON CONFLICT (id) DO UPDATE SET role_id = EXCLUDED.role_id, kind = EXCLUDED.kind, position = EXCLUDED.position,
				title = EXCLUDED.title, est_minutes = EXCLUDED.est_minutes, tags = EXCLUDED.tags,
				brief = EXCLUDED.brief, hints = EXCLUDED.hints, payload = EXCLUDED.payload, awards_mark = EXCLUDED.awards_mark,
				difficulty = EXCLUDED.difficulty, catalog_version = EXCLUDED.catalog_version,
				bundle_key = EXCLUDED.bundle_key, bundle_sha256 = EXCLUDED.bundle_sha256,
				bundle_bytes = EXCLUDED.bundle_bytes, updated_at = now()`,
			r.ID, org, r.Role, r.Kind, r.Position, r.Title, r.EstMinutes, r.Tags, r.Brief, r.Hints,
			r.Payload, r.AwardsMark, r.Difficulty, version, key, sum, size); err != nil {
			return err
		}
	}
	// Memberships the catalog no longer lists, then the paths and roles it dropped, after the items
	// moved off them. A role that still has items fails its foreign key and rolls the load back.
	if err := exec(`DELETE FROM path_roles pr USING paths p, roles r
		WHERE pr.path_id = p.id AND pr.role_id = r.id
		AND NOT ((p.slug, r.slug) IN (SELECT * FROM unnest($1::text[], $2::text[])))`,
		pairs(cat, true), pairs(cat, false)); err != nil {
		return err
	}
	if err := exec(`DELETE FROM paths WHERE NOT (slug = ANY($1))`, pathSlugs); err != nil {
		return err
	}
	if err := exec(`DELETE FROM roles WHERE NOT (slug = ANY($1))`, roleSlugs); err != nil {
		return err
	}
	// Retired marks last: no item points at them any more. A mark learners hold is refused by name; the
	// foreign key of belt_awards stays as the backstop. The rows are locked first, so an award racing
	// this load either lands before the lock (and is seen) or waits and fails its foreign key.
	if err := refuseHeldMarks(ctx, tx, marks); err != nil {
		return err
	}
	if err := exec(`DELETE FROM belt_marks WHERE NOT (slug = ANY($1))`, marks); err != nil {
		return err
	}
	return exec(`INSERT INTO catalog_state (id, org_id, catalog_version) VALUES (1, $1, $2)
		ON CONFLICT (id) DO UPDATE SET catalog_version = EXCLUDED.catalog_version, updated_at = now()`, org, version)
}

// refuseHeldMarks fails with ErrWouldDropMark naming every mark the catalog no longer lists that a
// learner holds.
func refuseHeldMarks(ctx context.Context, tx pgx.Tx, keep []string) error {
	rows, err := tx.Query(ctx, `SELECT id FROM belt_marks WHERE NOT (slug = ANY($1)) FOR UPDATE`, keep)
	if err != nil {
		return fmt.Errorf("lock marks: %w", err)
	}
	locked, err := pgx.CollectRows(rows, pgx.RowTo[pgtype.UUID])
	if err != nil {
		return fmt.Errorf("lock marks: %w", err)
	}
	if len(locked) == 0 {
		return nil
	}
	rows, err = tx.Query(ctx, `SELECT m.slug FROM belt_marks m WHERE m.id = ANY($1)
		AND EXISTS (SELECT 1 FROM belt_awards a WHERE a.mark_id = m.id) ORDER BY m.slug`, locked)
	if err != nil {
		return fmt.Errorf("check marks: %w", err)
	}
	held, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("check marks: %w", err)
	}
	if len(held) > 0 {
		return fmt.Errorf("%w: %s", ErrWouldDropMark, strings.Join(held, ", "))
	}
	return nil
}

func pairs(cat *catalog.Catalog, path bool) []string {
	out := make([]string, len(cat.PathRoles))
	for i, pr := range cat.PathRoles {
		out[i] = pr.Role
		if path {
			out[i] = pr.Path
		}
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// refuseLearnerRows runs query (which selects ids and locks them), and when any of them has a row in
// a learner table returns the error wrapped around those ids.
func refuseLearnerRows(ctx context.Context, tx pgx.Tx, query string, args []any, refusal error, what string) error {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("lock items: %w", err)
	}
	locked, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("lock items: %w", err)
	}
	if len(locked) == 0 {
		return nil
	}
	rows, err = tx.Query(ctx, `SELECT c.id FROM catalog_items c WHERE c.id = ANY($1) AND (`+hasLearnerRows()+`) ORDER BY c.id`, locked)
	if err != nil {
		return fmt.Errorf("check progress: %w", err)
	}
	blocked, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("check progress: %w", err)
	}
	if len(blocked) > 0 {
		return fmt.Errorf("%w: %s", refusal, strings.Join(blocked, ", "))
	}
	return nil
}
