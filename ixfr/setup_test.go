package ixfr

import (
	"strings"
	"testing"

	"github.com/coredns/caddy"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr string
		check   func(*testing.T, *IXFR)
	}{
		{
			name:  "defaults",
			input: "ixfr",
			check: func(t *testing.T, x *IXFR) {
				if x.Zone != "example.org." {
					t.Errorf("zone = %q", x.Zone)
				}
				if x.history != defaultHistory {
					t.Errorf("history = %d, want %d", x.history, defaultHistory)
				}
				if x.path != "" {
					t.Errorf("path = %q, want empty (filled at Register)", x.path)
				}
			},
		},
		{
			name:  "history and file",
			input: "ixfr {\nhistory 8\nfile /var/lib/coredns/db.example.org.ixfr\n}",
			check: func(t *testing.T, x *IXFR) {
				if x.history != 8 {
					t.Errorf("history = %d", x.history)
				}
				if x.path != "/var/lib/coredns/db.example.org.ixfr" {
					t.Errorf("path = %q", x.path)
				}
			},
		},
		{
			name:    "history zero",
			input:   "ixfr {\nhistory 0\n}",
			wantErr: "positive integer",
		},
		{
			name:    "two zones",
			input:   "ixfr a.org b.org",
			wantErr: "exactly one zone",
		},
		{
			name:    "unknown property",
			input:   "ixfr {\nwibble\n}",
			wantErr: "unknown property",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := caddy.NewTestController("dns", tc.input)
			c.ServerBlockKeys = []string{"example.org."}
			x, err := parse(c)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("parse succeeded, want %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if tc.check != nil {
				tc.check(t, x)
			}
		})
	}
}
