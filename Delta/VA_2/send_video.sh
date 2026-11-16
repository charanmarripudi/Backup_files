#!/bin/bash
INPUT="$1"
SRT_URI="$2"
BITRATE="${3:-2000}"

echo "=== Video to SRT Sender ==="
echo "Input file: $INPUT"
echo "SRT destination: $SRT_URI"
echo "Bitrate: ${BITRATE}k"

# Check if input file exists
if [ ! -f "$INPUT" ]; then
    echo "ERROR: Input file '$INPUT' not found!"
    exit 1
fi

# Use FFmpeg (most reliable)
echo "Starting stream with FFmpeg..."
ffmpeg -re -i "$INPUT" \
  -c:v libx264 \
  -preset medium \
  -tune zerolatency \
  -b:v ${BITRATE}k \
  -g 60 \
  -keyint_min 60 \
  -sc_threshold 0 \
  -f mpegts \
  "$SRT_URI"
