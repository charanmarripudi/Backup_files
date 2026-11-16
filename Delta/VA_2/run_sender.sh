#!/bin/bash
export GST_PLUGIN_PATH="/opt/homebrew/lib/gstreamer-1.0"
export DYLD_LIBRARY_PATH="/opt/homebrew/lib:$DYLD_LIBRARY_PATH"
./working_sender "$@"
