//go:build receiver

package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type FrameDetail struct {
	FrameNumber    int     `json:"frame_number"`
	Type           string  `json:"type"`
	SizeBytes      int64   `json:"size_bytes"`
	RawSizeBytes   int64   `json:"raw_size_bytes"`
	BitrateKbps    float64 `json:"bitrate_kbps"`
	CompressionPct float64 `json:"compression_pct"`
	QP             int     `json:"qp"`
	PSNR           float64 `json:"psnr_estimate"`
	Timestamp      string  `json:"timestamp"`
	Pts            float64 `json:"pts"`
	RawSizeKB      float64 `json:"raw_size_kb"`
	EncodedSizeKB  float64 `json:"encoded_size_kb"`
	SavingsKB      float64 `json:"savings_kb"`
	SavingsPct     float64 `json:"savings_pct"`
}

type ChunkStats struct {
	ChunkNumber int     `json:"chunk_number"`
	Filename    string  `json:"filename"`
	FileSizeMB  float64 `json:"file_size_mb"`
	StartTime   string  `json:"start_time"`
	EndTime     string  `json:"end_time"`
	DurationSec float64 `json:"duration_seconds"`

	Width     int     `json:"width"`
	Height    int     `json:"height"`
	FrameRate float64 `json:"frame_rate"`
	Codec     string  `json:"codec"`
	Profile   string  `json:"profile"`
	Level     string  `json:"level"`
	BitDepth  int     `json:"bit_depth"`

	BeforeEncodingMB float64 `json:"before_encoding_mb"`
	AfterEncodingMB  float64 `json:"after_encoding_mb"`
	OverheadMB       float64 `json:"overhead_mb"`
	CompressionPct   float64 `json:"compression_pct"`
	CompressionRatio float64 `json:"compression_ratio"`

	AvgBitrateKbps  float64 `json:"avg_bitrate_kbps"`
	PeakBitrateKbps float64 `json:"peak_bitrate_kbps"`
	MinBitrateKbps  float64 `json:"min_bitrate_kbps"`

	TotalFrames int `json:"total_frames"`
	IFrames     int `json:"i_frames"`
	PFrames     int `json:"p_frames"`
	BFrames     int `json:"b_frames"`

	IFrameAvgKB    float64   `json:"i_frame_avg_kb"`
	PFrameAvgKB    float64   `json:"p_frame_avg_kb"`
	BFrameAvgKB    float64   `json:"b_frame_avg_kb"`
	AvgFrameSizeKB float64   `json:"avg_frame_size_kb"`
	RawFrameSizeKB float64   `json:"raw_frame_size_kb"`
	IFrameSizes    []float64 `json:"i_frame_sizes_kb"`
	PFrameSizes    []float64 `json:"p_frame_sizes_kb"`
	BFrameSizes    []float64 `json:"b_frame_sizes_kb"`
	IFrameRawSizes []float64 `json:"i_frame_raw_sizes_kb"`
	PFrameRawSizes []float64 `json:"p_frame_raw_sizes_kb"`
	BFrameRawSizes []float64 `json:"b_frame_raw_sizes_kb"`

	PSNR         float64 `json:"psnr"`
	SSIM         float64 `json:"ssim"`
	AvgQP        float64 `json:"avg_qp"`
	BitsPerPixel float64 `json:"bits_per_pixel"`

	RecordingStart       time.Time `json:"-"`
	RecordingEnd         time.Time `json:"-"`
	FileAvailableTime    time.Time `json:"-"`
	AnalysisStartTime    time.Time `json:"-"`
	AnalysisCompleteTime time.Time `json:"-"`

	RecordingDuration float64 `json:"recording_duration_sec"`
	FileWriteTime     float64 `json:"file_write_time_sec"`
	AnalysisWaitTime  float64 `json:"analysis_wait_time_sec"`
	AnalysisDuration  float64 `json:"analysis_duration_sec"`
	TotalChunkTime    float64 `json:"total_chunk_time_sec"`

	StartTimestamp            string `json:"start_timestamp"`
	FileReadyTimestamp        string `json:"file_ready_timestamp"`
	AnalysisCompleteTimestamp string `json:"analysis_complete_timestamp"`

	Frames         []FrameDetail `json:"frames,omitempty"`
	AnalysisTimeMs int64         `json:"analysis_time_ms"`
}

type SessionSummary struct {
	StartTime       string  `json:"session_start"`
	EndTime         string  `json:"session_end"`
	DurationMinutes float64 `json:"duration_minutes"`
	TotalChunks     int     `json:"total_chunks"`

	TotalBeforeMB     float64 `json:"total_before_mb"`
	TotalAfterMB      float64 `json:"total_after_mb"`
	AvgCompressionPct float64 `json:"avg_compression_pct"`
	AvgBitrateKbps    float64 `json:"avg_bitrate_kbps"`
	AvgPSNR           float64 `json:"avg_psnr"`
	AvgQP             float64 `json:"avg_qp"`

	TotalFrames  int `json:"total_frames"`
	TotalIFrames int `json:"total_i_frames"`
	TotalPFrames int `json:"total_p_frames"`
	TotalBFrames int `json:"total_b_frames"`

	Chunks []ChunkStats `json:"chunks"`
}

type FrameIndexEntry struct {
	FrameNumber int     `json:"frame_number"`
	FrameType   string  `json:"frame_type"`
	PTS         float64 `json:"pts"`
	Size        int     `json:"size"`
	IsKeyframe  bool    `json:"is_keyframe"`
}

type ChunkIndex struct {
	ChunkNumber int               `json:"chunk_number"`
	CameraID    string            `json:"camera_id"`
	StartTime   string            `json:"start_time"`
	EndTime     string            `json:"end_time"`
	FrameRate   float64           `json:"frame_rate"`
	TotalFrames int               `json:"total_frames"`
	Keyframes   []int             `json:"keyframes"`
	Frames      []FrameIndexEntry `json:"frames"`
}

var (
	allChunks      []ChunkStats
	chunksMutex    sync.RWMutex
	sessionStart   time.Time
	verbose        bool
	includeFrames  bool
	outputDir      string
	filename       string
	processedFiles map[string]bool
	fileMutex      sync.Mutex
	httpPort       int
	chunkTime      int
	encoderType    string

	chunkStartTimes   = make(map[int]time.Time)
	chunkStartMu      sync.Mutex
	currentChunkNum   int
	currentChunkNumMu sync.Mutex

	receivedIndices = make(map[int]*ChunkIndex)
	indicesMutex    sync.RWMutex
)

func main() {
	var port int
	var autoPort bool
	var statsFile, sessionFile string
	var help bool

	flag.IntVar(&port, "port", 9003, "SRT listen port")
	flag.StringVar(&outputDir, "output", "./recordings", "Output directory")
	flag.IntVar(&chunkTime, "chunk-time", 120, "Chunk duration seconds")
	flag.StringVar(&filename, "filename", "delta_chunk", "Filename prefix")
	flag.BoolVar(&autoPort, "auto-port", false, "Auto-find available port")
	flag.StringVar(&statsFile, "stats", "./chunk_stats.json", "Statistics JSON file")
	flag.StringVar(&sessionFile, "session", "./session_summary.json", "Session JSON file")
	flag.BoolVar(&verbose, "verbose", false, "Verbose output")
	flag.BoolVar(&includeFrames, "frame-detail", false, "Include per-frame details")
	flag.IntVar(&httpPort, "http-port", 8080, "HTTP port for file info server")
	flag.BoolVar(&help, "help", false, "Show help")
	flag.StringVar(&encoderType, "encoder", "h264", "Encoder type: h264 or h265")

	flag.Parse()

	if help {
		printHelp()
		return
	}

	if !isPortAvailable(port) {
		if autoPort {
			port = findAvailablePort(port)
		} else {
			fmt.Printf("[%s] Port %d in use. Use --auto-port\n", now(), port)
			os.Exit(1)
		}
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		fmt.Printf("[%s] Failed to create output directory: %v\n", now(), err)
		os.Exit(1)
	}

	sessionStart = time.Now()
	processedFiles = make(map[string]bool)

	printHeader()
	printConfig(port, outputDir, chunkTime, filename, httpPort, encoderType)

	go startFileServer(httpPort)

	startReceiver(port, outputDir, filename, chunkTime, statsFile, sessionFile, encoderType)
}

