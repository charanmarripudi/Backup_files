//go:build encoder

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func getEncoderFileSizeMB(path string) float64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return float64(info.Size()) / (1024.0 * 1024.0)
}

func runCommand(cmd string) {
	c := exec.Command("bash", "-c", cmd)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	_ = c.Run()
}

func getBitrateKbps(file string) int {
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=bit_rate",
		"-of", "default=noprint_wrappers=1:nokey=1",
		file)

	out, err := cmd.Output()
	if err != nil {
		return 0
	}

	br, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return br / 1000
}

func getFPS(file string) float64 {
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=r_frame_rate",
		"-of", "default=noprint_wrappers=1:nokey=1",
		file)

	out, err := cmd.Output()
	if err != nil {
		return 25.0
	}

	parts := strings.Split(strings.TrimSpace(string(out)), "/")
	if len(parts) != 2 {
		return 25.0
	}

	num, _ := strconv.Atoi(parts[0])
	den, _ := strconv.Atoi(parts[1])
	if den == 0 {
		return 25.0
	}

	return float64(num) / float64(den)
}

func encodeDelta(in, out string, fps, crf int) {
	start := time.Now()

	cmd := fmt.Sprintf(`
gst-launch-1.0 -q -e \
filesrc location="%s" ! decodebin ! queue ! \
videoconvert ! queue ! \
video/x-raw,framerate=%d/1 ! \
x264enc key-int-max=250 bframes=8 ref=6 \
me=umh subme=8 quantizer=%d \
speed-preset=slow pass=quant \
! h264parse ! mp4mux ! \
filesink location="%s" sync=false`,
		in, fps, crf, out)

	runCommand(cmd)

	elapsed := time.Since(start).Seconds()
	fmt.Printf(">>> Delta encode wall-time: %.2f seconds\n", elapsed)
}

func encodeFullFrame(in, out string, fps, bitrate int) {
	start := time.Now()

	cmd := fmt.Sprintf(`
gst-launch-1.0 -q -e \
filesrc location="%s" ! decodebin ! queue ! \
videoconvert ! queue ! \
video/x-raw,framerate=%d/1 ! \
x264enc intra-refresh=true key-int-max=1 bframes=0 \
bitrate=%d speed-preset=slow tune=zerolatency \
! h264parse ! mp4mux ! \
filesink location="%s" sync=false`,
		in, fps, bitrate, out)

	runCommand(cmd)

	elapsed := time.Since(start).Seconds()
	fmt.Printf(">>> Full-frame encode wall-time: %.2f seconds\n", elapsed)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("Usage: %s <video1> <video2> ...\n", os.Args[0])
		return
	}

	_ = os.MkdirAll("output_videos", 0755)

	for _, input := range os.Args[1:] {
		base := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
		delta := fmt.Sprintf("output_videos/%s_G_delta.mp4", base)
		full := fmt.Sprintf("output_videos/%s_G_full.mp4", base)

		bitrate := getBitrateKbps(input)
		fps := getFPS(input)

		fmt.Printf("\nProcessing: %s\n", input)
		fmt.Printf("Original: Bitrate=%d kb/s, FPS=%.2f\n", bitrate, fps)

		encodeDelta(input, delta, int(fps+0.5), 32)
		encodeFullFrame(input, full, int(fps+0.5), bitrate)

		sizeOrig := getEncoderFileSizeMB(input)
		sizeDelta := getEncoderFileSizeMB(delta)
		sizeFull := getEncoderFileSizeMB(full)

		fmt.Println("\nPreset          Size      Compression")
		fmt.Printf("Delta           %.2f MB   %.0f%%\n",
			sizeDelta,
			(1-sizeDelta/sizeOrig)*100)

		fmt.Printf("Full-frame      %.2f MB   %.0f%%\n",
			sizeFull,
			(1-sizeFull/sizeOrig)*100)
	}
}
