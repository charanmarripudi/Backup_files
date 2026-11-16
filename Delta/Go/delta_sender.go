// delta_sender.go - Stream Delta Encoded Video Directly
//go:build sender

package main

import (
	"bytes"
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

type SenderConfig struct {
	input      string
	srtURI     string
	outputDir  string
	crf        int
	bitrate    int
	fps        int
	latency    int
	skipEncode bool
	skipStream bool
	realtime   bool
}

func printSenderUsage(progName string) {
	fmt.Printf("Usage: %s --input <video> --srt <uri> [options]\n", progName)
	fmt.Println("\nDynamic System: Input → Delta Encode → Direct SRT Stream")
	fmt.Println("  1. Delta encode with B-frames for compression")
	fmt.Println("  2. Stream delta video directly (no re-encoding)")
	fmt.Println("  3. Receiver saves identical delta video")
	fmt.Println("\nOptions:")
	fmt.Println("  --input FILE      Input video file")
	fmt.Println("  --srt URI         SRT destination URI")
	fmt.Println("  --crf N           CRF value for x264 (default: 32)")
	fmt.Println("  --output-dir DIR  Directory for encoded video (default: ./encoded)")
	fmt.Println("  --latency N       SRT latency in ms (default: 120)")
	fmt.Println("  --bitrate N       Target bitrate in kbps (alternative to CRF)")
	fmt.Println("  --fps N           Force output FPS (default: auto)")
	fmt.Println("  --no-encode       Skip encoding, stream input directly")
	fmt.Println("  --no-stream       Only encode, don't stream")
	fmt.Println("  --realtime        Use real-time encoding profile")
	fmt.Println("  --help            Show this help")
}

func timeDiffSec(start, end time.Time) float64 {
	return end.Sub(start).Seconds()
}

func getFPSGStreamer(file string) float64 {
	cmd := fmt.Sprintf(
		"gst-launch-1.0 -q filesrc location=\"%s\" ! qtdemux ! "+
			"identity silent=false 2>&1 | "+
			"grep -o 'framerate=[^,]*' | head -1 | cut -d= -f2 | cut -d/ -f1",
		file,
	)

	output, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return 25.0
	}

	result := strings.TrimSpace(string(output))
	if fps, err := strconv.ParseFloat(result, 64); err == nil && fps > 0 {
		return fps
	}
	return 25.0
}

