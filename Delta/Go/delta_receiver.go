// delta_receiver.go - Save Delta Video As-Is
//go:build receiver

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type ReceiverConfig struct {
	srtURI    string
	outputDir string
	filename  string
	buffer    int
	duration  int
	analyze   bool
	display   bool
}

var stopReceiving bool

func printReceiverUsage(progName string) {
	fmt.Printf("Usage: %s --srt <uri> [options]\n", progName)
	fmt.Println("\nSRT Receiver: Save Delta Video As-Is")
	fmt.Println("  1. Listen for SRT stream")
	fmt.Println("  2. Save IDENTICAL delta video from sender")
	fmt.Println("  3. Preserve B-frames and compression")
	fmt.Println("\nOptions:")
	fmt.Println("  --srt URI         SRT listen URI (e.g., srt://:9000)")
	fmt.Println("  --output DIR      Output directory (default: ./received)")
	fmt.Println("  --filename NAME   Custom output filename")
	fmt.Println("  --buffer N        SRT latency buffer in ms (default: 120)")
	fmt.Println("  --duration N      Stop after N seconds (0 = infinite)")
	fmt.Println("  --analyze         Run analytics after receiving")
	fmt.Println("  --display         Display while receiving")
	fmt.Println("  --help            Show this help")
}

func extractPort(srtURI string) int {
	port := 9000

	// Find :// and get port after last :
	if strings.Contains(srtURI, "://") {
		parts := strings.Split(srtURI, "://")
		if len(parts) > 1 {
			lastColon := strings.LastIndex(parts[1], ":")
			if lastColon != -1 {
				portStr := parts[1][lastColon+1:]
				if p, err := strconv.Atoi(portStr); err == nil && p > 0 && p <= 65535 {
					port = p
				}
			}
		}
	}

	return port
}

func signalHandler(sig os.Signal) {
	stopReceiving = true
	fmt.Printf("\nSignal %v received, stopping gracefully...\n", sig)
}

func getTimestampStr() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func createOutputFilename(outputDir, customName, format string, port int) string {
	timestamp := time.Now().Format("20060102_150405")

	if customName != "" {
		return filepath.Join(outputDir, fmt.Sprintf("%s_%s.%s", customName, timestamp, format))
	}

	return filepath.Join(outputDir, fmt.Sprintf("delta_received_port%d_%s.%s", port, timestamp, format))
}

func createGStreamerPipeline(port, bufferMs, duration int, outputFile string, display bool) string {
	var pipeline string

	if display {
		pipeline = fmt.Sprintf(
			"gst-launch-1.0 -q -e "+
				"srtsrc uri=\"srt://0.0.0.0:%d?mode=listener&latency=%d\" "+
				"wait-for-connection=true ! "+
				//"tsdemux name=demux "+
				"queue ! tsdemux name=demux "+
				"demux. ! queue ! h264parse ! "+
				"tee name=t ! "+
				"queue ! avdec_h264 ! videoconvert ! autovideosink "+
				"t. ! queue ! h264parse ! "+
				"mp4mux ! "+
				"filesink location=\"%s\" sync=true",
			port, bufferMs, outputFile,
		)
	} else {
		pipeline = fmt.Sprintf(
			"gst-launch-1.0 -q -e "+
				"srtsrc uri=\"srt://0.0.0.0:%d?mode=listener&latency=%d\" "+
				"wait-for-connection=true ! "+
				"queue ! tsdemux name=demux "+
				"demux. ! queue ! h264parse ! "+
				"mp4mux ! "+
				"filesink location=\"%s\" sync=true",
			port, bufferMs, outputFile,
		)
	}

	if duration > 0 {
		pipeline = fmt.Sprintf("timeout %d %s", duration, pipeline)
	}

	return pipeline
}

func runFFProbeVerify(outputFile string) {
	cmd := fmt.Sprintf(
		"ffprobe -v error -select_streams v:0 "+
			"-show_entries stream=has_b_frames "+
			"-of csv=p=0 \"%s\" 2>/dev/null",
		outputFile,
	)

	output, err := exec.Command("bash", "-c", cmd).Output()
	if err == nil {
		result := strings.TrimSpace(string(output))
		if result == "1" || result == "2" {
			fmt.Println(" B-frames preserved: Yes (good compression)")
		} else {
			fmt.Println(" Video saved successfully")
		}
	}
}

func getReceiverFileSizeMB(filePath string) (float64, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return 0, err
	}
	return float64(info.Size()) / (1024 * 1024), nil
}

