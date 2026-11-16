package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"camera-system/internal/analyzer"
	"camera-system/internal/config"
	"camera-system/internal/mq"
	"camera-system/internal/receiver"
)

// getLatestSessionDir returns the most recent timestamped subdirectory for a camera.
func getLatestSessionDir(cam config.CameraConfig, cfg *config.Config) string {
	base := filepath.Join(cfg.Server.OutputDir, cam.ID)
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	var sessions []string
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) == 15 { // format: 20060102_150405
			sessions = append(sessions, e.Name())
		}
	}
	if len(sessions) == 0 {
		return ""
	}
	sort.Strings(sessions) // lexicographic = chronological
	return filepath.Join(base, sessions[len(sessions)-1])
}

// Start now receives optional status functions
func Start(cfg *config.Config, cameras []config.CameraConfig, mqClient *mq.Client,
	getCameraStatus func(string) (interface{}, bool),
	getAllStatus func() map[string]interface{}) {

	mux := http.NewServeMux()
	registerRoutes(mux, cfg, cameras, mqClient, getCameraStatus, getAllStatus)

	addr := fmt.Sprintf(":%d", cfg.Server.HTTPPort)
	fmt.Printf("\n[API] HTTP server listening on %s\n", addr)
	fmt.Println("[API] Available endpoints:")
	fmt.Println("[API]   GET /health")
	fmt.Println("[API]   GET /cameras")
	fmt.Println("[API]   GET /api/status/all")
	fmt.Println("[API]   GET /api/camera/{id}/status")
	fmt.Println("[API]   GET /api/{camera_id}/chunks")
	fmt.Println("[API]   GET /api/{camera_id}/chunk/{n}")
	fmt.Println("[API]   GET /api/{camera_id}/keyframes/{n}")
	fmt.Println("[API]   GET /api/{camera_id}/frame/{chunk}/{frame}")
	fmt.Println("[API]   GET /api/{camera_id}/session")
	fmt.Println("[API]   GET /list")
	fmt.Println("[API]   POST /api/camera/{camera_id}/restart")
	fmt.Println()

	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Printf("[API] Server error: %v\n", err)
	}
}

