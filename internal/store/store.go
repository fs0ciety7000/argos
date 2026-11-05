// Package store keeps metric history in SQLite: raw samples for 24 h,
// one-minute aggregates and disk readings for 7 days.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, no CGO

	"github.com/fs0ciety7000/argos/internal/collector"
	"github.com/fs0ciety7000/argos/internal/docker"
)

const (
	RawRetention    = 24 * time.Hour
	MinuteRetention = 7 * 24 * time.Hour
	ActionRetention = 30 * 24 * time.Hour
)

const schema = `
CREATE TABLE IF NOT EXISTS samples (
	host TEXT NOT NULL, ts INTEGER NOT NULL,
	cpu REAL, mem_used INTEGER NOT NULL, mem_total INTEGER NOT NULL,
	load1 REAL NOT NULL, rx REAL, tx REAL,
	PRIMARY KEY (host, ts)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS samples_1m (
	host TEXT NOT NULL, ts INTEGER NOT NULL,
	cpu_avg REAL, cpu_max REAL, mem_used INTEGER NOT NULL, mem_total INTEGER NOT NULL,
	load1 REAL NOT NULL, rx_avg REAL, rx_max REAL, tx_avg REAL, tx_max REAL,
	PRIMARY KEY (host, ts)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS actions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	ts INTEGER NOT NULL, host TEXT NOT NULL, container TEXT NOT NULL,
	action TEXT NOT NULL, trigger TEXT NOT NULL, ok INTEGER NOT NULL, message TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS actions_ts ON actions (ts);
CREATE TABLE IF NOT EXISTS disks (
	host TEXT NOT NULL, mount TEXT NOT NULL, ts INTEGER NOT NULL,
	size INTEGER NOT NULL, used INTEGER NOT NULL,
	PRIMARY KEY (host, mount, ts)
) WITHOUT ROWID;
`

// Store wraps the database.
type Store struct {
	db *sql.DB
}

// Open opens (creating) the database at path and applies the schema.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One writer at a time keeps SQLite simple and avoids SQLITE_BUSY.
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func nullable(v float64) any {
	if v < 0 {
		return nil
	}
	return v
}

// Insert stores a sample; withDisks also records its disk readings (the
// collector does this at most once a minute per host).
func (s *Store) Insert(ctx context.Context, smp collector.Sample, withDisks bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	ts := smp.TS.UnixMilli()
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO samples (host, ts, cpu, mem_used, mem_total, load1, rx, tx) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		smp.Host, ts, nullable(smp.CPU), int64(smp.MemUsed), int64(smp.MemTotal), smp.Load1, nullable(smp.RxBps), nullable(smp.TxBps),
	); err != nil {
		return fmt.Errorf("insert sample: %w", err)
	}
	if withDisks {
		for _, d := range smp.Disks {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR REPLACE INTO disks (host, mount, ts, size, used) VALUES (?, ?, ?, ?, ?)`,
				smp.Host, d.Mount, ts, int64(d.Size), int64(d.Used),
			); err != nil {
				return fmt.Errorf("insert disk: %w", err)
			}
		}
	}
	return tx.Commit()
}

// Rollup (re)computes one-minute aggregates for the complete minutes in
// [now-lookback, now). It is idempotent.
func (s *Store) Rollup(ctx context.Context, now time.Time, lookback time.Duration) error {
	end := now.Truncate(time.Minute).UnixMilli()
	start := now.Add(-lookback).Truncate(time.Minute).UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO samples_1m (host, ts, cpu_avg, cpu_max, mem_used, mem_total, load1, rx_avg, rx_max, tx_avg, tx_max)
		SELECT host, (ts / 60000) * 60000 AS bucket,
			AVG(cpu), MAX(cpu), CAST(AVG(mem_used) AS INTEGER), MAX(mem_total), AVG(load1),
			AVG(rx), MAX(rx), AVG(tx), MAX(tx)
		FROM samples WHERE ts >= ? AND ts < ?
		GROUP BY host, bucket`, start, end)
	if err != nil {
		return fmt.Errorf("rollup: %w", err)
	}
	return nil
}

