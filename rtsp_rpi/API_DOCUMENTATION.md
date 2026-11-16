# RELIANCE AI VISION - INDUSTRIAL EDGE INFERENCE PLATFORM
## Complete REST API Specification & Endpoint Documentation
> **Base URL**: `http://localhost:8080` (or `http://localhost:8000`)
> **Interactive Swagger UI**: `http://localhost:8080/docs`
> **ReDoc UI**: `http://localhost:8080/redoc`

---


## 1. Core System & Inference Lifecycle APIs

These endpoints control starting, stopping, and monitoring the multi-threaded YOLOv8 edge detector workers.

### `POST` `/api/start`
**Description**: Spawns a DetectorWorker thread for a camera, connecting to RTSP and running YOLO inference.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
{
  "camera": "0",
  "rtsp": "rtsp://127.0.0.1:8554/webcam",
  "models": [
    "crash_guard.pt"
  ],
  "conf": 0.4,
  "iou": 0.45,
  "location": "Location 20",
  "roi": [
    [
      [
        0.12,
        0.25
      ],
      [
        0.88,
        0.25
      ],
      [
        0.88,
        0.85
      ],
      [
        0.12,
        0.85
      ]
    ]
  ]
}
```

**Server Response**:
```json
{
  "status": "started",
  "camera": "0",
  "stream_id": 0,
  "hls_url": "/hls/camera/0/playlist.m3u8",
  "models": [
    "crash_guard.pt"
  ]
}
```

---

### `POST` `/api/stop`
**Description**: Gracefully terminates the DetectorWorker thread, releases camera RTSP sockets, and flushes HLS segments.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
{
  "camera": "0"
}
```

**Server Response**:
```json
{
  "status": "ok",
  "camera": "0"
}
```

---

### `GET` `/api/status`
**Description**: Returns real-time status of all running camera workers and their active assigned models.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
None (GET request)
```

**Server Response**:
```json
{
  "active": [
    "0"
  ],
  "models": {
    "0": [
      "crash_guard.pt"
    ]
  }
}
```

---

### `GET` `/api/detection/status`
**Description**: Returns active camera IDs currently undergoing live inference.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
None (GET request)
```

**Server Response**:
```json
{
  "running": [
    "0"
  ],
  "count": 1
}
```

---


## 2. Camera Stream Configuration APIs

Manages persistent RTSP camera links, site locations, and assigned use cases.

### `GET` `/api/streams`
**Description**: Retrieves the full list of configured cameras with RTSP URLs, location IDs, assigned models, and ROIs.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
None (GET request)
```

**Server Response**:
```json
[
  {
    "id": "0",
    "name": "Location 20 - Cam 0",
    "rtsp": "rtsp://127.0.0.1:8554/webcam",
    "location": "Location 20",
    "location_id": "loc_20",
    "models": [
      "crash_guard.pt"
    ],
    "rois": [
      [
        [
          0.12,
          0.25
        ],
        [
          0.88,
          0.25
        ],
        [
          0.88,
          0.85
        ],
        [
          0.12,
          0.85
        ]
      ]
    ]
  }
]
```

---

### `POST` `/api/streams`
**Description**: Adds or updates camera stream metadata in streams.json.

**Query / Path Parameters**:
```
replace_all (bool, optional, default=false)
```

**Request Payload (Body)**:
```json
[
  {
    "id": "0",
    "name": "CAM 0 (Entry Gate)",
    "rtsp": "rtsp://127.0.0.1:8554/webcam",
    "location": "Location 20"
  }
]
```

**Server Response**:
```json
{
  "status": "saved",
  "count": 1
}
```

---

### `DELETE` `/api/streams`
**Description**: Removes a camera stream from the configuration.

**Query / Path Parameters**:
```
camera_id (query param, e.g. ?camera_id=0)
```

**Request Payload (Body)**:
```json
None
```

**Server Response**:
```json
{
  "status": "deleted",
  "camera_id": "0"
}
```

---


## 3. Models & Detectable Classes APIs

Endpoints exposing available YOLO model weights and their mapped detection classes.

### `GET` `/api/models`
**Description**: Returns all available .pt model weight files located in the models/ directory.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
None (GET request)
```

**Server Response**:
```json
{
  "models": [
    "crash_guard.pt",
    "dome.pt",
    "fire_smoke.pt",
    "leakage.pt",
    "ppe.pt",
    "spark_arrestor.pt",
    "tt_branding.pt",
    "tt_crew_entering_below_tt.pt",
    "tt_crew_non_availability.pt",
    "unauthorized_activity.pt",
    "wrong_parking_vehicle_counting.pt",
    "yolov8n.pt"
  ]
}
```

