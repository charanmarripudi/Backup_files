#!/bin/bash
# Use ONLY Framework GStreamer, exclude Homebrew
export GST_PLUGIN_SYSTEM_PATH="/Library/Frameworks/GStreamer.framework/Versions/1.0/lib/gstreamer-1.0"
export GST_PLUGIN_SCANNER="/Library/Frameworks/GStreamer.framework/Versions/1.0/libexec/gstreamer-1.0/gst-plugin-scanner"
export GST_REGISTRY="/tmp/gst_registry.bin"
export PATH="/Library/Frameworks/GStreamer.framework/Versions/Current/bin:$PATH"
export DYLD_LIBRARY_PATH="/Library/Frameworks/GStreamer.framework/Versions/Current/lib"

# Remove Homebrew from paths to avoid conflicts
export PATH=$(echo "$PATH" | sed 's|/opt/homebrew/bin:||g')
export DYLD_LIBRARY_PATH=$(echo "$DYLD_LIBRARY_PATH" | sed 's|/opt/homebrew/lib:||g')
export PKG_CONFIG_PATH=""

# Clean registry
rm -f "$GST_REGISTRY"

echo "Using Framework GStreamer only"
echo "PATH: $PATH"
echo "DYLD_LIBRARY_PATH: $DYLD_LIBRARY_PATH"
exec "$@"
