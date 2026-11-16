package main

import (
    "context"
    "flag"
    "fmt"
    "os"
    "os/signal"
    "sync"
    "syscall"
    "time"

    "camera-system/internal/api"
    "camera-system/internal/config"
    "camera-system/internal/mq"
    "camera-system/internal/receiver"
)

func main() {
    configPath := flag.String("config", "config.yaml", "Path to config.yaml")
    flag.Parse()

    cfg, err := config.Load(*configPath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
        os.Exit(1)
    }

    cameras := cfg.EnabledCameras()
    if len(cameras) == 0 {
        fmt.Println("No enabled cameras in config.yaml. Set enabled: true for at least one camera.")
        os.Exit(1)
    }

    printBanner(cfg, cameras)

    // Create output directories and register each camera in the global state
    for _, cam := range cameras {
        outDir := cfg.CameraOutputDir(cam)
        if err := os.MkdirAll(outDir, 0755); err != nil {
            fmt.Fprintf(os.Stderr, "ERROR: cannot create output dir %q: %v\n", outDir, err)
            os.Exit(1)
        }
        receiver.RegisterCamera(outDir)
        fmt.Printf("[SERVER] Registered camera %-12s → %s (SRT port %d)\n",
            cam.ID, outDir, cfg.SRTPort(cam))
    }
    fmt.Println()

    // Connect to RabbitMQ if enabled
    var mqClient *mq.Client
    if cfg.RabbitMQ.Enabled {
        mqClient, err = mq.NewClient(
            cfg.RabbitMQ.Host,
            cfg.RabbitMQ.Port,
            cfg.RabbitMQ.User,
            cfg.RabbitMQ.Password,
            cfg.RabbitMQ.Exchange,
        )
        if err != nil {
            fmt.Fprintf(os.Stderr, "ERROR: cannot connect to RabbitMQ: %v\n", err)
            os.Exit(1)
        }
        defer mqClient.Close()
        fmt.Println("[SERVER] Connected to RabbitMQ")
    }

    // Context to cancel all receivers
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Start the HTTP API in the background, passing mqClient
    go api.Start(cfg, cameras, mqClient)

    // Start one receiver per camera, each with auto-restart
    var wg sync.WaitGroup
    for _, cam := range cameras {
        wg.Add(1)
        go func(c config.CameraConfig) {
            defer wg.Done()
            receiver.RunWithRestart(ctx, cfg, c, 60*time.Second, mqClient)
        }(cam)
    }

    // Wait for Ctrl+C
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit

    fmt.Println("\n[SERVER] Received shutdown signal — stopping all receivers...")
    cancel() // Cancel all receiver contexts
    wg.Wait() // Wait for all receivers to finish
    fmt.Println("[SERVER] All receivers stopped. Exiting.")
}

func printBanner(cfg *config.Config, cameras []config.CameraConfig) {
    fmt.Println("╔══════════════════════════════════════════════════════╗")
    fmt.Println("║          DELTA SERVER — RECORDING SERVER              ║")
    fmt.Println("╠══════════════════════════════════════════════════════╣")
    fmt.Printf("║  Encoder   : %s\n", cfg.Server.Encoder)
    fmt.Printf("║  Chunk     : %ds per file\n", cfg.Server.ChunkTime)
    fmt.Printf("║  HTTP API  : :%d\n", cfg.Server.HTTPPort)
    fmt.Printf("║  Output    : %s\n", cfg.Server.OutputDir)
    fmt.Printf("║  Cameras   : %d enabled\n", len(cameras))
    if cfg.RabbitMQ.Enabled {
        fmt.Printf("║  RabbitMQ  : %s:%d\n", cfg.RabbitMQ.Host, cfg.RabbitMQ.Port)
    } else {
        fmt.Println("║  RabbitMQ  : disabled")
    }
    fmt.Println("╠══════════════════════════════════════════════════════╣")
    for _, cam := range cameras {
        fmt.Printf("║  %-10s  %-20s  SRT port %d\n",
            cam.ID, cam.Name, cfg.SRTPort(cam))
    }
    fmt.Println("╚══════════════════════════════════════════════════════╝")
    fmt.Println()
}


