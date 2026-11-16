#!/bin/bash
echo "=== Working Sender ==="
echo "Sending: mathena_edit1.mp4"
echo "Destination: UDP 127.0.0.1:9000"
echo ""

# Use the same pipeline that worked for remuxing, but send via UDP
./run_gstreamer.sh /Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0 -e \
  filesrc location="mathena_edit1.mp4" ! \
  qtdemux name=demux \
  demux.video_0 ! \
  queue ! \
  h264parse ! \
  rtph264pay config-interval=1 pt=96 ! \
  udpsink host=127.0.0.1 port=9000 sync=false
