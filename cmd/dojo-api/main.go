// Command dojo-api serves the Dojo HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kibanyadev/dojo/internal/github"
	"github.com/kibanyadev/dojo/internal/mail"
	"github.com/kibanyadev/dojo/internal/server"
	"github.com/kibanyadev/dojo/internal/server/repo"
	"github.com/kibanyadev/dojo/internal/storage"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("dojo-api failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	addr := envOr("LISTEN_ADDR", ":8080")
	switch {
	case len(args) == 1 && args[0] == "healthcheck":
		return healthcheck(addr)
	case len(args) > 0:
		return fmt.Errorf("unknown arguments %q: usage: dojo-api [healthcheck]", args)
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	secure, err := server.ParseOrigin(os.Getenv("DOJO_ENV"), os.Getenv("PUBLIC_ORIGIN"))
	if err != nil {
		return err
	}
	hops, err := server.ParseTrustedHops(os.Getenv("TRUSTED_PROXY_HOPS"))
	if err != nil {
		return err
	}
	local := os.Getenv("DOJO_ENV") == "local"
	ghCfg, err := github.LoadConfig(os.Getenv, local)
	if err != nil {
		return err
	}
	if err := github.RequireReadToken(os.Getenv("DOJO_ENV"), ghCfg); err != nil {
		return err
	}
	stateKey, err := server.ParseStateKey(os.Getenv("AUTH_STATE_KEY"), local)
	if err != nil {
		return err
	}
	termsVersion, err := server.ParseTermsVersion(os.Getenv("TERMS_VERSION"))
	if err != nil {
		return err
	}
	storeCfg, err := storage.LoadConfig(os.Getenv, local)
	if err != nil {
		return err
	}
	objects, err := storage.New(storeCfg)
	if err != nil {
		return err
	}
	slog.Info("object store configured", "bucket", storeCfg.Bucket, "endpoint", storeCfg.Endpoint, "public_endpoint", storeCfg.PublicEndpoint)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := repo.Open(ctx, dbURL)
	if err != nil {
		return err
	}
	defer store.Close()

	heartbeat, err := server.ParseStreamHeartbeat(os.Getenv("STREAM_HEARTBEAT_SECONDS"))
	if err != nil {
		return err
	}
	flagTTL, err := server.ParseFlagCacheTTL(os.Getenv("DOJO_FLAG_CACHE_SECONDS"))
	if err != nil {
		return err
	}
	hub := server.NewHub(store.ListenConn)
	go hub.Run(ctx)
	go server.RunExpiry(ctx, store, server.ExpiryInterval)
	if err := startOutbox(ctx, store); err != nil {
		return err
	}

	gh := github.New(ghCfg)
	verifyEvery, verifyBatch, err := server.ParseCommitVerify(os.Getenv)
	if err != nil {
		return err
	}
	go server.RunCommitVerifier(ctx, &server.CommitVerifier{Store: store, GitHub: gh, Batch: verifyBatch}, verifyEvery)

	auth := server.Auth{Store: store.Queries(), Catalog: store.Queries(),
		Bundles: &server.Bundles{Store: store.Queries(), Signer: objects}, Secure: secure, Events: store,
		Stream: hub, StreamHeartbeat: heartbeat,
		Tickets: &server.Tickets{Reader: store.Queries(), Opener: store}, Items: &server.Items{Reader: store.Queries(), Answerer: store, Chooser: store, Drafter: store, Submitter: store, FlagTTL: flagTTL}, Reports: store,
		Onboarding: &server.Onboarding{Facts: store.Queries(), Doctor: store, Confirmer: store,
			Env: os.Getenv("DOJO_ENV"), PublicOrigin: strings.TrimRight(os.Getenv("PUBLIC_ORIGIN"), "/")}}
	cli := &server.CLILogin{GitHub: gh, Store: store.Queries(), Tokens: store}
	signin := &server.SignIn{GitHub: gh, Store: store.Queries(), Auth: auth,
		Origin: strings.TrimRight(os.Getenv("PUBLIC_ORIGIN"), "/"), Key: stateKey, TermsVersion: termsVersion}
	srv := &http.Server{
		Addr:              addr,
		Handler:           server.New(store, auth, server.NewRateLimits(hops), signin, cli),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	srv.RegisterOnShutdown(hub.Shutdown) // open streams never end on their own
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("listening", "addr", addr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// healthcheck probes the local server; the scratch image has no curl for the compose check.
func healthcheck(addr string) error {
	if addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/v1/health", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health returned %d", resp.StatusCode)
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// startOutbox runs the email sender (design 3.3). With no DOJO_SMTP_HOST it logs that mail is off and
// returns: queued mail then waits in email_outbox.
func startOutbox(ctx context.Context, store *repo.Store) error {
	reportsTo := os.Getenv("DOJO_REPORTS_TO")
	switch {
	case reportsTo == "" && os.Getenv("DOJO_ENV") != "local":
		return errors.New("DOJO_REPORTS_TO is required outside local")
	case reportsTo != "" && !mail.ValidAddress(reportsTo):
		return errors.New("DOJO_REPORTS_TO must be a bare email address")
	}
	host := os.Getenv("DOJO_SMTP_HOST")
	if host == "" {
		slog.Warn("DOJO_SMTP_HOST is not set: queued email will not be sent")
		return nil
	}
	domain := os.Getenv("DOJO_EMAIL_DOMAIN")
	if domain == "" {
		return errors.New("DOJO_EMAIL_DOMAIN is required with DOJO_SMTP_HOST")
	}
	smtp := &mail.SMTP{Host: host, Port: envOr("DOJO_SMTP_PORT", "587"),
		Username: os.Getenv("DOJO_SMTP_USERNAME"), Password: os.Getenv("DOJO_SMTP_PASSWORD")}
	o := &server.Outbox{Queue: store, Reader: store.Queries(), Mail: smtp, Domain: domain, Origin: os.Getenv("PUBLIC_ORIGIN")}
	slog.Info("email sender configured", "host", host, "port", smtp.Port, "domain", domain)
	go server.RunOutbox(ctx, o, server.OutboxInterval)
	if reportsTo == "" {
		slog.Warn("DOJO_REPORTS_TO is not set: reports are stored and not forwarded")
		return nil
	}
	go server.RunReportForwarder(ctx, &server.ReportForwarder{Queue: store, Mail: smtp, To: reportsTo, Domain: domain}, server.OutboxInterval)
	return nil
}
