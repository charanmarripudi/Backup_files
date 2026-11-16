//go:build sender

package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

type Config struct {
	RTSPURL          string
	RTSPProtocol     string
	RTSPTimeout      int
	SRTURI           string
	SRTLatency       int
	FPS              int
	Bitrate          int
	Preset           string
	Tune             string
	BFrames          int
	RefFrames        int
	KeyInt           int
	QMin             int
	QMax             int
	Threads          int
	Pass             string
	QueueSize        int
	Verbose          bool
	ChunkTime        int
	ReceiverHTTPPort int
	OutputWidth      int
	OutputHeight     int
	Encoder          string
	Preview          bool
	PreviewScale     int
}

type StreamInfo struct {
	Width        int
	Height       int
	FPS          float64
	Codec        string
	Profile      string
	Level        string
	BitRate      int64
	PixFmt       string
	RawFrameKB   float64
	RawMbps      float64
	InputMbps    float64
	InputFrameKB float64
}

type ChunkSummary struct {
	ChunkNumber    int     `json:"chunk_number"`
	StartTime      string  `json:"start_time"`
	EndTime        string  `json:"end_time"`
	DurationSec    float64 `json:"duration_sec"`
	TargetFrames   int64   `json:"target_frames"`
	FileSizeMB     float64 `json:"file_size_mb"`
	RawSizeMB      float64 `json:"raw_size_mb"`
	CompressionPct float64 `json:"compression_pct"`
	AvgBitrateKbps float64 `json:"avg_bitrate_kbps"`
	ActualFrames   int64   `json:"actual_frames"`
}

type FrameIndexEntry struct {
	FrameNumber int     `json:"frame_number"`
	FrameType   string  `json:"frame_type"`
	PTS         float64 `json:"pts"`
	Size        int     `json:"size"`
	IsKeyframe  bool    `json:"is_keyframe"`
	ChangePct   float64 `json:"change_pct"`
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

type BandwidthMonitor struct {
	mu            sync.RWMutex
	bytesReceived int64
	lastBytes     int64
	lastTime      time.Time
	currentKbps   float64
	fps           float64
	frameCount    int64
	lastFpsTime   time.Time
	name          string
	seedKbps      float64
	seedFps       float64
}

type RealTimeStats struct {
	mu             sync.RWMutex
	frameCount     int64
	lastSecondTime time.Time
	currentFPS     float64
	fpsHistory     []float64
}

// ScrollLog – fixed-capacity ring buffer
const scrollLogMax = 20

type ScrollLog struct {
	mu    sync.Mutex
	lines [scrollLogMax]string
	head  int
	count int
}

func (sl *ScrollLog) Push(line string) {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.lines[sl.head] = line
	sl.head = (sl.head + 1) % scrollLogMax
	if sl.count < scrollLogMax {
		sl.count++
	}
}

func (sl *ScrollLog) Lines(n int) []string {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	if n > sl.count {
		n = sl.count
	}
	out := make([]string, n)
	start := (sl.head - n + scrollLogMax) % scrollLogMax
	for i := 0; i < n; i++ {
		out[i] = sl.lines[(start+i)%scrollLogMax]
	}
	return out
}

var (
	inputScrollLog      ScrollLog
	compressedScrollLog ScrollLog
	analysisScrollLog   ScrollLog
)

func NewRealTimeStats() *RealTimeStats {
	return &RealTimeStats{
		lastSecondTime: time.Now(),
		fpsHistory:     make([]float64, 0, 10),
		currentFPS:     0,
	}
}

func (r *RealTimeStats) UpdateFrame() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frameCount++
	now := time.Now()
	if r.lastSecondTime.IsZero() {
		r.lastSecondTime = now
	} else {
		elapsed := now.Sub(r.lastSecondTime).Seconds()
		if elapsed >= 1.0 {
			instantFPS := float64(r.frameCount) / elapsed
			r.fpsHistory = append(r.fpsHistory, instantFPS)
			if len(r.fpsHistory) > 5 {
				r.fpsHistory = r.fpsHistory[1:]
			}
			sumFPS := 0.0
			for _, v := range r.fpsHistory {
				sumFPS += v
			}
			r.currentFPS = sumFPS / float64(len(r.fpsHistory))
			r.frameCount = 0
			r.lastSecondTime = now
		}
	}
}

func (r *RealTimeStats) GetFPS() float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.currentFPS <= 0 {
		return 25.0
	}
	return r.currentFPS
}

type DisplayManager struct {
	mu               sync.Mutex
	inputWindow      *exec.Cmd
	compressedWindow *exec.Cmd
	analysisWindow   *exec.Cmd
	inputPipe        io.WriteCloser
	compressedPipe   io.WriteCloser
	analysisPipe     io.WriteCloser
	inputWidth       int
	inputHeight      int
	compressedWidth  int
	compressedHeight int
	analysisWidth    int
	analysisHeight   int
	inputBW          *BandwidthMonitor
	outputBW         *BandwidthMonitor
	realTimeStats    *RealTimeStats
}

type FrameProcessor struct {
	mu            sync.Mutex
	prevFrame     []byte
	width         int
	height        int
	frameCount    int
	changeHistory []float64
	previewDead   bool
	captureCmd    *exec.Cmd
	display       *DisplayManager
	inputBW       *BandwidthMonitor
	outputBW      *BandwidthMonitor
	realTimeStats *RealTimeStats
}

// ─────────────────────────────────────────────────────────────────────────────
// Globals
// ─────────────────────────────────────────────────────────────────────────────

var (
	gActive                 int32
	gChunkNum               int32
	gPipelineStart          time.Time
	gSessionStart           time.Time
	gMu                     sync.Mutex
	gCmd                    *exec.Cmd
	gFramesEncoded          int64
	gTotalFrames            int64
	gAllChunks              []ChunkSummary
	gMetricsMu              sync.RWMutex
	gChunkStart             time.Time
	currentStreamInfo       StreamInfo
	receiverPort            int
	progressMu              sync.Mutex
	progressStarted         time.Time
	avgFPS                  float64
	fpsSamples              []float64
	gCurrentFrames          = make([]FrameIndexEntry, 0)
	gCurrentFramesMu        sync.Mutex
	gCameraID               = "camera1"
	gFrameProcessor         *FrameProcessor
	gChunkFrames            int64
	gCaptureFrames          int64
	gCaptureChunkFrames     int64
	consoleMu               sync.Mutex
	gActualEncodedFrameSize int64

	// Per-second averages (stored as *10 to preserve one decimal)
	gAvgCompKB   int64
	gAvgMotionKB int64
)

// ─────────────────────────────────────────────────────────────────────────────
// BandwidthMonitor
// ─────────────────────────────────────────────────────────────────────────────

func NewBandwidthMonitor(name string, seedKbps, seedFps float64) *BandwidthMonitor {
	return &BandwidthMonitor{
		lastTime:    time.Now(),
		lastFpsTime: time.Now(),
		name:        name,
		seedKbps:    seedKbps,
		seedFps:     seedFps,
		currentKbps: seedKbps,
		fps:         seedFps,
	}
}

func (bm *BandwidthMonitor) AddBytes(n int64) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.bytesReceived += n
	bm.frameCount++
	now := time.Now()
	if now.Sub(bm.lastTime).Seconds() >= 1.0 {
		elapsed := now.Sub(bm.lastTime).Seconds()
		bytesInPeriod := bm.bytesReceived - bm.lastBytes
		instant := float64(bytesInPeriod*8) / elapsed / 1000
		if bm.currentKbps == bm.seedKbps {
			bm.currentKbps = instant
		} else {
			bm.currentKbps = 0.3*instant + 0.7*bm.currentKbps
		}
		bm.lastBytes = bm.bytesReceived
		bm.lastTime = now
	}
	if now.Sub(bm.lastFpsTime).Seconds() >= 1.0 {
		elapsed := now.Sub(bm.lastFpsTime).Seconds()
		instantFps := float64(bm.frameCount) / elapsed
		if bm.fps == bm.seedFps {
			bm.fps = instantFps
		} else {
			bm.fps = 0.3*instantFps + 0.7*bm.fps
		}
		bm.frameCount = 0
		bm.lastFpsTime = now
	}
}

func (bm *BandwidthMonitor) SetCurrentBitrate(kbps float64) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if kbps > 0 {
		bm.currentKbps = kbps
	}
}

func (bm *BandwidthMonitor) SetCurrentFPS(fps float64) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if fps > 0 {
		bm.fps = fps
	}
}

func (bm *BandwidthMonitor) GetStats() (kbps float64, fps float64) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	k := bm.currentKbps
	if k <= 0 {
		k = bm.seedKbps
	}
	f := bm.fps
	if f <= 0 {
		f = bm.seedFps
	}
	return k, f
}

// ─────────────────────────────────────────────────────────────────────────────
// Bitmap Font (5x7)
// ─────────────────────────────────────────────────────────────────────────────

