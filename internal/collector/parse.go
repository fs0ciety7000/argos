// Package collector polls hosts over SSH, turns /proc and df output into
// samples, stores them and publishes them live.
package collector

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
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
}

// NetCounters are cumulative interface byte counters.
type NetCounters struct{ Rx, Tx uint64 }

// Disk is one mounted filesystem, sizes in bytes.
type Disk struct {
	Mount string `json:"mount"`
	Size  uint64 `json:"size"`
	Used  uint64 `json:"used"`
}

const maxDisks = 64

// Parse reads the output of sshpool.CmdMetrics*.
func Parse(out []byte) (Raw, error) {
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
			r.parseDF(line)
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
	return r, nil
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
			return fmt.Errorf("bad cpu field %q", s)
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
	if err != nil {
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
	if err != nil {
		return fmt.Errorf("bad load %q", f[0])
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
func (r *Raw) parseDF(line string) {
	f := strings.Fields(line)
	if len(f) < 6 || f[0] == "Filesystem" || len(r.Disks) >= maxDisks {
		return
	}
	size, err1 := strconv.ParseUint(f[1], 10, 64)
	used, err2 := strconv.ParseUint(f[2], 10, 64)
	if err1 != nil || err2 != nil || size == 0 {
		return
	}
	mount := strings.Join(f[5:], " ")
	for _, d := range r.Disks {
		if d.Mount == mount {
			return
		}
	}
	r.Disks = append(r.Disks, Disk{Mount: mount, Size: size * 1024, Used: used * 1024})
}