func registerRoutes(mux *http.ServeMux, cfg *config.Config, cameras []config.CameraConfig,
	mqClient *mq.Client,
	getCameraStatus func(string) (interface{}, bool),
	getAllStatus func() map[string]interface{}) {

	camMap := make(map[string]config.CameraConfig, len(cameras))
	for _, cam := range cameras {
		camMap[cam.ID] = cam
	}

	// Health
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "OK")
	})

	// List cameras (shows latest session directory)
	mux.HandleFunc("/cameras", func(w http.ResponseWriter, r *http.Request) {
		type camInfo struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			SRTPort int    `json:"srt_port"`
			OutDir  string `json:"output_dir"`
		}
		var result []camInfo
		for _, cam := range cameras {
			outDir := getLatestSessionDir(cam, cfg)
			if outDir == "" {
				outDir = filepath.Join(cfg.Server.OutputDir, cam.ID) // fallback
			}
			result = append(result, camInfo{
				ID:      cam.ID,
				Name:    cam.Name,
				SRTPort: cfg.SRTPort(cam),
				OutDir:  outDir,
			})
		}
		jsonResponse(w, map[string]interface{}{
			"total":   len(result),
			"cameras": result,
		})
	})

	// GET /api/status/all – all camera statuses
	mux.HandleFunc("/api/status/all", func(w http.ResponseWriter, r *http.Request) {
		if getAllStatus == nil {
			http.Error(w, "status not available", http.StatusServiceUnavailable)
			return
		}
		jsonResponse(w, getAllStatus())
	})

	// GET /api/camera/{id}/status
	mux.HandleFunc("/api/camera/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/status") {
			parts := strings.Split(r.URL.Path, "/")
			if len(parts) >= 4 && parts[3] == "status" {
				cameraID := parts[2]
				if getCameraStatus == nil {
					http.Error(w, "status not available", http.StatusServiceUnavailable)
					return
				}
				status, ok := getCameraStatus(cameraID)
				if !ok {
					http.Error(w, "camera not found or no status yet", http.StatusNotFound)
					return
				}
				jsonResponse(w, status)
				return
			}
		}

		// POST restart
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/restart") {
			parts := strings.Split(r.URL.Path, "/")
			if len(parts) >= 5 && parts[4] == "restart" {
				cameraID := parts[3]
				if _, ok := camMap[cameraID]; !ok {
					http.Error(w, fmt.Sprintf("camera %q not found", cameraID), http.StatusNotFound)
					return
				}
				if mqClient == nil {
					http.Error(w, "RabbitMQ not enabled", http.StatusServiceUnavailable)
					return
				}
				err := mqClient.PublishCommand(cameraID, "restart", nil)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
				fmt.Fprintf(w, "Restart command sent to camera %s", cameraID)
				return
			}
		}

		// Fall through to /api/{camera_id}/{action}
		trimmed := strings.TrimPrefix(r.URL.Path, "/api/")
		parts := strings.SplitN(trimmed, "/", 4)
		if len(parts) < 2 {
			http.Error(w, "Usage: /api/{camera_id}/{action}", http.StatusBadRequest)
			return
		}

		cameraID := parts[0]
		action := parts[1]

		cam, ok := camMap[cameraID]
		if !ok {
			http.Error(w, fmt.Sprintf("camera %q not found", cameraID), http.StatusNotFound)
			return
		}

		outDir := getLatestSessionDir(cam, cfg)
		if outDir == "" {
			http.Error(w, "no recording session found for this camera", http.StatusNotFound)
			return
		}

		switch action {
		case "chunks":
			chunks := receiver.GetChunks(outDir)
			if chunks == nil {
				listChunksFromDisk(w, cam.ID, outDir)
				return
			}
			jsonResponse(w, map[string]interface{}{
				"camera_id": cameraID,
				"total":     len(chunks),
				"chunks":    chunks,
			})
		case "session":
			sessionFile := filepath.Join(outDir, "session_summary.json")
			data, err := os.ReadFile(sessionFile)
			if err != nil {
				listChunksFromDisk(w, cam.ID, outDir)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(data)
		case "chunk":
			if len(parts) < 3 {
				http.Error(w, "Usage: /api/{camera_id}/chunk/{n}", http.StatusBadRequest)
				return
			}
			n, err := strconv.Atoi(parts[2])
			if err != nil {
				http.Error(w, "invalid chunk number", http.StatusBadRequest)
				return
			}
			chunks := receiver.GetChunks(outDir)
			for _, c := range chunks {
				if c.ChunkNumber == n {
					jsonResponse(w, c)
					return
				}
			}
			chunkFile := filepath.Join(outDir, fmt.Sprintf("chunk_%05d.mp4", n-1))
			info, err := os.Stat(chunkFile)
			if err != nil {
				http.Error(w, fmt.Sprintf("chunk %d not found", n), http.StatusNotFound)
				return
			}
			jsonResponse(w, map[string]interface{}{
				"camera_id":    cameraID,
				"chunk_number": n,
				"file":         chunkFile,
				"size_mb":      float64(info.Size()) / (1024 * 1024),
				"mod_time":     info.ModTime().Format(time.RFC3339),
				"analyzed":     false,
			})
		case "keyframes":
			if len(parts) < 3 {
				http.Error(w, "Usage: /api/{camera_id}/keyframes/{n}", http.StatusBadRequest)
				return
			}
			n, err := strconv.Atoi(parts[2])
			if err != nil {
				http.Error(w, "invalid chunk number", http.StatusBadRequest)
				return
			}
			index := receiver.GetChunkIndex(outDir, n)
			if index == nil {
				index = loadChunkIndex(outDir, n)
			}
			if index == nil {
				http.Error(w, fmt.Sprintf("index for chunk %d not found", n), http.StatusNotFound)
				return
			}
			jsonResponse(w, map[string]interface{}{
				"camera_id":      cameraID,
				"chunk_number":   n,
				"keyframe_count": len(index.Keyframes),
				"total_frames":   index.TotalFrames,
				"keyframes":      index.Keyframes,
			})
		case "frame":
			if len(parts) < 4 {
				http.Error(w, "Usage: /api/{camera_id}/frame/{chunk}/{frame}", http.StatusBadRequest)
				return
			}
			chunkNum, err := strconv.Atoi(parts[2])
			if err != nil {
				http.Error(w, "invalid chunk number", http.StatusBadRequest)
				return
			}
			frameNum, err := strconv.Atoi(parts[3])
			if err != nil {
				http.Error(w, "invalid frame number", http.StatusBadRequest)
				return
			}
			index := receiver.GetChunkIndex(outDir, chunkNum)
			if index == nil {
				index = loadChunkIndex(outDir, chunkNum)
			}
			if index == nil {
				http.Error(w, fmt.Sprintf("index for chunk %d not found", chunkNum), http.StatusNotFound)
				return
			}
			var targetFrame *analyzer.FrameIndexEntry
			for i := range index.Frames {
				if index.Frames[i].FrameNumber == frameNum {
					targetFrame = &index.Frames[i]
					break
				}
			}
			if targetFrame == nil {
				http.Error(w, fmt.Sprintf("frame %d not found in chunk %d", frameNum, chunkNum), http.StatusNotFound)
				return
			}
			nearestKeyframe := 0
			for _, kf := range index.Keyframes {
				if kf <= frameNum {
					nearestKeyframe = kf
				} else {
					break
				}
			}
			videoFile := filepath.Join(outDir, fmt.Sprintf("chunk_%05d.mp4", chunkNum-1))
			jsonResponse(w, map[string]interface{}{
				"camera_id":    cameraID,
				"chunk_number": chunkNum,
				"frame":        targetFrame,
				"seek_info": map[string]interface{}{
					"file":             videoFile,
					"nearest_keyframe": nearestKeyframe,
					"frames_to_skip":   frameNum - nearestKeyframe,
					"file_exists":      fileExists(videoFile),
				},
			})
		default:
			http.Error(w, fmt.Sprintf("unknown action %q. Available: chunks, chunk, keyframes, frame, session", action), http.StatusBadRequest)
		}
	})
}

