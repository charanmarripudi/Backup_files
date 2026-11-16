#!/bin/bash

echo "=== COMPLETE GStreamer Video Pipeline ==="
echo "1. Cleaning previous runs..."
rm -rf ./encoded/*.mp4 ./received/*.mp4 2>/dev/null
mkdir -p encoded received

echo "2. Starting receiver in background..."
echo "   (This will wait for sender to connect)"
./delta_receiver --srt "srt://:9000" --output "./received" --stats &
RECEIVER_PID=$!
echo "Receiver PID: $RECEIVER_PID"

# Wait for receiver to fully initialize
echo "3. Waiting 5 seconds for receiver to start..."
for i in {5..1}; do
    echo "   Starting in $i seconds..."
    sleep 1
done

echo "4. Checking if receiver is listening..."
if netstat -an 2>/dev/null | grep -q ":9000.*LISTEN"; then
    echo "    Receiver is listening on port 9000"
else
    echo "    Receiver not listening. Restarting..."
    kill $RECEIVER_PID 2>/dev/null
    ./delta_receiver --srt "srt://:9000" --output "./received" &
    RECEIVER_PID=$!
    sleep 3
fi

echo "5. Starting sender with video: $1"
if [ -f "$1" ]; then
    VIDEO_FILE="$1"
else
    echo "   Using default video: /Users/algofusion/Downloads/mathena_edit1.mp4"
    VIDEO_FILE="/Users/algofusion/Downloads/mathena_edit1.mp4"
fi

# Run sender
./delta_sender --input "$VIDEO_FILE" --srt "srt://127.0.0.1:9000" --crf 32

echo "6. Waiting for completion..."
sleep 3

# Check if receiver is still running
if kill -0 $RECEIVER_PID 2>/dev/null; then
    echo "   Stopping receiver..."
    kill $RECEIVER_PID 2>/dev/null
    sleep 2
fi

echo ""
echo "=== RESULTS ==="
LATEST_VIDEO=$(ls -t ./received/*.mp4 2>/dev/null | head -1)
if [ -f "$LATEST_VIDEO" ]; then
    FILE_SIZE=$(du -h "$LATEST_VIDEO" | cut -f1)
    echo " Video saved: $LATEST_VIDEO"
    echo "   Size: $FILE_SIZE"
    echo ""
    echo "To play: ffplay \"$LATEST_VIDEO\""
    echo "Or: open \"$LATEST_VIDEO\""
    
    # Quick check if file is valid
    echo ""
    echo "Checking video file..."
    ffprobe -v error -show_format "$LATEST_VIDEO" 2>&1 | grep -E "duration|bit_rate|format_name"
else
    echo " No video was saved"
    echo "Check if:"
    echo "1. Receiver started properly"
    echo "2. Sender connected successfully"
    echo "3. Port 9000 is available"
fi