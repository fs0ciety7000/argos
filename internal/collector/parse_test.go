package collector

import (
	"math"
	"strings"
	"testing"
	"time"
)

const fixture = `@@STAT
cpu  1000 50 300 8000 200 10 20 5 40 0
cpu0 500 25 150 4000 100 5 10 2 20 0
intr 123456
@@MEM
MemTotal:       16384000 kB
MemFree:         2000000 kB
MemAvailable:    8192000 kB
@@LOAD
0.42 0.37 0.30 2/345 6789
@@NET
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 999999 10 0 0 0 0 0 0 999999 10 0 0 0 0 0 0
  eth0: 1000000 1000 0 0 0 0 0 0 500000 800 0 0 0 0 0 0
docker0: 777 1 0 0 0 0 0 0 777 1 0 0 0 0 0 0
vethabc: 555 1 0 0 0 0 0 0 555 1 0 0 0 0 0 0
@@DF
Filesystem     1024-blocks      Used Available Capacity Mounted on
/dev/sda1        100000000  60000000  40000000      60% /
/dev/sdb1       2000000000 1500000000 500000000     75% /mnt/media files
/dev/sdb1       2000000000 1500000000 500000000     75% /mnt/media files
`

func TestParseFixture(t *testing.T) {
	r, err := Parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	// total = 1000+50+300+8000+200+10+20+5 (guest excluded) = 9585
	if r.CPUTotal != 9585 || r.CPUIdle != 8200 {
		t.Fatalf("cpu total/idle = %d/%d", r.CPUTotal, r.CPUIdle)
	}
	if r.MemTotal != 16384000*1024 || r.MemAvailable != 8192000*1024 {
		t.Fatalf("mem = %d/%d", r.MemTotal, r.MemAvailable)
	}
	if r.Load1 != 0.42 {
		t.Fatalf("load = %v", r.Load1)
	}
	if len(r.Net) != 1 || r.Net["eth0"].Rx != 1000000 || r.Net["eth0"].Tx != 500000 {
		t.Fatalf("net = %+v (lo, docker, veth must be excluded)", r.Net)
	}
	if len(r.Disks) != 2 || r.Disks[1].Mount != "/mnt/media files" || r.Disks[0].Used != 60000000*1024 {
		t.Fatalf("disks = %+v", r.Disks)
	}
}

func TestParseRejectsIncomplete(t *testing.T) {
	cases := map[string]string{
		"empty":        "",
		"no stat":      strings.Replace(fixture, "@@STAT", "@@XXX", 1),
		"bad cpu":      strings.Replace(fixture, "cpu  1000", "cpu  abc", 1),
		"no memtotal":  strings.Replace(fixture, "MemTotal:", "MemX:", 1),
		"garbage load": strings.Replace(fixture, "0.42 0.37", "nope 0.37", 1),
	}
	for name, in := range cases {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// df may legitimately be empty (timeout on a hung mount).
	noDF := fixture[:strings.Index(fixture, "@@DF")]
	if r, err := Parse([]byte(noDF)); err != nil || len(r.Disks) != 0 {
		t.Fatalf("missing df section should be tolerated: %v", err)
	}
}

func TestCompute(t *testing.T) {
	t0 := time.Unix(1000, 0)
	prev, _ := Parse([]byte(fixture))
	first := Compute("box", t0, prev, nil, time.Time{})
	if first.CPU != -1 || first.RxBps != -1 {
		t.Fatalf("first sample should have unknown rates: %+v", first)
	}
	if first.MemUsed != (16384000-8192000)*1024 {
		t.Fatalf("mem used = %d", first.MemUsed)
	}

	cur := prev
	cur.Net = map[string]NetCounters{"eth0": {Rx: 1000000 + 50000, Tx: 500000 + 10000}}
	cur.CPUTotal, cur.CPUIdle = prev.CPUTotal+1000, prev.CPUIdle+750
	s := Compute("box", t0.Add(5*time.Second), cur, &prev, t0)
	if math.Abs(s.CPU-25) > 1e-9 {
		t.Fatalf("cpu = %v, want 25", s.CPU)
	}
	if s.RxBps != 10000 || s.TxBps != 2000 {
		t.Fatalf("rates = %v/%v", s.RxBps, s.TxBps)
	}

	// Counter reset (reboot): no negative or huge spike.
	reset := cur
	reset.Net = map[string]NetCounters{"eth0": {Rx: 10, Tx: 10}}
	reset.CPUTotal, reset.CPUIdle = 100, 90
	s = Compute("box", t0.Add(10*time.Second), reset, &cur, t0.Add(5*time.Second))
	if s.CPU != -1 || s.RxBps != -1 {
		t.Fatalf("reset should give unknown values: %+v", s)
	}
}
