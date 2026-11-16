import os

# Camera Configuration
CAMERA_IP = os.getenv("CAMERA_IP", "192.168.96.30")
ONVIF_PORT = int(os.getenv("ONVIF_PORT", "8888"))
RTSP_PORT = int(os.getenv("RTSP_PORT", "554"))
RTSP_URL = os.getenv("RTSP_URL", f"rtsp://admin:@{CAMERA_IP}:{RTSP_PORT}/ch0_0.264")
USERNAME = os.getenv("CAMERA_USER", "admin")
PASSWORD = os.getenv("CAMERA_PASS", "")

# Web Server & Stream Configuration
SERVER_PORT = int(os.getenv("PORT", "8085"))
SERVER_HOST = os.getenv("HOST", "0.0.0.0")

BASE_DIR = os.path.dirname(os.path.abspath(__file__))
HLS_DIR = os.path.join(BASE_DIR, "hls")
os.makedirs(HLS_DIR, exist_ok=True)
