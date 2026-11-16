#!/bin/bash
INPUT="$1"
HOST="${2:-0.0.0.0}"
PORT="${3:-9000}"

if [ -z "$INPUT" ]; then
    echo "Usage: $0 <input_video> [host] [port]"
    echo "Example: $0 test.mp4 0.0.0.0 9000"
    exit 1
fi

if [ ! -f "$INPUT" ]; then
    echo "Error: File '$INPUT' not found"
    exit 1
fi

echo "=== TCP Video Sender ==="
echo "Input: $INPUT"
echo "Host: $HOST"
echo "Port: $PORT"
echo ""

./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 -e \
  filesrc location="$INPUT" ! \
  decodebin ! \
  videoconvert ! \
  x264enc bitrate=2000 key-int-max=60 bframes=3 ! \
  h264parse ! \
  rtph264pay pt=96 ! \
  tcpserversink host="$HOST" port="$PORT"