var font = map[byte][7][5]byte{
	'0': {{0, 1, 1, 1, 0}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {0, 1, 1, 1, 0}},
	'1': {{0, 0, 1, 0, 0}, {0, 1, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 1, 1, 1, 0}},
	'2': {{0, 1, 1, 1, 0}, {1, 0, 0, 0, 1}, {0, 0, 0, 0, 1}, {0, 0, 0, 1, 0}, {0, 0, 1, 0, 0}, {0, 1, 0, 0, 0}, {1, 1, 1, 1, 1}},
	'3': {{1, 1, 1, 1, 1}, {0, 0, 0, 0, 1}, {0, 0, 0, 1, 0}, {0, 0, 1, 1, 0}, {0, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {0, 1, 1, 1, 0}},
	'4': {{0, 0, 0, 1, 0}, {0, 0, 1, 1, 0}, {0, 1, 0, 1, 0}, {1, 0, 0, 1, 0}, {1, 1, 1, 1, 1}, {0, 0, 0, 1, 0}, {0, 0, 0, 1, 0}},
	'5': {{1, 1, 1, 1, 1}, {1, 0, 0, 0, 0}, {1, 1, 1, 1, 0}, {0, 0, 0, 0, 1}, {0, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {0, 1, 1, 1, 0}},
	'6': {{0, 0, 1, 1, 0}, {0, 1, 0, 0, 0}, {1, 0, 0, 0, 0}, {1, 1, 1, 1, 0}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {0, 1, 1, 1, 0}},
	'7': {{1, 1, 1, 1, 1}, {0, 0, 0, 0, 1}, {0, 0, 0, 1, 0}, {0, 0, 1, 0, 0}, {0, 1, 0, 0, 0}, {0, 1, 0, 0, 0}, {0, 1, 0, 0, 0}},
	'8': {{0, 1, 1, 1, 0}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {0, 1, 1, 1, 0}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {0, 1, 1, 1, 0}},
	'9': {{0, 1, 1, 1, 0}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {0, 1, 1, 1, 1}, {0, 0, 0, 0, 1}, {0, 0, 0, 0, 1}, {0, 1, 1, 1, 0}},
	'.': {{0, 0, 0, 0, 0}, {0, 0, 0, 0, 0}, {0, 0, 0, 0, 0}, {0, 0, 0, 0, 0}, {0, 0, 0, 0, 0}, {0, 0, 1, 1, 0}, {0, 0, 1, 1, 0}},
	':': {{0, 0, 0, 0, 0}, {0, 0, 1, 1, 0}, {0, 0, 1, 1, 0}, {0, 0, 0, 0, 0}, {0, 0, 1, 1, 0}, {0, 0, 1, 1, 0}, {0, 0, 0, 0, 0}},
	'%': {{1, 0, 0, 0, 1}, {1, 0, 0, 1, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 1, 0, 0, 0}, {1, 0, 0, 1, 1}, {0, 0, 0, 0, 1}},
	'K': {{1, 0, 0, 0, 1}, {1, 0, 0, 1, 0}, {1, 0, 1, 0, 0}, {1, 1, 0, 0, 0}, {1, 0, 1, 0, 0}, {1, 0, 0, 1, 0}, {1, 0, 0, 0, 1}},
	'M': {{1, 0, 0, 0, 1}, {1, 1, 0, 1, 1}, {1, 0, 1, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}},
	' ': {{0, 0, 0, 0, 0}, {0, 0, 0, 0, 0}, {0, 0, 0, 0, 0}, {0, 0, 0, 0, 0}, {0, 0, 0, 0, 0}, {0, 0, 0, 0, 0}, {0, 0, 0, 0, 0}},
	'F': {{1, 1, 1, 1, 1}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 0}, {1, 1, 1, 1, 0}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 0}},
	'P': {{1, 1, 1, 1, 0}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 1, 1, 1, 0}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 0}},
	'S': {{0, 1, 1, 1, 0}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 0}, {0, 1, 1, 1, 0}, {0, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {0, 1, 1, 1, 0}},
	'A': {{0, 1, 1, 1, 0}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 1, 1, 1, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}},
	'B': {{1, 1, 1, 1, 0}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 1, 1, 1, 0}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 1, 1, 1, 0}},
	'C': {{0, 1, 1, 1, 0}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 1}, {0, 1, 1, 1, 0}},
	'D': {{1, 1, 1, 1, 0}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 1, 1, 1, 0}},
	'E': {{1, 1, 1, 1, 1}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 0}, {1, 1, 1, 1, 0}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 0}, {1, 1, 1, 1, 1}},
	'G': {{0, 1, 1, 1, 0}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 0}, {1, 0, 1, 1, 0}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {0, 1, 1, 1, 0}},
	'H': {{1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 1, 1, 1, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}},
	'I': {{0, 1, 1, 1, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 1, 1, 1, 0}},
	'J': {{0, 0, 0, 0, 1}, {0, 0, 0, 0, 1}, {0, 0, 0, 0, 1}, {0, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {0, 1, 1, 1, 0}},
	'L': {{1, 0, 0, 0, 0}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 0}, {1, 1, 1, 1, 1}},
	'N': {{1, 0, 0, 0, 1}, {1, 1, 0, 0, 1}, {1, 0, 1, 0, 1}, {1, 0, 0, 1, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}},
	'O': {{0, 1, 1, 1, 0}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {0, 1, 1, 1, 0}},
	'Q': {{0, 1, 1, 1, 0}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 1, 0, 1}, {1, 0, 0, 1, 0}, {0, 1, 1, 0, 1}},
	'R': {{1, 1, 1, 1, 0}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 1, 1, 1, 0}, {1, 0, 1, 0, 0}, {1, 0, 0, 1, 0}, {1, 0, 0, 0, 1}},
	'T': {{1, 1, 1, 1, 1}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}},
	'U': {{1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {0, 1, 1, 1, 0}},
	'V': {{1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {0, 1, 0, 1, 0}, {0, 1, 0, 1, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}},
	'W': {{1, 0, 0, 0, 1}, {1, 0, 0, 0, 1}, {1, 0, 1, 0, 1}, {1, 0, 1, 0, 1}, {1, 0, 1, 0, 1}, {1, 1, 0, 1, 1}, {1, 0, 0, 0, 1}},
	'X': {{1, 0, 0, 0, 1}, {0, 1, 0, 1, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 1, 0, 1, 0}, {1, 0, 0, 0, 1}},
	'Y': {{1, 0, 0, 0, 1}, {0, 1, 0, 1, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}, {0, 0, 1, 0, 0}},
	'Z': {{1, 1, 1, 1, 1}, {0, 0, 0, 0, 1}, {0, 0, 0, 1, 0}, {0, 0, 1, 0, 0}, {0, 1, 0, 0, 0}, {1, 0, 0, 0, 0}, {1, 1, 1, 1, 1}},
	'>': {{1, 0, 0, 0, 0}, {0, 1, 0, 0, 0}, {0, 0, 1, 0, 0}, {0, 0, 0, 1, 0}, {0, 0, 1, 0, 0}, {0, 1, 0, 0, 0}, {1, 0, 0, 0, 0}},
	'(': {{0, 0, 1, 1, 0}, {0, 1, 0, 0, 0}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 0}, {1, 0, 0, 0, 0}, {0, 1, 0, 0, 0}, {0, 0, 1, 1, 0}},
	')': {{0, 1, 1, 0, 0}, {0, 0, 0, 1, 0}, {0, 0, 0, 0, 1}, {0, 0, 0, 0, 1}, {0, 0, 0, 0, 1}, {0, 0, 0, 1, 0}, {0, 1, 1, 0, 0}},
}

func drawChar(img []byte, w, h int, ch byte, x, y int, r, g, b byte, scale int) {
	cf, ok := font[ch]
	if !ok {
		return
	}
	for row := 0; row < 7; row++ {
		for col := 0; col < 5; col++ {
			if cf[row][col] == 1 {
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						px, py := x+col*scale+dx, y+row*scale+dy
						if px >= 0 && px < w && py >= 0 && py < h {
							i := (py*w + px) * 3
							if i+2 < len(img) {
								img[i] = r
								img[i+1] = g
								img[i+2] = b
							}
						}
					}
				}
			}
		}
	}
}

func drawString(img []byte, w, h int, text string, x, y int, r, g, b byte, scale int) {
	sp := 6 * scale
	for i := 0; i < len(text); i++ {
		drawChar(img, w, h, text[i], x+i*sp, y, r, g, b, scale)
	}
}

func drawPanel(img []byte, w, h, px, py, pw, ph int) {
	for row := py; row < py+ph && row < h; row++ {
		for col := px; col < px+pw && col < w; col++ {
			i := (row*w + col) * 3
			if i+2 < len(img) {
				img[i] = uint8(float64(img[i])*0.4 + 120)
				img[i+1] = uint8(float64(img[i+1])*0.4 + 120)
				img[i+2] = uint8(float64(img[i+2])*0.4 + 120)
			}
		}
	}
}

