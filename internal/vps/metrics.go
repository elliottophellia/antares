package vps

import (
	"context"
	"strconv"
	"strings"
)

// Metrics is a point-in-time snapshot of a host, read on demand.
type Metrics struct {
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`

	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Kernel   string `json:"kernel"`
	Uptime   string `json:"uptime"`

	CPUCores   int     `json:"cpu_cores"`
	CPUPercent float64 `json:"cpu_percent"` // 0..100, from load1/cores as a rough gauge
	Load1      float64 `json:"load1"`
	Load5      float64 `json:"load5"`
	Load15     float64 `json:"load15"`

	MemTotalMB int     `json:"mem_total_mb"`
	MemUsedMB  int     `json:"mem_used_mb"`
	MemPercent float64 `json:"mem_percent"`

	SwapTotalMB int     `json:"swap_total_mb"`
	SwapUsedMB  int     `json:"swap_used_mb"`
	SwapPercent float64 `json:"swap_percent"`

	DiskTotalGB float64 `json:"disk_total_gb"`
	DiskUsedGB  float64 `json:"disk_used_gb"`
	DiskPercent float64 `json:"disk_percent"`

	Processes int      `json:"processes"`
	TopProc   []string `json:"top_proc"` // "pid cpu% mem% command", a few rows
}

// metricsScript is one shell payload that prints every section behind a marker
// so a single round trip yields everything. Uses only coreutils/procfs so it
// works on any Linux VPS without extra packages.
const metricsScript = `
echo "@@HOST"; hostname 2>/dev/null
echo "@@OS"; (. /etc/os-release 2>/dev/null; echo "$PRETTY_NAME")
echo "@@KERNEL"; uname -r 2>/dev/null
echo "@@UPTIME"; uptime -p 2>/dev/null || awk '{print int($1)" s"}' /proc/uptime
echo "@@CORES"; nproc 2>/dev/null || grep -c ^processor /proc/cpuinfo
echo "@@LOAD"; cat /proc/loadavg 2>/dev/null
echo "@@MEM"; free -m 2>/dev/null | awk '/^Mem:/{print $2" "$3} /^Swap:/{print "swap "$2" "$3}'
echo "@@DISK"; df -PBG / 2>/dev/null | awk 'NR==2{print $2" "$3" "$5}'
echo "@@PROCS"; ps -e --no-headers 2>/dev/null | wc -l
echo "@@TOP"; ps -eo pid,pcpu,pmem,comm --sort=-pcpu 2>/dev/null | head -n 6
echo "@@END"
`

// Collect reads a fresh snapshot over one SSH connection. It never returns an
// error for an unreachable host — that is expressed in Metrics.Reachable/Error
// so the caller can render a card either way.
func Collect(ctx context.Context, t Target) Metrics {
	out, err := Run(ctx, t, metricsScript)
	if err != nil && strings.TrimSpace(out) == "" {
		return Metrics{Reachable: false, Error: err.Error()}
	}
	m := parseMetrics(out)
	m.Reachable = true
	return m
}

func parseMetrics(out string) Metrics {
	var m Metrics
	section := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "@@") {
			section = strings.TrimPrefix(strings.TrimSpace(line), "@@")
			continue
		}
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		switch section {
		case "HOST":
			m.Hostname = strings.TrimSpace(line)
		case "OS":
			m.OS = strings.TrimSpace(line)
		case "KERNEL":
			m.Kernel = strings.TrimSpace(line)
		case "UPTIME":
			m.Uptime = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "up "))
		case "CORES":
			m.CPUCores = atoi(line)
		case "LOAD":
			f := strings.Fields(line)
			if len(f) >= 3 {
				m.Load1, m.Load5, m.Load15 = atof(f[0]), atof(f[1]), atof(f[2])
			}
		case "MEM":
			f := strings.Fields(line)
			if len(f) == 2 { // total used
				m.MemTotalMB, m.MemUsedMB = atoi(f[0]), atoi(f[1])
			} else if len(f) == 3 && f[0] == "swap" {
				m.SwapTotalMB, m.SwapUsedMB = atoi(f[1]), atoi(f[2])
			}
		case "DISK":
			f := strings.Fields(line)
			if len(f) >= 3 {
				m.DiskTotalGB = atof(strings.TrimSuffix(f[0], "G"))
				m.DiskUsedGB = atof(strings.TrimSuffix(f[1], "G"))
				m.DiskPercent = atof(strings.TrimSuffix(f[2], "%"))
			}
		case "PROCS":
			m.Processes = atoi(line)
		case "TOP":
			// Skip the ps header row.
			if strings.Contains(line, "COMMAND") || strings.HasPrefix(strings.TrimSpace(line), "PID") {
				continue
			}
			m.TopProc = append(m.TopProc, strings.Join(strings.Fields(line), " "))
		}
	}

	// Derived percentages.
	if m.MemTotalMB > 0 {
		m.MemPercent = round1(float64(m.MemUsedMB) / float64(m.MemTotalMB) * 100)
	}
	if m.SwapTotalMB > 0 {
		m.SwapPercent = round1(float64(m.SwapUsedMB) / float64(m.SwapTotalMB) * 100)
	}
	if m.CPUCores > 0 {
		// load1 / cores is a coarse "how busy" gauge, capped at 100.
		p := m.Load1 / float64(m.CPUCores) * 100
		if p > 100 {
			p = 100
		}
		m.CPUPercent = round1(p)
	}
	return m
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
func atof(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}
func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}