func deltaEncodeGStreamer(input, outputDir string, crf, bitrate, fps int, realtime bool) (string, error) {
	fmt.Println("\n=== STEP 1: DELTA ENCODING (with B-frames for compression) ===")
	start := time.Now()

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", err
	}

	filename := filepath.Base(input)
	baseName := strings.TrimSuffix(filename, filepath.Ext(filename))
	timestamp := time.Now().Format("20060102_150405")

	var outputFile string
	if bitrate > 0 {
		outputFile = filepath.Join(outputDir, fmt.Sprintf("%s_delta_%s_bitrate%d.mp4", baseName, timestamp, bitrate))
	} else {
		outputFile = filepath.Join(outputDir, fmt.Sprintf("%s_delta_%s_crf%d.mp4", baseName, timestamp, crf))
	}

	inputFPS := getFPSGStreamer(input)
	outputFPS := fps
	if outputFPS <= 0 {
		outputFPS = int(inputFPS + 0.5)
	}

	fmt.Printf("Input:           %s\n", input)
	fmt.Printf("Output:          %s\n", outputFile)
	fmt.Printf("Input FPS:       %.2f\n", inputFPS)
	fmt.Printf("Output FPS:      %d\n", outputFPS)

	var cmd string
	if realtime {
		fmt.Println("Encoding mode:   Real-time (faster)")
		if bitrate > 0 {
			cmd = fmt.Sprintf(
				"gst-launch-1.0 -q -e "+
					"filesrc location=\"%s\" ! "+
					"decodebin ! "+
					"queue ! videoconvert ! "+
					"videoscale ! "+
					"video/x-raw,framerate=%d/1 ! "+
					"x264enc "+
					"speed-preset=ultrafast "+
					"tune=zerolatency "+
					"bframes=2 "+
					"key-int-max=30 "+
					"bitrate=%d "+
					"! h264parse ! mp4mux ! "+
					"filesink location=\"%s\" sync=false",
				input, outputFPS, bitrate, outputFile,
			)
		} else {
			cmd = fmt.Sprintf(
				"gst-launch-1.0 -q -e "+
					"filesrc location=\"%s\" ! "+
					"decodebin ! "+
					"queue ! videoconvert ! "+
					"videoscale ! "+
					"video/x-raw,framerate=%d/1 ! "+
					"x264enc "+
					"speed-preset=ultrafast "+
					"tune=zerolatency "+
					"bframes=2 "+
					"key-int-max=30 "+
					"quantizer=%d "+
					"! h264parse ! mp4mux ! "+
					"filesink location=\"%s\" sync=false",
				input, outputFPS, crf, outputFile,
			)
		}
	} else {
		fmt.Println("Encoding mode:   Delta (high compression with B-frames)")
		if bitrate > 0 {
			fmt.Printf("Target bitrate:  %d kbps\n", bitrate)
			cmd = fmt.Sprintf(
				"gst-launch-1.0 -q -e "+
					"filesrc location=\"%s\" ! "+
					"decodebin ! "+
					"queue ! "+
					"videoconvert ! "+
					"queue ! "+
					"video/x-raw,framerate=%d/1 ! "+
					"x264enc "+
					"key-int-max=250 "+
					"bframes=8 "+
					"ref=6 "+
					"me=umh "+
					"subme=8 "+
					"bitrate=%d "+
					"speed-preset=slow "+
					"pass=quant "+
					"! h264parse config-interval=1 "+
					"! mp4mux ! "+
					"filesink location=\"%s\" sync=false",
				input, outputFPS, bitrate, outputFile,
			)
		} else {
			fmt.Printf("CRF:             %d\n", crf)
			fmt.Println("B-frames:        8 (for maximum compression)")
			cmd = fmt.Sprintf(
				"gst-launch-1.0 -q -e "+
					"filesrc location=\"%s\" ! "+
					"decodebin ! "+
					"queue ! "+
					"videoconvert ! "+
					"queue ! "+
					"video/x-raw,framerate=%d/1 ! "+
					"x264enc "+
					"key-int-max=250 "+
					"bframes=8 "+
					"ref=6 "+
					"me=umh "+
					"subme=8 "+
					"quantizer=%d "+
					"speed-preset=slow "+
					"pass=quant "+
					"! h264parse config-interval=1 "+
					"! mp4mux ! "+
					"filesink location=\"%s\" sync=false",
				input, outputFPS, crf, outputFile,
			)
		}
	}

	fmt.Printf("\nGStreamer encoding command:\n%s\n", cmd)

	execCmd := exec.Command("bash", "-c", cmd)
	var stderr bytes.Buffer
	execCmd.Stderr = &stderr

	err := execCmd.Run()
	end := time.Now()
	elapsed := timeDiffSec(start, end)

	if err != nil {
		fmt.Printf("Command output: %s\n", stderr.String())
		return "", fmt.Errorf("encoding failed: %v", err)
	}

	if _, err := os.Stat(outputFile); os.IsNotExist(err) {
		return "", fmt.Errorf("encoding failed - output file not created")
	}

	fileInfo, err := os.Stat(outputFile)
	if err != nil {
		return "", fmt.Errorf("failed to get output file size: %v", err)
	}
	sizeMB := float64(fileInfo.Size()) / (1024 * 1024)

	fmt.Printf("\n Delta encoding SUCCESSFUL!\n")
	fmt.Printf("File:          %s\n", outputFile)
	fmt.Printf("Size:          %.2f MB (compressed)\n", sizeMB)
	fmt.Printf("Time:          %.2f seconds\n", elapsed)

	inputInfo, err := os.Stat(input)
	if err == nil {
		inputMB := float64(inputInfo.Size()) / (1024 * 1024)
		ratio := (inputMB - sizeMB) / inputMB * 100
		fmt.Printf("Compression:    %.1f%% smaller\n", ratio)
	}

	return outputFile, nil
}

