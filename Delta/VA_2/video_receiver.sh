#!/bin/bash
SOURCE="${1:-srt://:9000}"
OUTPUT_DIR="${2:-./received_videos}"

if [ -z "$SOURCE" ]; then
    echo "Usage: $0 [source] [output_dir]"
    echo "Example: $0 srt://:9000"
    echo "Example: $0 'tcpclientsrc host=127.0.0.1 port=9000'"
    exit 1
fi

mkdir -p "$OUTPUT_DIR"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
OUTPUT_FILE="$OUTPUT_DIR/received_${TIMESTAMP}.mp4"

echo "=== Video Receiver ==="
echo "Source: $SOURCE"
echo "Saving to: $OUTPUT_FILE"
echo ""

# Check if source is SRT or TCP
if [[ "$SOURCE" == srt://* ]]; then
    echo "Using SRT protocol..."
    ./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 \
      srtsrc uri="$SOURCE" latency=2000000 ! \
      queue ! \
      rtph264depay ! \
      h264parse ! \
      mp4mux faststart=true ! \
      filesink location="$OUTPUT_FILE"
else
    echo "Using TCP protocol..."
    ./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 \
      "$SOURCE" ! \
      rtph264depay ! \
      h264parse ! \
      mp4mux faststart=true ! \
      filesink location="$OUTPUT_FILE"
fi
