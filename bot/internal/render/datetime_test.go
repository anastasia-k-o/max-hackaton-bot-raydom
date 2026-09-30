package render

import (
	"testing"
	"time"
)

func TestFormatDateTime(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{"september", time.Date(2026, 9, 22, 19, 0, 0, 0, moscow), "22 сентября, 19:00"},
		{"january", time.Date(2026, 1, 1, 9, 5, 0, 0, moscow), "1 января, 09:05"},
		{"december", time.Date(2026, 12, 31, 23, 59, 0, 0, moscow), "31 декабря, 23:59"},
		{"may", time.Date(2026, 5, 9, 12, 0, 0, 0, moscow), "9 мая, 12:00"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatDateTime(tc.at); got != tc.want {
				t.Errorf("formatDateTime() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFormatDateTimeRelative pins the calendar-day semantics: an event at
// 09:00 tomorrow is "завтра" even though it is under 24 hours away.
func TestFormatDateTimeRelative(t *testing.T) {
	tests := []struct {
		name  string
		event time.Time
		now   time.Time
		want  string
	}{
		{
			name:  "same day",
			event: time.Date(2026, 9, 22, 19, 0, 0, 0, moscow),
			now:   time.Date(2026, 9, 22, 10, 0, 0, 0, moscow),
			want:  "сегодня в 19:00",
		},
		{
			name:  "next calendar day, under 24 hours away",
			event: time.Date(2026, 9, 23, 9, 0, 0, 0, moscow),
			now:   time.Date(2026, 9, 22, 20, 0, 0, 0, moscow),
			want:  "завтра в 09:00",
		},
		{
			name:  "next calendar day, nearly 24 hours away",
			event: time.Date(2026, 9, 23, 19, 0, 0, 0, moscow),
			now:   time.Date(2026, 9, 22, 19, 30, 0, 0, moscow),
			want:  "завтра в 19:00",
		},
		{
			name:  "further out falls back to an absolute date",
			event: time.Date(2026, 9, 25, 19, 0, 0, 0, moscow),
			now:   time.Date(2026, 9, 22, 10, 0, 0, 0, moscow),
			want:  "25 сентября в 19:00",
		},
		{
			name:  "past dates are absolute",
			event: time.Date(2026, 9, 20, 19, 0, 0, 0, moscow),
			now:   time.Date(2026, 9, 22, 10, 0, 0, 0, moscow),
			want:  "20 сентября в 19:00",
		},
		{
			name:  "now in a different zone still resolves to the event's day",
			event: time.Date(2026, 9, 22, 19, 0, 0, 0, moscow),
			now:   time.Date(2026, 9, 22, 4, 0, 0, 0, time.UTC), // 07:00 MSK
			want:  "сегодня в 19:00",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatDateTimeRelative(tc.event, tc.now); got != tc.want {
				t.Errorf("formatDateTimeRelative() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCapitalise(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"сегодня в 19:00", "Сегодня в 19:00"},
		{"время", "Время"},
		{"Already", "Already"},
	}
	for _, tc := range tests {
		if got := capitalise(tc.in); got != tc.want {
			t.Errorf("capitalise(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
