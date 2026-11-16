import hmac
import os
import threading
import time
import json
from datetime import datetime, timezone


DEVICE_HEARTBEAT_TTL = int(os.getenv("DEVICE_HEARTBEAT_TTL", "90"))
DEVICE_HEARTBEAT_TOKEN = os.getenv("DEVICE_HEARTBEAT_TOKEN", "")

# Path to persistent storage file
_HEARTBEAT_FILE = os.path.join(
    os.path.dirname(os.path.abspath(__file__)),
    "device_heartbeats.json"
)

_heartbeat_lock = threading.Lock()
_heartbeats = {}  # Key: device_id, Value: dict with device_id, device_ip, location_id, last_communicated_time, device_status


def _load_heartbeats():
    """Load heartbeats from file on module load"""
    global _heartbeats
    if os.path.exists(_HEARTBEAT_FILE):
        try:
            with open(_HEARTBEAT_FILE, "r") as f:
                _heartbeats = json.load(f)
        except Exception:
            _heartbeats = {}

def _save_heartbeats():
    """Save heartbeats to file"""
    try:
        with open(_HEARTBEAT_FILE, "w") as f:
            json.dump(_heartbeats, f, indent=2)
    except Exception:
        pass

# Load heartbeats on module load
_load_heartbeats()


def validate_device_token(token):
    """Validates an incoming device heartbeat security token using constant-time comparison.

    Args:
        token (str): Security token string from the edge device.

    Returns:
        bool: True if token matches DEVICE_HEARTBEAT_TOKEN, False otherwise.
    """
    if not DEVICE_HEARTBEAT_TOKEN:
        return False
    return hmac.compare_digest(str(token or "").strip(), DEVICE_HEARTBEAT_TOKEN)


def mark_device_heartbeat(device_id, device_ip, location_id=None):
    """Records a fresh heartbeat ping from an edge device and persists state to file.

    Updates the device status to 'online' with the current UTC timestamp under a thread lock.

    Args:
        device_id (str): Unique hardware or logical device identifier (e.g. 'RPI-001').
        device_ip (str): Local or routed IP address of the edge device.
        location_id (str, optional): Facility or bay location identifier.

    Returns:
        bool: True if heartbeat successfully recorded, False on invalid inputs.
    """
    device_id = str(device_id or "").strip()
    device_ip = str(device_ip or "").strip()
    if not device_id or not device_ip:
        return False
    
    now = datetime.now(timezone.utc)
    with _heartbeat_lock:
        _heartbeats[device_id] = {
            "device_id": device_id,
            "device_ip": device_ip,
            "location_id": location_id or _heartbeats.get(device_id, {}).get("location_id", ""),
            "last_communicated_time": now.isoformat().replace("+00:00", "Z"),
            "device_status": "online"
        }
        # Save to file
        _save_heartbeats()
    return True


def get_device_status(device_id):
    """Calculates real-time online/offline status for a specific edge device based on TTL.

    Args:
        device_id (str): Unique device identifier.

    Returns:
        dict | None: Dictionary with device_id, device_ip, location_id, last_communicated_time,
                     and dynamic 'online'/'offline' status (TTL threshold: 90s). None if not found.
    """
    device_id = str(device_id or "").strip()
    if not device_id:
        return None
    
    now = time.time()
    with _heartbeat_lock:
        heartbeat = _heartbeats.get(device_id)
        if not heartbeat:
            return None
        
        # Convert last_communicated_time to timestamp for comparison
        try:
            last_dt = datetime.fromisoformat(heartbeat["last_communicated_time"].replace("Z", "+00:00"))
            last_seen = last_dt.timestamp()
        except:
            last_seen = 0
        
        is_online = (now - last_seen) <= DEVICE_HEARTBEAT_TTL
        
        # Return a copy with updated status
        return {
            "device_id": heartbeat["device_id"],
            "device_ip": heartbeat["device_ip"],
            "location_id": heartbeat.get("location_id", ""),
            "last_communicated_time": heartbeat["last_communicated_time"],
            "device_status": "online" if is_online else "offline"
        }


def check_registered_pi_online(device_id, device_ip):
    """Verifies whether a designated Raspberry Pi is registered, online, and IP-matched.

    Args:
        device_id (str): Target device identifier.
        device_ip (str): Expected device IP address.

    Returns:
        bool: True if device is online and IP matches, False otherwise.
    """
    device_id = str(device_id or "").strip()
    device_ip = str(device_ip or "").strip()
    if not device_id or not device_ip:
        return False
    
    status = get_device_status(device_id)
    if not status:
        return False
    
    return status["device_ip"] == device_ip and status["device_status"] == "online"


def get_all_device_statuses():
    """Retrieves dynamic online/offline status for all registered edge devices.

    Thread-safe computation without nested lock calls to prevent deadlocks.

    Returns:
        dict[str, dict]: Mapping of device_id to status dictionaries with TTL evaluation.
    """
    result = {}
    now = time.time()
    
    with _heartbeat_lock:
        for device_id, heartbeat in _heartbeats.items():
            # Calculate online status without calling get_device_status (to avoid deadlock)
            try:
                last_dt = datetime.fromisoformat(heartbeat["last_communicated_time"].replace("Z", "+00:00"))
                last_seen = last_dt.timestamp()
            except:
                last_seen = 0
            
            is_online = (now - last_seen) <= DEVICE_HEARTBEAT_TTL
            
            result[device_id] = {
                "device_id": heartbeat["device_id"],
                "device_ip": heartbeat["device_ip"],
                "location_id": heartbeat.get("location_id", ""),
                "last_communicated_time": heartbeat["last_communicated_time"],
                "device_status": "online" if is_online else "offline"
            }
    
    return result
