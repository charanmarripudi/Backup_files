package sender

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"camera-system/internal/config"
	"camera-system/internal/mq"
)

var (
	muProgress          sync.Mutex
	chunkStartTime      time.Time
	gActive             int32
	gCaptureChunkFrames int64
	gChunkFrames        int64
	progressStarted     time.Time
	currentFPS          float64
	fpsSamples          []float64
)

// Run builds and executes the GStreamer sender pipeline for one camera.
func Run(ctx context.Context, cfg *config.Config, cam config.CameraConfig, mqClient *mq.Client) error {
	srtURI := cfg.SRTCallerURI(cam)
	logPrefix := logPfx(cam)

	// Ensure streamid and latency are set
	if !strings.Contains(srtURI, "streamid=") {
		if strings.Contains(srtURI, "?") {
			srtURI += "&streamid=" + cam.ID
		} else {
			srtURI += "?streamid=" + cam.ID
		}
	}
	if !strings.Contains(srtURI, "latency=") {
		if strings.Contains(srtURI, "?") {
			srtURI += "&latency=200"
		} else {
			srtURI += "?latency=200"
		}
	}

	// Original working pipeline with scaling and caps
	pipeline := fmt.Sprintf(
		`gst-launch-1.0 -e -v `+
			`rtspsrc location="%s" protocols=tcp latency=0 timeout=10000000 do-retransmission=true ! `+
			`rtph264depay ! queue max-size-buffers=5 leaky=downstream ! `+
			`h264parse ! avdec_h264 ! `+
			`videoconvert ! videoscale ! videorate ! `+
			`video/x-raw,width=%d,height=%d,framerate=%d/1,pixel-aspect-ratio=1/1 ! `+
			`x265enc bitrate=%d key-int-max=%d speed-preset=%s tune=%s log-level=2 ! `+
			`identity name=frame_size silent=false ! `+
			`h265parse ! mpegtsmux ! `+
			`srtsink uri="%s" sync=false wait-for-connection=false latency=200`,
		cam.RTSPURL, cam.Width, cam.Height, cam.FPS,
		cam.Bitrate, cam.KeyInt, cam.Preset, cam.Tune,
		srtURI,
	)

	fmt.Printf("%s Starting GStreamer sender\n", logPrefix)
	fmt.Printf("%s RTSP  : %s\n", logPrefix, cam.RTSPURL)
	fmt.Printf("%s SRT   : %s\n", logPrefix, srtURI)
	fmt.Printf("%s Codec : H265  %dkbps  %dfps  %dx%d\n",
		logPrefix, cam.Bitrate, cam.FPS, cam.Width, cam.Height)
	fmt.Printf("%s Pipeline: %s\n\n", logPrefix, pipeline)

	cmd := exec.Command("bash", "-c", pipeline)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start GStreamer: %w", err)
	}
	fmt.Printf("%s GStreamer started (PID %d)\n", logPrefix, cmd.Process.Pid)

	// Reset counters
	atomic.StoreInt64(&gCaptureChunkFrames, 0)
	atomic.StoreInt64(&gChunkFrames, 0)
	atomic.StoreInt32(&gActive, 1)
	muProgress.Lock()
	chunkStartTime = time.Now()
	progressStarted = time.Now()
	currentFPS = 0
	fpsSamples = make([]float64, 0, 60)
	muProgress.Unlock()

	// Monitor output
	go monitorSenderOutput(stdout, cam)
	go monitorSenderOutput(stderr, cam)

	// Start progress display
	go runProgressDisplay(cam, cfg)

	// Wait for command to exit or context cancellation
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		atomic.StoreInt32(&gActive, 0)
		if err != nil {
			return fmt.Errorf("GStreamer exited: %w", err)
		}
		return nil
	case <-ctx.Done():
		// Context canceled – kill the process
		cmd.Process.Kill()
		<-done // wait for exit
		atomic.StoreInt32(&gActive, 0)
		return ctx.Err()
	}
}

func monitorSenderOutput(pipe io.ReadCloser, cam config.CameraConfig) {
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)

	// Regex to capture size in bytes from identity chain messages: (12345 bytes, ...)
	sizeRe := regexp.MustCompile(`\((\d+)\s+bytes`)
	pipelinePlaying := false

	for scanner.Scan() {
		line := scanner.Text()

		// Optional: print all lines for debugging (uncomment if needed)
		// fmt.Printf("[%s] GST: %s\n", cam.ID, line)

		// Capture frame size from identity chain messages
		if m := sizeRe.FindStringSubmatch(line); len(m) > 1 {
			if _, err := strconv.ParseInt(m[1], 10, 64); err == nil {
				atomic.AddInt64(&gCaptureChunkFrames, 1)
				atomic.AddInt64(&gChunkFrames, 1)

				// Update FPS
				muProgress.Lock()
				now := time.Now()
				elapsed := now.Sub(progressStarted).Seconds()
				if elapsed > 0 {
					fps := float64(atomic.LoadInt64(&gChunkFrames)) / elapsed
					if fps > 0 && fps < 1000 {
						fpsSamples = append(fpsSamples, fps)
						if len(fpsSamples) > 60 {
							fpsSamples = fpsSamples[1:]
						}
						avgFPS := 0.0
						for _, f := range fpsSamples {
							avgFPS += f
						}
						avgFPS /= float64(len(fpsSamples))
						currentFPS = avgFPS
					}
				}
				muProgress.Unlock()
			}
		}

		// Check for pipeline playing
		if !pipelinePlaying && strings.Contains(line, "PLAYING") {
			pipelinePlaying = true
			fmt.Printf("\n[%s] GStreamer pipeline PLAYING — sending stream\n", cam.ID)
		}

		// Print errors
		if strings.Contains(line, "ERROR:") {
			fmt.Printf("\n[%s] GStreamer ERROR: %s\n", cam.ID, line)
		}
	}
}

