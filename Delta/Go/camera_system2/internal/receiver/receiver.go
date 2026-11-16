package receiver
 
import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
 
	"camera-system/internal/analyzer"
	"camera-system/internal/config"
	"camera-system/internal/mq"
)
 
// ─────────────────────────────────────────────────────────────────────────────
// Per-camera in-memory state (keyed by outDir)
// ─────────────────────────────────────────────────────────────────────────────
 
type cameraState struct {
	mu              sync.Mutex
	chunkStartTimes map[int]time.Time
	currentChunkNum int
	processedFiles  map[string]bool
	allChunks       []*analyzer.ChunkStats
	receivedIndices map[int]*analyzer.ChunkIndex
	fileWriteMu     sync.Mutex // protects session_summary.json and chunk_*_index.json writes
}
 
func newCameraState() *cameraState {
	return &cameraState{
		chunkStartTimes: make(map[int]time.Time),
		processedFiles:  make(map[string]bool),
		receivedIndices: make(map[int]*analyzer.ChunkIndex),
	}
}
 
var globalStateMu sync.RWMutex
var globalStates = make(map[string]*cameraState)
 
// RegisterCamera pre-creates state for an output directory (called at startup).
func RegisterCamera(outDir string) {
	globalStateMu.Lock()
	defer globalStateMu.Unlock()
	if _, ok := globalStates[outDir]; !ok {
		globalStates[outDir] = newCameraState()
	}
}
 
func getOrCreateState(outDir string) *cameraState {
	globalStateMu.RLock()
	state, ok := globalStates[outDir]
	globalStateMu.RUnlock()
	if ok {
		return state
	}
	globalStateMu.Lock()
	defer globalStateMu.Unlock()
	if state, ok = globalStates[outDir]; ok {
		return state
	}
	state = newCameraState()
	globalStates[outDir] = state
	return state
}
 
// GetChunks returns the chunk list for any outDir (used by API).
func GetChunks(outDir string) []*analyzer.ChunkStats {
	globalStateMu.RLock()
	state, ok := globalStates[outDir]
	globalStateMu.RUnlock()
	if !ok {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	result := make([]*analyzer.ChunkStats, len(state.allChunks))
	copy(result, state.allChunks)
	return result
}
 
// GetChunkIndex returns the index for a specific chunk number.
func GetChunkIndex(outDir string, chunkNum int) *analyzer.ChunkIndex {
	globalStateMu.RLock()
	state, ok := globalStates[outDir]
	globalStateMu.RUnlock()
	if !ok {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.receivedIndices[chunkNum]
}
 
// ─────────────────────────────────────────────────────────────────────────────
// Port availability check
// ─────────────────────────────────────────────────────────────────────────────
 
func isPortAvailable(port int) bool {
	addr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
 
func waitForPort(ctx context.Context, port int, maxWait time.Duration) bool {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		if isPortAvailable(port) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}
 
// ─────────────────────────────────────────────────────────────────────────────
// Run — starts a GStreamer receiver pipeline for one camera session.
// outDir is passed explicitly (the same across restarts when using session timestamp).
// ─────────────────────────────────────────────────────────────────────────────
 
func Run(ctx context.Context, cfg *config.Config, cam config.CameraConfig, outDir string, mqClient *mq.Client) error {
	chunkTimeSec := cfg.Server.ChunkTime
	encoder := cfg.Server.Encoder
	logPrefix := logPfx(cam)
 
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("cannot create output dir %q: %w", outDir, err)
	}
 
	port := cfg.SRTPort(cam)
 
	// Wait up to 15 s for the port to be released from the previous run
	if !isPortAvailable(port) {
		fmt.Printf("%s Port %d busy, waiting up to 15s...\n", logPrefix, port)
		if !waitForPort(ctx, port, 15*time.Second) {
			return fmt.Errorf("port %d still in use after waiting", port)
		}
		fmt.Printf("%s Port %d is now free\n", logPrefix, port)
	}
 
	state := getOrCreateState(outDir)
	chunkPattern := filepath.Join(outDir, "chunk_%05d.mp4")
 
	var parserElem string
	switch strings.ToLower(encoder) {
	case "h265", "hevc":
		parserElem = "h265parse"
	default:
		parserElem = "h264parse"
	}
 
	srtURI := fmt.Sprintf("srt://0.0.0.0:%d?mode=listener&latency=200&timeout=10000000&transtype=live", port)
	pipeline := fmt.Sprintf(
		"gst-launch-1.0 -e -v "+
			"srtsrc uri=\"%s\" ! "+
			"queue max-size-buffers=200 leaky=downstream ! "+
			"tsdemux ! %s ! "+
			"splitmuxsink location=\"%s\" max-size-time=%d000000000 muxer=mp4mux",
		srtURI,
		parserElem,
		chunkPattern,
		chunkTimeSec,
	)
 
	fmt.Printf("%s Starting GStreamer receiver\n", logPrefix)
	fmt.Printf("%s SRT port : %d\n", logPrefix, port)
	fmt.Printf("%s Output   : %s\n", logPrefix, outDir)
	fmt.Printf("%s Chunks   : %ds each\n", logPrefix, chunkTimeSec)
	fmt.Printf("%s Pipeline : %s\n\n", logPrefix, pipeline)
 
	cmd := exec.Command("bash", "-c", pipeline)
	cmd.Env = os.Environ()
 
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
 
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start GStreamer receiver: %w", err)
	}
	fmt.Printf("%s GStreamer receiver started (PID %d)\n", logPrefix, cmd.Process.Pid)
 
	const prefix = "chunk"
 
	// Create a context for the monitor goroutines
	monitorCtx, cancelMonitors := context.WithCancel(ctx)
	var wg sync.WaitGroup
 
	wg.Add(1)
	go func() {
		defer wg.Done()
		monitorGStreamer(monitorCtx, stdout, cam, outDir, prefix, chunkTimeSec, state, cfg.Server.FrameDetail, mqClient)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		monitorGStreamer(monitorCtx, stderr, cam, outDir, prefix, chunkTimeSec, state, cfg.Server.FrameDetail, mqClient)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanForFinalizedChunks(monitorCtx, outDir, prefix, cam, chunkTimeSec, state, cfg.Server.FrameDetail, mqClient)
	}()
 
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
 
	select {
	case err := <-done:
		cancelMonitors()
		wg.Wait()
		return err
	case <-ctx.Done():
		cmd.Process.Kill()
		<-done
		cancelMonitors()
		wg.Wait()
		return ctx.Err()
	}
}
 
// ─────────────────────────────────────────────────────────────────────────────
// RunWithRestart — outer loop; uses the same session folder across restarts.
// ─────────────────────────────────────────────────────────────────────────────
 
// RunWithRestart — outer loop; generates a NEW timestamped folder on each (re)start.
func RunWithRestart(
	ctx context.Context,
	cfg *config.Config,
	cam config.CameraConfig,
	backoffMax time.Duration,
	mqClient *mq.Client,
) {
	logPrefix := logPfx(cam)
	backoff := 5 * time.Second
 
	for {
		select {
		case <-ctx.Done():
			fmt.Printf("%s Stopping receiver (shutdown)\n", logPrefix)
			return
		default:
		}
 
		// Generate a fresh session timestamp for this recording session
		sessionTS := time.Now().Format("20060102_150405")
		outDir := fmt.Sprintf("%s/%s/%s", cfg.Server.OutputDir, cam.ID, sessionTS)
 
		// Pre‑register so the API can serve this session’s data immediately
		RegisterCamera(outDir)
 
		fmt.Printf("%s Starting receiver... (session %s)\n", logPrefix, sessionTS)
		fmt.Printf("%s Recording → %s\n", logPrefix, outDir)
		start := time.Now()
 
		err := Run(ctx, cfg, cam, outDir, mqClient)
		elapsed := time.Since(start)
 
		if err != nil && err != context.Canceled {
			fmt.Printf("%s Receiver exited after %.1fs: %v\n", logPrefix, elapsed.Seconds(), err)
		} else if err == context.Canceled {
			fmt.Printf("%s Receiver stopped by command\n", logPrefix)
			return
		} else {
			fmt.Printf("%s Receiver exited cleanly after %.1fs\n", logPrefix, elapsed.Seconds())
		}
 
		// Tell the sender to restart so it reconnects to our new SRT listener.
		// Without this the sender's srtsink stays "connected" to a dead session
		// and never re-establishes the link to the new receiver session.
		if mqClient != nil {
			fmt.Printf("%s Receiver restarting — sending restart command to sender\n", logPrefix)
			_ = mqClient.PublishCommand(cam.ID, "restart", nil)
		}
 
		if elapsed > 30*time.Second {
			backoff = 5 * time.Second
		}
 
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
 
		backoff *= 2
		if backoff > backoffMax {
			backoff = backoffMax
		}
	}
}
 
// ─────────────────────────────────────────────────────────────────────────────
// monitorGStreamer — watches GStreamer logs for chunk boundary events
// ─────────────────────────────────────────────────────────────────────────────
 
func monitorGStreamer(
	ctx context.Context,
	pipe io.ReadCloser,
	cam config.CameraConfig,
	outDir, filenamePrefix string,
	chunkTimeSec int,
	state *cameraState,
	includeFrames bool,
	mqClient *mq.Client,
) {
	logPrefix := logPfx(cam)
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
 
	connected := false
	firstChunkDetected := false
 
	// Channel to receive lines or context done
	lineCh := make(chan string)
	go func() {
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			case lineCh <- scanner.Text():
			}
		}
	}()
 
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lineCh:
			if !ok {
				return
			}
			lineLower := strings.ToLower(line)
 
			if strings.Contains(line, "max-size-buffers") ||
				strings.Contains(line, "caps =") ||
				strings.Contains(line, "GstProxyPad") ||
				strings.Contains(line, "GstGhostPad") {
				continue
			}
 
			if strings.Contains(line, "ERROR:") && !strings.Contains(line, "preroll") {
				fmt.Printf("%s GST ERROR: %s\n", logPrefix, line)
			}
 
			if !connected && strings.Contains(line, "PLAYING") {
				connected = true
				fmt.Printf("\n%s Pipeline PLAYING — waiting for sender to connect...\n", logPrefix)
			}
 
			if strings.Contains(line, "location =") && strings.Contains(line, ".mp4") {
				chunkFile := extractFilePath(line)
				if chunkFile == "" {
					continue
				}
				logicalChunk := pathToChunkNum(chunkFile)
				if logicalChunk == 0 {
					continue
				}
 
				if !firstChunkDetected {
					firstChunkDetected = true
					fmt.Printf("\n%s First chunk detected: %s\n", logPrefix, filepath.Base(chunkFile))
				}
 
				startTime := time.Now()
				state.mu.Lock()
				state.chunkStartTimes[logicalChunk] = startTime
				prevChunkNum := state.currentChunkNum
				state.currentChunkNum = logicalChunk
				state.mu.Unlock()
 
				fmt.Printf("\n%s CHUNK %d STARTED → %s\n",
					logPrefix, logicalChunk, filepath.Base(chunkFile))
 
				if prevChunkNum > 0 && prevChunkNum < logicalChunk {
					prevFileIndex := prevChunkNum - 1
					prevChunkFile := filepath.Join(outDir,
						fmt.Sprintf("%s_%05d.mp4", filenamePrefix, prevFileIndex))
 
					state.mu.Lock()
					prevStart, hasPrevStart := state.chunkStartTimes[prevChunkNum]
					state.mu.Unlock()
 
					if hasPrevStart {
						capturedChunkNum := prevChunkNum
						capturedStart := prevStart
						capturedEnd := startTime
						capturedFile := prevChunkFile
						go func() {
							state.mu.Lock()
							state.processedFiles[capturedFile] = true
							state.mu.Unlock()
 
							analyzer.WaitForFileReady(ctx, capturedFile, cam.ID)
							stats := analyzer.Analyze(
								capturedFile, capturedChunkNum, cam.ID,
								capturedStart, capturedEnd, time.Now(),
								chunkTimeSec, includeFrames,
							)
							if stats != nil {
								appendChunkStats(state, outDir, capturedChunkNum, cam.ID, stats)
								printFullAnalysis(stats, capturedChunkNum)
								if mqClient != nil {
									_ = mqClient.PublishChunk(cam.ID, mq.ChunkData{
										ChunkNumber:    capturedChunkNum,
										File:           filepath.Base(capturedFile),
										SizeMB:         stats.FileSizeMB,
										DurationSec:    stats.DurationSec,
										AvgBitrateKbps: stats.AvgBitrateKbps,
										FrameCount:     stats.TotalFrames,
									})
								}
							}
						}()
					}
				}
			}
 
			if strings.Contains(lineLower, "finalized") ||
				(strings.Contains(lineLower, "closing") && strings.Contains(line, ".mp4")) {
 
				state.mu.Lock()
				cn := state.currentChunkNum
				startTime, exists := state.chunkStartTimes[cn]
				state.mu.Unlock()
 
				if cn == 0 || !exists {
					continue
				}
 
				endTime := time.Now()
				fileIndex := cn - 1
				chunkFile := filepath.Join(outDir,
					fmt.Sprintf("%s_%05d.mp4", filenamePrefix, fileIndex))
 
				fmt.Printf("%s CHUNK %d FINALIZED → %s\n", logPrefix, cn, filepath.Base(chunkFile))
 
				capturedChunkNum := cn
				capturedStart := startTime
				capturedEnd := endTime
				capturedFile := chunkFile
				go func() {
					state.mu.Lock()
					alreadyProcessed := state.processedFiles[capturedFile]
					state.mu.Unlock()
					if alreadyProcessed {
						return
					}
					state.mu.Lock()
					state.processedFiles[capturedFile] = true
					state.mu.Unlock()
 
					analyzer.WaitForFileReady(ctx, capturedFile, cam.ID)
					stats := analyzer.Analyze(
						capturedFile, capturedChunkNum, cam.ID,
						capturedStart, capturedEnd, time.Now(),
						chunkTimeSec, includeFrames,
					)
					if stats != nil {
						appendChunkStats(state, outDir, capturedChunkNum, cam.ID, stats)
						printFullAnalysis(stats, capturedChunkNum)
						if mqClient != nil {
							_ = mqClient.PublishChunk(cam.ID, mq.ChunkData{
								ChunkNumber:    capturedChunkNum,
								File:           filepath.Base(capturedFile),
								SizeMB:         stats.FileSizeMB,
								DurationSec:    stats.DurationSec,
								AvgBitrateKbps: stats.AvgBitrateKbps,
								FrameCount:     stats.TotalFrames,
							})
						}
					}
				}()
			}
		}
	}
}
 
