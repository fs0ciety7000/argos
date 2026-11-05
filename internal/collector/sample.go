package collector

import "time"

// Sample is one computed point for a host, ready to store and display.
type Sample struct {
	Host     string    `json:"host"`
	TS       time.Time `json:"ts"`
	CPU      float64   `json:"cpu"`      // percent, -1 on the first reading
	MemUsed  uint64    `json:"memUsed"`  // bytes
	MemTotal uint64    `json:"memTotal"` // bytes
	Load1    float64   `json:"load1"`
	RxBps    float64   `json:"rxBps"` // bytes per second, -1 on the first reading
	TxBps    float64   `json:"txBps"`
	Disks    []Disk    `json:"disks"`
}

// Compute derives a sample from the current reading and the previous one
// (prev may be nil). Counter resets (reboot, interface recreated) yield -1
// for the affected rate instead of a huge spike.
func Compute(host string, now time.Time, cur Raw, prev *Raw, prevTS time.Time) Sample {
	s := Sample{
		Host: host, TS: now, CPU: -1, RxBps: -1, TxBps: -1,
		MemTotal: cur.MemTotal, Load1: cur.Load1, Disks: cur.Disks,
	}
	if cur.MemAvailable <= cur.MemTotal {
		s.MemUsed = cur.MemTotal - cur.MemAvailable
	}
	if prev == nil {
		return s
	}
	if dt, di := int64(cur.CPUTotal)-int64(prev.CPUTotal), int64(cur.CPUIdle)-int64(prev.CPUIdle); dt > 0 && di >= 0 && di <= dt {
		s.CPU = 100 * float64(dt-di) / float64(dt)
	}
	secs := now.Sub(prevTS).Seconds()
	if secs <= 0 {
		return s
	}
	var rx, tx float64
	ok := false
	for name, c := range cur.Net {
		p, found := prev.Net[name]
		if !found || c.Rx < p.Rx || c.Tx < p.Tx {
			continue
		}
		rx += float64(c.Rx - p.Rx)
		tx += float64(c.Tx - p.Tx)
		ok = true
	}
	if ok {
		s.RxBps, s.TxBps = rx/secs, tx/secs
	}
	return s
}
