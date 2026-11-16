#!/bin/bash
echo "=== Receiving video ==="
echo "Listening on UDP port 9000"
echo "Saving to: ./received_mathena.mp4"
echo ""

# Create output directory
mkdir -p received_videos

# Use a simpler receiver pipeline
./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 \
  udpsrc port=9000 caps="application/x-rtp, media=(string)video, clock-rate=(int)90000, encoding-name=(string)H264, payload=(int)96" ! \
  rtph264depay ! \
  h264parse ! \
  mp4mux faststart=true ! \
  filesink location="./received_videos/received_mathena.mp4"
