#!/bin/bash
HOST="${1:-127.0.0.1}"
PORT="${2:-9000}"
OUTPUT_DIR="${3:-./received_videos}"

mkdir -p "$OUTPUT_DIR"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
OUTPUT_FILE="$OUTPUT_DIR/received_${TIMESTAMP}.mp4"

echo "=== TCP Video Receiver ==="
echo "Host: $HOST"
echo "Port: $PORT"
echo "Saving to: $OUTPUT_FILE"
echo ""

./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 \
  tcpclientsrc host="$HOST" port="$PORT" ! \
  "application/x-rtp, media=(string)video, clock-rate=(int)90000, encoding-name=(string)H264, payload=(int)96" ! \
  rtph264depay ! \
  h264parse ! \
  mp4mux faststart=true ! \
  filesink location="$OUTPUT_FILE"
