// SERVER CODE — runs on the recording server.
package main

import (
	"context"
	"encoding/json"
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

// Global camera status store (thread-safe)
var (
	statusMu     sync.RWMutex
	cameraStatus = make(map[string]CameraStatus)
)

type CameraStatus struct {
	State    string    `json:"state"` // online, offline, heartbeat
	LastSeen time.Time `json:"last_seen"`
	FPS      int       `json:"fps,omitempty"`
	Bitrate  int       `json:"bitrate,omitempty"`
}

func updateCameraStatus(cameraID string, data interface{}) {
	b, _ := json.Marshal(data)
	var sd mq.StatusData
	if err := json.Unmarshal(b, &sd); err != nil {
		return
	}
	statusMu.Lock()
	defer statusMu.Unlock()
	cs := cameraStatus[cameraID]
	cs.State = sd.Status
	cs.LastSeen = time.Now()
	cs.FPS = sd.FPS
	cs.Bitrate = sd.Bitrate
	cameraStatus[cameraID] = cs
	fmt.Printf("[STATUS] Camera %s → %s (FPS=%d, bitrate=%d)\n", cameraID, sd.Status, sd.FPS, sd.Bitrate)
}

func getCameraStatus(cameraID string) (CameraStatus, bool) {
	statusMu.RLock()
	defer statusMu.RUnlock()
	cs, ok := cameraStatus[cameraID]
	return cs, ok
}

func getAllStatus() map[string]CameraStatus {
	statusMu.RLock()
	defer statusMu.RUnlock()
	out := make(map[string]CameraStatus, len(cameraStatus))
	for k, v := range cameraStatus {
		out[k] = v
	}
	return out
}

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

	// ── Connect to RabbitMQ ───────────────────────────────────────────────────
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

		go func() {
			_ = mqClient.ConsumeWithContext(context.Background(), "camera.*.status", func(body []byte) {
				var msg mq.Message
				if err := json.Unmarshal(body, &msg); err != nil {
					return
				}
				updateCameraStatus(msg.CameraID, msg.Data)
			})
		}()

		go func() {
			_ = mqClient.ConsumeWithContext(context.Background(), "camera.*.chunk", func(body []byte) {
				var msg mq.Message
				if err := json.Unmarshal(body, &msg); err != nil {
					return
				}
				fmt.Printf("[CHUNK] Camera %s: %+v\n", msg.CameraID, msg.Data)
			})
		}()
	}

	// ── Context / WaitGroup ───────────────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	getCameraStatusGeneric := func(cameraID string) (interface{}, bool) {
		return getCameraStatus(cameraID)
	}
	getAllStatusGeneric := func() map[string]interface{} {
		out := make(map[string]interface{})
		for k, v := range getAllStatus() {
			out[k] = v
		}
		return out
	}

	// HTTP API
	go api.Start(cfg, cameras, mqClient, getCameraStatusGeneric, getAllStatusGeneric)

	// ── One receiver goroutine per camera ────────────────────────────────────
	var wg sync.WaitGroup
	for _, cam := range cameras {
		wg.Add(1)
		go func(c config.CameraConfig) {
			defer wg.Done()
			// Each restart will create a new timestamped folder
			receiver.RunWithRestart(ctx, cfg, c, 60*time.Second, mqClient)
		}(cam)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("\n[SERVER] Received shutdown signal — stopping all receivers...")
	cancel()
	wg.Wait()
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




// // SERVER CODE — runs on the recording server.
// package main

// import (
// 	"context"
// 	"encoding/json"
// 	"flag"
// 	"fmt"
// 	"os"
// 	"os/signal"
// 	"sync"
// 	"syscall"
// 	"time"

// 	"camera-system/internal/api"
// 	"camera-system/internal/config"
// 	"camera-system/internal/mq"
// 	"camera-system/internal/receiver"
// )

// // Global camera status store (thread-safe)
// var (
// 	statusMu     sync.RWMutex
// 	cameraStatus = make(map[string]CameraStatus)
// )

// type CameraStatus struct {
// 	State    string    `json:"state"` // online, offline, heartbeat
// 	LastSeen time.Time `json:"last_seen"`
// 	FPS      int       `json:"fps,omitempty"`
// 	Bitrate  int       `json:"bitrate,omitempty"`
// }

// func updateCameraStatus(cameraID string, data interface{}) {
// 	b, _ := json.Marshal(data)
// 	var sd mq.StatusData
// 	if err := json.Unmarshal(b, &sd); err != nil {
// 		return
// 	}
// 	statusMu.Lock()
// 	defer statusMu.Unlock()
// 	cs := cameraStatus[cameraID]
// 	cs.State = sd.Status
// 	cs.LastSeen = time.Now()
// 	cs.FPS = sd.FPS
// 	cs.Bitrate = sd.Bitrate
// 	cameraStatus[cameraID] = cs
// 	fmt.Printf("[STATUS] Camera %s → %s (FPS=%d, bitrate=%d)\n", cameraID, sd.Status, sd.FPS, sd.Bitrate)
// }

// func getCameraStatus(cameraID string) (CameraStatus, bool) {
// 	statusMu.RLock()
// 	defer statusMu.RUnlock()
// 	cs, ok := cameraStatus[cameraID]
// 	return cs, ok
// }

// func getAllStatus() map[string]CameraStatus {
// 	statusMu.RLock()
// 	defer statusMu.RUnlock()
// 	out := make(map[string]CameraStatus, len(cameraStatus))
// 	for k, v := range cameraStatus {
// 		out[k] = v
// 	}
// 	return out
// }

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

// 	// ── Connect to RabbitMQ ───────────────────────────────────────────────────
// 	var mqClient *mq.Client
// 	if cfg.RabbitMQ.Enabled {
// 		mqClient, err = mq.NewClient(
// 			cfg.RabbitMQ.Host,
// 			cfg.RabbitMQ.Port,
// 			cfg.RabbitMQ.User,
// 			cfg.RabbitMQ.Password,
// 			cfg.RabbitMQ.Exchange,
// 		)
// 		if err != nil {
// 			fmt.Fprintf(os.Stderr, "ERROR: cannot connect to RabbitMQ: %v\n", err)
// 			os.Exit(1)
// 		}
// 		defer mqClient.Close()
// 		fmt.Println("[SERVER] Connected to RabbitMQ")

// 		go func() {
// 			_ = mqClient.ConsumeWithContext(context.Background(), "camera.*.status", func(body []byte) {
// 				var msg mq.Message
// 				if err := json.Unmarshal(body, &msg); err != nil {
// 					return
// 				}
// 				updateCameraStatus(msg.CameraID, msg.Data)
// 			})
// 		}()

// 		go func() {
// 			_ = mqClient.ConsumeWithContext(context.Background(), "camera.*.chunk", func(body []byte) {
// 				var msg mq.Message
// 				if err := json.Unmarshal(body, &msg); err != nil {
// 					return
// 				}
// 				fmt.Printf("[CHUNK] Camera %s: %+v\n", msg.CameraID, msg.Data)
// 			})
// 		}()
// 	}

// 	// ── Context / WaitGroup ───────────────────────────────────────────────────
// 	ctx, cancel := context.WithCancel(context.Background())
// 	defer cancel()

// 	getCameraStatusGeneric := func(cameraID string) (interface{}, bool) {
// 		return getCameraStatus(cameraID)
// 	}
// 	getAllStatusGeneric := func() map[string]interface{} {
// 		out := make(map[string]interface{})
// 		for k, v := range getAllStatus() {
// 			out[k] = v
// 		}
// 		return out
// 	}

// 	// HTTP API
// 	go api.Start(cfg, cameras, mqClient, getCameraStatusGeneric, getAllStatusGeneric)

// 	// ── One receiver goroutine per camera ────────────────────────────────────
// 	// NOTE: receiver.RunWithRestart now generates its own session timestamp on
// 	// every (re)start, so each run gets a fresh folder automatically. The
// 	// server main no longer needs to set cfg.SessionTimestamp at all.
// 	var wg sync.WaitGroup
// 	for _, cam := range cameras {
// 		wg.Add(1)
// 		go func(c config.CameraConfig) {
// 			defer wg.Done()
// 			receiver.RunWithRestart(ctx, cfg, c, 60*time.Second, mqClient)
// 		}(cam)
// 	}

// 	quit := make(chan os.Signal, 1)
// 	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
// 	<-quit

// 	fmt.Println("\n[SERVER] Received shutdown signal — stopping all receivers...")
// 	cancel()
// 	wg.Wait()
// 	fmt.Println("[SERVER] All receivers stopped. Exiting.")
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
// 	if cfg.RabbitMQ.Enabled {
// 		fmt.Printf("║  RabbitMQ  : %s:%d\n", cfg.RabbitMQ.Host, cfg.RabbitMQ.Port)
// 	} else {
// 		fmt.Println("║  RabbitMQ  : disabled")
// 	}
// 	fmt.Println("╠══════════════════════════════════════════════════════╣")
// 	for _, cam := range cameras {
// 		fmt.Printf("║  %-10s  %-20s  SRT port %d\n",
// 			cam.ID, cam.Name, cfg.SRTPort(cam))
// 	}
// 	fmt.Println("╚══════════════════════════════════════════════════════╝")
// 	fmt.Println()
// }



