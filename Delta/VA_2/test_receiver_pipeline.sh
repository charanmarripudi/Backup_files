#!/bin/bash
echo "=== Testing Receiver Pipeline ==="

# Create a test RTP file
echo "1. Creating test RTP file..."
./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 -e \
  videotestsrc pattern=smpte num-buffers=30 ! \
  videoconvert ! \
  x264enc ! \
  h264parse ! \
  rtph264pay ! \
  filesink location=test.rtp

echo "2. Testing receiver with file..."
./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 \
  filesrc location=test.rtp ! \
  "application/x-rtp,media=video,clock-rate=90000,encoding-name=H264,payload=96" ! \
  rtph264depay ! \
  h264parse ! \
  mp4mux ! \
  filesink location=test_received.mp4

echo "3. Checking file..."
ls -lh test_received.mp4
