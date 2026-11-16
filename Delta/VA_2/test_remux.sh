#!/bin/bash
echo "=== Testing video re-muxing ==="

# Copy video stream to new file
./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 -e \
  filesrc location="mathena_edit1.mp4" ! \
  qtdemux ! \
  h264parse ! \
  mp4mux faststart=true ! \
  filesink location="./test_remuxed.mp4"

echo "Created: test_remuxed.mp4"
ls -lh test_remuxed.mp4

# Try to play it
echo "Trying to play..."
./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 \
  filesrc location="./test_remuxed.mp4" ! \
  qtdemux ! \
  h264parse ! \
  avdec_h264 ! \
  videoconvert ! \
  autovideosink
