package template

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CronNextOccurrence returns the next time after `from` that matches the
// given 5-field cron expression (minute hour dom month dow). Supports *,
// single numbers, ranges (1-5), and comma-separated lists (1,2,3). Used to
// evaluate Spec.Schedule into a cluster's spec.expiresAt when a cluster is
// created from a template.
func CronNextOccurrence(expr string, from time.Time) (time.Time, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("cron expression must have 5 fields, got %d", len(fields))
	}

	matchField := func(field string, val int) (bool, error) {
		if field == "*" {
			return true, nil
		}
		for _, part := range strings.Split(field, ",") {
			if strings.Contains(part, "-") {
				bounds := strings.SplitN(part, "-", 2)
				lo, err1 := strconv.Atoi(bounds[0])
				hi, err2 := strconv.Atoi(bounds[1])
				if err1 != nil || err2 != nil {
					return false, fmt.Errorf("invalid range %q", part)
				}
				if val >= lo && val <= hi {
					return true, nil
				}
			} else {
				n, err := strconv.Atoi(part)
				if err != nil {
					return false, fmt.Errorf("invalid field value %q", part)
				}
				if val == n {
					return true, nil
				}
			}
		}
		return false, nil
	}

	t := from.Truncate(time.Minute).Add(time.Minute)
	end := from.Add(366 * 24 * time.Hour)
	for t.Before(end) {
		minOK, _ := matchField(fields[0], t.Minute())
		hrOK, _ := matchField(fields[1], t.Hour())
		domOK, _ := matchField(fields[2], t.Day())
		monOK, _ := matchField(fields[3], int(t.Month()))
		dowOK, _ := matchField(fields[4], int(t.Weekday()))
		if minOK && hrOK && domOK && monOK && dowOK {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("no occurrence found within one year for %q", expr)
}
