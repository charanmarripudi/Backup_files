"""
video_tester.py - Edge AI Video Testing Engine for RTSP & Edge Server.
Supports both Instant Live Stream Mode (real-time MJPEG detection playback with live slider tuning)
and Offline Web-Compatible H.264 MP4 export.
"""

import os
import sys
import time
import math
import uuid
import shutil
import logging
import threading
import subprocess
import re
import hashlib
import csv
from datetime import datetime
from pathlib import Path
import cv2
import numpy as np

try:
    import torch
    torch.set_num_threads(4)
except Exception:
    torch = None

# Ensure root directory is on path
_curr = Path(__file__).resolve()
BASE_DIR = None
for p in [_curr.parent, _curr.parent.parent, _curr.parent.parent.parent]:
    if (p / "models").is_dir() and (p / "backend").is_dir():
        BASE_DIR = p
        break
if BASE_DIR is None:
    BASE_DIR = _curr.parents[2] if len(_curr.parents) > 2 else _curr.parent.parent

BACKEND_DIR = BASE_DIR / "backend"
DETECTOR_DIR = BACKEND_DIR / "detector"

for p in [str(DETECTOR_DIR), str(BACKEND_DIR), str(BASE_DIR)]:
    if p not in sys.path:
        sys.path.insert(0, p)

try:
    from ultralytics import YOLO
except ImportError:
    YOLO = None

try:
    from backend.fast_anpr import GLOBAL_FAST_ANPR, is_vehicle_class
except ImportError:
    try:
        from fast_anpr import GLOBAL_FAST_ANPR, is_vehicle_class
    except ImportError:
        GLOBAL_FAST_ANPR = None
        is_vehicle_class = lambda c: any(k in str(c).lower() for k in ("truck", "car", "bus", "vehicle", "tanker")) and not any(k in str(c).lower() for k in ("crew", "person"))

try:
    from detector_worker import (
        get_yolo_model,
        get_dynamic_class_color,
        match_class,
        clean_str,
        extract_negation_and_core,
        classify_pose_activity,
        is_opposite_class,
        is_negative_violation_class
    )
except ImportError:
    try:
        from detector.detector_worker import (
            get_yolo_model,
            get_dynamic_class_color,
            match_class,
            clean_str,
            extract_negation_and_core,
            classify_pose_activity,
            is_opposite_class,
            is_negative_violation_class
        )
    except ImportError:
        classify_pose_activity = None
        is_opposite_class = None
        is_negative_violation_class = lambda c, m="": any(k in str(c).lower() for k in ("no-", "no_", "without", "non", "violation", "entering_below", "leak", "fire", "smoke"))
        YOLO_CACHE = {}
        def get_yolo_model(model_path):
            if model_path not in YOLO_CACHE:
                YOLO_CACHE[model_path] = YOLO(model_path)
            return YOLO_CACHE[model_path]

    def clean_str(s):
        res = re.sub(r'[^a-z0-9]', '', str(s).lower())
        return res.replace("saftey", "safety")

    def extract_negation_and_core(s):
        s_str = str(s).strip()
        if " - " in s_str:
            s_str = s_str.split(" - ")[-1]
        elif ":" in s_str:
            s_str = s_str.split(":")[-1]
        elif "/" in s_str:
            s_str = s_str.split("/")[-1]
            
        cleaned = clean_str(s_str)
        neg_prefixes = ["no", "without", "non", "un"]
        is_neg = False
        core = cleaned
        for p in neg_prefixes:
            if cleaned.startswith(p):
                is_neg = True
                core = cleaned[len(p):]
                break
        
        synonym_map = {
            "hardhat": "headgear",
            "helmet": "headgear",
            "safetyhelmet": "headgear",
            "headprotection": "headgear",
            "head": "headgear",
            "vest": "bodyvest",
            "safetyvest": "bodyvest",
            "reflectivevest": "bodyvest",
            "jacket": "bodyvest",
            "mask": "facemask",
            "facemask": "facemask",
            "facecover": "facemask",
            "goggles": "eyegoggles",
            "glasses": "eyegoggles",
            "safetyglasses": "eyegoggles",
            "eyewear": "eyegoggles",
            "glove": "handgloves",
            "gloves": "handgloves",
            "handglove": "handgloves",
            "shoe": "footshoes",
            "shoes": "footshoes",
            "boot": "footshoes",
            "boots": "footshoes",
            "safetyshoe": "footshoes",
            "safetyshoes": "footshoes",
            "person": "person",
            "worker": "person",
            "human": "person",
            "man": "person",
            "woman": "person",
            "fire": "fire",
            "flame": "fire",
            "smoke": "smoke",
        }
        mapped_core = synonym_map.get(core, core)
        return is_neg, mapped_core, cleaned

    def match_class(box_cls, enabled_cls):
        if not box_cls or not enabled_cls:
            return False
        b_neg, b_core, b_clean = extract_negation_and_core(box_cls)
        e_neg, e_core, e_clean = extract_negation_and_core(enabled_cls)
        if b_clean == e_clean: return True

        # License plate matching
        if ("licenseplate" in b_clean or "license_plate" in b_clean) and ("licenseplate" in e_clean or "license_plate" in e_clean):
            return True

        # Wrong parking matching
        if ("wrongparking" in b_clean or "wrong_parking" in b_clean) and ("wrongparking" in e_clean or "wrong_parking" in e_clean):
            return True

        if b_neg != e_neg: return False
        if b_core == e_core: return True
        if (b_core in e_core or e_core in b_core) and len(b_core) >= 3 and len(e_core) >= 3: return True
        return False

    def is_opposite_class(cls1, cls2):
        try:
            b_neg, b_core, _ = extract_negation_and_core(cls1)
            e_neg, e_core, _ = extract_negation_and_core(cls2)
            if b_neg != e_neg and b_core == e_core and len(b_core) >= 3:
                return True
        except Exception:
            pass
        return False

    def get_dynamic_class_color(class_name):
        if not class_name:
            return (0, 255, 255)
        is_neg, core, cleaned = extract_negation_and_core(class_name)
        is_hazard = is_neg or any(w in cleaned for w in ("fire", "smoke", "fall", "danger", "hazard", "violation", "unauthorized"))
        if is_hazard:
            hazard_palette = [
                (0, 0, 255),      # Bright Alert Red (BGR)
                (0, 90, 255),     # Vivid Amber Orange (BGR)
                (255, 0, 255),    # Electric Magenta / Fuchsia (BGR)
                (180, 50, 255),   # Hot Crimson Pink (BGR)
                (0, 140, 255),    # Deep Tangerine (BGR)
                (220, 20, 180),   # Deep Purple Violet (BGR)
            ]
            core_h = int(hashlib.md5(core.encode('utf-8')).hexdigest(), 16)
            return hazard_palette[core_h % len(hazard_palette)]
        else:
            safe_palette = [
                (255, 215, 0),    # Bright Cyan Sky Blue (BGR)
                (0, 230, 80),     # Emerald Lime Green (BGR)
                (0, 230, 255),    # Golden Lemon Yellow (BGR)
                (180, 230, 50),   # Mint Teal (BGR)
                (255, 120, 180),  # Soft Lavender Violet (BGR)
                (50, 205, 50),    # Spring Green (BGR)
                (200, 100, 50),   # Slate Blue (BGR)
                (255, 140, 0),    # Deep Cobalt Blue (BGR)
            ]
            core_h = int(hashlib.md5(core.encode('utf-8')).hexdigest(), 16)
            return safe_palette[core_h % len(safe_palette)]

logger = logging.getLogger("VideoTester")
logger.setLevel(logging.INFO)

STORAGE_DIR = BASE_DIR / "test_videos"
RAW_DIR = STORAGE_DIR / "raw"
RESULTS_DIR = STORAGE_DIR / "results"

RAW_DIR.mkdir(parents=True, exist_ok=True)
RESULTS_DIR.mkdir(parents=True, exist_ok=True)


def resolve_model_path(model_name: str) -> str:
    """Resolve model file path across models directory or project root."""
    if not model_name or str(model_name).lower() in ("none", "raw", "preview", ""):
        return ""
    if os.path.isabs(model_name) and os.path.exists(model_name):
        return model_name
    basename = os.path.basename(model_name)
    candidates = [
        BASE_DIR / "models" / basename,
        BASE_DIR / "models" / model_name,
        BASE_DIR / "backend" / "models" / basename,
        BASE_DIR / "tests" / basename,
        BASE_DIR / basename,
        Path(model_name)
    ]
    for c in candidates:
        if c.exists():
            return str(c)
    return str(BASE_DIR / "models" / basename)


def resolve_raw_path(raw_filename: str) -> str:
    """Resolve raw uploaded video path across candidate storage directories or return RTSP URL directly."""
    if not raw_filename:
        return ""
    if raw_filename.startswith("rtsp://") or raw_filename.startswith("rtsps://") or raw_filename.startswith("http://") or raw_filename.startswith("https://"):
        return raw_filename
    if os.path.isabs(raw_filename) and os.path.exists(raw_filename):
        return raw_filename
    basename = os.path.basename(raw_filename)
    candidates = [
        RAW_DIR / basename,
        BASE_DIR / "test_videos" / "raw" / basename,
        BASE_DIR / "backend" / "test_videos" / "raw" / basename,
        Path(raw_filename)
    ]
    for c in candidates:
        if c.exists():
            return str(c)
    return str(RAW_DIR / basename)


# In-memory Job Store
JOBS = {}
JOBS_LOCK = threading.Lock()

# In-memory Live Streamers Store
LIVE_STREAMERS = {}
LIVE_STREAMERS_LOCK = threading.Lock()

try:
    from backend.detector.roi_utils import (
        normalize_roi_polygons,
        is_box_inside_rois,
        is_point_inside_rois,
        draw_rois_on_frame
    )
except ImportError:
    try:
        from roi_utils import (
            normalize_roi_polygons,
            is_box_inside_rois,
            is_point_inside_rois,
            draw_rois_on_frame
        )
    except ImportError:
        normalize_roi_polygons = lambda r: []
        is_box_inside_rois = lambda b, r, w, h: True
        is_point_inside_rois = lambda p, r, w, h: True
        draw_rois_on_frame = lambda f, r, l="ROI": f


class VideoLabObjectTracker:
    """
    Lightweight persistent object tracker for AI Video Lab.
    Maintains stable track IDs across frames for person, crew, truck, and TT.
    """
    def __init__(self, max_age_seconds=1.8, iou_thresh=0.20):
        self.tracks = {}
        self.next_id = 1
        self.max_age = max_age_seconds
        self.iou_thresh = iou_thresh

    def update(self, raw_detections, now_t):
        for tid in list(self.tracks.keys()):
            if (now_t - self.tracks[tid]['last_seen']) > self.max_age:
                del self.tracks[tid]

        results = []
        tracked_kw = ("person", "crew", "truck", "tank", "tt", "vehicle", "car", "bus", "reliance_tt", "tt_brand", "ttbrand")

        for det in raw_detections:
            xyxy, cls_name, conf_val = det[0], det[1], det[2]
            kp = det[3] if len(det) > 3 else None
            cls_lower = str(cls_name).lower()

            is_tracked = any(k in cls_lower for k in tracked_kw)
            if not is_tracked:
                results.append((xyxy, cls_name, conf_val, kp, None))
                continue

            bx1, by1, bx2, by2 = xyxy
            bcx, bcy = (bx1 + bx2) / 2.0, (by1 + by2) / 2.0
            best_tid, best_score = None, 0.0

            for tid, trk in self.tracks.items():
                t_cls = str(trk['cls']).lower()
                is_compat = (
                    (any(k in cls_lower for k in ("person", "crew")) and any(k in t_cls for k in ("person", "crew"))) or
                    (any(k in cls_lower for k in ("truck", "tank", "tt", "vehicle", "car", "bus")) and any(k in t_cls for k in ("truck", "tank", "tt", "vehicle", "car", "bus"))) or
                    (cls_lower == t_cls)
                )
                if not is_compat:
                    continue

                tx1, ty1, tx2, ty2 = trk['box']
                ix1, iy1 = max(bx1, tx1), max(by1, ty1)
                ix2, iy2 = min(bx2, tx2), min(by2, ty2)
                if ix2 > ix1 and iy2 > iy1:
                    inter = (ix2 - ix1) * (iy2 - iy1)
                    union = max(1.0, (bx2 - bx1) * (by2 - by1) + (tx2 - tx1) * (ty2 - ty1) - inter)
                    iou = inter / union
                    if iou > best_score:
                        best_score = iou
                        best_tid = tid
                else:
                    tcx, tcy = (tx1 + tx2) / 2.0, (ty1 + ty2) / 2.0
                    dist = math.hypot(bcx - tcx, bcy - tcy)
                    diag = max(1.0, math.hypot(bx2 - bx1, by2 - by1))
                    if dist < 0.70 * diag:
                        score = max(0.01, 1.0 - (dist / diag))
                        if score > best_score:
                            best_score = score
                            best_tid = tid

            if best_tid is not None and best_score >= self.iou_thresh:
                self.tracks[best_tid]['box'] = xyxy
                self.tracks[best_tid]['last_seen'] = now_t
                self.tracks[best_tid]['cls'] = cls_name
                results.append((xyxy, cls_name, conf_val, kp, best_tid))
            else:
                new_tid = self.next_id
                self.next_id += 1
                self.tracks[new_tid] = {
                    'box': xyxy,
                    'cls': cls_name,
                    'last_seen': now_t,
                    'first_seen': now_t
                }
                results.append((xyxy, cls_name, conf_val, kp, new_tid))

        return results


