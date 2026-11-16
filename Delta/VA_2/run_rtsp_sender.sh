#!/bin/bash
# Set up GStreamer environment for Homebrew on macOS
export GST_PLUGIN_PATH="/opt/homebrew/lib/gstreamer-1.0"
export GST_PLUGIN_SYSTEM_PATH="/opt/homebrew/lib/gstreamer-1.0"
export DYLD_LIBRARY_PATH="/opt/homebrew/lib:$DYLD_LIBRARY_PATH"
export PATH="/opt/homebrew/bin:$PATH"

# Run the program with arguments
echo "Running with environment:"
echo "GST_PLUGIN_PATH=$GST_PLUGIN_PATH"
echo "DYLD_LIBRARY_PATH=$DYLD_LIBRARY_PATH"
./rtsp_sender "$@"
