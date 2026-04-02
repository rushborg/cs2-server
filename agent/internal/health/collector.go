package health

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

type Metrics struct {
	CPU  float64 `json:"cpu_percent"`
	RAM  float64 `json:"ram_percent"`
	Disk float64 `json:"disk_percent"`
}

func Collect(diskPath string) Metrics {
	return Metrics{
		CPU:  cpuPercent(),
		RAM:  ramPercent(),
		Disk: diskPercent(diskPath),
	}
}

func cpuPercent() float64 {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return 0
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0
	}
	var total, idle float64
	for i := 1; i < len(fields); i++ {
		v, _ := strconv.ParseFloat(fields[i], 64)
		total += v
		if i == 4 {
			idle = v
		}
	}
	if total == 0 {
		return 0
	}
	return (1 - idle/total) * 100
}

func ramPercent() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	var total, available float64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		val, _ := strconv.ParseFloat(fields[1], 64)
		switch fields[0] {
		case "MemTotal:":
			total = val
		case "MemAvailable:":
			available = val
		}
	}
	if total == 0 {
		return 0
	}
	return (1 - available/total) * 100
}

func diskPercent(path string) float64 {
	if path == "" {
		path = "/opt/rushborg"
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	if total == 0 {
		return 0
	}
	return float64(total-free) / float64(total) * 100
}
