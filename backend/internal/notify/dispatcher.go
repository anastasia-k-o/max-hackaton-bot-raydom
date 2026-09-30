// Package notify delivers queued notifications to the bot.
//
// Rows are written to the outbox by the business rules, in the same
// transaction as the change that causes them. The dispatcher picks them up
// and calls the bot's POST /api/v1/notifications, one row at a time. The
// request_id stays the same across retries, so the bot's own deduplication
// guarantees one message even when a response is lost.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"hackatonCore/internal/store"
)

// Mode selects where notifications go.
type Mode string

const (
	ModeHTTP Mode = "http" // to the bot
	ModeLog  Mode = "log"  // to the log only: run the backend without a bot
)

// Config configures delivery.
type Config struct {
	Mode           Mode
	BotBaseURL     string // e.g. http://localhost:8080
	InternalAPIKey string // the bot's INTERNAL_API_KEY
	Timeout        time.Duration
	MaxAttempts    int
}

// Dispatcher sends due outbox rows.
type Dispatcher struct {
	store  *store.Store
	cfg    Config
	client *http.Client
	log    *slog.Logger
	wake   chan struct{}
	mu     sync.Mutex // one flush at a time
}

// New builds a dispatcher.
func New(st *store.Store, cfg Config, log *slog.Logger) *Dispatcher {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 6
	}
	cfg.BotBaseURL = strings.TrimRight(cfg.BotBaseURL, "/")
	return &Dispatcher{store: st, cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}, log: log, wake: make(chan struct{}, 1)}
}

// Wake asks for a flush soon. It never blocks.
func (d *Dispatcher) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Run flushes on every wake-up and every interval until ctx ends.
func (d *Dispatcher) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-d.wake:
		}
		if _, err := d.Flush(ctx); err != nil && ctx.Err() == nil {
			d.log.Error("notification flush failed", "error", err)
		}
	}
}

// FlushReport counts the outcomes of one flush.
type FlushReport struct {
	Sent    int `json:"sent"`
	Failed  int `json:"failed"`
	Retried int `json:"retried"`
	Logged  int `json:"logged"`
}

// Flush delivers every due row. Retries use real time, not the dev clock:
// "try again in a minute" means a real minute.
func (d *Dispatcher) Flush(ctx context.Context) (FlushReport, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var rep FlushReport
	for {
		var due []store.Notification
		err := d.store.InTx(ctx, func(tx *store.Tx) error {
			var err error
			due, err = tx.DueNotifications(ctx, time.Now(), 20)
			return err
		})
		if err != nil || len(due) == 0 {
			return rep, err
		}
		for _, n := range due {
			if err := d.deliver(ctx, n, &rep); err != nil {
				return rep, err
			}
		}
	}
}

// outcome is what to write back after one attempt.
type outcome struct {
	status    string
	lastError string
	messageID string
	retryIn   time.Duration
	// botBlocked: the bot said it cannot reach this user.
	botBlocked bool
}

func (d *Dispatcher) deliver(ctx context.Context, n store.Notification, rep *FlushReport) error {
	var out outcome
	if d.cfg.Mode == ModeLog {
		d.log.Info("notification (NOTIFY_MODE=log, not sent)", "type", n.Type, "request_id", n.RequestID, "payload", string(n.Payload))
		out = outcome{status: store.NotifLogged}
	} else {
		out = d.send(ctx, n)
	}

	attempts := n.Attempts + 1
	if out.status == store.NotifPending && attempts >= d.cfg.MaxAttempts {
		out.status, out.lastError = store.NotifFailed, "gave up after "+fmt.Sprint(attempts)+" attempts: "+out.lastError
	}
	now := time.Now()
	var sentAt *time.Time
	if out.status == store.NotifSent {
		sentAt = &now
	}
	err := d.store.InTx(ctx, func(tx *store.Tx) error {
		if err := tx.MarkNotification(ctx, n.ID, out.status, attempts, now.Add(out.retryIn), out.lastError, out.messageID, sentAt); err != nil {
			return err
		}
		if out.botBlocked {
			_, err := tx.SetBotAvailable(ctx, n.UserID, false, now)
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	switch out.status {
	case store.NotifSent:
		rep.Sent++
		d.log.Info("notification sent", "type", n.Type, "request_id", n.RequestID, "message_id", out.messageID)
	case store.NotifLogged:
		rep.Logged++
	case store.NotifPending:
		rep.Retried++
		d.log.Warn("notification will be retried", "type", n.Type, "request_id", n.RequestID, "attempt", attempts, "in", out.retryIn, "error", out.lastError)
	default:
		rep.Failed++
		d.log.Error("notification failed", "type", n.Type, "request_id", n.RequestID, "error", out.lastError)
	}
	return nil
}

// botReply is the bot's answer, success or error.
type botReply struct {
	MessageID string `json:"message_id"`
	Error     struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (d *Dispatcher) send(ctx context.Context, n store.Notification) outcome {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.cfg.BotBaseURL+"/api/v1/notifications", bytes.NewReader(n.Payload))
	if err != nil {
		return outcome{status: store.NotifFailed, lastError: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Api-Key", d.cfg.InternalAPIKey)
	req.Header.Set("X-Request-Id", n.RequestID)

	resp, err := d.client.Do(req)
	if err != nil {
		return outcome{status: store.NotifPending, lastError: "bot unreachable: " + err.Error(), retryIn: backoff(n.Attempts)}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var reply botReply
	_ = json.Unmarshal(body, &reply)
	detail := fmt.Sprintf("bot answered %d %s: %s", resp.StatusCode, reply.Error.Code, reply.Error.Message)

	switch {
	case resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusOK:
		// 200 is the bot's "duplicate, already sent" — delivered either way.
		return outcome{status: store.NotifSent, messageID: reply.MessageID}
	case resp.StatusCode == http.StatusUnprocessableEntity:
		// recipient_unavailable: the user stopped the bot. Retrying cannot
		// help until they open the dialog again, which the bot reports.
		return outcome{status: store.NotifFailed, lastError: detail, botBlocked: true}
	case resp.StatusCode == http.StatusUnauthorized:
		return outcome{status: store.NotifFailed, lastError: detail + " (check BOT_INTERNAL_API_KEY = bot's INTERNAL_API_KEY)"}
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return outcome{status: store.NotifPending, lastError: detail, retryIn: backoff(n.Attempts)}
	default:
		// 400 and friends: our payload is wrong. Retrying sends the same bytes.
		return outcome{status: store.NotifFailed, lastError: detail}
	}
}

// backoff is 10s, 30s, 1m30s, 4m30s, ... capped at 10 minutes.
func backoff(previousAttempts int) time.Duration {
	d := 10 * time.Second
	for i := 0; i < previousAttempts; i++ {
		d *= 3
		if d > 10*time.Minute {
			return 10 * time.Minute
		}
	}
	return d
}
