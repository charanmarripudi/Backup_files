#!/bin/bash
INPUT="$1"
DEST="${2:-srt://127.0.0.1:9000}"

if [ -z "$INPUT" ]; then
    echo "Usage: $0 <input_video> [destination]"
    echo "Example: $0 test.mp4 srt://127.0.0.1:9000"
    echo "Example: $0 test.mp4 tcpserversink host=0.0.0.0 port=9000"
    exit 1
fi

if [ ! -f "$INPUT" ]; then
    echo "Error: File '$INPUT' not found"
    exit 1
fi

echo "=== Video Sender ==="
echo "Input: $INPUT"
echo "Destination: $DEST"
echo ""

# Check if destination is SRT or TCP
if [[ "$DEST" == srt://* ]]; then
    echo "Using SRT protocol..."
    ./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 -e \
      filesrc location="$INPUT" ! \
      decodebin ! \
      videoconvert ! \
      x264enc bitrate=2000 key-int-max=60 bframes=3 ! \
      h264parse ! \
      rtph264pay ! \
      srtsink uri="$DEST" latency=2000000
else
    echo "Using TCP protocol..."
    ./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 -e \
      filesrc location="$INPUT" ! \
      decodebin ! \
      videoconvert ! \
      x264enc bitrate=2000 key-int-max=60 bframes=3 ! \
      h264parse ! \
      rtph264pay ! \
      "$DEST"
fi