// Purge drops data past retention.
func (s *Store) Purge(ctx context.Context, now time.Time) error {
	raw := now.Add(-RawRetention).UnixMilli()
	minute := now.Add(-MinuteRetention).UnixMilli()
	for _, q := range []struct {
		sql string
		cut int64
	}{
		{`DELETE FROM samples WHERE ts < ?`, raw},
		{`DELETE FROM samples_1m WHERE ts < ?`, minute},
		{`DELETE FROM disks WHERE ts < ?`, minute},
		{`DELETE FROM actions WHERE ts < ?`, now.Add(-ActionRetention).UnixMilli()},
	} {
		if _, err := s.db.ExecContext(ctx, q.sql, q.cut); err != nil {
			return fmt.Errorf("purge: %w", err)
		}
	}
	return nil
}

// Range is a supported history window.
type Range string

const (
	Range1h  Range = "1h"
	Range24h Range = "24h"
	Range7d  Range = "7d"
)

// ParseRange validates a range parameter.
func ParseRange(s string) (Range, error) {
	switch r := Range(s); r {
	case Range1h, Range24h, Range7d:
		return r, nil
	}
	return "", errors.New("range must be 1h, 24h or 7d")
}

func (r Range) window() time.Duration {
	switch r {
	case Range24h:
		return 24 * time.Hour
	case Range7d:
		return 7 * 24 * time.Hour
	default:
		return time.Hour
	}
}

// Series is column-oriented history, the shape uPlot consumes. Null
// values (unknown rates) are encoded as JSON null.
type Series struct {
	TS     []int64    `json:"ts"` // unix seconds
	CPU    []*float64 `json:"cpu"`
	MemPct []*float64 `json:"memPct"`
	Rx     []*float64 `json:"rx"`
	Tx     []*float64 `json:"tx"`
}

// History returns one host's series for a range: raw points for 1h, minute
// aggregates for 24h, 10-minute buckets of those for 7d.
func (s *Store) History(ctx context.Context, host string, r Range, now time.Time) (Series, error) {
	since := now.Add(-r.window()).UnixMilli()
	var q string
	switch r {
	case Range1h:
		q = `SELECT ts, cpu, CAST(mem_used AS REAL) * 100 / mem_total, rx, tx FROM samples
			WHERE host = ? AND ts >= ? ORDER BY ts`
	case Range24h:
		q = `SELECT ts, cpu_avg, CAST(mem_used AS REAL) * 100 / mem_total, rx_avg, tx_avg FROM samples_1m
			WHERE host = ? AND ts >= ? ORDER BY ts`
	default:
		q = `SELECT (ts / 600000) * 600000 AS b, AVG(cpu_avg), AVG(CAST(mem_used AS REAL) * 100 / mem_total), AVG(rx_avg), AVG(tx_avg)
			FROM samples_1m WHERE host = ? AND ts >= ? GROUP BY b ORDER BY b`
	}
	rows, err := s.db.QueryContext(ctx, q, host, since)
	if err != nil {
		return Series{}, fmt.Errorf("history: %w", err)
	}
	defer rows.Close()
	out := Series{TS: []int64{}, CPU: []*float64{}, MemPct: []*float64{}, Rx: []*float64{}, Tx: []*float64{}}
	for rows.Next() {
		var ts int64
		var cpu, mem, rx, tx sql.NullFloat64
		if err := rows.Scan(&ts, &cpu, &mem, &rx, &tx); err != nil {
			return Series{}, err
		}
		out.TS = append(out.TS, ts/1000)
		out.CPU = append(out.CPU, ptr(cpu))
		out.MemPct = append(out.MemPct, ptr(mem))
		out.Rx = append(out.Rx, ptr(rx))
		out.Tx = append(out.Tx, ptr(tx))
	}
	return out, rows.Err()
}

// Throughput is aggregated network traffic across all hosts.
type Throughput struct {
	TS []int64    `json:"ts"`
	Rx []*float64 `json:"rx"`
	Tx []*float64 `json:"tx"`
}