// // SERVER CODE — runs on the central recording server.
// //
// // This program reads config.yaml, finds all enabled cameras, and starts:
// //   - One GStreamer receiver goroutine per camera (listens for SRT stream,
// //     writes MP4 chunks to disk, triggers ffprobe analysis on each chunk).
// //   - One shared HTTP API server that exposes all recordings and analysis data.
// //
// // If a camera receiver crashes, it automatically restarts with exponential
// // backoff (5s → 10s → 20s ... up to 60s).
// //
// // Usage:
// //
// //	./delta_server --config config.yaml
// //
// // Build:
// //
// //	go build -o delta_server ./cmd/server
// package main

// import (
// 	"flag"
// 	"fmt"
// 	"os"
// 	"os/signal"
// 	"sync"
// 	"syscall"
// 	"time"

// 	"camera-system/internal/api"
// 	"camera-system/internal/config"
// 	"camera-system/internal/receiver"
// )

// func main() {
// 	configPath := flag.String("config", "config.yaml", "/Users/algofusion/Documents/dnc_backend_v2/orchestrator/Delta/Go/camera_system/config.yaml")
// 	flag.Parse()

// 	cfg, err := config.Load(*configPath)
// 	if err != nil {
// 		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
// 		os.Exit(1)
// 	}

// 	cameras := cfg.EnabledCameras()
// 	if len(cameras) == 0 {
// 		fmt.Println("No enabled cameras in config.yaml. Set enabled: true for at least one camera.")
// 		os.Exit(1)
// 	}

// 	printBanner(cfg, cameras)

// 	// Create output directories and register each camera in the global state
// 	for _, cam := range cameras {
// 		outDir := cfg.CameraOutputDir(cam)
// 		if err := os.MkdirAll(outDir, 0755); err != nil {
// 			fmt.Fprintf(os.Stderr, "ERROR: cannot create output dir %q: %v\n", outDir, err)
// 			os.Exit(1)
// 		}
// 		receiver.RegisterCamera(outDir)
// 		fmt.Printf("[SERVER] Registered camera %-12s → %s (SRT port %d)\n",
// 			cam.ID, outDir, cfg.SRTPort(cam))
// 	}
// 	fmt.Println()

// 	// Start the HTTP API in the background
// 	go api.Start(cfg, cameras)

// 	// Start one receiver per camera, each with auto-restart
// 	var wg sync.WaitGroup
// 	for _, cam := range cameras {
// 		wg.Add(1)
// 		go func(c config.CameraConfig) {
// 			defer wg.Done()
// 			receiver.RunWithRestart(cfg, c, 60*time.Second)
// 		}(cam)
// 	}

// 	// Wait for Ctrl+C
// 	quit := make(chan os.Signal, 1)
// 	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
// 	<-quit

// 	fmt.Println("\n[SERVER] Received shutdown signal — stopping all receivers...")
// 	os.Exit(0)
// }

// func printBanner(cfg *config.Config, cameras []config.CameraConfig) {
// 	fmt.Println("╔══════════════════════════════════════════════════════╗")
// 	fmt.Println("║          DELTA SERVER — RECORDING SERVER              ║")
// 	fmt.Println("╠══════════════════════════════════════════════════════╣")
// 	fmt.Printf("║  Encoder   : %s\n", cfg.Server.Encoder)
// 	fmt.Printf("║  Chunk     : %ds per file\n", cfg.Server.ChunkTime)
// 	fmt.Printf("║  HTTP API  : :%d\n", cfg.Server.HTTPPort)
// 	fmt.Printf("║  Output    : %s\n", cfg.Server.OutputDir)
// 	fmt.Printf("║  Cameras   : %d enabled\n", len(cameras))
// 	fmt.Println("╠══════════════════════════════════════════════════════╣")
// 	for _, cam := range cameras {
// 		fmt.Printf("║  %-10s  %-20s  SRT port %d\n",
// 			cam.ID, cam.Name, cfg.SRTPort(cam))
// 	}
// 	fmt.Println("╚══════════════════════════════════════════════════════╝")
// 	fmt.Println()
// }


