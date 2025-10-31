package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"sync/atomic"
	"time"
)

// Config represents the relay configuration
type Config struct {
	ListenPort    int      `json:"listen_port"`
	Targets       []Target `json:"targets"`
	BufferSize    int      `json:"buffer_size"`
	LogLevel      string   `json:"log_level"`
	StatsInterval int      `json:"stats_interval"`
}

// Target represents a forwarding destination
type Target struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	Name string `json:"name"`
}

// Stats tracks relay statistics
type Stats struct {
	PacketsReceived  int64
	PacketsForwarded int64
	PacketsDropped   int64
	TotalLatencyNs   int64
	PacketCount      int64
}

var (
	stats     = &Stats{}
	startTime = time.Now()
	logLevel  = "info"
)

func main() {
	configPath := flag.String("config", "relay_config.json", "Path to configuration file")
	flag.Parse()

	config, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("[ERROR] Failed to load config: %v", err)
	}

	logLevel = config.LogLevel
	if config.BufferSize == 0 {
		config.BufferSize = 4096
	}
	if config.StatsInterval == 0 {
		config.StatsInterval = 10
	}

	logInfo("iFacialMocap UDP Relay starting...")
	logInfo(fmt.Sprintf("Listening on :%d", config.ListenPort))

	// Resolve target addresses
	targetAddrs := make([]*net.UDPAddr, 0, len(config.Targets))
	for _, target := range config.Targets {
		addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", target.Host, target.Port))
		if err != nil {
			logError(fmt.Sprintf("Failed to resolve target %s (%s:%d): %v", target.Name, target.Host, target.Port, err))
			continue
		}
		targetAddrs = append(targetAddrs, addr)
		logInfo(fmt.Sprintf("Forwarding to %s:%d (%s)", target.Host, target.Port, target.Name))
	}

	if len(targetAddrs) == 0 {
		log.Fatalf("[ERROR] No valid targets configured")
	}

	// Create UDP listener
	listenAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", config.ListenPort))
	if err != nil {
		log.Fatalf("[ERROR] Failed to resolve listen address: %v", err)
	}

	conn, err := net.ListenUDP("udp", listenAddr)
	if err != nil {
		log.Fatalf("[ERROR] Failed to listen on port %d: %v", config.ListenPort, err)
	}
	defer conn.Close()

	// Set socket options for better performance and compatibility
	// Set buffer sizes to reduce packet loss
	conn.SetReadBuffer(65536) // 64KB read buffer
	conn.SetWriteBuffer(65536) // 64KB write buffer
	if logLevel == "debug" {
		logDebug("UDP socket buffers set to 64KB")
	}

	logInfo("Relay started successfully")

	// Start stats reporting goroutine
	go reportStats(config.StatsInterval)

	// Preallocate buffer
	buffer := make([]byte, config.BufferSize)

	// Main relay loop
	for {
		n, srcAddr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			logError(fmt.Sprintf("Read error: %v", err))
			continue
		}

		// Track received packet
		atomic.AddInt64(&stats.PacketsReceived, 1)

		if logLevel == "debug" {
			logDebug(fmt.Sprintf("Received %d bytes from %s", n, srcAddr))
			// Log first few bytes for debugging
			if n > 0 && n < 100 {
				logDebug(fmt.Sprintf("Packet preview: %s", string(buffer[:min(n, 50)])))
			}
		}

		// Forward packet to all targets
		start := time.Now()
		packetData := buffer[:n]
		successCount := forwardPacket(conn, packetData, targetAddrs)

		// Track latency
		latency := time.Since(start).Nanoseconds()
		atomic.AddInt64(&stats.TotalLatencyNs, latency)
		atomic.AddInt64(&stats.PacketCount, 1)

		if successCount == len(targetAddrs) {
			atomic.AddInt64(&stats.PacketsForwarded, 1)
		} else {
			dropped := int64(len(targetAddrs) - successCount)
			atomic.AddInt64(&stats.PacketsDropped, dropped)
			if logLevel == "debug" || logLevel == "info" {
				logInfo(fmt.Sprintf("Partially forwarded packet: %d/%d targets", successCount, len(targetAddrs)))
			}
		}
	}
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	err = json.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

func forwardPacket(conn *net.UDPConn, data []byte, targets []*net.UDPAddr) int {
	successCount := 0
	// Forward to all targets in parallel to minimize latency
	for _, targetAddr := range targets {
		// Write immediately without copying data (Go's WriteToUDP handles this efficiently)
		n, err := conn.WriteToUDP(data, targetAddr)
		if err != nil {
			logError(fmt.Sprintf("Failed to forward to %s: %v", targetAddr, err))
		} else if n != len(data) {
			logError(fmt.Sprintf("Partial write to %s: %d/%d bytes", targetAddr, n, len(data)))
		} else {
			successCount++
		}
	}
	return successCount
}

func reportStats(interval int) {
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		received := atomic.LoadInt64(&stats.PacketsReceived)
		forwarded := atomic.LoadInt64(&stats.PacketsForwarded)
		dropped := atomic.LoadInt64(&stats.PacketsDropped)
		totalLatency := atomic.LoadInt64(&stats.TotalLatencyNs)
		packetCount := atomic.LoadInt64(&stats.PacketCount)

		var avgLatency float64
		if packetCount > 0 {
			avgLatency = float64(totalLatency) / float64(packetCount) / 1000000.0 // Convert to milliseconds
		}

		uptime := time.Since(startTime).Round(time.Second)
		logInfo(fmt.Sprintf("[STATS] Uptime: %s | Received: %d | Forwarded: %d | Dropped: %d | Avg Latency: %.3f ms",
			uptime, received, forwarded, dropped, avgLatency))
	}
}

func logInfo(msg string) {
	if logLevel == "debug" || logLevel == "info" {
		log.Printf("[INFO] %s", msg)
	}
}

func logDebug(msg string) {
	if logLevel == "debug" {
		log.Printf("[DEBUG] %s", msg)
	}
}

func logError(msg string) {
	log.Printf("[ERROR] %s", msg)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
