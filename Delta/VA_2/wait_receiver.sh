#!/bin/bash
PORT="${1:-9000}"
OUTPUT_DIR="${2:-./received_videos}"

mkdir -p "$OUTPUT_DIR"
OUTPUT_FILE="$OUTPUT_DIR/received_$(date +%s).mp4"

echo "=== Waiting Receiver ==="
echo "Port: $PORT"
echo "Will save to: $OUTPUT_FILE"
echo "Waiting for sender to connect..."

./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 \
  multisocketsrc tcpport=$PORT ! \
  "application/x-rtp,media=video,clock-rate=90000,encoding-name=H264,payload=96" ! \
  rtph264depay ! \
  h264parse ! \
  mp4mux faststart=true ! \
  filesink location="$OUTPUT_FILE"