func isNoisyLine(line string) bool {
	noisy := []string{
		"max-size-buffers", "caps = video/x-h264", "caps = video/x-h265",
		"caps = video/x-raw", "caps = video/quicktime", "caps = video/mpegts",
		"caps = NULL", "GstProxyPad", "GstGhostPad", "GstQTMuxPad",
		"GstAggregatorPad", "codec_data=", "streamheader=",
		"Redistribute latency", "pixel-aspect-ratio", "chroma-site",
		"colorimetry", "coded-picture-structure", "bit-depth-luma",
		"bit-depth-chroma", "lcevc=",
	}
	for _, s := range noisy {
		if strings.Contains(line, s) {
			return true
		}
	}
	return false
}

func startFileServer(port int) {
	http.HandleFunc("/chunk/", func(w http.ResponseWriter, r *http.Request) {
		chunkNumStr := strings.TrimPrefix(r.URL.Path, "/chunk/")
		chunkNumStr = strings.TrimSuffix(chunkNumStr, ".mp4")
		chunkNum, err := strconv.Atoi(chunkNumStr)
		if err != nil || chunkNum < 1 {
			w.WriteHeader(400)
			return
		}

		fileIndex := chunkNum - 1
		chunkFile := filepath.Join(outputDir, fmt.Sprintf("%s_%05d.mp4", filename, fileIndex))

		info, err := os.Stat(chunkFile)
		if err != nil {
			w.WriteHeader(404)
			json.NewEncoder(w).Encode(map[string]string{"error": "not found", "file": chunkFile})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"chunk_number": chunkNum,
			"file_size_mb": float64(info.Size()) / (1024 * 1024),
			"file_exists":  true,
		})
	})

	http.HandleFunc("/api/chunk/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/chunk/")
		parts := strings.Split(path, "/")
		if len(parts) < 1 {
			http.Error(w, "Invalid path", 400)
			return
		}
		chunkNum, err := strconv.Atoi(parts[0])
		if err != nil {
			http.Error(w, "Invalid chunk number", 400)
			return
		}
		indicesMutex.RLock()
		idx, exists := receivedIndices[chunkNum]
		indicesMutex.RUnlock()
		if !exists {
			http.Error(w, "Chunk index not found", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(idx)
	})

	http.HandleFunc("/api/frame/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/frame/")
		parts := strings.Split(path, "/")
		if len(parts) < 2 {
			http.Error(w, "Invalid path. Use /api/frame/{chunk}/{frame}", 400)
			return
		}
		chunkNum, err := strconv.Atoi(parts[0])
		if err != nil {
			http.Error(w, "Invalid chunk number", 400)
			return
		}
		frameNum, err := strconv.Atoi(parts[1])
		if err != nil {
			http.Error(w, "Invalid frame number", 400)
			return
		}
		indicesMutex.RLock()
		idx, exists := receivedIndices[chunkNum]
		indicesMutex.RUnlock()
		if !exists {
			http.Error(w, "Chunk index not found", 404)
			return
		}
		var targetFrame *FrameIndexEntry
		for _, f := range idx.Frames {
			if f.FrameNumber == frameNum {
				targetFrame = &f
				break
			}
		}
		if targetFrame == nil {
			http.Error(w, "Frame not found", 404)
			return
		}
		nearestKeyframe := 0
		for _, kf := range idx.Keyframes {
			if kf <= frameNum {
				nearestKeyframe = kf
			} else {
				break
			}
		}
		fileIndex := chunkNum - 1
		videoFile := filepath.Join(outputDir, fmt.Sprintf("%s_%05d.mp4", filename, fileIndex))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"chunk_number":     chunkNum,
			"frame":            targetFrame,
			"nearest_keyframe": nearestKeyframe,
			"seek_info": map[string]interface{}{
				"file":            videoFile,
				"frame_to_decode": nearestKeyframe,
				"frames_to_skip":  frameNum - nearestKeyframe,
				"file_exists":     fileExists(videoFile),
			},
		})
	})

	http.HandleFunc("/api/keyframes/", func(w http.ResponseWriter, r *http.Request) {
		chunkNumStr := strings.TrimPrefix(r.URL.Path, "/api/keyframes/")
		chunkNum, err := strconv.Atoi(chunkNumStr)
		if err != nil {
			http.Error(w, "Invalid chunk number", 400)
			return
		}
		indicesMutex.RLock()
		idx, exists := receivedIndices[chunkNum]
		indicesMutex.RUnlock()
		if !exists {
			http.Error(w, "Chunk index not found", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"chunk_number":   chunkNum,
			"camera_id":      idx.CameraID,
			"keyframes":      idx.Keyframes,
			"total_frames":   idx.TotalFrames,
			"keyframe_count": len(idx.Keyframes),
		})
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, "OK")
	})

	http.HandleFunc("/list", func(w http.ResponseWriter, r *http.Request) {
		files, err := filepath.Glob(filepath.Join(outputDir, fmt.Sprintf("%s_*.mp4", filename)))
		if err != nil {
			w.WriteHeader(500)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		var chunks []map[string]interface{}
		for _, file := range files {
			info, _ := os.Stat(file)
			chunks = append(chunks, map[string]interface{}{
				"path":     file,
				"name":     filepath.Base(file),
				"size_mb":  float64(info.Size()) / (1024 * 1024),
				"mod_time": info.ModTime().Format("15:04:05"),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"output_dir": outputDir,
			"file_count": len(chunks),
			"chunks":     chunks,
		})
	})

	fmt.Printf("[%s] File info server on port %d\n", now(), port)
	fmt.Printf("[%s] Endpoints: /health /list /chunk/{n} /api/chunk/{n} /api/frame/{c}/{f} /api/keyframes/{n}\n", now())
	if err := http.ListenAndServe(fmt.Sprintf(":%d", port), nil); err != nil {
		fmt.Printf("[%s] HTTP server error: %v\n", now(), err)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func startReceiver(port int, outputDir, filename string, chunkTimeSec int, statsFile, sessionFile, encoder string) {
	chunkPattern := filepath.Join(outputDir, fmt.Sprintf("%s_%%05d.mp4", filename))

	var pipeline string
	if encoder == "h265" {
		pipeline = fmt.Sprintf(
			"gst-launch-1.0 -e -v "+
				"srtsrc uri=\"srt://0.0.0.0:%d?mode=listener&latency=200&timeout=10000000&transtype=live\" ! "+
				"queue max-size-buffers=200 leaky=downstream ! "+
				"tsdemux ! h265parse ! "+
				"splitmuxsink location=\"%s\" max-size-time=%d000000000 muxer=mp4mux",
			port, chunkPattern, chunkTimeSec)
	} else {
		pipeline = fmt.Sprintf(
			"gst-launch-1.0 -e -v "+
				"srtsrc uri=\"srt://0.0.0.0:%d?mode=listener&latency=200&timeout=10000000&transtype=live\" ! "+
				"queue max-size-buffers=200 leaky=downstream ! "+
				"tsdemux ! h264parse ! "+
				"splitmuxsink location=\"%s\" max-size-time=%d000000000 muxer=mp4mux",
			port, chunkPattern, chunkTimeSec)
	}

	fmt.Println("\nGSTREAMER PIPELINE:")
	fmt.Println(pipeline)
	fmt.Println()

	cmd := exec.Command("bash", "-c", pipeline)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	go monitorGStreamer(stdout)
	go monitorGStreamer(stderr)

	fmt.Printf("[%s] Listening on port %d for %s stream...\n", now(), port, strings.ToUpper(encoder))
	fmt.Printf("[%s] Saving chunks to: %s\n", now(), outputDir)

	if err := cmd.Start(); err != nil {
		fmt.Printf("[%s] Failed to start receiver: %v\n", now(), err)
		return
	}
	fmt.Printf("[%s] Receiver PID: %d\n", now(), cmd.Process.Pid)

	// Fallback scanner: only processes FINALIZED chunks (newer chunk file must exist)
	go scanForFinalizedChunks(outputDir, filename)

	go func() {
		time.Sleep(30 * time.Second)
		for {
			if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
				fmt.Printf("[%s] Receiver process died\n", now())
				return
			}
			time.Sleep(10 * time.Second)
		}
	}()

	if err := cmd.Wait(); err != nil {
		fmt.Printf("[%s] GStreamer exited: %v\n", now(), err)
	}

	saveSessionSummary(sessionFile)
	printFinalSummary()
}

func monitorGStreamer(pipe io.ReadCloser) {
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

	connected := false

	for scanner.Scan() {
		line := scanner.Text()

		isError := strings.Contains(line, "ERROR:") || strings.Contains(line, "error:")
		isWarn := strings.Contains(line, "WARNING:") || strings.Contains(line, "warning:")

		if isError {
			fmt.Printf("[%s]  GST ERROR: %s\n", now(), line)
		} else if isWarn {
			fmt.Printf("[%s]   GST WARN:  %s\n", now(), line)
		} else if verbose && !isNoisyLine(line) {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				fmt.Printf("   GST: %s\n", trimmed)
			}
		}

		if !connected && strings.Contains(line, "PLAYING") {
			connected = true
			fmt.Printf("\n[%s]  Pipeline PLAYING — waiting for sender to connect...\n", now())
		}

		// Track chunk start events from splitmuxsink
		if strings.Contains(line, "location =") && strings.Contains(line, ".mp4") {
			chunkFile := extractFilePath(line)
			if chunkFile == "" {
				continue
			}

			base := filepath.Base(chunkFile)
			base = strings.TrimSuffix(base, ".mp4")
			parts := strings.Split(base, "_")
			fileIndexStr := parts[len(parts)-1]
			fileIndex, err := strconv.Atoi(fileIndexStr)
			if err != nil {
				continue
			}
			logicalChunk := fileIndex + 1

			startTime := time.Now()
			chunkStartMu.Lock()
			chunkStartTimes[logicalChunk] = startTime
			prevChunkNum := currentChunkNum // chunk that just finished
			chunkStartMu.Unlock()

			currentChunkNumMu.Lock()
			currentChunkNum = logicalChunk
			currentChunkNumMu.Unlock()

			fmt.Printf("\n[%s]  CHUNK %d STARTED → %s\n", now(), logicalChunk, filepath.Base(chunkFile))

			// When chunk N+1 starts, chunk N is definitively closed by splitmuxsink.
			// Trigger analysis of the previous chunk now (it will wait for moov atom).
			if prevChunkNum > 0 && prevChunkNum < logicalChunk {
				prevFileIndex := prevChunkNum - 1
				prevChunkFile := filepath.Join(outputDir, fmt.Sprintf("%s_%05d.mp4", filename, prevFileIndex))

				chunkStartMu.Lock()
				prevStart, hasPrevStart := chunkStartTimes[prevChunkNum]
				chunkStartMu.Unlock()

				if hasPrevStart {
					fmt.Printf("[%s]  Chunk %d closed (chunk %d started) → queuing analysis\n",
						now(), prevChunkNum, logicalChunk)

					go func(cn int, recStart time.Time, recEnd time.Time, fPath string) {
						waitForFileReady(fPath)

						// Mark as processed so fallback scanner skips it
						fileMutex.Lock()
						processedFiles[fPath] = true
						fileMutex.Unlock()

						analyzeChunk(fPath, cn, recStart, recEnd, time.Now())
					}(prevChunkNum, prevStart, startTime, prevChunkFile)
				}
			}
		}

		// Detect chunk finalization — splitmuxsink signals when it closes a file
		lineLower := strings.ToLower(line)
		if strings.Contains(lineLower, "finalized") ||
			(strings.Contains(lineLower, "closing") && strings.Contains(line, ".mp4")) {

			currentChunkNumMu.Lock()
			cn := currentChunkNum
			currentChunkNumMu.Unlock()

			if cn == 0 {
				continue
			}

			endTime := time.Now()
			chunkStartMu.Lock()
			startTime, exists := chunkStartTimes[cn]
			chunkStartMu.Unlock()

			if !exists {
				continue
			}

			fileIndex := cn - 1
			chunkFile := filepath.Join(outputDir, fmt.Sprintf("%s_%05d.mp4", filename, fileIndex))

			fmt.Printf("[%s]  CHUNK %d FINALIZED → %s (%.1f sec)\n",
				now(), cn, filepath.Base(chunkFile), endTime.Sub(startTime).Seconds())

			go func(chunkNum int, recStart, recEnd time.Time, fPath string) {
				// Wait for file to be fully written and stable
				waitForFileReady(fPath)
				analyzeChunk(fPath, chunkNum, recStart, recEnd, time.Now())
			}(cn, startTime, endTime, chunkFile)
		}
	}
}

// waitForFileReady waits until the MP4 file is fully written (moov atom present).
// This is critical — ffprobe will fail on a partially-written MP4.
func waitForFileReady(filePath string) {
	minSize := int64(1 * 1024 * 1024) // at least 1 MB
	maxWait := 90 * time.Second
	deadline := time.Now().Add(maxWait)

	fmt.Printf("[%s]  Waiting for %s to be ready...\n", now(), filepath.Base(filePath))

	var lastSize int64 = -1
	stableCount := 0

	for time.Now().Before(deadline) {
		info, err := os.Stat(filePath)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		sz := info.Size()
		if sz < minSize {
			fmt.Printf("[%s]  File too small (%.1f KB), waiting...\n", now(), float64(sz)/1024)
			time.Sleep(2 * time.Second)
			continue
		}

		if sz == lastSize {
			stableCount++
			if stableCount >= 3 {
				// Verify the file is actually readable by ffprobe
				if verifyMP4Readable(filePath) {
					fmt.Printf("[%s]  File ready: %.2f MB\n", now(), float64(sz)/(1024*1024))
					return
				}
				// File size stable but not readable — moov atom may still be writing
				stableCount = 0
			}
		} else {
			stableCount = 0
			fmt.Printf("[%s]  File growing: %.2f MB\n", now(), float64(sz)/(1024*1024))
		}
		lastSize = sz
		time.Sleep(2 * time.Second)
	}
	fmt.Printf("[%s]  Timeout waiting for file, proceeding anyway\n", now())
}

// verifyMP4Readable does a quick ffprobe to check if the moov atom is present.
func verifyMP4Readable(filePath string) bool {
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name",
		"-of", "default=noprint_wrappers=1",
		filePath)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// scanForFinalizedChunks is a safety fallback that processes files confirmed
// to be finalized. A file is finalized when either:
//
//	(a) a newer chunk file exists (proves splitmuxsink has moved on), OR
//	(b) the file has been size-stable for ≥10s AND verifyMP4Readable passes
func scanForFinalizedChunks(outputDir, filenamePrefix string) {
	ticker := time.NewTicker(8 * time.Second)
	defer ticker.Stop()

	// track last-seen size per file for stability check
	lastSeen := make(map[string]int64)

	for range ticker.C {
		files, err := filepath.Glob(filepath.Join(outputDir, fmt.Sprintf("%s_*.mp4", filenamePrefix)))
		if err != nil || len(files) == 0 {
			continue
		}

		sort.Strings(files)

		for i, file := range files {
			fileMutex.Lock()
			if processedFiles[file] {
				fileMutex.Unlock()
				continue
			}

			info, err := os.Stat(file)
			if err != nil {
				fileMutex.Unlock()
				continue
			}

			sz := info.Size()

			// Must be at least 1 MB
			if sz < 1*1024*1024 {
				lastSeen[file] = sz
				fileMutex.Unlock()
				continue
			}

			// Determine if this file is finalized:
			// Case A: a newer chunk exists → this one is definitely closed
			newerExists := i < len(files)-1

			// Case B: size stable since last tick AND MP4 is readable
			prev, hasPrev := lastSeen[file]
			sizeStable := hasPrev && prev == sz

			lastSeen[file] = sz

			if !newerExists && !sizeStable {
				fileMutex.Unlock()
				continue
			}

			// Quick readable check before committing
			if !newerExists && sizeStable {
				// Must pass readability — moov atom must be present
				if !verifyMP4Readable(file) {
					fileMutex.Unlock()
					continue
				}
			}

			processedFiles[file] = true
			fileMutex.Unlock()

			chunkNum := extractChunkNumber(file, filenamePrefix)
			if chunkNum == 0 {
				continue
			}

			// Skip if already analyzed
			chunksMutex.RLock()
			alreadyDone := false
			for _, c := range allChunks {
				if c.ChunkNumber == chunkNum {
					alreadyDone = true
					break
				}
			}
			chunksMutex.RUnlock()

			if alreadyDone {
				continue
			}

			fmt.Printf("\n[%s]  Fallback: Processing finalized chunk %d (%.2f MB)\n",
				now(), chunkNum, float64(sz)/(1024*1024))

			capturedFile := file
			capturedChunk := chunkNum
			capturedSz := sz
			go func() {
				// Wait for full moov atom — verifyMP4Readable already passed
				// but give a moment for OS buffer flush
				if !newerExists {
					time.Sleep(1 * time.Second)
				} else {
					// newer file exists but still wait for stability
					waitForFileReady(capturedFile)
				}
				modTime := info.ModTime()
				recStart := modTime.Add(-time.Duration(chunkTime) * time.Second)
				fmt.Printf("[%s]  Fallback: Analyzing chunk %d (%.2f MB)\n",
					now(), capturedChunk, float64(capturedSz)/(1024*1024))
				analyzeChunk(capturedFile, capturedChunk, recStart, modTime, time.Now())
			}()
		}
	}
}

func extractChunkNumber(filePath, filenamePrefix string) int {
	base := filepath.Base(filePath)
	base = strings.TrimSuffix(base, ".mp4")
	base = strings.TrimPrefix(base, filenamePrefix+"_")
	fileIndex, err := strconv.Atoi(base)
	if err != nil {
		return 0
	}
	return fileIndex + 1
}

func analyzeChunk(filePath string, chunkNum int, recordStart, recordEnd, fileAvailableTime time.Time) {
	analysisStart := time.Now()

	fileMutex.Lock()
	processedFiles[filePath] = true
	fileMutex.Unlock()

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		fmt.Printf("[%s] Cannot analyze chunk %d: %v\n", now(), chunkNum, err)
		return
	}

	if fileInfo.Size() < 512*1024 {
		fmt.Printf("[%s]  Chunk %d too small (%.2f KB), skipping\n",
			now(), chunkNum, float64(fileInfo.Size())/1024)
		return
	}

	fmt.Printf("\n[%s]  ANALYZING CHUNK %d (%.2f MB)...\n",
		now(), chunkNum, float64(fileInfo.Size())/(1024*1024))

	indexFile := fmt.Sprintf("chunk_%03d_index.json", chunkNum)
	var loadedFromSender bool

	if data, err := os.ReadFile(indexFile); err == nil {
		var chunkIndex ChunkIndex
		if err := json.Unmarshal(data, &chunkIndex); err == nil {
			indicesMutex.Lock()
			receivedIndices[chunkNum] = &chunkIndex
			indicesMutex.Unlock()
			fmt.Printf("[%s]   Loaded sender frame index: %d frames, %d keyframes\n",
				now(), chunkIndex.TotalFrames, len(chunkIndex.Keyframes))
			loadedFromSender = true
		}
	}

	recordingDur := recordEnd.Sub(recordStart).Seconds()
	fileWriteTime := fileAvailableTime.Sub(recordEnd).Seconds()
	analysisWaitTime := analysisStart.Sub(fileAvailableTime).Seconds()

	stats := ChunkStats{
		ChunkNumber: chunkNum,
		Filename:    filepath.Base(filePath),
		FileSizeMB:  float64(fileInfo.Size()) / (1024 * 1024),
		StartTime:   recordStart.Format("15:04:05"),
		EndTime:     recordEnd.Format("15:04:05"),

		RecordingStart:    recordStart,
		RecordingEnd:      recordEnd,
		FileAvailableTime: fileAvailableTime,
		AnalysisStartTime: analysisStart,

		RecordingDuration: recordingDur,
		FileWriteTime:     fileWriteTime,
		AnalysisWaitTime:  analysisWaitTime,

		StartTimestamp:     recordStart.Format("15:04:05.000"),
		FileReadyTimestamp: fileAvailableTime.Format("15:04:05.000"),

		IFrameSizes:    make([]float64, 0),
		PFrameSizes:    make([]float64, 0),
		BFrameSizes:    make([]float64, 0),
		IFrameRawSizes: make([]float64, 0),
		PFrameRawSizes: make([]float64, 0),
		BFrameRawSizes: make([]float64, 0),
	}

	probeStreamInfo(&stats, filePath)
	frames := probeFrameInfo(&stats, filePath)
	calculateCompression(&stats)
	estimateQuality(&stats)

	if includeFrames {
		stats.Frames = frames
	}

	if !loadedFromSender && len(frames) > 0 {
		fmt.Printf("[%s]   Building frame index from analysis (%d frames)...\n", now(), len(frames))
		chunkIndex := &ChunkIndex{
			ChunkNumber: chunkNum,
			CameraID:    "camera1",
			StartTime:   recordStart.Format("15:04:05"),
			EndTime:     recordEnd.Format("15:04:05"),
			FrameRate:   stats.FrameRate,
			TotalFrames: stats.TotalFrames,
			Frames:      make([]FrameIndexEntry, len(frames)),
			Keyframes:   make([]int, 0),
		}
		for i, frame := range frames {
			isKeyframe := frame.Type == "I"
			chunkIndex.Frames[i] = FrameIndexEntry{
				FrameNumber: frame.FrameNumber,
				FrameType:   frame.Type,
				PTS:         frame.Pts,
				Size:        int(frame.SizeBytes),
				IsKeyframe:  isKeyframe,
			}
			if isKeyframe {
				chunkIndex.Keyframes = append(chunkIndex.Keyframes, frame.FrameNumber)
			}
		}
		if data, err := json.MarshalIndent(chunkIndex, "", "  "); err == nil {
			if err := os.WriteFile(indexFile, data, 0644); err == nil {
				fmt.Printf("[%s]   Saved frame index: %d frames, %d keyframes\n",
					now(), chunkIndex.TotalFrames, len(chunkIndex.Keyframes))
				indicesMutex.Lock()
				receivedIndices[chunkNum] = chunkIndex
				indicesMutex.Unlock()
			}
		}
	}

	analysisComplete := time.Now()
	stats.AnalysisCompleteTime = analysisComplete
	stats.AnalysisCompleteTimestamp = analysisComplete.Format("15:04:05.000")
	stats.AnalysisDuration = analysisComplete.Sub(analysisStart).Seconds()
	stats.TotalChunkTime = analysisComplete.Sub(recordStart).Seconds()
	stats.AnalysisTimeMs = time.Since(analysisStart).Milliseconds()

	chunksMutex.Lock()
	allChunks = append(allChunks, stats)
	chunksMutex.Unlock()

	printChunkReport(stats)
	printFrameDetails(&stats)
	printFrameChanges(&stats)
	saveFrameChangesReadable(&stats)
	saveFrameChangesCSV(&stats)
	saveChunkStats()
}

