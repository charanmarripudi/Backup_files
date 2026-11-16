#!/bin/bash
INPUT="$1"
PORT="${2:-9000}"

if [ -z "$INPUT" ]; then
    echo "Usage: $0 <input_video> [port]"
    exit 1
fi

echo "=== Connecting Sender ==="
echo "Input: $INPUT"
echo "Port: $PORT"

./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 -e \
  filesrc location="$INPUT" ! \
  decodebin ! \
  videoconvert ! \
  x264enc bitrate=2000 key-int-max=60 bframes=3 ! \
  h264parse ! \
  rtph264pay pt=96 ! \
  multisocketsink tcpport=$PORT
