package server

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/server/repo"
	"github.com/kibanyadev/dojo/internal/webpush"
)

const (
	pushKeyPath      = "/v1/push/key"
	pushSubsPath     = "/v1/push/subscriptions"
	maxPushBody      = 4 << 10
	maxDeviceLabel   = 64
	pushP256dhLen    = 65
	pushAuthLen      = 16
	exampleKeyEnvMsg = "DOJO_VAPID_PRIVATE_KEY must be a real key in pre and pro"
)

// PushStore stores the learner's push subscriptions (design 3.10).
type PushStore interface {
	SubscribePush(ctx context.Context, in repo.SubscribePushIn) (pgtype.UUID, error)
	ListPushSubscriptions(ctx context.Context, learner pgtype.UUID) ([]repo.PushSubscriptionRow, error)
	DeletePushSubscription(ctx context.Context, learner, id pgtype.UUID) error
}

// Push serves /v1/push (design 3.10): the VAPID public key and the learner's subscriptions.
type Push struct {
	Store  PushStore
	Policy webpush.Policy
	VAPID  *webpush.VAPID
}

// PushConfig is what the push settings of the environment decide.
type PushConfig struct {
	Policy webpush.Policy
	VAPID  *webpush.VAPID
}

// ParsePush reads DOJO_VAPID_PRIVATE_KEY, DOJO_PUSH_ALLOWED_HOSTS and DOJO_PUSH_ALLOWED_ORIGINS. In pre and
// pro the key is required and must not be the example key of .env.example, and the origins (which turn the
// dial guard off for the local stand-in) must be empty. Locally an unset key falls back to the example one.
func ParsePush(get func(string) string) (PushConfig, error) {
	env := get("DOJO_ENV")
	hosted := env == "pre" || env == "pro"
	raw := strings.TrimSpace(get("DOJO_VAPID_PRIVATE_KEY"))
	if raw == "" && !hosted {
		raw = webpush.ExampleKey
	}
	if raw == "" {
		return PushConfig{}, errors.New("DOJO_VAPID_PRIVATE_KEY is required in pre and pro")
	}
	v, err := webpush.ParseVAPID(raw)
	if err != nil {
		return PushConfig{}, errors.New("DOJO_VAPID_PRIVATE_KEY is not a base64url P-256 private key")
	}
	if hosted && v.IsExample() {
		return PushConfig{}, errors.New(exampleKeyEnvMsg)
	}
	hosts := webpush.DefaultHosts
	if list := splitList(get("DOJO_PUSH_ALLOWED_HOSTS")); len(list) > 0 {
		hosts = list
	}
	origins := splitList(get("DOJO_PUSH_ALLOWED_ORIGINS"))
	if hosted && len(origins) > 0 {
		return PushConfig{}, fmt.Errorf("DOJO_PUSH_ALLOWED_ORIGINS must be empty in %s", env)
	}
	p, err := webpush.NewPolicy(hosts, origins)
	if err != nil {
		return PushConfig{}, err
	}
	return PushConfig{Policy: p, VAPID: v}, nil
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// requireCookie passes only an active cookie session: a bearer is 403 forbidden, as on the stream.
func requireCookie(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := principalOf(r.Context())
		switch {
		case !ok:
			writeError(w, http.StatusUnauthorized, "unauthenticated")
		case p.bearer:
			writeError(w, http.StatusForbidden, "forbidden")
		case p.suspended:
			writeError(w, http.StatusForbidden, "account_suspended")
		default:
			next(w, r)
		}
	}
}

// pushFailed answers a store failure. Nothing the request carried reaches the log: only the route and,
// for a database error, its SQLSTATE.
func pushFailed(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return
	}
	code := "none"
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		code = pgErr.Code
	}
	slog.Error("push request failed", "route", r.Pattern, "sqlstate", code)
	writeError(w, http.StatusInternalServerError, "server_error")
}

// key answers GET /v1/push/key.
func (p *Push) key(w http.ResponseWriter, _ *http.Request) {
	writeNoStore(w, map[string]string{"public_key": base64.RawURLEncoding.EncodeToString(p.VAPID.PublicKey())})
}

