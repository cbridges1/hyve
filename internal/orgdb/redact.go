package orgdb

import (
	"net/url"
	"regexp"
)

var dsnPasswordKV = regexp.MustCompile(`(?i)(password=)(?:'(?:[^'\\]|\\.)*'|\S+)`)

// RedactDSN hides the password in a database DSN before it's logged — a
// postgres URL ("postgres://user:pass@host/db") or keyword/value form
// ("host=... password=..."). A SQLite path passes through unchanged.
func RedactDSN(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		return u.Redacted()
	}
	return dsnPasswordKV.ReplaceAllString(dsn, "${1}xxxxx")
}
