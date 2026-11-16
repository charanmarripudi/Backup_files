import urllib.request
import urllib.error
import base64
import hashlib
import os
import time
import re
import threading
import xml.etree.ElementTree as ET
from datetime import datetime, timezone
from config import CAMERA_IP, ONVIF_PORT, USERNAME, PASSWORD

class OnvifPtzClient:
    def __init__(self, ip=CAMERA_IP, port=ONVIF_PORT, username=USERNAME, password=PASSWORD):
        self.ip = ip
        self.port = port
        self.username = username
        self.password = password
        self.base_url = f"http://{self.ip}:{self.port}/onvif"
        self.ptz_url = f"{self.base_url}/ptz_service"
        self.media_url = f"{self.base_url}/media"
        self.device_url = f"{self.base_url}/device_service"
        self.profile_token = "PROFILE_000"
        self._lock = threading.Lock()

    def _create_ws_security_header(self):
        """Generates standard WS-Security Password Digest header."""
        created = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        nonce_bytes = os.urandom(16)
        nonce_b64 = base64.b64encode(nonce_bytes).decode("ascii")
        
        sha = hashlib.sha1()
        sha.update(nonce_bytes + created.encode("utf-8") + self.password.encode("utf-8"))
        digest_b64 = base64.b64encode(sha.digest()).decode("ascii")
        
        return f"""
        <s:Header xmlns:s="http://www.w3.org/2003/05/soap-envelope">
          <wsse:Security xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd" xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">
            <wsse:UsernameToken>
              <wsse:Username>{self.username}</wsse:Username>
              <wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">{digest_b64}</wsse:Password>
              <wsse:Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">{nonce_b64}</wsse:Nonce>
              <wsu:Created>{created}</wsu:Created>
            </wsse:UsernameToken>
          </wsse:Security>
        </s:Header>"""

    def _soap_request(self, endpoint_url, body_xml, timeout=3.0):
        """Sends a SOAP XML request to the ONVIF service endpoint."""
        header_xml = self._create_ws_security_header()
        envelope = f"""<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"
            xmlns:trt="http://www.onvif.org/ver10/media/wsdl"
            xmlns:tds="http://www.onvif.org/ver10/device/wsdl"
            xmlns:tt="http://www.onvif.org/ver10/schema">
  {header_xml}
  <s:Body>
    {body_xml}
  </s:Body>
</s:Envelope>"""
        req = urllib.request.Request(
            endpoint_url,
            data=envelope.encode("utf-8"),
            headers={"Content-Type": "application/soap+xml; charset=utf-8"}
        )
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, resp.read().decode("utf-8", errors="ignore")

    def move(self, direction: str, speed: float = 0.5):
        """
        Continuous Pan/Tilt motion.
        Directions: 'up', 'down', 'left', 'right', 'upleft', 'upright', 'downleft', 'downright'
        Speed: 0.1 to 1.0
        """
        speed = max(0.05, min(1.0, float(speed)))
        x, y = 0.0, 0.0
        dir_lower = direction.lower().strip().replace("-", "").replace("_", "").replace(" ", "")

        if dir_lower == "up":
            x, y = 0.0, speed
        elif dir_lower == "down":
            x, y = 0.0, -speed
        elif dir_lower == "left":
            x, y = -speed, 0.0
        elif dir_lower == "right":
            x, y = speed, 0.0
        elif dir_lower in ["upleft", "leftup"]:
            x, y = -speed, speed
        elif dir_lower in ["upright", "rightup"]:
            x, y = speed, speed
        elif dir_lower in ["downleft", "leftdown"]:
            x, y = -speed, -speed
        elif dir_lower in ["downright", "rightdown"]:
            x, y = speed, -speed
        else:
            raise ValueError(f"Unknown direction '{direction}'. Supported: up, down, left, right, upleft, upright, downleft, downright")

        body = f"""
        <tptz:ContinuousMove>
          <tptz:ProfileToken>{self.profile_token}</tptz:ProfileToken>
          <tptz:Velocity>
            <tt:PanTilt x="{x:.2f}" y="{y:.2f}" xmlns:tt="http://www.onvif.org/ver10/schema"/>
          </tptz:Velocity>
        </tptz:ContinuousMove>"""
        with self._lock:
            status, resp = self._soap_request(self.ptz_url, body)
            return {"status": "success", "action": "move", "direction": direction, "speed": speed, "http_status": status}

    def zoom(self, direction: str, speed: float = 0.5):
        """
        Continuous Zoom motion.
        Directions: 'in', 'out'
        Speed: 0.1 to 1.0
        """
        speed = max(0.05, min(1.0, float(speed)))
        z = 0.0
        dir_lower = direction.lower().strip()
        if dir_lower in ["in", "zoomin"]:
            z = speed
        elif dir_lower in ["out", "zoomout"]:
            z = -speed
        else:
            raise ValueError(f"Unknown zoom direction '{direction}'. Supported: in, out")

        body = f"""
        <tptz:ContinuousMove>
          <tptz:ProfileToken>{self.profile_token}</tptz:ProfileToken>
          <tptz:Velocity>
            <tt:Zoom x="{z:.2f}" xmlns:tt="http://www.onvif.org/ver10/schema"/>
          </tptz:Velocity>
        </tptz:ContinuousMove>"""
        with self._lock:
            status, resp = self._soap_request(self.ptz_url, body)
            return {"status": "success", "action": "zoom", "direction": direction, "speed": speed, "http_status": status}

    def stop(self):
        """Stops all Pan, Tilt, and Zoom movements immediately."""
        body = f"""
        <tptz:Stop>
          <tptz:ProfileToken>{self.profile_token}</tptz:ProfileToken>
          <tptz:PanTilt>true</tptz:PanTilt>
          <tptz:Zoom>true</tptz:Zoom>
        </tptz:Stop>"""
        with self._lock:
            status, resp = self._soap_request(self.ptz_url, body)
            return {"status": "success", "action": "stop", "http_status": status}

    def step(self, direction: str, speed: float = 0.5, duration: float = 0.35):
        """
        Performs a discrete step movement in a direction for a short duration, then automatically stops.
        Ideal for single button-clicks from the UI.
        """
        duration = max(0.1, min(3.0, float(duration)))
        self.move(direction, speed)
        time.sleep(duration)
        return self.stop()

    def zoom_step(self, direction: str, speed: float = 0.5, duration: float = 0.35):
        """Performs a discrete step zoom for a short duration, then stops."""
        duration = max(0.1, min(3.0, float(duration)))
        self.zoom(direction, speed)
        time.sleep(duration)
        return self.stop()

    def get_status(self):
        """Returns current Pan, Tilt, and Zoom position and movement status."""
        body = f"""
        <tptz:GetStatus>
          <tptz:ProfileToken>{self.profile_token}</tptz:ProfileToken>
        </tptz:GetStatus>"""
        try:
            status, resp = self._soap_request(self.ptz_url, body)
            pan_match = re.search(r'PanTilt[^>]*x="([^"]+)"\s*y="([^"]+)"', resp)
            zoom_match = re.search(r'Zoom[^>]*x="([^"]+)"', resp)
            
            pan = float(pan_match.group(1)) if pan_match else 0.0
            tilt = float(pan_match.group(2)) if pan_match else 0.0
            zoom = float(zoom_match.group(1)) if zoom_match else 0.0
            
            is_moving = "<MoveStatus>" in resp and ("MOVING" in resp)
            return {
                "status": "online",
                "pan": pan,
                "tilt": tilt,
                "zoom": zoom,
                "is_moving": is_moving,
                "raw_response": resp[:300]
            }
        except Exception as e:
            return {"status": "offline", "error": str(e)}

    def get_presets(self):
        """Retrieves the list of saved PTZ presets from the camera."""
        body = f"""
        <tptz:GetPresets>
          <tptz:ProfileToken>{self.profile_token}</tptz:ProfileToken>
        </tptz:GetPresets>"""
        try:
            status, resp = self._soap_request(self.ptz_url, body)
            presets = []
            # Extract preset tokens and names
            matches = re.findall(r'<[^:]*:Preset[^>]*token="([^"]+)"[^>]*>.*?<[^:]*:Name>(.*?)</[^:]*:Name>', resp, re.DOTALL)
            for token, name in matches:
                presets.append({"token": token, "name": name.strip()})
            return {"status": "success", "presets": presets, "count": len(presets)}
        except Exception as e:
            return {"status": "error", "error": str(e), "presets": []}

    def goto_preset(self, preset_token: str, speed: float = 1.0):
        """Commands the camera to move to a saved preset token."""
        body = f"""
        <tptz:GotoPreset>
          <tptz:ProfileToken>{self.profile_token}</tptz:ProfileToken>
          <tptz:PresetToken>{preset_token}</tptz:PresetToken>
          <tptz:Speed>
            <tt:PanTilt x="{speed:.2f}" y="{speed:.2f}" xmlns:tt="http://www.onvif.org/ver10/schema"/>
            <tt:Zoom x="{speed:.2f}" xmlns:tt="http://www.onvif.org/ver10/schema"/>
          </tptz:Speed>
        </tptz:GotoPreset>"""
        with self._lock:
            status, resp = self._soap_request(self.ptz_url, body)
            return {"status": "success", "action": "goto_preset", "preset_token": preset_token, "http_status": status}

    def set_preset(self, preset_name: str):
        """Saves current camera orientation as a new preset with given name."""
        body = f"""
        <tptz:SetPreset>
          <tptz:ProfileToken>{self.profile_token}</tptz:ProfileToken>
          <tptz:PresetName>{preset_name}</tptz:PresetName>
        </tptz:SetPreset>"""
        with self._lock:
            status, resp = self._soap_request(self.ptz_url, body)
            token_match = re.search(r'<[^:]*:PresetToken>(.*?)</[^:]*:PresetToken>', resp)
            token = token_match.group(1) if token_match else None
            return {"status": "success", "action": "set_preset", "preset_name": preset_name, "token": token}

    def remove_preset(self, preset_token: str):
        """Deletes a preset by its token."""
        body = f"""
        <tptz:RemovePreset>
          <tptz:ProfileToken>{self.profile_token}</tptz:ProfileToken>
          <tptz:PresetToken>{preset_token}</tptz:PresetToken>
        </tptz:RemovePreset>"""
        with self._lock:
            status, resp = self._soap_request(self.ptz_url, body)
            return {"status": "success", "action": "remove_preset", "preset_token": preset_token}

    def get_device_info(self):
        """Fetches manufacturer, model, firmware, serial number, and hardware ID."""
        body = """<tds:GetDeviceInformation/>"""
        try:
            status, resp = self._soap_request(self.device_url, body)
            mfg = re.search(r'<[^:]*:Manufacturer>(.*?)</[^:]*:Manufacturer>', resp)
            model = re.search(r'<[^:]*:Model>(.*?)</[^:]*:Model>', resp)
            firmware = re.search(r'<[^:]*:FirmwareVersion>(.*?)</[^:]*:FirmwareVersion>', resp)
            serial = re.search(r'<[^:]*:SerialNumber>(.*?)</[^:]*:SerialNumber>', resp)
            hardware = re.search(r'<[^:]*:HardwareId>(.*?)</[^:]*:HardwareId>', resp)
            return {
                "status": "online",
                "manufacturer": mfg.group(1) if mfg else "Unknown",
                "model": model.group(1) if model else "Unknown",
                "firmware": firmware.group(1) if firmware else "Unknown",
                "serial_number": serial.group(1) if serial else "Unknown",
                "hardware_id": hardware.group(1) if hardware else "Unknown"
            }
        except Exception as e:
            return {"status": "error", "error": str(e)}

    def get_profiles(self):
        """Retrieves available media stream profiles."""
        body = """<trt:GetProfiles/>"""
        try:
            status, resp = self._soap_request(self.media_url, body)
            tokens = re.findall(r'<trt:Profiles[^>]*token="([^"]+)"', resp)
            return {"status": "success", "profiles": tokens}
        except Exception as e:
            return {"status": "error", "error": str(e), "profiles": []}