func listChunksFromDisk(w http.ResponseWriter, cameraID, outDir string) {
	type fileInfo struct {
		Name    string  `json:"name"`
		SizeMB  float64 `json:"size_mb"`
		ModTime string  `json:"mod_time"`
	}
	files, _ := filepath.Glob(filepath.Join(outDir, "chunk_*.mp4"))
	var result []fileInfo
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		result = append(result, fileInfo{
			Name:    filepath.Base(f),
			SizeMB:  float64(info.Size()) / (1024 * 1024),
			ModTime: info.ModTime().Format(time.RFC3339),
		})
	}
	jsonResponse(w, map[string]interface{}{
		"camera_id": cameraID,
		"total":     len(result),
		"chunks":    result,
		"note":      "analysis not yet available, listing disk files",
	})
}

func loadChunkIndex(outDir string, chunkNum int) *analyzer.ChunkIndex {
	path := filepath.Join(outDir, fmt.Sprintf("chunk_%03d_index.json", chunkNum))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var index analyzer.ChunkIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil
	}
	return &index
}

func jsonResponse(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}


// package api

// import (
//     "encoding/json"
//     "fmt"
//     "net/http"
//     "os"
//     "path/filepath"
//     "strconv"
//     "strings"
//     "time"

//     "camera-system/internal/analyzer"
//     "camera-system/internal/config"
//     "camera-system/internal/mq"
//     "camera-system/internal/receiver"
// )

// // Start now receives optional status functions
// func Start(cfg *config.Config, cameras []config.CameraConfig, mqClient *mq.Client,
//     getCameraStatus func(string) (interface{}, bool),
//     getAllStatus func() map[string]interface{}) {

//     mux := http.NewServeMux()
//     registerRoutes(mux, cfg, cameras, mqClient, getCameraStatus, getAllStatus)