---

### `GET` `/api/model-classes`
**Description**: Returns the complete dictionary mapping each model to its detectable classes.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
None (GET request)
```

**Server Response**:
```json
{
  "crash_guard.pt": [
    "no-Crash_Guard"
  ],
  "dome.pt": [
    "Dome_Opened"
  ],
  "fire_smoke.pt": [
    "fire",
    "smoke"
  ],
  "leakage.pt": [
    "leak"
  ],
  "ppe.pt": [
    "NO-Hardhat",
    "NO-Mask",
    "NO-Safety Vest",
    "No-Gloves"
  ],
  "tt_crew_entering_below_tt.pt": [
    "tt_crew_below_tt"
  ],
  "tt_crew_non_availability.pt": [
    "no-TT_Crew_Availability"
  ],
  "wrong_parking_vehicle_counting.pt": [
    "wrong_parking",
    "truck",
    "car",
    "license_plate"
  ]
}
```

---

### `POST` `/api/camera-models`
**Description**: Assigns specific models and selective subclass checkboxes to a camera.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
{
  "camera_id": "0",
  "models": {
    "crash_guard.pt": {
      "enabled_classes": [
        "no-Crash_Guard"
      ],
      "conf": 0.4,
      "iou": 0.45
    }
  }
}
```

**Server Response**:
```json
{
  "status": "saved",
  "camera_id": "0"
}
```

---


## 4. Safety Alerts & Evidence APIs

Endpoints handling safety violation retrieval, database persistence, and snapshot evidence images.

### `GET` `/api/alerts`
**Description**: Retrieves the latest 100 safety violation alerts from local storage (alerts.json).

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
None (GET request)
```

**Server Response**:
```json
[
  {
    "id": 1731410656000,
    "camera_id": "0",
    "location": "Location 20",
    "type_of_alert": "VIOLATION: no-Crash_Guard 0.90",
    "image": "/hls/alerts/cam0_20261112_123104_no_crash_guard.jpg",
    "created_at": "2026-11-12 12:31:04",
    "is_negative": true
  }
]
```

---

### `POST` `/api/alerts/db`
**Description**: Stores a newly detected safety violation into the PostgreSQL alerts table.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
{
  "camera_id": "0",
  "location": "Location 20",
  "type_of_alert": "VIOLATION: TT Crew Entering Below TT",
  "image_path": "/hls/alerts/cam0_20261112_130416_tt_crew_below.jpg"
}
```

**Server Response**:
```json
{
  "status": "success"
}
```

---

### `GET` `/api/alerts/images/{filename}`
**Description**: Fetches high-resolution annotated snapshot JPEG showing the restricted ROI and bold red violation box.

**Query / Path Parameters**:
```
filename (path param, e.g. cam0_20261112_130416.jpg)
```

**Request Payload (Body)**:
```json
None
```

**Server Response**:
```json
Binary JPEG image data (image/jpeg)
```

---


## 5. Live Video Streaming APIs (HLS & Frames)

Serves real-time annotated video to the browser with sub-second latency.

### `GET` `/hls/camera/{cam_id}/playlist.m3u8`
**Description**: Master HLS m3u8 playlist file read directly from the RAM disk (/tmp/hls).

**Query / Path Parameters**:
```
cam_id (path param, e.g. '0')
```

**Request Payload (Body)**:
```json
None
```

**Server Response**:
```json
Content-Type: application/vnd.apple.mpegurl

#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXTINF:2.000,
segment_1731410656_0.ts
#EXTINF:2.000,
segment_1731410656_1.ts
```

---

### `GET` `/hls/camera/{cam_id}/{filename}`
**Description**: Serves individual 2-second H.264 Transport Stream (.ts) video chunks.

**Query / Path Parameters**:
```
cam_id, filename (path params)
```

**Request Payload (Body)**:
```json
None
```

**Server Response**:
```json
Binary MPEG-2 Transport Stream (video/mp2t)
```

---


## 6. AI Video Lab (Interactive Offline Testing)

Endpoints powering the AI Video Lab for uploading MP4 clips, tuning sliders in real time, and batch processing.

