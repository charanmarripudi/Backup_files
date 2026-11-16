#!/bin/bash
echo "=== Sending mathena_edit1.mp4 ==="
echo "Using UDP to 127.0.0.1:9000"
echo ""

# Use a simpler pipeline with proper settings
./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 -e \
  filesrc location="mathena_edit1.mp4" ! \
  qtdemux ! \
  h264parse ! \
  rtph264pay config-interval=1 pt=96 ! \
  udpsink host=127.0.0.1 port=9000 sync=false
