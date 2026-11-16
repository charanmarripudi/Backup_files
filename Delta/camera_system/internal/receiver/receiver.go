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

type cameraState struct {
    mu              sync.Mutex
    chunkStartTimes map[int]time.Time
    currentChunkNum int
    processedFiles  map[string]bool
    allChunks       []*analyzer.ChunkStats
    receivedIndices map[int]*analyzer.ChunkIndex
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

func isPortAvailable(port int) bool {
    addr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", port))
    conn, err := net.ListenUDP("udp", addr)
    if err != nil {
        return false
    }
    conn.Close()
    return true
}

func Run(ctx context.Context, cfg *config.Config, cam config.CameraConfig, mqClient *mq.Client) error {
    outDir := cfg.CameraOutputDir(cam)
    chunkTimeSec := cfg.Server.ChunkTime
    encoder := cfg.Server.Encoder
    logPrefix := logPfx(cam)

    if err := os.MkdirAll(outDir, 0755); err != nil {
        return fmt.Errorf("cannot create output dir %q: %w", outDir, err)
    }

    port := cfg.SRTPort(cam)
    if !isPortAvailable(port) {
        fmt.Printf("%s Port %d is in use\n", logPrefix, port)
        return fmt.Errorf("port %d is already in use", port)
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
    go monitorGStreamer(stdout, cam, outDir, prefix, chunkTimeSec, state, cfg.Server.FrameDetail, mqClient)
    go monitorGStreamer(stderr, cam, outDir, prefix, chunkTimeSec, state, cfg.Server.FrameDetail, mqClient)
    go scanForFinalizedChunks(outDir, prefix, cam, chunkTimeSec, state, cfg.Server.FrameDetail, mqClient)

    done := make(chan error, 1)
    go func() {
        done <- cmd.Wait()
    }()

    select {
    case err := <-done:
        return err
    case <-ctx.Done():
        cmd.Process.Kill()
        <-done
        return ctx.Err()
    }
}

func monitorGStreamer(
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

    for scanner.Scan() {
        line := scanner.Text()
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

                        analyzer.WaitForFileReady(capturedFile, cam.ID)
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

                analyzer.WaitForFileReady(capturedFile, cam.ID)
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

func scanForFinalizedChunks(
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

    for range ticker.C {
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
                    analyzer.WaitForFileReady(capturedFile, cam.ID)
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

// Helper functions

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

    if stats.Index != nil {
        saveChunkIndex(outDir, chunkNum, stats.Index)
    }
    saveSessionSummary(outDir, cameraID, chunks)
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

func saveChunkIndex(outDir string, chunkNum int, index *analyzer.ChunkIndex) {
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

func saveSessionSummary(outDir, cameraID string, chunks []*analyzer.ChunkStats) {
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

func RunWithRestart(ctx context.Context, cfg *config.Config, cam config.CameraConfig, backoffMax time.Duration, mqClient *mq.Client) {
    logPrefix := logPfx(cam)
    backoff := 5 * time.Second

    for {
        select {
        case <-ctx.Done():
            fmt.Printf("%s Stopping receiver (context done)\n", logPrefix)
            return
        default:
        }

        fmt.Printf("%s Starting receiver...\n", logPrefix)
        start := time.Now()
        err := Run(ctx, cfg, cam, mqClient)
        elapsed := time.Since(start)

        if err != nil && err != context.Canceled {
            fmt.Printf("%s Receiver exited after %.1fs: %v\n", logPrefix, elapsed.Seconds(), err)
        } else if err == context.Canceled {
            fmt.Printf("%s Receiver stopped by command\n", logPrefix)
            return
        } else {
            fmt.Printf("%s Receiver exited cleanly after %.1fs\n", logPrefix, elapsed.Seconds())
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

func logPfx(cam config.CameraConfig) string {
    return fmt.Sprintf("[%s][%s][%s]", ts(), cam.ID, cam.Name)
}

func ts() string {
    return time.Now().Format("15:04:05")
}





// // Package receiver runs the GStreamer pipeline that receives an SRT stream,
// // saves it as MP4 chunks, and triggers analysis on completed chunks.
// package receiver

// import (
// 	"bufio"
// 	"encoding/json"
// 	"fmt"
// 	"io"
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
// )

// // ─────────────────────────────────────────────────────────────────────────────
// // Per-camera state (shared between receiver pipeline and HTTP API)
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

// // Global registry — maps output directory → state.
// var globalStateMu sync.RWMutex
// var globalStates = make(map[string]*cameraState)

// // RegisterCamera must be called from main before starting receivers.
// func RegisterCamera(outDir string) {
// 	globalStateMu.Lock()
// 	defer globalStateMu.Unlock()
// 	if _, ok := globalStates[outDir]; !ok {
// 		globalStates[outDir] = newCameraState()
// 	}
// }

// // getOrCreateState returns the shared state for a camera output directory.
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

// // ─────────────────────────────────────────────────────────────────────────────
// // API-facing read functions (called by internal/api)
// // ─────────────────────────────────────────────────────────────────────────────

// // GetChunks returns all analyzed chunks for a camera output directory.
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

// // GetChunkIndex returns the frame index for a specific chunk number.
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
// // Main entry points
// // ─────────────────────────────────────────────────────────────────────────────

// // Run starts the GStreamer receiver pipeline for one camera and blocks
// // until the pipeline exits.
// func Run(cfg *config.Config, cam config.CameraConfig) error {
// 	outDir := cfg.CameraOutputDir(cam)
// 	chunkTimeSec := cfg.Server.ChunkTime
// 	encoder := cfg.Server.Encoder
// 	logPrefix := logPfx(cam)

// 	if err := os.MkdirAll(outDir, 0755); err != nil {
// 		return fmt.Errorf("cannot create output dir %q: %w", outDir, err)
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

// 	pipeline := fmt.Sprintf(
// 		"gst-launch-1.0 -e "+
// 			"srtsrc uri=\"%s\" ! "+
// 			"queue max-size-buffers=200 leaky=downstream ! "+
// 			"tsdemux ! %s ! "+
// 			"splitmuxsink location=\"%s\" max-size-time=%d000000000 muxer=mp4mux",
// 		cfg.SRTListenerURI(cam),
// 		parserElem,
// 		chunkPattern,
// 		chunkTimeSec,
// 	)

// 	fmt.Printf("%s Starting GStreamer receiver\n", logPrefix)
// 	fmt.Printf("%s SRT port : %d\n", logPrefix, cfg.SRTPort(cam))
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

// 	// Monitor GStreamer output on both stdout and stderr for chunk events
// 	const prefix = "chunk"
// 	go monitorGStreamer(stdout, cam, outDir, prefix, chunkTimeSec, state, cfg.Server.FrameDetail)
// 	go monitorGStreamer(stderr, cam, outDir, prefix, chunkTimeSec, state, cfg.Server.FrameDetail)

// 	// Safety fallback: periodically scan for finalized chunks that were missed
// 	go scanForFinalizedChunks(outDir, prefix, cam, chunkTimeSec, state, cfg.Server.FrameDetail)

// 	return cmd.Wait()
// }

// // RunWithRestart keeps calling Run and restarts it after any exit.
// // Uses exponential backoff up to backoffMax between restarts.
// func RunWithRestart(cfg *config.Config, cam config.CameraConfig, backoffMax time.Duration) {
// 	logPrefix := logPfx(cam)
// 	backoff := 5 * time.Second

// 	for {
// 		fmt.Printf("%s Starting receiver...\n", logPrefix)
// 		start := time.Now()

// 		err := Run(cfg, cam)
// 		elapsed := time.Since(start)

// 		if err != nil {
// 			fmt.Printf("%s Receiver exited after %.1fs: %v\n", logPrefix, elapsed.Seconds(), err)
// 		} else {
// 			fmt.Printf("%s Receiver exited cleanly after %.1fs\n", logPrefix, elapsed.Seconds())
// 		}

// 		// If ran >30s assume it was healthy; reset backoff
// 		if elapsed > 30*time.Second {
// 			backoff = 5 * time.Second
// 		}

// 		fmt.Printf("%s Restarting in %s...\n", logPrefix, backoff)
// 		time.Sleep(backoff)

// 		backoff *= 2
// 		if backoff > backoffMax {
// 			backoff = backoffMax
// 		}
// 	}
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // GStreamer output monitor
// // ─────────────────────────────────────────────────────────────────────────────

// func monitorGStreamer(
// 	pipe io.ReadCloser,
// 	cam config.CameraConfig,
// 	outDir, filenamePrefix string,
// 	chunkTimeSec int,
// 	state *cameraState,
// 	includeFrames bool,
// ) {
// 	logPrefix := logPfx(cam)
// 	scanner := bufio.NewScanner(pipe)
// 	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

// 	connected := false

// 	for scanner.Scan() {
// 		line := scanner.Text()
// 		lineLower := strings.ToLower(line)

// 		if strings.Contains(line, "ERROR:") {
// 			fmt.Printf("%s GST ERROR: %s\n", logPrefix, line)
// 		}

// 		if !connected && strings.Contains(line, "PLAYING") {
// 			connected = true
// 			fmt.Printf("\n%s Pipeline PLAYING — waiting for sender to connect...\n", logPrefix)
// 		}

// 		// Detect new chunk starting: GStreamer logs the file it just opened
// 		if strings.Contains(line, "location =") && strings.Contains(line, ".mp4") {
// 			chunkFile := extractFilePath(line)
// 			if chunkFile == "" {
// 				continue
// 			}
// 			logicalChunk := pathToChunkNum(chunkFile)
// 			if logicalChunk == 0 {
// 				continue
// 			}

// 			startTime := time.Now()
// 			state.mu.Lock()
// 			state.chunkStartTimes[logicalChunk] = startTime
// 			prevChunkNum := state.currentChunkNum
// 			state.currentChunkNum = logicalChunk
// 			state.mu.Unlock()

// 			fmt.Printf("\n%s CHUNK %d STARTED → %s\n",
// 				logPrefix, logicalChunk, filepath.Base(chunkFile))

// 			// When chunk N+1 starts, chunk N is closed — trigger analysis
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
// 							printFrameChanges(stats, capturedChunkNum)
// 						}
// 					}()
// 				}
// 			}
// 		}

// 		// Explicit finalization signal from GStreamer (splitmuxsink)
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
// 					printFrameChanges(stats, capturedChunkNum)
// 				}
// 			}()
// 		}
// 	}
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // Fallback scanner (in case GStreamer events are missed)
// // ─────────────────────────────────────────────────────────────────────────────

// func scanForFinalizedChunks(
// 	outDir, filenamePrefix string,
// 	cam config.CameraConfig,
// 	chunkTimeSec int,
// 	state *cameraState,
// 	includeFrames bool,
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

// 			// Check if already analyzed in memory
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
// 					printFrameChanges(stats, capturedChunk)
// 				}
// 			}()
// 		}
// 	}
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // Helpers
// // ─────────────────────────────────────────────────────────────────────────────

// // appendChunkStats adds a chunk to the in-memory state, saves the index and
// // session summary to disk.
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

// // pathToChunkNum extracts the logical chunk number (1-based) from an MP4 filename.
// // chunk_00000.mp4 → 1,  chunk_00001.mp4 → 2
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

// // printFrameChanges prints a detailed frame‑to‑frame analysis table for a chunk.
// func printFrameChanges(stats *analyzer.ChunkStats, chunkNum int) {
// 	if stats == nil || stats.Frames == nil || len(stats.Frames) == 0 {
// 		return
// 	}

// 	fmt.Println("\n" + strings.Repeat("═", 120))
// 	fmt.Printf(" FRAME-TO-FRAME CHANGE ANALYSIS (Chunk %d)\n", chunkNum)
// 	fmt.Println(strings.Repeat("═", 120))

// 	fmt.Printf("%-6s %-5s %-8s %-10s %-10s %-10s %-10s %-8s %-8s %-8s\n",
// 		"Frame", "Type", "Size(KB)", "Δ Size", "Δ%", "Bitrate", "Δ Bitrate", "PSNR", "Δ PSNR", "Time(ms)")
// 	fmt.Println(strings.Repeat("─", 120))

// 	var prevFrame *analyzer.FrameDetail
// 	frameCount := 0
// 	maxFramesToShow := 30

// 	for i := range stats.Frames {
// 		frame := &stats.Frames[i]
// 		if frameCount >= maxFramesToShow {
// 			fmt.Printf("\n... and %d more frames\n", len(stats.Frames)-maxFramesToShow)
// 			break
// 		}
// 		sizeKB := float64(frame.SizeBytes) / 1024
// 		timeMs := frame.Pts * 1000

// 		if prevFrame == nil {
// 			fmt.Printf("%-6d %-5s %-8.2f %-10s %-10s %-10.0f %-10s %-8.1f %-8s %-8.0f\n",
// 				frame.FrameNumber, frame.Type, sizeKB, "-", "-", frame.BitrateKbps, "-", frame.PSNR, "-", timeMs)
// 		} else {
// 			prevSizeKB := float64(prevFrame.SizeBytes) / 1024
// 			sizeChange := sizeKB - prevSizeKB
// 			sizeChangePct := 0.0
// 			if prevSizeKB > 0 {
// 				sizeChangePct = (sizeChange / prevSizeKB) * 100
// 			}
// 			bitrateChange := frame.BitrateKbps - prevFrame.BitrateKbps
// 			psnrChange := frame.PSNR - prevFrame.PSNR
// 			timeDelta := (frame.Pts - prevFrame.Pts) * 1000

// 			fmt.Printf("%-6d %-5s %-8.2f %-+10.2f %-+9.1f%% %-10.0f %-+10.0f %-8.1f %-+8.1f %-8.0f\n",
// 				frame.FrameNumber, frame.Type, sizeKB, sizeChange, sizeChangePct,
// 				frame.BitrateKbps, bitrateChange, frame.PSNR, psnrChange, timeDelta)
// 		}
// 		prevFrame = frame
// 		frameCount++
// 	}

// 	var peakBitrate float64
// 	var peakFrame int
// 	for _, frame := range stats.Frames {
// 		if frame.BitrateKbps > peakBitrate {
// 			peakBitrate = frame.BitrateKbps
// 			peakFrame = frame.FrameNumber
// 		}
// 	}

// 	fmt.Println("\n" + strings.Repeat("─", 120))
// 	fmt.Printf("Peak bitrate: %.0f kbps (Frame %d) | Total frames: %d\n", peakBitrate, peakFrame, len(stats.Frames))
// 	fmt.Println(strings.Repeat("═", 120) + "\n")
// }

// func logPfx(cam config.CameraConfig) string {
// 	return fmt.Sprintf("[%s][%s][%s]", ts(), cam.ID, cam.Name)
// }

// func ts() string {
// 	return time.Now().Format("15:04:05")
// }

// // Package receiver runs the GStreamer pipeline that receives an SRT stream,
// // saves it as MP4 chunks, and triggers analysis on completed chunks.
// package receiver

// import (
// 	"bufio"
// 	"encoding/json"
// 	"fmt"
// 	"io"
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
// )

// // ─────────────────────────────────────────────────────────────────────────────
// // Per-camera state (shared between receiver pipeline and HTTP API)
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

// // Global registry — maps output directory → state.
// // API reads from here; receiver writes here.
// var globalStateMu sync.RWMutex
// var globalStates = make(map[string]*cameraState)

// // RegisterCamera must be called from main before starting receivers.
// // It pre-creates the state so the API can serve requests even before
// // the first chunk arrives.
// func RegisterCamera(outDir string) {
// 	globalStateMu.Lock()
// 	defer globalStateMu.Unlock()
// 	if _, ok := globalStates[outDir]; !ok {
// 		globalStates[outDir] = newCameraState()
// 	}
// }

// // getOrCreateState returns the shared state for a camera output directory.
// func getOrCreateState(outDir string) *cameraState {
// 	globalStateMu.RLock()
// 	state, ok := globalStates[outDir]
// 	globalStateMu.RUnlock()
// 	if ok {
// 		return state
// 	}
// 	// Auto-create if RegisterCamera wasn't called
// 	globalStateMu.Lock()
// 	defer globalStateMu.Unlock()
// 	if state, ok = globalStates[outDir]; ok {
// 		return state
// 	}
// 	state = newCameraState()
// 	globalStates[outDir] = state
// 	return state
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // API-facing read functions (called by internal/api)
// // ─────────────────────────────────────────────────────────────────────────────

// // GetChunks returns all analyzed chunks for a camera output directory.
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

// // GetChunkIndex returns the frame index for a specific chunk number.
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
// // Main entry points
// // ─────────────────────────────────────────────────────────────────────────────

// // Run starts the GStreamer receiver pipeline for one camera and blocks
// // until the pipeline exits. It writes MP4 chunks and triggers analysis.
// func Run(cfg *config.Config, cam config.CameraConfig) error {
// 	outDir := cfg.CameraOutputDir(cam)
// 	chunkTimeSec := cfg.Server.ChunkTime
// 	encoder := cfg.Server.Encoder
// 	logPrefix := logPfx(cam)

// 	if err := os.MkdirAll(outDir, 0755); err != nil {
// 		return fmt.Errorf("cannot create output dir %q: %w", outDir, err)
// 	}

// 	// Use the shared global state so the HTTP API can read chunks
// 	state := getOrCreateState(outDir)

// 	chunkPattern := filepath.Join(outDir, "chunk_%05d.mp4")

// 	var parserElem string
// 	switch strings.ToLower(encoder) {
// 	case "h265", "hevc":
// 		parserElem = "h265parse"
// 	default:
// 		parserElem = "h264parse"
// 	}

// 	// pipeline := fmt.Sprintf(
//     // "gst-launch-1.0 -e -v "+
//     //     "srtsrc uri=\"srt://0.0.0.0:%d?mode=listener&latency=200&timeout=10000000&transtype=live\" ! "+
//     //     "queue max-size-buffers=200 leaky=downstream ! "+
//     //     "tsdemux ! %s ! "+
//     //     "splitmuxsink location=\"%s\" max-size-time=%d000000000 muxer=mp4mux",
//     // cfg.SRTPort(cam), parserElem, chunkPattern, chunkTimeSec)

// 	pipeline := fmt.Sprintf(
// 		"gst-launch-1.0 -e "+
// 			"srtsrc uri=\"%s\" ! "+
// 			"queue max-size-buffers=200 leaky=downstream ! "+
// 			"tsdemux ! %s ! "+
// 			"splitmuxsink location=\"%s\" max-size-time=%d000000000 muxer=mp4mux",
// 		cfg.SRTListenerURI(cam),
// 		parserElem,
// 		chunkPattern,
// 		chunkTimeSec,
// 	)

// 	fmt.Printf("%s Starting GStreamer receiver\n", logPrefix)
// 	fmt.Printf("%s SRT port : %d\n", logPrefix, cfg.SRTPort(cam))
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

// 	// Monitor GStreamer output on both stdout and stderr for chunk events
// 	const prefix = "chunk"
// 	go monitorGStreamer(stdout, cam, outDir, prefix, chunkTimeSec, state, cfg.Server.FrameDetail)
// 	go monitorGStreamer(stderr, cam, outDir, prefix, chunkTimeSec, state, cfg.Server.FrameDetail)

// 	// Safety fallback: periodically scan for finalized chunks that were missed
// 	go scanForFinalizedChunks(outDir, prefix, cam, chunkTimeSec, state, cfg.Server.FrameDetail)

// 	return cmd.Wait()
// }

// // RunWithRestart keeps calling Run and restarts it after any exit.
// // Uses exponential backoff up to backoffMax between restarts.
// func RunWithRestart(cfg *config.Config, cam config.CameraConfig, backoffMax time.Duration) {
// 	logPrefix := logPfx(cam)
// 	backoff := 5 * time.Second

// 	for {
// 		fmt.Printf("%s Starting receiver...\n", logPrefix)
// 		start := time.Now()

// 		err := Run(cfg, cam)
// 		elapsed := time.Since(start)

// 		if err != nil {
// 			fmt.Printf("%s Receiver exited after %.1fs: %v\n", logPrefix, elapsed.Seconds(), err)
// 		} else {
// 			fmt.Printf("%s Receiver exited cleanly after %.1fs\n", logPrefix, elapsed.Seconds())
// 		}

// 		// If ran >30s assume it was healthy; reset backoff
// 		if elapsed > 30*time.Second {
// 			backoff = 5 * time.Second
// 		}

// 		fmt.Printf("%s Restarting in %s...\n", logPrefix, backoff)
// 		time.Sleep(backoff)

// 		backoff *= 2
// 		if backoff > backoffMax {
// 			backoff = backoffMax
// 		}
// 	}
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // GStreamer output monitor
// // ─────────────────────────────────────────────────────────────────────────────

// func monitorGStreamer(
// 	pipe io.ReadCloser,
// 	cam config.CameraConfig,
// 	outDir, filenamePrefix string,
// 	chunkTimeSec int,
// 	state *cameraState,
// 	includeFrames bool,
// ) {
// 	logPrefix := logPfx(cam)
// 	scanner := bufio.NewScanner(pipe)
// 	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

// 	connected := false

// 	for scanner.Scan() {
// 		line := scanner.Text()
// 		lineLower := strings.ToLower(line)

// 		if strings.Contains(line, "ERROR:") {
// 			fmt.Printf("%s GST ERROR: %s\n", logPrefix, line)
// 		}

// 		if !connected && strings.Contains(line, "PLAYING") {
// 			connected = true
// 			fmt.Printf("\n%s Pipeline PLAYING — waiting for sender to connect...\n", logPrefix)
// 		}

// 		// Detect new chunk starting: GStreamer logs the file it just opened
// 		if strings.Contains(line, "location =") && strings.Contains(line, ".mp4") {
// 			chunkFile := extractFilePath(line)
// 			if chunkFile == "" {
// 				continue
// 			}
// 			logicalChunk := pathToChunkNum(chunkFile)
// 			if logicalChunk == 0 {
// 				continue
// 			}

// 			startTime := time.Now()
// 			state.mu.Lock()
// 			state.chunkStartTimes[logicalChunk] = startTime
// 			prevChunkNum := state.currentChunkNum
// 			state.currentChunkNum = logicalChunk
// 			state.mu.Unlock()

// 			fmt.Printf("\n%s CHUNK %d STARTED → %s\n",
// 				logPrefix, logicalChunk, filepath.Base(chunkFile))

// 			// When chunk N+1 starts, chunk N is closed — trigger analysis
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
// 						}
// 					}()
// 				}
// 			}
// 		}

// 		// Explicit finalization signal from GStreamer (splitmuxsink)
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
// 				}
// 			}()
// 		}
// 	}
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // Fallback scanner (in case GStreamer events are missed)
// // ─────────────────────────────────────────────────────────────────────────────

// func scanForFinalizedChunks(
// 	outDir, filenamePrefix string,
// 	cam config.CameraConfig,
// 	chunkTimeSec int,
// 	state *cameraState,
// 	includeFrames bool,
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

// 			// Check if already analyzed in memory
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
// 				}
// 			}()
// 		}
// 	}
// }

// // ─────────────────────────────────────────────────────────────────────────────
// // Helpers
// // ─────────────────────────────────────────────────────────────────────────────

// // appendChunkStats adds a chunk to the in-memory state, saves the index and
// // session summary to disk.
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

// // pathToChunkNum extracts the logical chunk number (1-based) from an MP4 filename.
// // chunk_00000.mp4 → 1,  chunk_00001.mp4 → 2
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
