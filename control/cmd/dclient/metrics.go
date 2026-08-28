package main

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"control/internal/proto"
)

type cpuSampler struct {
	prevBusy, prevTotal uint64
}

func (s *cpuSampler) collect(dataDir string) proto.Metrics {
	m := proto.Metrics{CPUCount: runtime.NumCPU()}
	m.CPUPercent = s.cpuPercent()
	m.Load1, m.Load5, m.Load15 = loadAvg()
	m.MemTotalBytes, m.MemUsedBytes = memInfo()
	m.DiskTotalBytes, m.DiskUsedBytes = diskUsage(dataDir)
	m.UptimeSeconds = uptime()
	return m
}

func (s *cpuSampler) cpuPercent() float64 {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0
	}

	var total, idle uint64
	for i, v := range fields[1:] {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			continue
		}
		total += n
		if i == 3 || i == 4 {
			idle += n
		}
	}
	busy := total - idle

	prevBusy, prevTotal := s.prevBusy, s.prevTotal
	s.prevBusy, s.prevTotal = busy, total
	if prevTotal == 0 || total <= prevTotal {
		return 0
	}
	return float64(busy-prevBusy) / float64(total-prevTotal) * 100
}

func loadAvg() (float64, float64, float64) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return 0, 0, 0
	}
	parse := func(s string) float64 {
		v, _ := strconv.ParseFloat(s, 64)
		return v
	}
	return parse(f[0]), parse(f[1]), parse(f[2])
}

func memInfo() (total, used int64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	var available int64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		if key != "MemTotal" && key != "MemAvailable" {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		if key == "MemTotal" {
			total = kb * 1024
		} else {
			available = kb * 1024
		}
	}
	if total > available {
		used = total - available
	}
	return total, used
}

func diskUsage(dir string) (total, used int64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0
	}
	bsize := int64(st.Bsize)
	total = int64(st.Blocks) * bsize
	used = int64(st.Blocks-st.Bfree) * bsize
	return total, used
}

func uptime() int64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0
	}
	return int64(v)
}
