// Package scheduler runs small, fixed-time recurring jobs directly inside
// the server process — no pg_cron or other DB-side scheduler required.
package scheduler

import (
	"bknd-3/internal/logger"
	"bknd-3/internal/services"
	"context"
	"time"

	"go.uber.org/zap"
)

// StartDailySessionReset force-logs-out every user once a day at 00:00 UTC
// (== 00:00 in Ghana, which has no daylight saving) via
// AuthService.ResetAllSessions. Runs in its own goroutine and stops when ctx
// is cancelled. Safe to run in more than one server instance at once — both
// UPDATEs in ResetAllSessions are naturally idempotent-ish (a redundant
// token_version bump or an already-revoked refresh token row is harmless),
// so no distributed lock is needed.
func StartDailySessionReset(ctx context.Context, authSvc *services.AuthService, logr *logger.Logger) {
	go func() {
		for {
			wait := time.Until(nextUTCMidnight())
			timer := time.NewTimer(wait)
			logr.Info("daily session reset scheduled", zap.Duration("in", wait))

			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				runCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if err := authSvc.ResetAllSessions(runCtx); err != nil {
					logr.Error("daily session reset failed", zap.Error(err))
				} else {
					logr.Info("daily session reset completed: all users logged out")
				}
				cancel()
			}
		}
	}()
}

func nextUTCMidnight() time.Time {
	now := time.Now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return midnight.Add(24 * time.Hour)
}
