import os
import uvicorn
from fastapi import FastAPI, HTTPException, Body, Query
from fastapi.middleware.cors import CORSMiddleware
from fastapi.staticfiles import StaticFiles
from fastapi.responses import FileResponse, JSONResponse
from pydantic import BaseModel, Field
from typing import Optional, List

from config import CAMERA_IP, ONVIF_PORT, RTSP_URL, SERVER_PORT, SERVER_HOST, HLS_DIR
from onvif_client import OnvifPtzClient
from stream_service import StreamService

app = FastAPI(
    title="ONVIF PTZ Camera Controller API",
    description="Production-ready REST API and Web Controller for ONVIF PTZ cameras (Pan, Tilt, Zoom, Presets, Status, and Live Stream).",
    version="1.0.0"
)

# Enable Full CORS so any frontend UI (React, Vue, Angular, Vanilla JS, ThingsBoard) can consume the APIs seamlessly
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

# Initialize ONVIF Client and Stream Service
ptz_client = OnvifPtzClient()
stream_service = StreamService()

# Serve static files and HLS segments
STATIC_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "static")
os.makedirs(STATIC_DIR, exist_ok=True)
os.makedirs(HLS_DIR, exist_ok=True)

app.mount("/static", StaticFiles(directory=STATIC_DIR), name="static")
app.mount("/hls", StaticFiles(directory=HLS_DIR), name="hls")

# ─────────────────────────────────────────────────────────────
# PYDANTIC REQUEST SCHEMAS
# ─────────────────────────────────────────────────────────────

class MoveRequest(BaseModel):
    direction: str = Field(..., description="Direction: up, down, left, right, upleft, upright, downleft, downright", example="left")
    speed: Optional[float] = Field(0.5, ge=0.05, le=1.0, description="Movement speed between 0.05 and 1.0", example=0.5)

class StepRequest(BaseModel):
    direction: str = Field(..., description="Direction: up, down, left, right, upleft, upright, downleft, downright", example="up")
    speed: Optional[float] = Field(0.5, ge=0.05, le=1.0, description="Movement speed between 0.05 and 1.0", example=0.5)
    duration: Optional[float] = Field(0.35, ge=0.05, le=3.0, description="Step duration in seconds", example=0.35)

class ZoomRequest(BaseModel):
    direction: str = Field(..., description="Zoom direction: in, out", example="in")
    speed: Optional[float] = Field(0.5, ge=0.05, le=1.0, description="Zoom speed between 0.05 and 1.0", example=0.5)

class ZoomStepRequest(BaseModel):
    direction: str = Field(..., description="Zoom direction: in, out", example="in")
    speed: Optional[float] = Field(0.5, ge=0.05, le=1.0, description="Zoom speed between 0.05 and 1.0", example=0.5)
    duration: Optional[float] = Field(0.35, ge=0.05, le=3.0, description="Zoom step duration in seconds", example=0.35)

class PresetGotoRequest(BaseModel):
    preset_token: str = Field(..., description="Token of the preset to move to", example="1")
    speed: Optional[float] = Field(1.0, ge=0.1, le=1.0, description="Speed to travel to preset", example=1.0)

class PresetSetRequest(BaseModel):
    preset_name: str = Field(..., description="Human-readable name for the new preset", example="Conference Table")

# ─────────────────────────────────────────────────────────────
# REST API ENDPOINTS
# ─────────────────────────────────────────────────────────────

@app.on_event("startup")
def startup_event():
    # Auto-start HLS stream transcoding in background
    try:
        stream_service.start()
    except Exception as e:
        print(f"[STREAM] Warning: Could not auto-start stream: {e}", flush=True)

@app.on_event("shutdown")
def shutdown_event():
    stream_service.stop()

@app.get("/")
def get_ui():
    """Serves the interactive PTZ controller Web Dashboard."""
    index_path = os.path.join(STATIC_DIR, "index.html")
    if os.path.exists(index_path):
        return FileResponse(index_path)
    return {"status": "ok", "message": "ONVIF PTZ Camera API is running. Visit /docs for Swagger documentation."}

# ── PTZ MOVEMENT APIS ──