func drawDarkPanel(img []byte, w, h, px, py, pw, ph int) {
	for row := py; row < py+ph && row < h; row++ {
		for col := px; col < px+pw && col < w; col++ {
			i := (row*w + col) * 3
			if i+2 < len(img) {
				img[i] = uint8(float64(img[i]) * 0.25)
				img[i+1] = uint8(float64(img[i+1]) * 0.25)
				img[i+2] = uint8(float64(img[i+2]) * 0.25)
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Scrolling frame-size panel — drawn in the TOP-RIGHT corner
// ─────────────────────────────────────────────────────────────────────────────

const (
	scrollPanelW    = 200
	scrollRowH      = 10
	scrollVisLines  = 14
	scrollCharScale = 1
	scrollCharW     = 6
	scrollMarginX   = 4
)

func drawScrollPanel(img []byte, imgW, imgH int, sl *ScrollLog, labelR, labelG, labelB byte) {
	panelH := scrollVisLines*scrollRowH + 14
	panelX := imgW - scrollPanelW - 2
	panelY := 2
	if panelX < 0 {
		panelX = 0
	}
	drawDarkPanel(img, imgW, imgH, panelX, panelY, scrollPanelW, panelH)

	headerY := panelY + 2
	drawString(img, imgW, imgH, "FRAME  SIZE  RATIO", panelX+scrollMarginX, headerY, labelR, labelG, labelB, scrollCharScale)

	divY := headerY + 9
	for col := panelX; col < panelX+scrollPanelW && col < imgW; col++ {
		i := (divY*imgW + col) * 3
		if i+2 < len(img) {
			img[i] = 200
			img[i+1] = 200
			img[i+2] = 200
		}
	}

	lines := sl.Lines(scrollVisLines)
	for li, line := range lines {
		rowY := divY + 2 + li*scrollRowH
		if rowY+7 >= imgH {
			break
		}
		brightness := byte(255 - li*12)
		if brightness < 100 {
			brightness = 100
		}
		drawString(img, imgW, imgH, line, panelX+scrollMarginX, rowY, brightness, brightness, brightness, scrollCharScale)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Formatting helpers
// ─────────────────────────────────────────────────────────────────────────────

func formatBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	} else if n < 1024*1024 {
		return fmt.Sprintf("%dK", n/1024)
	}
	return fmt.Sprintf("%.1fM", float64(n)/(1024*1024))
}

func formatRatio(rawBytes, compBytes int) string {
	if compBytes <= 0 {
		return "---"
	}
	r := float64(rawBytes) / float64(compBytes)
	if r > 999 {
		r = 999
	}
	return fmt.Sprintf("%.0f:1", r)
}

// ─────────────────────────────────────────────────────────────────────────────
// Window Overlay Functions (final, single copies)
// ─────────────────────────────────────────────────────────────────────────────

func (fp *FrameProcessor) addInputOverlay(frame []byte, w, h int, motionPct float64, frameNum int) []byte {
	out := make([]byte, len(frame))
	copy(out, frame)

	kbps, fps := fp.inputBW.GetStats()
	rawFrameBytes := w * h * 3
	rawKBstr := formatBytes(rawFrameBytes)
	ratioStr := "1:1"

	drawPanel(out, w, h, 4, 4, 224, 120)

	drawString(out, w, h, "BITRATE:", 12, 8, 255, 255, 255, 2)
	drawString(out, w, h, fmt.Sprintf("%.1fM", kbps/1000), 120, 8, 255, 255, 255, 2)

	drawString(out, w, h, "FPS:", 12, 36, 255, 255, 255, 2)
	drawString(out, w, h, fmt.Sprintf("%.1f", fps), 120, 36, 255, 255, 255, 2)

	drawString(out, w, h, "RAW SIZE:", 12, 64, 255, 255, 255, 2)
	drawString(out, w, h, rawKBstr, 120, 64, 255, 220, 100, 2)

	drawString(out, w, h, "RATIO:", 12, 92, 255, 255, 255, 2)
	drawString(out, w, h, ratioStr, 120, 92, 200, 200, 200, 2)

	line := fmt.Sprintf("F%05d %s %s", frameNum, rawKBstr, ratioStr)
	inputScrollLog.Push(line)
	drawScrollPanel(out, w, h, &inputScrollLog, 180, 220, 255)

	return out
}

func (fp *FrameProcessor) addCompressedOverlay(frame []byte, w, h, targetKbps int, motionPct float64, frameNum int, actualCompSize int) []byte {
	out := make([]byte, len(frame))
	copy(out, frame)

	kbps, fps := fp.outputBW.GetStats()
	rawFrameBytes := w * h * 3
	compFrameBytes := actualCompSize
	if compFrameBytes <= 0 && fps > 0 && kbps > 0 {
		compFrameBytes = int(kbps * 1000 / 8 / fps)
	}
	ratioStr := formatRatio(rawFrameBytes, compFrameBytes)

	drawPanel(out, w, h, 4, 4, 224, 108)

	drawString(out, w, h, "BITRATE:", 12, 8, 255, 255, 255, 2)
	drawString(out, w, h, fmt.Sprintf("%.1fM", kbps/1000), 120, 8, 255, 255, 255, 2)

	drawString(out, w, h, "FPS:", 12, 36, 255, 255, 255, 2)
	drawString(out, w, h, fmt.Sprintf("%.1f", fps), 120, 36, 255, 255, 255, 2)

	drawString(out, w, h, "RATIO:", 12, 64, 255, 255, 255, 2)
	drawString(out, w, h, ratioStr, 120, 64, 200, 200, 200, 2)

	// No scrolling panel
	return out
}

func (fp *FrameProcessor) addAnalysisOverlay(frame []byte, w, h int, avgChange float64, frameNum int) []byte {
	out := make([]byte, len(frame))
	copy(out, frame)

	kbps, fps := fp.inputBW.GetStats()

	avgMotionKB := float64(atomic.LoadInt64(&gAvgMotionKB)) / 10.0
	motionKBstr := fmt.Sprintf("%.1fK", avgMotionKB)

	rawFrameBytes := w * h * 3
	ratioStr := formatRatio(rawFrameBytes, int(avgMotionKB*1024))

	drawPanel(out, w, h, 4, 4, 224, 170)

	drawString(out, w, h, "CHANGE %:", 12, 8, 255, 255, 255, 2)
	drawString(out, w, h, fmt.Sprintf("%.1f%%", avgChange), 120, 8, 255, 255, 255, 2)

	drawString(out, w, h, "BITRATE:", 12, 36, 255, 255, 255, 2)
	drawString(out, w, h, fmt.Sprintf("%.1fM", kbps/1000), 120, 36, 255, 255, 255, 2)

	drawString(out, w, h, "FPS:", 12, 64, 255, 255, 255, 2)
	drawString(out, w, h, fmt.Sprintf("%.1f", fps), 120, 64, 255, 255, 255, 2)

	drawString(out, w, h, "FRAME_SZ:", 12, 92, 255, 255, 255, 2)
	drawString(out, w, h, motionKBstr, 120, 92, 100, 255, 100, 2)

	drawString(out, w, h, "RATIO:", 12, 120, 255, 255, 255, 2)
	drawString(out, w, h, ratioStr, 120, 120, 200, 200, 200, 2)

	drawScrollPanel(out, w, h, &analysisScrollLog, 100, 255, 100)

	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// DisplayManager (unchanged from original, but needed for completeness)
// ─────────────────────────────────────────────────────────────────────────────

func (dm *DisplayManager) StartAllWindows(cfg Config, si StreamInfo, inputBW, outputBW *BandwidthMonitor, realTimeStats *RealTimeStats) error {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.inputBW = inputBW
	dm.outputBW = outputBW
	dm.realTimeStats = realTimeStats

	codecName := "H.264"
	if cfg.Encoder == "x265enc" {
		codecName = "H.265"
	}
	analysisW, analysisH := calcPreviewSize(si.Width, si.Height, cfg.PreviewScale)
	dm.analysisWidth = analysisW
	dm.analysisHeight = analysisH
	dm.inputWidth = si.Width
	dm.inputHeight = si.Height
	dm.compressedWidth = cfg.OutputWidth
	dm.compressedHeight = cfg.OutputHeight

	inputCmd := exec.Command("ffplay",
		"-window_title", fmt.Sprintf("INPUT | %s | %dx%d", gCameraID, si.Width, si.Height),
		"-f", "rawvideo", "-pixel_format", "rgb24",
		"-video_size", fmt.Sprintf("%dx%d", si.Width, si.Height),
		"-framerate", fmt.Sprintf("%d", cfg.FPS),
		"-i", "pipe:0", "-loglevel", "warning",
		"-left", "10", "-top", "30",
		"-flags", "low_delay", "-fflags", "nobuffer",
		"-analyzeduration", "0", "-probesize", "32",
		"-infbuf", "-framedrop", "-sync", "ext")
	inputPipe, err := inputCmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("input pipe: %v", err)
	}
	dm.inputPipe = inputPipe
	dm.inputWindow = inputCmd
	if err := inputCmd.Start(); err != nil {
		return fmt.Errorf("start input: %v", err)
	}
	logMessage("[%s] Input window PID=%d", ts(), inputCmd.Process.Pid)

	compressedCmd := exec.Command("ffplay",
		"-window_title", fmt.Sprintf("COMPRESSED | %dx%d | %s | LIVE FPS", cfg.OutputWidth, cfg.OutputHeight, codecName),
		"-f", "rawvideo", "-pixel_format", "rgb24",
		"-video_size", fmt.Sprintf("%dx%d", cfg.OutputWidth, cfg.OutputHeight),
		"-framerate", fmt.Sprintf("%d", cfg.FPS),
		"-i", "pipe:0", "-loglevel", "warning",
		"-left", fmt.Sprintf("%d", si.Width+30), "-top", "30",
		"-flags", "low_delay", "-fflags", "nobuffer",
		"-analyzeduration", "0", "-probesize", "32",
		"-infbuf", "-framedrop", "-sync", "ext")
	compressedPipe, err := compressedCmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("compressed pipe: %v", err)
	}
	dm.compressedPipe = compressedPipe
	dm.compressedWindow = compressedCmd
	if err := compressedCmd.Start(); err != nil {
		return fmt.Errorf("start compressed: %v", err)
	}
	logMessage("[%s] Compressed window PID=%d", ts(), compressedCmd.Process.Pid)

	analysisCmd := exec.Command("ffplay",
		"-window_title", "MOTION ANALYSIS",
		"-f", "rawvideo", "-pixel_format", "rgb24",
		"-video_size", fmt.Sprintf("%dx%d", analysisW, analysisH),
		"-framerate", "10",
		"-i", "pipe:0", "-loglevel", "warning",
		"-left", "10", "-top", fmt.Sprintf("%d", si.Height+60),
		"-flags", "low_delay", "-fflags", "nobuffer",
		"-analyzeduration", "0", "-probesize", "32",
		"-infbuf", "-framedrop", "-sync", "ext")
	analysisPipe, err := analysisCmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("analysis pipe: %v", err)
	}
	dm.analysisPipe = analysisPipe
	dm.analysisWindow = analysisCmd
	if err := analysisCmd.Start(); err != nil {
		return fmt.Errorf("start analysis: %v", err)
	}
	logMessage("[%s] Analysis window PID=%d (%dx%d)", ts(), analysisCmd.Process.Pid, analysisW, analysisH)

	go func() {
		dm.inputWindow.Wait()
		logMessage("[%s] Input window closed — restarting...", ts())
		dm.mu.Lock()
		dm.inputPipe = nil
		dm.mu.Unlock()
		go dm.restartWindow("input", cfg, si)
	}()
	go func() {
		dm.compressedWindow.Wait()
		logMessage("[%s] Compressed window closed — restarting...", ts())
		dm.mu.Lock()
		dm.compressedPipe = nil
		dm.mu.Unlock()
		go dm.restartWindow("compressed", cfg, si)
	}()
	go func() {
		dm.analysisWindow.Wait()
		logMessage("[%s] Analysis window closed — restarting...", ts())
		dm.mu.Lock()
		dm.analysisPipe = nil
		dm.mu.Unlock()
		go dm.restartWindow("analysis", cfg, si)
	}()
	return nil
}

func (dm *DisplayManager) restartWindow(which string, cfg Config, si StreamInfo) {
	time.Sleep(1 * time.Second)
	dm.mu.Lock()
	defer dm.mu.Unlock()

	var cmd *exec.Cmd
	var args []string
	codecName := "H.264"
	if cfg.Encoder == "x265enc" {
		codecName = "H.265"
	}

	baseFlags := []string{"-loglevel", "warning", "-flags", "low_delay", "-fflags", "nobuffer",
		"-analyzeduration", "0", "-probesize", "32", "-infbuf", "-framedrop", "-sync", "ext"}

	switch which {
	case "input":
		args = append([]string{
			"-window_title", fmt.Sprintf("INPUT | %s | %dx%d", gCameraID, si.Width, si.Height),
			"-f", "rawvideo", "-pixel_format", "rgb24",
			"-video_size", fmt.Sprintf("%dx%d", si.Width, si.Height),
			"-framerate", fmt.Sprintf("%d", cfg.FPS),
			"-i", "pipe:0", "-left", "10", "-top", "30",
		}, baseFlags...)
	case "compressed":
		args = append([]string{
			"-window_title", fmt.Sprintf("COMPRESSED | %dx%d | %s | LIVE FPS", cfg.OutputWidth, cfg.OutputHeight, codecName),
			"-f", "rawvideo", "-pixel_format", "rgb24",
			"-video_size", fmt.Sprintf("%dx%d", cfg.OutputWidth, cfg.OutputHeight),
			"-framerate", fmt.Sprintf("%d", cfg.FPS),
			"-i", "pipe:0", "-left", fmt.Sprintf("%d", si.Width+30), "-top", "30",
		}, baseFlags...)
	case "analysis":
		args = append([]string{
			"-window_title", "MOTION ANALYSIS",
			"-f", "rawvideo", "-pixel_format", "rgb24",
			"-video_size", fmt.Sprintf("%dx%d", dm.analysisWidth, dm.analysisHeight),
			"-framerate", "10",
			"-i", "pipe:0", "-left", "10", "-top", fmt.Sprintf("%d", si.Height+60),
		}, baseFlags...)
	default:
		return
	}

	cmd = exec.Command("ffplay", args...)
	pipe, err := cmd.StdinPipe()
	if err != nil {
		logMessage("[%s] Restart %s pipe failed: %v", ts(), which, err)
		return
	}
	if err := cmd.Start(); err != nil {
		logMessage("[%s] Restart %s failed: %v", ts(), which, err)
		return
	}

	switch which {
	case "input":
		dm.inputWindow = cmd
		dm.inputPipe = pipe
		go func() {
			cmd.Wait()
			dm.mu.Lock()
			dm.inputPipe = nil
			dm.mu.Unlock()
			go dm.restartWindow("input", cfg, si)
		}()
	case "compressed":
		dm.compressedWindow = cmd
		dm.compressedPipe = pipe
		go func() {
			cmd.Wait()
			dm.mu.Lock()
			dm.compressedPipe = nil
			dm.mu.Unlock()
			go dm.restartWindow("compressed", cfg, si)
		}()
	case "analysis":
		dm.analysisWindow = cmd
		dm.analysisPipe = pipe
		go func() {
			cmd.Wait()
			dm.mu.Lock()
			dm.analysisPipe = nil
			dm.mu.Unlock()
			go dm.restartWindow("analysis", cfg, si)
		}()
	}
	logMessage("[%s] %s window restarted PID=%d", ts(), which, cmd.Process.Pid)
}

// ─────────────────────────────────────────────────────────────────────────────
// Main (unchanged)
// ─────────────────────────────────────────────────────────────────────────────

func main() {
	var cfg Config
	var help bool
	var metricsFile, cameraID string

	flag.StringVar(&cfg.RTSPURL, "rtsp", "", "RTSP URL (required)")
	flag.StringVar(&cfg.RTSPProtocol, "rtsp-protocol", "tcp", "RTSP transport")
	flag.IntVar(&cfg.RTSPTimeout, "rtsp-timeout", 10, "RTSP timeout seconds")
	flag.StringVar(&cfg.SRTURI, "srt", "", "SRT destination (required)")
	flag.IntVar(&cfg.SRTLatency, "srt-latency", 120, "SRT latency ms")
	flag.IntVar(&cfg.FPS, "fps", 25, "Output FPS")
	flag.IntVar(&cfg.Bitrate, "bitrate", 800, "Bitrate kbps")
	flag.StringVar(&cfg.Preset, "preset", "ultrafast", "x264/x265 preset")
	flag.StringVar(&cfg.Tune, "tune", "zerolatency", "Encoder tune")
	flag.IntVar(&cfg.BFrames, "bframes", 0, "B-frames")
	flag.IntVar(&cfg.RefFrames, "ref", 1, "Reference frames")
	flag.IntVar(&cfg.KeyInt, "keyint", 100, "Keyframe interval")
	flag.IntVar(&cfg.QMin, "qmin", 18, "Min quantizer")
	flag.IntVar(&cfg.QMax, "qmax", 32, "Max quantizer")
	flag.IntVar(&cfg.Threads, "threads", 0, "Encoding threads")
	flag.StringVar(&cfg.Pass, "pass", "cbr", "Encoding pass")
	flag.IntVar(&cfg.QueueSize, "queue", 5, "Queue size")
	flag.BoolVar(&cfg.Verbose, "verbose", false, "Verbose output")
	flag.IntVar(&cfg.ChunkTime, "chunk-time", 120, "Chunk duration seconds")
	flag.IntVar(&cfg.ReceiverHTTPPort, "receiver-port", 8080, "Receiver HTTP port")
	flag.StringVar(&metricsFile, "metrics", "sender_metrics.json", "Metrics file")
	flag.BoolVar(&help, "help", false, "Show help")
	flag.IntVar(&cfg.OutputWidth, "width", 0, "Output width")
	flag.IntVar(&cfg.OutputHeight, "height", 0, "Output height")
	flag.StringVar(&cfg.Encoder, "encoder", "x265enc", "x264enc or x265enc")
	flag.StringVar(&cameraID, "camera-id", "camera1", "Camera ID")
	flag.BoolVar(&cfg.Preview, "preview", true, "Show live preview")
	flag.IntVar(&cfg.PreviewScale, "preview-scale", 720, "Preview max dimension")
	flag.Parse()

	if help || cfg.RTSPURL == "" || cfg.SRTURI == "" {
		printHelp()
		return
	}
	if cfg.Encoder != "x264enc" && cfg.Encoder != "x265enc" {
		logMessage("[%s] Invalid encoder '%s'", ts(), cfg.Encoder)
		os.Exit(1)
	}

	gCameraID = cameraID
	receiverPort = cfg.ReceiverHTTPPort
	gSessionStart = time.Now()
	gChunkStart = time.Now()
	atomic.StoreInt32(&gChunkNum, 1)
	fpsSamples = make([]float64, 0, 60)

	printBanner(cfg)
	logMessage("[%s] Probing input stream...", ts())
	si := probeInputStream(cfg)
	currentStreamInfo = si
	printInputStreamInfo(si)

	if cfg.OutputWidth > 0 && cfg.OutputHeight > 0 {
		logMessage("[%s] Using resolution: %dx%d", ts(), cfg.OutputWidth, cfg.OutputHeight)
	} else {
		cfg.OutputWidth = si.Width
		cfg.OutputHeight = si.Height
		logMessage("[%s] Auto resolution: %dx%d", ts(), cfg.OutputWidth, cfg.OutputHeight)
	}
	if cfg.OutputWidth%2 != 0 {
		cfg.OutputWidth++
	}
	if cfg.OutputHeight%2 != 0 {
		cfg.OutputHeight++
	}

	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h")

	inputBW := NewBandwidthMonitor("input", float64(si.BitRate)/1000, si.FPS)
	outputBW := NewBandwidthMonitor("output", float64(cfg.Bitrate), float64(cfg.FPS))
	realTimeStats := NewRealTimeStats()

	if cfg.Preview {
		logMessage("[%s] Starting 3-window display with frame-size scrolling...", ts())
		display := &DisplayManager{
			inputWidth: si.Width, inputHeight: si.Height,
			compressedWidth: cfg.OutputWidth, compressedHeight: cfg.OutputHeight,
		}
		fp := &FrameProcessor{
			width: si.Width, height: si.Height,
			changeHistory: make([]float64, 0, 120),
			display:       display, inputBW: inputBW, outputBW: outputBW,
			realTimeStats: realTimeStats,
		}
		gFrameProcessor = fp
		if err := display.StartAllWindows(cfg, si, inputBW, outputBW, realTimeStats); err != nil {
			logMessage("[%s] WARNING: ffplay failed: %v", ts(), err)
			gFrameProcessor = nil
		} else {
			go fp.runSingleCapture(cfg, si.Width, si.Height, cfg.FPS,
				cfg.OutputWidth, cfg.OutputHeight,
				display.analysisWidth, display.analysisHeight)
		}
	}

	printEncodingPlan(si, cfg)

	go runPipeline(cfg, si)
	go monitorPipelineHealth(cfg)
	go runChunkMonitor(si, cfg)
	go runMetricsWriter(metricsFile)
	go runProgressDisplay(cfg)
	go testReceiverConnection(cfg.ReceiverHTTPPort)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	logMessage("\n[%s] Stopping...", ts())
	fmt.Print("\033[?25h")
	if gFrameProcessor != nil {
		gFrameProcessor.mu.Lock()
		if gFrameProcessor.captureCmd != nil {
			gFrameProcessor.captureCmd.Process.Kill()
		}
		d := gFrameProcessor.display
		if d != nil {
			if d.inputWindow != nil {
				d.inputWindow.Process.Kill()
			}
			if d.compressedWindow != nil {
				d.compressedWindow.Process.Kill()
			}
			if d.analysisWindow != nil {
				d.analysisWindow.Process.Kill()
			}
		}
		gFrameProcessor.mu.Unlock()
	}
	gracefulShutdown()
	printFinalSummary(si, cfg)
	saveFinalMetrics(metricsFile)
}

func logMessage(format string, args ...interface{}) {
	consoleMu.Lock()
	defer consoleMu.Unlock()
	fmt.Print("\r\033[K")
	fmt.Printf(format, args...)
	fmt.Println()
}

// ─────────────────────────────────────────────────────────────────────────────
// Preview Helpers
// ─────────────────────────────────────────────────────────────────────────────

func calcPreviewSize(origW, origH, maxHeight int) (int, int) {
	if maxHeight <= 0 {
		maxHeight = 720
	}
	if origH <= maxHeight {
		return (origW / 2) * 2, (origH / 2) * 2
	}
	aspect := float64(origW) / float64(origH)
	newH := maxHeight
	newW := (int(float64(newH)*aspect) / 2) * 2
	return newW, newH
}

func scaleImageFast(src []byte, srcW, srcH int, dst []byte, dstW, dstH int) {
	for y := 0; y < dstH; y++ {
		srcY := y * srcH / dstH
		for x := 0; x < dstW; x++ {
			srcX := x * srcW / dstW
			si := (srcY*srcW + srcX) * 3
			di := (y*dstW + x) * 3
			dst[di] = src[si]
			dst[di+1] = src[si+1]
			dst[di+2] = src[si+2]
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Motion Detection
// ─────────────────────────────────────────────────────────────────────────────

func (fp *FrameProcessor) detectMotion(frame []byte, w, h, frameNum int) ([]byte, int) {
	out := make([]byte, len(frame))
	copy(out, frame)
	fp.mu.Lock()
	defer fp.mu.Unlock()
	if fp.prevFrame == nil || len(fp.prevFrame) != len(frame) {
		fp.prevFrame = make([]byte, len(frame))
		copy(fp.prevFrame, frame)
		return out, 0
	}
	totalPx := w * h
	const pixThreshold = 18
	rawMask := make([]bool, totalPx)
	changedPx := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := (y*w + x) * 3
			dr := iabs(int(frame[idx]) - int(fp.prevFrame[idx]))
			dg := iabs(int(frame[idx+1]) - int(fp.prevFrame[idx+1]))
			db := iabs(int(frame[idx+2]) - int(fp.prevFrame[idx+2]))
			d := dr
			if dg > d {
				d = dg
			}
			if db > d {
				d = db
			}
			if d > pixThreshold {
				rawMask[y*w+x] = true
				changedPx++
			}
		}
	}
	// erosion & connected components (same as original)
	erodedMask := make([]bool, totalPx)
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			if !rawMask[y*w+x] {
				continue
			}
			n := 0
			if rawMask[(y-1)*w+x] {
				n++
			}
			if rawMask[(y+1)*w+x] {
				n++
			}
			if rawMask[y*w+x-1] {
				n++
			}
			if rawMask[y*w+x+1] {
				n++
			}
			if rawMask[(y-1)*w+x-1] {
				n++
			}
			if rawMask[(y-1)*w+x+1] {
				n++
			}
			if rawMask[(y+1)*w+x-1] {
				n++
			}
			if rawMask[(y+1)*w+x+1] {
				n++
			}
			if n >= 3 {
				erodedMask[y*w+x] = true
			}
		}
	}
	const R = 7
	hPass := make([]bool, totalPx)
	for y := 0; y < h; y++ {
		count := 0
		for x := 0; x <= R && x < w; x++ {
			if erodedMask[y*w+x] {
				count++
			}
		}
		for x := 0; x < w; x++ {
			if count > 0 {
				hPass[y*w+x] = true
			}
			if ax := x + R + 1; ax < w && erodedMask[y*w+ax] {
				count++
			}
			if rx := x - R; rx >= 0 && erodedMask[y*w+rx] {
				count--
			}
		}
	}
	edgeMask := make([]bool, totalPx)
	for x := 0; x < w; x++ {
		count := 0
		for y := 0; y <= R && y < h; y++ {
			if hPass[y*w+x] {
				count++
			}
		}
		for y := 0; y < h; y++ {
			if count > 0 {
				edgeMask[y*w+x] = true
			}
			if ay := y + R + 1; ay < h && hPass[ay*w+x] {
				count++
			}
			if ry := y - R; ry >= 0 && hPass[ry*w+x] {
				count--
			}
		}
	}
	background := make([]bool, totalPx)
	queue := make([]int, 0, w*h/4)
	isBarrier := func(px int) bool { return edgeMask[px] || rawMask[px] }
	seed := func(px int) {
		if !isBarrier(px) && !background[px] {
			background[px] = true
			queue = append(queue, px)
		}
	}
	for x := 0; x < w; x++ {
		seed(x)
		seed((h-1)*w + x)
	}
	for y := 1; y < h-1; y++ {
		seed(y * w)
		seed(y*w + w - 1)
	}
	for len(queue) > 0 {
		px := queue[0]
		queue = queue[1:]
		y, x := px/w, px%w
		chk := func(ny, nx int) {
			if ny < 0 || ny >= h || nx < 0 || nx >= w {
				return
			}
			npx := ny*w + nx
			if !isBarrier(npx) && !background[npx] {
				background[npx] = true
				queue = append(queue, npx)
			}
		}
		chk(y-1, x)
		chk(y+1, x)
		chk(y, x-1)
		chk(y, x+1)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			px := y*w + x
			if background[px] {
				continue
			}
			i := px * 3
			const keep, add = 15, 225
			newG := int(out[i+1])*keep/100 + add
			if newG > 255 {
				newG = 255
			}
			out[i] = uint8(int(out[i]) * keep / 100)
			out[i+1] = uint8(newG)
			out[i+2] = uint8(int(out[i+2]) * keep / 100)
		}
	}
	changePct := float64(changedPx) / float64(totalPx) * 100.0
	fp.changeHistory = append(fp.changeHistory, changePct)
	if len(fp.changeHistory) > 60 {
		fp.changeHistory = fp.changeHistory[1:]
	}
	fp.frameCount = frameNum
	copy(fp.prevFrame, frame)
	return out, changedPx
}

