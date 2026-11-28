package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// MaxPushSubscriptions is how many browsers one learner keeps; the oldest goes (design 3.10).
const MaxPushSubscriptions = 10

var (
	// ErrPushNotFound means no subscription of that learner has that id.
	ErrPushNotFound = errors.New("push subscription not found")
	// ErrBadTimeZone means Postgres does not accept the zone name.
	ErrBadTimeZone = errors.New("time zone not accepted")
)

// SubscribePushIn is one validated POST /v1/push/subscriptions.
type SubscribePushIn struct {
	Learner     pgtype.UUID
	Endpoint    string
	P256dh      string
	Auth        string
	DeviceLabel string
	TimeZone    string
}

// PushSubscriptionRow is one subscription as GET /v1/push/subscriptions lists it.
type PushSubscriptionRow struct {
	ID            pgtype.UUID
	DeviceLabel   string
	CreatedAt     time.Time
	LastSuccessAt *time.Time
}

const subscribeAttempts = 3

// SubscribePush stores the learner's zone and the subscription in one transaction, under the learner's
// class-1 lock so two subscribes cannot both pass the cap of 10. A row with the same endpoint, whoever's,
// is deleted first: a browser belongs to whoever subscribed last on it. Two learners racing on one
// endpoint can still collide on the unique index; that attempt is rerun.
func (s *Store) SubscribePush(ctx context.Context, in SubscribePushIn) (pgtype.UUID, error) {
	var lastErr error
	for range subscribeAttempts {
		id, err := s.subscribePushOnce(ctx, in)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			lastErr = err
			continue
		}
		return id, err
	}
	return pgtype.UUID{}, lastErr
}

func (s *Store) subscribePushOnce(ctx context.Context, in SubscribePushIn) (pgtype.UUID, error) {
	var id pgtype.UUID
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return id, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	if err := q.LockLearnerProgress(ctx, in.Learner); err != nil {
		return id, fmt.Errorf("lock: %w", err)
	}
	if err := checkTimeZone(ctx, tx, in.TimeZone); err != nil {
		return id, err
	}
	n, err := q.SetLearnerTimeZone(ctx, SetLearnerTimeZoneParams{TimeZone: pgtype.Text{String: in.TimeZone, Valid: true}, LearnerID: in.Learner})
	if err != nil {
		return id, fmt.Errorf("set time zone: %w", err)
	}
	if n == 0 {
		return id, ErrLearnerGone
	}
	if err := q.DeletePushSubscriptionByEndpoint(ctx, in.Endpoint); err != nil {
		return id, fmt.Errorf("delete endpoint: %w", err)
	}
	id, err = q.InsertPushSubscription(ctx, InsertPushSubscriptionParams{LearnerID: in.Learner, Endpoint: in.Endpoint,
		P256dh: in.P256dh, Auth: in.Auth, DeviceLabel: in.DeviceLabel})
	if err != nil {
		return id, fmt.Errorf("insert subscription: %w", err)
	}
	if err := q.TrimPushSubscriptions(ctx, TrimPushSubscriptionsParams{LearnerID: in.Learner, Keep: MaxPushSubscriptions}); err != nil {
		return id, fmt.Errorf("trim subscriptions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return id, fmt.Errorf("commit: %w", err)
	}
	return id, nil
}

// checkTimeZone asks Postgres whether it accepts the name: Go and Postgres do not share one zone list
// (design 3.10). A rejection poisons the transaction, which the caller abandons.
func checkTimeZone(ctx context.Context, tx pgx.Tx, name string) error {
	var ok bool
	err := tx.QueryRow(ctx, "SELECT (now() AT TIME ZONE $1::text) IS NOT NULL", name).Scan(&ok)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "22023" {
		return ErrBadTimeZone
	}
	if err != nil {
		return fmt.Errorf("check time zone: %w", err)
	}
	return nil
}

// ListPushSubscriptions returns the learner's subscriptions, newest first.
func (s *Store) ListPushSubscriptions(ctx context.Context, learner pgtype.UUID) ([]PushSubscriptionRow, error) {
	rows, err := s.q.ListPushSubscriptions(ctx, learner)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	out := make([]PushSubscriptionRow, 0, len(rows))
	for _, r := range rows {
		row := PushSubscriptionRow{ID: r.ID, DeviceLabel: r.DeviceLabel, CreatedAt: r.CreatedAt.Time}
		if r.LastSuccessAt.Valid {
			t := r.LastSuccessAt.Time
			row.LastSuccessAt = &t
		}
		out = append(out, row)
	}
	return out, nil
}

// DeletePushSubscription removes one of the learner's subscriptions; another learner's id is not found.
func (s *Store) DeletePushSubscription(ctx context.Context, learner, id pgtype.UUID) error {
	n, err := s.q.DeletePushSubscription(ctx, DeletePushSubscriptionParams{ID: id, LearnerID: learner})
	if err != nil {
		return fmt.Errorf("delete subscription: %w", err)
	}
	if n == 0 {
		return ErrPushNotFound
	}
	return nil
}
