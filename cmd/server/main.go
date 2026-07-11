package main

import (
	"bknd-3/internal/cache"
	"bknd-3/internal/config"
	"bknd-3/internal/database"
	"bknd-3/internal/logger"
	"bknd-3/internal/routes"
	"context"
	"go.uber.org/zap"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	cfg := config.Load()
	logr := logger.New(cfg)
	db, err := database.New(cfg.DatabaseURL, cfg)
	if err != nil {
		logr.Fatal("failed to connect to database", zap.Error(err))
	}
	defer db.Close()

	// Optional Redis cache. If REDIS_URL is unset (or Redis is unreachable) the
	// app runs normally with caching disabled.
	var c cache.Cache
	if cfg.RedisURL != "" {
		rc, cErr := cache.NewRedisCache(cfg.RedisURL)
		if cErr != nil {
			logr.Warn("failed to init redis cache, continuing without cache", zap.Error(cErr))
		} else {
			pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			if pErr := rc.Ping(pingCtx); pErr != nil {
				logr.Warn("redis ping failed, continuing without cache", zap.Error(pErr))
				_ = rc.Close()
			} else {
				c = rc
				defer rc.Close()
				logr.Info("redis cache enabled")
			}
			cancel()
		}
	}

	r := routes.NewRouter(db, cfg, logr, c)

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  2 * time.Minute, // ✅ 120 seconds
		WriteTimeout: 2 * time.Minute, // ✅ 120 seconds
		IdleTimeout:  2 * time.Minute, // ✅ 120 seconds
	}

	go func() {
		logr.Info("server started", zap.String("port", cfg.Port))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logr.Fatal("server failed", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	logr.Info("shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logr.Fatal("server forced to shutdown", zap.Error(err))
	}

	_ = db.Close()
	logr.Info("server exited gracefully")
}
