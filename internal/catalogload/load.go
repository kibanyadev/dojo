// Package catalogload is the one-off catalog load (design 3.8): it builds every ticket bundle,
// uploads the new ones to the object store and upserts the catalog tables in one transaction.
package catalogload

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/kibanyadev/dojo/internal/catalog"
	"github.com/kibanyadev/dojo/internal/storage"
)

// ErrWouldDropProgress means the load would remove catalog items learners have progress on.
var ErrWouldDropProgress = errors.New("catalog load refused: items with learner progress would disappear")

// ErrWouldChangeKind means the load would change the kind of a catalog item learners have progress on.
var ErrWouldChangeKind = errors.New("catalog load refused: items with learner progress would change kind")

// ErrWouldDropMark means the load would remove belt marks learners hold.
var ErrWouldDropMark = errors.New("catalog load refused: marks that learners hold would disappear")

var versionRE = regexp.MustCompile(`^[0-9a-f]{12}$`)

var commitRE = regexp.MustCompile(`^[0-9a-f]{12,64}$`)

// Version turns a catalog repo commit into the 12 character catalog_version.
func Version(commit string) (string, error) {
	c := strings.ToLower(strings.TrimSpace(commit))
	if !commitRE.MatchString(c) {
		return "", fmt.Errorf("version %q must be a commit hash of at least 12 hex characters", commit)
	}
	return c[:12], nil
}

// Result counts what a load did.
type Result struct {
	Uploaded int // bundles put in the store
	Reused   int // bundles whose key already existed
	Tickets  int // role tickets
	Practice int
}

// Beginner opens the transaction; *pgxpool.Pool satisfies it.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Load reads the catalog tree, uploads its bundles and writes the catalog tables as version.
// Nothing in the database changes unless the whole load succeeds.
func Load(ctx context.Context, db Beginner, store storage.Store, tree fs.FS, version string) (Result, error) {
	var res Result
	if !versionRE.MatchString(version) {
		return res, fmt.Errorf("version %q must be 12 lower-case hex characters", version)
	}
	cat, err := catalog.Load(tree)
	if err != nil {
		return res, err
	}
	// Role tickets first, then practice: itemRows reads them in that order.
	all := append(slices.Clone(cat.Tickets), cat.Practice...)
	bundles := make([]*catalog.Bundle, len(all))
	for i, t := range all {
		if bundles[i], err = catalog.BuildBundle(tree, t.ID); err != nil {
			return res, err
		}
	}
	// Objects are content addressed, so uploading before the transaction is safe: a refused or failed
	// load leaves only unreferenced objects behind.
	for _, b := range bundles {
		put, err := upload(ctx, store, b)
		if err != nil {
			return res, err
		}
		if put {
			res.Uploaded++
		} else {
			res.Reused++
		}
	}
	res.Tickets = len(cat.Tickets)
	res.Practice = len(cat.Practice)

	tx, err := db.Begin(ctx)
	if err != nil {
		return res, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Class 3 = catalog load (1 and 2 are the per-learner batch and the lab sweeper): loads run one at a time.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(3, 0)`); err != nil {
		return res, fmt.Errorf("lock: %w", err)
	}
	if err := write(ctx, tx, cat, bundles, version); err != nil {
		return res, err
	}
	if err := tx.Commit(ctx); err != nil {
		return res, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}

// upload puts the bundle unless its key exists, and reports whether it did.
func upload(ctx context.Context, store storage.Store, b *catalog.Bundle) (bool, error) {
	key := b.Key()
	switch _, err := store.Stat(ctx, key); {
	case err == nil:
		return false, nil
	case !errors.Is(err, storage.ErrNotFound):
		return false, err
	}
	err := store.Put(ctx, key, strings.NewReader(string(b.Data)), int64(len(b.Data)), b.SHA256)
	if errors.Is(err, storage.ErrExists) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("upload %s: %w", key, err)
	}
	return true, nil
}
