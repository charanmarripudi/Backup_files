// Package analyzer provides ffprobe-based video chunk analysis.
// It extracts stream info, per-frame statistics (I/P/B), compression ratios,
// bitrate measurements, and quality estimates (PSNR, SSIM).
package analyzer

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

type FrameDetail struct {
	FrameNumber    int     `json:"frame_number"`
	Type           string  `json:"type"`
	SizeBytes      int64   `json:"size_bytes"`
	RawSizeBytes   int64   `json:"raw_size_bytes"`
	BitrateKbps    float64 `json:"bitrate_kbps"`
	CompressionPct float64 `json:"compression_pct"`
	QP             int     `json:"qp"`
	PSNR           float64 `json:"psnr_estimate"`
	Pts            float64 `json:"pts"`
	RawSizeKB      float64 `json:"raw_size_kb"`
	EncodedSizeKB  float64 `json:"encoded_size_kb"`
	SavingsKB      float64 `json:"savings_kb"`
	SavingsPct     float64 `json:"savings_pct"`
	Timestamp      string  `json:"timestamp"`
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

type ChunkStats struct {
	ChunkNumber int     `json:"chunk_number"`
	CameraID    string  `json:"camera_id"`
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

	PSNR         float64 `json:"psnr_estimate"`
	SSIM         float64 `json:"ssim_estimate"`
	AvgQP        float64 `json:"avg_qp"`
	BitsPerPixel float64 `json:"bits_per_pixel"`

	RecordingDuration float64 `json:"recording_duration_sec"`
	FileWriteTime     float64 `json:"file_write_time_sec"`
	AnalysisDuration  float64 `json:"analysis_duration_sec"`
	TotalChunkTime    float64 `json:"total_chunk_time_sec"`

	StartTimestamp            string `json:"start_timestamp"`
	FileReadyTimestamp        string `json:"file_ready_timestamp"`
	AnalysisCompleteTimestamp string `json:"analysis_complete_timestamp"`

	Frames         []FrameDetail `json:"frames,omitempty"`
	AnalysisTimeMs int64         `json:"analysis_time_ms"`

	Index *ChunkIndex `json:"-"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Public API
// ─────────────────────────────────────────────────────────────────────────────

// Analyze runs a full ffprobe analysis on an MP4 chunk file and returns stats.
// includeFrames controls whether per-frame FrameDetail slice is populated.
func Analyze(
	filePath string,
	chunkNum int,
	cameraID string,
	recordStart, recordEnd, fileAvailableTime time.Time,
	chunkTimeSec int,
	includeFrames bool,
) *ChunkStats {
	analysisStart := time.Now()

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		fmt.Printf("[%s][%s] Cannot analyze chunk %d: %v\n", ts(), cameraID, chunkNum, err)
		return nil
	}
	if fileInfo.Size() < 512*1024 {
		fmt.Printf("[%s][%s] Chunk %d too small (%.2f KB), skipping\n",
			ts(), cameraID, chunkNum, float64(fileInfo.Size())/1024)
		return nil
	}

	fmt.Printf("\n[%s][%s] ANALYZING CHUNK %d (%.2f MB)...\n",
		ts(), cameraID, chunkNum, float64(fileInfo.Size())/(1024*1024))

	recordingDur := recordEnd.Sub(recordStart).Seconds()
	fileWriteTime := fileAvailableTime.Sub(recordEnd).Seconds()

	stats := &ChunkStats{
		ChunkNumber: chunkNum,
		CameraID:    cameraID,
		Filename:    filepath.Base(filePath),
		FileSizeMB:  float64(fileInfo.Size()) / (1024 * 1024),
		StartTime:   recordStart.Format("15:04:05"),
		EndTime:     recordEnd.Format("15:04:05"),

		RecordingDuration: recordingDur,
		FileWriteTime:     fileWriteTime,

		StartTimestamp:     recordStart.Format("15:04:05.000"),
		FileReadyTimestamp: fileAvailableTime.Format("15:04:05.000"),

		IFrameSizes:    []float64{},
		PFrameSizes:    []float64{},
		BFrameSizes:    []float64{},
		IFrameRawSizes: []float64{},
		PFrameRawSizes: []float64{},
		BFrameRawSizes: []float64{},
	}

	probeStreamInfo(stats, filePath, chunkTimeSec)
	frames := probeFrameInfo(stats, filePath, chunkTimeSec, includeFrames)
	calculateCompression(stats)
	estimateQuality(stats)

	if includeFrames {
		stats.Frames = frames
	}

	// Build frame index from analysis results
	if len(frames) > 0 {
		index := &ChunkIndex{
			ChunkNumber: chunkNum,
			CameraID:    cameraID,
			StartTime:   recordStart.Format("15:04:05"),
			EndTime:     recordEnd.Format("15:04:05"),
			FrameRate:   stats.FrameRate,
			TotalFrames: stats.TotalFrames,
			Frames:      make([]FrameIndexEntry, len(frames)),
			Keyframes:   []int{},
		}
		for i, f := range frames {
			isKF := f.Type == "I"
			index.Frames[i] = FrameIndexEntry{
				FrameNumber: f.FrameNumber,
				FrameType:   f.Type,
				PTS:         f.Pts,
				Size:        int(f.SizeBytes),
				IsKeyframe:  isKF,
			}
			if isKF {
				index.Keyframes = append(index.Keyframes, f.FrameNumber)
			}
		}
		stats.Index = index
	}

	analysisComplete := time.Now()
	stats.AnalysisCompleteTimestamp = analysisComplete.Format("15:04:05.000")
	stats.AnalysisDuration = analysisComplete.Sub(analysisStart).Seconds()
	stats.TotalChunkTime = analysisComplete.Sub(recordStart).Seconds()
	stats.AnalysisTimeMs = time.Since(analysisStart).Milliseconds()

	return stats
}

// WaitForFileReady waits until the MP4 file is fully written (moov atom present).
func WaitForFileReady(filePath string, cameraID string) {
	minSize := int64(1 * 1024 * 1024)
	maxWait := 90 * time.Second
	deadline := time.Now().Add(maxWait)

	fmt.Printf("[%s][%s] Waiting for %s to be ready...\n", ts(), cameraID, filepath.Base(filePath))

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
			fmt.Printf("[%s][%s] File too small (%.1f KB), waiting...\n",
				ts(), cameraID, float64(sz)/1024)
			time.Sleep(2 * time.Second)
			continue
		}
		if sz == lastSize {
			stableCount++
			if stableCount >= 3 {
				if verifyMP4Readable(filePath) {
					fmt.Printf("[%s][%s] File ready: %.2f MB\n",
						ts(), cameraID, float64(sz)/(1024*1024))
					return
				}
				stableCount = 0
			}
		} else {
			stableCount = 0
			fmt.Printf("[%s][%s] File growing: %.2f MB\n",
				ts(), cameraID, float64(sz)/(1024*1024))
		}
		lastSize = sz
		time.Sleep(2 * time.Second)
	}
	fmt.Printf("[%s][%s] Timeout waiting for file, proceeding anyway\n", ts(), cameraID)
}

// VerifyMP4Readable checks if the moov atom is present by running a quick ffprobe.
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

// ─────────────────────────────────────────────────────────────────────────────
// ffprobe helpers
// ─────────────────────────────────────────────────────────────────────────────

func probeStreamInfo(stats *ChunkStats, filePath string, chunkTimeSec int) {
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
		fmt.Printf("[%s][%s] ffprobe attempt %d failed: %v — retrying in 3s\n",
			ts(), stats.CameraID, attempt, err)
		time.Sleep(3 * time.Second)
	}

	if err != nil {
		fmt.Printf("[%s][%s] ffprobe failed for chunk %d: %v\n",
			ts(), stats.CameraID, stats.ChunkNumber, err)
		if stats.DurationSec == 0 {
			stats.DurationSec = float64(chunkTimeSec)
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
		fmt.Printf("[%s][%s] No video stream in chunk %d\n",
			ts(), stats.CameraID, stats.ChunkNumber)
		if stats.DurationSec == 0 {
			stats.DurationSec = float64(chunkTimeSec)
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
		stats.DurationSec = float64(chunkTimeSec)
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

	fmt.Printf("[%s][%s] Stream: %dx%d @ %.2ffps, %s %s, %.2f sec, %d frames\n",
		ts(), stats.CameraID, stats.Width, stats.Height, stats.FrameRate,
		stats.Codec, stats.Profile, stats.DurationSec, stats.TotalFrames)
}

func probeFrameInfo(stats *ChunkStats, filePath string, chunkTimeSec int, includeFrames bool) []FrameDetail {
	var frames []FrameDetail

	fmt.Printf("[%s][%s] Probing frame details...\n", ts(), stats.CameraID)

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
		fmt.Printf("[%s][%s] Frame probe attempt %d failed: %v — retrying in 3s\n",
			ts(), stats.CameraID, attempt, err)
		time.Sleep(3 * time.Second)
	}

	if err != nil {
		fmt.Printf("[%s][%s] Frame probe failed, using fallback\n", ts(), stats.CameraID)
		probeFrameTypesFallback(stats, filePath, chunkTimeSec)
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
		fmt.Printf("[%s][%s] Frame parse failed, using fallback: %v\n", ts(), stats.CameraID, err)
		probeFrameTypesFallback(stats, filePath, chunkTimeSec)
		return frames
	}

	rawBytesPerFrame := int64(stats.Width * stats.Height * 3 / 2)
	if rawBytesPerFrame == 0 {
		rawBytesPerFrame = int64(1920 * 1080 * 3 / 2)
	}

	var totalBytes, iBytes, pBytes, bBytes int64
	var iCount, pCount, bCount int
	var peakBitrate float64
	minBitrate := math.MaxFloat64

	stats.IFrameSizes = []float64{}
	stats.PFrameSizes = []float64{}
	stats.BFrameSizes = []float64{}
	stats.IFrameRawSizes = []float64{}
	stats.PFrameRawSizes = []float64{}
	stats.BFrameRawSizes = []float64{}

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

		psnr := 30.0 + 10*math.Log10(bitrate/100+1)
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
		stats.BeforeEncodingMB = float64(rawBytesPerFrame) * float64(stats.TotalFrames) / (1024 * 1024)
	}

	fmt.Printf("[%s][%s] Frames: %d total (%d I, %d P, %d B), avg %.0f kbps\n",
		ts(), stats.CameraID, stats.TotalFrames,
		stats.IFrames, stats.PFrames, stats.BFrames, stats.AvgBitrateKbps)

	return frames
}

func probeFrameTypesFallback(stats *ChunkStats, filePath string, chunkTimeSec int) {
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
		if stats.DurationSec > 0 {
			stats.AvgBitrateKbps = stats.FileSizeMB * 8 * 1024 / stats.DurationSec
		}
		rawBytesPerFrame := int64(stats.Width * stats.Height * 3 / 2)
		if rawBytesPerFrame > 0 {
			stats.BeforeEncodingMB = float64(rawBytesPerFrame) * float64(stats.TotalFrames) / (1024 * 1024)
			rawKB := float64(rawBytesPerFrame) / 1024
			stats.IFrameRawSizes = make([]float64, stats.IFrames)
			stats.PFrameRawSizes = make([]float64, stats.PFrames)
			stats.BFrameRawSizes = make([]float64, stats.BFrames)
			for i := range stats.IFrameRawSizes {
				stats.IFrameRawSizes[i] = rawKB
			}
			for i := range stats.PFrameRawSizes {
				stats.PFrameRawSizes[i] = rawKB
			}
			for i := range stats.BFrameRawSizes {
				stats.BFrameRawSizes[i] = rawKB
			}
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
		stats.PSNR = 30.0 + 10*math.Log10(stats.BitsPerPixel*1000+1)
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
		stats.AvgQP = 50.0 - 10*math.Log10(stats.AvgBitrateKbps/complexity+1)
		if stats.AvgQP < 10 {
			stats.AvgQP = 10
		}
		if stats.AvgQP > 51 {
			stats.AvgQP = 51
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Public Display Functions (matching enhanced receiver)
// ─────────────────────────────────────────────────────────────────────────────

// PrintChunkReport prints a detailed chunk analysis report
func PrintChunkReport(stats *ChunkStats) {
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

// PrintFrameDetails prints detailed frame type compression information
func PrintFrameDetails(stats *ChunkStats) {
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
		avgRaw := average(sizes)
		avgEnc := average(rawSizes)
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

// PrintFrameChanges prints detailed frame-to-frame change analysis
func PrintFrameChanges(stats *ChunkStats) {
	if stats == nil || stats.Frames == nil || len(stats.Frames) == 0 {
		return
	}

	fmt.Println("\n" + strings.Repeat("═", 120))
	fmt.Printf(" FRAME-TO-FRAME CHANGE ANALYSIS (Chunk %d)\n", stats.ChunkNumber)
	fmt.Println(strings.Repeat("═", 120))

	fmt.Printf("%-6s %-5s %-8s %-10s %-10s %-10s %-10s %-8s %-8s %-8s\n",
		"Frame", "Type", "Size(KB)", "Δ Size", "Δ%", "Bitrate", "Δ Bitrate", "PSNR", "Δ PSNR", "Time(ms)")
	fmt.Println(strings.Repeat("─", 120))

	var prevFrame *FrameDetail
	frameCount := 0
	maxFramesToShow := 30

	for i := range stats.Frames {
		frame := &stats.Frames[i]
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
		prevFrame = frame
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

// SaveFrameChangesReadable saves frame changes to a readable text file
func SaveFrameChangesReadable(stats *ChunkStats, chunkNum int) {
	if stats == nil || stats.Frames == nil {
		return
	}

	changesFile := fmt.Sprintf("chunk_%02d_frame_changes.txt", chunkNum)
	file, err := os.Create(changesFile)
	if err != nil {
		return
	}
	defer file.Close()

	fmt.Fprintf(file, "%s\n", strings.Repeat("=", 100))
	fmt.Fprintf(file, "FRAME-TO-FRAME CHANGE ANALYSIS - Chunk %d\n", chunkNum)
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
	fmt.Printf("[%s] Frame changes saved: %s\n", ts(), changesFile)
}

// SaveFrameChangesCSV saves frame changes to a CSV file
func SaveFrameChangesCSV(stats *ChunkStats, chunkNum int) {
	if stats == nil || stats.Frames == nil {
		return
	}

	csvFile := fmt.Sprintf("chunk_%02d_frame_changes.csv", chunkNum)
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
	fmt.Printf("[%s] CSV saved: %s\n", ts(), csvFile)
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

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

func ts() string {
	return time.Now().Format("15:04:05")
}