//     addr := fmt.Sprintf(":%d", cfg.Server.HTTPPort)
//     fmt.Printf("\n[API] HTTP server listening on %s\n", addr)
//     fmt.Println("[API] Available endpoints:")
//     fmt.Println("[API]   GET /health")
//     fmt.Println("[API]   GET /cameras")
//     fmt.Println("[API]   GET /api/status/all")
//     fmt.Println("[API]   GET /api/camera/{id}/status")
//     fmt.Println("[API]   GET /api/{camera_id}/chunks")
//     fmt.Println("[API]   GET /api/{camera_id}/chunk/{n}")
//     fmt.Println("[API]   GET /api/{camera_id}/keyframes/{n}")
//     fmt.Println("[API]   GET /api/{camera_id}/frame/{chunk}/{frame}")
//     fmt.Println("[API]   GET /api/{camera_id}/session")
//     fmt.Println("[API]   GET /list")
//     fmt.Println("[API]   POST /api/camera/{camera_id}/restart")
//     fmt.Println()

//     if err := http.ListenAndServe(addr, mux); err != nil {
//         fmt.Printf("[API] Server error: %v\n", err)
//     }
// }

// func registerRoutes(mux *http.ServeMux, cfg *config.Config, cameras []config.CameraConfig,
//     mqClient *mq.Client,
//     getCameraStatus func(string) (interface{}, bool),
//     getAllStatus func() map[string]interface{}) {

//     camMap := make(map[string]config.CameraConfig, len(cameras))
//     for _, cam := range cameras {
//         camMap[cam.ID] = cam
//     }

//     // Health
//     mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
//         w.WriteHeader(http.StatusOK)
//         fmt.Fprint(w, "OK")
//     })

//     // List cameras
//     mux.HandleFunc("/cameras", func(w http.ResponseWriter, r *http.Request) {
//         type camInfo struct {
//             ID      string `json:"id"`
//             Name    string `json:"name"`
//             SRTPort int    `json:"srt_port"`
//             OutDir  string `json:"output_dir"`
//         }
//         var result []camInfo
//         for _, cam := range cameras {
//             result = append(result, camInfo{
//                 ID:      cam.ID,
//                 Name:    cam.Name,
//                 SRTPort: cfg.SRTPort(cam),
//                 OutDir:  cfg.CameraOutputDir(cam),
//             })
//         }
//         jsonResponse(w, map[string]interface{}{
//             "total":   len(result),
//             "cameras": result,
//         })
//     })

//     // GET /api/status/all – all camera statuses
//     mux.HandleFunc("/api/status/all", func(w http.ResponseWriter, r *http.Request) {
//         if getAllStatus == nil {
//             http.Error(w, "status not available", http.StatusServiceUnavailable)
//             return
//         }
//         jsonResponse(w, getAllStatus())
//     })

//     // GET /api/camera/{id}/status
//     mux.HandleFunc("/api/camera/", func(w http.ResponseWriter, r *http.Request) {
//         // We'll handle both GET status and POST restart in this catch-all, but we need to differentiate.
//         // Use method and path length.
//         if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/status") {
//             parts := strings.Split(r.URL.Path, "/")
//             if len(parts) >= 4 && parts[3] == "status" {
//                 cameraID := parts[2]
//                 if getCameraStatus == nil {
//                     http.Error(w, "status not available", http.StatusServiceUnavailable)
//                     return
//                 }
//                 status, ok := getCameraStatus(cameraID)
//                 if !ok {
//                     http.Error(w, "camera not found or no status yet", http.StatusNotFound)
//                     return
//                 }
//                 jsonResponse(w, status)
//                 return
//             }
//         }

//         // POST restart
//         if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/restart") {
//             parts := strings.Split(r.URL.Path, "/")
//             if len(parts) >= 5 && parts[4] == "restart" {
//                 cameraID := parts[3]
//                 if _, ok := camMap[cameraID]; !ok {
//                     http.Error(w, fmt.Sprintf("camera %q not found", cameraID), http.StatusNotFound)
//                     return
//                 }
//                 if mqClient == nil {
//                     http.Error(w, "RabbitMQ not enabled", http.StatusServiceUnavailable)
//                     return
//                 }
//                 err := mqClient.PublishCommand(cameraID, "restart", nil)
//                 if err != nil {
//                     http.Error(w, err.Error(), http.StatusInternalServerError)
//                     return
//                 }
//                 w.WriteHeader(http.StatusOK)
//                 fmt.Fprintf(w, "Restart command sent to camera %s", cameraID)
//                 return
//             }
//         }

