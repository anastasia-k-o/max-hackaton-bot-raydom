// Command core is the Core Backend of «Рядом»: events, registrations, the
// waitlist and the reminder schedule. It tells the bot what to send and
// answers the bot when a user presses a button.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"hackatonCore/internal/clock"
	"hackatonCore/internal/config"
	"hackatonCore/internal/httpapi"
	"hackatonCore/internal/notify"
	"hackatonCore/internal/seed"
	"hackatonCore/internal/service"
	"hackatonCore/internal/store"
)

func main() {
	// `core healthcheck` is the container's health probe: it needs nothing
	// in the image but the binary itself.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("core backend stopped", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log.Info("starting core backend", cfg.Redacted()...)
	if cfg.DevMode {
		log.Warn("DEV_MODE=true: /dev/* is open to anyone who can reach this port — never expose it to the internet")
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	if cfg.SeedDemo {
		if seeded, err := seed.IfEmpty(context.Background(), st, time.Now()); err != nil {
			return err
		} else if seeded {
			log.Info("empty database: demo data from the mini app's mocks loaded (SEED_DEMO=false to skip)")
		}
	}
	if cfg.DevMode && cfg.DemoMaxUserID > 0 {
		log.Warn("dev login signs in as your MAX account: anyone who can open the mini app gets it — turn DEV_MODE off before sharing the address",
			"max_user_id", cfg.DemoMaxUserID)
	}
	if cfg.MaxBotToken == "" {
		log.Warn("MAX_BOT_TOKEN is not set: sign-in from MAX (POST /auth/max) is unavailable; dev login only")
	}

	clk := clock.NewTravel()
	dispatcher := notify.New(st, cfg.Notify, log)
	svc := service.New(st, clk, service.Config{Timing: cfg.Timing, MiniAppURL: cfg.MiniAppURL}, log, dispatcher.Wake)

	deps := httpapi.Deps{
		Service: svc, Store: st, Dispatcher: dispatcher, BotAPIKey: cfg.BotAPIKey,
		DevMode: cfg.DevMode, Log: log,
		Auth: service.AuthConfig{BotToken: cfg.MaxBotToken, MaxAge: cfg.InitDataMaxAge,
			DevLogin: cfg.DevMode, DevMaxUserID: cfg.DemoMaxUserID},
	}
	if cfg.SeedDemo {
		deps.Reseed = func(ctx context.Context) error {
			_, err := seed.IfEmpty(ctx, st, time.Now())
			return err
		}
	}
	if cfg.DevMode {
		deps.Clock = clk
	}
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.NewHandler(deps),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go dispatcher.Run(ctx, 5*time.Second)
	go func() {
		ticker := time.NewTicker(cfg.SchedulerInterval)
		defer ticker.Stop()
		for {
			if rep, err := svc.Tick(ctx); err != nil && ctx.Err() == nil {
				log.Error("scheduler tick failed", "error", err)
			} else if len(rep.Queued) > 0 || len(rep.ExpiredOffers) > 0 {
				log.Info("scheduler", "queued", rep.Queued, "expired_offers", rep.ExpiredOffers)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.HTTPAddr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func healthcheck() int {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/health")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
