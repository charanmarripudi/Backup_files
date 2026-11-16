#!/bin/bash
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Starting ONVIF PTZ Camera Service"
echo "  Camera: rtsp://admin:@192.168.96.30:554/ch0_0.264"
echo "  Port  : 8085"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# Check dependencies
python3 -c "import fastapi, uvicorn" 2>/dev/null || pip install -r requirements.txt

# Run server
exec python3 server.py
