package api

import (
    "encoding/json"
    "fmt"
    "net/http"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "time"

    "camera-system/internal/analyzer"
    "camera-system/internal/config"
    "camera-system/internal/mq"
    "camera-system/internal/receiver"
)

func Start(cfg *config.Config, cameras []config.CameraConfig, mqClient *mq.Client) {
    mux := http.NewServeMux()
    registerRoutes(mux, cfg, cameras, mqClient)

    addr := fmt.Sprintf(":%d", cfg.Server.HTTPPort)

    fmt.Printf("\n[API] HTTP server listening on %s\n", addr)
    fmt.Println("[API] Available endpoints:")
    fmt.Println("[API]   GET /health")
    fmt.Println("[API]   GET /cameras")
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

func registerRoutes(mux *http.ServeMux, cfg *config.Config, cameras []config.CameraConfig, mqClient *mq.Client) {
    camMap := make(map[string]config.CameraConfig, len(cameras))
    for _, cam := range cameras {
        camMap[cam.ID] = cam
    }

    mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        fmt.Fprint(w, "OK")
    })

    mux.HandleFunc("/cameras", func(w http.ResponseWriter, r *http.Request) {
        type camInfo struct {
            ID      string `json:"id"`
            Name    string `json:"name"`
            SRTPort int    `json:"srt_port"`
            OutDir  string `json:"output_dir"`
        }
        var result []camInfo
        for _, cam := range cameras {
            result = append(result, camInfo{
                ID:      cam.ID,
                Name:    cam.Name,
                SRTPort: cfg.SRTPort(cam),
                OutDir:  cfg.CameraOutputDir(cam),
            })
        }
        jsonResponse(w, map[string]interface{}{
            "total":   len(result),
            "cameras": result,
        })
    })

    mux.HandleFunc("/list", func(w http.ResponseWriter, r *http.Request) {
        type fileInfo struct {
            CameraID string  `json:"camera_id"`
            Name     string  `json:"name"`
            Path     string  `json:"path"`
            SizeMB   float64 `json:"size_mb"`
            ModTime  string  `json:"mod_time"`
        }
        var files []fileInfo
        for _, cam := range cameras {
            outDir := cfg.CameraOutputDir(cam)
            matches, _ := filepath.Glob(filepath.Join(outDir, "*.mp4"))
            for _, f := range matches {
                info, err := os.Stat(f)
                if err != nil {
                    continue
                }
                files = append(files, fileInfo{
                    CameraID: cam.ID,
                    Name:     filepath.Base(f),
                    Path:     f,
                    SizeMB:   float64(info.Size()) / (1024 * 1024),
                    ModTime:  info.ModTime().Format(time.RFC3339),
                })
            }
        }
        jsonResponse(w, map[string]interface{}{
            "total": len(files),
            "files": files,
        })
    })

    // POST /api/camera/{id}/restart
    mux.HandleFunc("/api/camera/", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost {
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
            return
        }
        // Path: /api/camera/{camera_id}/restart
        parts := strings.Split(r.URL.Path, "/")
        if len(parts) < 5 || parts[4] != "restart" {
            http.Error(w, "invalid endpoint. Use /api/camera/{camera_id}/restart", http.StatusBadRequest)
            return
        }
        cameraID := parts[3]
        if _, ok := camMap[cameraID]; !ok {
            http.Error(w, fmt.Sprintf("camera %q not found", cameraID), http.StatusNotFound)
            return
        }
        if mqClient == nil {
            http.Error(w, "RabbitMQ not enabled", http.StatusServiceUnavailable)
            return
        }
        cmd := mq.Command{Command: "restart"}
        body, err := json.Marshal(cmd)
        if err != nil {
            http.Error(w, "internal error", http.StatusInternalServerError)
            return
        }
        routingKey := "camera." + cameraID + ".command"
        err = mqClient.Publish(routingKey, body)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        w.WriteHeader(http.StatusOK)
        fmt.Fprintf(w, "Restart command sent to camera %s", cameraID)
    })

    // All /api/... routes (for data)
    mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
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

        outDir := cfg.CameraOutputDir(cam)

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
    // unchanged
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
    // unchanged
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




// // Package api provides the HTTP API server for the recording server.
// // All camera recordings, chunk stats, frame indexes, and keyframe data
// // are queryable through this API.
// package api

