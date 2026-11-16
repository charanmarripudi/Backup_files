#!/bin/bash
export GST_PLUGIN_PATH="/opt/homebrew/lib/gstreamer-1.0"
export DYLD_LIBRARY_PATH="/opt/homebrew/lib:$DYLD_LIBRARY_PATH"
export GST_DEBUG=2
echo "=== Running rtsp_sender ==="
./rtsp_sender --help 2>&1 | head -20
echo "=== Exit code: $? ==="
