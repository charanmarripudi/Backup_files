package main

import (
    "context"
    "encoding/json"
    "flag"
    "fmt"
    "log"
    "os"
    "os/signal"
    "sync"
    "syscall"
    "time"

    "camera-system/internal/config"
    "camera-system/internal/mq"
    "camera-system/internal/sender"
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
        fmt.Println("[LOCAL] Connected to RabbitMQ")
    }

    // Context to cancel all senders (global shutdown)
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    var wg sync.WaitGroup

    // Start a sender for each camera
    for _, cam := range cameras {
        // Create a restart channel for this camera (buffered to avoid blocking)
        restartChan := make(chan struct{}, 1)

        wg.Add(1)
        go func(c config.CameraConfig, restartCh chan struct{}) {
            defer wg.Done()
            sender.RunWithRestart(ctx, cfg, c, 60*time.Second, mqClient, restartCh)
        }(cam, restartChan)

        // Subscribe to commands for this camera if MQ enabled
        if mqClient != nil {
            routingKey := "camera." + cam.ID + ".command"
            go func(id string, restartCh chan struct{}) {
                err := mqClient.ConsumeWithContext(ctx, routingKey, func(body []byte) {
                    var cmd mq.Command
                    if err := json.Unmarshal(body, &cmd); err != nil {
                        log.Printf("[%s] Invalid command: %v", id, err)
                        return
                    }
                    switch cmd.Command {
                    case "restart":
                        fmt.Printf("[%s] Received restart command – restarting sender\n", id)
                        select {
                        case restartCh <- struct{}{}:
                        default:
                        }
                    }
                })
                if err != nil {
                    fmt.Fprintf(os.Stderr, "[%s] Failed to subscribe to commands: %v\n", id, err)
                }
            }(cam.ID, restartChan)
        }
    }

    // Wait for interrupt
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit

    fmt.Println("\n[LOCAL] Received shutdown signal — stopping all senders...")
    cancel()
    wg.Wait()
    fmt.Println("[LOCAL] All senders stopped. Exiting.")
}

func printBanner(cfg *config.Config, cameras []config.CameraConfig) {
    fmt.Println("╔══════════════════════════════════════════════════════╗")
    fmt.Println("║          DELTA LOCAL SENDER — CAMERA SITE            ║")
    fmt.Println("╠══════════════════════════════════════════════════════╣")
    fmt.Printf("║  Server    : %s\n", cfg.Server.Host)
    fmt.Printf("║  Encoder   : %s\n", cfg.Server.Encoder)
    fmt.Printf("║  Cameras   : %d enabled\n", len(cameras))
    if cfg.RabbitMQ.Enabled {
        fmt.Printf("║  RabbitMQ  : %s:%d\n", cfg.RabbitMQ.Host, cfg.RabbitMQ.Port)
    } else {
        fmt.Println("║  RabbitMQ  : disabled")
    }
    fmt.Println("╠══════════════════════════════════════════════════════╣")
    for _, cam := range cameras {
        fmt.Printf("║  %-10s  %-20s  → SRT port %d\n",
            cam.ID, cam.Name, cfg.SRTPort(cam))
    }
    fmt.Println("╚══════════════════════════════════════════════════════╝")
    fmt.Println()
}





// // LOCAL CODE — runs on the machine at the camera site.
// package main

// import (
// 	"flag"
// 	"fmt"
// 	"os"
// 	"os/signal"
// 	"sync"
// 	"syscall"
// 	"time"

// 	"camera-system/internal/config"
// 	"camera-system/internal/sender"
// )

// func main() {
// 	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
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

// 	var wg sync.WaitGroup
// 	for _, cam := range cameras {
// 		wg.Add(1)
// 		go func(c config.CameraConfig) {
// 			defer wg.Done()
// 			sender.RunWithRestart(cfg, c, 60*time.Second)
// 		}(cam)
// 	}

// 	quit := make(chan os.Signal, 1)
// 	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
// 	<-quit

// 	fmt.Println("\n[LOCAL] Received shutdown signal — stopping all senders...")
// 	os.Exit(0)
// }

// func printBanner(cfg *config.Config, cameras []config.CameraConfig) {
// 	fmt.Println("╔══════════════════════════════════════════════════════╗")
// 	fmt.Println("║          DELTA LOCAL SENDER — CAMERA SITE            ║")
// 	fmt.Println("╠══════════════════════════════════════════════════════╣")
// 	fmt.Printf("║  Server    : %s\n", cfg.Server.Host)
// 	fmt.Printf("║  Encoder   : %s\n", cfg.Server.Encoder)
// 	fmt.Printf("║  Cameras   : %d enabled\n", len(cameras))
// 	fmt.Println("╠══════════════════════════════════════════════════════╣")
// 	for _, cam := range cameras {
// 		fmt.Printf("║  %-10s  %-20s  → SRT port %d\n",
// 			cam.ID, cam.Name, cfg.SRTPort(cam))
// 	}
// 	fmt.Println("╚══════════════════════════════════════════════════════╝")
// 	fmt.Println()
// }








// // LOCAL CODE — runs on the machine at the camera site.
// //
// // This program reads config.yaml, finds all enabled cameras, and starts
// // one GStreamer sender goroutine per camera. Each sender reads from the
// // camera's RTSP URL, compresses the video (H264 or H265), and streams it
// // over SRT to the recording server.
// //
// // If a camera sender crashes or disconnects, it automatically restarts
// // with exponential backoff (5s → 10s → 20s ... up to 60s).
// //
// // Usage:
// //
// //	./delta_local --config config.yaml
// //
// // Build:
// //
// //	go build -o delta_local ./cmd/local
// package main

// import (
// 	"flag"
// 	"fmt"
// 	"os"
// 	"os/signal"
// 	"sync"
// 	"syscall"
// 	"time"

// 	"camera-system/internal/config"
// 	"camera-system/internal/sender"
// )

// func main() {
// 	configPath := flag.String("config", "config.yaml", "Path to config.yaml")
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

// 	var wg sync.WaitGroup
// 	for _, cam := range cameras {
// 		wg.Add(1)
// 		go func(c config.CameraConfig) {
// 			defer wg.Done()
// 			sender.RunWithRestart(cfg, c, 60*time.Second)
// 		}(cam)
// 	}

// 	// Wait for Ctrl+C
// 	quit := make(chan os.Signal, 1)
// 	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
// 	<-quit

// 	fmt.Println("\n[LOCAL] Received shutdown signal — stopping all senders...")
// 	os.Exit(0)
// }

// func printBanner(cfg *config.Config, cameras []config.CameraConfig) {
// 	fmt.Println("╔══════════════════════════════════════════════════════╗")
// 	fmt.Println("║          DELTA LOCAL SENDER — CAMERA SITE            ║")
// 	fmt.Println("╠══════════════════════════════════════════════════════╣")
// 	fmt.Printf("║  Server    : %s\n", cfg.Server.Host)
// 	fmt.Printf("║  Encoder   : %s\n", cfg.Server.Encoder)
// 	fmt.Printf("║  Cameras   : %d enabled\n", len(cameras))
// 	fmt.Println("╠══════════════════════════════════════════════════════╣")
// 	for _, cam := range cameras {
// 		fmt.Printf("║  %-10s  %-20s  → SRT port %d\n",
// 			cam.ID, cam.Name, cfg.SRTPort(cam))
// 	}
// 	fmt.Println("╚══════════════════════════════════════════════════════╝")
// 	fmt.Println()
// }
