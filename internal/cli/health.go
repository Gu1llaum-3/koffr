package cli

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Gu1llaum-3/koffr/internal/catalog"
	"github.com/Gu1llaum-3/koffr/internal/config"
	"github.com/Gu1llaum-3/koffr/internal/httpapi"
	"github.com/Gu1llaum-3/koffr/internal/scheduler"
)

// serveHealth starts the health listener, and returns a function that stops it.
//
// It is off unless configured. A backup tool that opened a port by default
// would be a backup tool people run with a firewall rule they wrote in a hurry.
func (a *app) serveHealth(
	ctx context.Context, cfg config.Config, jobs []scheduler.Job, running *atomic.Bool,
) (stop func(), err error) {
	if cfg.HTTP.Listen == "" {
		return func() {}, nil
	}

	srv := &http.Server{
		Addr:              cfg.HTTP.Listen,
		Handler:           httpapi.Handler(a.health(cfg, jobs, running)),
		ReadHeaderTimeout: 5 * time.Second,
	}

	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", cfg.HTTP.Listen)
	if err != nil {
		// Refused rather than warned about. Someone who configured a health
		// endpoint has a supervisor pointed at it, and a supervisor pointed at
		// a port nothing is listening on alarms tonight.
		return nil, &Fault{Code: ExitConfig, Err: err}
	}
	a.printf("health endpoints on http://%s (/healthz, /readyz, /api/v1/status)", listener.Addr())

	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.warnf("koffr: the health listener stopped: %v", err)
		}
	}()

	return func() {
		// Its own context: the caller's is already cancelled by the time this
		// runs, and a cancelled context makes Shutdown return immediately
		// without waiting for a probe in flight.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}, nil
}

// health assembles what the endpoints report on.
func (a *app) health(cfg config.Config, jobs []scheduler.Job, running *atomic.Bool) httpapi.Health {
	specs := make(map[string]string, len(jobs))
	for _, j := range jobs {
		specs[j.SourceID] = j.Spec
	}

	checks := map[string]func(context.Context) error{
		"catalog": func(ctx context.Context) error {
			cat, err := openCatalog(ctx, cfg)
			if err != nil {
				return err
			}
			defer func() { _ = cat.Close() }()
			_, err = cat.Overview(ctx)
			return err
		},
	}
	for _, name := range sortedKeys(cfg.Destinations) {
		dest := cfg.Destinations[name]
		checks["destination "+name] = func(ctx context.Context) error {
			st, err := openStorage(ctx, dest)
			if err != nil {
				return err
			}
			// Listing proves credentials and reachability without writing into
			// someone's repository as a side effect of a health probe.
			for _, err := range st.List(ctx, storagePrefixForProbe) {
				return err
			}
			return nil
		}
	}

	return httpapi.Health{
		Now:              time.Now,
		SchedulerRunning: running.Load,
		Checks:           checks,
		Sources: func(ctx context.Context) ([]httpapi.SourceStatus, error) {
			return sourceStatuses(ctx, cfg, specs)
		},
	}
}

// storagePrefixForProbe is a prefix that exists in every repository, so the
// listing costs one round trip and returns almost nothing.
const storagePrefixForProbe = "sources/"

// sourceStatuses answers EF-134 for every configured source.
//
// Every source, not only the scheduled ones: a source somebody forgot to give a
// schedule is exactly the one whose absence of backups should be visible.
func sourceStatuses(
	ctx context.Context, cfg config.Config, specs map[string]string,
) ([]httpapi.SourceStatus, error) {
	cat, err := openCatalog(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cat.Close() }()

	backups, err := cat.ListBackups(ctx, catalog.BackupFilter{Status: catalog.StatusCompleted})
	if err != nil {
		return nil, err
	}
	latest := map[string]time.Time{}
	for _, b := range backups {
		if at, seen := latest[b.SourceID]; !seen || b.StartedAt.After(at) {
			latest[b.SourceID] = b.StartedAt
		}
	}

	now := time.Now().In(cfg.Scheduler.Location())
	out := make([]httpapi.SourceStatus, 0, len(cfg.SourceIDs()))
	for _, id := range cfg.SourceIDs() {
		last, ever := latest[id]
		status := httpapi.SourceStatus{ID: id, Schedule: specs[id], LastSuccessAt: last}

		spec, scheduled := specs[id]
		if !scheduled {
			// Nothing to be late for. A source backed up by hand is not stale
			// because nobody said when it should happen.
			out = append(out, status)
			continue
		}
		// The scheduler's own rule, so "stale" here and "catch up" there can
		// never disagree about whether something was missed.
		if stale, err := scheduler.MissedWindow(spec, last, ever, now); err == nil {
			status.Stale = stale
		}
		if next, err := scheduler.NextRun(spec, now); err == nil {
			status.NextRunAt = next
		}
		out = append(out, status)
	}
	return out, nil
}