func runProgressDisplay(cam config.CameraConfig, cfg *config.Config) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	chunkTime := cfg.Server.ChunkTime
	targetFrames := int64(chunkTime * cam.FPS)
	lastProgress := time.Now()
	lastChunkNum := int64(0)

	for range ticker.C {
		if atomic.LoadInt32(&gActive) == 0 {
			continue
		}

		totalFrames := atomic.LoadInt64(&gCaptureChunkFrames)
		if totalFrames == 0 {
			if time.Since(lastProgress) > 2*time.Second {
				fmt.Printf("\r[%s] Waiting for frames...", cam.ID)
				lastProgress = time.Now()
			}
			continue
		}

		chunkNum := (totalFrames / targetFrames) + 1
		framesInChunk := totalFrames % targetFrames
		pct := math.Min(100, float64(framesInChunk)/float64(targetFrames)*100)

		muProgress.Lock()
		fps := currentFPS
		muProgress.Unlock()

		// When chunk number changes, print completion line for the previous chunk
		if chunkNum != lastChunkNum && lastChunkNum != 0 {
			duration := float64(targetFrames) / fps
			fmt.Printf("\n[%s] CHUNK %d COMPLETED — %d frames in %.2f sec (%.1f fps)\n",
				cam.ID, lastChunkNum, targetFrames, duration, fps)
		}
		lastChunkNum = chunkNum

		remainingFrames := targetFrames - framesInChunk
		eta := int(float64(remainingFrames) / fps)
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

		// Update the current chunk line (overwrites the same line)
		fmt.Printf("\r[%s] CHUNK %d [%s] %5.1f%%  FPS: %.1f  ETA: %3ds  Frames: %d/%d",
			cam.ID, chunkNum, bar, pct, fps, eta, framesInChunk, targetFrames)
	}

}