// AggregateThroughput sums per-host average rates in time buckets sized to
// the range (30 s, 5 min, 30 min), so hosts sampled at different rates add
// up correctly.
func (s *Store) AggregateThroughput(ctx context.Context, r Range, now time.Time) (Throughput, error) {
	since := now.Add(-r.window()).UnixMilli()
	table, rx, tx, bucket := "samples", "rx", "tx", int64(30_000)
	switch r {
	case Range24h:
		table, rx, tx, bucket = "samples_1m", "rx_avg", "tx_avg", 300_000
	case Range7d:
		table, rx, tx, bucket = "samples_1m", "rx_avg", "tx_avg", 1_800_000
	}
	// Table and column names come from the switch above, never from input.
	q := fmt.Sprintf(`SELECT b, SUM(r), SUM(t) FROM (
			SELECT host, (ts / ?) * ? AS b, AVG(%s) AS r, AVG(%s) AS t FROM %s WHERE ts >= ? GROUP BY host, b
		) GROUP BY b ORDER BY b`, rx, tx, table)
	rows, err := s.db.QueryContext(ctx, q, bucket, bucket, since)
	if err != nil {
		return Throughput{}, fmt.Errorf("throughput: %w", err)
	}
	defer rows.Close()
	out := Throughput{TS: []int64{}, Rx: []*float64{}, Tx: []*float64{}}
	for rows.Next() {
		var ts int64
		var r, t sql.NullFloat64
		if err := rows.Scan(&ts, &r, &t); err != nil {
			return Throughput{}, err
		}
		out.TS = append(out.TS, ts/1000)
		out.Rx = append(out.Rx, ptr(r))
		out.Tx = append(out.Tx, ptr(t))
	}
	return out, rows.Err()
}

// DiskTrend is the latest reading of a mount plus its 7-day growth.
type DiskTrend struct {
	Host        string   `json:"host"`
	Mount       string   `json:"mount"`
	Size        int64    `json:"size"`
	Used        int64    `json:"used"`
	BytesPerDay float64  `json:"bytesPerDay"`          // over the whole window (up to 7 days)
	DaysToFull  *float64 `json:"daysToFull,omitempty"` // nil when not growing
	// Growth over the last 24 h: reacts faster to a big download.
	BytesPerDay24h *float64 `json:"bytesPerDay24h,omitempty"`
	DaysToFull24h  *float64 `json:"daysToFull24h,omitempty"`
	Since          int64    `json:"since"` // unix seconds of the oldest reading used
	UpdatedAt      int64    `json:"updatedAt"`
}

// minSlopeSpan is the history needed before a growth rate means anything.
const minSlopeSpan = time.Hour