//         // If not handled, fall through to the original /api/... data endpoints
//         // (we'll keep the original logic from your existing api.go)
//         trimmed := strings.TrimPrefix(r.URL.Path, "/api/")
//         parts := strings.SplitN(trimmed, "/", 4)
//         if len(parts) < 2 {
//             http.Error(w, "Usage: /api/{camera_id}/{action}", http.StatusBadRequest)
//             return
//         }

//         cameraID := parts[0]
//         action := parts[1]

//         cam, ok := camMap[cameraID]
//         if !ok {
//             http.Error(w, fmt.Sprintf("camera %q not found", cameraID), http.StatusNotFound)
//             return
//         }

//         outDir := cfg.CameraOutputDir(cam)

//         switch action {
//         case "chunks":
//             chunks := receiver.GetChunks(outDir)
//             if chunks == nil {
//                 listChunksFromDisk(w, cam.ID, outDir)
//                 return
//             }
//             jsonResponse(w, map[string]interface{}{
//                 "camera_id": cameraID,
//                 "total":     len(chunks),
//                 "chunks":    chunks,
//             })
//         case "session":
//             sessionFile := filepath.Join(outDir, "session_summary.json")
//             data, err := os.ReadFile(sessionFile)
//             if err != nil {
//                 listChunksFromDisk(w, cam.ID, outDir)
//                 return
//             }
//             w.Header().Set("Content-Type", "application/json")
//             w.Write(data)
//         case "chunk":
//             if len(parts) < 3 {
//                 http.Error(w, "Usage: /api/{camera_id}/chunk/{n}", http.StatusBadRequest)
//                 return
//             }
//             n, err := strconv.Atoi(parts[2])
//             if err != nil {
//                 http.Error(w, "invalid chunk number", http.StatusBadRequest)
//                 return
//             }
//             chunks := receiver.GetChunks(outDir)
//             for _, c := range chunks {
//                 if c.ChunkNumber == n {
//                     jsonResponse(w, c)
//                     return
//                 }
//             }
//             chunkFile := filepath.Join(outDir, fmt.Sprintf("chunk_%05d.mp4", n-1))
//             info, err := os.Stat(chunkFile)
//             if err != nil {
//                 http.Error(w, fmt.Sprintf("chunk %d not found", n), http.StatusNotFound)
//                 return
//             }
//             jsonResponse(w, map[string]interface{}{
//                 "camera_id":    cameraID,
//                 "chunk_number": n,
//                 "file":         chunkFile,
//                 "size_mb":      float64(info.Size()) / (1024 * 1024),
//                 "mod_time":     info.ModTime().Format(time.RFC3339),
//                 "analyzed":     false,
//             })
//         case "keyframes":
//             if len(parts) < 3 {
//                 http.Error(w, "Usage: /api/{camera_id}/keyframes/{n}", http.StatusBadRequest)
//                 return
//             }
//             n, err := strconv.Atoi(parts[2])
//             if err != nil {
//                 http.Error(w, "invalid chunk number", http.StatusBadRequest)
//                 return
//             }
//             index := receiver.GetChunkIndex(outDir, n)
//             if index == nil {
//                 index = loadChunkIndex(outDir, n)
//             }
//             if index == nil {
//                 http.Error(w, fmt.Sprintf("index for chunk %d not found", n), http.StatusNotFound)
//                 return
//             }
//             jsonResponse(w, map[string]interface{}{
//                 "camera_id":      cameraID,
//                 "chunk_number":   n,
//                 "keyframe_count": len(index.Keyframes),
//                 "total_frames":   index.TotalFrames,
//                 "keyframes":      index.Keyframes,
//             })
//         case "frame":
//             if len(parts) < 4 {
//                 http.Error(w, "Usage: /api/{camera_id}/frame/{chunk}/{frame}", http.StatusBadRequest)
//                 return
//             }
//             chunkNum, err := strconv.Atoi(parts[2])
//             if err != nil {
//                 http.Error(w, "invalid chunk number", http.StatusBadRequest)
//                 return
//             }
//             frameNum, err := strconv.Atoi(parts[3])
//             if err != nil {
//                 http.Error(w, "invalid frame number", http.StatusBadRequest)
//                 return
//             }
//             index := receiver.GetChunkIndex(outDir, chunkNum)
//             if index == nil {
//                 index = loadChunkIndex(outDir, chunkNum)
//             }
//             if index == nil {
//                 http.Error(w, fmt.Sprintf("index for chunk %d not found", chunkNum), http.StatusNotFound)
//                 return
//             }
//             var targetFrame *analyzer.FrameIndexEntry
//             for i := range index.Frames {
//                 if index.Frames[i].FrameNumber == frameNum {
//                     targetFrame = &index.Frames[i]
//                     break
//                 }
//             }
//             if targetFrame == nil {
//                 http.Error(w, fmt.Sprintf("frame %d not found in chunk %d", frameNum, chunkNum), http.StatusNotFound)
//                 return
//             }
//             nearestKeyframe := 0
//             for _, kf := range index.Keyframes {
//                 if kf <= frameNum {
//                     nearestKeyframe = kf
//                 } else {
//                     break
//                 }
//             }
//             videoFile := filepath.Join(outDir, fmt.Sprintf("chunk_%05d.mp4", chunkNum-1))
//             jsonResponse(w, map[string]interface{}{
//                 "camera_id":    cameraID,
//                 "chunk_number": chunkNum,
//                 "frame":        targetFrame,
//                 "seek_info": map[string]interface{}{
//                     "file":             videoFile,
//                     "nearest_keyframe": nearestKeyframe,
//                     "frames_to_skip":   frameNum - nearestKeyframe,
//                     "file_exists":      fileExists(videoFile),
//                 },
//             })
//         default:
//             http.Error(w, fmt.Sprintf("unknown action %q. Available: chunks, chunk, keyframes, frame, session", action), http.StatusBadRequest)
//         }
//     })
// }

