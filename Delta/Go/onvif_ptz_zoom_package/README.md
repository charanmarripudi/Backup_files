# ONVIF & CGI Camera PTZ + Zoom Integration Package

This package contains everything needed to implement **Pan/Tilt (Up, Down, Left, Right)** and **Zoom (Zoom In, Zoom Out)** for IP cameras across any backend system or frontend dashboard.

---

## 📁 Package File Structure

```text
onvif_ptz_zoom_package/
├── utilities/
│   ├── onvif_controller.py       # Core Python Controller (ONVIF & CGI protocol handling)
│   └── digital_zoom_player.js    # Frontend JavaScript helper for digital player video scaling
├── api_manager/
│   └── devices_onvif_actions.py  # FastAPI Router Endpoints (/control-pan-tilt, /control-zoom)
├── ptz_camera_dashboard.html     # Interactive Frontend Web Dashboard (8-Way D-Pad & Zoom Controls)
├── test_zoom.py                  # CLI Terminal Test Script
├── requirements.txt              # Python dependencies
└── README.md                     # Integration guide
```

---

## 🛠️ Step-by-Step Integration for Other Systems

### 1. Copy Files to Your Target Repository
- Copy `utilities/onvif_controller.py` into your project's `utilities/` or `services/` directory.
- Copy `api_manager/devices_onvif_actions.py` into your project's API routes directory.
- Copy `utilities/digital_zoom_player.js` into your frontend JavaScript assets folder.

### 2. Install Python Dependencies
```bash
pip install -r requirements.txt
```

### 3. Register the FastAPI Router (Backend)
In your main FastAPI application (`main.py` or `server.py`):
```python
from api_manager.devices_onvif_actions import router as devices_router

app.include_router(devices_router)
```

---

## 📡 API Endpoints Reference

### 1. Pan / Tilt Control (`/devices/control-pan-tilt`)
**POST** `http://your-server:8000/devices/control-pan-tilt`

**Request Body:**
```json
{
  "device_ip": "192.168.96.30",
  "onvif_port": 80,
  "onvif_username": "admin",
  "onvif_password": "",
  "position": "Up"
}
```
*Allowed `position` values*: `"Up"`, `"Down"`, `"Left"`, `"Right"`, `"UpLeft"`, `"UpRight"`, `"DownLeft"`, `"DownRight"`.

### 2. Zoom Control (`/devices/control-zoom`)
**POST** `http://your-server:8000/devices/control-zoom`

**Request Body:**
```json
{
  "device_ip": "192.168.96.30",
  "onvif_port": 80,
  "onvif_username": "admin",
  "onvif_password": "",
  "position": "ZoomIn"
}
```
*Allowed `position` values*: `"ZoomIn"`, `"ZoomOut"`.

---

## 🎮 Frontend Digital Zoom Integration

To enable real-time video player zoom in any web application:

```javascript
// Import or include digital_zoom_player.js
const zoomPlayer = new DigitalZoomPlayer('video-container-id', {
    step: 0.25,
    maxZoom: 4.0,
    minZoom: 1.0
});

// Attach to UI buttons
document.getElementById('zoom-in-btn').onclick = () => zoomPlayer.zoomIn();
document.getElementById('zoom-out-btn').onclick = () => zoomPlayer.zoomOut();
```

---

## 🧪 Terminal Command Testing

You can test camera controls on any system using `test_zoom.py`:

```bash
python test_zoom.py in        # Zoom In (+)
python test_zoom.py out       # Zoom Out (-)
python test_zoom.py up        # Move Up
python test_zoom.py down      # Move Down
python test_zoom.py left      # Move Left
python test_zoom.py right     # Move Right
```
