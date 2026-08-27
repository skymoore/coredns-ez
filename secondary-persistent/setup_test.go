package secondarypersist

import (
	"slices"
	"testing"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/pkg/fall"
)

func TestParseConfig(t *testing.T) {
	tests := []struct {
		inputFileRules string
		shouldErr      bool
		transferFrom   string
		zones          []string
		fall           fall.F
		catalogZones   map[string]plugin.Zones
		persistPath    bool
		persistDir     bool
	}{
		{
			`secondary-persistent {
				transfer from 127.0.0.1
				persist db.example.org
			}`,
			false,
			"127.0.0.1:53",
			nil,
			fall.F{},
			nil,
			true,
			false,
		},
		{
			`secondary-persistent example.org {
				transfer from 127.0.0.1
				persist db.example.org
			}`,
			false,
			"127.0.0.1:53",
			[]string{"example.org."},
			fall.F{},
			nil,
			true,
			false,
		},
		{
			`secondary-persistent catalog.example {
				transfer from 127.0.0.1
				catalog
				directory /var/lib/coredns/secondary
			}`,
			false,
			"127.0.0.1:53",
			[]string{"catalog.example."},
			fall.F{},
			map[string]plugin.Zones{"catalog.example.": nil},
			false,
			true,
		},
		{
			`secondary-persistent catalog.example {
				transfer from 127.0.0.1
				catalog EXAMPLE.ORG internal.example
				directory /tmp/zones
			}`,
			false,
			"127.0.0.1:53",
			[]string{"catalog.example."},
			fall.F{},
			map[string]plugin.Zones{"catalog.example.": {"example.org.", "internal.example."}},
			false,
			true,
		},
		{
			`secondary-persistent catalog.example {
				transfer from 127.0.0.1
				catalog example.org EXAMPLE.ORG
				catalog internal.example
				directory /tmp/zones
			}`,
			false,
			"127.0.0.1:53",
			[]string{"catalog.example."},
			fall.F{},
			map[string]plugin.Zones{"catalog.example.": {"example.org.", "internal.example."}},
			false,
			true,
		},
		{
			`secondary-persistent catalog.example {
				transfer from 127.0.0.1
				catalog example.org
				catalog
				directory /tmp/zones
			}`,
			false,
			"127.0.0.1:53",
			[]string{"catalog.example."},
			fall.F{},
			map[string]plugin.Zones{"catalog.example.": nil},
			false,
			true,
		},
		{
			`secondary-persistent catalog.example {
				transfer from 127.0.0.1
				catalog :
				directory /tmp/zones
			}`,
			true,
			"",
			nil,
			fall.F{},
			nil,
			false,
			false,
		},
		{
			`secondary-persistent`,
			true,
			"",
			nil,
			fall.F{},
			nil,
			false,
			false,
		},
		{
			`secondary-persistent example.org {
				transfer from 127.0.0.1
			}`,
			false,
			"127.0.0.1:53",
			[]string{"example.org."},
			fall.F{},
			nil,
			false,
			false,
		},
		{
			`secondary-persistent catalog.example {
				transfer from 127.0.0.1
				catalog
				persist db.catalog
			}`,
			false,
			"127.0.0.1:53",
			[]string{"catalog.example."},
			fall.F{},
			map[string]plugin.Zones{"catalog.example.": nil},
			true,
			false,
		},
		{
			`secondary-persistent example.org example.net {
				transfer from 127.0.0.1
				persist db.zone
			}`,
			false,
			"127.0.0.1:53",
			[]string{"example.org.", "example.net."},
			fall.F{},
			nil,
			true,
			false,
		},
		{
			`secondary-persistent example.org {
				transferr from 127.0.0.1
				persist db.example.org
			}`,
			true,
			"",
			nil,
			fall.F{},
			nil,
			false,
			false,
		},
		{
			`secondary-persistent {
				transfer from 127.0.0.1
				persist db.example.org
				fallthrough
			}`,
			false,
			"127.0.0.1:53",
			nil,
			fall.Root,
			nil,
			true,
			false,
		},
		{
			`secondary-persistent example.org {
				transfer from 127.0.0.1
				persist db.example.org
				fallthrough example.org
			}`,
			false,
			"127.0.0.1:53",
			[]string{"example.org."},
			fall.F{Zones: []string{"example.org."}},
			nil,
			true,
			false,
		},
	}

	for i, test := range tests {
		c := caddy.NewTestController("dns", test.inputFileRules)
		s, f, catalogZones, pc, err := parseConfig(c)

		if err == nil && test.shouldErr {
			t.Fatalf("Test %d expected errors, but got no error", i)
		} else if err != nil && !test.shouldErr {
			t.Fatalf("Test %d expected no errors, but got '%v'", i, err)
		}
		if test.shouldErr {
			continue
		}

		for j, name := range test.zones {
			if x := s.Names[j]; x != name {
				t.Fatalf("Test %d zone names don't match expected %q, but got %q", i, name, x)
			}
		}

		for _, v := range s.Z {
			if x := v.TransferFrom[0]; x != test.transferFrom {
				t.Fatalf("Test %d transfer from names don't match expected %q, but got %q", i, test.transferFrom, x)
			}
		}

		if !f.Equal(test.fall) {
			t.Fatalf("Test %d fallthrough not equal: expected %v, got %v", i, test.fall, f)
		}
		if len(catalogZones) != len(test.catalogZones) {
			t.Fatalf("Test %d catalog zone count mismatch: expected %d, got %d", i, len(test.catalogZones), len(catalogZones))
		}
		for name, expected := range test.catalogZones {
			actual, ok := catalogZones[name]
			if !ok {
				t.Fatalf("Test %d expected catalog zone %q", i, name)
			}
			if !slices.Equal(actual, expected) {
				t.Fatalf("Test %d catalog member zones for %q mismatch: expected %v, got %v", i, name, expected, actual)
			}
		}
		if test.persistPath && pc.path == "" {
			t.Fatalf("Test %d expected persist path", i)
		}
		if test.persistDir && pc.dir == "" {
			t.Fatalf("Test %d expected persist directory", i)
		}
	}
}
