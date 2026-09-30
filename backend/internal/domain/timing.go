package domain

import "time"

// Timing is every "how long before the start" rule in one place.
//
// The real values come from the product spec and the mini app's mocks. The
// fast preset shrinks hours to minutes so the whole reminder chain of an
// event can be watched in MAX within a quarter of an hour.
type Timing struct {
	Reminder24h        time.Duration // reminder_24h this long before the start
	Confirmation       time.Duration // confirmation_required; signing up later confirms at once
	ConfirmationRetry  time.Duration // confirmation_retry for those who stayed silent
	Reminder1h         time.Duration // reminder_1h
	RegistrationCloses time.Duration // no sign-ups or queue after this point
	OfferTTL           time.Duration // how long a freed seat is held for the next in line
	OfferTTLSoon       time.Duration // the same, once inside the confirmation window
}

// RealTiming is the production schedule.
func RealTiming() Timing {
	return Timing{
		Reminder24h:        24 * time.Hour,
		Confirmation:       6 * time.Hour,
		ConfirmationRetry:  3 * time.Hour,
		Reminder1h:         time.Hour,
		RegistrationCloses: time.Hour,
		OfferTTL:           30 * time.Minute,
		OfferTTLSoon:       15 * time.Minute,
	}
}

// FastTiming keeps the order of the real schedule at minute scale.
func FastTiming() Timing {
	return Timing{
		Reminder24h:        10 * time.Minute,
		Confirmation:       6 * time.Minute,
		ConfirmationRetry:  4 * time.Minute,
		Reminder1h:         2 * time.Minute,
		RegistrationCloses: time.Minute,
		OfferTTL:           3 * time.Minute,
		OfferTTLSoon:       2 * time.Minute,
	}
}

// Validate checks that the milestones come in the order users expect.
func (t Timing) Validate() error {
	steps := []struct {
		name string
		d    time.Duration
	}{
		{"reminder_24h", t.Reminder24h},
		{"confirmation", t.Confirmation},
		{"confirmation_retry", t.ConfirmationRetry},
		{"registration_closes", t.RegistrationCloses},
	}
	for i := 1; i < len(steps); i++ {
		if steps[i].d >= steps[i-1].d {
			return Errorf(0, "invalid_timing", "%s (%s) must be earlier than %s (%s) before the start",
				steps[i-1].name, steps[i-1].d, steps[i].name, steps[i].d)
		}
	}
	if t.Reminder1h <= 0 || t.Reminder1h >= t.Confirmation {
		return Errorf(0, "invalid_timing", "reminder_1h (%s) must be positive and closer to the start than confirmation (%s)", t.Reminder1h, t.Confirmation)
	}
	if t.RegistrationCloses <= 0 || t.OfferTTL <= 0 || t.OfferTTLSoon <= 0 {
		return Errorf(0, "invalid_timing", "durations must be positive")
	}
	return nil
}
