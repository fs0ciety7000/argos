package collector

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/fs0ciety7000/argos/internal/inventory"
	"github.com/fs0ciety7000/argos/internal/sshpool"
)

// Runner runs a named command on a host (sshpool.Pool).
type Runner interface {
	Run(ctx context.Context, host string, cmd sshpool.Command) ([]byte, error)
}

// Sink receives every computed sample (storage, live hub).
type Sink interface {
	Insert(ctx context.Context, s Sample, withDisks bool) error
}

// Maintainer runs periodic database upkeep.
type Maintainer interface {
	Rollup(ctx context.Context, now time.Time, lookback time.Duration) error
	Purge(ctx context.Context, now time.Time) error
}

// Options sets polling intervals; zero values get defaults.
type Options struct {
	VPSEvery     time.Duration // default 5 s
	SeedboxEvery time.Duration // default 15 s, gentle on shared machines
	DiskEvery    time.Duration // disk readings stored at most this often, default 1 min
}

// Collector polls every host on its own schedule.
type Collector struct {
	runner  Runner
	sink    Sink
	maint   Maintainer
	publish func(Sample)
	logger  *slog.Logger
	hosts   []inventory.Host
	opts    Options

	mu     sync.RWMutex
	latest map[string]Sample
}

// New builds a collector. publish may be nil.
func New(hosts []inventory.Host, runner Runner, sink Sink, maint Maintainer, publish func(Sample), logger *slog.Logger, opts Options) *Collector {
	if opts.VPSEvery == 0 {
		opts.VPSEvery = 5 * time.Second
	}
	if opts.SeedboxEvery == 0 {
		opts.SeedboxEvery = 15 * time.Second
	}
	if opts.DiskEvery == 0 {
		opts.DiskEvery = time.Minute
	}
	if publish == nil {
		publish = func(Sample) {}
	}
	return &Collector{runner: runner, sink: sink, maint: maint, publish: publish, logger: logger,
		hosts: hosts, opts: opts, latest: map[string]Sample{}}
}

// Latest returns the most recent sample of every host that has one.
func (c *Collector) Latest() map[string]Sample {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]Sample, len(c.latest))
	for k, v := range c.latest {
		out[k] = v
	}
	return out
}

// Start launches one poller per host plus the maintenance loop.
func (c *Collector) Start(ctx context.Context) *sync.WaitGroup {
	var wg sync.WaitGroup
	for _, h := range c.hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.poll(ctx, h)
		}()
	}
	if c.maint != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.maintain(ctx)
		}()
	}
	return &wg
}

func (c *Collector) poll(ctx context.Context, h inventory.Host) {
	every, cmd := c.opts.VPSEvery, sshpool.CmdMetricsVPS
	if h.Kind == inventory.KindSeedbox {
		every, cmd = c.opts.SeedboxEvery, sshpool.CmdMetricsSeedbox
	}
	t := time.NewTicker(every)
	defer t.Stop()

	var prev *Raw
	var prevTS, lastDisk, lastWarn time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		out, err := c.runner.Run(ctx, h.Name, cmd)
		if err != nil {
			prev = nil // rates across a gap would be misleading
			// Not connected is already reported by the pool; other errors
			// are logged at most every 5 minutes per host.
			if !errors.Is(err, sshpool.ErrNotConnected) && ctx.Err() == nil && time.Since(lastWarn) > 5*time.Minute {
				c.logger.Warn("metrics collection failed", "host", h.Name, "err", err)
				lastWarn = time.Now()
			}
			continue
		}
		raw, err := Parse(out)
		if err != nil {
			if time.Since(lastWarn) > 5*time.Minute {
				c.logger.Warn("metrics output not understood", "host", h.Name, "err", err)
				lastWarn = time.Now()
			}
			prev = nil
			continue
		}
		now := time.Now()
		s := Compute(h.Name, now, raw, prev, prevTS)
		prev, prevTS = &raw, now

		c.mu.Lock()
		c.latest[h.Name] = s
		c.mu.Unlock()

		withDisks := now.Sub(lastDisk) >= c.opts.DiskEvery
		if err := c.sink.Insert(ctx, s, withDisks); err != nil {
			if ctx.Err() == nil {
				c.logger.Error("store sample", "host", h.Name, "err", err)
			}
		} else if withDisks {
			lastDisk = now
		}
		c.publish(s)
	}
}

func (c *Collector) maintain(ctx context.Context) {
	rollup := time.NewTicker(time.Minute)
	defer rollup.Stop()
	purge := time.NewTicker(time.Hour)
	defer purge.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-rollup.C:
			if err := c.maint.Rollup(ctx, now, 5*time.Minute); err != nil && ctx.Err() == nil {
				c.logger.Error("rollup", "err", err)
			}
		case now := <-purge.C:
			if err := c.maint.Purge(ctx, now); err != nil && ctx.Err() == nil {
				c.logger.Error("purge", "err", err)
			}
		}
	}
}