func drawMotionBar(img []byte, w, h int, changePct float64) {
	barY := h - 16
	if barY < 0 {
		barY = 0
	}
	barH := 10
	bx0, bx1 := 4, w-4
	fill := int(float64(bx1-bx0) * changePct / 100.0)
	if fill > bx1-bx0 {
		fill = bx1 - bx0
	}
	if fill < 0 {
		fill = 0
	}
	var fr, fg, fb byte = 0, 200, 220
	if changePct > 20 {
		fr, fg, fb = 255, 50, 50
	} else if changePct > 8 {
		fr, fg, fb = 255, 200, 0
	}
	for y := barY; y < barY+barH && y < h; y++ {
		for x := bx0; x < bx1 && x < w; x++ {
			i := (y*w + x) * 3
			if i+2 >= len(img) {
				continue
			}
			if x < bx0+fill {
				img[i] = fr
				img[i+1] = fg
				img[i+2] = fb
			} else {
				img[i] = 25
				img[i+1] = 25
				img[i+2] = 25
			}
		}
	}
}

func iabs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ─────────────────────────────────────────────────────────────────────────────
// runSingleCapture – main frame loop
// ─────────────────────────────────────────────────────────────────────────────

func (fp *FrameProcessor) runSingleCapture(cfg Config,
	inputW, inputH, inputFPS, compressedW, compressedH, analysisW, analysisH int) {

	frameSize := inputW * inputH * 3
	compBuf := make([]byte, compressedW*compressedH*3)
	anaBuf := make([]byte, analysisW*analysisH*3)

	for {
		fp.mu.Lock()
		dead := fp.previewDead
		display := fp.display
		fp.mu.Unlock()
		if dead || display == nil {
			return
		}

		captureCmd := exec.Command("ffmpeg",
			"-loglevel", "warning",
			"-rtsp_transport", cfg.RTSPProtocol,
			"-timeout", fmt.Sprintf("%d000000", cfg.RTSPTimeout),
			"-user_agent", "FFmpeg/5.0",
			"-i", cfg.RTSPURL,
			"-an",
			"-vf", fmt.Sprintf("fps=%d", inputFPS),
			"-f", "rawvideo", "-pix_fmt", "rgb24",
			"-flags", "low_delay", "-fflags", "nobuffer",
			"-analyzeduration", "0", "-probesize", "32",
			"pipe:1",
		)

		stdout, err := captureCmd.StdoutPipe()
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		stderrPipe, _ := captureCmd.StderrPipe()
		bitrateRe := regexp.MustCompile(`bitrate=\s*([\d.]+)kbits`)
		go func() {
			sc := bufio.NewScanner(stderrPipe)
			for sc.Scan() {
				line := sc.Text()
				if m := bitrateRe.FindStringSubmatch(line); len(m) > 1 {
					if kbps, err := strconv.ParseFloat(m[1], 64); err == nil && kbps > 0 {
						fp.inputBW.SetCurrentBitrate(kbps)
					}
				}
				if cfg.Verbose && !strings.Contains(line, "frame=") {
					logMessage("[FFMPEG] %s", line)
				}
			}
		}()

		fp.mu.Lock()
		fp.captureCmd = captureCmd
		fp.mu.Unlock()
		if err := captureCmd.Start(); err != nil {
			logMessage("[%s] Capture start failed: %v", ts(), err)
			time.Sleep(2 * time.Second)
			continue
		}
		logMessage("[%s] Capture PID=%d (%dx%d @ %dfps)", ts(), captureCmd.Process.Pid, inputW, inputH, inputFPS)

		buf := make([]byte, frameSize)
		frameNum := 0
		lastAnalysis := time.Now()
		lastFPSUpdate := time.Now()
		frameCounter := 0

		// Per-second averaging variables
		var secondFrames int
		var secondCompSum int64
		var secondMotionSum int64
		lastSecondTime := time.Now()

		for {
			n, err := io.ReadFull(stdout, buf)
			if err != nil || n != frameSize {
				break
			}
			frameNum++
			frameCounter++

			atomic.AddInt64(&gCaptureFrames, 1)
			atomic.AddInt64(&gCaptureChunkFrames, 1)
			fp.inputBW.AddBytes(int64(n))

			now := time.Now()

			if now.Sub(lastFPSUpdate) >= time.Second {
				fps := float64(frameCounter) / now.Sub(lastFPSUpdate).Seconds()
				fp.inputBW.SetCurrentFPS(fps)
				frameCounter = 0
				lastFPSUpdate = now
			}

			fp.realTimeStats.UpdateFrame()

			var motionFrame []byte
			var changedPx int
			doAnalysis := now.Sub(lastAnalysis) >= 100*time.Millisecond

			if doAnalysis {
				scaleImageFast(buf, inputW, inputH, anaBuf, analysisW, analysisH)
				motionFrame, changedPx = fp.detectMotion(anaBuf, analysisW, analysisH, frameNum)
				lastAnalysis = now
			}

			fp.mu.Lock()
			avgChange := 0.0
			if len(fp.changeHistory) > 0 {
				weight, total := 1.0, 0.0
				for i := len(fp.changeHistory) - 1; i >= 0; i-- {
					avgChange += fp.changeHistory[i] * weight
					total += weight
					weight *= 0.9
				}
				avgChange /= total
			}
			fp.mu.Unlock()

			// Get actual compressed frame size from encoder logs
			actualCompSize := int(atomic.LoadInt64(&gActualEncodedFrameSize))

			// Accumulate for per-second averages
			secondFrames++
			secondCompSum += int64(actualCompSize)
			secondMotionSum += int64(changedPx * 3) // raw bytes of motion region

			// Update averages every second
			if now.Sub(lastSecondTime) >= time.Second {
				avgCompKB := float64(secondCompSum) / float64(secondFrames) / 1024.0
				avgMotionKB := float64(secondMotionSum) / float64(secondFrames) / 1024.0
				atomic.StoreInt64(&gAvgCompKB, int64(avgCompKB*10))
				atomic.StoreInt64(&gAvgMotionKB, int64(avgMotionKB*10))

				secondFrames = 0
				secondCompSum = 0
				secondMotionSum = 0
				lastSecondTime = now
			}

			// Input window
			display.mu.Lock()
			inputPipe := display.inputPipe
			display.mu.Unlock()
			if inputPipe != nil {
				frame := fp.addInputOverlay(buf, inputW, inputH, avgChange, frameNum)
				if _, err := inputPipe.Write(frame); err != nil {
					display.mu.Lock()
					display.inputPipe = nil
					display.mu.Unlock()
				}
			}

			// Compressed window
			display.mu.Lock()
			compPipe := display.compressedPipe
			display.mu.Unlock()
			if compPipe != nil {
				scaleImageFast(buf, inputW, inputH, compBuf, compressedW, compressedH)
				frame := fp.addCompressedOverlay(compBuf, compressedW, compressedH, cfg.Bitrate, avgChange, frameNum, actualCompSize)
				if _, err := compPipe.Write(frame); err != nil {
					display.mu.Lock()
					display.compressedPipe = nil
					display.mu.Unlock()
				}
			}

			// Analysis window
			display.mu.Lock()
			anaPipe := display.analysisPipe
			display.mu.Unlock()
			if anaPipe != nil && doAnalysis && motionFrame != nil {
				// Compute per‑frame motion size for the scrolling log
				motionRawKB := float64(changedPx*3) / 1024.0
				motionKBstr := fmt.Sprintf("%.1fK", motionRawKB)
				ratioStr := formatRatio(analysisW*analysisH*3, int(motionRawKB*1024))
				analysisScrollLog.Push(fmt.Sprintf("F%05d %s %s", frameNum, motionKBstr, ratioStr))

				// Overlay uses the average MSZ (from atomic)
				motionFrame = fp.addAnalysisOverlay(motionFrame, analysisW, analysisH, avgChange, frameNum)
				drawMotionBar(motionFrame, analysisW, analysisH, avgChange)
				if _, err := anaPipe.Write(motionFrame); err != nil {
					display.mu.Lock()
					display.analysisPipe = nil
					display.mu.Unlock()
				}
			}
		}
		captureCmd.Wait()
		fp.mu.Lock()
		dead = fp.previewDead
		fp.mu.Unlock()
		if dead {
			return
		}
		logMessage("[%s] Capture restarting...", ts())
		time.Sleep(2 * time.Second)
	}
}

