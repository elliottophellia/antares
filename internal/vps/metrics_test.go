package vps

import "testing"

func TestParseMetrics(t *testing.T) {
	sample := `@@HOST
web-1
@@OS
Ubuntu 22.04.4 LTS
@@KERNEL
5.15.0-91-generic
@@UPTIME
up 3 days, 4 hours
@@CORES
4
@@LOAD
2.00 1.50 0.90 1/234 5678
@@MEM
7976 3210
swap 2048 128
@@DISK
80G 32G 41%
@@PROCS
187
@@TOP
  PID %CPU %MEM COMMAND
  101 22.0  3.1 node
  202 10.5  1.2 postgres
@@END
`
	m := parseMetrics(sample)
	if m.Hostname != "web-1" || m.OS != "Ubuntu 22.04.4 LTS" || m.Kernel != "5.15.0-91-generic" {
		t.Fatalf("identity wrong: %+v", m)
	}
	if m.Uptime != "3 days, 4 hours" {
		t.Errorf("uptime = %q", m.Uptime)
	}
	if m.CPUCores != 4 || m.Load1 != 2.0 || m.Load5 != 1.5 || m.Load15 != 0.9 {
		t.Errorf("cpu/load wrong: cores=%d load=%v/%v/%v", m.CPUCores, m.Load1, m.Load5, m.Load15)
	}
	if m.CPUPercent != 50 { // load1 2.0 / 4 cores = 50%
		t.Errorf("cpu%% = %v, want 50", m.CPUPercent)
	}
	if m.MemTotalMB != 7976 || m.MemUsedMB != 3210 {
		t.Errorf("mem wrong: %d/%d", m.MemUsedMB, m.MemTotalMB)
	}
	if m.SwapTotalMB != 2048 || m.SwapUsedMB != 128 {
		t.Errorf("swap wrong: %d/%d", m.SwapUsedMB, m.SwapTotalMB)
	}
	if m.DiskTotalGB != 80 || m.DiskUsedGB != 32 || m.DiskPercent != 41 {
		t.Errorf("disk wrong: %v/%v %v%%", m.DiskUsedGB, m.DiskTotalGB, m.DiskPercent)
	}
	if m.Processes != 187 {
		t.Errorf("procs = %d", m.Processes)
	}
	if len(m.TopProc) != 2 || m.TopProc[0] != "101 22.0 3.1 node" {
		t.Errorf("top procs wrong (header should be skipped): %v", m.TopProc)
	}
}
