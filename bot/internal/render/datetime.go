package render

import (
	"fmt"
	"time"
)

// Localisation of dates and times lives here and nowhere else.
//
// Timestamps arrive as RFC3339 instants and stay instants all the way through
// the application; they become "22 сентября, 19:00" only at the moment a
// message is rendered. That is what keeps the API contract unambiguous and a
// future locale switch a change to this file.

// monthsGenitive holds Russian month names in the genitive case, which is what
// "22 сентября" requires. Index 1..12.
var monthsGenitive = [...]string{
	"", "января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря",
}

// Clock supplies the current time. Injecting it keeps "сегодня"/"завтра"
// decisions testable.
type Clock func() time.Time

// formatTime renders "19:00" in the event's own offset.
func formatTime(t time.Time) string {
	return t.Format("15:04")
}

// formatDate renders "22 сентября".
func formatDate(t time.Time) string {
	month := ""
	if m := int(t.Month()); m >= 1 && m <= 12 {
		month = monthsGenitive[m]
	}
	return fmt.Sprintf("%d %s", t.Day(), month)
}

// formatDateTime renders "22 сентября, 19:00".
func formatDateTime(t time.Time) string {
	return fmt.Sprintf("%s, %s", formatDate(t), formatTime(t))
}

// formatDateTimeRelative renders a date-time with a "сегодня"/"завтра" prefix
// when it falls on the current or next calendar day in the event's own
// timezone.
//
// Comparing calendar days rather than 24-hour windows is intentional: an event
// at 09:00 tomorrow is "завтра" even when it is only 14 hours away.
func formatDateTimeRelative(t time.Time, now time.Time) string {
	eventDay := startOfDay(t)
	reference := startOfDay(now.In(t.Location()))

	switch eventDay.Sub(reference) {
	case 0:
		return fmt.Sprintf("сегодня в %s", formatTime(t))
	case 24 * time.Hour:
		return fmt.Sprintf("завтра в %s", formatTime(t))
	default:
		return fmt.Sprintf("%s в %s", formatDate(t), formatTime(t))
	}
}

// startOfDay truncates to midnight in the value's own location.
func startOfDay(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, t.Location())
}