// RunWithRestart keeps restarting the sender pipeline if it crashes.
func RunWithRestart(ctx context.Context, cfg *config.Config, cam config.CameraConfig, backoffMax time.Duration, mqClient *mq.Client) {
	logPrefix := logPfx(cam)
	backoff := 5 * time.Second

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("%s Stopping sender (context done)\n", logPrefix)
			return
		default:
		}

		if mqClient != nil {
			if err := mqClient.PublishStatus(cam.ID, "online", cam.FPS, cam.Bitrate); err != nil {
				fmt.Printf("%s Failed to publish status: %v\n", logPrefix, err)
			}
		}

		fmt.Printf("%s Starting sender...\n", logPrefix)
		start := time.Now()
		err := Run(ctx, cfg, cam, mqClient)
		elapsed := time.Since(start)

		if mqClient != nil && ctx.Err() == nil {
			if pubErr := mqClient.PublishStatus(cam.ID, "offline", 0, 0); pubErr != nil {
				fmt.Printf("%s Failed to publish offline status: %v\n", logPrefix, pubErr)
			}
		}

		if err != nil && err != context.Canceled {
			fmt.Printf("%s Sender exited after %.1fs: %v\n", logPrefix, elapsed.Seconds(), err)
		} else if err == context.Canceled {
			fmt.Printf("%s Sender stopped by command\n", logPrefix)
			return
		} else {
			fmt.Printf("%s Sender exited cleanly after %.1fs\n", logPrefix, elapsed.Seconds())
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









// // Working but chunk bar is not updating
// package sender

// import (
// 	"bufio"
// 	"fmt"
// 	"io"
// 	"math"
// 	"os/exec"
// 	"regexp"
// 	"strconv"
// 	"strings"
// 	"sync"
// 	"sync/atomic"
// 	"syscall"
// 	"time"

// 	"camera-system/internal/config"
// )

// var (
// 	muProgress          sync.Mutex
// 	chunkStartTime      time.Time
// 	lastFrameNum        int64
// 	gActive             int32
// 	gCaptureFrames      int64
// 	gCaptureChunkFrames int64
// 	gChunkFrames        int64
// 	gTotalFrames        int64
// 	gChunkStart         time.Time
// 	progressStarted     time.Time
// 	currentFPS          float64
// 	lastFrameTime       time.Time
// 	lastFrameCount      int64
// 	fpsSamples          []float64
// )

// // Run builds and executes the GStreamer sender pipeline for one camera.
// func Run(cfg *config.Config, cam config.CameraConfig) error {
// 	srtURI := cfg.SRTCallerURI(cam)
// 	logPrefix := logPfx(cam)

// 	// Add streamid if not present
// 	if !strings.Contains(srtURI, "streamid=") {
// 		if strings.Contains(srtURI, "?") {
// 			srtURI += "&streamid=" + cam.ID
// 		} else {
// 			srtURI += "?streamid=" + cam.ID
// 		}
// 	}
// 	if !strings.Contains(srtURI, "latency=") {
// 		if strings.Contains(srtURI, "?") {
// 			srtURI += "&latency=200"
// 		} else {
// 			srtURI += "?latency=200"
// 		}
// 	}

// 	// Build the GStreamer pipeline
// 	pipeline := fmt.Sprintf(
// 		`gst-launch-1.0 -e -v `+
// 			`rtspsrc location="%s" protocols=tcp latency=0 timeout=10000000 do-retransmission=true ! `+
// 			`rtph264depay ! queue max-size-buffers=5 leaky=downstream ! `+
// 			`h264parse ! avdec_h264 ! `+
// 			`videoconvert ! videoscale ! videorate ! `+
// 			`video/x-raw,width=%d,height=%d,framerate=%d/1,pixel-aspect-ratio=1/1 ! `+
// 			`x265enc bitrate=%d key-int-max=%d speed-preset=%s tune=%s log-level=2 ! `+
// 			`identity name=frame_size silent=false ! `+
// 			`h265parse ! mpegtsmux ! `+
// 			`srtsink uri="%s" sync=false wait-for-connection=false latency=200`,
// 		cam.RTSPURL, cam.Width, cam.Height, cam.FPS,
// 		cam.Bitrate, cam.KeyInt, cam.Preset, cam.Tune,
// 		srtURI,
// 	)

// 	fmt.Printf("%s Starting GStreamer sender\n", logPrefix)
// 	fmt.Printf("%s RTSP  : %s\n", logPrefix, cam.RTSPURL)
// 	fmt.Printf("%s SRT   : %s\n", logPrefix, srtURI)
// 	fmt.Printf("%s Codec : H265  %dkbps  %dfps  %dx%d\n",
// 		logPrefix, cam.Bitrate, cam.FPS, cam.Width, cam.Height)
// 	fmt.Printf("%s Pipeline: %s\n\n", logPrefix, pipeline)

// 	cmd := exec.Command("bash", "-c", pipeline)
// 	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

// 	stdout, err := cmd.StdoutPipe()
// 	if err != nil {
// 		return fmt.Errorf("stdout pipe: %w", err)
// 	}
// 	stderr, err := cmd.StderrPipe()
// 	if err != nil {
// 		return fmt.Errorf("stderr pipe: %w", err)
// 	}

// 	if err := cmd.Start(); err != nil {
// 		return fmt.Errorf("failed to start GStreamer: %w", err)
// 	}
// 	fmt.Printf("%s GStreamer started (PID %d)\n", logPrefix, cmd.Process.Pid)

// 	// Reset counters
// 	atomic.StoreInt64(&lastFrameNum, 0)
// 	atomic.StoreInt64(&gCaptureFrames, 0)
// 	atomic.StoreInt64(&gCaptureChunkFrames, 0)
// 	atomic.StoreInt64(&gChunkFrames, 0)
// 	atomic.StoreInt32(&gActive, 1)
// 	muProgress.Lock()
// 	chunkStartTime = time.Now()
// 	gChunkStart = time.Now()
// 	progressStarted = time.Now()
// 	lastFrameTime = time.Now()
// 	lastFrameCount = 0
// 	currentFPS = 0
// 	fpsSamples = make([]float64, 0, 60)
// 	muProgress.Unlock()

// 	// Monitor output
// 	go monitorSenderOutput(stdout, cam)
// 	go monitorSenderOutput(stderr, cam)

// 	// Start progress display
// 	go runProgressDisplay(cam, cfg)

// 	err = cmd.Wait()
// 	atomic.StoreInt32(&gActive, 0)

// 	if err != nil {
// 		return fmt.Errorf("GStreamer exited: %w", err)
// 	}
// 	return nil
// }

// func monitorSenderOutput(pipe io.ReadCloser, cam config.CameraConfig) {
// 	scanner := bufio.NewScanner(pipe)
// 	scanner.Buffer(make([]byte, 256*1024), 256*1024)

// 	// Multiple patterns to catch all frame outputs
// 	patterns := []*regexp.Regexp{
// 		regexp.MustCompile(`frame[:\s]+(\d+)`),
// 		regexp.MustCompile(`encoded\s+(\d+)\s+frames`),
// 		regexp.MustCompile(`\[(\d+)\]\s+[IPB]`),
// 	}
// 	identitySizeRe := regexp.MustCompile(`frame_size.*?size[:\s]+(\d+)`)
// 	frameStatsRe := regexp.MustCompile(`frame\s+(\d+):\s+([IPB]),\s+(\d+)\s+bytes`)
// 	pipelinePlaying := false
// 	lastFrame := int64(0)

// 	for scanner.Scan() {
// 		line := scanner.Text()

// 		// Debug first frame to see what we're getting
// 		if !pipelinePlaying && (strings.Contains(line, "frame") || strings.Contains(line, "size")) {
// 			if !pipelinePlaying {
// 				fmt.Printf("[%s] Debug: %s\n", cam.ID, line)
// 			}
// 		}

// 		// Capture frame size from identity element - this is the most reliable
// 		if m := identitySizeRe.FindStringSubmatch(line); len(m) > 1 {
// 			if _, err := strconv.ParseInt(m[1], 10, 64); err == nil {
// 				// Each identity output = one encoded frame
// 				current := atomic.LoadInt64(&lastFrameNum)
// 				atomic.StoreInt64(&lastFrameNum, current+1)
// 				atomic.AddInt64(&gCaptureFrames, 1)
// 				atomic.AddInt64(&gCaptureChunkFrames, 1)
// 				atomic.AddInt64(&gChunkFrames, 1)
// 				atomic.AddInt64(&gTotalFrames, 1)

// 				// Update FPS
// 				muProgress.Lock()
// 				now := time.Now()
// 				elapsed := now.Sub(progressStarted).Seconds()
// 				if elapsed > 0 {
// 					fps := float64(atomic.LoadInt64(&gChunkFrames)) / elapsed
// 					if fps > 0 && fps < 1000 {
// 						fpsSamples = append(fpsSamples, fps)
// 						if len(fpsSamples) > 60 {
// 							fpsSamples = fpsSamples[1:]
// 						}
// 						avgFPS := 0.0
// 						for _, f := range fpsSamples {
// 							avgFPS += f
// 						}
// 						avgFPS /= float64(len(fpsSamples))
// 						currentFPS = avgFPS
// 					}
// 				}
// 				muProgress.Unlock()
// 			}
// 		}

// 		// Count frames from encoder logs
// 		for _, re := range patterns {
// 			if matches := re.FindStringSubmatch(line); len(matches) > 1 {
// 				if frameNum, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
// 					if frameNum > lastFrame {
// 						delta := frameNum - lastFrame
// 						lastFrame = frameNum
// 						atomic.AddInt64(&gCaptureFrames, delta)
// 						atomic.AddInt64(&gCaptureChunkFrames, delta)
// 						atomic.AddInt64(&gChunkFrames, delta)
// 						atomic.AddInt64(&gTotalFrames, delta)

// 						// Update FPS
// 						muProgress.Lock()
// 						now := time.Now()
// 						elapsed := now.Sub(progressStarted).Seconds()
// 						if elapsed > 0 {
// 							fps := float64(atomic.LoadInt64(&gChunkFrames)) / elapsed
// 							if fps > 0 && fps < 1000 {
// 								fpsSamples = append(fpsSamples, fps)
// 								if len(fpsSamples) > 60 {
// 									fpsSamples = fpsSamples[1:]
// 								}
// 								avgFPS := 0.0
// 								for _, f := range fpsSamples {
// 									avgFPS += f
// 								}
// 								avgFPS /= float64(len(fpsSamples))
// 								currentFPS = avgFPS
// 							}
// 						}
// 						muProgress.Unlock()
// 					}
// 					break
// 				}
// 			}
// 		}

// 		// Parse frame stats
// 		if m := frameStatsRe.FindStringSubmatch(line); len(m) > 3 {
// 			if frameNum, err := strconv.ParseInt(m[1], 10, 64); err == nil {
// 				if frameNum > lastFrame {
// 					delta := frameNum - lastFrame
// 					lastFrame = frameNum
// 					atomic.AddInt64(&gCaptureFrames, delta)
// 					atomic.AddInt64(&gCaptureChunkFrames, delta)
// 					atomic.AddInt64(&gChunkFrames, delta)
// 				}
// 			}
// 		}

// 		// Check for pipeline playing
// 		if !pipelinePlaying && strings.Contains(line, "PLAYING") {
// 			pipelinePlaying = true
// 			fmt.Printf("\n[%s] GStreamer pipeline PLAYING — sending stream\n", cam.ID)
// 		}

// 		// Print errors
// 		if strings.Contains(line, "ERROR:") {
// 			fmt.Printf("\n[%s] GStreamer ERROR: %s\n", cam.ID, line)
// 		}
// 	}
// }

// func runProgressDisplay(cam config.CameraConfig, cfg *config.Config) {
// 	ticker := time.NewTicker(500 * time.Millisecond)
// 	defer ticker.Stop()

// 	chunkTime := cfg.Server.ChunkTime
// 	targetFrames := int64(chunkTime * cam.FPS)
// 	lastProgress := time.Now()

// 	for range ticker.C {
// 		if atomic.LoadInt32(&gActive) == 0 {
// 			continue
// 		}

// 		current := atomic.LoadInt64(&gCaptureChunkFrames)
// 		if current == 0 {
// 			if time.Since(lastProgress) > 2*time.Second {
// 				fmt.Printf("\r[%s] Waiting for frames...", cam.ID)
// 				lastProgress = time.Now()
// 			}
// 			continue
// 		}

// 		pct := math.Min(100, float64(current)/float64(targetFrames)*100)

// 		muProgress.Lock()
// 		elapsed := time.Since(chunkStartTime).Seconds()
// 		fps := currentFPS
// 		muProgress.Unlock()

// 		eta := int(float64(chunkTime) - elapsed)
// 		if eta < 0 {
// 			eta = 0
// 		}

// 		barWidth := 40
// 		filled := int(float64(barWidth) * pct / 100)
// 		bar := strings.Repeat("=", filled)
// 		if filled < barWidth {
// 			bar += ">" + strings.Repeat(" ", barWidth-filled-1)
// 		} else {
// 			bar = strings.Repeat("=", barWidth)
// 		}

// 		fmt.Printf("\r[%s] CHUNK %d [%s] %5.1f%%  FPS: %.1f  ETA: %3ds  Frames: %d/%d",
// 			cam.ID, 1, bar, pct, fps, eta, current, targetFrames)
// 	}
// }

// // RunWithRestart keeps restarting the sender pipeline if it crashes.
// func RunWithRestart(cfg *config.Config, cam config.CameraConfig, backoffMax time.Duration) {
// 	logPrefix := logPfx(cam)
// 	backoff := 5 * time.Second

// 	for {
// 		fmt.Printf("%s Starting sender...\n", logPrefix)
// 		start := time.Now()

// 		err := Run(cfg, cam)
// 		elapsed := time.Since(start)

// 		if err != nil {
// 			fmt.Printf("%s Sender exited after %.1fs: %v\n", logPrefix, elapsed.Seconds(), err)
// 		} else {
// 			fmt.Printf("%s Sender exited cleanly after %.1fs\n", logPrefix, elapsed.Seconds())
// 		}

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

// func logPfx(cam config.CameraConfig) string {
// 	return fmt.Sprintf("[%s][%s][%s]", ts(), cam.ID, cam.Name)
// }

// func ts() string {
// 	return time.Now().Format("15:04:05")
// }

// // Backup
// package sender

// import (
// 	"bufio"
// 	"fmt"
// 	"io"
// 	"math"
// 	"os/exec"
// 	"regexp"
// 	"strconv"
// 	"strings"
// 	"sync"
// 	"sync/atomic"
// 	"syscall"
// 	"time"

// 	"camera-system/internal/config"
// )

// var (
// 	muProgress     sync.Mutex
// 	lastFrameNum   int64
// 	chunkStartTime time.Time
// )

// // Run builds and executes the GStreamer sender pipeline for one camera.
// func Run(cfg *config.Config, cam config.CameraConfig) error {
// 	srtURI := cfg.SRTCallerURI(cam)
// 	logPrefix := logPfx(cam)

// 	var encoderElem, parserElem string
// 	switch strings.ToLower(cfg.Server.Encoder) {
// 	case "h265", "hevc":
// 		encoderElem = fmt.Sprintf(
// 			"x265enc bitrate=%d key-int-max=%d speed-preset=%s tune=%s log-level=2",
// 			cam.Bitrate, cam.KeyInt, cam.Preset, cam.Tune,
// 		)
// 		parserElem = "h265parse"
// 	default:
// 		encoderElem = fmt.Sprintf(
// 			"x264enc bitrate=%d speed-preset=%s tune=%s key-int-max=%d bframes=%d ref=1",
// 			cam.Bitrate, cam.Preset, cam.Tune, cam.KeyInt, cam.BFrames,
// 		)
// 		parserElem = "h264parse"
// 	}

// 	pipeline := fmt.Sprintf(
// 		"gst-launch-1.0 -e -v "+
// 			"rtspsrc location=\"%s\" protocols=tcp latency=0 timeout=10000000 do-retransmission=true ! "+
// 			"rtph264depay ! queue max-size-buffers=200 leaky=downstream ! "+
// 			"h264parse ! avdec_h264 ! "+
// 			"videoconvert ! videoscale ! videorate ! "+
// 			"video/x-raw,width=%d,height=%d,framerate=%d/1 ! "+
// 			"%s ! "+
// 			"identity name=frame_size silent=false ! "+
// 			"%s ! "+
// 			"mpegtsmux ! "+
// 			"srtsink uri=\"%s\" sync=false wait-for-connection=false latency=200",
// 		cam.RTSPURL, cam.Width, cam.Height, cam.FPS,
// 		encoderElem, parserElem, srtURI,
// 	)

// 	fmt.Printf("%s Starting GStreamer sender\n", logPrefix)
// 	fmt.Printf("%s RTSP  : %s\n", logPrefix, cam.RTSPURL)
// 	fmt.Printf("%s SRT   : %s\n", logPrefix, srtURI)
// 	fmt.Printf("%s Codec : %s  %dkbps  %dfps  %dx%d\n",
// 		logPrefix, strings.ToUpper(cfg.Server.Encoder),
// 		cam.Bitrate, cam.FPS, cam.Width, cam.Height)
// 	fmt.Printf("%s Pipeline: %s\n\n", logPrefix, pipeline)

// 	cmd := exec.Command("bash", "-c", pipeline)
// 	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

// 	stdout, _ := cmd.StdoutPipe()
// 	stderr, _ := cmd.StderrPipe()

// 	if err := cmd.Start(); err != nil {
// 		return fmt.Errorf("failed to start GStreamer: %w", err)
// 	}
// 	fmt.Printf("%s GStreamer started (PID %d)\n", logPrefix, cmd.Process.Pid)

// 	atomic.StoreInt64(&lastFrameNum, 0)
// 	muProgress.Lock()
// 	chunkStartTime = time.Now()
// 	muProgress.Unlock()

// 	go monitorSenderOutput(stdout, cam, cfg)
// 	go monitorSenderOutput(stderr, cam, cfg)

// 	go runProgressDisplay(cam, cfg)

// 	err := cmd.Wait()
// 	atomic.StoreInt64(&lastFrameNum, 0)
// 	return err
// }

// func monitorSenderOutput(pipe io.ReadCloser, cam config.CameraConfig, cfg *config.Config) {
// 	scanner := bufio.NewScanner(pipe)
// 	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

// 	identitySizeRe := regexp.MustCompile(`frame_size.*?size[:\s]+(\d+)`)
// 	frameRe := regexp.MustCompile(`(?i)frame[:\s]+(\d+)`)

// 	for scanner.Scan() {
// 		line := scanner.Text()
// 		fmt.Printf("[%s][%s] GST: %s\n", ts(), cam.ID, line)

// 		// Print all GStreamer output for debugging (optional, but helpful)
// 		if strings.Contains(line, "ERROR") || strings.Contains(line, "WARNING") {
// 			fmt.Printf("[%s][%s] GST: %s\n", ts(), cam.ID, line)
// 		}

// 		if matches := identitySizeRe.FindStringSubmatch(line); len(matches) > 1 {
// 			if _, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
// 				atomic.AddInt64(&lastFrameNum, 1)
// 			}
// 		}
// 		if matches := frameRe.FindStringSubmatch(line); len(matches) > 1 {
// 			if frameNum, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
// 				atomic.StoreInt64(&lastFrameNum, frameNum)
// 			}
// 		}
// 	}
// }

// // runProgressDisplay prints a progress bar with live FPS.
// func runProgressDisplay(cam config.CameraConfig, cfg *config.Config) {
// 	ticker := time.NewTicker(500 * time.Millisecond)
// 	defer ticker.Stop()

// 	chunkTime := cfg.Server.ChunkTime
// 	targetFrames := int64(chunkTime * cam.FPS)
// 	var lastFrame int64
// 	lastTime := time.Now()

// 	for range ticker.C {
// 		current := atomic.LoadInt64(&lastFrameNum)
// 		if current == 0 {
// 			fmt.Printf("\r[%s] Waiting for frames...", cam.ID)
// 			continue
// 		}

// 		now := time.Now()
// 		elapsedSec := now.Sub(lastTime).Seconds()
// 		framesSince := current - lastFrame
// 		liveFPS := float64(framesSince) / elapsedSec
// 		if liveFPS > 1000 || liveFPS < 0 {
// 			liveFPS = 0
// 		}
// 		lastFrame = current
// 		lastTime = now

// 		pct := math.Min(100, float64(current)/float64(targetFrames)*100)

// 		muProgress.Lock()
// 		chunkElapsed := time.Since(chunkStartTime).Seconds()
// 		muProgress.Unlock()

// 		eta := int(float64(chunkTime) - chunkElapsed)
// 		if eta < 0 {
// 			eta = 0
// 		}

// 		barWidth := 40
// 		filled := int(float64(barWidth) * pct / 100)
// 		bar := strings.Repeat("=", filled)
// 		if filled < barWidth {
// 			bar += ">" + strings.Repeat(" ", barWidth-filled-1)
// 		} else {
// 			bar = strings.Repeat("=", barWidth)
// 		}

// 		fmt.Printf("\r[%s] CHUNK 1 [%s] %5.1f%%  FPS: %.1f  ETA: %3ds  Frames: %d/%d",
// 			cam.ID, bar, pct, liveFPS, eta, current, targetFrames)
// 	}
// }

// // RunWithRestart keeps restarting the sender pipeline if it crashes.
// func RunWithRestart(cfg *config.Config, cam config.CameraConfig, backoffMax time.Duration) {
// 	logPrefix := logPfx(cam)
// 	backoff := 5 * time.Second

// 	for {
// 		fmt.Printf("%s Starting sender...\n", logPrefix)
// 		start := time.Now()

// 		err := Run(cfg, cam)
// 		elapsed := time.Since(start)

// 		if err != nil {
// 			fmt.Printf("%s Sender exited after %.1fs: %v\n", logPrefix, elapsed.Seconds(), err)
// 		} else {
// 			fmt.Printf("%s Sender exited cleanly after %.1fs\n", logPrefix, elapsed.Seconds())
// 		}

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

// func logPfx(cam config.CameraConfig) string {
// 	return fmt.Sprintf("[%s][%s][%s]", ts(), cam.ID, cam.Name)
// }

// func ts() string {
// 	return time.Now().Format("15:04:05")
// }

// package sender

// import (
// 	"bufio"
// 	"fmt"
// 	"io"
// 	"math"
// 	"os/exec"
// 	"regexp"
// 	"strconv"
// 	"strings"
// 	"sync"
// 	"sync/atomic"
// 	"syscall"
// 	"time"

// 	"camera-system/internal/config"
// )

// var (
// 	muProgress     sync.Mutex
// 	lastFrameNum   int64
// 	chunkStartTime time.Time
// )

// // Run builds and executes the GStreamer sender pipeline for one camera.
// // It blocks until the pipeline exits.
// func Run(cfg *config.Config, cam config.CameraConfig) error {
// 	srtURI := cfg.SRTCallerURI(cam)
// 	logPrefix := logPfx(cam)

// 	// Select encoder element and parser based on config
// 	var encoderElem, parserElem string
// 	switch strings.ToLower(cfg.Server.Encoder) {
// 	case "h265", "hevc":
// 		encoderElem = fmt.Sprintf(
// 			"x265enc speed-preset=%s tune=%s bitrate=%d key-int-max=%d",
// 			cam.Preset, cam.Tune, cam.Bitrate, cam.KeyInt,
// 		)
// 		parserElem = "h265parse"
// 	default:
// 		encoderElem = fmt.Sprintf(
// 			"x264enc speed-preset=%s tune=%s bitrate=%d key-int-max=%d bframes=%d",
// 			cam.Preset, cam.Tune, cam.Bitrate, cam.KeyInt, cam.BFrames,
// 		)
// 		parserElem = "h264parse"
// 	}

// 	// Scale filter
// 	scaleFilter := ""
// 	if cam.Width > 0 && cam.Height > 0 {
// 		scaleFilter = fmt.Sprintf(
// 			"videoscale ! video/x-raw,width=%d,height=%d ! ",
// 			cam.Width, cam.Height,
// 		)
// 	}

// 	// Pipeline uses -v so we can parse encoder logs.
// 	pipeline := fmt.Sprintf(
// 		"gst-launch-1.0 -e -v "+
// 			"rtspsrc location=\"%s\" protocols=tcp latency=0 ! "+
// 			"queue max-size-buffers=200 leaky=downstream ! "+
// 			"rtph264depay ! h264parse ! avdec_h264 ! "+
// 			"%s"+
// 			"videoconvert ! "+
// 			"queue max-size-buffers=100 ! "+
// 			"%s ! "+
// 			"%s ! "+
// 			"queue max-size-buffers=200 ! "+
// 			"mpegtsmux ! "+
// 			"srtsink uri=\"%s\" sync=false wait-for-connection=false",
// 		cam.RTSPURL,
// 		scaleFilter,
// 		encoderElem,
// 		parserElem,
// 		srtURI,
// 	)

// 	fmt.Printf("%s Starting GStreamer sender\n", logPrefix)
// 	fmt.Printf("%s RTSP  : %s\n", logPrefix, cam.RTSPURL)
// 	fmt.Printf("%s SRT   : %s\n", logPrefix, srtURI)
// 	fmt.Printf("%s Codec : %s  %dkbps  %dfps  %dx%d\n",
// 		logPrefix, strings.ToUpper(cfg.Server.Encoder),
// 		cam.Bitrate, cam.FPS, cam.Width, cam.Height)
// 	fmt.Printf("%s Pipeline: %s\n\n", logPrefix, pipeline)

// 	cmd := exec.Command("bash", "-c", pipeline)
// 	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

// 	stdout, _ := cmd.StdoutPipe()
// 	stderr, _ := cmd.StderrPipe()

// 	if err := cmd.Start(); err != nil {
// 		return fmt.Errorf("failed to start GStreamer: %w", err)
// 	}
// 	fmt.Printf("%s GStreamer started (PID %d)\n", logPrefix, cmd.Process.Pid)

// 	// Reset counters
// 	atomic.StoreInt64(&lastFrameNum, 0)
// 	muProgress.Lock()
// 	chunkStartTime = time.Now()
// 	muProgress.Unlock()

// 	// Monitor output to count frames from encoder logs
// 	go monitorSenderOutput(stdout, cam, cfg)
// 	go monitorSenderOutput(stderr, cam, cfg)

// 	// Start progress display
// 	go runProgressDisplay(cam, cfg)

// 	err := cmd.Wait()
// 	atomic.StoreInt64(&lastFrameNum, 0)
// 	return err
// }

// // monitorSenderOutput reads GStreamer output and counts frames from x265/x264 logs.
// func monitorSenderOutput(pipe io.ReadCloser, cam config.CameraConfig, cfg *config.Config) {
// 	scanner := bufio.NewScanner(pipe)
// 	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

// 	// Regex to match "frame: 123" or "frame 123" (x265 prints "frame: <num>")
// 	frameRe := regexp.MustCompile(`(?i)frame[:\s]+(\d+)`)

// 	for scanner.Scan() {
// 		line := scanner.Text()
// 		if matches := frameRe.FindStringSubmatch(line); len(matches) > 1 {
// 			if frameNum, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
// 				// Update frame count (we only care about the latest number)
// 				atomic.StoreInt64(&lastFrameNum, frameNum)
// 			}
// 		}
// 	}
// }

// // runProgressDisplay prints a progress bar, FPS, and ETA.
// func runProgressDisplay(cam config.CameraConfig, cfg *config.Config) {
// 	ticker := time.NewTicker(500 * time.Millisecond)
// 	defer ticker.Stop()

// 	chunkTime := cfg.Server.ChunkTime
// 	targetFrames := int64(chunkTime * cam.FPS)

// 	for range ticker.C {
// 		current := atomic.LoadInt64(&lastFrameNum)
// 		if current == 0 {
// 			// Show a waiting message until frames start
// 			fmt.Printf("\r[%s] Waiting for frames...", cam.ID)
// 			continue
// 		}
// 		pct := math.Min(100, float64(current)/float64(targetFrames)*100)

// 		muProgress.Lock()
// 		elapsed := time.Since(chunkStartTime).Seconds()
// 		muProgress.Unlock()

// 		fps := float64(current) / elapsed
// 		eta := int(float64(chunkTime) - elapsed)
// 		if eta < 0 {
// 			eta = 0
// 		}

// 		barWidth := 40
// 		filled := int(float64(barWidth) * pct / 100)
// 		bar := strings.Repeat("=", filled)
// 		if filled < barWidth {
// 			bar += ">" + strings.Repeat(" ", barWidth-filled-1)
// 		} else {
// 			bar = strings.Repeat("=", barWidth)
// 		}

// 		fmt.Printf("\r[%s] CHUNK 1 [%s] %5.1f%%  FPS: %.1f  ETA: %3ds  Frames: %d/%d",
// 			cam.ID, bar, pct, fps, eta, current, targetFrames)
// 	}
// }

// // RunWithRestart keeps restarting the sender pipeline if it crashes.
// func RunWithRestart(cfg *config.Config, cam config.CameraConfig, backoffMax time.Duration) {
// 	logPrefix := logPfx(cam)
// 	backoff := 5 * time.Second

// 	for {
// 		fmt.Printf("%s Starting sender...\n", logPrefix)
// 		start := time.Now()

// 		err := Run(cfg, cam)
// 		elapsed := time.Since(start)

// 		if err != nil {
// 			fmt.Printf("%s Sender exited after %.1fs: %v\n", logPrefix, elapsed.Seconds(), err)
// 		} else {
// 			fmt.Printf("%s Sender exited cleanly after %.1fs\n", logPrefix, elapsed.Seconds())
// 		}

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

// func logPfx(cam config.CameraConfig) string {
// 	return fmt.Sprintf("[%s][%s][%s]", ts(), cam.ID, cam.Name)
// }

// func ts() string {
// 	return time.Now().Format("15:04:05")
// }

// // Package sender runs the GStreamer pipeline that reads from a camera RTSP URL,
// // compresses the video, and sends it over SRT to the recording server.
// package sender

// import (
// 	"fmt"
// 	"os"
// 	"os/exec"
// 	"strings"
// 	"syscall"
// 	"time"

// 	"camera-system/internal/config"
// )

// // Run builds and executes the GStreamer sender pipeline for one camera.
// // It blocks until the pipeline exits.
// func Run(cfg *config.Config, cam config.CameraConfig) error {
// 	srtURI := cfg.SRTCallerURI(cam)
// 	logPrefix := logPfx(cam)

// 	// Select encoder element and parser based on config
// 	var encoderElem, parserElem string
// 	switch strings.ToLower(cfg.Server.Encoder) {
// 	case "h265", "hevc":
// 		// x265enc does NOT support a "bframes" property in many builds.
// 		encoderElem = fmt.Sprintf(
// 			"x265enc speed-preset=%s tune=%s bitrate=%d key-int-max=%d",
// 			cam.Preset, cam.Tune, cam.Bitrate, cam.KeyInt,
// 		)
// 		parserElem = "h265parse"
// 	default: // h264
// 		encoderElem = fmt.Sprintf(
// 			"x264enc speed-preset=%s tune=%s bitrate=%d key-int-max=%d bframes=%d",
// 			cam.Preset, cam.Tune, cam.Bitrate, cam.KeyInt, cam.BFrames,
// 		)
// 		parserElem = "h264parse"
// 	}

// 	// Scale filter (only if width/height are set)
// 	scaleFilter := ""
// 	if cam.Width > 0 && cam.Height > 0 {
// 		scaleFilter = fmt.Sprintf(
// 			"videoscale ! video/x-raw,width=%d,height=%d ! ",
// 			cam.Width, cam.Height,
// 		)
// 	}

// 	// Full GStreamer pipeline:
// 	//   RTSP source → decode H264 → scale → convert → encode → mux → SRT sink
// 	pipeline := fmt.Sprintf(
// 		"gst-launch-1.0 -e "+
// 			"rtspsrc location=\"%s\" protocols=tcp latency=0 ! "+
// 			"rtph264depay ! h264parse ! avdec_h264 ! "+
// 			"%s"+
// 			"videoconvert ! "+
// 			"%s ! "+
// 			"%s ! "+
// 			"mpegtsmux ! "+
// 			"srtsink uri=\"%s\" sync=false wait-for-connection=false",
// 		cam.RTSPURL,
// 		scaleFilter,
// 		encoderElem,
// 		parserElem,
// 		srtURI,
// 	)

// 	fmt.Printf("%s Starting GStreamer sender\n", logPrefix)
// 	fmt.Printf("%s RTSP  : %s\n", logPrefix, cam.RTSPURL)
// 	fmt.Printf("%s SRT   : %s\n", logPrefix, srtURI)
// 	fmt.Printf("%s Codec : %s  %dkbps  %dfps  %dx%d\n",
// 		logPrefix, strings.ToUpper(cfg.Server.Encoder),
// 		cam.Bitrate, cam.FPS, cam.Width, cam.Height)
// 	fmt.Printf("%s Pipeline: %s\n\n", logPrefix, pipeline)

// 	cmd := exec.Command("bash", "-c", pipeline)
// 	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
// 	cmd.Stdout = os.Stdout
// 	cmd.Stderr = os.Stderr

// 	if err := cmd.Start(); err != nil {
// 		return fmt.Errorf("failed to start GStreamer: %w", err)
// 	}
// 	fmt.Printf("%s GStreamer started (PID %d)\n", logPrefix, cmd.Process.Pid)

// 	return cmd.Wait()
// }

// // RunWithRestart keeps restarting the sender pipeline if it crashes.
// // It uses exponential backoff up to backoffMax between restarts.
// func RunWithRestart(cfg *config.Config, cam config.CameraConfig, backoffMax time.Duration) {
// 	logPrefix := logPfx(cam)
// 	backoff := 5 * time.Second

// 	for {
// 		fmt.Printf("%s Starting sender...\n", logPrefix)
// 		start := time.Now()

// 		err := Run(cfg, cam)
// 		elapsed := time.Since(start)

// 		if err != nil {
// 			fmt.Printf("%s Sender exited after %.1fs: %v\n", logPrefix, elapsed.Seconds(), err)
// 		} else {
// 			fmt.Printf("%s Sender exited cleanly after %.1fs\n", logPrefix, elapsed.Seconds())
// 		}

// 		// If it ran > 30s treat it as a healthy run and reset the backoff
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

// func logPfx(cam config.CameraConfig) string {
// 	return fmt.Sprintf("[%s][%s][%s]", ts(), cam.ID, cam.Name)
// }

// func ts() string {
// 	return time.Now().Format("15:04:05")
// }

// // Package sender runs the GStreamer pipeline that reads from a camera RTSP URL,
// // compresses the video, and sends it over SRT to the recording server.
// package sender

// import (
// 	"fmt"
// 	"os"
// 	"os/exec"
// 	"strings"
// 	"syscall"
// 	"time"

// 	"camera-system/internal/config"
// )

// // Run builds and executes the GStreamer sender pipeline for one camera.
// // It blocks until the pipeline exits.
// func Run(cfg *config.Config, cam config.CameraConfig) error {
// 	srtURI := cfg.SRTCallerURI(cam)
// 	logPrefix := logPfx(cam)

// 	// Select encoder element and parser based on config
// 	var encoderElem, parserElem string
// 	switch strings.ToLower(cfg.Server.Encoder) {
// 	case "h265", "hevc":
// 		encoderElem = fmt.Sprintf(
// 			"x265enc bitrate=%d key-int-max=%d speed-preset=%s tune=%s log-level=2",
// 			cam.Bitrate, cam.KeyInt, cam.Preset, cam.Tune,
// 		)
// 		parserElem = "h265parse"
// 	default: // h264
// 		encoderElem = fmt.Sprintf(
// 			"x264enc bitrate=%d speed-preset=%s tune=%s key-int-max=%d bframes=%d ref=1",
// 			cam.Bitrate, cam.Preset, cam.Tune, cam.KeyInt, cam.BFrames,
// 		)
// 		parserElem = "h264parse"
// 	}

// 	// Pipeline exactly matching the standalone sender (using variables for flexibility)
// 	pipeline := fmt.Sprintf(
// 		"gst-launch-1.0 -e -v "+
// 			"rtspsrc location=\"%s\" protocols=tcp latency=0 timeout=%d do-retransmission=true ! "+
// 			"rtph264depay ! queue max-size-buffers=%d leaky=downstream ! "+
// 			"h264parse ! avdec_h264 ! "+
// 			"videoconvert ! videoscale ! videorate ! "+
// 			"video/x-raw,width=%d,height=%d,framerate=%d/1,pixel-aspect-ratio=1/1 ! "+
// 			"%s ! "+
// 			"identity name=frame_size silent=false ! "+
// 			"%s ! "+
// 			"mpegtsmux ! "+
// 			"srtsink uri=\"%s\" sync=false wait-for-connection=false latency=%d",
// 		cam.RTSPURL,
// 		10*1000000, // timeout = 10 seconds (as in standalone)
// 		5,          // queue max-size-buffers = 5
// 		cam.Width, cam.Height, cam.FPS,
// 		encoderElem,
// 		parserElem,
// 		srtURI,
// 		200, // SRT latency = 200 ms
// 	)

// 	fmt.Printf("%s Starting GStreamer sender\n", logPrefix)
// 	fmt.Printf("%s RTSP  : %s\n", logPrefix, cam.RTSPURL)
// 	fmt.Printf("%s SRT   : %s\n", logPrefix, srtURI)
// 	fmt.Printf("%s Codec : %s  %dkbps  %dfps  %dx%d\n",
// 		logPrefix, strings.ToUpper(cfg.Server.Encoder),
// 		cam.Bitrate, cam.FPS, cam.Width, cam.Height)
// 	fmt.Printf("%s Pipeline: %s\n\n", logPrefix, pipeline)

// 	cmd := exec.Command("bash", "-c", pipeline)
// 	// Critical: set process group to avoid signals affecting the child
// 	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
// 	// Ensure environment is passed (though inherited by default, be explicit)
// 	cmd.Env = os.Environ()
// 	cmd.Stdout = os.Stdout
// 	cmd.Stderr = os.Stderr

// 	if err := cmd.Start(); err != nil {
// 		return fmt.Errorf("failed to start GStreamer: %w", err)
// 	}
// 	fmt.Printf("%s GStreamer started (PID %d)\n", logPrefix, cmd.Process.Pid)

// 	return cmd.Wait()
// }

// // RunWithRestart keeps restarting the sender pipeline if it crashes.
// // It uses exponential backoff up to backoffMax between restarts.
// func RunWithRestart(cfg *config.Config, cam config.CameraConfig, backoffMax time.Duration) {
// 	logPrefix := logPfx(cam)
// 	backoff := 5 * time.Second

// 	for {
// 		fmt.Printf("%s Starting sender...\n", logPrefix)
// 		start := time.Now()

// 		err := Run(cfg, cam)
// 		elapsed := time.Since(start)

// 		if err != nil {
// 			fmt.Printf("%s Sender exited after %.1fs: %v\n", logPrefix, elapsed.Seconds(), err)
// 		} else {
// 			fmt.Printf("%s Sender exited cleanly after %.1fs\n", logPrefix, elapsed.Seconds())
// 		}

// 		// If it ran > 30s treat it as a healthy run and reset the backoff
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

// func logPfx(cam config.CameraConfig) string {
// 	return fmt.Sprintf("[%s][%s][%s]", ts(), cam.ID, cam.Name)
// }

// func ts() string {
// 	return time.Now().Format("15:04:05")
// }