// import (
// 	"encoding/json"
// 	"fmt"
// 	"net/http"
// 	"os"
// 	"path/filepath"
// 	"strconv"
// 	"strings"
// 	"time"

// 	"camera-system/internal/analyzer"
// 	"camera-system/internal/config"
// 	"camera-system/internal/receiver"
// )

// // ─────────────────────────────────────────────────────────────────────────────
// // Start the HTTP API server
// // ─────────────────────────────────────────────────────────────────────────────

// // Start registers all routes and starts listening.
// // It blocks forever (runs in its own goroutine from main).
// func Start(cfg *config.Config, cameras []config.CameraConfig) {
// 	mux := http.NewServeMux()

// 	registerRoutes(mux, cfg, cameras)

// 	addr := fmt.Sprintf(":%d", cfg.Server.HTTPPort)

// 	fmt.Printf("\n[API] HTTP server listening on %s\n", addr)
// 	fmt.Println("[API] Available endpoints:")
// 	fmt.Println("[API]   GET /health")
// 	fmt.Println("[API]   GET /cameras")
// 	fmt.Println("[API]   GET /api/{camera_id}/chunks")
// 	fmt.Println("[API]   GET /api/{camera_id}/chunk/{n}")
// 	fmt.Println("[API]   GET /api/{camera_id}/keyframes/{n}")
// 	fmt.Println("[API]   GET /api/{camera_id}/frame/{chunk}/{frame}")
// 	fmt.Println("[API]   GET /api/{camera_id}/session")
// 	fmt.Println("[API]   GET /list")
// 	fmt.Println()

// 	if err := http.ListenAndServe(addr, mux); err != nil {
// 		fmt.Printf("[API] Server error: %v\n", err)
// 	}
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // Route registration
// // ─────────────────────────────────────────────────────────────────────────────

// func registerRoutes(mux *http.ServeMux, cfg *config.Config, cameras []config.CameraConfig) {
// 	// Build a lookup map for fast camera access
// 	camMap := make(map[string]config.CameraConfig, len(cameras))
// 	for _, cam := range cameras {
// 		camMap[cam.ID] = cam
// 	}

// 	// GET /health
// 	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
// 		w.WriteHeader(http.StatusOK)
// 		fmt.Fprint(w, "OK")
// 	})

// 	// GET /cameras — list all enabled cameras and their SRT ports
// 	mux.HandleFunc("/cameras", func(w http.ResponseWriter, r *http.Request) {
// 		type camInfo struct {
// 			ID      string `json:"id"`
// 			Name    string `json:"name"`
// 			SRTPort int    `json:"srt_port"`
// 			OutDir  string `json:"output_dir"`
// 		}
// 		var result []camInfo
// 		for _, cam := range cameras {
// 			result = append(result, camInfo{
// 				ID:      cam.ID,
// 				Name:    cam.Name,
// 				SRTPort: cfg.SRTPort(cam),
// 				OutDir:  cfg.CameraOutputDir(cam),
// 			})
// 		}
// 		jsonResponse(w, map[string]interface{}{
// 			"total":   len(result),
// 			"cameras": result,
// 		})
// 	})

// 	// GET /list — list all MP4 files across all cameras
// 	mux.HandleFunc("/list", func(w http.ResponseWriter, r *http.Request) {
// 		type fileInfo struct {
// 			CameraID string  `json:"camera_id"`
// 			Name     string  `json:"name"`
// 			Path     string  `json:"path"`
// 			SizeMB   float64 `json:"size_mb"`
// 			ModTime  string  `json:"mod_time"`
// 		}
// 		var files []fileInfo
// 		for _, cam := range cameras {
// 			outDir := cfg.CameraOutputDir(cam)
// 			matches, _ := filepath.Glob(filepath.Join(outDir, "*.mp4"))
// 			for _, f := range matches {
// 				info, err := os.Stat(f)
// 				if err != nil {
// 					continue
// 				}
// 				files = append(files, fileInfo{
// 					CameraID: cam.ID,
// 					Name:     filepath.Base(f),
// 					Path:     f,
// 					SizeMB:   float64(info.Size()) / (1024 * 1024),
// 					ModTime:  info.ModTime().Format(time.RFC3339),
// 				})
// 			}
// 		}
// 		jsonResponse(w, map[string]interface{}{
// 			"total": len(files),
// 			"files": files,
// 		})
// 	})

