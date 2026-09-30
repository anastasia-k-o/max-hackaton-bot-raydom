package domain

import (
	"fmt"
	"slices"
	"sync"
	"time"
	_ "time/tzdata" // the container image has no zoneinfo; embed it
)

// City mirrors frontend/src/lib/cities.js. Add cities in both places.
type City struct {
	ID        string
	Name      string
	Timezone  string
	Districts []string
}

// Cities known to the backend.
var Cities = []City{{
	ID:       "msk",
	Name:     "Москва",
	Timezone: "Europe/Moscow",
	Districts: []string{
		"Хамовники", "Басманный", "Тверской", "Пресненский",
		"Замоскворечье", "Таганский", "Сокольники", "Останкинский",
	},
}}

// DefaultCity is where new users and events land unless told otherwise.
const DefaultCity = "msk"

// CityByID finds a city.
func CityByID(id string) (City, bool) {
	i := slices.IndexFunc(Cities, func(c City) bool { return c.ID == id })
	if i < 0 {
		return City{}, false
	}
	return Cities[i], true
}

var (
	locMu    sync.Mutex
	locCache = map[string]*time.Location{}
)

// LocationOf loads a time zone once. An unknown name falls back to Moscow:
// sending a UTC time would be rejected by the bot, which is worse.
func LocationOf(name string) *time.Location {
	locMu.Lock()
	defer locMu.Unlock()
	if loc, ok := locCache[name]; ok {
		return loc
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		loc, _ = time.LoadLocation("Europe/Moscow")
	}
	locCache[name] = loc
	return loc
}

// FormatTime renders t as RFC 3339 in the event's city: 2026-09-22T19:00:00+03:00.
func FormatTime(t time.Time, loc *time.Location) string {
	return t.In(loc).Truncate(time.Second).Format(time.RFC3339)
}

var monthsGenitive = [...]string{
	"января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря",
}

// HumanTime is how a changed start time reads in an event_updated message:
// «19:00» when only the hour moved, «29 сентября, 19:00» when the day did.
func HumanTime(t, other time.Time, loc *time.Location) string {
	t, other = t.In(loc), other.In(loc)
	clock := t.Format("15:04")
	if t.Year() == other.Year() && t.YearDay() == other.YearDay() {
		return clock
	}
	return fmt.Sprintf("%d %s, %s", t.Day(), monthsGenitive[t.Month()-1], clock)
}
