# ONVIF PTZ Camera Control Service & REST API

Production-ready REST API and interactive Web Controller for PTZ IP Cameras (Pan, Tilt, Zoom, Presets, Status, and Live Video Stream).

* **Camera IP**: `192.168.96.30`
* **ONVIF Port**: `8888`
* **RTSP Stream URL**: `rtsp://admin:@192.168.96.30:554/ch0_0.264`
* **Web Service Port**: `8085`
* **Interactive UI**: `http://localhost:8085/`
* **Swagger API Documentation**: `http://localhost:8085/docs`

---

## 🚀 Quick Start

```bash
cd /Users/algofusion/Documents/dnc_backend_v2/orchestrator/Delta/Go/onvif_ptz
./start.sh
```
Or directly:
```bash
python3 server.py
```

---

## 📡 REST API Documentation for Frontend UI

*Full CORS is enabled (`Access-Control-Allow-Origin: *`), so your React / Vue / Angular / Vanilla frontend can call these endpoints directly from any port/domain.*

---

### 1. Continuous Pan / Tilt Movement
*Use on `mousedown` / `touchstart` (when user holds down an arrow button).*

* **Endpoint**: `POST /api/ptz/move`
* **Headers**: `Content-Type: application/json`
* **Payload**:
```json
{
  "direction": "left",
  "speed": 0.5
}
```
* **Supported Directions**: `"up"`, `"down"`, `"left"`, `"right"`, `"upleft"`, `"upright"`, `"downleft"`, `"downright"`
* **Speed Range**: `0.05` to `1.0` (Default: `0.5`)

#### JavaScript Example:
```javascript
async function moveCamera(direction, speed = 0.5) {
  await fetch("http://localhost:8085/api/ptz/move", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ direction, speed })
  });
}
```

---

### 2. Stop All Movement
*Use on `mouseup` / `touchend` / `mouseleave` (when user releases the arrow button).*

* **Endpoint**: `POST /api/ptz/stop`
* **Payload**: `{}` (Empty JSON or no body)

#### JavaScript Example:
```javascript
async function stopCamera() {
  await fetch("http://localhost:8085/api/ptz/stop", { method: "POST" });
}
```

---

### 3. Discrete Step Movement (Nudge on Single Click)
*Use on single button clicks to rotate the camera in small steps.*

* **Endpoint**: `POST /api/ptz/step`
* **Payload**:
```json
{
  "direction": "up",
  "speed": 0.5,
  "duration": 0.35
}
```
* **Duration**: Seconds to move before stopping automatically (`0.1` to `3.0`).

#### JavaScript Example:
```javascript
async function stepCamera(direction, speed = 0.5, duration = 0.35) {
  await fetch("http://localhost:8085/api/ptz/step", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ direction, speed, duration })
  });
}
```

---

### 4. Zoom Control
*Zoom In or Out continuously or in steps.*

#### Continuous Zoom:
* **Endpoint**: `POST /api/ptz/zoom`
* **Payload**: `{"direction": "in", "speed": 0.5}`  *(or `"direction": "out"`)*

#### Step Zoom (Single Click):
* **Endpoint**: `POST /api/ptz/zoom-step`
* **Payload**: `{"direction": "in", "speed": 0.5, "duration": 0.35}`

---

### 5. Get Current PTZ Coordinates & Status
* **Endpoint**: `GET /api/ptz/status`
* **Response**:
```json
{
  "status": "online",
  "pan": 0.15,
  "tilt": -0.05,
  "zoom": 0.0,
  "is_moving": false
}
```

---

### 6. Preset Management

#### List All Presets:
* **Endpoint**: `GET /api/ptz/presets`
* **Response**:
```json
{
  "status": "success",
  "presets": [
    { "token": "1", "name": "Main Table" },
    { "token": "2", "name": "Entrance Door" }
  ],
  "count": 2
}
```

#### Go to Preset:
* **Endpoint**: `POST /api/ptz/presets/goto`
* **Payload**: `{"preset_token": "1", "speed": 1.0}`

#### Save Current Position as Preset:
* **Endpoint**: `POST /api/ptz/presets/set`
* **Payload**: `{"preset_name": "Gate A"}`

#### Delete Preset:
* **Endpoint**: `DELETE /api/ptz/presets/{token}`

---

### 7. Camera Info & Video Stream Endpoints

* **Camera Info**: `GET /api/camera/info`
* **Live Stream Metadata**: `GET /api/camera/stream`
* **HLS Live Video Feed**: `http://localhost:8085/hls/live/playlist.m3u8`
