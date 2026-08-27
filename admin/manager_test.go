package admin

import "testing"

func TestQualifyMbox(t *testing.T) {
	origin := "example.com."
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "hostmaster.example.com.", false},
		{"  ", "hostmaster.example.com.", false},
		{"hostmaster.example.com.", "hostmaster.example.com.", false},
		{"hostmaster.example.com", "hostmaster.example.com.", false},
		{"sky@rwx.dev", "sky.rwx.dev.", false},
		{"hostmaster@example.com", "hostmaster.example.com.", false},
		{"First.Last@rwx.dev", `first\.last.rwx.dev.`, false},
		{"@example.com", "", true},
		{"hostmaster@", "", true},
	}
	for _, tc := range cases {
		got, err := qualifyMbox(tc.in, origin)
		if tc.wantErr {
			if err == nil {
				t.Errorf("qualifyMbox(%q) = %q, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("qualifyMbox(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("qualifyMbox(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