// 	// All /api/* routes
// 	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
// 		// Parse: /api/{camera_id}/{action}[/{args}]
// 		trimmed := strings.TrimPrefix(r.URL.Path, "/api/")
// 		parts := strings.SplitN(trimmed, "/", 4)
// 		if len(parts) < 2 {
// 			http.Error(w, "Usage: /api/{camera_id}/{action}", http.StatusBadRequest)
// 			return
// 		}

// 		cameraID := parts[0]
// 		action := parts[1]

// 		cam, ok := camMap[cameraID]
// 		if !ok {
// 			http.Error(w, fmt.Sprintf("camera %q not found", cameraID), http.StatusNotFound)
// 			return
// 		}

// 		outDir := cfg.CameraOutputDir(cam)

// 		switch action {

// 		// GET /api/{camera_id}/chunks — list all analyzed chunks
// 		case "chunks":
// 			chunks := receiver.GetChunks(outDir)
// 			if chunks == nil {
// 				// Fall back to listing files on disk
// 				listChunksFromDisk(w, cam.ID, outDir)
// 				return
// 			}
// 			jsonResponse(w, map[string]interface{}{
// 				"camera_id": cameraID,
// 				"total":     len(chunks),
// 				"chunks":    chunks,
// 			})

// 		// GET /api/{camera_id}/session — full session summary JSON
// 		case "session":
// 			sessionFile := filepath.Join(outDir, "session_summary.json")
// 			data, err := os.ReadFile(sessionFile)
// 			if err != nil {
// 				listChunksFromDisk(w, cam.ID, outDir)
// 				return
// 			}
// 			w.Header().Set("Content-Type", "application/json")
// 			w.Write(data)

// 		// GET /api/{camera_id}/chunk/{n} — info about a specific chunk
// 		case "chunk":
// 			if len(parts) < 3 {
// 				http.Error(w, "Usage: /api/{camera_id}/chunk/{n}", http.StatusBadRequest)
// 				return
// 			}
// 			n, err := strconv.Atoi(parts[2])
// 			if err != nil {
// 				http.Error(w, "invalid chunk number", http.StatusBadRequest)
// 				return
// 			}
// 			// Try from analyzer state first
// 			chunks := receiver.GetChunks(outDir)
// 			for _, c := range chunks {
// 				if c.ChunkNumber == n {
// 					jsonResponse(w, c)
// 					return
// 				}
// 			}
// 			// Fall back to disk
// 			chunkFile := filepath.Join(outDir, fmt.Sprintf("chunk_%05d.mp4", n-1))
// 			info, err := os.Stat(chunkFile)
// 			if err != nil {
// 				http.Error(w, fmt.Sprintf("chunk %d not found", n), http.StatusNotFound)
// 				return
// 			}
// 			jsonResponse(w, map[string]interface{}{
// 				"camera_id":    cameraID,
// 				"chunk_number": n,
// 				"file":         chunkFile,
// 				"size_mb":      float64(info.Size()) / (1024 * 1024),
// 				"mod_time":     info.ModTime().Format(time.RFC3339),
// 				"analyzed":     false,
// 			})

// 		// GET /api/{camera_id}/keyframes/{n} — keyframe list for chunk N
// 		case "keyframes":
// 			if len(parts) < 3 {
// 				http.Error(w, "Usage: /api/{camera_id}/keyframes/{n}", http.StatusBadRequest)
// 				return
// 			}
// 			n, err := strconv.Atoi(parts[2])
// 			if err != nil {
// 				http.Error(w, "invalid chunk number", http.StatusBadRequest)
// 				return
// 			}
// 			index := receiver.GetChunkIndex(outDir, n)
// 			if index == nil {
// 				// Try reading from saved index file
// 				index = loadChunkIndex(outDir, n)
// 			}
// 			if index == nil {
// 				http.Error(w, fmt.Sprintf("index for chunk %d not found", n), http.StatusNotFound)
// 				return
// 			}
// 			jsonResponse(w, map[string]interface{}{
// 				"camera_id":      cameraID,
// 				"chunk_number":   n,
// 				"keyframe_count": len(index.Keyframes),
// 				"total_frames":   index.TotalFrames,
// 				"keyframes":      index.Keyframes,
// 			})

