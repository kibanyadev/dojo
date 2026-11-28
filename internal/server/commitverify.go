package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/github"
	"github.com/kibanyadev/dojo/internal/server/repo"
)

const (
	defaultVerifyInterval = 60
	defaultVerifyBatch    = 20
)

// retryDelays is the wait after the n-th counted try (design 3.9); later tries wait a day.
var retryDelays = []time.Duration{5 * time.Second, time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

const lateRetryDelay = 24 * time.Hour

// rowTimeout bounds one row's GitHub reads, well inside the 5-minute lease; a timeout is a read
// error, which counts no try.
const rowTimeout = 60 * time.Second

// CommitStore is the verifier's storage: a lease, a result write that touches only the commit columns,
// and the login refresh.
type CommitStore interface {
	LeaseCommits(ctx context.Context, n int) ([]repo.CommitLease, error)
	FinishCommit(ctx context.Context, r repo.CommitResult) (bool, error)
	SetGithubLogin(ctx context.Context, learner pgtype.UUID, login string) error
}

// CommitReader is the GitHub adapter's public reads.
type CommitReader interface {
	UserByID(ctx context.Context, id int64) (github.User, error)
	Commit(ctx context.Context, login, sha string) (github.CommitInfo, error)
	Compare(ctx context.Context, login, base, head string) (github.Comparison, error)
}

// CommitVerifier checks the pass commits of pending tickets in the learners' public repos (design 3.9).
type CommitVerifier struct {
	Store  CommitStore
	GitHub CommitReader
	Batch  int
}

// ParseCommitVerify reads DOJO_COMMIT_VERIFY_INTERVAL_SECONDS (default 60) and DOJO_COMMIT_VERIFY_BATCH (default 20).
func ParseCommitVerify(get func(string) string) (time.Duration, int, error) {
	secs, err := envInt(get, "DOJO_COMMIT_VERIFY_INTERVAL_SECONDS", defaultVerifyInterval, 3600)
	if err != nil {
		return 0, 0, err
	}
	batch, err := envInt(get, "DOJO_COMMIT_VERIFY_BATCH", defaultVerifyBatch, 200)
	if err != nil {
		return 0, 0, err
	}
	return time.Duration(secs) * time.Second, batch, nil
}

// envInt reads a whole number from 1 to max, or def when the variable is empty.
func envInt(get func(string) string, name string, def, max int) (int, error) {
	v := strings.TrimSpace(get(name))
	if v == "" {
		return def, nil
	}
	i, err := strconv.Atoi(v)
	if err != nil || i < 1 || i > max {
		return 0, fmt.Errorf("%s must be a whole number from 1 to %d, got %q", name, max, v)
	}
	return i, nil
}

// RunCommitVerifier ticks until ctx ends; a failed tick is logged and the next one tries again.
func RunCommitVerifier(ctx context.Context, v *CommitVerifier, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if _, err := v.Tick(ctx); err != nil && ctx.Err() == nil {
			slog.Error("commit verification failed", "error", err)
		}
	}
}

// Tick claims a batch and checks each commit once. It returns how many rows it wrote.
func (v *CommitVerifier) Tick(ctx context.Context) (int, error) {
	leases, err := v.Store.LeaseCommits(ctx, v.Batch)
	if err != nil {
		return 0, err
	}
	wrote := 0
	for _, l := range leases {
		if ctx.Err() != nil {
			return wrote, ctx.Err()
		}
		rowCtx, cancel := context.WithTimeout(ctx, rowTimeout)
		res, write := v.check(rowCtx, l)
		cancel()
		if !write {
			continue
		}
		ok, err := v.Store.FinishCommit(ctx, res)
		if err != nil {
			slog.Error("commit result not saved", "item", l.ItemID, "error", err)
			continue
		}
		if ok {
			wrote++
			if res.State != "pending" {
				slog.Info("pass commit checked", "item", l.ItemID, "state", res.State)
			}
		}
	}
	return wrote, nil
}

// check reads GitHub for one lease. It reports no write when the answer is not the learner's doing
// (GitHub 5xx, network, rate limit): the lease is the wait, and only the 7-day rule ends it.
func (v *CommitVerifier) check(ctx context.Context, l repo.CommitLease) (repo.CommitResult, bool) {
	ok, err := v.probe(ctx, l, l.GithubLogin)
	if errors.Is(err, github.ErrNotFound) {
		err = v.refreshLogin(ctx, l, &ok)
	}
	switch {
	case err == nil && ok:
		return repo.CommitResult{Lease: l, State: "verified", Tries: l.Tries}, true
	case l.Expired:
		return repo.CommitResult{Lease: l, State: "unverified", Tries: l.Tries}, true
	case err == nil || errors.Is(err, github.ErrNotFound):
		tries := l.Tries + 1
		return repo.CommitResult{Lease: l, State: "pending", Tries: tries, DelaySeconds: retryDelay(tries).Seconds()}, true
	}
	slog.Warn("commit check skipped", "item", l.ItemID, "error", err)
	return repo.CommitResult{}, false
}

// refreshLogin runs after a 404: it asks GitHub for the login of the stable account id and, when it
// changed, saves it and reads again once. The returned error is the answer to count (ErrNotFound) or not.
func (v *CommitVerifier) refreshLogin(ctx context.Context, l repo.CommitLease, ok *bool) error {
	u, err := v.GitHub.UserByID(ctx, l.GithubID)
	if err != nil {
		return err
	}
	if u.Login == l.GithubLogin {
		return github.ErrNotFound
	}
	if err := v.Store.SetGithubLogin(ctx, l.LearnerID, u.Login); err != nil {
		slog.Error("github login not saved", "error", err)
	}
	var perr error
	*ok, perr = v.probe(ctx, l, u.Login)
	return perr
}

// probe is the four checks: the commit answers 200, GitHub verified its signature, the message names the
// ticket, and the commit is on main (compare says behind or identical).
func (v *CommitVerifier) probe(ctx context.Context, l repo.CommitLease, login string) (bool, error) {
	c, err := v.GitHub.Commit(ctx, login, l.Commit)
	if err != nil || !c.Verified || !hasTrailer(c.Message, "Dojo-Ticket", l.ItemID) {
		return false, err
	}
	cmp, err := v.GitHub.Compare(ctx, login, "main", l.Commit)
	if err != nil {
		return false, err
	}
	return cmp.Status == "behind" || cmp.Status == "identical", nil
}

func hasTrailer(message, key, value string) bool {
	want := key + ": " + value
	for _, line := range strings.Split(message, "\n") {
		if strings.TrimRight(line, "\r ") == want {
			return true
		}
	}
	return false
}

func retryDelay(tries int32) time.Duration {
	if tries >= 1 && int(tries) <= len(retryDelays) {
		return retryDelays[tries-1]
	}
	return lateRetryDelay
}
