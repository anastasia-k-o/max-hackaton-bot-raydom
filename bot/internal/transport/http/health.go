package http

import (
	"context"
	"net/http"
	"time"

	"hackatonBotMAX/internal/core"
	"hackatonBotMAX/internal/maxapi"
)

// healthHandler serves GET /health.
//
// Liveness only: the process is up and serving. It performs no I/O, so a
// dependency outage never causes an orchestrator to kill a perfectly healthy
// bot.
type healthHandler struct {
	version string
	started time.Time
}

func (h *healthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{
		"status":         "ok",
		"version":        h.version,
		"uptime_seconds": int64(time.Since(h.started).Seconds()),
	})
}

// readyHandler serves GET /ready.
//
// Readiness checks dependencies. In real MAX mode that means calling GetMe: an
// invalid token is exactly the configuration error this endpoint exists to
// surface, and the spec is explicit that the bot must not carry on quietly
// with a broken token. In mock mode the checks always pass, which is what lets
// the demo run with nothing configured.
type readyHandler struct {
	max     maxapi.Client
	gateway core.Gateway
	// updatesMode — откуда бот ждёт события: "webhook" или "polling".
	// Поле диагностическое: чаще всего «бот не отвечает на кнопки» — это
	// расхождение между тем, как бот настроен, и тем, как он подписан.
	updatesMode  string
	checkTimeout time.Duration
}

// dependencyStatus is one dependency's health.
type dependencyStatus struct {
	Name   string `json:"name"`
	Mode   string `json:"mode"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func (h *readyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	timeout := h.checkTimeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	checks := []dependencyStatus{
		h.checkMax(ctx),
		h.checkCore(ctx),
	}

	ready := true
	for _, check := range checks {
		if check.Status != "ok" {
			ready = false
		}
	}

	status := http.StatusOK
	overall := "ready"
	if !ready {
		status = http.StatusServiceUnavailable
		overall = "not_ready"
	}

	writeJSON(w, r, status, map[string]any{
		"status":       overall,
		"updates_mode": h.updatesMode,
		"dependencies": checks,
	})
}

func (h *readyHandler) checkMax(ctx context.Context) dependencyStatus {
	check := dependencyStatus{Name: "max", Mode: h.max.Mode(), Status: "ok"}
	if _, err := h.max.GetMe(ctx); err != nil {
		check.Status = "error"
		// The message is developer-facing and appears in a readiness probe,
		// so it stays short and carries no token material.
		check.Error = shortError(err)
	}
	return check
}

func (h *readyHandler) checkCore(ctx context.Context) dependencyStatus {
	check := dependencyStatus{Name: "core", Mode: h.gateway.Mode(), Status: "ok"}
	if err := h.gateway.Ping(ctx); err != nil {
		check.Status = "error"
		check.Error = shortError(err)
	}
	return check
}

func shortError(err error) string {
	const limit = 200
	message := err.Error()
	if len(message) > limit {
		return message[:limit] + "…"
	}
	return message
}
