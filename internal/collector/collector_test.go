package collector

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/fs0ciety7000/argos/internal/inventory"
	"github.com/fs0ciety7000/argos/internal/sshpool"
)

type fakeRunner struct {
	mu    sync.Mutex
	calls map[string][]sshpool.Command
	fail  map[string]error
}

func (f *fakeRunner) Run(_ context.Context, host string, cmd sshpool.Command) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[host] = append(f.calls[host], cmd)
	if err := f.fail[host]; err != nil {
		return nil, err
	}
	return []byte(fixture), nil
}

type fakeSink struct {
	mu        sync.Mutex
	samples   []Sample
	diskTimes int
}

func (f *fakeSink) Insert(_ context.Context, s Sample, withDisks bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.samples = append(f.samples, s)
	if withDisks {
		f.diskTimes++
	}
	return nil
}

func TestCollectorPollsByKind(t *testing.T) {
	hosts := []inventory.Host{
		{Name: "vps", Kind: inventory.KindVPS},
		{Name: "box", Kind: inventory.KindSeedbox},
		{Name: "down", Kind: inventory.KindVPS},
	}
	runner := &fakeRunner{calls: map[string][]sshpool.Command{}, fail: map[string]error{"down": sshpool.ErrNotConnected}}
	sink := &fakeSink{}
	var published sync.Map
	c := New(hosts, runner, sink, nil, func(s Sample) { published.Store(s.Host, true) },
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options{VPSEvery: 10 * time.Millisecond, SeedboxEvery: 30 * time.Millisecond, DiskEvery: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	wg := c.Start(ctx)
	time.Sleep(200 * time.Millisecond)
	cancel()
	wg.Wait()

	runner.mu.Lock()
	vpsCalls, boxCalls := len(runner.calls["vps"]), len(runner.calls["box"])
	vpsCmd, boxCmd := runner.calls["vps"][0], runner.calls["box"][0]
	runner.mu.Unlock()
	if vpsCalls <= boxCalls {
		t.Fatalf("seedbox should be polled less often: vps=%d box=%d", vpsCalls, boxCalls)
	}
	if vpsCmd != sshpool.CmdMetricsVPS || boxCmd != sshpool.CmdMetricsSeedbox {
		t.Fatal("wrong command for host kind")
	}
	if _, ok := published.Load("vps"); !ok {
		t.Fatal("vps sample not published")
	}
	if _, ok := published.Load("down"); ok {
		t.Fatal("disconnected host must not publish")
	}
	latest := c.Latest()
	if _, ok := latest["vps"]; !ok || len(latest) != 2 {
		t.Fatalf("latest = %v", latest)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.diskTimes != 2 { // first sample of each reachable host, then throttled by DiskEvery
		t.Fatalf("disk readings stored %d times, want 2", sink.diskTimes)
	}
}

func TestCollectorSurvivesErrors(t *testing.T) {
	hosts := []inventory.Host{{Name: "vps", Kind: inventory.KindVPS}}
	runner := &fakeRunner{calls: map[string][]sshpool.Command{}, fail: map[string]error{"vps": errors.New("boom")}}
	c := New(hosts, runner, &fakeSink{}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options{VPSEvery: 5 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	c.Start(ctx).Wait()
	if len(c.Latest()) != 0 {
		t.Fatal("failed host has a sample")
	}
}
