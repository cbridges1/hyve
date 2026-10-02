package orgdb

import (
	"strings"
	"testing"
)

func TestRedactDSN(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"postgres://hyve:s3cret!@db:5432/hyve?sslmode=require", "postgres://hyve:xxxxx@db:5432/hyve?sslmode=require"},
		{"host=db user=hyve password=s3cret dbname=hyve", "host=db user=hyve password=xxxxx dbname=hyve"},
		{"host=db password='s3 cret' dbname=hyve", "host=db password=xxxxx dbname=hyve"},
		{"/var/lib/hyve/hyve.db", "/var/lib/hyve/hyve.db"},
	} {
		got := RedactDSN(tc.in)
		if got != tc.want {
			t.Errorf("RedactDSN(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if strings.Contains(got, "s3") {
			t.Errorf("RedactDSN(%q) leaked the password: %q", tc.in, got)
		}
	}
}