func init() {
	// Limit memory usage to 80% of system RAM to avoid OOM
	debug.SetMemoryLimit(400 * 1024 * 1024) // Set to 400MB if you have 512MB RAM
}

// ─────────────────────────────────────────────────────────────────────────────
// GStreamer Pipeline
// ─────────────────────────────────────────────────────────────────────────────

func runPipeline(cfg Config, si StreamInfo) {
	threads := cfg.Threads
	if threads <= 0 {
		threads = runtime.NumCPU()
	}
	outW, outH := cfg.OutputWidth, cfg.OutputHeight

	srtURI := cfg.SRTURI
	if strings.Contains(srtURI, "?") {
		if !strings.Contains(srtURI, "mode=") {
			srtURI += "&mode=caller"
		}
		if !strings.Contains(srtURI, "streamid=") {
			srtURI += "&streamid=" + gCameraID
		}
	} else {
		srtURI += "?mode=caller&streamid=" + gCameraID
	}
	if !strings.Contains(srtURI, "latency=") {
		if strings.Contains(srtURI, "?") {
			srtURI += fmt.Sprintf("&latency=%d", cfg.SRTLatency)
		} else {
			srtURI += fmt.Sprintf("?latency=%d", cfg.SRTLatency)
		}
	}
	logMessage("[%s] SRT URI: %s", ts(), srtURI)

	var pipeline string
	if cfg.Encoder == "x265enc" {
		pipeline = fmt.Sprintf(
			`gst-launch-1.0 -e -v `+
				`rtspsrc location="%s" protocols=%s latency=0 timeout=%d do-retransmission=true ! `+
				`rtph264depay ! queue max-size-buffers=%d leaky=downstream ! `+
				`h264parse ! avdec_h264 ! `+
				`videoconvert ! videoscale ! videorate ! `+
				`video/x-raw,width=%d,height=%d,framerate=%d/1,pixel-aspect-ratio=1/1 ! `+
				`x265enc bitrate=%d key-int-max=%d speed-preset=%s tune=%s log-level=2 ! `+
				`identity name=frame_size silent=false ! `+
				`h265parse ! mpegtsmux ! `+
				`srtsink uri="%s" sync=false wait-for-connection=false latency=%d`,
			cfg.RTSPURL, cfg.RTSPProtocol, cfg.RTSPTimeout*1000000,
			cfg.QueueSize, outW, outH, cfg.FPS,
			cfg.Bitrate, cfg.KeyInt, cfg.Preset, cfg.Tune,
			srtURI, cfg.SRTLatency)
	} else {
		pipeline = fmt.Sprintf(
			`gst-launch-1.0 -e -v `+
				`rtspsrc location="%s" protocols=%s latency=0 timeout=%d do-retransmission=true ! `+
				`rtph264depay ! queue max-size-buffers=%d leaky=downstream ! `+
				`h264parse ! avdec_h264 ! `+
				`videoconvert ! videoscale ! videorate ! `+
				`video/x-raw,width=%d,height=%d,framerate=%d/1,pixel-aspect-ratio=1/1 ! `+
				`x264enc bitrate=%d speed-preset=%s tune=%s key-int-max=%d bframes=%d ref=%d qp-min=%d qp-max=%d threads=%d verbose=1 ! `+
				`identity name=frame_size silent=false ! `+
				`video/x-h264,stream-format=byte-stream ! h264parse ! mpegtsmux ! `+
				`srtsink uri="%s" sync=false wait-for-connection=false latency=%d`,
			cfg.RTSPURL, cfg.RTSPProtocol, cfg.RTSPTimeout*1000000,
			cfg.QueueSize, outW, outH, cfg.FPS,
			cfg.Bitrate, cfg.Preset, cfg.Tune, cfg.KeyInt, cfg.BFrames, cfg.RefFrames,
			cfg.QMin, cfg.QMax, threads, srtURI, cfg.SRTLatency)
	}

	fmt.Println("\n" + strings.Repeat("-", 80))
	fmt.Println("GSTREAMER PIPELINE:")
	fmt.Println(pipeline)
	fmt.Println(strings.Repeat("-", 80) + "\n")

	attempt := 0
	for {
		attempt++
		gPipelineStart = time.Now()
		cmd := exec.Command("bash", "-c", pipeline)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()
		go parseGStreamerOutput(stdout, cfg)
		go parseGStreamerOutput(stderr, cfg)

		if err := cmd.Start(); err != nil {
			logMessage("[%s] Pipeline start failed (attempt %d): %v — retry in 5s", ts(), attempt, err)
			time.Sleep(5 * time.Second)
			continue
		}
		gMu.Lock()
		gCmd = cmd
		gMu.Unlock()
		logMessage("[%s] GStreamer PID: %d (attempt %d)", ts(), cmd.Process.Pid, attempt)

		err := cmd.Wait()
		atomic.StoreInt32(&gActive, 0)
		errStr := fmt.Sprintf("%v", err)
		if strings.Contains(errStr, "signal: interrupt") || strings.Contains(errStr, "signal: killed") {
			logMessage("[%s] Pipeline stopped by signal", ts())
			return
		}
		if err != nil {
			logMessage("[%s] Pipeline exited (attempt %d): %v — retry in 5s", ts(), attempt, err)
		}
		time.Sleep(5 * time.Second)
	}
}

