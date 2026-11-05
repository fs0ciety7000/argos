// Package collector polls hosts over SSH, turns /proc and df output into
// samples, stores them and publishes them live.
package collector

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Raw is one parsed reading of a host. Counters (CPU, network) only make
// sense as deltas between two readings.
type Raw struct {
	CPUTotal, CPUIdle uint64
	MemTotal          uint64 // bytes
	MemAvailable      uint64 // bytes
	Load1             float64
	Net               map[string]NetCounters
	Disks             []Disk
	// Quota is the account's real storage quota on a seedbox, when the
	// host reports one. It replaces Disks (df shows the shared disk).
	Quota *Disk
}

// QuotaMount is the mount name used for quota-based readings.
const QuotaMount = "quota"

// NetCounters are cumulative interface byte counters.
type NetCounters struct{ Rx, Tx uint64 }

// Disk is one mounted filesystem, sizes in bytes.
type Disk struct {
	Mount string `json:"mount"`
	Size  uint64 `json:"size"`
	Used  uint64 `json:"used"`
}

// Output comes from remote hosts, including shared seedboxes whose root is
// a third party: every value is bounded before it reaches the database or
// the JSON encoder.
const (
	maxDisks      = 16
	maxMountLen   = 256
	maxKiB        = math.MaxInt64 / 1024
	maxLoad       = 1e6
	maxQuotedSpan = 64
)

// Parse reads the output of sshpool.CmdMetrics*. maxDisks caps the number
// of filesystems kept (1 for a seedbox, whose df only covers $HOME).
func Parse(out []byte) (Raw, error) { return ParseLimit(out, maxDisks) }

// ParseLimit is Parse with an explicit disk cap.
func ParseLimit(out []byte, diskCap int) (Raw, error) {
	r := Raw{Net: map[string]NetCounters{}}
	var section string
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "@@") {
			section = line[2:]
			seen[section] = true
			continue
		}
		var err error
		switch section {
		case "STAT":
			err = r.parseStat(line)
		case "MEM":
			r.parseMem(line)
		case "LOAD":
			err = r.parseLoad(line)
		case "NET":
			r.parseNet(line)
		case "DF":
			r.parseDF(line, min(diskCap, maxDisks))
		case "QUOTA":
			r.parseQuota(line)
		}
		if err != nil {
			return Raw{}, fmt.Errorf("%s: %w", strings.ToLower(section), err)
		}
	}
	if err := sc.Err(); err != nil {
		return Raw{}, err
	}
	for _, s := range []string{"STAT", "MEM", "LOAD", "NET"} {
		if !seen[s] {
			return Raw{}, fmt.Errorf("missing %s section", strings.ToLower(s))
		}
	}
	if r.CPUTotal == 0 || r.MemTotal == 0 {
		return Raw{}, errors.New("incomplete cpu or memory data")
	}
	if r.Quota != nil {
		r.Disks = []Disk{*r.Quota}
	}
	return r, nil
}

// parseQuota understands two formats, first match wins:
//   - Whatbox: {"plan":21700000002048.0,"used":21284396404736.0,"free":...} (bytes)
//   - quota -w: "/dev/sdaa  2554  27344  27344  ...  files quota limit grace" (KiB)
func (r *Raw) parseQuota(line string) {
	if r.Quota != nil {
		return
	}
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "{") {
		var q struct {
			Plan *float64 `json:"plan"`
			Used *float64 `json:"used"`
		}
		if json.Unmarshal([]byte(line), &q) != nil || q.Plan == nil || q.Used == nil {
			return
		}
		size, ok1 := bytesValue(*q.Plan)
		used, ok2 := bytesValue(*q.Used)
		if ok1 && ok2 && size > 0 && used <= size {
			r.Quota = &Disk{Mount: QuotaMount, Size: size, Used: used}
		}
		return
	}
	f := strings.Fields(line)
	if len(f) < 4 || !strings.HasPrefix(f[0], "/") {
		return // banner, header or a wrapped device line
	}
	used, err1 := strconv.ParseUint(strings.TrimSuffix(f[1], "*"), 10, 64)
	soft, err2 := strconv.ParseUint(f[2], 10, 64)
	hard, err3 := strconv.ParseUint(f[3], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || used > maxKiB || soft > maxKiB || hard > maxKiB {
		return
	}
	limit := soft
	if limit == 0 {
		limit = hard
	}
	if limit == 0 {
		return // no quota on this filesystem
	}
	r.Quota = &Disk{Mount: QuotaMount, Size: limit * 1024, Used: min(used, limit) * 1024}
}