# =========================================================================
# [LIVE] INSTANT LIVE VIDEO DETECTION STREAMER (REAL-TIME MJPEG)
# =========================================================================
class LiveVideoTestStreamer:
    def __init__(self, session_id: str, raw_filename: str, model_name: str, conf=0.35, iou=0.45, imgsz=640, enabled_classes=None, frame_step=1, roi_polygon=None):
        self.session_id = session_id
        self.raw_filename = raw_filename
        self.raw_path = resolve_raw_path(raw_filename)
        self.model_name = model_name
        self.model_path = resolve_model_path(model_name)
        self.conf = float(conf)
        self.iou = float(iou)
        raw_sz = int(imgsz) if imgsz else 640
        self.imgsz = raw_sz
        self.enabled_classes = enabled_classes or []
        self.frame_step = max(1, int(frame_step))
        self.roi_polygon = roi_polygon or []
        self.companion_smoking_model = None
        self.companion_vehicle_model = None
        self.companion_person_model = None
        
        self.running = False
        self.paused = False
        self.loop = True
        self.reader_thread = None
        self.infer_thread = None
        
        self.current_frame = None
        self.current_jpeg_bytes = None
        self.latest_detections = []
        self._active_det_cache = []
        self.active_classes_count = {}
        self.fps = 25.0
        self.processed_fps = 0.0
        self.lock = threading.Lock()
        self.frame_lock = threading.Lock()
        self._latest_raw_frame = None
        self.model = None
        self._trk_cache = {}
        self.tracker = VideoLabObjectTracker()

    def start(self):
        if self.running:
            return
        self.running = True
        self.reader_thread = threading.Thread(target=self._run_reader_loop, daemon=True, name=f"LiveStreamer-Reader-{self.session_id}")
        self.reader_thread.start()
        self.infer_thread = threading.Thread(target=self._run_inference_loop, daemon=True, name=f"LiveStreamer-Infer-{self.session_id}")
        self.infer_thread.start()

    def stop(self):
        self.running = False

    def update_params(self, conf=None, iou=None, imgsz=None, enabled_classes=None, model_name=None, frame_step=None, roi_polygon=None):
        with self.lock:
            if conf is not None:
                self.conf = float(conf)
            if iou is not None:
                self.iou = float(iou)
            if imgsz is not None:
                raw_sz = int(imgsz)
                self.imgsz = raw_sz
            if enabled_classes is not None:
                self.enabled_classes = enabled_classes
            if frame_step is not None:
                self.frame_step = max(1, int(frame_step))
            if roi_polygon is not None:
                self.roi_polygon = roi_polygon
            if model_name is not None and model_name != self.model_name:
                self.model_name = model_name
                self.model_path = resolve_model_path(model_name)
                self.companion_smoking_model = None
                self.companion_vehicle_model = None
                self.companion_person_model = None
                self._active_det_cache = []
                try:
                    self.model = get_yolo_model(self.model_path)
                except Exception as e:
                    logger.error(f"Error loading new model {model_name}: {e}")

    def get_jpeg_frame(self):
        with self.lock:
            return self.current_jpeg_bytes

    def _run_reader_loop(self):
        cap = cv2.VideoCapture(self.raw_path)
        if not cap.isOpened():
            alt_path = resolve_raw_path(self.raw_filename)
            if alt_path != self.raw_path:
                self.raw_path = alt_path
                cap = cv2.VideoCapture(self.raw_path)

        if not cap.isOpened():
            logger.error(f"Cannot open video or RTSP stream for streaming: {self.raw_path}")
            self.running = False
            return

        is_rtsp = self.raw_path.startswith("rtsp://") or self.raw_path.startswith("rtsps://") or self.raw_path.startswith("http://") or self.raw_path.startswith("https://")
        if is_rtsp:
            self.loop = False

        native_fps = cap.get(cv2.CAP_PROP_FPS) or 25.0
        if native_fps <= 0 or math.isnan(native_fps) or native_fps > 120:
            native_fps = 25.0
        self.fps = native_fps
        target_fps = min(30.0, max(12.0, native_fps))
        target_frame_time = 1.0 / target_fps
        frame_idx = 0

        # Publish initial raw frame IMMEDIATELY so stream never starts on black screen
        ret, initial_frame = cap.read()
        if ret and initial_frame is not None:
            h_init, w_init = initial_frame.shape[:2]
            if w_init > 854:
                disp_init = cv2.resize(initial_frame, (854, int(h_init * 854 / float(w_init))), interpolation=cv2.INTER_LINEAR)
            else:
                disp_init = initial_frame
            ret_j, jpeg = cv2.imencode('.jpg', disp_init, [int(cv2.IMWRITE_JPEG_QUALITY), 75])
            if ret_j:
                with self.lock:
                    self.current_jpeg_bytes = jpeg.tobytes()
            with self.frame_lock:
                self._latest_raw_frame = disp_init.copy()

        logger.info(f"LiveStreamer: Started streaming {self.raw_filename} at native {native_fps:.1f} FPS (pace: {target_frame_time*1000:.1f}ms/frame)")

        while self.running:
            if self.paused:
                time.sleep(0.05)
                continue

            t_frame_start = time.time()

            ret, frame = cap.read()
            if not ret or frame is None:
                if self.loop and not is_rtsp:
                    cap.set(cv2.CAP_PROP_POS_FRAMES, 0)
                    ret, frame = cap.read()
                    if not ret or frame is None:
                        cap.release()
                        cap = cv2.VideoCapture(self.raw_path)
                        ret, frame = cap.read()
                    if not ret or frame is None:
                        time.sleep(0.05)
                        continue
                elif is_rtsp:
                    time.sleep(0.02)
                    continue
                else:
                    break

            h_orig, w_orig = frame.shape[:2]
            max_disp_w = 854
            if w_orig > max_disp_w:
                disp_w = max_disp_w
                disp_h = int(h_orig * (max_disp_w / float(w_orig)))
                disp_frame = cv2.resize(frame, (disp_w, disp_h), interpolation=cv2.INTER_LINEAR)
                scale_disp_x = disp_w / float(w_orig)
                scale_disp_y = disp_h / float(h_orig)
            else:
                disp_frame = frame
                scale_disp_x = 1.0
                scale_disp_y = 1.0

            frame_idx += 1

            # Feed downscaled display frame to async inference worker (saves huge memory copying)
            with self.frame_lock:
                self._latest_raw_frame = disp_frame.copy()

            # Retrieve active detections
            with self.lock:
                dets = list(self.latest_detections)

            h_d, w_d = disp_frame.shape[:2]
            now_draw_t = time.time()

            # Draw User-Drawn ROI Zone(s) (supports rectangles, polygons, and multiple ROIs)
            with self.lock:
                c_roi = list(self.roi_polygon) if self.roi_polygon else []
            if c_roi:
                try:
                    disp_frame = draw_rois_on_frame(disp_frame, c_roi)
                except Exception:
                    pass

            # Draw exact bounding boxes smoothly on this frame
            for det in dets:
                # Discard detections older than 1.2s to prevent lingering ghost boxes on empty road
                det_ts = det.get("ts", 0.0)
                if det_ts > 0 and (now_draw_t - det_ts) > 1.2:
                    continue
                box = det.get("box")
                if box is None:
                    continue
                x1, y1, x2, y2 = box
                x1 = max(0, min(w_d - 1, int(x1)))
                y1 = max(0, min(h_d - 1, int(y1)))
                x2 = max(0, min(w_d - 1, int(x2)))
                y2 = max(0, min(h_d - 1, int(y2)))
                if x2 <= x1 or y2 <= y1:
                    continue
                
                color = det.get("color", (0, 255, 128))
                label = det.get("label", "")
                conf = det.get("conf", 0.0)

                cv2.rectangle(disp_frame, (x1, y1), (x2, y2), color, 2)
                if str(label).startswith("PLATE:"):
                    tag_text = label
                else:
                    tag_text = f"{label} {conf:.2f}" if label else f"{conf:.2f}"
                (tw, th), _ = cv2.getTextSize(tag_text, cv2.FONT_HERSHEY_SIMPLEX, 0.50, 1)
                
                if y1 - th - 6 > 0:
                    bg_y1 = y1 - th - 6
                    bg_y2 = y1
                    text_y = y1 - 4
                else:
                    bg_y1 = y1
                    bg_y2 = min(h_d - 1, y1 + th + 6)
                    text_y = y1 + th + 2
                    
                bg_x2 = min(w_d - 1, x1 + tw + 6)
                cv2.rectangle(disp_frame, (x1, bg_y1), (bg_x2, bg_y2), (18, 20, 24), -1)
                cv2.rectangle(disp_frame, (x1, bg_y1), (bg_x2, bg_y2), color, 1)
                cv2.putText(disp_frame, tag_text, (x1 + 3, text_y), cv2.FONT_HERSHEY_SIMPLEX, 0.50, (255, 255, 255), 1, cv2.LINE_AA)

            # Crowd Analysis Overlay (Proximity lines, cluster hulls, and HUD banner)
            is_crowd_model = any(k in str(getattr(self, "model_name", "")).lower() for k in ("crowd", "yolo"))
            if is_crowd_model:
                person_pts = []
                for det in dets:
                    b = det.get("box")
                    if b is not None:
                        bx1, by1, bx2, by2 = b
                        cx = int(((bx1 + bx2) / 2) * scale_disp_x)
                        cy = int(by2 * scale_disp_y)
                        person_pts.append((cx, cy))
                if person_pts:
                    _draw_crowd_hud_and_clusters(disp_frame, person_pts, threshold=4, dist_px=180)

            # Encode to JPEG and publish to stream (ultra-fast <4ms at 854px display width)
            encode_param = [int(cv2.IMWRITE_JPEG_QUALITY), 75]
            ret_j, jpeg = cv2.imencode('.jpg', disp_frame, encode_param)
            if ret_j:
                with self.lock:
                    self.current_jpeg_bytes = jpeg.tobytes()

            # Real-time FPS Pacing: enforce smooth native playback speed
            t_frame_end = time.time()
            elapsed = t_frame_end - t_frame_start
            sleep_needed = target_frame_time - elapsed
            if sleep_needed > 0.002:
                time.sleep(sleep_needed)

        cap.release()
        self.running = False

    def _run_inference_loop(self):
        # Pre-warm model in background without delaying video playback
        if self.model_path and self.model is None:
            try:
                m = get_yolo_model(self.model_path)
                with self.lock:
                    self.model = m
                print(f"[VideoTester] LiveStreamer loaded model {self.model_name}", flush=True)
            except Exception as e:
                print(f"[VideoTester] LiveStreamer failed to load model {self.model_name}: {e}", flush=True)

        while self.running:
            with self.frame_lock:
                frame_to_infer = self._latest_raw_frame.copy() if self._latest_raw_frame is not None else None

            if frame_to_infer is None:
                time.sleep(0.04)
                continue

            with self.lock:
                c_conf = self.conf
                c_iou = self.iou
                c_imgsz = self.imgsz or 640
                c_model = self.model
                m_name = self.model_name
                c_classes = list(self.enabled_classes)

            # Lazy reload if model was not ready initially
            if c_model is None and self.model_path:
                try:
                    c_model = get_yolo_model(self.model_path)
                    if c_model is not None:
                        with self.lock:
                            self.model = c_model
                except Exception:
                    pass

            if c_model is None:
                time.sleep(0.1)
                continue

            dets = []
            t_infer_0 = time.time()
            try:
                h_orig, w_orig = frame_to_infer.shape[:2]
                infer_w = min(max(c_imgsz, 640), w_orig)
                infer_h = int(h_orig * (infer_w / float(w_orig)))
                infer_input = cv2.resize(frame_to_infer, (infer_w, infer_h), interpolation=cv2.INTER_LINEAR)
                scale_x = float(w_orig) / float(infer_w)
                scale_y = float(h_orig) / float(infer_h)

                eff_conf = c_conf

                predict_imgsz = ((int(c_imgsz) + 31) // 32) * 32
                predict_kwargs = {
                    "source": infer_input,
                    "imgsz": predict_imgsz,
                    "conf": eff_conf,
                    "iou": c_iou,
                    "verbose": False
                }

                if 'torch' in globals() and torch is not None and hasattr(torch, 'inference_mode'):
                    with torch.inference_mode():
                        results = c_model.predict(**predict_kwargs)
                else:
                    results = c_model.predict(**predict_kwargs)

                if results and len(results) > 0:
                    r = results[0]
                    if r.boxes is not None and len(r.boxes) > 0:
                        is_unauth_mod = "unauth" in str(m_name).lower() or getattr(c_model, 'task', '') == 'pose'
                        smoking_objects = []
                        if is_unauth_mod:
                            if self.companion_smoking_model is None:
                                for smk_path in ["models/smoking.pt", "backend/companion_models/smoking.pt"]:
                                    if os.path.exists(smk_path):
                                        try:
                                            self.companion_smoking_model = get_yolo_model(smk_path)
                                            break
                                        except Exception:
                                            pass
                            if self.companion_smoking_model is not None:
                                try:
                                    smk_res = self.companion_smoking_model.predict(
                                        source=infer_input,
                                        imgsz=predict_imgsz,
                                        conf=0.18,
                                        iou=0.45,
                                        verbose=False
                                    )
                                    if smk_res and len(smk_res) > 0 and smk_res[0].boxes is not None:
                                        for sb in smk_res[0].boxes:
                                            s_xy = sb.xyxy[0].cpu().numpy()
                                            if scale_x != 1.0 or scale_y != 1.0:
                                                s_box = [float(s_xy[0] * scale_x), float(s_xy[1] * scale_y), float(s_xy[2] * scale_x), float(s_xy[3] * scale_y)]
                                            else:
                                                s_box = [float(s_xy[0]), float(s_xy[1]), float(s_xy[2]), float(s_xy[3])]
                                            smoking_objects.append({
                                                'box': s_box,
                                                'cls': 'smoking',
                                                'conf': float(sb.conf[0].item())
                                            })
                                except Exception:
                                    pass

                        is_tt_mod = "tt_brand" in str(m_name).lower()
                        is_cg_mod = "crash_guard" in str(m_name).lower()
                        is_tt_crew_mod = any(k in str(m_name).lower() for k in ("crew_non_availability", "entering_below", "crew"))
                        is_wp_mod = "wrong_parking" in str(m_name).lower()
                        has_pos_tt = False
                        has_pos_cg = False
                        if is_tt_mod and r.boxes is not None and len(r.boxes) > 0:
                            for b in r.boxes:
                                raw_cid = int(b.cls[0].item())
                                raw_cname = r.names.get(raw_cid, str(raw_cid)) if hasattr(r, 'names') else str(raw_cid)
                                if raw_cname == "reliance_tt":
                                    raw_cname = "tt_branding"
                                raw_cf = float(b.conf[0].item())
                                if raw_cf >= eff_conf and any(k in raw_cname.lower() for k in ("tt_brand", "ttbrand", "reliance")):
                                    has_pos_tt = True
                                    break
                        elif is_cg_mod and r.boxes is not None and len(r.boxes) > 0:
                            for b in r.boxes:
                                raw_cid = int(b.cls[0].item())
                                raw_cname = r.names.get(raw_cid, str(raw_cid)) if hasattr(r, 'names') else str(raw_cid)
                                raw_cf = float(b.conf[0].item())
                                if raw_cf >= eff_conf and "crash" in raw_cname.lower():
                                    has_pos_cg = True
                                    break

                        kps_data = r.keypoints.data.cpu().numpy() if (hasattr(r, 'keypoints') and r.keypoints is not None) else []
                        raw_dets = []
                        for b_idx, b in enumerate(r.boxes):
                            cls_id = int(b.cls[0].item())
                            conf_val = float(b.conf[0].item())
                            cls_name = r.names.get(cls_id, str(cls_id)) if hasattr(r, 'names') else str(cls_id)
                            if cls_name == "reliance_tt":
                                cls_name = "tt_branding"
                            if (is_tt_crew_mod or is_wp_mod) and str(cls_name).lower() == "train":
                                cls_name = "truck"

                            if is_unauth_mod and cls_name.lower() == "person":
                                pass  # Allow person through to evaluate activity pose
                            elif (is_tt_crew_mod or is_wp_mod or is_cg_mod) and any(k in cls_name.lower() for k in ("person", "truck", "tank", "vehicle", "car", "bus", "train", "crash")):
                                pass  # Allow person and vehicles through for crew / parking / crash guard analysis
                            elif c_classes and not any(match_class(cls_name, fc) for fc in c_classes):
                                continue

                            xyxy = b.xyxy[0].cpu().numpy()
                            # Map coordinates back to original frame size
                            if scale_x != 1.0 or scale_y != 1.0:
                                xyxy = np.array([
                                     xyxy[0] * scale_x,
                                     xyxy[1] * scale_y,
                                     xyxy[2] * scale_x,
                                     xyxy[3] * scale_y
                                ])
                            kp = kps_data[b_idx] if b_idx < len(kps_data) else None
                            if kp is not None and (scale_x != 1.0 or scale_y != 1.0):
                                kp = kp.copy()
                                kp[:, 0] *= scale_x
                                kp[:, 1] *= scale_y
                            raw_dets.append((xyxy, cls_name, conf_val, kp))

                        if is_tt_mod and any("no-tt" in str(c).lower() or "branding" in str(c).lower() for c in c_classes):
                            if not has_pos_tt:
                                if self.companion_vehicle_model is None:
                                    for veh_path in ["models/yolov8n.pt", "models/vehicle_intrusion.pt"]:
                                        if os.path.exists(veh_path):
                                            try:
                                                self.companion_vehicle_model = get_yolo_model(veh_path)
                                                break
                                            except Exception:
                                                pass
                                veh_boxes = []
                                if self.companion_vehicle_model is not None:
                                    try:
                                        v_res = self.companion_vehicle_model.predict(
                                            source=infer_input,
                                            imgsz=640,
                                            conf=0.25,
                                            classes=[2, 5, 7],  # car, bus, truck
                                            verbose=False
                                        )
                                        if v_res and len(v_res) > 0 and v_res[0].boxes is not None:
                                            for vb in v_res[0].boxes:
                                                v_xy = vb.xyxy[0].cpu().numpy()
                                                if scale_x != 1.0 or scale_y != 1.0:
                                                    v_box = np.array([v_xy[0] * scale_x, v_xy[1] * scale_y, v_xy[2] * scale_x, v_xy[3] * scale_y])
                                                else:
                                                    v_box = v_xy
                                                veh_boxes.append((v_box, float(vb.conf[0].item())))
                                    except Exception:
                                        pass
                                if veh_boxes:
                                    veh_boxes.sort(key=lambda item: (item[0][2] - item[0][0]) * (item[0][3] - item[0][1]), reverse=True)
                                    v_best, v_conf = veh_boxes[0]
                                    raw_dets.append((v_best, "no-TT_Branding", max(0.85, v_conf), None))

                        elif is_cg_mod and any("no-crash" in str(c).lower() or "guard" in str(c).lower() for c in (c_classes or [])):
                            if not has_pos_cg:
                                if self.companion_vehicle_model is None:
                                    for veh_path in ["models/yolov8n.pt", "models/vehicle_intrusion.pt"]:
                                        if os.path.exists(veh_path):
                                            try:
                                                self.companion_vehicle_model = get_yolo_model(veh_path)
                                                break
                                            except Exception:
                                                pass
                                veh_boxes = []
                                if self.companion_vehicle_model is not None:
                                    try:
                                        v_res = self.companion_vehicle_model.predict(
                                            source=infer_input,
                                            imgsz=640,
                                            conf=0.25,
                                            classes=[2, 5, 7],  # car, bus, truck
                                            verbose=False
                                        )
                                        if v_res and len(v_res) > 0 and v_res[0].boxes is not None:
                                            for vb in v_res[0].boxes:
                                                v_xy = vb.xyxy[0].cpu().numpy()
                                                if scale_x != 1.0 or scale_y != 1.0:
                                                    v_box = np.array([v_xy[0] * scale_x, v_xy[1] * scale_y, v_xy[2] * scale_x, v_xy[3] * scale_y])
                                                else:
                                                    v_box = v_xy
                                                veh_boxes.append((v_box, float(vb.conf[0].item())))
                                    except Exception:
                                        pass
                                with self.lock:
                                    cur_roi = list(self.roi_polygon) if self.roi_polygon else []
                                if cur_roi:
                                    veh_boxes = [v for v in veh_boxes if is_box_inside_rois(v[0], cur_roi, w_orig, h_orig)]
                                if veh_boxes:
                                    veh_boxes.sort(key=lambda item: (item[0][2] - item[0][0]) * (item[0][3] - item[0][1]), reverse=True)
                                    v_best, v_conf = veh_boxes[0]
                                    raw_dets.append((v_best, "no-Crash_Guard", max(0.85, v_conf), None))

                        is_tt_crew_avail_mod = "crew_non_availability" in str(m_name).lower()
                        is_tt_crew_below_mod = "entering_below" in str(m_name).lower()

                        if is_tt_crew_avail_mod:
                            v_boxes = [d[0] for d in raw_dets if any(vk in str(d[1]).lower() for vk in ("truck", "tank", "bus", "car", "vehicle", "train"))]
                            p_boxes = [d[0] for d in raw_dets if "person" in str(d[1]).lower()]
                            has_no_crew_selected = any("no-tt" in str(c).lower() or "non" in str(c).lower() or "availability" in str(c).lower() for c in (c_classes or []))
                            
                            with self.lock:
                                cur_roi = list(self.roi_polygon) if self.roi_polygon else []
                            
                            if cur_roi:
                                bay_trucks = [v for v in v_boxes if is_box_inside_rois(v, cur_roi, w_orig, h_orig)]
                                p_in_roi = [p for p in p_boxes if is_box_inside_rois(p, cur_roi, w_orig, h_orig)]
                            else:
                                bay_trucks = v_boxes
                                p_in_roi = p_boxes

                            if bay_trucks and not p_in_roi and (has_no_crew_selected or not c_classes):
                                bay_trucks.sort(key=lambda b: (b[2] - b[0]) * (b[3] - b[1]), reverse=True)
                                v_main = bay_trucks[0]
                                raw_dets.append((v_main, "no-TT_Crew_Availability", 0.90, None))
                            elif bay_trucks and p_in_roi and any("available" in str(c).lower() for c in (c_classes or [])):
                                updated_dets = []
                                for d in raw_dets:
                                    if "person" in str(d[1]).lower() and (not cur_roi or is_box_inside_rois(d[0], cur_roi, w_orig, h_orig)):
                                        updated_dets.append((d[0], "tt_crew_available", d[2], d[3]))
                                    else:
                                        updated_dets.append(d)
                                raw_dets = updated_dets

                        elif is_tt_crew_below_mod:
                            # Both a Tank Truck and a Person must be physically present in the monitored bay ROI!
                            # The person must be physically located beneath or adjacent to the truck's undercarriage!
                            v_boxes = [d[0] for d in raw_dets if is_vehicle_class(d[1])]
                            with self.lock:
                                cur_roi = list(self.roi_polygon) if self.roi_polygon else []

                            if cur_roi:
                                bay_trucks = [v for v in v_boxes if is_box_inside_rois(v, cur_roi, w_orig, h_orig)]
                            else:
                                bay_trucks = v_boxes

                            updated_dets = []
                            for d in raw_dets:
                                d_cls = str(d[1]).lower()
                                if "person" in d_cls or "crew" in d_cls:
                                    p_xy = d[0]
                                    p_cx = (p_xy[0] + p_xy[2]) / 2.0
                                    p_cy = (p_xy[1] + p_xy[3]) / 2.0
                                    p_gx, p_gy = p_cx, p_xy[3]
                                    p_w = max(1.0, p_xy[2] - p_xy[0])
                                    p_h = max(1.0, p_xy[3] - p_xy[1])

                                    # 1. Bay ROI Check: Person center or base must be inside ROI
                                    if cur_roi:
                                        person_in_roi = is_point_inside_rois((p_cx, p_cy), cur_roi, w_orig, h_orig) or \
                                                        is_point_inside_rois((p_gx, p_gy), cur_roi, w_orig, h_orig)
                                        if not person_in_roi:
                                            updated_dets.append(d)
                                            continue

                                    in_below = False
                                    if len(bay_trucks) > 0:
                                        is_under_truck = False
                                        for v_xy in bay_trucks:
                                            v_w = max(1.0, v_xy[2] - v_xy[0])
                                            v_h = max(1.0, v_xy[3] - v_xy[1])

                                            in_x = (v_xy[0] - 15 <= p_cx <= v_xy[2] + 15)
                                            v_under_y = v_xy[1] + 0.55 * v_h
                                            in_y = (v_under_y <= p_cy <= v_xy[3] + 35)

                                            # Reject foreground walking in front of truck
                                            if p_xy[3] > v_xy[3] + 25:
                                                continue
                                            # Reject upright climbers or high head
                                            if p_xy[1] < v_xy[1] + 0.48 * v_h:
                                                continue
                                            # Reject upright walking/standing aspect ratio
                                            if (p_w / p_h < 0.62) and (p_h > 0.42 * v_h):
                                                continue

                                            is_crawling_or_crouching = (p_w / p_h >= 0.65) or (p_h < 0.45 * v_h) or (p_xy[1] >= v_xy[1] + 0.60 * v_h)

                                            if in_x and in_y and is_crawling_or_crouching:
                                                is_under_truck = True
                                                break

                                        in_below = is_under_truck
                                    if in_below:
                                        updated_dets.append((p_xy, "tt_crew_below_tt", 0.90, d[3]))
                                    else:
                                        updated_dets.append(d)
                                else:
                                    updated_dets.append(d)
                            raw_dets = updated_dets

                        # Determine if vehicle model
                        is_veh_model = "vehicle" in str(m_name).lower() or any(
                            any(vk in str(c).lower() for vk in ("car", "truck", "bike", "vehicle", "bus", "van"))
                            for c in (c_classes or [m_name])
                        )

                        is_smok_mod = "smok" in str(m_name).lower() or any("smok" in str(c).lower() for c in (c_classes or []))
                        detected_persons = [d[0] for d in raw_dets if "person" in str(d[1]).lower()]
                        if is_smok_mod and not detected_persons:
                            if self.companion_person_model is None:
                                for p_path in ["models/yolov8n.pt", "backend/companion_models/yolov8n.pt"]:
                                    if os.path.exists(p_path):
                                        try:
                                            self.companion_person_model = get_yolo_model(p_path)
                                            break
                                        except Exception:
                                            pass
                            if self.companion_person_model is not None:
                                try:
                                    p_res = self.companion_person_model.predict(
                                        source=infer_input, imgsz=640, classes=[0], conf=0.25, verbose=False
                                    )
                                    if p_res and len(p_res) > 0 and p_res[0].boxes is not None:
                                        for pb in p_res[0].boxes:
                                            pxy = pb.xyxy[0].cpu().numpy()
                                            if scale_x != 1.0 or scale_y != 1.0:
                                                p_box = np.array([pxy[0] * scale_x, pxy[1] * scale_y, pxy[2] * scale_x, pxy[3] * scale_y])
                                            else:
                                                p_box = pxy
                                            detected_persons.append(p_box)
                                except Exception:
                                    pass

                        all_person_boxes = [d[0].tolist() for d in raw_dets if 'person' in str(d[1]).lower()]
                        now_ts = time.time()

                        # Strict In-ROI Gating: If user has drawn ROI(s), discard detections outside all ROIs
                        # Note: For TT crew models, preserve vehicles & violation boxes so bay presence is never lost
                        with self.lock:
                            cur_roi = list(self.roi_polygon) if self.roi_polygon else []
                        if cur_roi:
                            is_tt_crew_active = any(k in str(m_name).lower() for k in ("crew_non_availability", "entering_below", "tt_crew"))
                            raw_dets = [
                                d for d in raw_dets
                                if is_box_inside_rois(d[0], cur_roi, w_orig, h_orig) or
                                   (is_tt_crew_active and (any(vk in str(d[1]).lower() for vk in ("truck", "tank", "bus", "car", "vehicle")) or is_negative_violation_class(d[1])))
                            ]

                        tracked_raw = self.tracker.update(raw_dets, now_ts)
                        # Sort violations first so priority alerts (no-TT_Crew_Availability, no-TT_Branding) precede generic objects
                        tracked_raw = sorted(
                            tracked_raw,
                            key=lambda t: (1 if is_negative_violation_class(t[1]) else 0, float(t[2])),
                            reverse=True
                        )
                        for (xyxy, cls_name, conf_val, kp, tid) in tracked_raw:
                            x1, y1, x2, y2 = xyxy
                            bw = max(0, x2 - x1)
                            bh = max(0, y2 - y1)

                            if "smok" in str(cls_name).lower():
                                # Smoking False Positive Filter: A cigarette/vape cannot smoke by itself without a person!
                                # Standalone detections on bare pipes, walls, or switchboards with no person nearby are rejected.
                                if not detected_persons:
                                    continue
                                smk_cx = (x1 + x2) / 2.0
                                smk_cy = (y1 + y2) / 2.0
                                near_person = False
                                for pbox in detected_persons:
                                    px1, py1, px2, py2 = pbox
                                    pbw = max(1.0, px2 - px1)
                                    pbh = max(1.0, py2 - py1)
                                    if (px1 - 0.35 * pbw <= smk_cx <= px2 + 0.35 * pbw) and (py1 - 0.20 * pbh <= smk_cy <= py1 + 0.75 * pbh):
                                        near_person = True
                                        break
                                if not near_person:
                                    continue

                            if is_veh_model:
                                # 1. Confidence floor for vehicle classes (reject road marking hallucinations)
                                min_veh_conf = max(0.35, c_conf) if not is_wp_mod else max(0.35, min(0.35, c_conf))
                                if conf_val < min_veh_conf:
                                    continue
                                # 2. Discard tiny noise
                                if bw < 35 or bh < 25 or (bw * bh) < 900:
                                    continue
                                # 3. Discard roadside reflector posts
                                if (bh / max(1.0, bw)) > 3.0 and (x1 < w_orig * 0.12 or x2 > w_orig * 0.88):
                                    continue
                                # 4. Discard horizontal road dashed line noise
                                if (bw / max(1.0, bh)) > 3.5 and bh < 40:
                                    continue
                                # 5. Discard sky / top foliage
                                if y2 < h_orig * 0.20 and (x1 > w_orig * 0.60 or x2 < w_orig * 0.25):
                                    continue
                                # 6. Discard architectural security booths, kiosks, and vertical structures misclassified as trucks
                                veh_aspect = bw / max(1.0, bh)
                                if veh_aspect < 0.85 and conf_val < 0.65:
                                    continue
                            elif bw < 12 or bh < 12:
                                continue

                            # 6. Suppress nested duplicate sub-boxes of the SAME object family
                            # NEVER suppress a person/crew or license plate inside a vehicle, or PPE inside a person!
                            area = bw * bh
                            is_sub_box = False
                            is_p_or_c = any(k in str(cls_name).lower() for k in ("person", "crew", "worker", "plate", "license", "helmet", "vest", "smoking"))
                            for (other_xyxy, other_cls, other_conf, other_kp) in raw_dets:
                                if np.array_equal(other_xyxy, xyxy):
                                    continue
                                o_cls_lower = str(other_cls).lower()
                                # Never suppress person/crew by vehicle or vice versa
                                if is_p_or_c and any(vk in o_cls_lower for vk in ("truck", "tank", "vehicle", "car", "bus", "train", "no-tt", "violation")):
                                    continue
                                if any(vk in str(cls_name).lower() for vk in ("truck", "tank", "vehicle", "car", "bus", "train")) and any(k in o_cls_lower for k in ("person", "crew")):
                                    continue
                                ox1, oy1, ox2, oy2 = other_xyxy
                                o_area = max(0, ox2 - ox1) * max(0, oy2 - oy1)
                                ix1, iy1 = max(x1, ox1), max(y1, oy1)
                                ix2, iy2 = min(x2, ox2), min(y2, oy2)
                                if ix1 < ix2 and iy1 < iy2:
                                    inter = (ix2 - ix1) * (iy2 - iy1)
                                    if o_area >= area and (inter / float(area)) > 0.65:
                                        same_family = (
                                            (any(k in str(cls_name).lower() for k in ("person", "crew")) and any(k in o_cls_lower for k in ("person", "crew"))) or
                                            (is_vehicle_class(cls_name) and is_vehicle_class(other_cls))
                                        )
                                        if same_family:
                                            is_sub_box = True
                                            break
                            if is_sub_box:
                                continue

                            label_name = cls_name
                            color = get_dynamic_class_color(cls_name)
                            conf_to_show = conf_val
                            det_cls = cls_name

                            if is_unauth_mod and cls_name.lower() == "person" and classify_pose_activity is not None:
                                box_cen = ((x1 + x2) / 2.0, (y1 + y2) / 2.0)
                                if tid not in self._trk_cache:
                                    self._trk_cache[tid] = {'first_seen': now_ts}
                                trk_obj = self._trk_cache[tid]
                                trk_obj['cen'] = box_cen
                                trk_obj['last_seen'] = now_ts
                                dwell = now_ts - trk_obj.get('first_seen', now_ts)

                                pose_activities = [c for c in (c_classes or []) if any(k in c.lower() for k in ("cook", "crowd", "lying", "smok", "fight", "sitting", "loiter"))]
                                if not pose_activities:
                                    pose_activities = ["cooking", "crowd", "smoking_gesture", "fighting_posture", "lying_down"]
                                all_person_records = [{'box': d[0].tolist(), 'keypoints': d[3]} for d in raw_dets if 'person' in str(d[1]).lower()]
                                cooking_objects = [
                                    {
                                        'box': d[0].tolist(),
                                        'cls': str(d[1]).lower(),
                                        'conf': float(d[2])
                                    }
                                    for d in raw_dets
                                    if any(k in str(d[1]).lower() for k in (
                                        "bottle", "cup", "fork", "knife", "spoon", "bowl",
                                        "microwave", "oven", "toaster", "sink", "refrigerator",
                                        "sandwich", "pizza", "pot", "pan", "stove", "fire", "smoke", "gas", "spatula"
                                    ))
                                ]
                                act, act_conf, act_sub = classify_pose_activity(
                                    trk_obj, kp, xyxy.tolist(), dwell, pose_activities,
                                    all_persons=all_person_records, frame_height=h_orig, now_t=now_ts, is_video_test=True,
                                    nearby_objects=cooking_objects, smoking_objects=smoking_objects
                                )
                                if act:
                                    det_cls = act
                                    label_name = f"VIOLATION: UNAUTHORIZED ACTIVITY ({act.replace('_', ' ').upper()}) #{tid}"
                                    color = (20, 20, 237)  # Alert Red
                                    conf_to_show = act_conf or conf_val
                                else:
                                    det_cls = "person"
                                    label_name = f"PERSON #{tid}"
                                    color = (0, 255, 100)  # Compliant Green
                                    conf_to_show = conf_val
                            elif tid is not None:
                                is_tracked_entity = any(k in str(cls_name).lower() for k in ("person", "crew", "truck", "tank", "tt", "vehicle", "car", "bus"))
                                if is_tracked_entity:
                                    plate_tag = f" #{tid}"
                                    detected_p_box = None
                                    detected_p_txt = None
                                    is_vehicle = is_vehicle_class(cls_name)
                                    if is_vehicle and GLOBAL_FAST_ANPR is not None:
                                        detected_p_box, detected_p_txt = GLOBAL_FAST_ANPR.detect_plate_box_and_text(
                                            frame_to_infer,
                                            vehicle_box=[int(x1), int(y1), int(x2), int(y2)],
                                            track_id=tid,
                                            vehicle_type=cls_name,
                                            now_t=now_ts
                                        )
                                        if detected_p_txt:
                                            plate_tag = f" [{detected_p_txt}]"

                                    if is_wp_mod:
                                        # Continuous Wrong Parking dwell timer measured strictly from ROI entry
                                        in_roi = is_box_inside_rois(xyxy, cur_roi, w_orig, h_orig) if cur_roi else True
                                        if tid not in self._trk_cache:
                                            self._trk_cache[tid] = {'first_seen': now_ts}
                                        trk_obj = self._trk_cache[tid]

                                        if in_roi:
                                            if 'roi_entry_t' not in trk_obj:
                                                trk_obj['roi_entry_t'] = now_ts
                                            parked_dur = max(0.0, now_ts - trk_obj['roi_entry_t'])
                                        else:
                                            trk_obj.pop('roi_entry_t', None)
                                            parked_dur = 0.0

                                        # ========================================================
                                        # WRONG PARKING TIME THRESHOLD IN LIVE STREAMER (ADJUST HERE):
                                        # Set to 2.0 seconds for quick testing; change to 30.0 for prod.
                                        # ========================================================
                                        parking_threshold = 2.0

                                        if in_roi and parked_dur >= parking_threshold:
                                            det_cls = "wrong_parking"
                                            label_name = f"VIOLATION: WRONG PARKING ({cls_name.upper()}{plate_tag}) {int(parked_dur)}s"
                                            color = (20, 20, 237)  # Alert Red
                                        elif in_roi:
                                            label_name = f"PARKED: {cls_name.upper()}{plate_tag} ({int(parked_dur)}s / {int(parking_threshold)}s)"
                                            color = (0, 220, 255)  # Active Cyan
                                        else:
                                            label_name = f"{cls_name.upper()}{plate_tag}"
                                    elif is_negative_violation_class(cls_name):
                                        label_name = f"VIOLATION: {cls_name}{plate_tag}"
                                    else:
                                        label_name = f"{cls_name.upper()}{plate_tag}"

                            # Strict User-Selected Class Filter:
                            # If user selected specific enabled classes in UI, verify that this detection matches at least one selected class!
                            if c_classes:
                                is_match = any(match_class(det_cls, fc) or match_class(cls_name, fc) for fc in c_classes)
                                if not is_match and is_wp_mod:
                                    if any(match_class("wrong_parking", fc) or match_class("license_plate", fc) or match_class(cls_name, fc) for fc in c_classes):
                                        is_match = True
                                    elif any(match_class("truck", fc) for fc in c_classes) and str(cls_name).lower() in ("truck", "bus"):
                                        is_match = True
                                if not is_match:
                                    continue

                            # Also: Suppress generic vehicle box if a higher-priority TT violation (e.g. no-TT_Crew_Availability or no-TT_Branding) covers this vehicle
                            if any(vk in str(det_cls).lower() for vk in ("truck", "tank", "vehicle")):
                                has_tt_violation_covering = any(
                                    any(vk in str(d.get("label", "")).lower() for vk in ("no-tt", "availability", "branding")) and
                                    (max(0, min(x2, d["box"][2]) - max(x1, d["box"][0])) * max(0, min(y2, d["box"][3]) - max(y1, d["box"][1])) / max(1.0, (x2 - x1) * (y2 - y1))) > 0.40
                                    for d in dets
                                )
                                if has_tt_violation_covering:
                                    continue

                            dets.append({
                                "box": xyxy,
                                "label": label_name,
                                "conf": conf_to_show,
                                "color": color,
                                "ts": now_ts
                            })

                            # Dedicated License Plate Bounding Box in LiveStreamer (rendered on physical bumper)
                            is_veh_for_plate = is_vehicle_class(cls_name)
                            if detected_p_box and is_veh_for_plate and (not c_classes or any(match_class("license_plate", fc) for fc in c_classes) or is_wp_mod):
                                p_lbl = str(detected_p_txt) if detected_p_txt else "LICENSE PLATE"
                                dets.append({
                                    "box": detected_p_box,
                                    "label": p_lbl,
                                    "conf": 0.92,
                                    "color": (237, 116, 10),  # Precision Blue matching Image 5
                                    "ts": now_ts,
                                    "is_plate": True,
                                    "vehicle_tid": tid
                                })
            except Exception as e:
                print(f"[VideoTester] Live infer error in streamer: {e}", flush=True)

            dt_infer = max(0.001, time.time() - t_infer_0)
            p_fps = 1.0 / dt_infer

            # Temporal hysteresis smoothing (0.45s window): prevents bounding box blinking
            now_t = time.time()
            updated_cache = []

            for d in dets:
                d_copy = dict(d)
                d_copy["ts"] = now_t
                updated_cache.append(d_copy)

            for prev in self._active_det_cache:
                if (now_t - prev.get("ts", 0)) <= 0.45:
                    already_covered = False
                    for cur in updated_cache:
                        # Spatial IoU and identity matching:
                        # If a current detection spatially covers prev or shares vehicle_tid,
                        # cur completely supersedes and replaces prev!
                        b1, b2 = cur["box"], prev["box"]
                        ix1, iy1 = max(b1[0], b2[0]), max(b1[1], b2[1])
                        ix2, iy2 = min(b1[2], b2[2]), min(b1[3], b2[3])
                        if ix1 < ix2 and iy1 < iy2:
                            inter = (ix2 - ix1) * (iy2 - iy1)
                            a1 = max(1.0, (b1[2] - b1[0]) * (b1[3] - b1[1]))
                            a2 = max(1.0, (b2[2] - b2[0]) * (b2[3] - b2[1]))
                            union = max(1.0, a1 + a2 - inter)
                            if (inter / union) >= 0.25 or (inter / min(a1, a2)) >= 0.45:
                                already_covered = True
                                break
                        if cur.get("vehicle_tid") and cur.get("vehicle_tid") == prev.get("vehicle_tid"):
                            already_covered = True
                            break
                    if not already_covered:
                        # Never carry forward stale license plate boxes
                        if prev.get("is_plate") or str(prev.get("label", "")).startswith("PLATE:"):
                            continue
                        updated_cache.append(prev)

            # Spatial Non-Maximum Suppression (NMS):
            # Eliminate duplicate or overlapping boxes on the same vehicle
            # Prioritize VIOLATION boxes over PARKED boxes!
            final_dets = []
            sorted_cache = sorted(
                updated_cache,
                key=lambda d: (
                    1 if "VIOLATION" in str(d.get("label", "")) else 0,
                    1 if d.get("is_plate") else 0,
                    float(d.get("conf", 0.0))
                ),
                reverse=True
            )
            for d in sorted_cache:
                d_box = d["box"]
                is_dup = False
                if not d.get("is_plate"):
                    for kept in final_dets:
                        if kept.get("is_plate"):
                            continue
                        kb = kept["box"]
                        ix1, iy1 = max(d_box[0], kb[0]), max(d_box[1], kb[1])
                        ix2, iy2 = min(d_box[2], kb[2]), min(d_box[3], kb[3])
                        if ix1 < ix2 and iy1 < iy2:
                            inter = (ix2 - ix1) * (iy2 - iy1)
                            da = max(1.0, (d_box[2] - d_box[0]) * (d_box[3] - d_box[1]))
                            ka = max(1.0, (kb[2] - kb[0]) * (kb[3] - kb[1]))
                            if (inter / min(da, ka)) > 0.45 or (inter / max(1.0, da + ka - inter)) > 0.30:
                                is_dup = True
                                break
                if not is_dup:
                    final_dets.append(d)

            self._active_det_cache = [d for d in final_dets if (now_t - d.get("ts", 0)) <= 0.45]

            with self.lock:
                self.latest_detections = list(self._active_det_cache)
                self.processed_fps = p_fps
            print(f"[VideoTester] Infer cycle took {dt_infer*1000:.0f}ms -> {len(dets)} active detections ({len(self._active_det_cache)} smoothed)", flush=True)

            # Brief pause so CPU remains responsive for smooth video rendering
            time.sleep(0.02)


def get_or_create_live_streamer(session_id: str, raw_filename: str, model_name: str, conf=0.35, iou=0.45, imgsz=640, enabled_classes=None, frame_step=1, roi_polygon=None):
    with LIVE_STREAMERS_LOCK:
        if session_id in LIVE_STREAMERS:
            s = LIVE_STREAMERS[session_id]
            if s.running and s.raw_filename == raw_filename:
                s.update_params(conf=conf, iou=iou, imgsz=imgsz, enabled_classes=enabled_classes, model_name=model_name, frame_step=frame_step, roi_polygon=roi_polygon)
                return s
            else:
                s.stop()
                del LIVE_STREAMERS[session_id]

        streamer = LiveVideoTestStreamer(
            session_id=session_id,
            raw_filename=raw_filename,
            model_name=model_name,
            conf=conf,
            iou=iou,
            imgsz=imgsz,
            enabled_classes=enabled_classes,
            frame_step=frame_step,
            roi_polygon=roi_polygon
        )
        LIVE_STREAMERS[session_id] = streamer
        streamer.start()
        return streamer


def get_live_streamer(session_id: str):
    with LIVE_STREAMERS_LOCK:
        return LIVE_STREAMERS.get(session_id)


def stop_live_streamer(session_id: str):
    with LIVE_STREAMERS_LOCK:
        s = LIVE_STREAMERS.pop(session_id, None)
        if s:
            s.stop()
            return True
    return False


# =========================================================================
# [BATCH] BATCH JOB ENGINE (EXPORT H.264 MP4)
# =========================================================================
class VideoTestJob:
    def __init__(self, job_id, raw_filename, model_name, conf=0.35, iou=0.45, imgsz=640, enabled_classes=None, frame_skip=1, roi_polygon=None):
        self.job_id = job_id
        self.raw_filename = raw_filename
        self.raw_path = resolve_raw_path(raw_filename)
        self.result_filename = f"{job_id}_annotated.mp4"
        self.result_path = str(RESULTS_DIR / self.result_filename)
        self.csv_filename = f"{job_id}_results.csv"
        self.csv_path = str(RESULTS_DIR / self.csv_filename)
        self.evidence_images = []
        
        self.model_name = model_name
        self.model_path = resolve_model_path(model_name)
        self.conf = float(conf)
        self.iou = float(iou)
        self.imgsz = int(imgsz)
        self.enabled_classes = enabled_classes or []
        self.frame_skip = max(1, int(frame_skip))
        self.roi_polygon = roi_polygon or []
        
        self.status = "queued"
        self.progress = 0.0
        self.current_frame = 0
        self.total_frames = 0
        self.video_fps = 25.0
        self.video_width = 0
        self.video_height = 0
        self.duration_sec = 0.0
        
        self.processing_fps = 0.0
        self.start_time = None
        self.elapsed_sec = 0.0
        self.eta_sec = 0.0
        
        self.detections_total = 0
        self.class_counts = {}
        self.error = None
        self.cancel_requested = False

    def to_dict(self):
        return {
            "job_id": self.job_id,
            "raw_filename": self.raw_filename,
            "result_filename": self.result_filename,
            "status": self.status,
            "progress": round(self.progress, 1),
            "current_frame": self.current_frame,
            "total_frames": self.total_frames,
            "video_fps": round(self.video_fps, 2),
            "video_resolution": f"{self.video_width}x{self.video_height}" if self.video_width else "N/A",
            "duration_sec": round(self.duration_sec, 2),
            "processing_fps": round(self.processing_fps, 1),
            "elapsed_sec": round(self.elapsed_sec, 1),
            "eta_sec": round(self.eta_sec, 1),
            "model_name": self.model_name,
            "conf": self.conf,
            "iou": self.iou,
            "imgsz": self.imgsz,
            "enabled_classes": self.enabled_classes,
            "frame_skip": self.frame_skip,
            "detections_total": self.detections_total,
            "class_counts": self.class_counts,
            "result_url": f"/api/video-test/result/{self.job_id}",
            "raw_url": f"/api/video-test/raw/{self.job_id}",
            "csv_filename": self.csv_filename,
            "csv_url": f"/api/video-test/csv/{self.job_id}",
            "evidence_images": self.evidence_images,
            "error": self.error,
        }


def get_job(job_id: str) -> VideoTestJob:
    with JOBS_LOCK:
        return JOBS.get(job_id)


def create_job(raw_filename: str, model_name: str, conf=0.35, iou=0.45, imgsz=640, enabled_classes=None, frame_skip=1, roi_polygon=None) -> VideoTestJob:
    job_id = str(uuid.uuid4())[:8]
    job = VideoTestJob(
        job_id=job_id,
        raw_filename=raw_filename,
        model_name=model_name,
        conf=conf,
        iou=iou,
        imgsz=imgsz,
        enabled_classes=enabled_classes,
        frame_skip=frame_skip,
        roi_polygon=roi_polygon
    )
    with JOBS_LOCK:
        JOBS[job_id] = job
    return job


def delete_job(job_id: str):
    with JOBS_LOCK:
        job = JOBS.pop(job_id, None)
    if job:
        job.cancel_requested = True
        try:
            if os.path.exists(job.raw_path):
                os.remove(job.raw_path)
            if os.path.exists(job.result_path):
                os.remove(job.result_path)
        except Exception as e:
            logger.error(f"Error removing files for job {job_id}: {e}")
        return True
    return False


def _draw_crowd_hud_and_clusters(frame, person_points, threshold=4, dist_px=180):
    """Draws cluster lines, convex hulls, and HUD banner for crowd detection."""
    h, w = frame.shape[:2]
    n = len(person_points)
    if n == 0:
        return

    # Compute clusters
    adj = {i: [] for i in range(n)}
    for i in range(n):
        for j in range(i + 1, n):
            gx1, gy1 = person_points[i]
            gx2, gy2 = person_points[j]
            if math.hypot(gx1 - gx2, gy1 - gy2) <= dist_px:
                adj[i].append(j)
                adj[j].append(i)

    visited = set()
    groups = []
    for i in range(n):
        if i not in visited:
            cluster = []
            queue = [i]
            visited.add(i)
            while queue:
                curr = queue.pop(0)
                cluster.append(curr)
                for neighbor in adj[curr]:
                    if neighbor not in visited:
                        visited.add(neighbor)
                        queue.append(neighbor)
            groups.append(cluster)

    has_crowd = (n >= threshold) or any(len(g) >= threshold for g in groups)

    # Connecting lines & hulls
    for g in groups:
        if len(g) > 1:
            line_color = (0, 0, 255) if has_crowd else (0, 200, 255)
            for i in range(len(g)):
                for j in range(i + 1, len(g)):
                    pt1 = person_points[g[i]]
                    pt2 = person_points[g[j]]
                    if math.hypot(pt1[0] - pt2[0], pt1[1] - pt2[1]) <= dist_px:
                        cv2.line(frame, pt1, pt2, line_color, 2, cv2.LINE_AA)

            if len(g) >= 3:
                pts = np.array([person_points[idx] for idx in g], dtype=np.int32)
                hull = cv2.convexHull(pts)
                cv2.polylines(frame, [hull], True, (0, 0, 255) if has_crowd else (0, 255, 255), 2, cv2.LINE_AA)

    # Top banner HUD
    banner_h = 50
    overlay = frame.copy()
    cv2.rectangle(overlay, (0, 0), (w, banner_h), (20, 20, 20), -1)
    cv2.addWeighted(overlay, 0.75, frame, 0.25, 0, frame)

    badge_color = (0, 0, 255) if has_crowd else (0, 200, 0)
    badge_text = f"CROWD ALERT: {n} DETECTED" if has_crowd else "STATUS: NORMAL"
    cv2.rectangle(frame, (12, 8), (280, 42), badge_color, -1)
    cv2.putText(frame, badge_text, (20, 31), cv2.FONT_HERSHEY_SIMPLEX, 0.55, (255, 255, 255), 2, cv2.LINE_AA)

    stats_text = f"Total: {n} | Clusters: {len(groups)} | Threshold: {threshold}"
    cv2.putText(frame, stats_text, (300, 31), cv2.FONT_HERSHEY_SIMPLEX, 0.52, (220, 220, 220), 1, cv2.LINE_AA)


def _draw_detection_overlay(frame, detections, job: VideoTestJob, frame_idx: int, total_frames: int):
    h, w = frame.shape[:2]
    
    # Draw ROI Zone(s) if present in job
    j_roi = getattr(job, "roi_polygon", None)
    if j_roi:
        try:
            frame = draw_rois_on_frame(frame, j_roi)
        except Exception:
            pass

    person_pts = []
    for det in detections:
        box = det["box"]
        label = det["label"]
        conf = det["conf"]
        color = det["color"]
        
        x1, y1, x2, y2 = map(int, box)
        x1 = max(0, min(w - 1, x1))
        y1 = max(0, min(h - 1, y1))
        x2 = max(0, min(w - 1, x2))
        y2 = max(0, min(h - 1, y2))
        
        cv2.rectangle(frame, (x1, y1), (x2, y2), color, 2)
        if str(label).startswith("PLATE:"):
            tag_text = label
        else:
            tag_text = f"{label} {conf:.2f}"
        (tw, th), baseline = cv2.getTextSize(tag_text, cv2.FONT_HERSHEY_SIMPLEX, 0.50, 1)
        
        if y1 - th - 6 > 0:
            bg_y1 = y1 - th - 6
            bg_y2 = y1
            text_y = y1 - 4
        else:
            bg_y1 = y1
            bg_y2 = min(h - 1, y1 + th + 6)
            text_y = y1 + th + 2
            
        bg_x2 = min(w - 1, x1 + tw + 6)
        cv2.rectangle(frame, (x1, bg_y1), (bg_x2, bg_y2), (18, 20, 24), -1)
        cv2.rectangle(frame, (x1, bg_y1), (bg_x2, bg_y2), color, 1)
        cv2.putText(frame, tag_text, (x1 + 3, text_y), cv2.FONT_HERSHEY_SIMPLEX, 0.50, (255, 255, 255), 1, cv2.LINE_AA)

        if "person" in str(label).lower() or "crowd" in str(label).lower():
            person_pts.append((int((x1 + x2) / 2), y2))
    
    is_crowd_model = any(k in str(getattr(job, "model_name", "")).lower() for k in ("crowd", "yolo"))
    if is_crowd_model and person_pts:
        _draw_crowd_hud_and_clusters(frame, person_pts, threshold=4, dist_px=180)

    return frame


def _process_video_worker(job: VideoTestJob):
    job.status = "processing"
    job.start_time = time.time()
    
    temp_avi_path = str(RESULTS_DIR / f"{job.job_id}_temp.avi")
    
    try:
        if not os.path.exists(job.model_path):
            raise FileNotFoundError(f"YOLO model not found at: {job.model_path}")
            
        model = get_yolo_model(job.model_path)
        cap = cv2.VideoCapture(job.raw_path)
        if not cap.isOpened():
            raise RuntimeError(f"Unable to open uploaded video file: {job.raw_path}")
            
        total_frames = int(cap.get(cv2.CAP_PROP_FRAME_COUNT))
        video_fps = cap.get(cv2.CAP_PROP_FPS) or 25.0
        width = int(cap.get(cv2.CAP_PROP_FRAME_WIDTH))
        height = int(cap.get(cv2.CAP_PROP_FRAME_HEIGHT))
        
        job.total_frames = total_frames
        job.video_fps = video_fps
        job.video_width = width
        job.video_height = height
        job.duration_sec = total_frames / video_fps if video_fps > 0 else 0.0
        
        filter_classes = [c.strip() for c in job.enabled_classes if c.strip()]
        is_unauth_mod = "unauth" in str(job.model_name).lower() or getattr(model, 'task', '') == 'pose'
        companion_smoking_model = None
        if is_unauth_mod:
            for smk_path in ["models/smoking.pt", "backend/companion_models/smoking.pt"]:
                if os.path.exists(smk_path):
                    try:
                        companion_smoking_model = get_yolo_model(smk_path)
                        break
                    except Exception:
                        pass

        is_tt_mod = "tt_brand" in str(job.model_name).lower()
        is_cg_mod = "crash_guard" in str(job.model_name).lower()
        is_tt_crew_mod = any(k in str(job.model_name).lower() for k in ("crew_non_availability", "entering_below", "crew"))
        is_wp_mod = "wrong_parking" in str(job.model_name).lower()
        companion_vehicle_model = None
        if is_tt_mod or is_cg_mod:
            for veh_path in ["models/yolov8n.pt", "models/vehicle_intrusion.pt"]:
                if os.path.exists(veh_path):
                    try:
                        companion_vehicle_model = get_yolo_model(veh_path)
                        break
                    except Exception:
                        pass

        target_class_ids = []
        if hasattr(model, 'names') and isinstance(model.names, dict):
            if filter_classes and not is_unauth_mod and not is_tt_mod and not is_tt_crew_mod and not is_wp_mod and not is_cg_mod:
                for cid, cname in model.names.items():
                    for fc in filter_classes:
                        if match_class(cname, fc):
                            target_class_ids.append(cid)
                            break
        
        fourcc = cv2.VideoWriter_fourcc(*'MJPG')
        out_writer = cv2.VideoWriter(temp_avi_path, fourcc, video_fps, (width, height))
        if not out_writer.isOpened():
            fourcc = cv2.VideoWriter_fourcc(*'XVID')
            out_writer = cv2.VideoWriter(temp_avi_path, fourcc, video_fps, (width, height))
            
        frame_idx = 0
        last_detections = []
        job_trk_cache = {}
        job_tracker = VideoLabObjectTracker()
        last_evidence_times = {}

        # Initialize CSV result logging for video testing pipeline (Section 6)
        csv_file = open(job.csv_path, "w", newline="", encoding="utf-8")
        csv_writer = csv.writer(csv_file)
        csv_writer.writerow([
            "frame_idx", "timestamp_sec", "track_id", "class_name", "confidence",
            "x1", "y1", "x2", "y2", "is_violation", "activity_detail"
        ])
        
        while cap.isOpened():
            if job.cancel_requested:
                job.status = "cancelled"
                break
                
            ret, frame = cap.read()
            if not ret or frame is None:
                break
                
            frame_idx += 1
            run_detection = ((frame_idx - 1) % job.frame_skip == 0)
            current_frame_detections = []
            current_time_sec = frame_idx / video_fps if video_fps > 0 else 0.0
            
            if run_detection:
                eff_conf = job.conf
                predict_imgsz = ((int(job.imgsz or 640) + 31) // 32) * 32
                predict_kwargs = {
                    "source": frame,
                    "imgsz": predict_imgsz,
                    "conf": eff_conf,
                    "iou": job.iou,
                    "verbose": False,
                }
                if target_class_ids and not is_unauth_mod:
                    predict_kwargs["classes"] = target_class_ids
                    
                results = model.predict(**predict_kwargs)
                smoking_objects = []
                if is_unauth_mod and companion_smoking_model is not None:
                    try:
                        smk_res = companion_smoking_model.predict(
                            source=frame,
                            imgsz=predict_imgsz,
                            conf=0.18,
                            iou=0.45,
                            verbose=False
                        )
                        if smk_res and len(smk_res) > 0 and smk_res[0].boxes is not None:
                            for sb in smk_res[0].boxes:
                                smoking_objects.append({
                                    'box': sb.xyxy[0].cpu().numpy().tolist(),
                                    'cls': 'smoking',
                                    'conf': float(sb.conf[0].item())
                                })
                    except Exception:
                        pass
                
                if results and len(results) > 0:
                    r = results[0]
                    if r.boxes is not None and len(r.boxes) > 0:
                        has_pos_tt = False
                        has_pos_cg = False
                        if is_tt_mod:
                            for b in r.boxes:
                                raw_cid = int(b.cls[0].item())
                                raw_cname = model.names.get(raw_cid, str(raw_cid)) if hasattr(model, 'names') else str(raw_cid)
                                if raw_cname == "reliance_tt":
                                    raw_cname = "tt_branding"
                                raw_cf = float(b.conf[0].item())
                                if raw_cf >= eff_conf and any(k in raw_cname.lower() for k in ("tt_brand", "ttbrand", "reliance")):
                                    has_pos_tt = True
                                    break
                        elif is_cg_mod:
                            for b in r.boxes:
                                raw_cid = int(b.cls[0].item())
                                raw_cname = model.names.get(raw_cid, str(raw_cid)) if hasattr(model, 'names') else str(raw_cid)
                                raw_cf = float(b.conf[0].item())
                                if raw_cf >= eff_conf and "crash" in raw_cname.lower():
                                    has_pos_cg = True
                                    break

                        kps_data = r.keypoints.data.cpu().numpy() if (hasattr(r, 'keypoints') and r.keypoints is not None) else []
                        raw_boxes = []
                        for b_idx, b in enumerate(r.boxes):
                            cls_id = int(b.cls[0].item())
                            conf_val = float(b.conf[0].item())
                            cls_name = model.names.get(cls_id, str(cls_id)) if hasattr(model, 'names') else str(cls_id)
                            if cls_name == "reliance_tt":
                                cls_name = "tt_branding"
                            if (is_tt_crew_mod or is_wp_mod) and str(cls_name).lower() == "train":
                                cls_name = "truck"
                            
                            if is_unauth_mod and cls_name.lower() == "person":
                                pass
                            elif (is_tt_crew_mod or is_wp_mod or is_cg_mod) and any(k in cls_name.lower() for k in ("person", "truck", "tank", "vehicle", "car", "bus", "train", "crash")):
                                pass
                            elif filter_classes:
                                matched = any(match_class(cls_name, fc) for fc in filter_classes)
                                if not matched:
                                    continue
                                    
                            xyxy = b.xyxy[0].cpu().numpy()
                            kp = kps_data[b_idx] if b_idx < len(kps_data) else None
                            raw_boxes.append((xyxy, cls_name, conf_val, kp))

                        if is_tt_mod and any("no-tt" in str(c).lower() or "branding" in str(c).lower() for c in filter_classes):
                            if not has_pos_tt:
                                veh_boxes = []
                                if companion_vehicle_model is not None:
                                    try:
                                        v_res = companion_vehicle_model.predict(
                                            source=frame,
                                            imgsz=640,
                                            conf=0.25,
                                            classes=[2, 5, 7],
                                            verbose=False
                                        )
                                        if v_res and len(v_res) > 0 and v_res[0].boxes is not None:
                                            for vb in v_res[0].boxes:
                                                veh_boxes.append((vb.xyxy[0].cpu().numpy(), float(vb.conf[0].item())))
                                    except Exception:
                                        pass
                                if veh_boxes:
                                    veh_boxes.sort(key=lambda item: (item[0][2] - item[0][0]) * (item[0][3] - item[0][1]), reverse=True)
                                    v_best, v_conf = veh_boxes[0]
                                    raw_boxes.append((v_best, "no-TT_Branding", max(0.85, v_conf), None))

                        elif is_cg_mod and any("no-crash" in str(c).lower() or "guard" in str(c).lower() for c in (filter_classes or [])):
                            if not has_pos_cg:
                                veh_boxes = []
                                if companion_vehicle_model is not None:
                                    try:
                                        v_res = companion_vehicle_model.predict(
                                            source=frame,
                                            imgsz=640,
                                            conf=0.25,
                                            classes=[2, 5, 7],
                                            verbose=False
                                        )
                                        if v_res and len(v_res) > 0 and v_res[0].boxes is not None:
                                            for vb in v_res[0].boxes:
                                                veh_boxes.append((vb.xyxy[0].cpu().numpy(), float(vb.conf[0].item())))
                                    except Exception:
                                        pass
                                j_roi = getattr(job, "roi_polygon", None)
                                if j_roi:
                                    veh_boxes = [v for v in veh_boxes if is_box_inside_rois(v[0], j_roi, width, height)]
                                if veh_boxes:
                                    veh_boxes.sort(key=lambda item: (item[0][2] - item[0][0]) * (item[0][3] - item[0][1]), reverse=True)
                                    v_best, v_conf = veh_boxes[0]
                                    raw_boxes.append((v_best, "no-Crash_Guard", max(0.85, v_conf), None))

                        is_tt_crew_avail_mod = "crew_non_availability" in str(job.model_name).lower()
                        is_tt_crew_below_mod = "entering_below" in str(job.model_name).lower()

                        if is_tt_crew_avail_mod:
                            v_boxes = [d[0] for d in raw_boxes if any(vk in str(d[1]).lower() for vk in ("truck", "tank", "bus", "car", "vehicle", "train"))]
                            p_boxes = [d[0] for d in raw_boxes if "person" in str(d[1]).lower()]
                            has_no_crew_selected = any("no-tt" in str(c).lower() or "non" in str(c).lower() or "availability" in str(c).lower() for c in (filter_classes or []))
                            
                            j_roi = getattr(job, "roi_polygon", None)
                            if j_roi:
                                bay_trucks = [v for v in v_boxes if is_box_inside_rois(v, j_roi, width, height)]
                                p_in_roi = [p for p in p_boxes if is_box_inside_rois(p, j_roi, width, height)]
                            else:
                                bay_trucks = v_boxes
                                p_in_roi = p_boxes

                            if bay_trucks and not p_in_roi and (has_no_crew_selected or not filter_classes):
                                bay_trucks.sort(key=lambda b: (b[2] - b[0]) * (b[3] - b[1]), reverse=True)
                                v_main = bay_trucks[0]
                                raw_boxes.append((v_main, "no-TT_Crew_Availability", 0.90, None))
                            elif bay_trucks and p_in_roi and any("available" in str(c).lower() for c in (filter_classes or [])):
                                updated_boxes = []
                                for d in raw_boxes:
                                    if "person" in str(d[1]).lower() and (not j_roi or is_box_inside_rois(d[0], j_roi, width, height)):
                                        updated_boxes.append((d[0], "tt_crew_available", d[2], d[3]))
                                    else:
                                        updated_boxes.append(d)
                                raw_boxes = updated_boxes

                        elif is_tt_crew_below_mod:
                            # Both a Tank Truck and a Person must be physically present in the monitored bay ROI!
                            # The person must be physically located beneath or adjacent to the truck's undercarriage!
                            v_boxes = [d[0] for d in raw_boxes if is_vehicle_class(d[1])]
                            j_roi = getattr(job, "roi_polygon", None)

                            if j_roi:
                                bay_trucks = [v for v in v_boxes if is_box_inside_rois(v, j_roi, width, height)]
                            else:
                                bay_trucks = v_boxes

                            updated_boxes = []
                            for d in raw_boxes:
                                d_cls = str(d[1]).lower()
                                if "person" in d_cls or "crew" in d_cls:
                                    p_xy = d[0]
                                    p_cx = (p_xy[0] + p_xy[2]) / 2.0
                                    p_cy = (p_xy[1] + p_xy[3]) / 2.0
                                    p_gx, p_gy = p_cx, p_xy[3]
                                    p_w = max(1.0, p_xy[2] - p_xy[0])
                                    p_h = max(1.0, p_xy[3] - p_xy[1])

                                    if j_roi:
                                        person_in_roi = is_point_inside_rois((p_cx, p_cy), j_roi, width, height) or \
                                                        is_point_inside_rois((p_gx, p_gy), j_roi, width, height)
                                        if not person_in_roi:
                                            updated_boxes.append(d)
                                            continue

                                    in_below = False
                                    if len(bay_trucks) > 0:
                                        is_under_truck = False
                                        for v_xy in bay_trucks:
                                            v_w = max(1.0, v_xy[2] - v_xy[0])
                                            v_h = max(1.0, v_xy[3] - v_xy[1])

                                            in_x = (v_xy[0] - 15 <= p_cx <= v_xy[2] + 15)
                                            v_under_y = v_xy[1] + 0.55 * v_h
                                            in_y = (v_under_y <= p_cy <= v_xy[3] + 35)

                                            # Reject foreground walking in front of truck
                                            if p_xy[3] > v_xy[3] + 25:
                                                continue
                                            # Reject upright climbers or high head
                                            if p_xy[1] < v_xy[1] + 0.48 * v_h:
                                                continue
                                            # Reject upright walking/standing aspect ratio
                                            if (p_w / p_h < 0.62) and (p_h > 0.42 * v_h):
                                                continue

                                            is_crawling_or_crouching = (p_w / p_h >= 0.65) or (p_h < 0.45 * v_h) or (p_xy[1] >= v_xy[1] + 0.60 * v_h)

                                            if in_x and in_y and is_crawling_or_crouching:
                                                is_under_truck = True
                                                break

                                        in_below = is_under_truck
                                    if in_below:
                                        updated_boxes.append((p_xy, "tt_crew_below_tt", 0.90, d[3]))
                                    else:
                                        updated_boxes.append(d)
                                else:
                                    updated_boxes.append(d)
                            raw_boxes = updated_boxes

                        all_person_records = [
                            {
                                'id': d_i + 1,
                                'box': d[0].tolist(),
                                'keypoints': d[3],
                                'wrist_vel': job_trk_cache.get(d_i + 1, {}).get('wrist_max_v', 0.0)
                            }
                            for d_i, d in enumerate(raw_boxes)
                            if 'person' in str(d[1]).lower()
                        ]

                        cooking_objects = [
                            {
                                'box': d[0].tolist(),
                                'cls': str(d[1]).lower(),
                                'conf': float(d[2])
                            }
                            for d in raw_boxes
                            if any(k in str(d[1]).lower() for k in (
                                "bottle", "cup", "fork", "knife", "spoon", "bowl",
                                "microwave", "oven", "toaster", "sink", "refrigerator",
                                "sandwich", "pizza", "pot", "pan", "stove", "fire", "smoke", "gas", "spatula"
                            ))
                        ]

                        # Strict In-ROI Gating: If user has drawn ROI(s), discard any detection outside all ROIs
                        j_roi = getattr(job, "roi_polygon", None)
                        if j_roi:
                            raw_boxes = [d for d in raw_boxes if is_box_inside_rois(d[0], j_roi, width, height)]

                        tracked_boxes = job_tracker.update(raw_boxes, current_time_sec)
                        for b_i, (xyxy, cls_name, conf_val, kp, tid) in enumerate(tracked_boxes):
                            x1, y1, x2, y2 = xyxy
                            bw = max(0, x2 - x1)
                            bh = max(0, y2 - y1)

                            if is_vehicle_class(cls_name):
                                veh_aspect = bw / max(1.0, bh)
                                if veh_aspect < 0.85 and conf_val < 0.65:
                                    continue
                                if conf_val < 0.35:
                                    continue

                            if "smok" in str(cls_name).lower():
                                # Smoking False Positive Filter: A cigarette/vape cannot smoke by itself without a person!
                                if not all_person_records:
                                    continue
                                smk_cx = (x1 + x2) / 2.0
                                smk_cy = (y1 + y2) / 2.0
                                near_person = False
                                for prec in all_person_records:
                                    px1, py1, px2, py2 = prec['box']
                                    pbw = max(1.0, px2 - px1)
                                    pbh = max(1.0, py2 - py1)
                                    if (px1 - 0.35 * pbw <= smk_cx <= px2 + 0.35 * pbw) and (py1 - 0.20 * pbh <= smk_cy <= py1 + 0.75 * pbh):
                                        near_person = True
                                        break
                                if not near_person:
                                    continue
                            label_name = cls_name
                            color = get_dynamic_class_color(cls_name)
                            conf_to_show = conf_val
                            act = None
                            act_sub = ""

                            if is_unauth_mod and cls_name.lower() == "person" and classify_pose_activity is not None:
                                box_cen = ((x1 + x2) / 2.0, (y1 + y2) / 2.0)
                                if tid not in job_trk_cache:
                                    job_trk_cache[tid] = {'first_seen': current_time_sec}
                                trk_obj = job_trk_cache[tid]
                                trk_obj['cen'] = box_cen
                                trk_obj['last_seen'] = current_time_sec
                                dwell = current_time_sec - trk_obj.get('first_seen', current_time_sec)

                                pose_activities = [c for c in (filter_classes or []) if any(k in c.lower() for k in ("cook", "crowd", "lying", "smok", "fight", "sitting", "loiter"))]
                                if not pose_activities:
                                    pose_activities = ["cooking", "crowd", "smoking_gesture", "fighting_posture", "lying_down"]
                                
                                act, act_conf, act_sub = classify_pose_activity(
                                    trk_obj, kp, xyxy.tolist(), dwell, pose_activities,
                                    all_persons=all_person_records, frame_height=height,
                                    now_t=current_time_sec, is_video_test=True, nearby_objects=cooking_objects,
                                    smoking_objects=smoking_objects
                                )
                                if act:
                                    label_name = f"VIOLATION: UNAUTHORIZED ACTIVITY ({act.replace('_', ' ').upper()}) #{tid}"
                                    color = (20, 20, 237)
                                    conf_to_show = act_conf or conf_val
                                    job.class_counts[act] = job.class_counts.get(act, 0) + 1
                                    
                                    # Save Event Evidence Image snapshot with cooldown (Section 6)
                                    snap_key = f"{tid}_{act}"
                                    last_snap = last_evidence_times.get(snap_key, -999.0)
                                    if (current_time_sec - last_snap) >= 2.0 and len(job.evidence_images) < 30:
                                        evidence_fname = f"{job.job_id}_evidence_{act}_f{frame_idx}.jpg"
                                        evidence_path = str(RESULTS_DIR / evidence_fname)
                                        try:
                                            cv2.imwrite(evidence_path, frame)
                                            job.evidence_images.append({
                                                "filename": evidence_fname,
                                                "url": f"/api/video-test/evidence/{evidence_fname}",
                                                "frame": frame_idx,
                                                "time_sec": round(current_time_sec, 2),
                                                "activity": act,
                                                "conf": round(conf_to_show, 2)
                                            })
                                            last_evidence_times[snap_key] = current_time_sec
                                        except Exception:
                                            pass
                                else:
                                    det_cls = "person"
                                    label_name = f"PERSON #{tid}"
                                    color = (0, 255, 100)
                                    conf_to_show = conf_val
                            elif tid is not None:
                                det_cls = cls_name
                                is_tracked_entity = any(k in str(cls_name).lower() for k in ("person", "crew", "truck", "tank", "tt", "vehicle", "car", "bus"))
                                if is_tracked_entity:
                                    plate_tag = f" #{tid}"
                                    detected_p_box = None
                                    detected_p_txt = None
                                    is_vehicle = is_vehicle_class(cls_name)
                                    if is_vehicle and GLOBAL_FAST_ANPR is not None:
                                        detected_p_box, detected_p_txt = GLOBAL_FAST_ANPR.detect_plate_box_and_text(
                                            frame,
                                            vehicle_box=[int(x1), int(y1), int(x2), int(y2)],
                                            track_id=tid,
                                            vehicle_type=cls_name,
                                            now_t=current_time_sec
                                        )
                                        if detected_p_txt:
                                            plate_tag = f" [{detected_p_txt}]"

                                    if is_wp_mod:
                                        # Continuous Wrong Parking dwell timer measured from ROI entry
                                        in_roi = is_box_inside_rois(xyxy, j_roi, width, height) if j_roi else True
                                        if tid not in job_trk_cache:
                                            job_trk_cache[tid] = {'first_seen': current_time_sec}
                                        trk_obj = job_trk_cache[tid]

                                        if in_roi:
                                            if 'roi_entry_t' not in trk_obj:
                                                trk_obj['roi_entry_t'] = current_time_sec
                                            parked_dur = max(0.0, current_time_sec - trk_obj['roi_entry_t'])
                                        else:
                                            trk_obj.pop('roi_entry_t', None)
                                            parked_dur = 0.0

                                        # ========================================================
                                        # WRONG PARKING TIME THRESHOLD IN BATCH TESTER (ADJUST HERE):
                                        # Set to 2.0 seconds for quick testing; change to 30.0 for prod.
                                        # ========================================================
                                        parking_threshold = 2.0

                                        if in_roi and parked_dur >= parking_threshold:
                                            det_cls = "wrong_parking"
                                            label_name = f"VIOLATION: WRONG PARKING ({cls_name.upper()}{plate_tag}) {int(parked_dur)}s"
                                            color = (20, 20, 237)  # Alert Red
                                        elif in_roi:
                                            label_name = f"PARKED: {cls_name.upper()}{plate_tag} ({int(parked_dur)}s / {int(parking_threshold)}s)"
                                            color = (0, 220, 255)  # Active Cyan
                                        else:
                                            label_name = f"{cls_name.upper()}{plate_tag}"
                                    elif is_negative_violation_class(cls_name):
                                        label_name = f"VIOLATION: {cls_name}{plate_tag}"
                                    else:
                                        label_name = f"{cls_name.upper()}{plate_tag}"
                            else:
                                det_cls = cls_name

                            # Strict User-Selected Class Filter for Batch Job:
                            if filter_classes:
                                is_match = any(match_class(det_cls, fc) or match_class(cls_name, fc) for fc in filter_classes)
                                if not is_match and is_wp_mod:
                                    if any(match_class("wrong_parking", fc) or match_class("license_plate", fc) or match_class(cls_name, fc) for fc in filter_classes):
                                        is_match = True
                                    elif any(match_class("truck", fc) for fc in filter_classes) and str(cls_name).lower() in ("truck", "bus"):
                                        is_match = True
                                if not is_match:
                                    continue

                            job.class_counts[det_cls] = job.class_counts.get(det_cls, 0) + 1
                            matched_tid = tid or (b_i + 1)

                            # Write detection record to CSV results file
                            csv_writer.writerow([
                                frame_idx, round(current_time_sec, 3), matched_tid or 0, label_name,
                                round(conf_to_show, 3), int(x1), int(y1), int(x2), int(y2),
                                bool(act), act_sub or ""
                            ])

                            current_frame_detections.append({
                                "box": xyxy,
                                "label": label_name,
                                "conf": conf_to_show,
                                "color": color
                            })
                            job.detections_total += 1

                            # Dedicated License Plate Bounding Box on Vehicle Bumper
                            is_veh_for_plate = is_vehicle_class(cls_name)
                            if detected_p_box and is_veh_for_plate and (not filter_classes or any(match_class("license_plate", fc) for fc in filter_classes) or is_wp_mod):
                                p_lbl = str(detected_p_txt) if detected_p_txt else "LICENSE PLATE"
                                current_frame_detections.append({
                                    "box": detected_p_box,
                                    "label": p_lbl,
                                    "conf": 0.92,
                                    "color": (237, 116, 10) # Precision Blue matching Image 5
                                })
                                job.class_counts["license_plate"] = job.class_counts.get("license_plate", 0) + 1
                                csv_writer.writerow([
                                    frame_idx, round(current_time_sec, 3), matched_tid or 0, p_lbl,
                                    0.92, int(detected_p_box[0]), int(detected_p_box[1]), int(detected_p_box[2]), int(detected_p_box[3]),
                                    False, "license_plate"
                                ])
                                job.detections_total += 1
                            
                # Deduplicate overlapping vehicle boxes: violation boxes supersede parked boxes
                dedup_frame_dets = []
                sorted_f_dets = sorted(
                    current_frame_detections,
                    key=lambda d: (
                        1 if "VIOLATION" in str(d.get("label", "")) else 0,
                        1 if "PLATE" in str(d.get("label", "")) else 0,
                        float(d.get("conf", 0.0))
                    ),
                    reverse=True
                )
                for d in sorted_f_dets:
                    d_box = d["box"]
                    is_dup = False
                    is_p = "PLATE" in str(d.get("label", ""))
                    if not is_p:
                        for kept in dedup_frame_dets:
                            if "PLATE" in str(kept.get("label", "")):
                                continue
                            kb = kept["box"]
                            ix1, iy1 = max(d_box[0], kb[0]), max(d_box[1], kb[1])
                            ix2, iy2 = min(d_box[2], kb[2]), min(d_box[3], kb[3])
                            if ix1 < ix2 and iy1 < iy2:
                                inter = (ix2 - ix1) * (iy2 - iy1)
                                da = max(1.0, (d_box[2] - d_box[0]) * (d_box[3] - d_box[1]))
                                ka = max(1.0, (kb[2] - kb[0]) * (kb[3] - kb[1]))
                                if (inter / min(da, ka)) > 0.45 or (inter / max(1.0, da + ka - inter)) > 0.30:
                                    is_dup = True
                                    break
                    if not is_dup:
                        dedup_frame_dets.append(d)
                current_frame_detections = dedup_frame_dets
                last_detections = current_frame_detections
            else:
                current_frame_detections = last_detections
                
            annotated_frame = _draw_detection_overlay(frame, current_frame_detections, job, frame_idx, total_frames)
            out_writer.write(annotated_frame)
            
            job.current_frame = frame_idx
            if total_frames > 0:
                job.progress = (frame_idx / total_frames) * 100.0
                
            now = time.time()
            job.elapsed_sec = now - job.start_time
            if job.elapsed_sec > 0:
                job.processing_fps = frame_idx / job.elapsed_sec
                remaining_frames = max(0, total_frames - frame_idx)
                job.eta_sec = remaining_frames / job.processing_fps if job.processing_fps > 0 else 0.0

        cap.release()
        out_writer.release()
        try:
            csv_file.close()
        except Exception:
            pass
        
        if job.status == "cancelled":
            if os.path.exists(temp_avi_path):
                os.remove(temp_avi_path)
            return

        logger.info(f"[VideoTester] Remuxing job {job.job_id} to H.264 MP4 via FFmpeg...")
        ffmpeg_cmd = [
            "ffmpeg", "-y",
            "-i", temp_avi_path,
            "-c:v", "libx264",
            "-preset", "ultrafast",
            "-crf", "23",
            "-pix_fmt", "yuv420p",
            "-movflags", "+faststart",
            job.result_path
        ]
        
        res = subprocess.run(ffmpeg_cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        if res.returncode != 0:
            logger.warning(f"[VideoTester] FFmpeg libx264 conversion failed: {res.stderr.decode('utf-8', errors='ignore')}")
            shutil.copyfile(temp_avi_path, job.result_path)
            
        if os.path.exists(temp_avi_path):
            try: os.remove(temp_avi_path)
            except: pass
            
        job.progress = 100.0
        job.status = "completed"
        logger.info(f"[VideoTester] Job {job.job_id} successfully completed in {job.elapsed_sec:.1f}s.")
        
    except Exception as e:
        logger.error(f"[VideoTester] Job {job.job_id} failed with error: {e}", exc_info=True)
        job.status = "failed"
        job.error = str(e)
        if os.path.exists(temp_avi_path):
            try: os.remove(temp_avi_path)
            except: pass


def start_video_test(job_id: str):
    job = get_job(job_id)
    if not job:
        raise ValueError(f"Job {job_id} not found")
    t = threading.Thread(target=_process_video_worker, args=(job,), daemon=True, name=f"VideoTestWorker-{job_id}")
    t.start()
    return job