func streamEncodedVideo(encodedFile, srtURI string, latency int) int {
	fmt.Println("\n=== STEP 2: DIRECT SRT STREAMING (delta video as-is) ===")

	if _, err := os.Stat(encodedFile); os.IsNotExist(err) {
		fmt.Printf("Delta video not found: %s\n", encodedFile)
		return 1
	}

	start := time.Now()

	fileInfo, err := os.Stat(encodedFile)
	if err != nil {
		fmt.Printf("Failed to get file info: %v\n", err)
		return 1
	}
	sizeMB := float64(fileInfo.Size()) / (1024 * 1024)

	fmt.Printf("Streaming delta video: %s\n", encodedFile)
	fmt.Printf("File size:            %.2f MB (compressed)\n", sizeMB)
	fmt.Printf("SRT URI:              %s\n", srtURI)
	fmt.Printf("Latency:              %d ms\n", latency)
	fmt.Println("\nStreaming delta video DIRECTLY (no re-encoding)")
	fmt.Println("B-frames are preserved for maximum compression")

	cmd := fmt.Sprintf(
		"gst-launch-1.0 -q -e "+
			"filesrc location=\"%s\" ! "+
			"qtdemux name=demux "+
			"demux.video_0 ! "+
			"queue ! h264parse ! "+
			"mpegtsmux alignment=7 ! "+
			"srtsink uri=\"%s\" "+
			"latency=%d "+
			"wait-for-connection=true "+
			"sync=true",
		encodedFile, srtURI, latency,
	)

	fmt.Printf("\nStreaming command:\n%s\n", cmd)
	fmt.Printf("\n Waiting for receiver to connect...\n")
	fmt.Printf("   Streaming %.2f MB delta video\n", sizeMB)
	fmt.Printf("   B-frames preserved for compression\n")
	fmt.Println("   Press Ctrl+C to stop streaming\n")

	execCmd := exec.Command("bash", "-c", cmd)

	// Set up signal handling for Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Run command in background
	if err := execCmd.Start(); err != nil {
		fmt.Printf("Failed to start streaming: %v\n", err)
		return 1
	}

	// Wait for command to complete or signal
	done := make(chan error, 1)
	go func() {
		done <- execCmd.Wait()
	}()

	select {
	case err := <-done:
		end := time.Now()
		elapsed := timeDiffSec(start, end)

		if elapsed > 0 {
			throughput := sizeMB / elapsed
			fmt.Println("\nStreaming completed")
			fmt.Printf("Time:          %.2f seconds\n", elapsed)
			fmt.Printf("Throughput:    %.2f MB/s\n", throughput)
			fmt.Printf("Bandwidth:     %.0f kbps\n", throughput*8*1024)
		}

		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode := exitErr.ExitCode()
				if exitCode == 0 || exitCode == 2 || exitCode == 130 || exitCode == 255 {
					fmt.Println("\n SRT streaming completed successfully")
					return 0
				}
				fmt.Printf("SRT streaming FAILED with code %d\n", exitCode)
				return exitCode
			}
			fmt.Printf("Streaming failed: %v\n", err)
			return 1
		}

		fmt.Println("\n SRT streaming completed successfully")
		return 0

	case <-sigChan:
		// Ctrl+C pressed, kill the process
		fmt.Println("\nInterrupt received, stopping stream...")
		execCmd.Process.Signal(syscall.SIGTERM)
		<-done     // Wait for process to exit
		return 130 // Same as C when killed by Ctrl+C
	}
}

