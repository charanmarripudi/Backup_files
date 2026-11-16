#!/bin/bash
echo "=== Auto Test ==="

# Kill any existing processes
pkill -f "gst-launch-1.0" 2>/dev/null

# Start receiver in background
./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 \
  udpsrc port=9000 ! \
  "application/x-rtp,media=video,clock-rate=90000,encoding-name=H264,payload=96" ! \
  rtph264depay ! \
  h264parse ! \
  mp4mux ! \
  filesink location=./auto_received.mp4 &
RECEIVER_PID=$!

echo "Receiver started (PID: $RECEIVER_PID)"
sleep 2

# Send test video
echo "Sending test video..."
./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 -e \
  filesrc location=test.mp4 ! \
  decodebin ! \
  videoconvert ! \
  x264enc bitrate=2000 key-int-max=60 bframes=3 ! \
  h264parse ! \
  rtph264pay pt=96 ! \
  udpsink host=127.0.0.1 port=9000

echo "Sender finished"
sleep 1

# Kill receiver
kill $RECEIVER_PID 2>/dev/null

# Check result
if [ -f "./auto_received.mp4" ]; then
    echo "SUCCESS! File received."
    ls -lh ./auto_received.mp4
else
    echo "FAILED: No file received"
fi