func parseGStreamerOutput(pipe io.ReadCloser, cfg Config) {
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	pipelinePlaying := false
	lastFrameNum := 0
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`frame[:\s]+(\d+)`),
		regexp.MustCompile(`encoded\s+(\d+)\s+frames`),
		regexp.MustCompile(`\[(\d+)\]\s+[IPB]`),
	}
	frameSizeRe := regexp.MustCompile(`size[:\s]+(\d+)`)
	bitrateRe := regexp.MustCompile(`(?i)bitrate[:\s=]+([\d.]+)`)
	identitySizeRe := regexp.MustCompile(`frame_size.*?size[:\s]+(\d+)`)

	// Multiple regex patterns to match different encoder log formats
	frameStatsRe1 := regexp.MustCompile(`frame\s+(\d+):\s+([IPB]),\s+(\d+)\s+bytes`)   // "frame 1: I, 45000 bytes"
	frameStatsRe2 := regexp.MustCompile(`frame\s+(\d+)\s+([IPB])\s+(\d+)\s+bytes`)     // "frame 1 I 45000 bytes"
	frameStatsRe3 := regexp.MustCompile(`\[(\d+)\]\s+([IPB])\s+(\d+)\s+bytes`)         // "[1] I 45000 bytes"
	frameStatsRe4 := regexp.MustCompile(`frame\s+(\d+)[,]\s+([IPB]),\s+(\d+)\s+bytes`) // "frame 1, I, 45000 bytes"
	allStatsRe := []*regexp.Regexp{frameStatsRe1, frameStatsRe2, frameStatsRe3, frameStatsRe4}

	for scanner.Scan() {
		line := scanner.Text()

		// Debug: if verbose, print any line containing "frame" or "bytes"
		if cfg.Verbose && (strings.Contains(line, "frame") || strings.Contains(line, "bytes")) {
			logMessage("[GST-DBG] %s", line)
		}

		// ---- Identity element ----
		if strings.Contains(line, "frame_size") {
			if m := identitySizeRe.FindStringSubmatch(line); len(m) > 1 {
				if sz, err := strconv.ParseInt(m[1], 10, 64); err == nil && sz > 0 {
					atomic.StoreInt64(&gActualEncodedFrameSize, sz)
					if gFrameProcessor != nil {
						gFrameProcessor.outputBW.AddBytes(sz)
					}
				}
			}
		}

		// ---- Encoder log patterns ----
		for _, re := range allStatsRe {
			if m := re.FindStringSubmatch(line); len(m) > 3 {
				if sz, err := strconv.ParseInt(m[3], 10, 64); err == nil && sz > 0 {
					atomic.StoreInt64(&gActualEncodedFrameSize, sz)
					if gFrameProcessor != nil {
						gFrameProcessor.outputBW.AddBytes(sz)
					}
					break
				}
			}
		}

		// ---- Error / warning ----
		if strings.Contains(line, "ERROR") {
			logMessage("\n[%s] GST ERROR: %s", ts(), line)
		}
		if strings.Contains(line, "WARNING") && cfg.Verbose {
			logMessage("[%s] GST WARN: %s", ts(), line)
		}

		// ---- Pipeline playing detection ----
		if strings.Contains(line, "PLAYING") && !pipelinePlaying {
			pipelinePlaying = true
			logMessage("\n[%s] Pipeline PLAYING — recording started", ts())
			atomic.StoreInt32(&gActive, 1)
			progressMu.Lock()
			progressStarted = time.Now()
			progressMu.Unlock()
		}

		// ---- Generic size lines (ignore) ----
		if m := frameSizeRe.FindStringSubmatch(line); len(m) > 1 {
			// not used
		}

		// ---- Bitrate extraction ----
		if m := bitrateRe.FindStringSubmatch(line); len(m) > 1 {
			if kbps, err := strconv.ParseFloat(m[1], 64); err == nil && kbps > 50 && kbps < 100000 {
				if gFrameProcessor != nil {
					gFrameProcessor.outputBW.SetCurrentBitrate(kbps)
				}
			}
		}

		// ---- Frame counting from encoder logs ----
		if strings.Contains(line, "x265") || strings.Contains(line, "x264") {
			for _, re := range patterns {
				if ms := re.FindStringSubmatch(line); len(ms) > 1 {
					if num, err := strconv.Atoi(ms[1]); err == nil && num > lastFrameNum {
						delta := int64(num - lastFrameNum)
						lastFrameNum = num
						atomic.AddInt64(&gTotalFrames, delta)
						atomic.AddInt64(&gFramesEncoded, delta)
						atomic.AddInt64(&gChunkFrames, delta)
						progressMu.Lock()
						elapsed := time.Since(progressStarted).Seconds()
						if elapsed > 0 {
							fps := float64(atomic.LoadInt64(&gChunkFrames)) / elapsed
							if fps > 0 && fps < 1000 {
								fpsSamples = append(fpsSamples, fps)
								if len(fpsSamples) > 60 {
									fpsSamples = fpsSamples[1:]
								}
								avgFPS = 0
								for _, f := range fpsSamples {
									avgFPS += f
								}
								avgFPS /= float64(len(fpsSamples))
							}
						}
						progressMu.Unlock()
						break
					}
				}
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Progress Display
// ─────────────────────────────────────────────────────────────────────────────

func runProgressDisplay(cfg Config) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	totalFramesTarget := int64(cfg.ChunkTime * cfg.FPS)
	var lastChunkNum int32

	for range ticker.C {
		chunkNum := atomic.LoadInt32(&gChunkNum)
		if chunkNum != lastChunkNum {
			lastChunkNum = chunkNum
			fmt.Println()
		}

		currentFrames := atomic.LoadInt64(&gCaptureChunkFrames)
		pct := math.Min(100, float64(currentFrames)/float64(totalFramesTarget)*100)

		now := time.Now()
		chunkElapsed := now.Sub(gChunkStart).Seconds()
		eta := int(float64(cfg.ChunkTime) - chunkElapsed)
		if eta < 0 {
			eta = 0
		}

		barWidth := 40
		filled := int(float64(barWidth) * pct / 100)
		bar := strings.Repeat("=", filled)
		if filled < barWidth {
			bar += ">" + strings.Repeat(" ", barWidth-filled-1)
		} else {
			bar = strings.Repeat("=", barWidth)
		}

		liveFPS := 0.0
		if gFrameProcessor != nil {
			liveFPS = gFrameProcessor.realTimeStats.GetFPS()
		}

		fmt.Printf("\rCHUNK %d [%s] %5.1f%%  ETA:%4ds  FPS:%.1f  Frames:%d/%d",
			chunkNum, bar, pct, eta, liveFPS, currentFrames, totalFramesTarget)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Chunk Monitor (unchanged)
// ─────────────────────────────────────────────────────────────────────────────

func runChunkMonitor(si StreamInfo, cfg Config) {
	for atomic.LoadInt32(&gActive) == 0 {
		time.Sleep(100 * time.Millisecond)
	}
	ticker := time.NewTicker(time.Duration(cfg.ChunkTime) * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		chunkNum := int(atomic.LoadInt32(&gChunkNum))
		endTime := time.Now()
		logMessage("\n\n[%s] CHUNK %d COMPLETE", ts(), chunkNum)

		gCurrentFramesMu.Lock()
		frames := make([]FrameIndexEntry, len(gCurrentFrames))
		copy(frames, gCurrentFrames)
		frameCount := len(frames)
		gCurrentFrames = make([]FrameIndexEntry, 0)
		gCurrentFramesMu.Unlock()

		iF, pF, bF := 0, 0, 0
		for _, f := range frames {
			switch f.FrameType {
			case "I":
				iF++
			case "P":
				pF++
			case "B":
				bF++
			}
		}

		fileSizeMB := getActualFileSizeWithRetry(chunkNum, 10)

		capturedThisChunk := atomic.SwapInt64(&gCaptureChunkFrames, 0)
		encodedThisChunk := atomic.LoadInt64(&gChunkFrames)

		actualFrames := encodedThisChunk
		if actualFrames == 0 {
			actualFrames = capturedThisChunk
		}

		targetFramesVal := int64(cfg.ChunkTime * cfg.FPS)
		rawMB := float64(si.Width) * float64(si.Height) * 1.5 * float64(targetFramesVal) / (1024 * 1024)

		compPct := 0.0
		if rawMB > 0 && fileSizeMB > 0 {
			compPct = (1 - fileSizeMB/rawMB) * 100
		}

		avgKbps := 0.0
		if fileSizeMB > 0 {
			avgKbps = fileSizeMB * 8 * 1024 / float64(cfg.ChunkTime)
		}

		chunk := ChunkSummary{
			ChunkNumber:    chunkNum,
			StartTime:      gChunkStart.Format("15:04:05"),
			EndTime:        endTime.Format("15:04:05"),
			DurationSec:    float64(cfg.ChunkTime),
			TargetFrames:   targetFramesVal,
			ActualFrames:   actualFrames,
			FileSizeMB:     fileSizeMB,
			RawSizeMB:      rawMB,
			CompressionPct: compPct,
			AvgBitrateKbps: avgKbps,
		}
		printDetailedChunkLog(chunk, si, cfg, actualFrames, int64(iF), int64(pF), int64(bF))
		gMetricsMu.Lock()
		gAllChunks = append(gAllChunks, chunk)
		gMetricsMu.Unlock()

		if frameCount > 0 {
			idx := &ChunkIndex{
				ChunkNumber: chunkNum,
				CameraID:    gCameraID,
				StartTime:   gChunkStart.Format("15:04:05"),
				EndTime:     endTime.Format("15:04:05"),
				FrameRate:   si.FPS,
				TotalFrames: frameCount,
				Frames:      frames,
				Keyframes:   make([]int, 0),
			}
			for _, f := range frames {
				if f.IsKeyframe {
					idx.Keyframes = append(idx.Keyframes, f.FrameNumber)
				}
			}
			if data, err := json.MarshalIndent(idx, "", "  "); err == nil {
				fname := fmt.Sprintf("chunk_%03d_index.json", chunkNum)
				os.WriteFile(fname, data, 0644)
				logMessage("[%s] Saved index: %s (%d frames)", ts(), fname, frameCount)
			}
		}

		logMessage("[%s] Waiting 20s for receiver...", ts())
		for i := 20; i > 0; i-- {
			fmt.Printf("\r[%s] Waiting %2ds... ", ts(), i)
			time.Sleep(time.Second)
		}
		fmt.Println()

		if realIdx, err := fetchFrameDataFromReceiver(chunkNum); err == nil && realIdx != nil {
			logMessage("[%s] Receiver: %d frames, %d keyframes", ts(), realIdx.TotalFrames, len(realIdx.Keyframes))
			if data, err := json.MarshalIndent(realIdx, "", "  "); err == nil {
				os.WriteFile(fmt.Sprintf("chunk_%03d_index_receiver.json", chunkNum), data, 0644)
			}
		}

		atomic.AddInt32(&gChunkNum, 1)
		atomic.StoreInt64(&gChunkFrames, 0)
		gChunkStart = time.Now()
		progressMu.Lock()
		progressStarted = time.Now()
		fpsSamples = make([]float64, 0, 60)
		progressMu.Unlock()

		if gFrameProcessor != nil {
			gFrameProcessor.mu.Lock()
			gFrameProcessor.changeHistory = make([]float64, 0, 120)
			gFrameProcessor.mu.Unlock()
		}
		logMessage("[%s] Chunk %d done, starting %d", ts(), chunkNum, chunkNum+1)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP / Probe Helpers
// ─────────────────────────────────────────────────────────────────────────────

func testReceiverConnection(port int) {
	time.Sleep(3 * time.Second)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
	if err != nil {
		logMessage("\n[%s] Cannot reach receiver port %d", ts(), port)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		logMessage("[%s] Receiver HTTP OK on port %d", ts(), port)
	}
}

func fetchFrameDataFromReceiver(chunkNum int) (*ChunkIndex, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/chunk/%d", receiverPort, chunkNum))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var idx ChunkIndex
	if err := json.NewDecoder(resp.Body).Decode(&idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

func getActualFileSize(chunkNum int) float64 {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/chunk/%d", receiverPort, chunkNum))
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var result struct {
		FileSizeMB float64 `json:"file_size_mb"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0
	}
	return result.FileSizeMB
}

func getActualFileSizeWithRetry(chunkNum, maxRetries int) float64 {
	time.Sleep(3 * time.Second)
	for i := 0; i < maxRetries; i++ {
		if sz := getActualFileSize(chunkNum); sz > 0 {
			return sz
		}
		time.Sleep(time.Second)
	}
	return 0
}

func probeInputStream(cfg Config) StreamInfo {
	var si StreamInfo
	cmd := exec.Command("ffprobe", "-v", "quiet",
		"-rtsp_transport", cfg.RTSPProtocol,
		"-timeout", fmt.Sprintf("%d000000", cfg.RTSPTimeout),
		"-select_streams", "v:0", "-show_streams", "-show_format", "-of", "json", cfg.RTSPURL)
	output, err := cmd.Output()
	if err != nil {
		logMessage("[%s] ffprobe failed: %v — using defaults", ts(), err)
		si.Width = 1920
		si.Height = 1080
		si.FPS = 25
		si.Codec = "h264"
		si.BitRate = 4000000
	} else {
		var result struct {
			Streams []struct {
				Width        int    `json:"width"`
				Height       int    `json:"height"`
				CodecName    string `json:"codec_name"`
				Profile      string `json:"profile"`
				Level        int    `json:"level"`
				PixFmt       string `json:"pix_fmt"`
				RFrameRate   string `json:"r_frame_rate"`
				AvgFrameRate string `json:"avg_frame_rate"`
				BitRate      string `json:"bit_rate"`
			} `json:"streams"`
			Format struct {
				BitRate string `json:"bit_rate"`
			} `json:"format"`
		}
		if err := json.Unmarshal(output, &result); err == nil && len(result.Streams) > 0 {
			s := result.Streams[0]
			si.Width = s.Width
			si.Height = s.Height
			si.Codec = s.CodecName
			si.Profile = s.Profile
			si.PixFmt = s.PixFmt
			if s.Level > 0 {
				si.Level = fmt.Sprintf("%.1f", float64(s.Level)/10.0)
			}
			si.FPS = parseFrameRate(s.AvgFrameRate)
			if si.FPS == 0 {
				si.FPS = parseFrameRate(s.RFrameRate)
			}
			if br, err := strconv.ParseInt(s.BitRate, 10, 64); err == nil && br > 0 {
				si.BitRate = br
			} else if br, err := strconv.ParseInt(result.Format.BitRate, 10, 64); err == nil && br > 0 {
				si.BitRate = br
			}
		}
	}
	if si.Width < si.Height {
		si.Width, si.Height = si.Height, si.Width
	}
	if si.Width == 0 {
		si.Width = 1920
	}
	if si.Height == 0 {
		si.Height = 1080
	}
	if si.FPS == 0 {
		si.FPS = 25
	}
	if si.BitRate == 0 {
		si.BitRate = 4000000
	}
	if si.Codec == "" {
		si.Codec = "h264"
	}
	rawBytes := float64(si.Width) * float64(si.Height) * 1.5
	si.RawFrameKB = rawBytes / 1024
	si.RawMbps = rawBytes * si.FPS * 8 / 1_000_000
	si.InputMbps = float64(si.BitRate) / 1_000_000
	si.InputFrameKB = float64(si.BitRate) / 8 / si.FPS / 1024
	return si
}

func parseFrameRate(s string) float64 {
	if s == "" || s == "0/0" {
		return 0
	}
	parts := strings.Split(s, "/")
	if len(parts) == 2 {
		num, _ := strconv.ParseFloat(parts[0], 64)
		den, _ := strconv.ParseFloat(parts[1], 64)
		if den > 0 {
			fps := num / den
			if fps > 0 && fps <= 120 {
				return fps
			}
		}
	}
	fps, _ := strconv.ParseFloat(s, 64)
	return fps
}

// ─────────────────────────────────────────────────────────────────────────────
// Health / Metrics / Shutdown
// ─────────────────────────────────────────────────────────────────────────────

func monitorPipelineHealth(cfg Config) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	lastFrameCount := int64(0)
	stallCount := 0
	for range ticker.C {
		cur := atomic.LoadInt64(&gFramesEncoded)
		if atomic.LoadInt32(&gActive) == 1 {
			if cur == lastFrameCount && cur > 0 {
				stallCount++
				if stallCount >= 3 {
					logMessage("\n[%s] WARNING: No new frames for 30s — possible stall", ts())
				}
			} else {
				stallCount = 0
			}
			lastFrameCount = cur
		}
		client := &http.Client{Timeout: 2 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", receiverPort))
		if err != nil && atomic.LoadInt32(&gActive) == 1 {
			logMessage("[%s] Receiver not reachable", ts())
		} else if resp != nil {
			resp.Body.Close()
		}
	}
}

func runMetricsWriter(f string) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		saveMetrics(f)
	}
}

func saveMetrics(f string) {
	gMetricsMu.RLock()
	defer gMetricsMu.RUnlock()
	if d, err := json.MarshalIndent(gAllChunks, "", "  "); err == nil {
		tmp := f + ".tmp"
		if os.WriteFile(tmp, d, 0644) == nil {
			os.Rename(tmp, f)
		}
	}
}

func saveFinalMetrics(f string) {
	gMetricsMu.Lock()
	defer gMetricsMu.Unlock()
	if d, err := json.MarshalIndent(gAllChunks, "", "  "); err == nil {
		os.WriteFile(f, d, 0644)
		logMessage("[%s] Metrics: %s", ts(), f)
	}
}

func gracefulShutdown() {
	gMu.Lock()
	defer gMu.Unlock()
	if gCmd != nil && gCmd.Process != nil {
		gCmd.Process.Signal(syscall.SIGINT)
		done := make(chan error, 1)
		go func() { done <- gCmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			gCmd.Process.Kill()
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Print Helpers
// ─────────────────────────────────────────────────────────────────────────────

func printInputStreamInfo(si StreamInfo) {
	consoleMu.Lock()
	defer consoleMu.Unlock()
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("INPUT STREAM")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("  Resolution: %dx%d @ %.2ffps\n", si.Width, si.Height, si.FPS)
	fmt.Printf("  Codec:      %s (%s L%s)\n", si.Codec, si.Profile, si.Level)
	fmt.Printf("  Bitrate:    %.1f Mbps | Raw frame: %.1f KB\n", si.InputMbps, si.RawFrameKB)
	fmt.Println(strings.Repeat("=", 60))
}

func printEncodingPlan(si StreamInfo, cfg Config) {
	outW, outH := cfg.OutputWidth, cfg.OutputHeight
	previewW, previewH := calcPreviewSize(si.Width, si.Height, cfg.PreviewScale)
	codecName := "H.264"
	if cfg.Encoder == "x265enc" {
		codecName = "H.265/HEVC"
	}
	consoleMu.Lock()
	defer consoleMu.Unlock()
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("ENCODING PLAN")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("  Camera:  %dx%d @ %.2ffps | %s\n", si.Width, si.Height, si.FPS, si.Codec)
	fmt.Printf("  Output:  %dx%d @ %dfps | %s | %dkbps\n", outW, outH, cfg.FPS, codecName, cfg.Bitrate)
	if outW == si.Width && outH == si.Height {
		fmt.Printf("  Scale:   Native (no scaling)\n")
	} else {
		fmt.Printf("  Scale:   From %dx%d\n", si.Width, si.Height)
	}
	fmt.Printf("  Preset:  %s  Tune: %s  Keyint: %d  B-frames: %d\n", cfg.Preset, cfg.Tune, cfg.KeyInt, cfg.BFrames)
	if cfg.Preview {
		fmt.Printf("  Windows: Input %dx%d | Output %dx%d | Analysis %dx%d\n",
			si.Width, si.Height, outW, outH, previewW, previewH)
		fmt.Printf("  Scrolling frame-size panel in top-right of each window\n")
	}
	fmt.Println(strings.Repeat("=", 60) + "\n")
}

func printBanner(cfg Config) {
	codecName := "H.264"
	if cfg.Encoder == "x265enc" {
		codecName = "H.265/HEVC"
	}
	consoleMu.Lock()
	defer consoleMu.Unlock()
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("DELTA SENDER — RTSP → SRT + FRAME SIZE SCROLLING")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("  %dfps | %dkbps | %s | Encoder: %s\n", cfg.FPS, cfg.Bitrate, codecName, cfg.Encoder)
	fmt.Printf("  Top-right scrolling panel: FRAME / SIZE / RATIO per frame\n")
	fmt.Println(strings.Repeat("=", 60) + "\n")
}

func printDetailedChunkLog(chunk ChunkSummary, si StreamInfo, cfg Config, actualFrames, iF, pF, bF int64) {
	outW, outH := si.Width, si.Height
	if cfg.OutputWidth > 0 && cfg.OutputHeight > 0 {
		outW, outH = cfg.OutputWidth, cfg.OutputHeight
	}
	codecName := "H.264"
	if cfg.Encoder == "x265enc" {
		codecName = "H.265/HEVC"
	}
	consoleMu.Lock()
	defer consoleMu.Unlock()
	fmt.Println("\n" + strings.Repeat("=", 70))
	fmt.Printf(" CHUNK %d ANALYSIS\n", chunk.ChunkNumber)
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("\n  Resolution:  %dx%d → %dx%d | %s\n", si.Width, si.Height, outW, outH, codecName)
	fmt.Printf("  Duration:    %.1fs  Target: %d kbps  Actual: %.0f kbps\n", chunk.DurationSec, cfg.Bitrate, chunk.AvgBitrateKbps)
	fmt.Printf("  Frames:      %d captured (target %d)\n", actualFrames, chunk.TargetFrames)
	if actualFrames > 0 {
		fmt.Printf("  Frame types: I:%d (%.0f%%)  P:%d (%.0f%%)  B:%d (%.0f%%)\n",
			iF, float64(iF)*100/float64(actualFrames), pF, float64(pF)*100/float64(actualFrames), bF, float64(bF)*100/float64(actualFrames))
	}
	fmt.Printf("  File:        %.2f MB  Raw: %.2f MB  Compression: %.1f%%\n", chunk.FileSizeMB, chunk.RawSizeMB, chunk.CompressionPct)
	if chunk.RawSizeMB > 0 && chunk.FileSizeMB > 0 {
		fmt.Printf("  Ratio:       %.0f:1\n", chunk.RawSizeMB/chunk.FileSizeMB)
	}
	if chunk.AvgBitrateKbps > 0 && outW > 0 && outH > 0 {
		bpp := chunk.AvgBitrateKbps * 1000 / (float64(outW*outH) * float64(cfg.FPS))
		psnr := math.Min(50, math.Max(20, 30+10*math.Log10(bpp*1000)))
		fmt.Printf("  Est PSNR:    %.1f dB\n", psnr)
	}
	fmt.Printf("  Time:        %s → %s\n", chunk.StartTime, chunk.EndTime)
	fmt.Println(strings.Repeat("=", 70) + "\n")
}

func printFinalSummary(si StreamInfo, cfg Config) {
	gMetricsMu.RLock()
	defer gMetricsMu.RUnlock()
	totalFrames := atomic.LoadInt64(&gTotalFrames)
	totalDuration := time.Since(gSessionStart).Seconds()
	consoleMu.Lock()
	defer consoleMu.Unlock()
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("SESSION FINAL SUMMARY")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("  Duration:     %.1f seconds\n", totalDuration)
	fmt.Printf("  Chunks:       %d\n", len(gAllChunks))
	fmt.Printf("  Total Frames: %d\n", totalFrames)
	if totalDuration > 0 {
		fmt.Printf("  Average FPS:  %.1f\n", float64(totalFrames)/totalDuration)
	}
	if len(gAllChunks) > 0 {
		var totalSize float64
		for _, c := range gAllChunks {
			totalSize += c.FileSizeMB
		}
		fmt.Printf("  Total Data:   %.2f MB\n", totalSize)
	}
	fmt.Println(strings.Repeat("=", 60) + "\n")
}

func printHelp() {
	fmt.Print(`
Usage: ./delta_sender --rtsp <url> --srt <uri> [options]

REQUIRED:
  --rtsp URL       RTSP camera URL
  --srt URI        SRT destination

VIDEO:
  --encoder STR    x264enc or x265enc (default: x265enc)
  --fps N          Output FPS (default: 25)
  --bitrate N      Bitrate kbps (default: 800)
  --preset STR     Encoder preset (default: ultrafast)
  --tune STR       Encoder tune (default: zerolatency)
  --bframes N      B-frames (default: 0)
  --keyint N       Keyframe interval (default: 100)
  --width N        Output width (default: auto)
  --height N       Output height (default: auto)

STREAMING:
  --chunk-time N   Chunk seconds (default: 120)
  --camera-id STR  Camera ID (default: camera1)
  --receiver-port  Receiver HTTP port (default: 8080)
  --srt-latency N  SRT latency ms (default: 120)

PREVIEW:
  --preview        Enable 3-window preview (default: true)
  --preview-scale  Analysis max dimension (default: 720)

  --verbose        Verbose output
  --help           Show help

OVERLAY PANELS (each window):
  Top-left:  BW / FPS / RAW / CHG / SZF (frame size) / RAT (ratio)
  Top-right: Scrolling log — newest frame at top
             Format: F##### SIZE RATIO
             INPUT  header: blue   | 14 lines visible
             COMP   header: amber  | 14 lines visible
             ANALYSIS header: green | 14 lines visible
`)
}

func ts() string { return time.Now().Format("15:04:05") }
