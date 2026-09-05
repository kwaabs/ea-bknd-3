package etl

import (
	"bknd-3/internal/config"
	"bknd-3/internal/logger"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/uptrace/bun"
	"go.uber.org/zap"
)

// Engine schedules and runs ETL jobs. One goroutine per enabled job
// computes and waits for its next trigger time (mirroring the
// time.Until/time.NewTimer idiom scheduler.StartDailySessionReset already
// uses for its single fixed-time job, generalized here to several
// trigger_times per job); at each firing it dispatches the job onto a
// channel drained by a bounded pool of worker goroutines, so no more than
// Engine.workers extracts ever run at once regardless of how many jobs
// happen to fire around the same time.
type Engine struct {
	db             *bun.DB
	logr           *logger.Logger
	workers        int
	reload_        time.Duration
	dispatch       chan Job
	credentialsKey string // config.ETLCredentialsKey — see crypto.go

	mu          sync.Mutex
	jobsByID    map[string]Job
	sourcesByID map[string]Source
	sourcePools map[string]*sourceConn        // source ID -> reused connection (SQL pool or HTTP creds)
	cancelFuncs map[string]context.CancelFunc // job ID -> stops its scheduling goroutine
	running     map[string]bool               // job ID -> a run is currently in flight
}

// Start builds an Engine and launches its worker pool and reload loop as
// background goroutines. Returns immediately; everything stops when ctx is
// cancelled. Safe to call even if app.etl_sources/app.etl_jobs don't exist
// yet (sql/etl_engine.sql not applied) or are empty — the reload loop logs
// and simply schedules nothing, the same graceful-degradation shape this
// codebase already uses for an unreachable Redis cache in cmd/server/main.go.
func Start(ctx context.Context, db *bun.DB, cfg *config.Config, logr *logger.Logger) *Engine {
	workers := cfg.ETLWorkers
	if workers <= 0 {
		workers = 3
	}
	reloadEvery := cfg.ETLReloadInterval
	if reloadEvery <= 0 {
		reloadEvery = 5 * time.Minute
	}

	if cfg.ETLCredentialsKey == "" {
		logr.Warn("etl: ETL_CREDENTIALS_ENCRYPTION_KEY is not set — any source with a password will fail to connect")
	}

	e := &Engine{
		db:             db,
		logr:           logr,
		workers:        workers,
		reload_:        reloadEvery,
		dispatch:       make(chan Job, 32),
		credentialsKey: cfg.ETLCredentialsKey,
		jobsByID:       make(map[string]Job),
		sourcesByID:    make(map[string]Source),
		sourcePools:    make(map[string]*sourceConn),
		cancelFuncs:    make(map[string]context.CancelFunc),
		running:        make(map[string]bool),
	}

	for i := 0; i < workers; i++ {
		go e.worker(ctx, i)
	}
	go e.reloadLoop(ctx)

	logr.Info("etl: engine started", zap.Int("workers", workers), zap.Duration("reload_interval", reloadEvery))
	return e
}

func (e *Engine) reloadLoop(ctx context.Context) {
	e.reload(ctx)
	ticker := time.NewTicker(e.reload_)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.reload(ctx)
		}
	}
}

