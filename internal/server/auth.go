package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/server/repo"
)

const (
	// SessionCookie is the session cookie name (design 3.1).
	SessionCookie = "dojo_session"
	// CSRFHeader must be present on every state-changing request authenticated by the cookie.
	CSRFHeader = "X-Dojo-Csrf"

	sessionTTL = 30 * 24 * time.Hour
	// touchEvery bounds the sliding-expiry write: at most one UPDATE per session per interval.
	touchEvery = time.Minute
	// tokenLen is the length of 32 random bytes in unpadded base64url.
	tokenLen = 43
)

// SessionStore is the data the session middleware and /v1/me need.
type SessionStore interface {
	GetActiveSession(ctx context.Context, tokenHash []byte) (repo.GetActiveSessionRow, error)
	TouchSession(ctx context.Context, id pgtype.UUID) error
	RevokeSession(ctx context.Context, id pgtype.UUID) error
	GetActiveCLIToken(ctx context.Context, tokenHash []byte) (repo.GetActiveCLITokenRow, error)
	TouchCLIToken(ctx context.Context, id pgtype.UUID) error
	RevokeCLIToken(ctx context.Context, id pgtype.UUID) error
	ListLearnerCLITokens(ctx context.Context, learnerID pgtype.UUID) ([]repo.ListLearnerCLITokensRow, error)
	RevokeLearnerCLIToken(ctx context.Context, arg repo.RevokeLearnerCLITokenParams) (int64, error)
	GetLearnerByID(ctx context.Context, id pgtype.UUID) (repo.Learner, error)
	UpdateLearnerProfile(ctx context.Context, arg repo.UpdateLearnerProfileParams) (repo.Learner, error)
	SetLearnerEmail(ctx context.Context, id pgtype.UUID, email string) (repo.Learner, bool, error)
	MarkFirstRunSeen(ctx context.Context, id pgtype.UUID) (repo.Learner, error)
	DeleteLearner(ctx context.Context, id pgtype.UUID) (bool, error)
	InsertTelemetryEvents(ctx context.Context, arg repo.InsertTelemetryEventsParams) error
}

// Auth authenticates requests by session cookie or CLI bearer token.
type Auth struct {
	Store   SessionStore
	Catalog CatalogStore // nil leaves GET /v1/catalog, /v1/path, /v1/today and /v1/activity out
	Bundles *Bundles     // nil leaves GET /v1/tickets/{id}/bundle out
	Secure  bool         // cookie Secure flag, from PUBLIC_ORIGIN
	// Events backs POST /v1/events; nil leaves the route out.
	Events EventStore
	// Stream backs GET /v1/stream; nil leaves the route out. StreamHeartbeat defaults to 25 s.
	Stream          *Hub
	StreamHeartbeat time.Duration
	// Tickets backs GET /v1/tickets/{id} and POST /v1/tickets/{id}/hints/{n}; nil leaves them out.
	Tickets *Tickets
	// Onboarding backs GET /v1/onboarding (it also needs Catalog); nil leaves the route out.
	Onboarding *Onboarding
	// Reports backs POST /v1/reports; nil leaves the route out.
	Reports ReportStore
	// Items backs GET /v1/items/{id}; nil leaves the route out.
	Items *Items
	// Inbox backs /v1/inbox; nil leaves the routes out.
	Inbox *Inbox
	// Push backs /v1/push; nil leaves the routes out.
	Push *Push
}

// ParseOrigin validates PUBLIC_ORIGIN for the environment (local, pre or pro) and reports
// whether session cookies must be Secure. pre and pro refuse a non-https origin (design 3.1).
func ParseOrigin(env, origin string) (secure bool, err error) {
	switch env {
	case "local", "pre", "pro":
	default:
		return false, fmt.Errorf("DOJO_ENV must be local, pre or pro, got %q", env)
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false, fmt.Errorf("PUBLIC_ORIGIN must be an http or https origin, got %q", origin)
	}
	if u.Scheme != "https" && env != "local" {
		return false, fmt.Errorf("PUBLIC_ORIGIN must be https in %s, got %q", env, origin)
	}
	return u.Scheme == "https", nil
}

