package inventory

import (
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	hosts, err := Parse(`[
		{"name":"netcup-01","kind":"vps","address":"152.53.176.13","user":"argos","docker":true},
		{"name":"whatbox","kind":"seedbox","address":"box.whatbox.ca","port":2222,"user":"me"},
		{"name":"v6","kind":"vps","address":"2001:db8::1","user":"argos"}
	]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 3 {
		t.Fatalf("got %d hosts", len(hosts))
	}
	if hosts[0].Port != 22 || hosts[0].Addr() != "152.53.176.13:22" {
		t.Fatalf("default port not applied: %+v", hosts[0])
	}
	if hosts[2].Addr() != "[2001:db8::1]:22" {
		t.Fatalf("IPv6 addr = %s", hosts[2].Addr())
	}
	if empty, err := Parse("  "); err != nil || empty != nil {
		t.Fatalf("empty config: %v %v", empty, err)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"not json":        `nope`,
		"object":          `{"name":"a"}`,
		"unknown field":   `[{"name":"a","kind":"vps","address":"h","user":"u","command":"rm -rf /"}]`,
		"trailing data":   `[] []`,
		"bad name":        `[{"name":"A B","kind":"vps","address":"h","user":"u"}]`,
		"bad kind":        `[{"name":"a","kind":"nas","address":"h","user":"u"}]`,
		"seedbox docker":  `[{"name":"a","kind":"seedbox","address":"h","user":"u","docker":true}]`,
		"address options": `[{"name":"a","kind":"vps","address":"-oProxyCommand=x","user":"u"}]`,
		"address with at": `[{"name":"a","kind":"vps","address":"u@h","user":"u"}]`,
		"bad port":        `[{"name":"a","kind":"vps","address":"h","port":70000,"user":"u"}]`,
		"bad user":        `[{"name":"a","kind":"vps","address":"h","user":"root;id"}]`,
		"duplicate":       `[{"name":"a","kind":"vps","address":"h","user":"u"},{"name":"a","kind":"vps","address":"h2","user":"u"}]`,
		"too many":        "[" + strings.TrimSuffix(strings.Repeat(`{"name":"x","kind":"vps","address":"h","user":"u"},`, maxHosts+1), ",") + "]",
	}
	for name, raw := range cases {
		if _, err := Parse(raw); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseCanonicalAddress(t *testing.T) {
	hosts, err := Parse(`[{"name":"a","kind":"vps","address":"Box.Example.COM","user":"u"},{"name":"b","kind":"vps","address":"2001:DB8:0:0::1","user":"u"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if hosts[0].Address != "box.example.com" || hosts[1].Address != "2001:db8::1" {
		t.Fatalf("not canonical: %q %q", hosts[0].Address, hosts[1].Address)
	}
}