// 		// GET /api/{camera_id}/frame/{chunk}/{frame} — info + seek data for a frame
// 		case "frame":
// 			if len(parts) < 4 {
// 				http.Error(w, "Usage: /api/{camera_id}/frame/{chunk}/{frame}", http.StatusBadRequest)
// 				return
// 			}
// 			chunkNum, err := strconv.Atoi(parts[2])
// 			if err != nil {
// 				http.Error(w, "invalid chunk number", http.StatusBadRequest)
// 				return
// 			}
// 			frameNum, err := strconv.Atoi(parts[3])
// 			if err != nil {
// 				http.Error(w, "invalid frame number", http.StatusBadRequest)
// 				return
// 			}
// 			index := receiver.GetChunkIndex(outDir, chunkNum)
// 			if index == nil {
// 				index = loadChunkIndex(outDir, chunkNum)
// 			}
// 			if index == nil {
// 				http.Error(w, fmt.Sprintf("index for chunk %d not found", chunkNum), http.StatusNotFound)
// 				return
// 			}

// 			var targetFrame *analyzer.FrameIndexEntry
// 			for i := range index.Frames {
// 				if index.Frames[i].FrameNumber == frameNum {
// 					targetFrame = &index.Frames[i]
// 					break
// 				}
// 			}
// 			if targetFrame == nil {
// 				http.Error(w, fmt.Sprintf("frame %d not found in chunk %d", frameNum, chunkNum), http.StatusNotFound)
// 				return
// 			}

// 			nearestKeyframe := 0
// 			for _, kf := range index.Keyframes {
// 				if kf <= frameNum {
// 					nearestKeyframe = kf
// 				} else {
// 					break
// 				}
// 			}

// 			videoFile := filepath.Join(outDir, fmt.Sprintf("chunk_%05d.mp4", chunkNum-1))
// 			jsonResponse(w, map[string]interface{}{
// 				"camera_id":    cameraID,
// 				"chunk_number": chunkNum,
// 				"frame":        targetFrame,
// 				"seek_info": map[string]interface{}{
// 					"file":             videoFile,
// 					"nearest_keyframe": nearestKeyframe,
// 					"frames_to_skip":   frameNum - nearestKeyframe,
// 					"file_exists":      fileExists(videoFile),
// 				},
// 			})

// 		default:
// 			http.Error(w, fmt.Sprintf("unknown action %q. Available: chunks, chunk, keyframes, frame, session", action), http.StatusBadRequest)
// 		}
// 	})
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // Helpers
// // ─────────────────────────────────────────────────────────────────────────────

// func listChunksFromDisk(w http.ResponseWriter, cameraID, outDir string) {
// 	type fileInfo struct {
// 		Name    string  `json:"name"`
// 		SizeMB  float64 `json:"size_mb"`
// 		ModTime string  `json:"mod_time"`
// 	}
// 	files, _ := filepath.Glob(filepath.Join(outDir, "chunk_*.mp4"))
// 	var result []fileInfo
// 	for _, f := range files {
// 		info, err := os.Stat(f)
// 		if err != nil {
// 			continue
// 		}
// 		result = append(result, fileInfo{
// 			Name:    filepath.Base(f),
// 			SizeMB:  float64(info.Size()) / (1024 * 1024),
// 			ModTime: info.ModTime().Format(time.RFC3339),
// 		})
// 	}
// 	jsonResponse(w, map[string]interface{}{
// 		"camera_id": cameraID,
// 		"total":     len(result),
// 		"chunks":    result,
// 		"note":      "analysis not yet available, listing disk files",
// 	})
// }

// func loadChunkIndex(outDir string, chunkNum int) *analyzer.ChunkIndex {
// 	path := filepath.Join(outDir, fmt.Sprintf("chunk_%03d_index.json", chunkNum))
// 	data, err := os.ReadFile(path)
// 	if err != nil {
// 		return nil
// 	}
// 	var index analyzer.ChunkIndex
// 	if err := json.Unmarshal(data, &index); err != nil {
// 		return nil
// 	}
// 	return &index
// }

// func jsonResponse(w http.ResponseWriter, v interface{}) {
// 	w.Header().Set("Content-Type", "application/json")
// 	enc := json.NewEncoder(w)
// 	enc.SetIndent("", "  ")
// 	_ = enc.Encode(v)
// }

// func fileExists(path string) bool {
// 	_, err := os.Stat(path)
// 	return err == nil
// }