// principal is the learner a request acts as. A cookie principal carries session, a bearer
// principal carries cliToken and bearer: the two never mix on one request.
type principal struct {
	learner   pgtype.UUID
	session   pgtype.UUID
	cliToken  pgtype.UUID
	bearer    bool
	suspended bool
}

type ctxKey struct{}

func principalOf(ctx context.Context) (principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(principal)
	return p, ok
}

// HashToken is how a session token is stored: sha256 of the cookie value as sent.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Session resolves the credential into a principal on the request context. A request carries one
// credential: with an Authorization header the bearer token decides and any session cookie is
// ignored (never both, so the CSRF exemption of bearer calls cannot cover a cookie call). A
// non-empty Authorization header that is not a valid, active CLI token is 401, never anonymous.
// Without the header the cookie is resolved; no cookie, or a bad one, leaves the request anonymous:
// routes that need a learner use requireSession. POST /v1/auth/cli is the exception that proves
// the rule: it carries the GitHub token in its body, so it ignores both credentials.
func (a Auth) Session(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch h := r.Header.Values("Authorization"); {
		case r.Method == http.MethodPost && r.URL.Path == cliPath:
		case len(h) == 0 || (len(h) == 1 && h[0] == ""):
			if p, ok := a.lookup(r); ok {
				r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, p))
			}
		default:
			p, status := a.lookupBearer(r, h)
			if status != 0 {
				name := "unauthenticated"
				if status != http.StatusUnauthorized {
					name = "server_error"
				}
				writeError(w, status, name)
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, p))
		}
		next.ServeHTTP(w, r)
	})
}

func (a Auth) lookup(r *http.Request) (principal, bool) {
	c, err := r.Cookie(SessionCookie)
	if err != nil || len(c.Value) != tokenLen {
		return principal{}, false
	}
	row, err := a.Store.GetActiveSession(r.Context(), HashToken(c.Value))
	if err != nil {
		return principal{}, false
	}
	if time.Since(row.LastUsedAt.Time) > touchEvery {
		if err := a.Store.TouchSession(context.WithoutCancel(r.Context()), row.ID); err != nil {
			slog.Warn("touch session failed", "error", err)
		}
	}
	return principal{learner: row.LearnerID, session: row.ID, suspended: row.SuspendedAt.Valid}, true
}

// lookupBearer resolves "Authorization: Bearer <cli token>". It returns the HTTP status to refuse
// with, or 0 for a principal. The token shape is checked before the database is asked.
func (a Auth) lookupBearer(r *http.Request, header []string) (principal, int) {
	token, ok := parseBearer(header)
	if !ok {
		return principal{}, http.StatusUnauthorized
	}
	row, err := a.Store.GetActiveCLIToken(r.Context(), HashToken(token))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return principal{}, http.StatusUnauthorized
	case errors.Is(err, context.Canceled):
		return principal{}, http.StatusServiceUnavailable
	case err != nil:
		slog.Error("bearer lookup failed", "error", err)
		return principal{}, http.StatusInternalServerError
	}
	if time.Since(row.LastUsedAt.Time) > touchEvery {
		if err := a.Store.TouchCLIToken(context.WithoutCancel(r.Context()), row.ID); err != nil {
			slog.Warn("touch cli token failed", "error", err)
		}
	}
	return principal{learner: row.LearnerID, cliToken: row.ID, bearer: true, suspended: row.SuspendedAt.Valid}, 0
}

// parseBearer accepts exactly one header of the form "Bearer dojo_<43 base64url characters>".
func parseBearer(header []string) (string, bool) {
	if len(header) != 1 || len(header[0]) < 7 || !strings.EqualFold(header[0][:7], "bearer ") {
		return "", false
	}
	return header[0][7:], validCLIToken(header[0][7:])
}