func probeStreamInfo(stats *ChunkStats, filePath string) {
	// Retry ffprobe a few times — the file must have a valid moov atom
	var output []byte
	var err error
	for attempt := 1; attempt <= 5; attempt++ {
		cmd := exec.Command("ffprobe",
			"-v", "quiet",
			"-select_streams", "v:0",
			"-show_entries",
			"stream=width,height,r_frame_rate,avg_frame_rate,codec_name,profile,level,bits_per_raw_sample,nb_frames:format=duration,bit_rate",
			"-of", "json",
			filePath)

		output, err = cmd.Output()
		if err == nil && len(output) > 0 {
			break
		}
		fmt.Printf("[%s]  ffprobe attempt %d failed: %v — retrying in 3s\n", now(), attempt, err)
		time.Sleep(3 * time.Second)
	}

	if err != nil {
		fmt.Printf("[%s]  ffprobe failed after retries for chunk %d: %v\n", now(), stats.ChunkNumber, err)
		// Fallback: estimate from file size
		stats.FileSizeMB = stats.FileSizeMB // already set
		if stats.DurationSec == 0 {
			stats.DurationSec = float64(chunkTime)
		}
		return
	}

	var probe struct {
		Streams []struct {
			Width            int    `json:"width"`
			Height           int    `json:"height"`
			RFrameRate       string `json:"r_frame_rate"`
			AvgFrameRate     string `json:"avg_frame_rate"`
			CodecName        string `json:"codec_name"`
			Profile          string `json:"profile"`
			Level            int    `json:"level"`
			BitsPerRawSample string `json:"bits_per_raw_sample"`
			NbFrames         string `json:"nb_frames"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
			BitRate  string `json:"bit_rate"`
		} `json:"format"`
	}

	if err := json.Unmarshal(output, &probe); err != nil || len(probe.Streams) == 0 {
		fmt.Printf("[%s]  No video stream in chunk %d — file may be incomplete\n", now(), stats.ChunkNumber)
		if stats.DurationSec == 0 {
			stats.DurationSec = float64(chunkTime)
		}
		return
	}

	s := probe.Streams[0]
	stats.Width = s.Width
	stats.Height = s.Height
	stats.Codec = s.CodecName
	stats.Profile = s.Profile
	if s.Level > 0 {
		stats.Level = fmt.Sprintf("%.1f", float64(s.Level)/10.0)
	}

	stats.BitDepth = 8
	if s.BitsPerRawSample != "" {
		if bd, err := strconv.Atoi(s.BitsPerRawSample); err == nil && bd > 0 {
			stats.BitDepth = bd
		}
	}

	parseRate := func(r string) float64 {
		parts := strings.Split(r, "/")
		if len(parts) == 2 {
			num, _ := strconv.ParseFloat(parts[0], 64)
			den, _ := strconv.ParseFloat(parts[1], 64)
			if den > 0 && num > 0 {
				return num / den
			}
		}
		return 0
	}
	stats.FrameRate = parseRate(s.AvgFrameRate)
	if stats.FrameRate == 0 {
		stats.FrameRate = parseRate(s.RFrameRate)
	}

	if probe.Format.Duration != "" {
		stats.DurationSec, _ = strconv.ParseFloat(probe.Format.Duration, 64)
	}
	if stats.DurationSec == 0 {
		stats.DurationSec = float64(chunkTime)
	}

	if s.NbFrames != "" {
		stats.TotalFrames, _ = strconv.Atoi(s.NbFrames)
	}
	if stats.TotalFrames == 0 && stats.FrameRate > 0 && stats.DurationSec > 0 {
		stats.TotalFrames = int(stats.DurationSec * stats.FrameRate)
	}

	if stats.Width > 0 && stats.Height > 0 {
		stats.RawFrameSizeKB = float64(stats.Width*stats.Height) * 1.5 / 1024
	}

	fmt.Printf("[%s]  Stream: %dx%d @ %.2ffps, %s %s, %.2f sec, %d frames\n",
		now(), stats.Width, stats.Height, stats.FrameRate,
		stats.Codec, stats.Profile, stats.DurationSec, stats.TotalFrames)
}

func probeFrameInfo(stats *ChunkStats, filePath string) []FrameDetail {
	var frames []FrameDetail

	fmt.Printf("[%s]  Probing frame details...\n", now())

	// Retry frame probe with same patience
	var output []byte
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		cmd := exec.Command("ffprobe",
			"-v", "quiet",
			"-select_streams", "v:0",
			"-show_frames",
			"-show_entries", "frame=pict_type,pkt_size,pts_time",
			"-of", "json",
			filePath)
		output, err = cmd.Output()
		if err == nil && len(output) > 2 {
			break
		}
		fmt.Printf("[%s]  Frame probe attempt %d failed: %v — retrying in 3s\n", now(), attempt, err)
		time.Sleep(3 * time.Second)
	}

	if err != nil {
		fmt.Printf("[%s]  Frame probe failed, using fallback for chunk %d\n", now(), stats.ChunkNumber)
		probeFrameTypesFallback(stats, filePath)
		return frames
	}

	var probe struct {
		Frames []struct {
			PictType string `json:"pict_type"`
			PktSize  string `json:"pkt_size"`
			PtsTime  string `json:"pts_time"`
		} `json:"frames"`
	}

	if err := json.Unmarshal(output, &probe); err != nil {
		fmt.Printf("[%s]  Frame parse failed, using fallback: %v\n", now(), err)
		probeFrameTypesFallback(stats, filePath)
		return frames
	}

	rawBytesPerFrame := int64(stats.Width * stats.Height * 3 / 2)
	if rawBytesPerFrame == 0 {
		rawBytesPerFrame = int64(1920 * 1080 * 3 / 2)
	}

	var totalBytes int64
	var iBytes, pBytes, bBytes int64
	var iCount, pCount, bCount int
	var peakBitrate float64
	minBitrate := math.MaxFloat64

	stats.IFrameSizes = make([]float64, 0)
	stats.PFrameSizes = make([]float64, 0)
	stats.BFrameSizes = make([]float64, 0)
	stats.IFrameRawSizes = make([]float64, 0)
	stats.PFrameRawSizes = make([]float64, 0)
	stats.BFrameRawSizes = make([]float64, 0)

	for i, f := range probe.Frames {
		sizeBytes, _ := strconv.ParseInt(f.PktSize, 10, 64)
		totalBytes += sizeBytes

		var bitrate float64
		if stats.FrameRate > 0 {
			bitrate = float64(sizeBytes*8) * stats.FrameRate / 1000
		}
		if bitrate > peakBitrate {
			peakBitrate = bitrate
		}
		if bitrate < minBitrate && bitrate > 0 {
			minBitrate = bitrate
		}

		compressPct := 0.0
		if rawBytesPerFrame > 0 {
			compressPct = (1.0 - float64(sizeBytes)/float64(rawBytesPerFrame)) * 100
		}

		pts, _ := strconv.ParseFloat(f.PtsTime, 64)
		rawSizeKB := float64(rawBytesPerFrame) / 1024
		encSizeKB := float64(sizeBytes) / 1024
		savingsKB := rawSizeKB - encSizeKB

		qp := 20 + int((100-compressPct)/5)
		if qp > 51 {
			qp = 51
		}
		if qp < 10 {
			qp = 10
		}

		psnr := 30.0 + 10*math.Log10(bitrate/100)
		if psnr > 50 {
			psnr = 50
		}
		if psnr < 20 {
			psnr = 20
		}

		frameDetail := FrameDetail{
			FrameNumber:    i + 1,
			Type:           f.PictType,
			SizeBytes:      sizeBytes,
			RawSizeBytes:   rawBytesPerFrame,
			BitrateKbps:    bitrate,
			CompressionPct: compressPct,
			QP:             qp,
			PSNR:           psnr,
			Pts:            pts,
			RawSizeKB:      rawSizeKB,
			EncodedSizeKB:  encSizeKB,
			SavingsKB:      savingsKB,
			SavingsPct:     compressPct,
		}

		switch f.PictType {
		case "I":
			stats.IFrames++
			iCount++
			iBytes += sizeBytes
			stats.IFrameSizes = append(stats.IFrameSizes, encSizeKB)
			stats.IFrameRawSizes = append(stats.IFrameRawSizes, rawSizeKB)
		case "P":
			stats.PFrames++
			pCount++
			pBytes += sizeBytes
			stats.PFrameSizes = append(stats.PFrameSizes, encSizeKB)
			stats.PFrameRawSizes = append(stats.PFrameRawSizes, rawSizeKB)
		case "B":
			stats.BFrames++
			bCount++
			bBytes += sizeBytes
			stats.BFrameSizes = append(stats.BFrameSizes, encSizeKB)
			stats.BFrameRawSizes = append(stats.BFrameRawSizes, rawSizeKB)
		}

		if includeFrames {
			frames = append(frames, frameDetail)
		}
	}

	if minBitrate == math.MaxFloat64 {
		minBitrate = 0
	}

	stats.TotalFrames = stats.IFrames + stats.PFrames + stats.BFrames
	stats.AfterEncodingMB = float64(totalBytes) / (1024 * 1024)

	if stats.DurationSec > 0 {
		stats.AvgBitrateKbps = float64(totalBytes*8) / stats.DurationSec / 1000
	}
	stats.PeakBitrateKbps = peakBitrate
	stats.MinBitrateKbps = minBitrate

	if iCount > 0 {
		stats.IFrameAvgKB = float64(iBytes) / float64(iCount) / 1024
	}
	if pCount > 0 {
		stats.PFrameAvgKB = float64(pBytes) / float64(pCount) / 1024
	}
	if bCount > 0 {
		stats.BFrameAvgKB = float64(bBytes) / float64(bCount) / 1024
	}
	if stats.TotalFrames > 0 {
		stats.AvgFrameSizeKB = float64(totalBytes) / float64(stats.TotalFrames) / 1024
	}

	if stats.Width > 0 && stats.Height > 0 && stats.TotalFrames > 0 {
		stats.BeforeEncodingMB = (float64(rawBytesPerFrame) * float64(stats.TotalFrames)) / (1024 * 1024)
	}

	fmt.Printf("[%s]  Frames: %d total (%d I, %d P, %d B), avg %.0f kbps\n",
		now(), stats.TotalFrames, stats.IFrames, stats.PFrames, stats.BFrames, stats.AvgBitrateKbps)

	return frames
}

func probeFrameTypesFallback(stats *ChunkStats, filePath string) {
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_frames",
		"-show_entries", "frame=pict_type",
		"-of", "csv=p=0",
		filePath)

	output, _ := cmd.Output()
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")

	for _, line := range lines {
		switch strings.TrimSpace(line) {
		case "I":
			stats.IFrames++
		case "P":
			stats.PFrames++
		case "B":
			stats.BFrames++
		}
	}
	stats.TotalFrames = stats.IFrames + stats.PFrames + stats.BFrames

	if stats.TotalFrames > 0 && stats.FileSizeMB > 0 {
		avgSize := stats.FileSizeMB * 1024 / float64(stats.TotalFrames)
		stats.IFrameAvgKB = avgSize * 5
		stats.PFrameAvgKB = avgSize * 1.5
		stats.BFrameAvgKB = avgSize * 0.5
		stats.AvgFrameSizeKB = avgSize
		stats.AfterEncodingMB = stats.FileSizeMB

		rawBytesPerFrame := int64(stats.Width * stats.Height * 3 / 2)
		stats.BeforeEncodingMB = float64(rawBytesPerFrame) * float64(stats.TotalFrames) / (1024 * 1024)

		if stats.DurationSec > 0 {
			stats.AvgBitrateKbps = stats.FileSizeMB * 8 * 1024 / stats.DurationSec
		}

		rawKB := float64(rawBytesPerFrame) / 1024
		stats.IFrameSizes = make([]float64, stats.IFrames)
		stats.PFrameSizes = make([]float64, stats.PFrames)
		stats.BFrameSizes = make([]float64, stats.BFrames)
		stats.IFrameRawSizes = make([]float64, stats.IFrames)
		stats.PFrameRawSizes = make([]float64, stats.PFrames)
		stats.BFrameRawSizes = make([]float64, stats.BFrames)
		for i := 0; i < stats.IFrames; i++ {
			stats.IFrameSizes[i] = stats.IFrameAvgKB
			stats.IFrameRawSizes[i] = rawKB
		}
		for i := 0; i < stats.PFrames; i++ {
			stats.PFrameSizes[i] = stats.PFrameAvgKB
			stats.PFrameRawSizes[i] = rawKB
		}
		for i := 0; i < stats.BFrames; i++ {
			stats.BFrameSizes[i] = stats.BFrameAvgKB
			stats.BFrameRawSizes[i] = rawKB
		}
	}
}

func calculateCompression(stats *ChunkStats) {
	if stats.AfterEncodingMB == 0 {
		stats.AfterEncodingMB = stats.FileSizeMB
	}
	stats.OverheadMB = stats.FileSizeMB - stats.AfterEncodingMB
	if stats.OverheadMB < 0 {
		stats.OverheadMB = 0
	}
	if stats.BeforeEncodingMB > 0 && stats.AfterEncodingMB > 0 {
		stats.CompressionPct = (1.0 - stats.AfterEncodingMB/stats.BeforeEncodingMB) * 100
		if stats.CompressionPct > 99.9 {
			stats.CompressionPct = 99.9
		}
		stats.CompressionRatio = stats.BeforeEncodingMB / stats.AfterEncodingMB
	}
	if stats.Width > 0 && stats.Height > 0 && stats.FrameRate > 0 && stats.AvgBitrateKbps > 0 {
		pixelsPerFrame := float64(stats.Width * stats.Height)
		stats.BitsPerPixel = stats.AvgBitrateKbps * 1000 / (pixelsPerFrame * stats.FrameRate)
	}
}

func estimateQuality(stats *ChunkStats) {
	if stats.BitsPerPixel > 0 {
		stats.PSNR = 30.0 + 10*math.Log10(stats.BitsPerPixel*1000)
		if stats.PSNR > 50 {
			stats.PSNR = 50
		}
		if stats.PSNR < 20 {
			stats.PSNR = 20
		}
		stats.SSIM = 1.0 - (1.0 / math.Pow(10, stats.PSNR/20))
	}
	if stats.AvgBitrateKbps > 0 && stats.Width > 0 && stats.Height > 0 && stats.FrameRate > 0 {
		complexity := float64(stats.Width*stats.Height) * stats.FrameRate / 1_000_000
		stats.AvgQP = 50.0 - 10*math.Log10(stats.AvgBitrateKbps/complexity)
		if stats.AvgQP < 10 {
			stats.AvgQP = 10
		}
		if stats.AvgQP > 51 {
			stats.AvgQP = 51
		}
	}
}

func saveChunkStats() {
	chunksMutex.RLock()
	defer chunksMutex.RUnlock()
	data, _ := json.MarshalIndent(allChunks, "", "  ")
	os.WriteFile("chunk_stats.json", data, 0644)
}

func saveSessionSummary(sessionFile string) {
	chunksMutex.RLock()
	defer chunksMutex.RUnlock()
	if len(allChunks) == 0 {
		return
	}
	var totalDur, totalBefore, totalAfter float64
	var totalI, totalP, totalB, totalFrames int
	var psnrSum, qpSum float64

	for _, chunk := range allChunks {
		totalDur += chunk.DurationSec
		totalBefore += chunk.BeforeEncodingMB
		totalAfter += chunk.AfterEncodingMB
		totalI += chunk.IFrames
		totalP += chunk.PFrames
		totalB += chunk.BFrames
		totalFrames += chunk.TotalFrames
		psnrSum += chunk.PSNR
		qpSum += chunk.AvgQP
	}

	n := float64(len(allChunks))
	avgComp := 0.0
	if totalBefore > 0 {
		avgComp = (1 - totalAfter/totalBefore) * 100
	}
	avgBitrate := 0.0
	if totalDur > 0 {
		avgBitrate = totalAfter * 8 * 1024 / totalDur
	}

	summary := SessionSummary{
		StartTime:         sessionStart.Format("2006-01-02 15:04:05"),
		EndTime:           time.Now().Format("2006-01-02 15:04:05"),
		DurationMinutes:   totalDur / 60,
		TotalChunks:       len(allChunks),
		TotalBeforeMB:     totalBefore,
		TotalAfterMB:      totalAfter,
		AvgCompressionPct: avgComp,
		AvgBitrateKbps:    avgBitrate,
		AvgPSNR:           psnrSum / n,
		AvgQP:             qpSum / n,
		TotalFrames:       totalFrames,
		TotalIFrames:      totalI,
		TotalPFrames:      totalP,
		TotalBFrames:      totalB,
		Chunks:            allChunks,
	}

	data, _ := json.MarshalIndent(summary, "", "  ")
	os.WriteFile(sessionFile, data, 0644)
	fmt.Printf("\n[%s] Session saved to: %s\n", now(), sessionFile)
}

func printFrameChanges(stats *ChunkStats) {
	fmt.Println("\n" + strings.Repeat("═", 120))
	fmt.Printf(" FRAME-TO-FRAME CHANGE ANALYSIS (Chunk %d)\n", stats.ChunkNumber)
	fmt.Println(strings.Repeat("═", 120))

	fmt.Printf("%-6s %-5s %-8s %-10s %-10s %-10s %-10s %-8s %-8s %-8s\n",
		"Frame", "Type", "Size(KB)", "Δ Size", "Δ%", "Bitrate", "Δ Bitrate", "PSNR", "Δ PSNR", "Time(ms)")
	fmt.Println(strings.Repeat("─", 120))

	var prevFrame *FrameDetail
	frameCount := 0
	maxFramesToShow := 30

	for _, frame := range stats.Frames {
		if frameCount >= maxFramesToShow {
			fmt.Printf("\n... and %d more frames\n", len(stats.Frames)-maxFramesToShow)
			break
		}
		sizeKB := float64(frame.SizeBytes) / 1024
		timeMs := frame.Pts * 1000

		if prevFrame == nil {
			fmt.Printf("%-6d %-5s %-8.2f %-10s %-10s %-10.0f %-10s %-8.1f %-8s %-8.0f\n",
				frame.FrameNumber, frame.Type, sizeKB, "-", "-", frame.BitrateKbps, "-", frame.PSNR, "-", timeMs)
		} else {
			prevSizeKB := float64(prevFrame.SizeBytes) / 1024
			sizeChange := sizeKB - prevSizeKB
			sizeChangePct := 0.0
			if prevSizeKB > 0 {
				sizeChangePct = (sizeChange / prevSizeKB) * 100
			}
			bitrateChange := frame.BitrateKbps - prevFrame.BitrateKbps
			psnrChange := frame.PSNR - prevFrame.PSNR
			timeDelta := (frame.Pts - prevFrame.Pts) * 1000

			fmt.Printf("%-6d %-5s %-8.2f %-+10.2f %-+9.1f%% %-10.0f %-+10.0f %-8.1f %-+8.1f %-8.0f\n",
				frame.FrameNumber, frame.Type, sizeKB, sizeChange, sizeChangePct,
				frame.BitrateKbps, bitrateChange, frame.PSNR, psnrChange, timeDelta)
		}
		prevFrame = &frame
		frameCount++
	}

	var peakBitrate float64
	var peakFrame int
	for _, frame := range stats.Frames {
		if frame.BitrateKbps > peakBitrate {
			peakBitrate = frame.BitrateKbps
			peakFrame = frame.FrameNumber
		}
	}

	fmt.Println("\n" + strings.Repeat("─", 120))
	fmt.Printf("Peak bitrate: %.0f kbps (Frame %d) | Total frames: %d\n", peakBitrate, peakFrame, len(stats.Frames))
	fmt.Println(strings.Repeat("═", 120) + "\n")
}

func saveFrameChangesReadable(stats *ChunkStats) {
	changesFile := fmt.Sprintf("chunk_%02d_frame_changes.txt", stats.ChunkNumber)
	file, err := os.Create(changesFile)
	if err != nil {
		return
	}
	defer file.Close()

	fmt.Fprintf(file, "%s\n", strings.Repeat("=", 100))
	fmt.Fprintf(file, "FRAME-TO-FRAME CHANGE ANALYSIS - Chunk %d\n", stats.ChunkNumber)
	fmt.Fprintf(file, "Filename: %s | Duration: %.2f sec | Frames: %d\n", stats.Filename, stats.DurationSec, stats.TotalFrames)
	fmt.Fprintf(file, "%s\n", strings.Repeat("=", 100))
	fmt.Fprintf(file, "%-6s %-5s %-10s %-12s %-10s %-12s %-12s %-8s %-10s %-8s\n",
		"Frame", "Type", "Size(KB)", "Δ Size", "Δ%", "Bitrate", "Δ Bitrate", "PSNR", "Δ PSNR", "Time(ms)")
	fmt.Fprintf(file, "%s\n", strings.Repeat("─", 100))

	var prevFrame *FrameDetail
	for _, frame := range stats.Frames {
		sizeKB := float64(frame.SizeBytes) / 1024
		timeMs := frame.Pts * 1000
		if prevFrame == nil {
			fmt.Fprintf(file, "%-6d %-5s %-10.2f %-12s %-10s %-12.0f %-12s %-8.1f %-10s %-8.0f\n",
				frame.FrameNumber, frame.Type, sizeKB, "-", "-", frame.BitrateKbps, "-", frame.PSNR, "-", timeMs)
		} else {
			prevSizeKB := float64(prevFrame.SizeBytes) / 1024
			sizeChange := sizeKB - prevSizeKB
			sizeChangePct := 0.0
			if prevSizeKB > 0 {
				sizeChangePct = (sizeChange / prevSizeKB) * 100
			}
			fmt.Fprintf(file, "%-6d %-5s %-10.2f %-+11.2f %-+9.1f%% %-12.0f %-+11.0f %-8.1f %-+9.1f %-8.0f\n",
				frame.FrameNumber, frame.Type, sizeKB, sizeChange, sizeChangePct,
				frame.BitrateKbps, frame.BitrateKbps-prevFrame.BitrateKbps,
				frame.PSNR, frame.PSNR-prevFrame.PSNR, (frame.Pts-prevFrame.Pts)*1000)
		}
		prevFrame = &frame
	}
	fmt.Printf("[%s] Frame changes saved: %s\n", now(), changesFile)
}

func saveFrameChangesCSV(stats *ChunkStats) {
	csvFile := fmt.Sprintf("chunk_%02d_frame_changes.csv", stats.ChunkNumber)
	file, err := os.Create(csvFile)
	if err != nil {
		return
	}
	defer file.Close()

	fmt.Fprintf(file, "Frame,Type,SizeKB,DeltaSizeKB,DeltaSizePct,BitrateKbps,DeltaBitrate,PSNR,DeltaPSNR,TimeMs\n")

	var prevFrame *FrameDetail
	for _, frame := range stats.Frames {
		sizeKB := float64(frame.SizeBytes) / 1024
		timeMs := frame.Pts * 1000
		if prevFrame == nil {
			fmt.Fprintf(file, "%d,%s,%.2f,0,0,%.0f,0,%.1f,0,%.0f\n",
				frame.FrameNumber, frame.Type, sizeKB, frame.BitrateKbps, frame.PSNR, timeMs)
		} else {
			prevSizeKB := float64(prevFrame.SizeBytes) / 1024
			sizeChange := sizeKB - prevSizeKB
			sizeChangePct := 0.0
			if prevSizeKB > 0 {
				sizeChangePct = (sizeChange / prevSizeKB) * 100
			}
			fmt.Fprintf(file, "%d,%s,%.2f,%.2f,%.1f,%.0f,%.0f,%.1f,%.1f,%.0f\n",
				frame.FrameNumber, frame.Type, sizeKB, sizeChange, sizeChangePct,
				frame.BitrateKbps, frame.BitrateKbps-prevFrame.BitrateKbps,
				frame.PSNR, frame.PSNR-prevFrame.PSNR, (frame.Pts-prevFrame.Pts)*1000)
		}
		prevFrame = &frame
	}
	fmt.Printf("[%s] CSV saved: %s\n", now(), csvFile)
}

func printChunkReport(stats ChunkStats) {
	if stats.FileSizeMB < 0.1 {
		return
	}

	totalFrames := stats.IFrames + stats.PFrames + stats.BFrames
	if totalFrames == 0 {
		totalFrames = stats.TotalFrames
	}

	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Printf("CHUNK %d ANALYSIS REPORT\n", stats.ChunkNumber)
	fmt.Println(strings.Repeat("=", 80))

	fmt.Println("\n1. FILE INFORMATION")
	fmt.Println(strings.Repeat("-", 40))
	fmt.Printf("   Filename:     %s\n", stats.Filename)
	fmt.Printf("   File size:    %.2f MB\n", stats.FileSizeMB)
	fmt.Printf("   Duration:     %.2f seconds\n", stats.DurationSec)
	fmt.Printf("   Resolution:   %d x %d\n", stats.Width, stats.Height)
	fmt.Printf("   Frame rate:   %.2f fps\n", stats.FrameRate)
	fmt.Printf("   Codec:        %s (%s, Level %s)\n", stats.Codec, stats.Profile, stats.Level)
	fmt.Printf("   Bit depth:    %d-bit\n", stats.BitDepth)
	fmt.Printf("   Total frames: %d\n", totalFrames)

	fmt.Println("\n2. BEFORE ENCODING (Raw Uncompressed)")
	fmt.Println(strings.Repeat("-", 40))
	fmt.Printf("   Raw frame size:     %.1f KB\n", stats.RawFrameSizeKB)
	fmt.Printf("   Total raw size:     %.2f MB\n", stats.BeforeEncodingMB)
	rawMbps := 0.0
	if stats.FrameRate > 0 {
		rawMbps = stats.RawFrameSizeKB * 1024 * 8 * stats.FrameRate / 1_000_000
	}
	fmt.Printf("   Raw bitrate:        %.0f Mbps\n", rawMbps)

	fmt.Println("\n3. AFTER ENCODING")
	fmt.Println(strings.Repeat("-", 40))
	fmt.Printf("   Encoded size:       %.2f MB\n", stats.AfterEncodingMB)
	fmt.Printf("   File on disk:       %.2f MB\n", stats.FileSizeMB)
	fmt.Printf("   Average bitrate:    %.0f kbps\n", stats.AvgBitrateKbps)
	fmt.Printf("   Peak bitrate:       %.0f kbps\n", stats.PeakBitrateKbps)

	fmt.Println("\n4. COMPRESSION")
	fmt.Println(strings.Repeat("-", 40))
	fmt.Printf("   Compression:        %.1f%% (%.1f:1)\n", stats.CompressionPct, stats.CompressionRatio)
	fmt.Printf("   Bits per pixel:     %.4f\n", stats.BitsPerPixel)

	iPct, pPct, bPct := 0.0, 0.0, 0.0
	if totalFrames > 0 {
		iPct = float64(stats.IFrames) / float64(totalFrames) * 100
		pPct = float64(stats.PFrames) / float64(totalFrames) * 100
		bPct = float64(stats.BFrames) / float64(totalFrames) * 100
	}

	fmt.Println("\n5. FRAME ANALYSIS")
	fmt.Println(strings.Repeat("-", 40))
	fmt.Printf("   Total:    %d frames\n", totalFrames)
	fmt.Printf("   I-frames: %d (%.1f%%) avg %.1f KB\n", stats.IFrames, iPct, stats.IFrameAvgKB)
	fmt.Printf("   P-frames: %d (%.1f%%) avg %.1f KB\n", stats.PFrames, pPct, stats.PFrameAvgKB)
	fmt.Printf("   B-frames: %d (%.1f%%) avg %.1f KB\n", stats.BFrames, bPct, stats.BFrameAvgKB)
	fmt.Printf("   Average:  %.1f KB/frame\n", stats.AvgFrameSizeKB)

	fmt.Println("\n6. QUALITY")
	fmt.Println(strings.Repeat("-", 40))
	quality := "POOR"
	if stats.PSNR >= 40 {
		quality = "EXCELLENT"
	} else if stats.PSNR >= 37 {
		quality = "GOOD"
	} else if stats.PSNR >= 34 {
		quality = "FAIR"
	}
	fmt.Printf("   PSNR (est): %.2f dB (%s)\n", stats.PSNR, quality)
	fmt.Printf("   SSIM (est): %.4f\n", stats.SSIM)
	fmt.Printf("   Avg QP:     %.1f\n", stats.AvgQP)

	fmt.Println("\n7. TIMELINE")
	fmt.Println(strings.Repeat("-", 40))
	fmt.Printf("   Recording Start:   %s\n", stats.StartTimestamp)
	fmt.Printf("   File Ready:        %s\n", stats.FileReadyTimestamp)
	fmt.Printf("   Analysis Complete: %s\n", stats.AnalysisCompleteTimestamp)
	fmt.Printf("   Recording:         %.3f sec\n", stats.RecordingDuration)
	fmt.Printf("   File Write:        %.3f sec\n", stats.FileWriteTime)
	fmt.Printf("   Analysis:          %.3f sec\n", stats.AnalysisDuration)
	fmt.Printf("   TOTAL:             %.3f sec\n", stats.TotalChunkTime)

	fmt.Println("\n" + strings.Repeat("=", 80) + "\n")
}

func printFrameDetails(stats *ChunkStats) {
	if len(stats.IFrameSizes) == 0 && len(stats.PFrameSizes) == 0 {
		return
	}

	fmt.Println("\n" + strings.Repeat("═", 100))
	fmt.Printf(" FRAME TYPE COMPRESSION DETAIL (Chunk %d)\n", stats.ChunkNumber)
	fmt.Println(strings.Repeat("═", 100))

	printFrameTypeTable := func(label string, sizes, rawSizes []float64) {
		if len(sizes) == 0 {
			return
		}
		fmt.Printf("\n %s\n", label)
		fmt.Println(strings.Repeat("─", 80))
		fmt.Printf("%-12s %-15s %-15s %-15s %-10s\n", "Frame #", "Before (KB)", "After (KB)", "Saved (KB)", "Saved %")
		for i := 0; i < len(sizes) && i < 5; i++ {
			rawKB := rawSizes[i]
			encKB := sizes[i]
			savedKB := rawKB - encKB
			savedPct := (savedKB / rawKB) * 100
			fmt.Printf("  %-10d %-15.1f %-15.1f %-15.1f %.1f%%\n", i+1, rawKB, encKB, savedKB, savedPct)
		}
		if len(sizes) > 5 {
			fmt.Printf("  ... and %d more\n", len(sizes)-5)
		}
		avgRaw := average(rawSizes)
		avgEnc := average(sizes)
		avgSaved := avgRaw - avgEnc
		fmt.Printf("\n  Average: %.1f KB raw → %.1f KB encoded (saved %.1f KB, %.1f%%)\n",
			avgRaw, avgEnc, avgSaved, (avgSaved/avgRaw)*100)
	}

	printFrameTypeTable("I-FRAMES (Key Frames)", stats.IFrameSizes, stats.IFrameRawSizes)
	printFrameTypeTable("P-FRAMES (Predicted)", stats.PFrameSizes, stats.PFrameRawSizes)
	printFrameTypeTable("B-FRAMES (Bi-directional)", stats.BFrameSizes, stats.BFrameRawSizes)

	fmt.Println("\n COMPRESSION SUMMARY")
	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("%-12s %-15s %-15s %-15s %-10s\n", "Type", "Before (KB)", "After (KB)", "Saved (KB)", "Ratio")
	if stats.RawFrameSizeKB > 0 && stats.IFrameAvgKB > 0 {
		fmt.Printf("%-12s %-15.1f %-15.1f %-15.1f %.1f:1\n", "I-frame",
			stats.RawFrameSizeKB, stats.IFrameAvgKB, stats.RawFrameSizeKB-stats.IFrameAvgKB,
			stats.RawFrameSizeKB/stats.IFrameAvgKB)
	}
	if stats.RawFrameSizeKB > 0 && stats.PFrameAvgKB > 0 {
		fmt.Printf("%-12s %-15.1f %-15.1f %-15.1f %.1f:1\n", "P-frame",
			stats.RawFrameSizeKB, stats.PFrameAvgKB, stats.RawFrameSizeKB-stats.PFrameAvgKB,
			stats.RawFrameSizeKB/stats.PFrameAvgKB)
	}
	if stats.RawFrameSizeKB > 0 && stats.BFrameAvgKB > 0 {
		fmt.Printf("%-12s %-15.1f %-15.1f %-15.1f %.1f:1\n", "B-frame",
			stats.RawFrameSizeKB, stats.BFrameAvgKB, stats.RawFrameSizeKB-stats.BFrameAvgKB,
			stats.RawFrameSizeKB/stats.BFrameAvgKB)
	}
	fmt.Println("\n" + strings.Repeat("═", 100) + "\n")
}

func average(nums []float64) float64 {
	if len(nums) == 0 {
		return 0
	}
	sum := 0.0
	for _, n := range nums {
		sum += n
	}
	return sum / float64(len(nums))
}

func printFinalSummary() {
	chunksMutex.RLock()
	defer chunksMutex.RUnlock()

	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("SESSION FINAL SUMMARY")
	fmt.Println(strings.Repeat("=", 80))

	if len(allChunks) == 0 {
		fmt.Println("No chunks analyzed")
		return
	}

	var totalDur, totalBefore, totalAfter float64
	var totalI, totalP, totalB, totalFrames int
	var psnrSum float64

	for _, chunk := range allChunks {
		totalDur += chunk.DurationSec
		totalBefore += chunk.BeforeEncodingMB
		totalAfter += chunk.AfterEncodingMB
		totalI += chunk.IFrames
		totalP += chunk.PFrames
		totalB += chunk.BFrames
		totalFrames += chunk.TotalFrames
		psnrSum += chunk.PSNR
	}

	fmt.Printf("Session: %.1f min | Chunks: %d | Frames: %d\n",
		totalDur/60, len(allChunks), totalFrames)
	if totalFrames > 0 {
		fmt.Printf("I: %d (%.1f%%) | P: %d (%.1f%%) | B: %d (%.1f%%)\n",
			totalI, float64(totalI)/float64(totalFrames)*100,
			totalP, float64(totalP)/float64(totalFrames)*100,
			totalB, float64(totalB)/float64(totalFrames)*100)
	}
	if totalBefore > 0 {
		fmt.Printf("Raw: %.1f MB → Encoded: %.1f MB | Compression: %.1f%%\n",
			totalBefore, totalAfter, (1-totalAfter/totalBefore)*100)
	}
	if totalDur > 0 {
		fmt.Printf("Avg bitrate: %.0f kbps | Avg PSNR: %.1f dB\n",
			totalAfter*8*1024/totalDur, psnrSum/float64(len(allChunks)))
	}
	fmt.Println(strings.Repeat("=", 80) + "\n")
}

func printHeader() {
	fmt.Println("\nDELTA RECEIVER - SRT to MP4 with Frame-Level Analysis")
	fmt.Println()
}

func printConfig(port int, outputDir string, chunkTimeSec int, filenamePrefix string, hp int, encoder string) {
	fmt.Printf("SRT Port: %d | Output: %s | Chunk: %ds | HTTP: %d | Encoder: %s | FrameDebug: %v\n\n",
		port, outputDir, chunkTimeSec, hp, encoder, includeFrames)
}

func printHelp() {
	fmt.Println(`
Usage: ./delta_receiver [options]

  --port N         SRT listen port (default: 9003)
  --output DIR     Output directory (default: ./recordings)
  --chunk-time N   Chunk seconds (default: 120)
  --filename PRE   Filename prefix (default: delta_chunk)
  --http-port N    HTTP API port (default: 8080)
  --encoder TYPE   h264 or h265 (default: h264)
  --auto-port      Auto-find available port
  --frame-detail   Include per-frame data in JSON
  --verbose        Verbose GStreamer output
  --help           Show this help
`)
}

func now() string {
	return time.Now().Format("15:04:05")
}

func extractFilePath(line string) string {
	words := strings.Fields(line)
	for _, word := range words {
		if strings.HasSuffix(word, ".mp4") || strings.Contains(word, ".mp4") {
			word = strings.Trim(word, `"'`)
			return word
		}
	}
	return ""
}

func isPortAvailable(port int) bool {
	addr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func findAvailablePort(startPort int) int {
	for p := startPort; p < startPort+100; p++ {
		if isPortAvailable(p) {
			return p
		}
	}
	return startPort + 1000
}