@app.post("/api/ptz/move", summary="Continuous Pan/Tilt Movement")
def ptz_move(req: MoveRequest):
    """
    Commands the camera to continuously pan/tilt in the specified direction.
    Call `/api/ptz/stop` when the user releases the button (e.g. mouseup/touchend).
    """
    try:
        res = ptz_client.move(req.direction, req.speed)
        return res
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@app.post("/api/ptz/step", summary="Discrete Step Pan/Tilt Movement")
def ptz_step(req: StepRequest):
    """
    Performs a short discrete step movement in a direction for the given duration, then automatically stops.
    Ideal for single button-clicks from the UI.
    """
    try:
        res = ptz_client.step(req.direction, req.speed, req.duration)
        return {"status": "success", "action": "step", "direction": req.direction, "duration": req.duration}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@app.post("/api/ptz/zoom", summary="Continuous Zoom Movement")
def ptz_zoom(req: ZoomRequest):
    """
    Commands the camera to continuously zoom in or out.
    Call `/api/ptz/stop` when the button is released.
    """
    try:
        res = ptz_client.zoom(req.direction, req.speed)
        return res
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@app.post("/api/ptz/zoom-step", summary="Discrete Step Zoom")
def ptz_zoom_step(req: ZoomStepRequest):
    """Performs a short discrete zoom step then automatically stops."""
    try:
        res = ptz_client.zoom_step(req.direction, req.speed, req.duration)
        return {"status": "success", "action": "zoom_step", "direction": req.direction, "duration": req.duration}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@app.post("/api/ptz/stop", summary="Stop All PTZ Movement")
def ptz_stop():
    """Immediately stops all ongoing Pan, Tilt, and Zoom motions."""
    try:
        res = ptz_client.stop()
        return res
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@app.get("/api/ptz/status", summary="Get Current PTZ Status & Position")
def ptz_status():
    """Returns current PTZ coordinates (pan, tilt, zoom) and moving status."""
    return ptz_client.get_status()

# ── PRESET MANAGEMENT APIS ──

@app.get("/api/ptz/presets", summary="List Saved PTZ Presets")
def list_presets():
    """Returns all saved presets on the camera with their tokens and names."""
    return ptz_client.get_presets()

@app.post("/api/ptz/presets/goto", summary="Move Camera to Preset")
def goto_preset(req: PresetGotoRequest):
    """Commands the camera to navigate to a saved preset token."""
    try:
        res = ptz_client.goto_preset(req.preset_token, req.speed)
        return res
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@app.post("/api/ptz/presets/set", summary="Save Current Position as Preset")
def set_preset(req: PresetSetRequest):
    """Saves the camera's current pan/tilt/zoom position as a new preset."""
    try:
        res = ptz_client.set_preset(req.preset_name)
        return res
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

@app.delete("/api/ptz/presets/{token}", summary="Delete Preset")
def delete_preset(token: str):
    """Deletes a preset by token."""
    try:
        res = ptz_client.remove_preset(token)
        return res
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))

# ── CAMERA & STREAM INFO APIS ──

@app.get("/api/camera/info", summary="Get Camera Information")
def camera_info():
    """Returns camera hardware, firmware, manufacturer details, and media profiles."""
    dev_info = ptz_client.get_device_info()
    profiles = ptz_client.get_profiles()
    return {
        "camera_ip": CAMERA_IP,
        "onvif_port": ONVIF_PORT,
        "rtsp_url": RTSP_URL,
        "device_info": dev_info,
        "profiles": profiles.get("profiles", []),
        "stream_alive": stream_service.is_alive()
    }

@app.get("/api/camera/stream", summary="Get Live Stream Information")
def stream_info():
    """Returns RTSP URL, live HLS URL, and stream status."""
    return {
        "rtsp_url": RTSP_URL,
        "hls_url": "/hls/live/playlist.m3u8",
        "is_active": stream_service.is_alive()
    }

@app.post("/api/camera/stream/restart", summary="Restart Live HLS Stream")
def restart_stream():
    """Restarts the background FFmpeg transcoder."""
    stream_service.stop()
    res = stream_service.start()
    return res

if __name__ == "__main__":
    print(f"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
    print(f"  ONVIF PTZ Camera Service Started")
    print(f"  Camera IP : {CAMERA_IP}:{ONVIF_PORT}")
    print(f"  RTSP URL  : {RTSP_URL}")
    print(f"  API Docs  : http://localhost:{SERVER_PORT}/docs")
    print(f"  Web UI    : http://localhost:{SERVER_PORT}/")
    print(f"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
    uvicorn.run("server:app", host=SERVER_HOST, port=SERVER_PORT, reload=False)
