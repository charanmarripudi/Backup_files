#!/bin/bash
SRT_URI="$1"
OUTPUT_DIR="${2:-./received_videos}"

mkdir -p "$OUTPUT_DIR"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
OUTPUT_FILE="$OUTPUT_DIR/received_${TIMESTAMP}.mp4"

echo "=== SRT Receiver (Shell) ==="
echo "Listening on: $SRT_URI"
echo "Saving to: $OUTPUT_FILE"
echo ""

# Use gst-launch-1.0 directly
gst-launch-1.0 srtsrc uri="$SRT_URI" latency=2000000 ! \
  queue ! \
  rtph264depay ! \
  h264parse ! \
  mp4mux faststart=true ! \
  filesink location="$OUTPUT_FILE"