func bytesValue(v float64) (uint64, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > math.MaxInt64 {
		return 0, false
	}
	return uint64(v), true
}

// "cpu  user nice system idle iowait irq softirq steal guest guest_nice"
func (r *Raw) parseStat(line string) error {
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return nil // per-core lines and other counters
	}
	var vals []uint64
	for _, s := range f[1:] {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return fmt.Errorf("bad cpu field %q", clip(s))
		}
		vals = append(vals, v)
	}
	// guest and guest_nice are already counted in user and nice.
	if len(vals) > 8 {
		vals = vals[:8]
	}
	var total uint64
	for _, v := range vals {
		total += v
	}
	idle := vals[3]
	if len(vals) > 4 {
		idle += vals[4] // iowait
	}
	r.CPUTotal, r.CPUIdle = total, idle
	return nil
}

func (r *Raw) parseMem(line string) {
	f := strings.Fields(line)
	if len(f) < 2 {
		return
	}
	v, err := strconv.ParseUint(f[1], 10, 64)
	if err != nil || v > maxKiB {
		return
	}
	switch f[0] {
	case "MemTotal:":
		r.MemTotal = v * 1024
	case "MemAvailable:":
		r.MemAvailable = v * 1024
	}
}

func (r *Raw) parseLoad(line string) error {
	f := strings.Fields(line)
	if len(f) < 1 {
		return nil
	}
	v, err := strconv.ParseFloat(f[0], 64)
	// ParseFloat accepts "NaN" and "Inf", which json.Marshal then refuses:
	// one hostile value would break the live stream for everyone.
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > maxLoad {
		return fmt.Errorf("bad load %q", clip(f[0]))
	}
	r.Load1 = v
	return nil
}

// ExcludedInterface reports interfaces that do not carry the machine's
// real traffic (loopback, container bridges and veths).
func ExcludedInterface(name string) bool {
	return name == "lo" || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "veth") ||
		strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "virbr") || strings.HasPrefix(name, "cni") ||
		strings.HasPrefix(name, "flannel") || strings.HasPrefix(name, "tailscale")
}

// "  eth0: rxbytes rxpackets ... (8 rx fields) txbytes ..."
func (r *Raw) parseNet(line string) {
	name, rest, ok := strings.Cut(line, ":")
	if !ok {
		return // header lines
	}
	name = strings.TrimSpace(name)
	f := strings.Fields(rest)
	if len(f) < 9 || ExcludedInterface(name) {
		return
	}
	rx, err1 := strconv.ParseUint(f[0], 10, 64)
	tx, err2 := strconv.ParseUint(f[8], 10, 64)
	if err1 != nil || err2 != nil {
		return
	}
	r.Net[name] = NetCounters{Rx: rx, Tx: tx}
}

// "Filesystem 1024-blocks Used Available Capacity Mounted on"
func (r *Raw) parseDF(line string, diskCap int) {
	f := strings.Fields(line)
	if len(f) < 6 || f[0] == "Filesystem" || len(r.Disks) >= diskCap {
		return
	}
	size, err1 := strconv.ParseUint(f[1], 10, 64)
	used, err2 := strconv.ParseUint(f[2], 10, 64)
	if err1 != nil || err2 != nil || size == 0 || size > maxKiB || used > size {
		return
	}
	mount := strings.Join(f[5:], " ")
	if !validMount(mount) {
		return
	}
	for _, d := range r.Disks {
		if d.Mount == mount {
			return
		}
	}
	r.Disks = append(r.Disks, Disk{Mount: mount, Size: size * 1024, Used: used * 1024})
}

func validMount(m string) bool {
	if m == "" || len(m) > maxMountLen || !utf8.ValidString(m) {
		return false
	}
	for _, r := range m {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// clip bounds host-supplied text quoted in errors and logs.
func clip(s string) string {
	if len(s) <= maxQuotedSpan {
		return s
	}
	return s[:maxQuotedSpan] + "…"
}
