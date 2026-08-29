package cachegen

import (
	"net"
	"testing"
)

func TestLabelMatcherAndFailClosed(t *testing.T) {
	if Label("192.168.8.7") != "" {
		t.Fatal("no matcher: Label must be empty so cache falls back to netmask")
	}
	SetMatcher(func(ip net.IP) string {
		if v4 := ip.To4(); v4 != nil && v4[0] == 10 {
			return "internal"
		}
		return ""
	})
	t.Cleanup(func() { SetMatcher(nil) })

	if got := Label("10.1.2.3"); got != "internal" {
		t.Fatalf("got %q want internal", got)
	}
	if got := Label("8.8.8.8"); got != "public" {
		t.Fatalf("empty matcher return must become public, got %q", got)
	}
	if got := Label("not-an-ip"); got != "" {
		t.Fatalf("unparseable must fail closed, got %q", got)
	}
}
