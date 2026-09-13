package orgdb

import (
	"strconv"
	"strings"
)

// rebind rewrites a query written with SQLite/MySQL-style "?" placeholders
// into Postgres's "$1", "$2", ... form when driver is "postgres", and
// returns query unchanged for "sqlite" (modernc.org/sqlite accepts "?"
// natively). Every query in this package is written once, with "?"
// placeholders, and passed through rebind before execution — the same
// shape sqlx's own Rebind helper takes, kept local here rather than
// pulling in that dependency for one function.
func rebind(driver, query string) string {
	if driver != "postgres" {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
