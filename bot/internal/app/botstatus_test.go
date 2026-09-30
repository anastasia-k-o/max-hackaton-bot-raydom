package app

import (
	"context"
	"testing"
	"time"

	"hackatonBotMAX/internal/core"
	"hackatonBotMAX/internal/core/stub"
	"hackatonBotMAX/internal/maxapi"
)

func dialogUpdate(updateType maxapi.UpdateType, userID int64) maxapi.Update {
	return maxapi.Update{
		Type:      updateType,
		Timestamp: time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC).UnixMilli(),
		ChatID:    555,
		User:      &maxapi.UpdateUser{UserID: userID, Name: "Тест"},
	}
}

// statusActions returns the bot_status records the stub collected.
func statusActions(gateway *stub.Gateway) []stub.Action {
	var out []stub.Action
	for _, action := range gateway.Actions() {
		if action.Kind == stub.KindBotStatus {
			out = append(out, action)
		}
	}
	return out
}

// TestBotStartedReportsAvailable: when the user opens the dialog the backend
// learns the bot can now write to them. That is what turns off the mini app's
// "бот не может написать вам" banner.
func TestBotStartedReportsAvailable(t *testing.T) {
	service, maxClient, gateway := newTestBotService(t)

	outcome, err := service.HandleUpdate(context.Background(), dialogUpdate(maxapi.UpdateBotStarted, 777))
	if err != nil || outcome.Result != ResultOK {
		t.Fatalf("outcome = %+v, err = %v", outcome, err)
	}

	got := statusActions(gateway)
	if len(got) != 1 {
		t.Fatalf("bot status reports = %+v, want one", got)
	}
	if got[0].MaxUserID != 777 || got[0].Available == nil || !*got[0].Available || got[0].Reason != string(core.ReasonBotStarted) {
		t.Errorf("report = %+v, want user 777 available via bot_started", got[0])
	}
	if got[0].OccurredAt == nil || !got[0].OccurredAt.Equal(time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("occurred_at = %v, want the MAX timestamp", got[0].OccurredAt)
	}

	if _, ok := maxClient.LastSent(); !ok {
		t.Error("the greeting must still be sent")
	}
}

// TestDialogClosedReportsUnavailable: bot_stopped and dialog_removed both mean
// the bot can no longer reach the user. Nothing is sent to them.
func TestDialogClosedReportsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		update maxapi.UpdateType
		reason core.BotStatusReason
	}{
		{maxapi.UpdateBotStopped, core.ReasonBotStopped},
		{maxapi.UpdateDialogRemoved, core.ReasonDialogRemoved},
	} {
		t.Run(string(tc.update), func(t *testing.T) {
			service, maxClient, gateway := newTestBotService(t)

			outcome, err := service.HandleUpdate(context.Background(), dialogUpdate(tc.update, 888))
			if err != nil {
				t.Fatalf("HandleUpdate(): %v", err)
			}
			if !outcome.Handled || outcome.Result != ResultOK {
				t.Fatalf("outcome = %+v", outcome)
			}

			got := statusActions(gateway)
			if len(got) != 1 || got[0].MaxUserID != 888 || got[0].Available == nil || *got[0].Available {
				t.Fatalf("reports = %+v, want user 888 unavailable", got)
			}
			if got[0].Reason != string(tc.reason) {
				t.Errorf("reason = %q, want %q", got[0].Reason, tc.reason)
			}
			if records := maxClient.Records(); len(records) != 0 {
				t.Errorf("sent something to a user who closed the dialog: %+v", records)
			}
		})
	}
}

// TestBotStatusFailureDoesNotFailTheUpdate: a Core Backend outage must not
// cost the user their greeting, and must not turn into a webhook 502 that MAX
// would redeliver into the idempotency filter.
func TestBotStatusFailureDoesNotFailTheUpdate(t *testing.T) {
	service, maxClient, gateway := newTestBotService(t)
	gateway.FailNext(core.NewError(core.CodeUnavailable, "down", 0, nil))

	outcome, err := service.HandleUpdate(context.Background(), dialogUpdate(maxapi.UpdateBotStarted, 777))
	if err != nil {
		t.Fatalf("HandleUpdate() returned %v; a failed status report must be swallowed", err)
	}
	if outcome.Result != ResultOK {
		t.Errorf("outcome = %+v", outcome)
	}
	if _, ok := maxClient.LastSent(); !ok {
		t.Error("greeting not sent because the backend was down")
	}

	gateway.FailNext(core.NewError(core.CodeUnavailable, "down", 0, nil))
	outcome, err = service.HandleUpdate(context.Background(), dialogUpdate(maxapi.UpdateBotStopped, 777))
	if err != nil {
		t.Fatalf("HandleUpdate(bot_stopped) returned %v", err)
	}
	if outcome.Result != ResultUnavailable {
		t.Errorf("outcome = %+v, want result unavailable for the log", outcome)
	}
}

// TestDialogEventWithoutUserIsIgnored: without a user id there is nobody to
// report about.
func TestDialogEventWithoutUserIsIgnored(t *testing.T) {
	service, _, gateway := newTestBotService(t)
	update := dialogUpdate(maxapi.UpdateBotStopped, 0)
	update.User = nil

	outcome, err := service.HandleUpdate(context.Background(), update)
	if err != nil || outcome.Handled {
		t.Fatalf("outcome = %+v, err = %v", outcome, err)
	}
	if got := statusActions(gateway); len(got) != 0 {
		t.Fatalf("reported without a user: %+v", got)
	}
}
