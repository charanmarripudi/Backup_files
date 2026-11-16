#!/bin/bash
INPUT="$1"
SRT_URI="$2"
BITRATE="${3:-2000}"

echo "=== SRT Sender (Shell) ==="
echo "Input: $INPUT"
echo "Destination: $SRT_URI"
echo "Bitrate: ${BITRATE}k"
echo ""

# Use gst-launch-1.0 directly
gst-launch-1.0 -e \
  filesrc location="$INPUT" ! \
  decodebin ! \
  videoconvert ! \
  x264enc bitrate=$BITRATE key-int-max=60 bframes=3 ! \
  h264parse ! \
  rtph264pay ! \
  srtsink uri="$SRT_URI" latency=2000000
