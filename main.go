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
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Name   string `json:"name"`
	Format string `json:"format,omitempty"` // "raw" or "vsf", defaults to "raw"
}

// TargetInfo holds resolved address and format for a target
type TargetInfo struct {
	Addr   *net.UDPAddr
	Format string
	Name   string
}

// iFacialMocapMessage represents the incoming JSON structure from iFacialMocap
type iFacialMocapMessage struct {
	BlendShapes map[string]float64 `json:"blendShapes"`
	Head        *struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
		Z float64 `json:"z"`
	} `json:"head,omitempty"`
	Rotation *struct {
		X float64 `json:"x"` // pitch
		Y float64 `json:"y"` // yaw
		Z float64 `json:"z"` // roll
	} `json:"rotation,omitempty"`
}

// VSFMessage represents the VSF (Virtual Stream Format) JSON structure
type VSFMessage struct {
	Type        string             `json:"type"`
	Time        float64            `json:"time"`
	Blendshapes map[string]float64 `json:"blendshapes"`
	Head        VSFHead            `json:"head"`
}

// VSFHead represents head tracking data in VSF format
type VSFHead struct {
	Yaw   float64 `json:"yaw"`
	Pitch float64 `json:"pitch"`
	Roll  float64 `json:"roll"`
	X     float64 `json:"x,omitempty"`
	Y     float64 `json:"y,omitempty"`
	Z     float64 `json:"z,omitempty"`
}

// Stats tracks relay statistics
type Stats struct {
	PacketsReceived  int64
	PacketsForwarded int64
	PacketsDropped   int64
	VSFConverted     int64
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

	// Resolve target addresses with format info
	targetInfos := make([]TargetInfo, 0, len(config.Targets))
	for _, target := range config.Targets {
		addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", target.Host, target.Port))
		if err != nil {
			logError(fmt.Sprintf("Failed to resolve target %s (%s:%d): %v", target.Name, target.Host, target.Port, err))
			continue
		}

		format := target.Format
		if format == "" {
			format = "raw" // Default to raw forwarding
		}

		if format != "raw" && format != "vsf" {
			logError(fmt.Sprintf("Invalid format '%s' for target %s, defaulting to 'raw'", format, target.Name))
			format = "raw"
		}

		targetInfos = append(targetInfos, TargetInfo{
			Addr:   addr,
			Format: format,
			Name:   target.Name,
		})

		formatInfo := ""
		if format == "vsf" {
			formatInfo = " (VSF format)"
		}
		logInfo(fmt.Sprintf("Forwarding to %s:%d (%s)%s", target.Host, target.Port, target.Name, formatInfo))
	}

	if len(targetInfos) == 0 {
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
		}

		// Forward packet to all targets
		start := time.Now()
		packetData := buffer[:n]
		successCount := forwardPacket(conn, packetData, targetInfos)

		// Track latency
		latency := time.Since(start).Nanoseconds()
		atomic.AddInt64(&stats.TotalLatencyNs, latency)
		atomic.AddInt64(&stats.PacketCount, 1)

		if successCount == len(targetInfos) {
			atomic.AddInt64(&stats.PacketsForwarded, 1)
		} else {
			dropped := int64(len(targetInfos) - successCount)
			atomic.AddInt64(&stats.PacketsDropped, dropped)
			if logLevel == "debug" || logLevel == "info" {
				logInfo(fmt.Sprintf("Partially forwarded packet: %d/%d targets", successCount, len(targetInfos)))
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

// forwardPacket forwards data to all targets, converting to VSF format when needed
func forwardPacket(conn *net.UDPConn, data []byte, targets []TargetInfo) int {
	successCount := 0
	vsfData := make([]byte, 0) // Cache for VSF conversion

	for _, target := range targets {
		var sendData []byte
		var err error

		if target.Format == "vsf" {
			// Convert to VSF format
			vsfBytes, vsfErr := convertToVSF(data)
			if vsfErr != nil {
				logError(fmt.Sprintf("Failed to convert to VSF for %s: %v", target.Name, vsfErr))
				continue
			}
			sendData = vsfBytes
			atomic.AddInt64(&stats.VSFConverted, 1)
		} else {
			// Forward raw packet
			sendData = data
		}

		_, err = conn.WriteToUDP(sendData, target.Addr)
		if err != nil {
			logError(fmt.Sprintf("Failed to forward to %s (%s): %v", target.Name, target.Format, err))
		} else {
			successCount++
		}
	}

	// Reuse vsfData buffer (keep for potential future reuse)
	_ = vsfData

	return successCount
}

// convertToVSF converts iFacialMocap JSON to VSF JSON format
func convertToVSF(data []byte) ([]byte, error) {
	// Parse iFacialMocap message
	var ifmMsg iFacialMocapMessage
	err := json.Unmarshal(data, &ifmMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to parse iFacialMocap JSON: %w", err)
	}

	// Create VSF message
	vsfMsg := VSFMessage{
		Type:        "vsf.blendshape",
		Time:        float64(time.Now().UnixNano()) / 1e9, // Unix timestamp in seconds
		Blendshapes: ifmMsg.BlendShapes,
		Head: VSFHead{},
	}

	// Map rotation fields (iFacialMocap -> VSF)
	if ifmMsg.Rotation != nil {
		vsfMsg.Head.Yaw = ifmMsg.Rotation.Y   // rotation.y -> head.yaw
		vsfMsg.Head.Pitch = ifmMsg.Rotation.X // rotation.x -> head.pitch
		vsfMsg.Head.Roll = ifmMsg.Rotation.Z  // rotation.z -> head.roll
	}

	// Map head position if present
	if ifmMsg.Head != nil {
		vsfMsg.Head.X = ifmMsg.Head.X
		vsfMsg.Head.Y = ifmMsg.Head.Y
		vsfMsg.Head.Z = ifmMsg.Head.Z
	}

	// Marshal to JSON
	vsfJSON, err := json.Marshal(vsfMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal VSF JSON: %w", err)
	}

	return vsfJSON, nil
}

func reportStats(interval int) {
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		received := atomic.LoadInt64(&stats.PacketsReceived)
		forwarded := atomic.LoadInt64(&stats.PacketsForwarded)
		dropped := atomic.LoadInt64(&stats.PacketsDropped)
		vsfConverted := atomic.LoadInt64(&stats.VSFConverted)
		totalLatency := atomic.LoadInt64(&stats.TotalLatencyNs)
		packetCount := atomic.LoadInt64(&stats.PacketCount)

		var avgLatency float64
		if packetCount > 0 {
			avgLatency = float64(totalLatency) / float64(packetCount) / 1000000.0 // Convert to milliseconds
		}

		uptime := time.Since(startTime).Round(time.Second)
		logInfo(fmt.Sprintf("[STATS] Uptime: %s | Received: %d | Forwarded: %d | Dropped: %d | VSF Converted: %d | Avg Latency: %.3f ms",
			uptime, received, forwarded, dropped, vsfConverted, avgLatency))
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