// DiskTrends returns, for every host and mount still reported, the latest
// reading and linear projections over the window and over the last 24 h.
func (s *Store) DiskTrends(ctx context.Context, now time.Time) ([]DiskTrend, error) {
	since := now.Add(-MinuteRetention).UnixMilli()
	day := now.Add(-24 * time.Hour).UnixMilli()
	rows, err := s.db.QueryContext(ctx, `
		WITH bounds AS (
			SELECT host, mount, MIN(ts) AS t0, MAX(ts) AS t1, MIN(CASE WHEN ts >= ? THEN ts END) AS t24
			FROM disks WHERE ts >= ? GROUP BY host, mount
		), latest AS (
			SELECT host, MAX(ts) AS t FROM disks WHERE ts >= ? GROUP BY host
		)
		SELECT b.host, b.mount, b.t0, b.t1, f.used, l.size, l.used, b.t24, d.used
		FROM bounds b
		JOIN disks f ON f.host = b.host AND f.mount = b.mount AND f.ts = b.t0
		JOIN disks l ON l.host = b.host AND l.mount = b.mount AND l.ts = b.t1
		LEFT JOIN disks d ON d.host = b.host AND d.mount = b.mount AND d.ts = b.t24
		JOIN latest x ON x.host = b.host
		-- Only mounts still reported recently: a mount replaced (df -> quota)
		-- or unmounted must not linger for 7 days and be counted twice.
		WHERE b.t1 >= x.t - 600000
		ORDER BY b.host, b.mount`, day, since, since)
	if err != nil {
		return nil, fmt.Errorf("disk trends: %w", err)
	}
	defer rows.Close()
	out := []DiskTrend{}
	for rows.Next() {
		var d DiskTrend
		var t0, t1, firstUsed int64
		var t24, used24 sql.NullInt64
		if err := rows.Scan(&d.Host, &d.Mount, &t0, &t1, &firstUsed, &d.Size, &d.Used, &t24, &used24); err != nil {
			return nil, err
		}
		d.Since, d.UpdatedAt = t0/1000, t1/1000
		if rate, ok := slope(firstUsed, d.Used, t0, t1); ok {
			d.BytesPerDay = rate
			d.DaysToFull = daysToFull(d.Size, d.Used, rate)
		}
		if t24.Valid && used24.Valid {
			if rate, ok := slope(used24.Int64, d.Used, t24.Int64, t1); ok {
				d.BytesPerDay24h = &rate
				d.DaysToFull24h = daysToFull(d.Size, d.Used, rate)
			}
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// slope returns bytes per day between two readings (timestamps in ms).
func slope(usedA, usedB, tA, tB int64) (float64, bool) {
	if tB-tA < minSlopeSpan.Milliseconds() {
		return 0, false
	}
	return float64(usedB-usedA) / (float64(tB-tA) / 86_400_000), true
}

func daysToFull(size, used int64, perDay float64) *float64 {
	if perDay <= 0 || size <= used {
		return nil
	}
	d := float64(size-used) / perDay
	return &d
}

// DiskSeries is one volume's fill history, column-oriented for uPlot.
type DiskSeries struct {
	TS   []int64 `json:"ts"` // unix seconds
	Used []int64 `json:"used"`
	Size []int64 `json:"size"`
}

// DiskHistory returns a volume's readings: one per minute over 24 h,
// 10-minute averages over 7 days.
func (s *Store) DiskHistory(ctx context.Context, host, mount string, r Range, now time.Time) (DiskSeries, error) {
	since := now.Add(-r.window()).UnixMilli()
	q := `SELECT ts, used, size FROM disks WHERE host = ? AND mount = ? AND ts >= ? ORDER BY ts`
	if r == Range7d {
		q = `SELECT (ts / 600000) * 600000 AS b, CAST(AVG(used) AS INTEGER), MAX(size)
			FROM disks WHERE host = ? AND mount = ? AND ts >= ? GROUP BY b ORDER BY b`
	}
	rows, err := s.db.QueryContext(ctx, q, host, mount, since)
	if err != nil {
		return DiskSeries{}, fmt.Errorf("disk history: %w", err)
	}
	defer rows.Close()
	out := DiskSeries{TS: []int64{}, Used: []int64{}, Size: []int64{}}
	for rows.Next() {
		var ts, used, size int64
		if err := rows.Scan(&ts, &used, &size); err != nil {
			return DiskSeries{}, err
		}
		out.TS = append(out.TS, ts/1000)
		out.Used = append(out.Used, used)
		out.Size = append(out.Size, size)
	}
	return out, rows.Err()
}

func ptr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

// LogAction appends an entry to the action journal.
func (s *Store) LogAction(ctx context.Context, a docker.Action) error {
	ok := 0
	if a.OK {
		ok = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO actions (ts, host, container, action, trigger, ok, message) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.TS.UnixMilli(), a.Host, a.Container, a.Action, a.Trigger, ok, a.Message)
	if err != nil {
		return fmt.Errorf("log action: %w", err)
	}
	return nil
}

// RecentActions returns the latest journal entries, newest first.
func (s *Store) RecentActions(ctx context.Context, limit int) ([]docker.Action, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT ts, host, container, action, trigger, ok, message FROM actions ORDER BY ts DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("recent actions: %w", err)
	}
	defer rows.Close()
	out := []docker.Action{}
	for rows.Next() {
		var a docker.Action
		var ts int64
		var ok int
		if err := rows.Scan(&ts, &a.Host, &a.Container, &a.Action, &a.Trigger, &ok, &a.Message); err != nil {
			return nil, err
		}
		a.TS, a.OK = time.UnixMilli(ts).UTC(), ok == 1
		out = append(out, a)
	}
	return out, rows.Err()
}
