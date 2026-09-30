package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// Times are stored as Unix seconds. The city's offset is not stored with
// them: it belongs to the event (its timezone) and is applied on output.

func unix(t time.Time) int64 { return t.Unix() }

func fromUnix(v int64) time.Time { return time.Unix(v, 0).UTC() }

func nullUnix(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

func timePtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromUnix(v.Int64)
	return &t
}

func nullString(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func stringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func nullInt(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}

func intPtr(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int64)
	return &i
}

func nullInt64(i *int64) any {
	if i == nil {
		return nil
	}
	return *i
}

func int64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	i := v.Int64
	return &i
}

func nullFloat(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

func floatPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func jsonList(list []string) string {
	if list == nil {
		list = []string{}
	}
	b, _ := json.Marshal(list)
	return string(b)
}

func parseList(raw string) []string {
	var out []string
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		out = []string{}
	}
	return out
}
