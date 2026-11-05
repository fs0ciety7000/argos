package collector

import (
	"encoding/json"
	"fmt"
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

func TestParseHostileOutput(t *testing.T) {
	for _, load := range []string{"NaN", "Inf", "+Infinity", "-inf", "-1", "1e400"} {
		in := strings.Replace(fixture, "0.42 0.37", load+" 0.37", 1)
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("load %q accepted", load)
		}
	}

	var df strings.Builder
	df.WriteString("@@DF\nFilesystem 1024-blocks Used Available Capacity Mounted on\n")
	df.WriteString("/dev/x 100 10 90 10% /" + strings.Repeat("a", 300) + "\n") // too long
	df.WriteString("/dev/x 100 10 90 10% /bad\x07bell\n")                      // control char
	df.WriteString("/dev/x 100 200 0 100% /usedbig\n")                         // used > size
	df.WriteString("/dev/x 99999999999999999 1 1 1% /huge\n")                  // overflow on *1024
	for i := range 40 {
		df.WriteString(fmt.Sprintf("/dev/x 100 10 90 10%% /m%d\n", i))
	}
	base := fixture[:strings.Index(fixture, "@@DF")]
	r, err := Parse([]byte(base + df.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Disks) != maxDisks || r.Disks[0].Mount != "/m0" {
		t.Fatalf("disks = %d, first %q (hostile lines must be skipped, list capped)", len(r.Disks), r.Disks[0].Mount)
	}
	if r, _ := ParseLimit([]byte(base+df.String()), 1); len(r.Disks) != 1 {
		t.Fatalf("seedbox cap: %d disks", len(r.Disks))
	}

	// Whatever survives parsing must always encode (the live stream relies on it).
	s := Compute("box", time.Now(), r, nil, time.Time{})
	if _, err := json.Marshal(s); err != nil {
		t.Fatalf("sample does not encode: %v", err)
	}
}

func TestParseQuota(t *testing.T) {
	base := fixture[:strings.Index(fixture, "@@DF")] +
		"@@DF\nFilesystem 1024-blocks Used Available Capacity Mounted on\n" +
		"/dev/mapper/mpathe 21483235328 21068951468 414283860 99% /mnt/mpathe\n@@QUOTA\n"

	// Whatbox's own quota tool (real output).
	r, err := Parse([]byte(base + `{"plan":21700000002048.0,"used":21284396404736.0,"free":415603597312.0,"percent_free":1.0}` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Disks) != 1 || r.Disks[0] != (Disk{Mount: QuotaMount, Size: 21700000002048, Used: 21284396404736}) {
		t.Fatalf("whatbox quota: %+v", r.Disks)
	}

	// Standard quota -w (real itsby / HostingByDesign layout).
	std := "Disk quotas for user phantomhex (uid 1085):\n" +
		"     Filesystem  blocks   quota   limit   grace   files   quota   limit   grace\n" +
		"      /dev/sdaa 2554331136  27344699392 27344699392            150123       0       0\n"
	r, err = Parse([]byte(base + std))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Disks) != 1 || r.Disks[0].Size != 27344699392*1024 || r.Disks[0].Used != 2554331136*1024 {
		t.Fatalf("std quota: %+v", r.Disks)
	}

	// Over quota: '*' marker; soft 0 falls back to hard limit.
	r, _ = Parse([]byte(base + "/dev/sdb 1200* 0 1000 6days 5 0 0\n"))
	if r.Disks[0].Size != 1000*1024 || r.Disks[0].Used != 1000*1024 {
		t.Fatalf("over quota: %+v", r.Disks)
	}

	// No usable quota: df stays.
	for _, bad := range []string{
		"", "/dev/sdb 10 0 0 0 0 0\n", `{"plan":"NaN","used":1}` + "\n", `{"plan":10,"used":20}` + "\n",
		`{"plan":1e400,"used":1}` + "\n", "quota: error: not allowed\n",
	} {
		r, err := Parse([]byte(base + bad))
		if err != nil || len(r.Disks) != 1 || r.Disks[0].Mount != "/mnt/mpathe" {
			t.Errorf("%q: disks = %+v, err = %v (df must stay)", bad, r.Disks, err)
		}
	}
}
