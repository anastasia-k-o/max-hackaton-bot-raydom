package config

import (
	"strings"
	"testing"
	"time"
)

func TestDevDefaultsStartWithoutBot(t *testing.T) {
	t.Setenv("DEV_MODE", "true")
	t.Setenv("NOTIFY_MODE", "log")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":8090" || c.Timing.Reminder24h != 24*time.Hour || c.SchedulerInterval != 15*time.Second {
		t.Errorf("config = %+v", c)
	}
}

func TestFastPresetAndOverrides(t *testing.T) {
	t.Setenv("DEV_MODE", "true")
	t.Setenv("NOTIFY_MODE", "log")
	t.Setenv("TIMING_PRESET", "fast")
	t.Setenv("OFFER_TTL", "90s")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Timing.Reminder24h != 10*time.Minute || c.Timing.OfferTTL != 90*time.Second || c.SchedulerInterval != 5*time.Second {
		t.Errorf("timing = %+v, interval %s", c.Timing, c.SchedulerInterval)
	}
}

func TestErrorsAreCollected(t *testing.T) {
	t.Setenv("NOTIFY_MODE", "http")
	t.Setenv("BOT_BASE_URL", " ")
	t.Setenv("TIMING_PRESET", "slow")
	t.Setenv("CONFIRMATION_BEFORE", "48h") // later than the 24 h reminder
	_, err := Load()
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"INTERNAL_API_KEY", "CORE_API_KEY", "TIMING_PRESET", "confirmation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s:\n%v", want, err)
		}
	}
}

// TestSharedEnvNames: the repository-wide .env names each secret once, with
// the bot's names; the backend reads them as they are.
func TestSharedEnvNames(t *testing.T) {
	t.Setenv("INTERNAL_API_KEY", "to-bot")
	t.Setenv("CORE_API_KEY", "from-bot")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Notify.InternalAPIKey != "to-bot" || c.BotAPIKey != "from-bot" || c.Notify.BotBaseURL != "http://localhost:8080" {
		t.Errorf("config = %+v / %q", c.Notify, c.BotAPIKey)
	}
	t.Setenv("BOT_API_KEY", "explicit")
	if c, _ := Load(); c.BotAPIKey != "explicit" {
		t.Errorf("BOT_API_KEY should win, got %q", c.BotAPIKey)
	}
}
