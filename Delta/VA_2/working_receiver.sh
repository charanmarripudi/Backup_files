#!/bin/bash
echo "=== Working Receiver ==="
echo "Listening: UDP port 9000"
echo "Saving to: ./received_videos/working_received.mp4"
echo ""

mkdir -p received_videos

# Receiver that matches the sender
./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 \
  udpsrc port=9000 ! \
  "application/x-rtp, media=(string)video, clock-rate=(int)90000, encoding-name=(string)H264, payload=(int)96" ! \
  rtph264depay ! \
  h264parse ! \
  mp4mux faststart=true name=mux ! \
  filesink location="./received_videos/working_received.mp4"