// reload re-reads enabled sources and jobs from Postgres, starts a
// scheduling goroutine for any newly-enabled job, and stops one for any
// job that's been disabled or deleted since the last reload. An edit to an
// already-scheduled job's fields (trigger_times, source_query, batch size,
// ...) is picked up by its existing goroutine on its own next loop
// iteration — see scheduleJob — without needing to restart it here.
func (e *Engine) reload(ctx context.Context) {
	var sources []Source
	if err := e.db.NewSelect().Model(&sources).Where("enabled").Scan(ctx); err != nil {
		e.logr.Error("etl: failed to load app.etl_sources (not applied yet? see sql/etl_engine.sql)", zap.Error(err))
		return
	}
	sourcesByID := make(map[string]Source, len(sources))
	for _, s := range sources {
		sourcesByID[s.ID] = s
	}

	var jobs []Job
	if err := e.db.NewSelect().Model(&jobs).Where("enabled").Scan(ctx); err != nil {
		e.logr.Error("etl: failed to load app.etl_jobs", zap.Error(err))
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.sourcesByID = sourcesByID
	jobsByID := make(map[string]Job, len(jobs))
	seen := make(map[string]bool, len(jobs))
	for _, job := range jobs {
		jobsByID[job.ID] = job
		seen[job.ID] = true
		if _, scheduled := e.cancelFuncs[job.ID]; !scheduled {
			jobCtx, cancel := context.WithCancel(ctx)
			e.cancelFuncs[job.ID] = cancel
			e.scheduleJob(jobCtx, job.ID)
			e.logr.Info("etl: scheduled job", zap.String("job", job.Name), zap.Strings("trigger_times", job.TriggerTimes))
		}
	}
	e.jobsByID = jobsByID

	for id, cancel := range e.cancelFuncs {
		if !seen[id] {
			cancel()
			delete(e.cancelFuncs, id)
			e.logr.Info("etl: unscheduled job (disabled or removed)", zap.String("job_id", id))
		}
	}
}

// scheduleJob runs for the lifetime of one enabled job: wait until its
// next trigger_times entry, dispatch, repeat. Re-reads the job from
// jobsByID both when computing the next trigger and right before
// dispatching, so an edit made while it's waiting takes effect on the next
// iteration rather than requiring a restart.
func (e *Engine) scheduleJob(ctx context.Context, jobID string) {
	go func() {
		for {
			job, ok := e.getJob(jobID)
			if !ok {
				return
			}
			if len(job.TriggerTimes) == 0 {
				// A deliberate, valid state (see trigger_times's comment in
				// sql/etl_engine.sql) — a manual-only job, not a
				// misconfigured one. Nothing to schedule; the goroutine
				// just exits, and "Run now" (Engine.TriggerNow) is
				// unaffected since it never consults trigger_times.
				e.logr.Info("etl: job has no trigger_times — manual-only, will not run automatically", zap.String("job", job.Name))
				return
			}
			next, err := nextTriggerTime(job.TriggerTimes, time.Now().UTC())
			if err != nil {
				e.logr.Error("etl: invalid trigger_times, job will not run", zap.String("job", job.Name), zap.Error(err))
				return
			}

			timer := time.NewTimer(time.Until(next))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				job, ok := e.getJob(jobID)
				if !ok {
					return
				}
				select {
				case e.dispatch <- job:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
}

func (e *Engine) getJob(jobID string) (Job, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	job, ok := e.jobsByID[jobID]
	return job, ok
}

func (e *Engine) worker(ctx context.Context, id int) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-e.dispatch:
			e.runOnce(ctx, job)
		}
	}
}

// runOnce guards against overlapping runs of the same job (a slow run
// still in progress when its next trigger_times entry fires is skipped,
// not queued or run concurrently with itself), opens/reuses that job's
// source connection pool, and runs it under its own timeout_seconds.
func (e *Engine) runOnce(ctx context.Context, job Job) {
	if !e.tryLock(job.ID) {
		e.logr.Warn("etl: skipping trigger, previous run still in progress", zap.String("job", job.Name))
		return
	}
	defer e.unlock(job.ID)

	sourceDB, err := e.sourceFor(ctx, job.SourceID)
	if err != nil {
		e.logr.Error("etl: cannot reach source", zap.String("job", job.Name), zap.Error(err))
		return
	}

	timeout := time.Duration(job.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = time.Hour
	}
	runCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	started := time.Now()
	result, err := runJob(runCtx, e.db, sourceDB, job)
	elapsed := time.Since(started)

	if err != nil {
		e.logr.Error("etl: job failed",
			zap.String("job", job.Name), zap.Error(err), zap.Duration("elapsed", elapsed))
		return
	}
	e.logr.Info("etl: job complete",
		zap.String("job", job.Name),
		zap.Int64("rows_extracted", result.RowsExtracted),
		zap.Int64("rows_loaded", result.RowsLoaded),
		zap.Duration("elapsed", elapsed))
}

func (e *Engine) tryLock(jobID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running[jobID] {
		return false
	}
	e.running[jobID] = true
	return true
}

func (e *Engine) unlock(jobID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.running, jobID)
}

// sourceFor returns a cached *sourceConn for a source, opening one on
// first use. Decrypting the source's secret is a real Postgres round trip
// (unlike sql.Open, which only constructs the pool object and doesn't
// dial anything), so it happens OUTSIDE e.mu — only the map reads/writes
// around it are guarded, avoiding a slow decrypt (or an external source
// actually being dialed inside sql.Open's DSN validation) blocking every
// other Engine operation. A second caller racing to open the same
// first-use source is resolved by re-checking the cache once the new pool
// is ready, keeping whichever one won. A source disabled/deleted after its
// pool was opened keeps being served from cache until the process
// restarts; not reconciled here since jobs are already filtered to
// `enabled` in reload (a v1 simplification, not a correctness issue for
// the common case of a job simply being disabled rather than its source
// ripped out from under it).
func (e *Engine) sourceFor(ctx context.Context, sourceID string) (*sourceConn, error) {
	e.mu.Lock()
	if db, ok := e.sourcePools[sourceID]; ok {
		e.mu.Unlock()
		return db, nil
	}
	src, ok := e.sourcesByID[sourceID]
	e.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("etl: source %s not found or disabled", sourceID)
	}

	password, err := decryptPassword(ctx, e.db, e.credentialsKey, src.PasswordEncrypted)
	if err != nil {
		return nil, err
	}
	db, err := openSource(src, password)
	if err != nil {
		return nil, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if existing, ok := e.sourcePools[sourceID]; ok {
		_ = db.Close()
		return existing, nil
	}
	e.sourcePools[sourceID] = db
	return db, nil
}

