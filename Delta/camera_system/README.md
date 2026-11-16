# Camera Streaming System

Multi-camera RTSP → SRT → MP4 recording system with per-chunk analysis.

## Architecture

```
[Camera Site — LOCAL]                     [Recording Server — SERVER]
  delta_local binary                        delta_server binary
  ─────────────────                         ───────────────────────────
  camera1 RTSP ──→ GStreamer encode ──SRT→  GStreamer receive → chunk_00001.mp4
  camera2 RTSP ──→ GStreamer encode ──SRT→  GStreamer receive → chunk_00001.mp4
  camera3 RTSP ──→ GStreamer encode ──SRT→  GStreamer receive → chunk_00001.mp4
                                            ffprobe analysis per chunk
                                            HTTP API :8080
```

## File Structure

```
camera-system/
├── config.yaml                   ← configure cameras, server, encoding
├── go.mod
├── cmd/
│   ├── local/main.go             ← local sender binary (run at camera site)
│   └── server/main.go            ← server receiver binary (run on server)
└── internal/
    ├── config/config.go          ← YAML config parsing
    ├── sender/sender.go          ← GStreamer RTSP→SRT pipeline
    ├── receiver/receiver.go      ← GStreamer SRT→MP4 + chunk monitoring
    ├── analyzer/analyzer.go      ← ffprobe frame analysis
    └── api/api.go                ← HTTP API server
```

## Prerequisites

### Both local and server machines

```bash
# Ubuntu / Debian
sudo apt install golang-go gstreamer1.0-tools gstreamer1.0-plugins-base \
  gstreamer1.0-plugins-good gstreamer1.0-plugins-bad gstreamer1.0-plugins-ugly \
  gstreamer1.0-libav ffmpeg

# Verify GStreamer SRT support
gst-inspect-1.0 srtsink
gst-inspect-1.0 srtsrc

# Verify ffprobe
ffprobe -version
```

## Quick Start

### 1. Edit config.yaml

```yaml
server:
  host: "192.168.1.100"   # IP of your recording server
  base_port: 9000
  http_port: 8080
  output_dir: "./recordings"
  chunk_time: 120
  encoder: "h265"
  frame_detail: true

cameras:
  - id: "camera1"
    name: "Front Gate"
    rtsp_url: "rtsp://admin:pass@192.168.1.10:554/stream1"
    port_offset: 1
    fps: 25
    bitrate: 900
    width: 1920
    height: 1080
    preset: "ultrafast"
    tune: "zerolatency"
    keyint: 30
    bframes: 2
    enabled: true
```

### 2. Build

```bash
cd camera-system

# Download dependencies
go mod tidy

# Build local sender (run this on the camera site machine)
go build -o delta_local ./cmd/local

# Build server receiver (run this on the recording server)
go build -o delta_server ./cmd/server
```

### 3. Run

**On the recording server:**
```bash
./delta_server --config config.yaml
```

**On the camera site machine:**
```bash
./delta_local --config config.yaml
```

Both binaries read the same `config.yaml`. Copy the file to both machines.

## HTTP API

All endpoints run on the server at `http://server:8080`.

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/health` | Health check → `OK` |
| GET | `/cameras` | List all enabled cameras |
| GET | `/list` | List all MP4 files on disk |
| GET | `/api/{camera_id}/chunks` | List analyzed chunks for camera |
| GET | `/api/{camera_id}/chunk/{n}` | Stats for specific chunk |
| GET | `/api/{camera_id}/keyframes/{n}` | Keyframe list for chunk N |
| GET | `/api/{camera_id}/frame/{chunk}/{frame}` | Frame info + seek data |
| GET | `/api/{camera_id}/session` | Full session summary JSON |

### Example requests

```bash
# Health check
curl http://192.168.1.100:8080/health

# List cameras
curl http://192.168.1.100:8080/cameras

# Get chunk 3 stats for camera1
curl http://192.168.1.100:8080/api/camera1/chunk/3 | python3 -m json.tool

# Get keyframes in chunk 3
curl http://192.168.1.100:8080/api/camera1/keyframes/3

# Get frame 150 info in chunk 3
curl http://192.168.1.100:8080/api/camera1/frame/3/150
```

## Output Files (per camera)

```
recordings/
└── camera1/
    ├── chunk_00001.mp4          ← recording chunk (2 min each by default)
    ├── chunk_00002.mp4
    ├── chunk_001_index.json     ← per-frame index (I/P/B types, PTS, size)
    ├── chunk_002_index.json
    └── session_summary.json     ← all chunk stats, updated after each chunk
```

## Adding More Cameras

Add entries to `cameras:` in `config.yaml`. Use a unique `id` and a unique
`port_offset` for each camera. No code changes needed. Restart both binaries.

```yaml
cameras:
  - id: "camera4"
    name: "Lobby"
    rtsp_url: "rtsp://admin:pass@192.168.1.14:554/stream1"
    port_offset: 4
    fps: 25
    bitrate: 800
    width: 1920
    height: 1080
    preset: "ultrafast"
    tune: "zerolatency"
    keyint: 30
    bframes: 2
    enabled: true
```

## Encoder Options

| Setting | Value | Notes |
|---------|-------|-------|
| `encoder` | `h265` | Better compression, requires x265enc |
| `encoder` | `h264` | More compatible, uses x264enc |
| `preset` | `ultrafast` | Lowest CPU, lowest latency |
| `preset` | `fast` | Slightly better quality |
| `tune` | `zerolatency` | Required for live streaming |
| `bframes` | `0` | Lowest latency |
| `bframes` | `2` | Better compression |

## Troubleshooting

**GStreamer not found:**
```bash
which gst-launch-1.0
# If missing: sudo apt install gstreamer1.0-tools
```

**SRT plugin not available:**
```bash
gst-inspect-1.0 srtsrc
# If missing: sudo apt install gstreamer1.0-plugins-bad
```

**x265enc not found (using h264 fallback):**
```bash
gst-inspect-1.0 x265enc
# If missing: sudo apt install gstreamer1.0-plugins-ugly
# Or change encoder: h264 in config.yaml
```

**Port already in use:**
Change `base_port` in config.yaml. SRT port = base_port + port_offset.

**ffprobe analysis fails:**
```bash
which ffprobe
# If missing: sudo apt install ffmpeg
```