// func listChunksFromDisk(w http.ResponseWriter, cameraID, outDir string) {
//     type fileInfo struct {
//         Name    string  `json:"name"`
//         SizeMB  float64 `json:"size_mb"`
//         ModTime string  `json:"mod_time"`
//     }
//     files, _ := filepath.Glob(filepath.Join(outDir, "chunk_*.mp4"))
//     var result []fileInfo
//     for _, f := range files {
//         info, err := os.Stat(f)
//         if err != nil {
//             continue
//         }
//         result = append(result, fileInfo{
//             Name:    filepath.Base(f),
//             SizeMB:  float64(info.Size()) / (1024 * 1024),
//             ModTime: info.ModTime().Format(time.RFC3339),
//         })
//     }
//     jsonResponse(w, map[string]interface{}{
//         "camera_id": cameraID,
//         "total":     len(result),
//         "chunks":    result,
//         "note":      "analysis not yet available, listing disk files",
//     })
// }

// func loadChunkIndex(outDir string, chunkNum int) *analyzer.ChunkIndex {
//     path := filepath.Join(outDir, fmt.Sprintf("chunk_%03d_index.json", chunkNum))
//     data, err := os.ReadFile(path)
//     if err != nil {
//         return nil
//     }
//     var index analyzer.ChunkIndex
//     if err := json.Unmarshal(data, &index); err != nil {
//         return nil
//     }
//     return &index
// }

// func jsonResponse(w http.ResponseWriter, v interface{}) {
//     w.Header().Set("Content-Type", "application/json")
//     enc := json.NewEncoder(w)
//     enc.SetIndent("", "  ")
//     _ = enc.Encode(v)
// }

// func fileExists(path string) bool {
//     _, err := os.Stat(path)
//     return err == nil
// }


