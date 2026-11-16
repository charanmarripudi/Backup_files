#!/bin/bash
echo "=== Testing GStreamer Pipeline ==="
echo ""

# Test 1: Encode test video to file
echo "1. Encoding test video..."
./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 -e \
  videotestsrc pattern=smpte num-buffers=30 ! \
  videoconvert ! \
  x264enc bitrate=2000 key-int-max=30 ! \
  h264parse ! \
  mp4mux ! \
  filesink location=test_encoded.mp4

echo "Created: test_encoded.mp4"
ls -lh test_encoded.mp4
echo ""

# Test 2: Play it back
echo "2. Playing back encoded video..."
./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 \
  filesrc location=test_encoded.mp4 ! \
  qtdemux ! h264parse ! avdec_h264 ! \
  videoconvert ! \
  autovideosink