// CSRF requires a JSON content type and the custom header on state-changing requests
// authenticated by the cookie. Requests without a cookie principal (bearer, anonymous) pass.
func CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// start is a browser form POST that acts on no session; a signed-in browser must still reach it.
		// events is bearer only: a cookie call reaches the handler to be answered 401, never to act.
		if p, ok := principalOf(r.Context()); ok && !p.bearer && !safeMethod(r.Method) && r.URL.Path != startPath && r.URL.Path != eventsPath {
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mt != "application/json" || r.Header.Get(CSRFHeader) == "" {
				writeError(w, http.StatusForbidden, "csrf_rejected")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// requireSession passes only an active, unsuspended learner.
func requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := principalOf(r.Context())
		switch {
		case !ok:
			writeError(w, http.StatusUnauthorized, "unauthenticated")
		case p.suspended:
			writeError(w, http.StatusForbidden, "account_suspended")
		default:
			next(w, r)
		}
	}
}

// requireAuthenticated passes any resolved principal, suspended or not, for self-service routes
// that only end the caller's own credentials (like sign-out). CSRF has already run.
func requireAuthenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := principalOf(r.Context()); !ok {
			writeError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		next(w, r)
	}
}

func writeError(w http.ResponseWriter, code int, name string) {
	writeJSON(w, code, map[string]string{"error": name})
}

type meResponse struct {
	ID               string  `json:"id"`
	Username         string  `json:"username"`
	GithubLogin      string  `json:"github_login"`
	GithubID         int64   `json:"github_id"`
	Email            *string `json:"email"`
	Language         string  `json:"language"`
	UsageStats       bool    `json:"usage_stats"`
	AnalyticsConsent *bool   `json:"analytics_consent"`
	HostedDefault    bool    `json:"hosted_default"`
}

func (a Auth) me(w http.ResponseWriter, r *http.Request) {
	p, _ := principalOf(r.Context())
	l, err := a.Store.GetLearnerByID(r.Context(), p.learner)
	if errors.Is(err, context.Canceled) {
		return
	}
	if err != nil {
		// A learner deleted since the session lookup is the only expected miss.
		slog.Warn("load learner failed", "error", err)
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	writeJSON(w, http.StatusOK, newMeResponse(l))
}

func newMeResponse(l repo.Learner) meResponse {
	out := meResponse{
		ID: l.ID.String(), Username: l.Username, GithubLogin: l.GithubLogin, GithubID: l.GithubID,
		Language: l.Language, UsageStats: l.UsageStats, HostedDefault: l.HostedDefault,
	}
	if l.Email.Valid {
		out.Email = &l.Email.String
	}
	if l.AnalyticsConsent.Valid {
		out.AnalyticsConsent = &l.AnalyticsConsent.Bool
	}
	return out
}

// cliLogout ends the CLI token the request presented (`dojo logout`). Only a bearer principal can:
// a cookie session signs out through logout. Suspended learners can still sign out.
func (a Auth) cliLogout(w http.ResponseWriter, r *http.Request) {
	p, ok := principalOf(r.Context())
	if !ok || !p.bearer {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if err := a.Store.RevokeCLIToken(context.WithoutCancel(r.Context()), p.cliToken); err != nil {
		slog.Error("cli logout: revoking the token failed", "error", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// logout ends the session Session resolved (never the cookie itself, so the CSRF check has
// already run) and clears the cookie. Suspended learners can still sign out.
func (a Auth) logout(w http.ResponseWriter, r *http.Request) {
	p, ok := principalOf(r.Context())
	if !ok || p.bearer {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if err := a.Store.RevokeSession(context.WithoutCancel(r.Context()), p.session); err != nil {
		slog.Error("logout: revoking the session failed", "error", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	http.SetCookie(w, a.ClearCookie())
	w.WriteHeader(http.StatusNoContent)
}

// SetCookie returns the session cookie for a freshly generated token.
func (a Auth) SetCookie(token string) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // Secure follows PUBLIC_ORIGIN by design (3.1)
		Name: SessionCookie, Value: token, Path: "/", MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.Secure,
	}
}

// ClearCookie returns the cookie that removes the session cookie.
func (a Auth) ClearCookie() *http.Cookie {
	c := a.SetCookie("") //nolint:gosec // see SetCookie
	c.MaxAge = -1
	return c
}
