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

// Host metrics come straight from /proc and statfs rather than from a
// dependency: everything here is a handful of lines, and dagent is already
// Linux-only by virtue of nftables, tap devices and cgroups.
//
// CPU utilisation is a rate, so it needs two samples. cpuSampler keeps the
// previous one; the first call after start has nothing to compare against and
// reports zero, which is the honest answer rather than a fabricated one.
type cpuSampler struct {
  prevBusy, prevTotal uint64
}

// collect reads one snapshot. Any field that cannot be read stays zero rather
// than failing the whole frame -- a host with an unusual /proc should still
// report its memory.
func (s *cpuSampler) collect(dataDir string) proto.Metrics {
  m := proto.Metrics{CPUCount: runtime.NumCPU()}
  m.CPUPercent = s.cpuPercent()
  m.Load1, m.Load5, m.Load15 = loadAvg()
  m.MemTotalBytes, m.MemUsedBytes = memInfo()
  m.DiskTotalBytes, m.DiskUsedBytes = diskUsage(dataDir)
  m.UptimeSeconds = uptime()
  return m
}

// cpuPercent is aggregate busy time over the interval since the previous call,
// as a percentage of one wall-clock interval across all cpus.
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

  // user nice system idle iowait irq softirq steal ...
  var total, idle uint64
  for i, v := range fields[1:] {
    n, err := strconv.ParseUint(v, 10, 64)
    if err != nil {
      continue
    }
    total += n
    if i == 3 || i == 4 { // idle and iowait are both "not doing work"
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

// memInfo reports used as total minus MemAvailable. MemFree would count the
// page cache as used and make every busy host look like it is out of memory.
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
    fields := strings.Fields(value) // "  16316372 kB"
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

// diskUsage reports the filesystem holding the data directory: images and
// per-VM overlays are what actually fill a host up.
func diskUsage(dir string) (total, used int64) {
  var st syscall.Statfs_t
  if err := syscall.Statfs(dir, &st); err != nil {
    return 0, 0
  }
  bsize := int64(st.Bsize)
  total = int64(st.Blocks) * bsize
  // Blocks - Bfree is what is really consumed; Bavail excludes the reserve, so
  // using it here would report a full disk as slightly over-full.
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
