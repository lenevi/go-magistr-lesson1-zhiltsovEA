package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	PollInterval     time.Duration
	RequestTimeout   time.Duration
	LoadThreshold    float64
	MemoryThreshold  int
	DiskThreshold    int
	NetworkThreshold int
}

type SystemMetrics struct {
	LoadAverage float64
	TotalRAM    uint64
	UsedRAM     uint64
	TotalDisk   uint64
	UsedDisk    uint64
	NetCapacity uint64
	NetUsed     uint64
}

var appConfig = Config{
	PollInterval:     getPollInterval(),
	RequestTimeout:   1500 * time.Millisecond,
	LoadThreshold:    30.0,
	MemoryThreshold:  80,
	DiskThreshold:    90,
	NetworkThreshold: 90,
}

func getPollInterval() time.Duration {
	intervalMs := 200
	if envVal := os.Getenv("POLL_INTERVAL_MS"); envVal != "" {
		if val, err := strconv.Atoi(envVal); err == nil && val > 0 {
			intervalMs = val
		}
	}
	return time.Duration(intervalMs) * time.Millisecond
}

func main() {
	httpClient := &http.Client{Timeout: appConfig.RequestTimeout}
	monitor := NewSystemMonitor(httpClient)

	fmt.Println("Starting system monitor...")
	monitor.RunContinuousCheck()
}

type Monitor struct {
	client            *http.Client
	errorCount        int
	errorNotification bool
}

func NewSystemMonitor(client *http.Client) *Monitor {
	return &Monitor{
		client: client,
	}
}

func (m *Monitor) RunContinuousCheck() {
	for {
		if err := m.PerformHealthCheck(); err != nil {
			m.handleMonitoringError(err)
		} else {
			m.resetErrorState()
		}
		time.Sleep(appConfig.PollInterval)
	}
}

func (m *Monitor) PerformHealthCheck() error {
	metrics, err := m.fetchSystemMetrics()
	if err != nil {
		return err
	}

	m.evaluateSystemHealth(metrics)
	return nil
}

func (m *Monitor) fetchSystemMetrics() (*SystemMetrics, error) {
	ctx, cancel := context.WithTimeout(context.Background(), appConfig.RequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "http://srv.msk01.gigacorp.local/_stats", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned: %s", resp.Status)
	}

	return m.parseMetricsResponse(resp)
}

func (m *Monitor) parseMetricsResponse(resp *http.Response) (*SystemMetrics, error) {
	scanner := bufio.NewScanner(resp.Body)
	if !scanner.Scan() {
		return nil, fmt.Errorf("empty response")
	}

	dataLine := strings.TrimSpace(scanner.Text())
	components := strings.Split(dataLine, ",")
	if len(components) != 7 {
		return nil, fmt.Errorf("invalid data format: expected 7 fields, got %d", len(components))
	}

	metrics := &SystemMetrics{}
	var parseErrors []string

	if val, err := strconv.ParseFloat(strings.TrimSpace(components[0]), 64); err == nil {
		metrics.LoadAverage = val
	} else {
		parseErrors = append(parseErrors, "load average")
	}

	metrics.TotalRAM, _ = strconv.ParseUint(strings.TrimSpace(components[1]), 10, 64)
	metrics.UsedRAM, _ = strconv.ParseUint(strings.TrimSpace(components[2]), 10, 64)
	metrics.TotalDisk, _ = strconv.ParseUint(strings.TrimSpace(components[3]), 10, 64)
	metrics.UsedDisk, _ = strconv.ParseUint(strings.TrimSpace(components[4]), 10, 64)
	metrics.NetCapacity, _ = strconv.ParseUint(strings.TrimSpace(components[5]), 10, 64)
	metrics.NetUsed, _ = strconv.ParseUint(strings.TrimSpace(components[6]), 10, 64)

	if len(parseErrors) > 0 {
		return nil, fmt.Errorf("parse errors: %s", strings.Join(parseErrors, ", "))
	}

	return metrics, nil
}

func (m *Monitor) evaluateSystemHealth(metrics *SystemMetrics) {
	if metrics.LoadAverage > appConfig.LoadThreshold {
		fmt.Printf("High system load detected: %s\n", formatDecimal(metrics.LoadAverage))
	}

	if metrics.TotalRAM > 0 {
		memUsagePercent := int(100 * metrics.UsedRAM / metrics.TotalRAM)
		if memUsagePercent > appConfig.MemoryThreshold {
			fmt.Printf("Memory usage exceeded threshold: %d%%\n", memUsagePercent)
		}
	}

	if metrics.TotalDisk > 0 {
		diskUsagePercent := int(100 * metrics.UsedDisk / metrics.TotalDisk)
		if diskUsagePercent > appConfig.DiskThreshold {
			freeSpaceMB := (metrics.TotalDisk - metrics.UsedDisk) / (1024 * 1024)
			fmt.Printf("Low disk space: %d MB remaining\n", freeSpaceMB)
		}
	}

	if metrics.NetCapacity > 0 {
		netUsagePercent := int(100 * metrics.NetUsed / metrics.NetCapacity)
		if netUsagePercent > appConfig.NetworkThreshold {
			availableBandwidth := (metrics.NetCapacity - metrics.NetUsed) / 1000000
			fmt.Printf("Network bandwidth limited: %d Mbps available\n", availableBandwidth)
		}
	}
}

func (m *Monitor) handleMonitoringError(err error) {
	m.errorCount++
	if m.errorCount >= 3 && !m.errorNotification {
		fmt.Println("System monitoring unavailable: cannot retrieve statistics.")
		m.errorNotification = true
	}
}

func (m *Monitor) resetErrorState() {
	m.errorCount = 0
	m.errorNotification = false
}

func formatDecimal(value float64) string {
	str := fmt.Sprintf("%.2f", value)
	str = strings.TrimRight(str, "0")
	return strings.TrimRight(str, ".")
}