// ─────────────────────────────────────────────────────────────────────────────
// scanForFinalizedChunks — fallback poller in case GStreamer events are missed
// ─────────────────────────────────────────────────────────────────────────────
 
func scanForFinalizedChunks(
	ctx context.Context,
	outDir, filenamePrefix string,
	cam config.CameraConfig,
	chunkTimeSec int,
	state *cameraState,
	includeFrames bool,
	mqClient *mq.Client,
) {
	ticker := time.NewTicker(8 * time.Second)
	defer ticker.Stop()
 
	lastSeen := make(map[string]int64)
 
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
 
		pattern := filepath.Join(outDir, fmt.Sprintf("%s_*.mp4", filenamePrefix))
		files, err := filepath.Glob(pattern)
		if err != nil || len(files) == 0 {
			continue
		}
		sort.Strings(files)
 
		for i, file := range files {
			state.mu.Lock()
			alreadyProcessed := state.processedFiles[file]
			state.mu.Unlock()
			if alreadyProcessed {
				continue
			}
 
			info, err := os.Stat(file)
			if err != nil {
				continue
			}
			sz := info.Size()
			if sz < 1*1024*1024 {
				lastSeen[file] = sz
				continue
			}
 
			newerExists := i < len(files)-1
			prev, hasPrev := lastSeen[file]
			sizeStable := hasPrev && prev == sz
			lastSeen[file] = sz
 
			if !newerExists && !sizeStable {
				continue
			}
			if !newerExists && sizeStable && !verifyMP4Readable(file) {
				continue
			}
 
			chunkNum := extractChunkNumber(file, filenamePrefix)
			if chunkNum == 0 {
				continue
			}
			state.mu.Lock()
			alreadyDone := false
			for _, c := range state.allChunks {
				if c.ChunkNumber == chunkNum {
					alreadyDone = true
					break
				}
			}
			if alreadyDone {
				state.processedFiles[file] = true
				state.mu.Unlock()
				continue
			}
			state.processedFiles[file] = true
			state.mu.Unlock()
 
			capturedFile := file
			capturedChunk := chunkNum
			capturedInfo := info
			capturedNewerExists := newerExists
			go func() {
				if !capturedNewerExists {
					time.Sleep(1 * time.Second)
				} else {
					analyzer.WaitForFileReady(ctx, capturedFile, cam.ID)
				}
				modTime := capturedInfo.ModTime()
				recStart := modTime.Add(-time.Duration(chunkTimeSec) * time.Second)
				stats := analyzer.Analyze(
					capturedFile, capturedChunk, cam.ID,
					recStart, modTime, time.Now(),
					chunkTimeSec, includeFrames,
				)
				if stats != nil {
					appendChunkStats(state, outDir, capturedChunk, cam.ID, stats)
					printFullAnalysis(stats, capturedChunk)
					if mqClient != nil {
						_ = mqClient.PublishChunk(cam.ID, mq.ChunkData{
							ChunkNumber:    capturedChunk,
							File:           filepath.Base(capturedFile),
							SizeMB:         stats.FileSizeMB,
							DurationSec:    stats.DurationSec,
							AvgBitrateKbps: stats.AvgBitrateKbps,
							FrameCount:     stats.TotalFrames,
						})
					}
				}
			}()
		}
	}
}
 
// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────
 
func printFullAnalysis(stats *analyzer.ChunkStats, chunkNum int) {
	if stats == nil {
		return
	}
	analyzer.PrintChunkReport(stats)
	analyzer.PrintFrameDetails(stats)
	analyzer.PrintFrameChanges(stats)
	analyzer.SaveFrameChangesReadable(stats, chunkNum)
	analyzer.SaveFrameChangesCSV(stats, chunkNum)
}
 
func appendChunkStats(state *cameraState, outDir string, chunkNum int, cameraID string, stats *analyzer.ChunkStats) {
	state.mu.Lock()
	state.allChunks = append(state.allChunks, stats)
	if stats.Index != nil {
		state.receivedIndices[chunkNum] = stats.Index
	}
	chunks := make([]*analyzer.ChunkStats, len(state.allChunks))
	copy(chunks, state.allChunks)
	state.mu.Unlock()
 
	// Use fileWriteMu to prevent concurrent writes
	if stats.Index != nil {
		saveChunkIndex(state, outDir, chunkNum, stats.Index)
	}
	saveSessionSummary(state, outDir, cameraID, chunks)
}
 
func pathToChunkNum(filePath string) int {
	base := strings.TrimSuffix(filepath.Base(filePath), ".mp4")
	parts := strings.Split(base, "_")
	if len(parts) < 2 {
		return 0
	}
	fileIndex, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 0
	}
	return fileIndex + 1
}
 
