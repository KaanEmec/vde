package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// windowRe is the ISO 8601 duration subset with fixed-length units: weeks, days, hours,
// minutes and seconds (P7D, P1W, PT1H, P1DT12H). Years and months have no fixed length
// and are refused.
var windowRe = regexp.MustCompile(`^P(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// ParseWindow returns the length of a quota window given as an ISO 8601 duration such as
// P1D, PT1H or P7D. The result is positive.
func ParseWindow(s string) (time.Duration, error) {
	m := windowRe.FindStringSubmatch(s)
	if m == nil || s == "P" || s[len(s)-1] == 'T' {
		return 0, fmt.Errorf("%q is not an ISO 8601 duration of weeks, days, hours, minutes or seconds (e.g. P1D, PT1H)", s)
	}
	units := []time.Duration{7 * 24 * time.Hour, 24 * time.Hour, time.Hour, time.Minute, time.Second}
	var d time.Duration
	for i, unit := range units {
		if m[i+1] == "" {
			continue
		}
		n, err := strconv.ParseInt(m[i+1], 10, 32)
		if err != nil {
			return 0, fmt.Errorf("%q: %w", s, err)
		}
		d += time.Duration(n) * unit
	}
	if d <= 0 {
		return 0, fmt.Errorf("%q is not a positive duration", s)
	}
	return d, nil
}

// ParseTime reads a time as the operator commands and the admin API take it (token
// expiry, audit window): an RFC 3339 time, a date (midnight UTC), or a positive number of
// days (90d) or Go duration (2160h) from now, forward, or back when back is set. ok is
// false for anything else.
func ParseTime(s string, now time.Time, back bool) (t time.Time, ok bool) {
	for _, layout := range []string{time.RFC3339, time.DateOnly} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	d, err := time.ParseDuration(s)
	if days, isDays := strings.CutSuffix(s, "d"); isDays {
		n, nErr := strconv.Atoi(days)
		d, err = time.Duration(n)*24*time.Hour, nErr
	}
	if err != nil || d <= 0 {
		return time.Time{}, false
	}
	if back {
		d = -d
	}
	return now.Add(d), true
}
