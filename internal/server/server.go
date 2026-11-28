// Package server holds the HTTP routes of the Dojo API.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// healthTimeout bounds the database check so a hung database cannot hang the probe.
const healthTimeout = 2 * time.Second

// Readiness reports whether the API's dependencies are usable.
type Readiness interface {
	Ready(ctx context.Context) error
}

// logoutPath ends the cookie session (design 3.1).
const logoutPath = "/v1/auth/logout"

// New returns the API router.
// signin may be nil, which leaves the web sign-in routes out.
// cli may be nil, which leaves the CLI login route out (DELETE /v1/auth/cli, the CLI's own sign-out, stays).
func New(ready Readiness, auth Auth, limits *RateLimits, signin *SignIn, cli *CLILogin) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", health(ready))
	mux.HandleFunc("GET /v1/me", requireSession(auth.me))
	mux.HandleFunc("PATCH /v1/me", requireSession(auth.patchMe))
	mux.HandleFunc("DELETE /v1/me", requireAuthenticated(auth.deleteMe))
	mux.HandleFunc("GET /v1/me/devices", requireSession(auth.devices))
	mux.HandleFunc("DELETE /v1/me/devices/{id}", requireAuthenticated(auth.revokeDevice))
	mux.HandleFunc("POST /v1/telemetry", auth.telemetry)
	if auth.Catalog != nil {
		mux.HandleFunc("GET /v1/catalog", requireSession(auth.catalog))
		mux.HandleFunc("GET /v1/path", requireSession(auth.path))
		mux.HandleFunc("GET /v1/today", requireSession(auth.today))
		mux.HandleFunc("GET /v1/activity", requireSession(auth.activity))
		mux.HandleFunc("GET /v1/practice", requireSession(auth.practice))
		mux.HandleFunc("GET /v1/belt", requireSession(auth.belt))
		if auth.Onboarding != nil {
			mux.HandleFunc("GET "+onboardingPath, requireSession(auth.onboarding))
			if auth.Onboarding.Confirmer != nil {
				mux.HandleFunc("PUT "+confirmRoute, requireSession(auth.confirmStep(true)))
				mux.HandleFunc("DELETE "+confirmRoute, requireSession(auth.confirmStep(false)))
			}
		}
	}
	if auth.Events != nil {
		mux.HandleFunc("POST "+eventsPath, auth.postEvents)
	}
	if auth.Bundles != nil || auth.Tickets != nil {
		// A ticket id holds a slash, so the routes are the subtree and the dispatchers split the path.
		mux.HandleFunc("GET "+ticketsPrefix, requireSession(auth.getTicketRoute))
	}
	if auth.Tickets != nil {
		mux.HandleFunc("POST "+ticketsPrefix, requireSession(auth.postTicketRoute))
	}
	if auth.Items != nil {
		mux.HandleFunc("GET "+itemsPrefix, requireSession(auth.getItemRoute))
		mux.HandleFunc("POST "+itemsPrefix, requireSession(auth.postItemRoute))
		mux.HandleFunc("PUT "+itemsPrefix, requireSession(auth.putItemRoute))
	}
	if auth.Inbox != nil {
		mux.HandleFunc("GET /v1/inbox", requireSession(auth.Inbox.list))
		mux.HandleFunc("GET /v1/inbox/{id}", requireSession(auth.Inbox.get))
		mux.HandleFunc("POST /v1/inbox/{id}/read", requireSession(auth.Inbox.read))
		mux.HandleFunc("POST /v1/inbox/read-all", requireSession(auth.Inbox.readAll))
	}
	if auth.Reports != nil {
		mux.HandleFunc("POST "+reportsPath, requireSession(auth.postReport))
	}
	if auth.Stream != nil {
		mux.HandleFunc("GET "+streamPath, auth.stream)
	}
	mux.HandleFunc("POST "+logoutPath, auth.logout)
	mux.HandleFunc("DELETE "+cliPath, auth.cliLogout)
	if cli != nil {
		mux.HandleFunc("POST "+cliPath, cli.login)
	}
	if signin != nil {
		signin.limits = limits
		mux.HandleFunc("POST "+startPath, signin.start)
		mux.HandleFunc("GET "+callbackPath, signin.callback)
		mux.HandleFunc("POST "+pathChoicePath, signin.postPath)
		mux.HandleFunc("POST "+pathWaitlistPath, signin.postWaitlist)
		mux.HandleFunc(pathWaitlistRoute, signin.deleteWaitlist)
	}
	return auth.Session(limits.wrap(mux, CSRF(mux)))
}

func health(ready Readiness) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
		defer cancel()
		if err := ready.Ready(ctx); err != nil {
			slog.Warn("health check failed", "error", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