func extractFilePath(line string) string {
	idx := strings.Index(line, "location =")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(line[idx+len("location ="):])
	rest = strings.Trim(rest, "\"'")
	if end := strings.IndexAny(rest, " ,;\t\n"); end > 0 {
		rest = rest[:end]
	}
	if strings.HasSuffix(rest, ".mp4") {
		return rest
	}
	return ""
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
 
func saveChunkIndex(state *cameraState, outDir string, chunkNum int, index *analyzer.ChunkIndex) {
	state.fileWriteMu.Lock()
	defer state.fileWriteMu.Unlock()
	path := filepath.Join(outDir, fmt.Sprintf("chunk_%03d_index.json", chunkNum))
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
	fmt.Printf("[%s] Saved index: %s\n", ts(), filepath.Base(path))
}
 
type sessionSummary struct {
	CameraID    string                 `json:"camera_id"`
	UpdatedAt   string                 `json:"updated_at"`
	TotalChunks int                    `json:"total_chunks"`
	Chunks      []*analyzer.ChunkStats `json:"chunks"`
}
 
func saveSessionSummary(state *cameraState, outDir, cameraID string, chunks []*analyzer.ChunkStats) {
	state.fileWriteMu.Lock()
	defer state.fileWriteMu.Unlock()
	summary := sessionSummary{
		CameraID:    cameraID,
		UpdatedAt:   time.Now().Format(time.RFC3339),
		TotalChunks: len(chunks),
		Chunks:      chunks,
	}
	path := filepath.Join(outDir, "session_summary.json")
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}
 
func logPfx(cam config.CameraConfig) string {
	return fmt.Sprintf("[%s][%s][%s]", ts(), cam.ID, cam.Name)
}
 
func ts() string {
	return time.Now().Format("15:04:05")
}
 




//almost working
// package receiver

// import (
// 	"bufio"
// 	"context"
// 	"encoding/json"
// 	"fmt"
// 	"io"
// 	"net"
// 	"os"
// 	"os/exec"
// 	"path/filepath"
// 	"sort"
// 	"strconv"
// 	"strings"
// 	"sync"
// 	"time"

// 	"camera-system/internal/analyzer"
// 	"camera-system/internal/config"
// 	"camera-system/internal/mq"
// )

// // ─────────────────────────────────────────────────────────────────────────────
// // Per-camera in-memory state (keyed by outDir)
// // ─────────────────────────────────────────────────────────────────────────────

// type cameraState struct {
// 	mu              sync.Mutex
// 	chunkStartTimes map[int]time.Time
// 	currentChunkNum int
// 	processedFiles  map[string]bool
// 	allChunks       []*analyzer.ChunkStats
// 	receivedIndices map[int]*analyzer.ChunkIndex
// 	fileWriteMu     sync.Mutex // protects session_summary.json and chunk_*_index.json writes
// }

// func newCameraState() *cameraState {
// 	return &cameraState{
// 		chunkStartTimes: make(map[int]time.Time),
// 		processedFiles:  make(map[string]bool),
// 		receivedIndices: make(map[int]*analyzer.ChunkIndex),
// 	}
// }

// var globalStateMu sync.RWMutex
// var globalStates = make(map[string]*cameraState)

// // RegisterCamera pre-creates state for an output directory (called at startup).
// func RegisterCamera(outDir string) {
// 	globalStateMu.Lock()
// 	defer globalStateMu.Unlock()
// 	if _, ok := globalStates[outDir]; !ok {
// 		globalStates[outDir] = newCameraState()
// 	}
// }

// func getOrCreateState(outDir string) *cameraState {
// 	globalStateMu.RLock()
// 	state, ok := globalStates[outDir]
// 	globalStateMu.RUnlock()
// 	if ok {
// 		return state
// 	}
// 	globalStateMu.Lock()
// 	defer globalStateMu.Unlock()
// 	if state, ok = globalStates[outDir]; ok {
// 		return state
// 	}
// 	state = newCameraState()
// 	globalStates[outDir] = state
// 	return state
// }

// // GetChunks returns the chunk list for any outDir (used by API).
// func GetChunks(outDir string) []*analyzer.ChunkStats {
// 	globalStateMu.RLock()
// 	state, ok := globalStates[outDir]
// 	globalStateMu.RUnlock()
// 	if !ok {
// 		return nil
// 	}
// 	state.mu.Lock()
// 	defer state.mu.Unlock()
// 	result := make([]*analyzer.ChunkStats, len(state.allChunks))
// 	copy(result, state.allChunks)
// 	return result
// }

// // GetChunkIndex returns the index for a specific chunk number.
// func GetChunkIndex(outDir string, chunkNum int) *analyzer.ChunkIndex {
// 	globalStateMu.RLock()
// 	state, ok := globalStates[outDir]
// 	globalStateMu.RUnlock()
// 	if !ok {
// 		return nil
// 	}
// 	state.mu.Lock()
// 	defer state.mu.Unlock()
// 	return state.receivedIndices[chunkNum]
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // Port availability check
// // ─────────────────────────────────────────────────────────────────────────────

// func isPortAvailable(port int) bool {
// 	addr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
// 	conn, err := net.ListenUDP("udp", addr)
// 	if err != nil {
// 		return false
// 	}
// 	conn.Close()
// 	return true
// }

// func waitForPort(ctx context.Context, port int, maxWait time.Duration) bool {
// 	deadline := time.Now().Add(maxWait)
// 	for time.Now().Before(deadline) {
// 		select {
// 		case <-ctx.Done():
// 			return false
// 		default:
// 		}
// 		if isPortAvailable(port) {
// 			return true
// 		}
// 		time.Sleep(500 * time.Millisecond)
// 	}
// 	return false
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // Run — starts a GStreamer receiver pipeline for one camera session.
// // outDir is passed explicitly (the same across restarts when using session timestamp).
// // ─────────────────────────────────────────────────────────────────────────────

// func Run(ctx context.Context, cfg *config.Config, cam config.CameraConfig, outDir string, mqClient *mq.Client) error {
// 	chunkTimeSec := cfg.Server.ChunkTime
// 	encoder := cfg.Server.Encoder
// 	logPrefix := logPfx(cam)

// 	if err := os.MkdirAll(outDir, 0755); err != nil {
// 		return fmt.Errorf("cannot create output dir %q: %w", outDir, err)
// 	}

// 	port := cfg.SRTPort(cam)

// 	// Wait up to 15 s for the port to be released from the previous run
// 	if !isPortAvailable(port) {
// 		fmt.Printf("%s Port %d busy, waiting up to 15s...\n", logPrefix, port)
// 		if !waitForPort(ctx, port, 15*time.Second) {
// 			return fmt.Errorf("port %d still in use after waiting", port)
// 		}
// 		fmt.Printf("%s Port %d is now free\n", logPrefix, port)
// 	}

// 	state := getOrCreateState(outDir)
// 	chunkPattern := filepath.Join(outDir, "chunk_%05d.mp4")

// 	var parserElem string
// 	switch strings.ToLower(encoder) {
// 	case "h265", "hevc":
// 		parserElem = "h265parse"
// 	default:
// 		parserElem = "h264parse"
// 	}

// 	srtURI := fmt.Sprintf("srt://0.0.0.0:%d?mode=listener&latency=200&timeout=10000000&transtype=live", port)
// 	pipeline := fmt.Sprintf(
// 		"gst-launch-1.0 -e -v "+
// 			"srtsrc uri=\"%s\" ! "+
// 			"queue max-size-buffers=200 leaky=downstream ! "+
// 			"tsdemux ! %s ! "+
// 			"splitmuxsink location=\"%s\" max-size-time=%d000000000 muxer=mp4mux",
// 		srtURI,
// 		parserElem,
// 		chunkPattern,
// 		chunkTimeSec,
// 	)

// 	fmt.Printf("%s Starting GStreamer receiver\n", logPrefix)
// 	fmt.Printf("%s SRT port : %d\n", logPrefix, port)
// 	fmt.Printf("%s Output   : %s\n", logPrefix, outDir)
// 	fmt.Printf("%s Chunks   : %ds each\n", logPrefix, chunkTimeSec)
// 	fmt.Printf("%s Pipeline : %s\n\n", logPrefix, pipeline)

// 	cmd := exec.Command("bash", "-c", pipeline)
// 	cmd.Env = os.Environ()

// 	stdout, err := cmd.StdoutPipe()
// 	if err != nil {
// 		return fmt.Errorf("stdout pipe: %w", err)
// 	}
// 	stderr, err := cmd.StderrPipe()
// 	if err != nil {
// 		return fmt.Errorf("stderr pipe: %w", err)
// 	}

// 	if err := cmd.Start(); err != nil {
// 		return fmt.Errorf("failed to start GStreamer receiver: %w", err)
// 	}
// 	fmt.Printf("%s GStreamer receiver started (PID %d)\n", logPrefix, cmd.Process.Pid)

// 	const prefix = "chunk"

// 	// Create a context for the monitor goroutines
// 	monitorCtx, cancelMonitors := context.WithCancel(ctx)
// 	var wg sync.WaitGroup

// 	wg.Add(1)
// 	go func() {
// 		defer wg.Done()
// 		monitorGStreamer(monitorCtx, stdout, cam, outDir, prefix, chunkTimeSec, state, cfg.Server.FrameDetail, mqClient)
// 	}()
// 	wg.Add(1)
// 	go func() {
// 		defer wg.Done()
// 		monitorGStreamer(monitorCtx, stderr, cam, outDir, prefix, chunkTimeSec, state, cfg.Server.FrameDetail, mqClient)
// 	}()
// 	wg.Add(1)
// 	go func() {
// 		defer wg.Done()
// 		scanForFinalizedChunks(monitorCtx, outDir, prefix, cam, chunkTimeSec, state, cfg.Server.FrameDetail, mqClient)
// 	}()

// 	done := make(chan error, 1)
// 	go func() { done <- cmd.Wait() }()

// 	select {
// 	case err := <-done:
// 		cancelMonitors()
// 		wg.Wait()
// 		return err
// 	case <-ctx.Done():
// 		cmd.Process.Kill()
// 		<-done
// 		cancelMonitors()
// 		wg.Wait()
// 		return ctx.Err()
// 	}
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // RunWithRestart — outer loop; uses the same session folder across restarts.
// // ─────────────────────────────────────────────────────────────────────────────

// // RunWithRestart — outer loop; generates a NEW timestamped folder on each (re)start.
// func RunWithRestart(
// 	ctx context.Context,
// 	cfg *config.Config,
// 	cam config.CameraConfig,
// 	backoffMax time.Duration,
// 	mqClient *mq.Client,
// ) {
// 	logPrefix := logPfx(cam)
// 	backoff := 5 * time.Second

// 	for {
// 		select {
// 		case <-ctx.Done():
// 			fmt.Printf("%s Stopping receiver (shutdown)\n", logPrefix)
// 			return
// 		default:
// 		}

// 		// Generate a fresh session timestamp for this recording session
// 		sessionTS := time.Now().Format("20060102_150405")
// 		outDir := fmt.Sprintf("%s/%s/%s", cfg.Server.OutputDir, cam.ID, sessionTS)

// 		// Pre‑register so the API can serve this session’s data immediately
// 		RegisterCamera(outDir)

// 		fmt.Printf("%s Starting receiver... (session %s)\n", logPrefix, sessionTS)
// 		fmt.Printf("%s Recording → %s\n", logPrefix, outDir)
// 		start := time.Now()

// 		err := Run(ctx, cfg, cam, outDir, mqClient)
// 		elapsed := time.Since(start)

// 		if err != nil && err != context.Canceled {
// 			fmt.Printf("%s Receiver exited after %.1fs: %v\n", logPrefix, elapsed.Seconds(), err)
// 		} else if err == context.Canceled {
// 			fmt.Printf("%s Receiver stopped by command\n", logPrefix)
// 			return
// 		} else {
// 			fmt.Printf("%s Receiver exited cleanly after %.1fs\n", logPrefix, elapsed.Seconds())
// 		}

// 		if elapsed > 30*time.Second {
// 			backoff = 5 * time.Second
// 		}

// 		select {
// 		case <-ctx.Done():
// 			return
// 		case <-time.After(backoff):
// 		}

// 		backoff *= 2
// 		if backoff > backoffMax {
// 			backoff = backoffMax
// 		}
// 	}
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // monitorGStreamer — watches GStreamer logs for chunk boundary events
// // ─────────────────────────────────────────────────────────────────────────────

// func monitorGStreamer(
// 	ctx context.Context,
// 	pipe io.ReadCloser,
// 	cam config.CameraConfig,
// 	outDir, filenamePrefix string,
// 	chunkTimeSec int,
// 	state *cameraState,
// 	includeFrames bool,
// 	mqClient *mq.Client,
// ) {
// 	logPrefix := logPfx(cam)
// 	scanner := bufio.NewScanner(pipe)
// 	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

// 	connected := false
// 	firstChunkDetected := false

// 	// Channel to receive lines or context done
// 	lineCh := make(chan string)
// 	go func() {
// 		for scanner.Scan() {
// 			select {
// 			case <-ctx.Done():
// 				return
// 			case lineCh <- scanner.Text():
// 			}
// 		}
// 	}()

// 	for {
// 		select {
// 		case <-ctx.Done():
// 			return
// 		case line, ok := <-lineCh:
// 			if !ok {
// 				return
// 			}
// 			lineLower := strings.ToLower(line)

// 			if strings.Contains(line, "max-size-buffers") ||
// 				strings.Contains(line, "caps =") ||
// 				strings.Contains(line, "GstProxyPad") ||
// 				strings.Contains(line, "GstGhostPad") {
// 				continue
// 			}

// 			if strings.Contains(line, "ERROR:") && !strings.Contains(line, "preroll") {
// 				fmt.Printf("%s GST ERROR: %s\n", logPrefix, line)
// 			}

// 			if !connected && strings.Contains(line, "PLAYING") {
// 				connected = true
// 				fmt.Printf("\n%s Pipeline PLAYING — waiting for sender to connect...\n", logPrefix)
// 			}

// 			if strings.Contains(line, "location =") && strings.Contains(line, ".mp4") {
// 				chunkFile := extractFilePath(line)
// 				if chunkFile == "" {
// 					continue
// 				}
// 				logicalChunk := pathToChunkNum(chunkFile)
// 				if logicalChunk == 0 {
// 					continue
// 				}

// 				if !firstChunkDetected {
// 					firstChunkDetected = true
// 					fmt.Printf("\n%s First chunk detected: %s\n", logPrefix, filepath.Base(chunkFile))
// 				}

// 				startTime := time.Now()
// 				state.mu.Lock()
// 				state.chunkStartTimes[logicalChunk] = startTime
// 				prevChunkNum := state.currentChunkNum
// 				state.currentChunkNum = logicalChunk
// 				state.mu.Unlock()

// 				fmt.Printf("\n%s CHUNK %d STARTED → %s\n",
// 					logPrefix, logicalChunk, filepath.Base(chunkFile))

// 				if prevChunkNum > 0 && prevChunkNum < logicalChunk {
// 					prevFileIndex := prevChunkNum - 1
// 					prevChunkFile := filepath.Join(outDir,
// 						fmt.Sprintf("%s_%05d.mp4", filenamePrefix, prevFileIndex))

// 					state.mu.Lock()
// 					prevStart, hasPrevStart := state.chunkStartTimes[prevChunkNum]
// 					state.mu.Unlock()

// 					if hasPrevStart {
// 						capturedChunkNum := prevChunkNum
// 						capturedStart := prevStart
// 						capturedEnd := startTime
// 						capturedFile := prevChunkFile
// 						go func() {
// 							state.mu.Lock()
// 							state.processedFiles[capturedFile] = true
// 							state.mu.Unlock()

// 							analyzer.WaitForFileReady(capturedFile, cam.ID)
// 							stats := analyzer.Analyze(
// 								capturedFile, capturedChunkNum, cam.ID,
// 								capturedStart, capturedEnd, time.Now(),
// 								chunkTimeSec, includeFrames,
// 							)
// 							if stats != nil {
// 								appendChunkStats(state, outDir, capturedChunkNum, cam.ID, stats)
// 								printFullAnalysis(stats, capturedChunkNum)
// 								if mqClient != nil {
// 									_ = mqClient.PublishChunk(cam.ID, mq.ChunkData{
// 										ChunkNumber:    capturedChunkNum,
// 										File:           filepath.Base(capturedFile),
// 										SizeMB:         stats.FileSizeMB,
// 										DurationSec:    stats.DurationSec,
// 										AvgBitrateKbps: stats.AvgBitrateKbps,
// 										FrameCount:     stats.TotalFrames,
// 									})
// 								}
// 							}
// 						}()
// 					}
// 				}
// 			}

// 			if strings.Contains(lineLower, "finalized") ||
// 				(strings.Contains(lineLower, "closing") && strings.Contains(line, ".mp4")) {

// 				state.mu.Lock()
// 				cn := state.currentChunkNum
// 				startTime, exists := state.chunkStartTimes[cn]
// 				state.mu.Unlock()

// 				if cn == 0 || !exists {
// 					continue
// 				}

// 				endTime := time.Now()
// 				fileIndex := cn - 1
// 				chunkFile := filepath.Join(outDir,
// 					fmt.Sprintf("%s_%05d.mp4", filenamePrefix, fileIndex))

// 				fmt.Printf("%s CHUNK %d FINALIZED → %s\n", logPrefix, cn, filepath.Base(chunkFile))

// 				capturedChunkNum := cn
// 				capturedStart := startTime
// 				capturedEnd := endTime
// 				capturedFile := chunkFile
// 				go func() {
// 					state.mu.Lock()
// 					alreadyProcessed := state.processedFiles[capturedFile]
// 					state.mu.Unlock()
// 					if alreadyProcessed {
// 						return
// 					}
// 					state.mu.Lock()
// 					state.processedFiles[capturedFile] = true
// 					state.mu.Unlock()

// 					analyzer.WaitForFileReady(capturedFile, cam.ID)
// 					stats := analyzer.Analyze(
// 						capturedFile, capturedChunkNum, cam.ID,
// 						capturedStart, capturedEnd, time.Now(),
// 						chunkTimeSec, includeFrames,
// 					)
// 					if stats != nil {
// 						appendChunkStats(state, outDir, capturedChunkNum, cam.ID, stats)
// 						printFullAnalysis(stats, capturedChunkNum)
// 						if mqClient != nil {
// 							_ = mqClient.PublishChunk(cam.ID, mq.ChunkData{
// 								ChunkNumber:    capturedChunkNum,
// 								File:           filepath.Base(capturedFile),
// 								SizeMB:         stats.FileSizeMB,
// 								DurationSec:    stats.DurationSec,
// 								AvgBitrateKbps: stats.AvgBitrateKbps,
// 								FrameCount:     stats.TotalFrames,
// 							})
// 						}
// 					}
// 				}()
// 			}
// 		}
// 	}
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // scanForFinalizedChunks — fallback poller in case GStreamer events are missed
// // ─────────────────────────────────────────────────────────────────────────────

// func scanForFinalizedChunks(
// 	ctx context.Context,
// 	outDir, filenamePrefix string,
// 	cam config.CameraConfig,
// 	chunkTimeSec int,
// 	state *cameraState,
// 	includeFrames bool,
// 	mqClient *mq.Client,
// ) {
// 	ticker := time.NewTicker(8 * time.Second)
// 	defer ticker.Stop()

// 	lastSeen := make(map[string]int64)

// 	for {
// 		select {
// 		case <-ctx.Done():
// 			return
// 		case <-ticker.C:
// 		}

// 		pattern := filepath.Join(outDir, fmt.Sprintf("%s_*.mp4", filenamePrefix))
// 		files, err := filepath.Glob(pattern)
// 		if err != nil || len(files) == 0 {
// 			continue
// 		}
// 		sort.Strings(files)

// 		for i, file := range files {
// 			state.mu.Lock()
// 			alreadyProcessed := state.processedFiles[file]
// 			state.mu.Unlock()
// 			if alreadyProcessed {
// 				continue
// 			}

// 			info, err := os.Stat(file)
// 			if err != nil {
// 				continue
// 			}
// 			sz := info.Size()
// 			if sz < 1*1024*1024 {
// 				lastSeen[file] = sz
// 				continue
// 			}

// 			newerExists := i < len(files)-1
// 			prev, hasPrev := lastSeen[file]
// 			sizeStable := hasPrev && prev == sz
// 			lastSeen[file] = sz

// 			if !newerExists && !sizeStable {
// 				continue
// 			}
// 			if !newerExists && sizeStable && !verifyMP4Readable(file) {
// 				continue
// 			}

// 			chunkNum := extractChunkNumber(file, filenamePrefix)
// 			if chunkNum == 0 {
// 				continue
// 			}
// 			state.mu.Lock()
// 			alreadyDone := false
// 			for _, c := range state.allChunks {
// 				if c.ChunkNumber == chunkNum {
// 					alreadyDone = true
// 					break
// 				}
// 			}
// 			if alreadyDone {
// 				state.processedFiles[file] = true
// 				state.mu.Unlock()
// 				continue
// 			}
// 			state.processedFiles[file] = true
// 			state.mu.Unlock()

// 			capturedFile := file
// 			capturedChunk := chunkNum
// 			capturedInfo := info
// 			capturedNewerExists := newerExists
// 			go func() {
// 				if !capturedNewerExists {
// 					time.Sleep(1 * time.Second)
// 				} else {
// 					analyzer.WaitForFileReady(capturedFile, cam.ID)
// 				}
// 				modTime := capturedInfo.ModTime()
// 				recStart := modTime.Add(-time.Duration(chunkTimeSec) * time.Second)
// 				stats := analyzer.Analyze(
// 					capturedFile, capturedChunk, cam.ID,
// 					recStart, modTime, time.Now(),
// 					chunkTimeSec, includeFrames,
// 				)
// 				if stats != nil {
// 					appendChunkStats(state, outDir, capturedChunk, cam.ID, stats)
// 					printFullAnalysis(stats, capturedChunk)
// 					if mqClient != nil {
// 						_ = mqClient.PublishChunk(cam.ID, mq.ChunkData{
// 							ChunkNumber:    capturedChunk,
// 							File:           filepath.Base(capturedFile),
// 							SizeMB:         stats.FileSizeMB,
// 							DurationSec:    stats.DurationSec,
// 							AvgBitrateKbps: stats.AvgBitrateKbps,
// 							FrameCount:     stats.TotalFrames,
// 						})
// 					}
// 				}
// 			}()
// 		}
// 	}
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // Helpers
// // ─────────────────────────────────────────────────────────────────────────────

// func printFullAnalysis(stats *analyzer.ChunkStats, chunkNum int) {
// 	if stats == nil {
// 		return
// 	}
// 	analyzer.PrintChunkReport(stats)
// 	analyzer.PrintFrameDetails(stats)
// 	analyzer.PrintFrameChanges(stats)
// 	analyzer.SaveFrameChangesReadable(stats, chunkNum)
// 	analyzer.SaveFrameChangesCSV(stats, chunkNum)
// }

// func appendChunkStats(state *cameraState, outDir string, chunkNum int, cameraID string, stats *analyzer.ChunkStats) {
// 	state.mu.Lock()
// 	state.allChunks = append(state.allChunks, stats)
// 	if stats.Index != nil {
// 		state.receivedIndices[chunkNum] = stats.Index
// 	}
// 	chunks := make([]*analyzer.ChunkStats, len(state.allChunks))
// 	copy(chunks, state.allChunks)
// 	state.mu.Unlock()

// 	// Use fileWriteMu to prevent concurrent writes
// 	if stats.Index != nil {
// 		saveChunkIndex(state, outDir, chunkNum, stats.Index)
// 	}
// 	saveSessionSummary(state, outDir, cameraID, chunks)
// }

// func pathToChunkNum(filePath string) int {
// 	base := strings.TrimSuffix(filepath.Base(filePath), ".mp4")
// 	parts := strings.Split(base, "_")
// 	if len(parts) < 2 {
// 		return 0
// 	}
// 	fileIndex, err := strconv.Atoi(parts[len(parts)-1])
// 	if err != nil {
// 		return 0
// 	}
// 	return fileIndex + 1
// }

// func extractFilePath(line string) string {
// 	idx := strings.Index(line, "location =")
// 	if idx < 0 {
// 		return ""
// 	}
// 	rest := strings.TrimSpace(line[idx+len("location ="):])
// 	rest = strings.Trim(rest, "\"'")
// 	if end := strings.IndexAny(rest, " ,;\t\n"); end > 0 {
// 		rest = rest[:end]
// 	}
// 	if strings.HasSuffix(rest, ".mp4") {
// 		return rest
// 	}
// 	return ""
// }

// func extractChunkNumber(filePath, filenamePrefix string) int {
// 	base := filepath.Base(filePath)
// 	base = strings.TrimSuffix(base, ".mp4")
// 	base = strings.TrimPrefix(base, filenamePrefix+"_")
// 	fileIndex, err := strconv.Atoi(base)
// 	if err != nil {
// 		return 0
// 	}
// 	return fileIndex + 1
// }

// func verifyMP4Readable(filePath string) bool {
// 	cmd := exec.Command("ffprobe",
// 		"-v", "error",
// 		"-select_streams", "v:0",
// 		"-show_entries", "stream=codec_name",
// 		"-of", "default=noprint_wrappers=1",
// 		filePath)
// 	out, err := cmd.Output()
// 	if err != nil {
// 		return false
// 	}
// 	return len(strings.TrimSpace(string(out))) > 0
// }

// func saveChunkIndex(state *cameraState, outDir string, chunkNum int, index *analyzer.ChunkIndex) {
// 	state.fileWriteMu.Lock()
// 	defer state.fileWriteMu.Unlock()
// 	path := filepath.Join(outDir, fmt.Sprintf("chunk_%03d_index.json", chunkNum))
// 	data, err := json.MarshalIndent(index, "", "  ")
// 	if err != nil {
// 		return
// 	}
// 	_ = os.WriteFile(path, data, 0644)
// 	fmt.Printf("[%s] Saved index: %s\n", ts(), filepath.Base(path))
// }

// type sessionSummary struct {
// 	CameraID    string                 `json:"camera_id"`
// 	UpdatedAt   string                 `json:"updated_at"`
// 	TotalChunks int                    `json:"total_chunks"`
// 	Chunks      []*analyzer.ChunkStats `json:"chunks"`
// }

// func saveSessionSummary(state *cameraState, outDir, cameraID string, chunks []*analyzer.ChunkStats) {
// 	state.fileWriteMu.Lock()
// 	defer state.fileWriteMu.Unlock()
// 	summary := sessionSummary{
// 		CameraID:    cameraID,
// 		UpdatedAt:   time.Now().Format(time.RFC3339),
// 		TotalChunks: len(chunks),
// 		Chunks:      chunks,
// 	}
// 	path := filepath.Join(outDir, "session_summary.json")
// 	data, err := json.MarshalIndent(summary, "", "  ")
// 	if err != nil {
// 		return
// 	}
// 	_ = os.WriteFile(path, data, 0644)
// }

// func logPfx(cam config.CameraConfig) string {
// 	return fmt.Sprintf("[%s][%s][%s]", ts(), cam.ID, cam.Name)
// }

// func ts() string {
// 	return time.Now().Format("15:04:05")
// }








// package receiver

// import (
// 	"bufio"
// 	"context"
// 	"encoding/json"
// 	"fmt"
// 	"io"
// 	"net"
// 	"os"
// 	"os/exec"
// 	"path/filepath"
// 	"sort"
// 	"strconv"
// 	"strings"
// 	"sync"
// 	"time"

// 	"camera-system/internal/analyzer"
// 	"camera-system/internal/config"
// 	"camera-system/internal/mq"
// )

// // ─────────────────────────────────────────────────────────────────────────────
// // Per-camera in-memory state (keyed by outDir so each session gets its own)
// // ─────────────────────────────────────────────────────────────────────────────

// type cameraState struct {
// 	mu              sync.Mutex
// 	chunkStartTimes map[int]time.Time
// 	currentChunkNum int
// 	processedFiles  map[string]bool
// 	allChunks       []*analyzer.ChunkStats
// 	receivedIndices map[int]*analyzer.ChunkIndex
// }

// func newCameraState() *cameraState {
// 	return &cameraState{
// 		chunkStartTimes: make(map[int]time.Time),
// 		processedFiles:  make(map[string]bool),
// 		receivedIndices: make(map[int]*analyzer.ChunkIndex),
// 	}
// }

// var globalStateMu sync.RWMutex
// var globalStates = make(map[string]*cameraState)

// // RegisterCamera pre-creates state for an output directory (called at startup).
// func RegisterCamera(outDir string) {
// 	globalStateMu.Lock()
// 	defer globalStateMu.Unlock()
// 	if _, ok := globalStates[outDir]; !ok {
// 		globalStates[outDir] = newCameraState()
// 	}
// }

// func getOrCreateState(outDir string) *cameraState {
// 	globalStateMu.RLock()
// 	state, ok := globalStates[outDir]
// 	globalStateMu.RUnlock()
// 	if ok {
// 		return state
// 	}
// 	globalStateMu.Lock()
// 	defer globalStateMu.Unlock()
// 	if state, ok = globalStates[outDir]; ok {
// 		return state
// 	}
// 	state = newCameraState()
// 	globalStates[outDir] = state
// 	return state
// }

// // GetChunks returns the chunk list for any outDir (used by API).
// func GetChunks(outDir string) []*analyzer.ChunkStats {
// 	globalStateMu.RLock()
// 	state, ok := globalStates[outDir]
// 	globalStateMu.RUnlock()
// 	if !ok {
// 		return nil
// 	}
// 	state.mu.Lock()
// 	defer state.mu.Unlock()
// 	result := make([]*analyzer.ChunkStats, len(state.allChunks))
// 	copy(result, state.allChunks)
// 	return result
// }

// // GetChunkIndex returns the index for a specific chunk number.
// func GetChunkIndex(outDir string, chunkNum int) *analyzer.ChunkIndex {
// 	globalStateMu.RLock()
// 	state, ok := globalStates[outDir]
// 	globalStateMu.RUnlock()
// 	if !ok {
// 		return nil
// 	}
// 	state.mu.Lock()
// 	defer state.mu.Unlock()
// 	return state.receivedIndices[chunkNum]
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // Port availability check
// // ─────────────────────────────────────────────────────────────────────────────

// func isPortAvailable(port int) bool {
// 	addr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
// 	conn, err := net.ListenUDP("udp", addr)
// 	if err != nil {
// 		return false
// 	}
// 	conn.Close()
// 	return true
// }

// // waitForPort retries until the port is free or the context is cancelled.
// func waitForPort(ctx context.Context, port int, maxWait time.Duration) bool {
// 	deadline := time.Now().Add(maxWait)
// 	for time.Now().Before(deadline) {
// 		select {
// 		case <-ctx.Done():
// 			return false
// 		default:
// 		}
// 		if isPortAvailable(port) {
// 			return true
// 		}
// 		time.Sleep(500 * time.Millisecond)
// 	}
// 	return false
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // Run — starts a GStreamer receiver pipeline for one camera session.
// //
// // outDir is passed explicitly so each restart uses its own timestamped folder.
// // ─────────────────────────────────────────────────────────────────────────────

// func Run(ctx context.Context, cfg *config.Config, cam config.CameraConfig, outDir string, mqClient *mq.Client) error {
// 	chunkTimeSec := cfg.Server.ChunkTime
// 	encoder := cfg.Server.Encoder
// 	logPrefix := logPfx(cam)

// 	if err := os.MkdirAll(outDir, 0755); err != nil {
// 		return fmt.Errorf("cannot create output dir %q: %w", outDir, err)
// 	}

// 	port := cfg.SRTPort(cam)

// 	// Wait up to 15 s for the port to be released from the previous run
// 	if !isPortAvailable(port) {
// 		fmt.Printf("%s Port %d busy, waiting up to 15s...\n", logPrefix, port)
// 		if !waitForPort(ctx, port, 15*time.Second) {
// 			return fmt.Errorf("port %d still in use after waiting", port)
// 		}
// 		fmt.Printf("%s Port %d is now free\n", logPrefix, port)
// 	}

// 	state := getOrCreateState(outDir)
// 	chunkPattern := filepath.Join(outDir, "chunk_%05d.mp4")

// 	var parserElem string
// 	switch strings.ToLower(encoder) {
// 	case "h265", "hevc":
// 		parserElem = "h265parse"
// 	default:
// 		parserElem = "h264parse"
// 	}

// 	srtURI := fmt.Sprintf("srt://0.0.0.0:%d?mode=listener&latency=200&timeout=10000000&transtype=live", port)
// 	pipeline := fmt.Sprintf(
// 		"gst-launch-1.0 -e -v "+
// 			"srtsrc uri=\"%s\" ! "+
// 			"queue max-size-buffers=200 leaky=downstream ! "+
// 			"tsdemux ! %s ! "+
// 			"splitmuxsink location=\"%s\" max-size-time=%d000000000 muxer=mp4mux",
// 		srtURI,
// 		parserElem,
// 		chunkPattern,
// 		chunkTimeSec,
// 	)

// 	fmt.Printf("%s Starting GStreamer receiver\n", logPrefix)
// 	fmt.Printf("%s SRT port : %d\n", logPrefix, port)
// 	fmt.Printf("%s Output   : %s\n", logPrefix, outDir)
// 	fmt.Printf("%s Chunks   : %ds each\n", logPrefix, chunkTimeSec)
// 	fmt.Printf("%s Pipeline : %s\n\n", logPrefix, pipeline)

// 	cmd := exec.Command("bash", "-c", pipeline)
// 	cmd.Env = os.Environ()

// 	stdout, err := cmd.StdoutPipe()
// 	if err != nil {
// 		return fmt.Errorf("stdout pipe: %w", err)
// 	}
// 	stderr, err := cmd.StderrPipe()
// 	if err != nil {
// 		return fmt.Errorf("stderr pipe: %w", err)
// 	}

// 	if err := cmd.Start(); err != nil {
// 		return fmt.Errorf("failed to start GStreamer receiver: %w", err)
// 	}
// 	fmt.Printf("%s GStreamer receiver started (PID %d)\n", logPrefix, cmd.Process.Pid)

// 	const prefix = "chunk"
// 	go monitorGStreamer(stdout, cam, outDir, prefix, chunkTimeSec, state, cfg.Server.FrameDetail, mqClient)
// 	go monitorGStreamer(stderr, cam, outDir, prefix, chunkTimeSec, state, cfg.Server.FrameDetail, mqClient)
// 	go scanForFinalizedChunks(outDir, prefix, cam, chunkTimeSec, state, cfg.Server.FrameDetail, mqClient)

// 	done := make(chan error, 1)
// 	go func() { done <- cmd.Wait() }()

// 	select {
// 	case err := <-done:
// 		return err
// 	case <-ctx.Done():
// 		cmd.Process.Kill()
// 		<-done
// 		return ctx.Err()
// 	}
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // RunWithRestart — outer loop; creates a NEW timestamped folder on every start.
// // ─────────────────────────────────────────────────────────────────────────────

// func RunWithRestart(
// 	ctx context.Context,
// 	cfg *config.Config,
// 	cam config.CameraConfig,
// 	backoffMax time.Duration,
// 	mqClient *mq.Client,
// ) {
// 	logPrefix := logPfx(cam)
// 	backoff := 5 * time.Second

// 	for {
// 		select {
// 		case <-ctx.Done():
// 			fmt.Printf("%s Stopping receiver (shutdown)\n", logPrefix)
// 			return
// 		default:
// 		}

// 		// Generate a fresh session timestamp for this recording session.
// 		// This gives every restart its own folder, e.g.:
// 		//   recordings/camera1/20260402_154426/   ← first run
// 		//   recordings/camera1/20260402_155100/   ← after restart
// 		sessionTS := time.Now().Format("20060102_150405")
// 		outDir := fmt.Sprintf("%s/%s/%s", cfg.Server.OutputDir, cam.ID, sessionTS)

// 		// Pre-register so the API can serve this session's data immediately
// 		RegisterCamera(outDir)

// 		fmt.Printf("%s Starting receiver... (session %s)\n", logPrefix, sessionTS)
// 		fmt.Printf("%s Recording → %s\n", logPrefix, outDir)
// 		start := time.Now()

// 		err := Run(ctx, cfg, cam, outDir, mqClient)
// 		elapsed := time.Since(start)

// 		if err != nil && err != context.Canceled {
// 			fmt.Printf("%s Receiver exited after %.1fs: %v\n", logPrefix, elapsed.Seconds(), err)
// 		} else if err == context.Canceled {
// 			fmt.Printf("%s Receiver stopped by command\n", logPrefix)
// 			return
// 		} else {
// 			fmt.Printf("%s Receiver exited cleanly after %.1fs\n", logPrefix, elapsed.Seconds())
// 		}

// 		if elapsed > 30*time.Second {
// 			backoff = 5 * time.Second
// 		}

// 		select {
// 		case <-ctx.Done():
// 			return
// 		case <-time.After(backoff):
// 		}

// 		backoff *= 2
// 		if backoff > backoffMax {
// 			backoff = backoffMax
// 		}
// 	}
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // monitorGStreamer — watches GStreamer logs for chunk boundary events
// // ─────────────────────────────────────────────────────────────────────────────

// func monitorGStreamer(
// 	pipe io.ReadCloser,
// 	cam config.CameraConfig,
// 	outDir, filenamePrefix string,
// 	chunkTimeSec int,
// 	state *cameraState,
// 	includeFrames bool,
// 	mqClient *mq.Client,
// ) {
// 	logPrefix := logPfx(cam)
// 	scanner := bufio.NewScanner(pipe)
// 	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

// 	connected := false
// 	firstChunkDetected := false

// 	for scanner.Scan() {
// 		line := scanner.Text()
// 		lineLower := strings.ToLower(line)

// 		if strings.Contains(line, "max-size-buffers") ||
// 			strings.Contains(line, "caps =") ||
// 			strings.Contains(line, "GstProxyPad") ||
// 			strings.Contains(line, "GstGhostPad") {
// 			continue
// 		}

// 		if strings.Contains(line, "ERROR:") && !strings.Contains(line, "preroll") {
// 			fmt.Printf("%s GST ERROR: %s\n", logPrefix, line)
// 		}

// 		if !connected && strings.Contains(line, "PLAYING") {
// 			connected = true
// 			fmt.Printf("\n%s Pipeline PLAYING — waiting for sender to connect...\n", logPrefix)
// 		}

// 		if strings.Contains(line, "location =") && strings.Contains(line, ".mp4") {
// 			chunkFile := extractFilePath(line)
// 			if chunkFile == "" {
// 				continue
// 			}
// 			logicalChunk := pathToChunkNum(chunkFile)
// 			if logicalChunk == 0 {
// 				continue
// 			}

// 			if !firstChunkDetected {
// 				firstChunkDetected = true
// 				fmt.Printf("\n%s First chunk detected: %s\n", logPrefix, filepath.Base(chunkFile))
// 			}

// 			startTime := time.Now()
// 			state.mu.Lock()
// 			state.chunkStartTimes[logicalChunk] = startTime
// 			prevChunkNum := state.currentChunkNum
// 			state.currentChunkNum = logicalChunk
// 			state.mu.Unlock()

// 			fmt.Printf("\n%s CHUNK %d STARTED → %s\n",
// 				logPrefix, logicalChunk, filepath.Base(chunkFile))

// 			if prevChunkNum > 0 && prevChunkNum < logicalChunk {
// 				prevFileIndex := prevChunkNum - 1
// 				prevChunkFile := filepath.Join(outDir,
// 					fmt.Sprintf("%s_%05d.mp4", filenamePrefix, prevFileIndex))

// 				state.mu.Lock()
// 				prevStart, hasPrevStart := state.chunkStartTimes[prevChunkNum]
// 				state.mu.Unlock()

// 				if hasPrevStart {
// 					capturedChunkNum := prevChunkNum
// 					capturedStart := prevStart
// 					capturedEnd := startTime
// 					capturedFile := prevChunkFile
// 					go func() {
// 						state.mu.Lock()
// 						state.processedFiles[capturedFile] = true
// 						state.mu.Unlock()

// 						analyzer.WaitForFileReady(capturedFile, cam.ID)
// 						stats := analyzer.Analyze(
// 							capturedFile, capturedChunkNum, cam.ID,
// 							capturedStart, capturedEnd, time.Now(),
// 							chunkTimeSec, includeFrames,
// 						)
// 						if stats != nil {
// 							appendChunkStats(state, outDir, capturedChunkNum, cam.ID, stats)
// 							printFullAnalysis(stats, capturedChunkNum)
// 							if mqClient != nil {
// 								_ = mqClient.PublishChunk(cam.ID, mq.ChunkData{
// 									ChunkNumber:    capturedChunkNum,
// 									File:           filepath.Base(capturedFile),
// 									SizeMB:         stats.FileSizeMB,
// 									DurationSec:    stats.DurationSec,
// 									AvgBitrateKbps: stats.AvgBitrateKbps,
// 									FrameCount:     stats.TotalFrames,
// 								})
// 							}
// 						}
// 					}()
// 				}
// 			}
// 		}

// 		if strings.Contains(lineLower, "finalized") ||
// 			(strings.Contains(lineLower, "closing") && strings.Contains(line, ".mp4")) {

// 			state.mu.Lock()
// 			cn := state.currentChunkNum
// 			startTime, exists := state.chunkStartTimes[cn]
// 			state.mu.Unlock()

// 			if cn == 0 || !exists {
// 				continue
// 			}

// 			endTime := time.Now()
// 			fileIndex := cn - 1
// 			chunkFile := filepath.Join(outDir,
// 				fmt.Sprintf("%s_%05d.mp4", filenamePrefix, fileIndex))

// 			fmt.Printf("%s CHUNK %d FINALIZED → %s\n", logPrefix, cn, filepath.Base(chunkFile))

// 			capturedChunkNum := cn
// 			capturedStart := startTime
// 			capturedEnd := endTime
// 			capturedFile := chunkFile
// 			go func() {
// 				state.mu.Lock()
// 				alreadyProcessed := state.processedFiles[capturedFile]
// 				state.mu.Unlock()
// 				if alreadyProcessed {
// 					return
// 				}
// 				state.mu.Lock()
// 				state.processedFiles[capturedFile] = true
// 				state.mu.Unlock()

// 				analyzer.WaitForFileReady(capturedFile, cam.ID)
// 				stats := analyzer.Analyze(
// 					capturedFile, capturedChunkNum, cam.ID,
// 					capturedStart, capturedEnd, time.Now(),
// 					chunkTimeSec, includeFrames,
// 				)
// 				if stats != nil {
// 					appendChunkStats(state, outDir, capturedChunkNum, cam.ID, stats)
// 					printFullAnalysis(stats, capturedChunkNum)
// 					if mqClient != nil {
// 						_ = mqClient.PublishChunk(cam.ID, mq.ChunkData{
// 							ChunkNumber:    capturedChunkNum,
// 							File:           filepath.Base(capturedFile),
// 							SizeMB:         stats.FileSizeMB,
// 							DurationSec:    stats.DurationSec,
// 							AvgBitrateKbps: stats.AvgBitrateKbps,
// 							FrameCount:     stats.TotalFrames,
// 						})
// 					}
// 				}
// 			}()
// 		}
// 	}
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // scanForFinalizedChunks — fallback poller in case GStreamer events are missed
// // ─────────────────────────────────────────────────────────────────────────────

// func scanForFinalizedChunks(
// 	outDir, filenamePrefix string,
// 	cam config.CameraConfig,
// 	chunkTimeSec int,
// 	state *cameraState,
// 	includeFrames bool,
// 	mqClient *mq.Client,
// ) {
// 	ticker := time.NewTicker(8 * time.Second)
// 	defer ticker.Stop()

// 	lastSeen := make(map[string]int64)

// 	for range ticker.C {
// 		pattern := filepath.Join(outDir, fmt.Sprintf("%s_*.mp4", filenamePrefix))
// 		files, err := filepath.Glob(pattern)
// 		if err != nil || len(files) == 0 {
// 			continue
// 		}
// 		sort.Strings(files)

// 		for i, file := range files {
// 			state.mu.Lock()
// 			alreadyProcessed := state.processedFiles[file]
// 			state.mu.Unlock()
// 			if alreadyProcessed {
// 				continue
// 			}

// 			info, err := os.Stat(file)
// 			if err != nil {
// 				continue
// 			}
// 			sz := info.Size()
// 			if sz < 1*1024*1024 {
// 				lastSeen[file] = sz
// 				continue
// 			}

// 			newerExists := i < len(files)-1
// 			prev, hasPrev := lastSeen[file]
// 			sizeStable := hasPrev && prev == sz
// 			lastSeen[file] = sz

// 			if !newerExists && !sizeStable {
// 				continue
// 			}
// 			if !newerExists && sizeStable && !verifyMP4Readable(file) {
// 				continue
// 			}

// 			chunkNum := extractChunkNumber(file, filenamePrefix)
// 			if chunkNum == 0 {
// 				continue
// 			}
// 			state.mu.Lock()
// 			alreadyDone := false
// 			for _, c := range state.allChunks {
// 				if c.ChunkNumber == chunkNum {
// 					alreadyDone = true
// 					break
// 				}
// 			}
// 			if alreadyDone {
// 				state.processedFiles[file] = true
// 				state.mu.Unlock()
// 				continue
// 			}
// 			state.processedFiles[file] = true
// 			state.mu.Unlock()

// 			capturedFile := file
// 			capturedChunk := chunkNum
// 			capturedInfo := info
// 			capturedNewerExists := newerExists
// 			go func() {
// 				if !capturedNewerExists {
// 					time.Sleep(1 * time.Second)
// 				} else {
// 					analyzer.WaitForFileReady(capturedFile, cam.ID)
// 				}
// 				modTime := capturedInfo.ModTime()
// 				recStart := modTime.Add(-time.Duration(chunkTimeSec) * time.Second)
// 				stats := analyzer.Analyze(
// 					capturedFile, capturedChunk, cam.ID,
// 					recStart, modTime, time.Now(),
// 					chunkTimeSec, includeFrames,
// 				)
// 				if stats != nil {
// 					appendChunkStats(state, outDir, capturedChunk, cam.ID, stats)
// 					printFullAnalysis(stats, capturedChunk)
// 					if mqClient != nil {
// 						_ = mqClient.PublishChunk(cam.ID, mq.ChunkData{
// 							ChunkNumber:    capturedChunk,
// 							File:           filepath.Base(capturedFile),
// 							SizeMB:         stats.FileSizeMB,
// 							DurationSec:    stats.DurationSec,
// 							AvgBitrateKbps: stats.AvgBitrateKbps,
// 							FrameCount:     stats.TotalFrames,
// 						})
// 					}
// 				}
// 			}()
// 		}
// 	}
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // Helpers
// // ─────────────────────────────────────────────────────────────────────────────

// func printFullAnalysis(stats *analyzer.ChunkStats, chunkNum int) {
// 	if stats == nil {
// 		return
// 	}
// 	analyzer.PrintChunkReport(stats)
// 	analyzer.PrintFrameDetails(stats)
// 	analyzer.PrintFrameChanges(stats)
// 	analyzer.SaveFrameChangesReadable(stats, chunkNum)
// 	analyzer.SaveFrameChangesCSV(stats, chunkNum)
// }

// func appendChunkStats(state *cameraState, outDir string, chunkNum int, cameraID string, stats *analyzer.ChunkStats) {
// 	state.mu.Lock()
// 	state.allChunks = append(state.allChunks, stats)
// 	if stats.Index != nil {
// 		state.receivedIndices[chunkNum] = stats.Index
// 	}
// 	chunks := make([]*analyzer.ChunkStats, len(state.allChunks))
// 	copy(chunks, state.allChunks)
// 	state.mu.Unlock()

// 	if stats.Index != nil {
// 		saveChunkIndex(outDir, chunkNum, stats.Index)
// 	}
// 	saveSessionSummary(outDir, cameraID, chunks)
// }

// func pathToChunkNum(filePath string) int {
// 	base := strings.TrimSuffix(filepath.Base(filePath), ".mp4")
// 	parts := strings.Split(base, "_")
// 	if len(parts) < 2 {
// 		return 0
// 	}
// 	fileIndex, err := strconv.Atoi(parts[len(parts)-1])
// 	if err != nil {
// 		return 0
// 	}
// 	return fileIndex + 1
// }

// func extractFilePath(line string) string {
// 	idx := strings.Index(line, "location =")
// 	if idx < 0 {
// 		return ""
// 	}
// 	rest := strings.TrimSpace(line[idx+len("location ="):])
// 	rest = strings.Trim(rest, "\"'")
// 	if end := strings.IndexAny(rest, " ,;\t\n"); end > 0 {
// 		rest = rest[:end]
// 	}
// 	if strings.HasSuffix(rest, ".mp4") {
// 		return rest
// 	}
// 	return ""
// }

// func extractChunkNumber(filePath, filenamePrefix string) int {
// 	base := filepath.Base(filePath)
// 	base = strings.TrimSuffix(base, ".mp4")
// 	base = strings.TrimPrefix(base, filenamePrefix+"_")
// 	fileIndex, err := strconv.Atoi(base)
// 	if err != nil {
// 		return 0
// 	}
// 	return fileIndex + 1
// }

// func verifyMP4Readable(filePath string) bool {
// 	cmd := exec.Command("ffprobe",
// 		"-v", "error",
// 		"-select_streams", "v:0",
// 		"-show_entries", "stream=codec_name",
// 		"-of", "default=noprint_wrappers=1",
// 		filePath)
// 	out, err := cmd.Output()
// 	if err != nil {
// 		return false
// 	}
// 	return len(strings.TrimSpace(string(out))) > 0
// }

// func saveChunkIndex(outDir string, chunkNum int, index *analyzer.ChunkIndex) {
// 	path := filepath.Join(outDir, fmt.Sprintf("chunk_%03d_index.json", chunkNum))
// 	data, err := json.MarshalIndent(index, "", "  ")
// 	if err != nil {
// 		return
// 	}
// 	_ = os.WriteFile(path, data, 0644)
// 	fmt.Printf("[%s] Saved index: %s\n", ts(), filepath.Base(path))
// }

// type sessionSummary struct {
// 	CameraID    string                 `json:"camera_id"`
// 	UpdatedAt   string                 `json:"updated_at"`
// 	TotalChunks int                    `json:"total_chunks"`
// 	Chunks      []*analyzer.ChunkStats `json:"chunks"`
// }

// func saveSessionSummary(outDir, cameraID string, chunks []*analyzer.ChunkStats) {
// 	summary := sessionSummary{
// 		CameraID:    cameraID,
// 		UpdatedAt:   time.Now().Format(time.RFC3339),
// 		TotalChunks: len(chunks),
// 		Chunks:      chunks,
// 	}
// 	path := filepath.Join(outDir, "session_summary.json")
// 	data, err := json.MarshalIndent(summary, "", "  ")
// 	if err != nil {
// 		return
// 	}
// 	_ = os.WriteFile(path, data, 0644)
// }

// func logPfx(cam config.CameraConfig) string {
// 	return fmt.Sprintf("[%s][%s][%s]", ts(), cam.ID, cam.Name)
// }

// func ts() string {
// 	return time.Now().Format("15:04:05")
// }