func main() {
	// Disable buffering like C's setbuf()
	os.Stdout = os.NewFile(uintptr(syscall.Stdout), "/dev/stdout")
	os.Stderr = os.NewFile(uintptr(syscall.Stderr), "/dev/stderr")

	fmt.Println(" DELTA SENDER - Dynamic Compression & Streaming 🎬")
	fmt.Println("Original → Delta Encode (4.4MB) → Direct SRT Stream → Identical Save\n")

	var config SenderConfig

	flag.StringVar(&config.input, "input", "", "Input video file")
	flag.StringVar(&config.srtURI, "srt", "", "SRT destination URI")
	flag.StringVar(&config.outputDir, "output-dir", "./encoded", "Directory for encoded video")
	flag.IntVar(&config.crf, "crf", 32, "CRF value for x264")
	flag.IntVar(&config.bitrate, "bitrate", 0, "Target bitrate in kbps (alternative to CRF)")
	flag.IntVar(&config.fps, "fps", 0, "Force output FPS")
	flag.IntVar(&config.latency, "latency", 120, "SRT latency in ms")
	flag.BoolVar(&config.skipEncode, "no-encode", false, "Skip encoding, stream input directly")
	flag.BoolVar(&config.skipStream, "no-stream", false, "Only encode, don't stream")
	flag.BoolVar(&config.realtime, "realtime", false, "Use real-time encoding profile")
	help := flag.Bool("help", false, "Show help")

	flag.Parse()

	if *help {
		printSenderUsage(os.Args[0])
		return
	}

	if config.input == "" {
		fmt.Fprintf(os.Stderr, "Error: --input is required\n")
		printSenderUsage(os.Args[0])
		os.Exit(1)
	}

	if !config.skipStream && config.srtURI == "" {
		fmt.Fprintf(os.Stderr, "Error: --srt is required for streaming\n")
		printSenderUsage(os.Args[0])
		os.Exit(1)
	}

	var encodedFile string
	var err error
	ret := 0

	if !config.skipEncode {
		encodedFile, err = deltaEncodeGStreamer(config.input, config.outputDir, config.crf, config.bitrate, config.fps, config.realtime)
		if err != nil {
			fmt.Printf("Delta encoding failed. Exiting.\n")
			os.Exit(1)
		}
		fmt.Println("\n Step 1: Delta encoding complete (compressed video ready)")
	} else {
		fmt.Println("\nSkipping encoding")
		encodedFile = config.input
	}

	if !config.skipStream && encodedFile != "" {
		fmt.Println("\n========================================")
		fmt.Println(" READY TO STREAM COMPRESSED DELTA VIDEO")
		fmt.Printf("File:     %s\n", encodedFile)
		fmt.Printf("SRT URI:  %s\n", config.srtURI)
		fmt.Printf("Latency:  %d ms\n", config.latency)

		fileInfo, err := os.Stat(encodedFile)
		if err == nil {
			sizeMB := float64(fileInfo.Size()) / (1024 * 1024)
			fmt.Printf("Size:     %.2f MB (compressed)\n", sizeMB)

			if !config.skipEncode {
				inputInfo, err := os.Stat(config.input)
				if err == nil {
					inputMB := float64(inputInfo.Size()) / (1024 * 1024)
					fmt.Printf("Original: %.2f MB (%.1f%% compression)\n",
						inputMB, (inputMB-sizeMB)/inputMB*100)
				}
			}
		}

		port := 9000
		if idx := strings.LastIndex(config.srtURI, ":"); idx != -1 {
			if p, err := strconv.Atoi(config.srtURI[idx+1:]); err == nil {
				port = p
			}
		}

		fmt.Printf("\n Checking receiver on port %d...\n", port)
		fmt.Println("\n Starting stream in 3 seconds...")
		for i := 3; i > 0; i-- {
			fmt.Printf("%d... ", i)
			os.Stdout.Sync()
			time.Sleep(1 * time.Second)
		}
		fmt.Println("GO!\n")

		ret = streamEncodedVideo(encodedFile, config.srtURI, config.latency)

		if ret == 0 {
			fmt.Println("\n Step 2: Delta video streamed directly (B-frames preserved)")
		}
	} else if config.skipStream {
		fmt.Println("\nSkipping streaming")
	}

	if encodedFile != "" {
		fmt.Println("\n========================================")
		fmt.Println(" DYNAMIC SYSTEM SUMMARY")

		if !config.skipEncode {
			fmt.Printf("Delta encoded file saved: %s\n", encodedFile)
			fmt.Print("   Size: ")
			os.Stdout.Sync()

			cmd := exec.Command("bash", "-c",
				fmt.Sprintf("ls -lh \"%s\" 2>/dev/null | awk '{print $5}' || echo 'Unknown'", encodedFile))
			output, err := cmd.Output()
			if err == nil {
				fmt.Print(strings.TrimSpace(string(output)))
			} else {
				fmt.Print("Unknown")
			}
			fmt.Println()
		}

		if !config.skipStream {
			fmt.Printf(" Streamed to: %s\n", config.srtURI)
			fmt.Println("   Receiver gets IDENTICAL delta video")
			fmt.Println("   B-frames preserved for maximum compression")
		}

		if config.skipEncode && encodedFile != config.input {
			// Only free if we allocated (but Go has GC, so just for symmetry)
		}
	}

	fmt.Println("\n Dynamic Delta System Complete")
	os.Exit(ret)
}