// openSourceByID fetches a source fresh from Postgres by id (ignoring the
// enabled flag — a human explicitly testing or manually running a job
// should be able to do so against a source that's still being set up, not
// yet flipped on for the nightly schedule) and opens a throwaway
// connection to it. Unlike sourceFor (used by the scheduler), this never
// reads or writes Engine's pooled sourcePools cache — every call opens
// fresh so it always reflects the source's current config; the caller
// owns closing the returned *sourceConn.
func (e *Engine) openSourceByID(ctx context.Context, sourceID string) (*sourceConn, error) {
	src := new(Source)
	if err := e.db.NewSelect().Model(src).Where("id = ?", sourceID).Scan(ctx); err != nil {
		return nil, fmt.Errorf("etl: source not found: %w", err)
	}
	password, err := decryptPassword(ctx, e.db, e.credentialsKey, src.PasswordEncrypted)
	if err != nil {
		return nil, err
	}
	return openSource(*src, password)
}

// TriggerNow runs a job immediately, outside its normal schedule — the
// admin API's "Run now" action. Works regardless of the job's or its
// source's enabled flag (the point is letting an operator validate a job
// before flipping it on for the nightly schedule). Fetches the job fresh
// from Postgres rather than Engine's cache, so it reflects any edit made
// moments ago that hasn't reached the next reload cycle.
//
// Returns as soon as the run-history row exists (fast) — the actual
// extract+load happens in a background goroutine, since it can run far
// longer than an HTTP request should stay open. Callers poll
// app.etl_job_runs (Service.ListRuns) by the returned run id for status.
func (e *Engine) TriggerNow(ctx context.Context, jobID string) (int64, error) {
	job := new(Job)
	if err := e.db.NewSelect().Model(job).Where("id = ?", jobID).Scan(ctx); err != nil {
		return 0, fmt.Errorf("etl: job not found: %w", err)
	}

	if !e.tryLock(job.ID) {
		return 0, fmt.Errorf("job %q already has a run in progress", job.Name)
	}

	sourceDB, err := e.openSourceByID(ctx, job.SourceID)
	if err != nil {
		e.unlock(job.ID)
		return 0, err
	}

	runID, err := insertRunStarted(ctx, e.db, job.ID)
	if err != nil {
		e.unlock(job.ID)
		_ = sourceDB.Close()
		return 0, fmt.Errorf("record run start: %w", err)
	}

	go func() {
		defer e.unlock(job.ID)
		defer func() { _ = sourceDB.Close() }()

		timeout := time.Duration(job.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = time.Hour
		}
		runCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		result, runErr := extractAndLoad(runCtx, e.db, sourceDB, *job)
		status := RunStatusSuccess
		errMsg := ""
		if runErr != nil {
			status = RunStatusFailed
			errMsg = runErr.Error()
			e.logr.Error("etl: manual run failed", zap.String("job", job.Name), zap.Error(runErr))
		} else {
			e.logr.Info("etl: manual run complete",
				zap.String("job", job.Name),
				zap.Int64("rows_extracted", result.RowsExtracted),
				zap.Int64("rows_loaded", result.RowsLoaded))
		}
		if err := finishRun(context.Background(), e.db, runID, status, result, errMsg); err != nil {
			e.logr.Error("etl: failed to record manual run result", zap.Error(err))
		}
	}()

	return runID, nil
}

// nextTriggerTime returns the soonest of times (each "HH:MM", 24h, UTC)
// that is strictly after now — today's entry if it hasn't passed yet,
// otherwise tomorrow's.
func nextTriggerTime(times []string, now time.Time) (time.Time, error) {
	if len(times) == 0 {
		return time.Time{}, fmt.Errorf("etl: no trigger_times configured")
	}
	var best time.Time
	for _, t := range times {
		parsed, err := time.Parse("15:04", t)
		if err != nil {
			return time.Time{}, fmt.Errorf("etl: invalid trigger time %q: %w", t, err)
		}
		candidate := time.Date(now.Year(), now.Month(), now.Day(), parsed.Hour(), parsed.Minute(), 0, 0, time.UTC)
		if !candidate.After(now) {
			candidate = candidate.Add(24 * time.Hour)
		}
		if best.IsZero() || candidate.Before(best) {
			best = candidate
		}
	}
	return best, nil
}