func main() {
	// Disable buffering
	os.Stdout = os.NewFile(uintptr(syscall.Stdout), "/dev/stdout")
	os.Stderr = os.NewFile(uintptr(syscall.Stderr), "/dev/stderr")

	fmt.Println("=== DELTA VIDEO RECEIVER - Save Compressed Video As-Is ===")
	fmt.Println("Receives IDENTICAL delta video from sender (B-frames preserved)")

	var config ReceiverConfig

	flag.StringVar(&config.srtURI, "srt", "", "SRT listen URI (e.g., srt://:9000)")
	flag.StringVar(&config.outputDir, "output", "./received", "Output directory")
	flag.StringVar(&config.filename, "filename", "", "Custom output filename")
	flag.IntVar(&config.buffer, "buffer", 120, "SRT latency buffer in ms")
	flag.IntVar(&config.duration, "duration", 0, "Stop after N seconds (0 = infinite)")
	flag.BoolVar(&config.analyze, "analyze", false, "Run analytics after receiving")
	flag.BoolVar(&config.display, "display", false, "Display while receiving")
	help := flag.Bool("help", false, "Show help")

	flag.Parse()

	if *help {
		printReceiverUsage(os.Args[0])
		return
	}

	if config.srtURI == "" {
		fmt.Fprintf(os.Stderr, "Error: --srt is required\n")
		printReceiverUsage(os.Args[0])
		os.Exit(1)
	}

	port := extractPort(config.srtURI)

	// Create output directory
	if err := os.MkdirAll(config.outputDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Cannot create output directory '%s': %v\n", config.outputDir, err)
		os.Exit(1)
	}

	outputFile := createOutputFilename(config.outputDir, config.filename, "mp4", port)

	start := time.Now()

	fmt.Println("\n=== Configuration ===")
	fmt.Printf("SRT URI:          %s\n", config.srtURI)
	fmt.Printf("Port:             %d\n", port)
	fmt.Printf("Buffer:           %d ms\n", config.buffer)
	fmt.Printf("Output dir:       %s\n", config.outputDir)
	fmt.Printf("Output file:      %s\n", outputFile)
	fmt.Printf("Display:          %s\n", boolToYesNo(config.display))
	fmt.Printf("Analyze:          %s\n", boolToYesNo(config.analyze))
	if config.duration > 0 {
		fmt.Printf("Duration:         %d seconds\n", config.duration)
	}

	fmt.Println("\n=== Starting Receiver ===")
	fmt.Println("Will save IDENTICAL delta video from sender")
	fmt.Println("B-frames preserved for maximum compression")

	fmt.Printf("[%s] Listening on port %d...\n", getTimestampStr(), port)
	fmt.Println("Waiting for delta video stream...")

	// Create GStreamer pipeline
	gstCmd := createGStreamerPipeline(port, config.buffer, config.duration, outputFile, config.display)

	fmt.Printf("\nGStreamer pipeline:\n%s\n", gstCmd)

	fmt.Printf("\nWaiting for SRT connection on port %d...\n", port)
	fmt.Println("Press Ctrl+C to stop")

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Run the command
	execCmd := exec.Command("bash", "-c", gstCmd)

	// Start command
	if err := execCmd.Start(); err != nil {
		fmt.Printf("Failed to start GStreamer: %v\n", err)
		os.Exit(1)
	}

	// Wait for command to complete or signal
	done := make(chan error, 1)
	go func() {
		done <- execCmd.Wait()
	}()

	var ret int
	select {
	case err := <-done:
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				ret = exitErr.ExitCode()
			} else {
				ret = 1
			}
		} else {
			ret = 0
		}

	case <-sigChan:
		signalHandler(syscall.SIGINT)
		// Kill the process
		execCmd.Process.Signal(syscall.SIGTERM)
		<-done // Wait for process to exit
		ret = 130
	}

	elapsed := time.Since(start).Seconds()

	fmt.Println("\n=== Receiver Complete ===")
	fmt.Printf("Exit code: %d\n", ret)

	// Check output file
	if _, err := os.Stat(outputFile); err == nil {
		sizeMB, err := getReceiverFileSizeMB(outputFile)
		if err == nil && sizeMB > 0.01 {
			fmt.Println("\n Delta video received and saved!")
			fmt.Printf("File:      %s\n", outputFile)
			fmt.Printf("Size:      %.2f MB (compressed)\n", sizeMB)

			if elapsed > 0.1 {
				fmt.Printf("Time:      %.2f seconds\n", elapsed)
				fmt.Printf("Rate:      %.2f MB/s\n", sizeMB/elapsed)
			}

			fmt.Println("\n Verifying B-frames (compression)...")
			runFFProbeVerify(outputFile)

			fmt.Println("\n To play:")
			fmt.Printf("   ffplay %s\n", outputFile)

			if config.analyze {
				fmt.Println("\n Running analytics...")
				runVideoAnalytics(outputFile)
			}

		} else {
			fmt.Printf("\n File created but is small (%.2f MB)\n", sizeMB)
		}

	} else {
		fmt.Println("\n No file was saved")
		fmt.Println("\n Troubleshooting:")
		fmt.Println("1. Check if sender is running")
		fmt.Printf("2. Test SRT: gst-launch-1.0 srtsrc uri=\"srt://0.0.0.0:%d?mode=listener\" ! fakesink\n", port)
	}

	if ret == 0 || ret == 2 || ret == 130 || ret == 255 {
		fmt.Println("\n Receiver completed")
		os.Exit(0)
	} else {
		fmt.Printf("\n Receiver exited with code %d\n", ret)
		os.Exit(1)
	}
}

func boolToYesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

func runVideoAnalytics(outputFile string) {
	// Run basic ffprobe analysis
	cmds := []string{
		fmt.Sprintf("ffprobe -v error -show_format -show_streams \"%s\" 2>/dev/null | grep -E 'codec_name|duration|bit_rate|width|height|has_b_frames'", outputFile),
		fmt.Sprintf("ffprobe -v error -select_streams v:0 -count_packets -show_entries packet=pts_time,flags -of csv \"%s\" 2>/dev/null | head -20", outputFile),
	}

	for _, cmd := range cmds {
		output, err := exec.Command("bash", "-c", cmd).Output()
		if err == nil && len(output) > 0 {
			fmt.Println(strings.TrimSpace(string(output)))
		}
	}
}
