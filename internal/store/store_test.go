package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fs0ciety7000/argos/internal/collector"
	"github.com/fs0ciety7000/argos/internal/docker"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "argos.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sample(host string, ts time.Time, cpu, rx float64, used uint64) collector.Sample {
	return collector.Sample{
		Host: host, TS: ts, CPU: cpu, MemUsed: 4 << 30, MemTotal: 8 << 30, Load1: 0.5, RxBps: rx, TxBps: rx / 2,
		Disks: []collector.Disk{{Mount: "/", Size: 100 << 30, Used: used}},
	}
}

func TestInsertHistoryAndRollup(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	base := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	for i := range 24 { // 2 minutes at 5 s
		cpu := float64(i)
		if i == 0 {
			cpu = -1 // first reading: unknown
		}
		if err := s.Insert(ctx, sample("a", base.Add(time.Duration(i)*5*time.Second), cpu, 1000, 10<<30), i%12 == 0); err != nil {
			t.Fatal(err)
		}
	}
	now := base.Add(2 * time.Minute)

	h, err := s.History(ctx, "a", Range1h, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.TS) != 24 || h.CPU[0] != nil || *h.CPU[5] != 5 || *h.MemPct[0] != 50 {
		t.Fatalf("raw history wrong: n=%d cpu0=%v", len(h.TS), h.CPU[0])
	}

	if err := s.Rollup(ctx, now, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.Rollup(ctx, now, 10*time.Minute); err != nil { // idempotent
		t.Fatal(err)
	}
	m, err := s.History(ctx, "a", Range24h, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.TS) != 2 {
		t.Fatalf("want 2 minute buckets, got %d", len(m.TS))
	}
	// minute 0: cpu 1..11 (first unknown is ignored by AVG) → 6
	if *m.CPU[0] != 6 {
		t.Fatalf("minute avg cpu = %v, want 6", *m.CPU[0])
	}
	if h, _ := s.History(ctx, "b", Range1h, now); len(h.TS) != 0 || h.CPU == nil {
		t.Fatal("unknown host should return empty (non-nil) series")
	}
}

func TestAggregateThroughputSumsHosts(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	base := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	for i := range 6 { // a: every 5 s at 1000 B/s
		_ = s.Insert(ctx, sample("a", base.Add(time.Duration(i)*5*time.Second), 1, 1000, 1), false)
	}
	for i := range 2 { // b: every 15 s at 3000 B/s
		_ = s.Insert(ctx, sample("b", base.Add(time.Duration(i)*15*time.Second), 1, 3000, 1), false)
	}
	tp, err := s.AggregateThroughput(ctx, Range1h, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(tp.TS) != 1 || *tp.Rx[0] != 4000 || *tp.Tx[0] != 2000 {
		t.Fatalf("aggregate = %+v (want one 30 s bucket summing 1000+3000)", tp)
	}
}

func TestDiskTrendsAndPurge(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	// +10 GiB per day over 2 days, disk 100 GiB, 30 GiB used at the end.
	for i := range 3 {
		_ = s.Insert(ctx, sample("a", base.Add(time.Duration(i)*24*time.Hour), 1, 1, uint64(10+i*10)<<30), true)
	}
	now := base.Add(2 * 24 * time.Hour)
	trends, err := s.DiskTrends(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(trends) != 1 || trends[0].DaysToFull == nil {
		t.Fatalf("trends = %+v", trends)
	}
	if got := *trends[0].DaysToFull; got < 6.99 || got > 7.01 {
		t.Fatalf("days to full = %v, want 7", got)
	}

	if err := s.Purge(ctx, now.Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if trends, _ := s.DiskTrends(ctx, now.Add(8*24*time.Hour)); len(trends) != 0 {
		t.Fatalf("purge left %d trends", len(trends))
	}
}

func TestParseRange(t *testing.T) {
	for _, ok := range []string{"1h", "24h", "7d"} {
		if _, err := ParseRange(ok); err != nil {
			t.Errorf("%s rejected", ok)
		}
	}
	for _, bad := range []string{"", "2h", "1h; DROP TABLE samples"} {
		if _, err := ParseRange(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestActionsJournal(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	base := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	for i, ok := range []bool{true, false} {
		if err := s.LogAction(ctx, docker.Action{TS: base.Add(time.Duration(i) * time.Minute), Host: "vps",
			Container: "web", Action: "restart", Trigger: "manual", OK: ok, Message: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.RecentActions(ctx, 10)
	if err != nil || len(got) != 2 || got[0].OK || !got[1].OK || got[0].Container != "web" {
		t.Fatalf("actions = %+v, %v", got, err)
	}
	_ = s.Purge(ctx, base.Add(31*24*time.Hour))
	if got, _ := s.RecentActions(ctx, 10); len(got) != 0 {
		t.Fatal("old actions not purged")
	}
}

func TestDiskTrendsDropsStaleMounts(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	base := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	old := collector.Sample{Host: "box", TS: base, MemTotal: 1, Disks: []collector.Disk{{Mount: "/mnt/shared", Size: 100, Used: 99}}}
	cur := collector.Sample{Host: "box", TS: base.Add(time.Hour), MemTotal: 1, Disks: []collector.Disk{{Mount: "quota", Size: 50, Used: 10}}}
	if err := s.Insert(ctx, old, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Insert(ctx, cur, true); err != nil {
		t.Fatal(err)
	}
	trends, err := s.DiskTrends(ctx, base.Add(time.Hour))
	if err != nil || len(trends) != 1 || trends[0].Mount != "quota" {
		t.Fatalf("trends = %+v, %v (stale mount must be dropped)", trends, err)
	}
}

func TestDiskGrowth24hAndHistory(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	ins := func(at time.Time, used uint64) {
		t.Helper()
		if err := s.Insert(ctx, collector.Sample{Host: "a", TS: at, MemTotal: 1,
			Disks: []collector.Disk{{Mount: "/", Size: 1000 << 30, Used: used << 30}}}, true); err != nil {
			t.Fatal(err)
		}
	}
	// Flat for 5 days, then +100 GiB over the last 24 h.
	ins(base, 100)
	ins(base.Add(5*24*time.Hour), 100)
	ins(base.Add(5*24*time.Hour+time.Hour), 120)
	ins(base.Add(6*24*time.Hour), 200)
	now := base.Add(6 * 24 * time.Hour)

	trends, err := s.DiskTrends(ctx, now)
	if err != nil || len(trends) != 1 {
		t.Fatalf("trends = %+v, %v", trends, err)
	}
	d := trends[0]
	if got := d.BytesPerDay / (1 << 30); got < 16 || got > 17 { // 100 GiB over 6 days
		t.Fatalf("7-day rate = %.2f GiB/day", got)
	}
	if d.BytesPerDay24h == nil || *d.BytesPerDay24h/(1<<30) < 99 { // ~100 GiB in the last day
		t.Fatalf("24 h rate = %v", d.BytesPerDay24h)
	}
	if d.DaysToFull24h == nil || *d.DaysToFull24h < 7.9 || *d.DaysToFull24h > 8.1 { // 800 GiB left at 100/day
		t.Fatalf("days to full (24 h) = %v", d.DaysToFull24h)
	}

	h, err := s.DiskHistory(ctx, "a", "/", Range7d, now)
	if err != nil || len(h.TS) != 4 || h.Used[3] != 200<<30 || h.Size[0] != 1000<<30 {
		t.Fatalf("7d history = %+v, %v", h, err)
	}
	h, _ = s.DiskHistory(ctx, "a", "/", Range24h, now)
	if len(h.TS) != 3 {
		t.Fatalf("24h history has %d points, want 3", len(h.TS))
	}
	if h, _ := s.DiskHistory(ctx, "a", "/nope", Range24h, now); len(h.TS) != 0 || h.TS == nil {
		t.Fatal("unknown mount should give an empty, non-nil series")
	}
}