### `POST` `/api/video-test/upload`
**Description**: Uploads an MP4 or AVI video file to the server for AI verification.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
Multipart form-data: file=@test_truck.mp4
```

**Server Response**:
```json
{
  "status": "success",
  "filename": "test_truck.mp4",
  "filesize": 4823412,
  "duration_sec": 12.4
}
```

---

### `POST` `/api/video-test/live-start`
**Description**: Starts interactive live AI video playback of an uploaded video with dynamic slider controls.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
{
  "filename": "test_truck.mp4",
  "model": "wrong_parking_vehicle_counting.pt",
  "conf": 0.35,
  "iou": 0.45,
  "enabled_classes": [
    "wrong_parking",
    "truck",
    "license_plate"
  ]
}
```

**Server Response**:
```json
{
  "status": "started",
  "stream_url": "/api/video-test/live-frame"
}
```

---

### `POST` `/api/video-test/live-params`
**Description**: Dynamically updates Confidence and IoU sliders in real-time without restarting playback.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
{
  "conf": 0.45,
  "iou": 0.5,
  "enabled_classes": [
    "wrong_parking"
  ]
}
```

**Server Response**:
```json
{
  "status": "updated"
}
```

---

### `GET` `/api/video-test/live-frame`
**Description**: Streams real-time MJPEG annotated video frames directly to the frontend Video Lab player.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
None
```

**Server Response**:
```json
Multipart MJPEG Stream (multipart/x-mixed-replace; boundary=frame)
```

---

### `POST` `/api/video-test/process`
**Description**: Runs offline batch frame-by-frame analysis, generating an annotated output video and CSV detection log.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
{
  "filename": "test_truck.mp4",
  "model": "crash_guard.pt",
  "conf": 0.35,
  "iou": 0.45
}
```

**Server Response**:
```json
{
  "job_id": "job_9a8b7c",
  "status": "processing"
}
```

---

### `GET` `/api/video-test/csv/{job_id}`
**Description**: Downloads the frame-by-frame telemetry detection CSV report for offline audit.

**Query / Path Parameters**:
```
job_id (path param)
```

**Request Payload (Body)**:
```json
None
```

**Server Response**:
```json
CSV Text File: frame_idx, timestamp, class, confidence, x1, y1, x2, y2
```

---


## 7. Vehicle Speed Tracker APIs

Controls optical speed measurement using dual gate calibration lines.

### `GET` `/api/speed/config`
**Description**: Returns current speed limit, gate distance in meters, and calibration line pixel ratios.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
None
```

**Server Response**:
```json
{
  "speed_limit_kmh": 10.0,
  "road_distance_meters": 20.0,
  "line_a_ratio": 0.4,
  "line_b_ratio": 0.75
}
```

---

### `POST` `/api/speed/config`
**Description**: Updates speed threshold and physical distance calibration parameters.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
{
  "speed_limit_kmh": 15.0,
  "road_distance_meters": 25.0,
  "line_a_ratio": 0.35,
  "line_b_ratio": 0.7
}
```

**Server Response**:
```json
{
  "status": "saved"
}
```

---

### `GET` `/api/speed/violations`
**Description**: Retrieves the list of overspeeding vehicles with recorded speeds, timestamps, and camera IDs.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
None
```

**Server Response**:
```json
[
  {
    "vehicle_id": "truck_42",
    "speed_kmh": 24.3,
    "limit_kmh": 10.0,
    "timestamp": "2026-11-12 14:15:30"
  }
]
```

---


## 8. Device Management & ONVIF PTZ APIs

Hardware discovery, health monitoring, and PTZ motor controls.

### `GET` `/api/devices`
**Description**: Lists all ONVIF IP camera devices discovered on the plant network.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
None
```

**Server Response**:
```json
[
  {
    "id": "dev_01",
    "ip": "192.168.1.105",
    "port": 80,
    "name": "Bay 3 PTZ Camera",
    "manufacturer": "Hikvision / Dahua",
    "status": "online"
  }
]
```

---

### `POST` `/devices/control/zoom`
**Description**: Sends continuous or absolute optical zoom commands to an ONVIF PTZ camera.

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
{
  "device_id": "dev_01",
  "zoom_velocity": 0.5,
  "timeout_seconds": 2
}
```

**Server Response**:
```json
{
  "status": "success",
  "zoom": "active"
}
```

---

### `GET` `/api/rpi-status`
**Description**: Returns Raspberry Pi edge hardware telemetry (CPU usage, RAM usage, core temperature).

**Query / Path Parameters**:
```
None
```

**Request Payload (Body)**:
```json
None
```

**Server Response**:
```json
{
  "cpu_percent": 54.2,
  "memory_percent": 48.0,
  "temperature_c": 51.5,
  "uptime_hours": 142.5
}
```

---