type pushSubscribeRequest struct {
	Endpoint string `json:"endpoint"`
	// ExpirationTime is part of PushSubscription.toJSON(); it is accepted and ignored.
	ExpirationTime json.RawMessage `json:"expirationTime"`
	Keys           struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	DeviceLabel string `json:"device_label"`
	TimeZone    string `json:"time_zone"`
}

// decodeKey reads strict unpadded base64url of exactly n bytes.
func decodeKey(s string, n int) ([]byte, bool) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	return b, err == nil && len(b) == n
}

func validDeviceLabel(s string) bool {
	if n := utf8.RuneCountInString(s); n < 1 || n > maxDeviceLabel || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if !unicode.IsPrint(c) {
			return false
		}
	}
	return true
}

// validZone is the shape rule of a time zone name (as parseTZ in 3.8); Postgres has the last word.
func validZone(name string) bool {
	if name == "" || len(name) > maxTZLen || name == "Local" {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

// subscribe answers POST /v1/push/subscriptions.
func (p *Push) subscribe(w http.ResponseWriter, r *http.Request) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPushBody))
	dec.DisallowUnknownFields()
	var req pushSubscribeRequest
	if err := dec.Decode(&req); err != nil || dec.Decode(&struct{}{}) != io.EOF || req.Endpoint == "" {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	pub, okP := decodeKey(req.Keys.P256dh, pushP256dhLen)
	_, okA := decodeKey(req.Keys.Auth, pushAuthLen)
	if okP {
		_, err := ecdh.P256().NewPublicKey(pub)
		okP = err == nil
	}
	if !okP || !okA || !validDeviceLabel(req.DeviceLabel) || !validZone(req.TimeZone) {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	endpoint, err := p.Policy.Normalize(req.Endpoint)
	if err != nil {
		writeError(w, http.StatusBadRequest, "endpoint_not_allowed")
		return
	}
	pr, _ := principalOf(r.Context())
	id, err := p.Store.SubscribePush(context.WithoutCancel(r.Context()), repo.SubscribePushIn{Learner: pr.learner,
		Endpoint: endpoint, P256dh: req.Keys.P256dh, Auth: req.Keys.Auth, DeviceLabel: req.DeviceLabel, TimeZone: req.TimeZone})
	switch {
	case errors.Is(err, repo.ErrBadTimeZone):
		writeError(w, http.StatusBadRequest, "bad_request")
	case errors.Is(err, repo.ErrLearnerGone):
		writeError(w, http.StatusUnauthorized, "unauthenticated")
	case err != nil:
		pushFailed(w, r, err)
	default:
		writeJSON(w, http.StatusCreated, map[string]string{"id": uuidText(id)})
	}
}

type pushSubscriptionView struct {
	ID            string  `json:"id"`
	DeviceLabel   string  `json:"device_label"`
	CreatedAt     string  `json:"created_at"`
	LastSuccessAt *string `json:"last_success_at"`
}

// list answers GET /v1/push/subscriptions.
func (p *Push) list(w http.ResponseWriter, r *http.Request) {
	pr, _ := principalOf(r.Context())
	rows, err := p.Store.ListPushSubscriptions(r.Context(), pr.learner)
	if err != nil {
		pushFailed(w, r, err)
		return
	}
	out := make([]pushSubscriptionView, 0, len(rows))
	for _, s := range rows {
		v := pushSubscriptionView{ID: uuidText(s.ID), DeviceLabel: s.DeviceLabel, CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339)}
		if s.LastSuccessAt != nil {
			t := s.LastSuccessAt.UTC().Format(time.RFC3339)
			v.LastSuccessAt = &t
		}
		out = append(out, v)
	}
	writeNoStore(w, map[string][]pushSubscriptionView{"subscriptions": out})
}

// remove answers DELETE /v1/push/subscriptions/{id}; an unknown, foreign or malformed id is one 404.
func (p *Push) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := parseCanonicalUUID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	pr, _ := principalOf(r.Context())
	err := p.Store.DeletePushSubscription(context.WithoutCancel(r.Context()), pr.learner, id)
	switch {
	case errors.Is(err, repo.ErrPushNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case err != nil:
		pushFailed(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
