package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rushborg/agent/internal/commands"
	"github.com/rushborg/agent/internal/connection"
	"github.com/rushborg/agent/internal/health"
)

type Config struct {
	APIUrl      string `yaml:"api_url"`
	HostID      string `yaml:"host_id"`
	APIKey      string `yaml:"api_key"`
	DataDir     string `yaml:"data_dir"`
	DockerImage string `yaml:"docker_image"`
}

func main() {
	configPath := flag.String("config", "/etc/rushborg/agent.yaml", "Path to config file")
	flag.Parse()

	// Load config
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("[agent] failed to load config: %v", err)
	}

	if cfg.APIUrl == "" || cfg.HostID == "" || cfg.APIKey == "" {
		log.Fatal("[agent] api_url, host_id, and api_key are required in config")
	}

	if cfg.DataDir == "" {
		cfg.DataDir = "/opt/rushborg-srv"
	}
	if cfg.DockerImage == "" {
		cfg.DockerImage = "ghcr.io/rushborg/cs2-server:latest"
	}

	log.Printf("[agent] starting rush-b.org agent v1.0.0")
	log.Printf("[agent] host_id: %s", cfg.HostID)
	log.Printf("[agent] api_url: %s", cfg.APIUrl)
	log.Printf("[agent] data_dir: %s", cfg.DataDir)
	log.Printf("[agent] docker_image: %s", cfg.DockerImage)

	// Create command handler
	cmdHandler := &commands.Handler{
		DataDir:     cfg.DataDir,
		DockerImage: cfg.DockerImage,
	}

	// Create WebSocket client
	client := connection.NewClient(cfg.APIUrl, cfg.HostID, cfg.APIKey)
	client.SetHandler(cmdHandler.HandleCommand)

	// Start connection in background
	go client.Run()

	// Heartbeat ticker — sends health + container status every 30s
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		// Send immediately on start
		sendHeartbeat(client, cmdHandler, cfg.DataDir)

		for range ticker.C {
			sendHeartbeat(client, cmdHandler, cfg.DataDir)
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Printf("[agent] received %v, shutting down...", sig)

	client.Close()
	log.Printf("[agent] stopped")
}

func sendHeartbeat(client *connection.Client, cmdHandler *commands.Handler, dataDir string) {
	if !client.IsConnected() {
		return
	}

	metrics := health.Collect(dataDir)
	containers, _ := cmdHandler.HandleCommand("get_status", json.RawMessage("{}"))

	payload, _ := json.Marshal(map[string]interface{}{
		"cpu_percent":  metrics.CPU,
		"ram_percent":  metrics.RAM,
		"disk_percent": metrics.Disk,
		"version":      "1.0.0",
		"containers":   containers,
	})

	client.Send(connection.Message{
		Type:    "heartbeat",
		Payload: payload,
	})
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &cfg, nil
}
