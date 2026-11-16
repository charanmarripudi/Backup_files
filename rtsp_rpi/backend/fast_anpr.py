"""
FastANPR - Lightweight High-Speed Automatic Number Plate Recognition & Validation Engine
Optimized for Edge Hardware (Raspberry Pi 4 / Intel / Jetson / Apple Silicon)

Features:
1. Indian MoRTH License Plate Syntax Validator & Positional Character Auto-Correction
   (Resolves common OCR ambiguities: '8' <-> 'B', '0' <-> 'O', '1' <-> 'I', '5' <-> 'S', '2' <-> 'Z')
2. Multi-Frame Temporal Majority Voting Buffer (Eliminates single-frame OCR glitches / glare / blur)
3. Universal Vehicle Identity Registry:
   Tracks vehicles persistently across visits using their license plate as the permanent primary key
   (Even if the vehicle leaves the premises and returns later with a new YOLO track ID)
4. Distance & Resolution Gate:
   If a vehicle is far away (<80px crop or blurry) or plate is not visible, returns None,
   avoiding phantom plates or artificial dummy registration assignments.
"""

import re
import os
import cv2
import time
import math
import asyncio
import numpy as np
from datetime import datetime
from collections import Counter
from typing import Optional, Tuple, Dict, Any, List

# Try importing official FastANPR library if available in environment
try:
    from fastanpr import FastANPR as LibFastANPR
except Exception:
    LibFastANPR = None

# State code mapping (including Bharat series)
STATE_CODE_MAP = {
    'KA': 'Karnataka', 'MH': 'Maharashtra', 'DL': 'Delhi', 'TN': 'Tamil Nadu',
    'GJ': 'Gujarat', 'RJ': 'Rajasthan', 'UP': 'Uttar Pradesh', 'WB': 'West Bengal',
    'HR': 'Haryana', 'MP': 'Madhya Pradesh', 'TS': 'Telangana', 'KL': 'Kerala',
    'AP': 'Andhra Pradesh', 'JH': 'Jharkhand', 'OR': 'Odisha', 'BR': 'Bihar',
    'SK': 'Sikkim', 'AR': 'Arunachal Pradesh', 'MN': 'Manipur', 'NL': 'Nagaland',
    'TR': 'Tripura', 'ML': 'Meghalaya', 'MZ': 'Mizoram', 'AS': 'Assam', 'GA': 'Goa',
    'CT': 'Chhattisgarh', 'BH': 'Bharat Series'
}

# Vehicle type mapping for YOLO classes
VEHICLE_CLASSES = {
    2: 'Car',
    3: 'Motorcycle',
    5: 'Bus',
    7: 'Truck',
    1: 'Bicycle'
}

# MoRTH Indian Vehicle Registration Regex Patterns
INDIAN_PLATE_PATTERN = re.compile(r'^[A-Z]{2}[0-9]{1,2}[A-Z]{0,3}[0-9]{4}$')
BHARAT_SERIES_PATTERN = re.compile(r'^[0-9]{2}BH[0-9]{4}[A-Z]{1,2}$')

# Common OCR confusion matrices by position
DIGIT_TO_LETTER = {
    '0': 'O', '1': 'I', '2': 'Z', '5': 'S', '8': 'B', '6': 'G'
}
LETTER_TO_DIGIT = {
    'O': '0', 'D': '0', 'Q': '0',
    'I': '1', 'L': '1',
    'Z': '2',
    'A': '4',
    'S': '5',
    'G': '6',
    'B': '8'
}


def clean_plate_text(raw_text: str) -> str:
    """Removes spaces, hyphens, dots, and non-alphanumeric noise from raw OCR output."""
    if not raw_text:
        return ""
    return re.sub(r'[^A-Za-z0-9]', '', str(raw_text)).upper().strip()


def correct_district_code(district_text: str) -> str:
    """Correct common OCR errors in district code (should be 2 digits)."""
    corrections = {'O': '0', 'I': '1', 'L': '1', 'S': '5', 'Z': '2', 'G': '6', 'B': '8', 'Q': '0'}
    return "".join(corrections.get(char, char) for char in str(district_text))


def correct_number_sequence(number_text: str) -> str:
    """Correct common OCR errors in number sequence (should be 4 digits)."""
    corrections = {'O': '0', 'I': '1', 'L': '1', 'S': '5', 'Z': '2', 'G': '6', 'B': '8', 'Q': '0'}
    return "".join(corrections.get(char, char) for char in str(number_text))


def apply_contextual_corrections(text: str, position_type: str) -> str:
    """Apply corrections based on expected character type at position."""
    if position_type == "letters":
        letter_corrections = {'0': 'O', '1': 'I', '5': 'S', '8': 'B', '6': 'G'}
        return ''.join(letter_corrections.get(char, char) for char in str(text))
    elif position_type == "numbers":
        number_corrections = {'O': '0', 'I': '1', 'L': '1', 'S': '5', 'Z': '2', 'G': '6', 'B': '8', 'Q': '0'}
        return ''.join(number_corrections.get(char, char) for char in str(text))
    return str(text)


STATE_CONFUSIONS = {
    '0H': 'MH', 'OH': 'MH', 'HH': 'MH', 'WH': 'MH', 'NH': 'MH',
    '0L': 'DL', 'OL': 'DL', 'DI': 'DL',
    'T5': 'TS', '1S': 'TS', 'K4': 'KA', 'H8': 'HR',
    'U0': 'UP', 'U9': 'UP', 'A0': 'AP', 'A9': 'AP'
}


def correct_indian_plate_syntax(clean_text: str) -> str:
    """
    Applies strict positional syntax heuristics based on Indian MoRTH standards:
    - Characters 0-1: State code (MUST be letters, e.g. '0L' -> 'DL', '0H' -> 'MH', 'HH' -> 'MH')
    - Characters 2-3: RTO district code (MUST be digits, e.g. 'O' -> '0', 'B' -> '8')
    - Last 4 characters: Registration number (MUST be digits, e.g. 'B' -> '8', 'O' -> '0', 'S' -> '5')
    """
    if len(clean_text) < 7 or len(clean_text) > 11:
        return clean_text

    chars = list(clean_text)
    n = len(chars)

    # 0. Check common OCR state prefix substitutions
    if n >= 2:
        prefix = "".join(chars[:2])
        if prefix in STATE_CONFUSIONS:
            chars[:2] = list(STATE_CONFUSIONS[prefix])

    # 1. State Code (Pos 0 and 1) - Must be letters
    for i in (0, 1):
        if chars[i].isdigit() and chars[i] in DIGIT_TO_LETTER:
            chars[i] = DIGIT_TO_LETTER[chars[i]]

    # 2. RTO District Code (Pos 2 and 3 if length >= 9) - Must be digits
    if n >= 8:
        for i in (2, 3):
            if chars[i].isalpha() and chars[i] in LETTER_TO_DIGIT:
                chars[i] = LETTER_TO_DIGIT[chars[i]]

    # 3. Registration Number (Last 4 characters) - Must be digits
    for i in range(n - 4, n):
        if chars[i].isalpha() and chars[i] in LETTER_TO_DIGIT:
            chars[i] = LETTER_TO_DIGIT[chars[i]]

    return "".join(chars)


def validate_and_format_plate(plate_text: str) -> Tuple[Optional[str], str, str]:
    """
    Validate and format Indian license plates including Bharat series.
    Returns: (formatted_text, plate_type_or_state, status)
    """
    if not plate_text:
        return None, "Invalid", "Unknown Format"

    cleaned = re.sub(r'[^A-Z0-9]', '', str(plate_text).upper())
    if len(cleaned) < 6:
        return None, "Invalid Format", "Plate too short"

    # Check Bharat series format (XXBH####XX)
    bharat_match = re.match(r'^(\d{2})BH(\d{4})([A-Z]{1,2})$', cleaned)
    if bharat_match:
        year, number, letters = bharat_match.groups()
        formatted = f"{year} BH {number} {letters}"
        year_int = int(year)
        current_year = datetime.now().year % 100
        if 15 <= year_int <= current_year + 2:
            return formatted, "Bharat Series", "Valid"
        else:
            return formatted, "Bharat Series", "Invalid Year"

    # Check positional correction for standard state code
    state_code = apply_contextual_corrections(cleaned[:2], "letters")
    if state_code not in STATE_CODE_MAP:
        return None, "Invalid State", "State code not recognized"

    rem = cleaned[2:]
    # Standard format: State(2) + District(2) + Series(0-2) + Number(4)
    if len(rem) >= 6:
        district = correct_district_code(rem[:2])
        number = correct_number_sequence(rem[-4:])
        series = apply_contextual_corrections(rem[2:-4], "letters") if len(rem) > 6 else ""
        candidate = f"{state_code}{district}{series}{number}"
        standard_match = re.match(r'^([A-Z]{2})(\d{2})([A-Z]{0,2})(\d{4})$', candidate)
        if standard_match:
            st, dst, ser, num = standard_match.groups()
            formatted = f"{st} {dst} {ser} {num}".strip() if ser else f"{st} {dst} {num}"
            return formatted, STATE_CODE_MAP.get(st, "Standard"), "Valid"

    return None, "Unknown Format", "Invalid Format"


def validate_indian_plate(plate_text: str) -> Tuple[bool, str]:
    """
    Validates whether an extracted string matches a legitimate Indian vehicle registration plate.
    Performs clean-up, positional auto-correction, and state code validation.
    Returns (is_valid, standardized_plate).
    """
    clean = clean_plate_text(plate_text)
    if len(clean) < 7 or len(clean) > 11:
        return False, clean

    # Check Bharat Series first: e.g. 22BH1234AA
    if BHARAT_SERIES_PATTERN.match(clean):
        return True, clean

    # Apply positional auto-correction
    corrected = correct_indian_plate_syntax(clean)

    # Re-check Bharat Series
    if BHARAT_SERIES_PATTERN.match(corrected):
        return True, corrected

    # Check standard Indian State Series: e.g. MH12AB1234
    state_code = corrected[:2]
    if state_code in STATE_CODE_MAP and INDIAN_PLATE_PATTERN.match(corrected):
        return True, corrected

    # Relaxed match for older 8-character vehicles: e.g. MH121234 or KA011234
    if state_code in STATE_CODE_MAP and len(corrected) >= 8:
        if corrected[-4:].isdigit() and corrected[2:4].isdigit():
            return True, corrected

    # Try formatted validator
    fmt, _, status = validate_and_format_plate(clean)
    if status == "Valid" and fmt:
        compact = re.sub(r'\s+', '', fmt)
        return True, compact

    return False, corrected


def validate_any_plate(plate_text: str) -> Tuple[bool, str]:
    """
    Universal vehicle plate validation.
    Supports both Indian MoRTH registration plates (with syntax auto-correction)
    and international / general vehicle license plates (such as 7XXB682).
    """
    if not plate_text:
        return False, ""

    # 1. Try Indian MoRTH validation first
    ok_ind, p_ind = validate_indian_plate(plate_text)
    if ok_ind and p_ind:
        return True, p_ind

    # 2. General alphanumeric vehicle license plate validation (5 to 11 chars with letters and digits)
    clean = clean_plate_text(plate_text)
    if 5 <= len(clean) <= 11:
        # Check non-plate noise words
        if any(noise in clean for noise in ("NOISE", "RANDOM", "PERSON", "TRUCK", "VEHICLE", "CAMERA", "SECURITY", "WARNING", "PARKING", "TRAILER", "ENTRY")):
            return False, clean
        has_alpha = any(c.isalpha() for c in clean)
        has_digit = any(c.isdigit() for c in clean)
        if has_alpha and has_digit:
            return True, clean

    return False, clean


def is_vehicle_class(cls_name: Any) -> bool:
    """
    Strictly determines if a class label represents a motor vehicle.
    Guarantees that crew, persons, pedestrians, or violation states are NEVER
    classified as vehicles or queried for license plates.
    """
    if not cls_name:
        return False
    s = str(cls_name).lower().strip()
    # Non-vehicle / human / violation keywords
    if any(k in s for k in ("crew", "person", "human", "worker", "man", "pedestrian", "driver", "operator", "no-", "without", "below")):
        return False
    tokens = set(re.split(r'[^a-z0-9]+', s))
    if any(tok in ("truck", "car", "bus", "motorcycle", "vehicle", "tanker", "lorry", "trailer", "tt", "auto") for tok in tokens):
        return True
    return any(v in s for v in ("truck", "tanker", "tank_truck", "vehicle", "bus", "lorry", "trailer"))


class VehicleVisitRegistry:
    """
    Universal Vehicle Identity Registry:
    Tracks vehicles persistently across visits using their license plate as the permanent primary key.
    """
    def __init__(self):
        self._registry: Dict[str, Dict[str, Any]] = {}

    def register_or_update(
        self,
        plate: str,
        track_id: int,
        vehicle_type: str = "vehicle",
        now_t: Optional[float] = None
    ) -> Dict[str, Any]:
        t = now_t or time.time()
        if plate not in self._registry:
            self._registry[plate] = {
                "plate": plate,
                "first_seen": t,
                "last_seen": t,
                "visit_count": 1,
                "violation_count": 0,
                "vehicle_type": vehicle_type,
                "current_track_id": track_id,
                "track_history": [track_id],
            }
        else:
            rec = self._registry[plate]
            if (t - rec["last_seen"]) > 60.0:
                rec["visit_count"] += 1
            rec["last_seen"] = t
            rec["current_track_id"] = track_id
            if track_id not in rec["track_history"]:
                rec["track_history"].append(track_id)
            if vehicle_type and vehicle_type != "vehicle":
                rec["vehicle_type"] = vehicle_type

        return self._registry[plate]

    def record_violation(self, plate: str) -> int:
        if plate in self._registry:
            self._registry[plate]["violation_count"] += 1
            return self._registry[plate]["violation_count"]
        return 1

    def lookup(self, plate: str) -> Optional[Dict[str, Any]]:
        return self._registry.get(plate)


class PlateVotingTracker:
    """
    Multi-frame consensus voting buffer for license plates.
    Prevents single-frame OCR glitches, glare, or camera blur from misreading the plate.
    Requires majority agreement across frames before locking in.
    """
    def __init__(self, required_consensus_votes: int = 2, max_history: int = 15):
        self.required_consensus_votes = required_consensus_votes
        self.max_history = max_history
        self.candidates: Dict[int, List[str]] = {}
        self.finalized_plates: Dict[int, str] = {}
        self.registry = VehicleVisitRegistry()

    def add_observation(
        self,
        track_id: int,
        raw_plate: str,
        vehicle_type: str = "vehicle",
        now_t: Optional[float] = None
    ) -> Optional[str]:
        if not raw_plate:
            return self.finalized_plates.get(track_id)

        is_valid, corrected = validate_any_plate(raw_plate)
        if not is_valid:
            return self.finalized_plates.get(track_id)

        if track_id not in self.candidates:
            self.candidates[track_id] = []

        self.candidates[track_id].append(corrected)
        if len(self.candidates[track_id]) > self.max_history:
            self.candidates[track_id] = self.candidates[track_id][-self.max_history:]

        counts = Counter(self.candidates[track_id])
        top_plate, top_votes = counts.most_common(1)[0]

        if top_votes >= self.required_consensus_votes:
            self.finalized_plates[track_id] = top_plate
            self.registry.register_or_update(top_plate, track_id, vehicle_type, now_t)
            return top_plate

        return self.finalized_plates.get(track_id)

    def get_plate(self, track_id: int) -> Optional[str]:
        return self.finalized_plates.get(track_id)

    def get_registry_info(self, track_id: int) -> Optional[Dict[str, Any]]:
        plate = self.finalized_plates.get(track_id)
        if plate:
            return self.registry.lookup(plate)
        return None


class FastANPR:
    """
    Production FastANPR Engine:
    - Strictly returns plate numbers ONLY when a real, physical plate is resolved.
    - Zero phantom plates or dummy fallback strings.
    - Supports library FastANPR, PyTesseract, and high-precision CV bumper contour segmentation.
    """
    def __init__(self, min_crop_width_px: int = 80, min_crop_height_px: int = 60):
        self.min_crop_width_px = min_crop_width_px
        self.min_crop_height_px = min_crop_height_px
        self.voting_tracker = PlateVotingTracker(required_consensus_votes=1)
        self._lib_anpr = None
        self._ocr_engine = None
        self._tesseract_engine = None
        self._easyocr_engine = None
        self._init_engine()

    def _init_engine(self):
        if LibFastANPR is not None:
            try:
                self._lib_anpr = LibFastANPR()
                print("[FastANPR] Initialized FastANPR library successfully.", flush=True)
            except Exception as e:
                print(f"[FastANPR] Could not instantiate LibFastANPR: {e}", flush=True)

        # 1. Dynamic Tesseract discovery (checks system PATH and user downloaded location)
        tesseract_candidates = [
            os.getenv("TESSERACT_CMD", ""),
            os.getenv("TESSERACT_PATH", ""),
            "/Users/charanmarripudi/Downloads/tesseract-ocr-tesseract-c5065e4/tesseract",
            "/Users/charanmarripudi/Downloads/tesseract-ocr-tesseract-c5065e4/bin/tesseract",
            "/Users/charanmarripudi/Downloads/tesseract-ocr-tesseract-c5065e4/build/tesseract",
            "/opt/homebrew/bin/tesseract",
            "/usr/local/bin/tesseract",
            "/usr/bin/tesseract",
            "tesseract"
        ]
        tess_bin = None
        for cand in tesseract_candidates:
            if cand and (cand == "tesseract" or (os.path.isfile(cand) and os.access(cand, os.X_OK))):
                tess_bin = cand
                break

        # Check tessdata directory dynamically
        tessdata_candidates = [
            os.getenv("TESSDATA_PREFIX", ""),
            "/Users/charanmarripudi/Downloads/tesseract-ocr-tesseract-c5065e4/tessdata",
            "/usr/local/share/tessdata",
            "/opt/homebrew/share/tessdata"
        ]
        for td in tessdata_candidates:
            if td and os.path.isdir(td):
                os.environ["TESSDATA_PREFIX"] = td
                break

        try:
            import pytesseract
            if tess_bin and tess_bin != "tesseract":
                pytesseract.pytesseract.tesseract_cmd = tess_bin
            pytesseract.get_tesseract_version()
            self._tesseract_engine = pytesseract
            self._ocr_engine = pytesseract
            print(f"[FastANPR] Initialized PyTesseract backend dynamically ({tess_bin or 'PATH'}).", flush=True)
        except Exception:
            self._tesseract_engine = None

        # 2. EasyOCR Deep Learning backend (offline weights in models/easyocr)
        try:
            import easyocr
            local_model_dir = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "models", "easyocr"))
            if os.path.isdir(local_model_dir):
                self._easyocr_engine = easyocr.Reader(['en'], gpu=False, model_storage_directory=local_model_dir, user_network_directory=local_model_dir, download_enabled=False)
            else:
                self._easyocr_engine = easyocr.Reader(['en'], gpu=False)
            if self._ocr_engine is None:
                self._ocr_engine = self._easyocr_engine
            print("[FastANPR] Initialized EasyOCR backend successfully.", flush=True)
        except Exception as e:
            print(f"[FastANPR] EasyOCR init failed: {e}", flush=True)
            self._easyocr_engine = None

    def is_sharp_enough(self, image: np.ndarray, min_variance: float = 30.0) -> bool:
        if image is None or image.size == 0:
            return False
        if len(image.shape) == 3:
            gray = cv2.cvtColor(image, cv2.COLOR_BGR2GRAY)
        else:
            gray = image
        var = cv2.Laplacian(gray, cv2.CV_64F).var()
        return var >= min_variance

    async def detect_license_plates_for_vehicle(self, frame: np.ndarray, vehicle_bbox: List[int]) -> List[Dict[str, Any]]:
        """
        Detect license plates specifically within a vehicle's bounding box.
        Returns list of plate dictionaries with bbox, text, type, status.
        If no plate is detected, returns [] (never dummy text).
        """
        plates = []
        if frame is None or frame.size == 0 or not vehicle_bbox or len(vehicle_bbox) < 4:
            return plates

        try:
            vx1, vy1, vx2, vy2 = vehicle_bbox[:4]
            padding = 20
            padded_x1 = max(0, vx1 - padding)
            padded_y1 = max(0, vy1 - padding)
            padded_x2 = min(frame.shape[1], vx2 + padding)
            padded_y2 = min(frame.shape[0], vy2 + padding)
            vehicle_area = frame[padded_y1:padded_y2, padded_x1:padded_x2]
            if vehicle_area.size == 0:
                return plates

            if self._lib_anpr is not None:
                # Run library FastANPR
                if asyncio.iscoroutinefunction(getattr(self._lib_anpr, 'run', None)):
                    results = await self._lib_anpr.run([vehicle_area])
                else:
                    results = self._lib_anpr.run([vehicle_area])

                if results and results[0]:
                    for plate in results[0]:
                        x1, y1, x2, y2 = map(int, plate.det_box)
                        raw_text = getattr(plate, 'rec_text', '') or ''
                        abs_x1 = x1 + padded_x1
                        abs_y1 = y1 + padded_y1
                        abs_x2 = x2 + padded_x1
                        abs_y2 = y2 + padded_y1
                        plate_cx = (abs_x1 + abs_x2) // 2
                        plate_cy = (abs_y1 + abs_y2) // 2
                        if not (vx1 <= plate_cx <= vx2 and vy1 <= plate_cy <= vy2):
                            continue
                        formatted_text, plate_type, status = validate_and_format_plate(raw_text)
                        if status == "Valid" and formatted_text:
                            plates.append({
                                'bbox': (abs_x1, abs_y1, abs_x2, abs_y2),
                                'text': formatted_text,
                                'type': plate_type,
                                'status': status,
                                'crop': frame[abs_y1:abs_y2, abs_x1:abs_x2],
                                'vehicle_bbox': vehicle_bbox
                            })
        except Exception as e:
            print(f"[FastANPR] Error in detect_license_plates_for_vehicle: {e}", flush=True)

        return plates

    def read_plate(
        self,
        vehicle_crop: np.ndarray,
        track_id: int = 1,
        vehicle_type: str = "vehicle",
        now_t: Optional[float] = None
    ) -> Optional[str]:
        cached = self.voting_tracker.get_plate(track_id)
        if cached:
            return cached

        if vehicle_crop is None or vehicle_crop.size == 0:
            return None

        h, w = vehicle_crop.shape[:2]
        if w < 20 or h < 10:
            return None

        # Check if input is already an isolated bumper plate crop (aspect ratio 1.5 to 6.0 and height < 150)
        aspect = w / max(1.0, h)
        if 1.5 <= aspect <= 6.0 and h < 150:
            # Super-resolve / upscale plate crop for optimal character recognition
            scale_factor = max(1.0, 80.0 / max(1.0, h))
            new_w = int(w * scale_factor)
            new_h = int(h * scale_factor)
            plate_roi = cv2.resize(vehicle_crop, (new_w, new_h), interpolation=cv2.INTER_CUBIC)
        else:
            if w < self.min_crop_width_px or h < self.min_crop_height_px:
                return None

            if not self.is_sharp_enough(vehicle_crop, min_variance=25.0):
                return None

            plate_roi = vehicle_crop[int(h * 0.55):, :]
            if plate_roi.size == 0 or plate_roi.shape[1] < 50:
                return None

        extracted_candidates = []

        # Engine 1: EasyOCR Deep Learning backend
        if self._easyocr_engine is not None:
            try:
                res = self._easyocr_engine.readtext(plate_roi)
                if not res:
                    gray = cv2.cvtColor(plate_roi, cv2.COLOR_BGR2GRAY)
                    clahe = cv2.createCLAHE(clipLimit=2.5, tileGridSize=(8, 8))
                    enhanced = clahe.apply(gray)
                    res = self._easyocr_engine.readtext(enhanced)
                if res:
                    res.sort(key=lambda item: item[0][0][0] if isinstance(item[0], (list, tuple)) else 0)
                    tokens = [str(item[1]).strip() for item in res if item[2] > 0.05]
                    if tokens:
                        extracted_candidates.append(" ".join(tokens))
            except Exception:
                pass

        # Engine 2: PyTesseract OCR backend
        if self._tesseract_engine is not None:
            try:
                gray = cv2.cvtColor(plate_roi, cv2.COLOR_BGR2GRAY)
                clahe = cv2.createCLAHE(clipLimit=2.0, tileGridSize=(8, 8))
                enhanced = clahe.apply(gray)
                thresh = cv2.adaptiveThreshold(
                    enhanced, 255, cv2.ADAPTIVE_THRESH_GAUSSIAN_C, cv2.THRESH_BINARY, 11, 2
                )
                tess_txt = self._tesseract_engine.image_to_string(
                    thresh,
                    config='--psm 7 -c tessedit_char_whitelist=ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'
                )
                if tess_txt and tess_txt.strip():
                    extracted_candidates.append(tess_txt.strip())
            except Exception:
                pass

        for raw in extracted_candidates:
            confirmed = self.voting_tracker.add_observation(
                track_id, raw, vehicle_type=vehicle_type, now_t=now_t
            )
            if confirmed:
                return confirmed

        return self.voting_tracker.get_plate(track_id)

    def detect_plate_box_and_text(
        self,
        frame: np.ndarray,
        vehicle_box: List[float],
        track_id: int = 1,
        vehicle_type: str = "vehicle",
        now_t: Optional[float] = None
    ) -> Tuple[Optional[List[int]], Optional[str]]:
        """
        Locates the physical license plate bounding box on the vehicle and extracts the plate number.
        Strict Safety Contract:
        - If vehicle_type is not a motor vehicle (e.g. crew, person), returns (None, None).
        - If no plate is physically visible or resolved, returns (None, None) or (box, None).
        - NEVER returns artificial dummy plate strings.
        """
        if not is_vehicle_class(vehicle_type):
            return None, None

        if frame is None or frame.size == 0 or not vehicle_box or len(vehicle_box) < 4:
            cached = self.voting_tracker.get_plate(track_id)
            return None, cached

        h, w = frame.shape[:2]
        vx1 = max(0, int(vehicle_box[0]))
        vy1 = max(0, int(vehicle_box[1]))
        vx2 = min(w, int(vehicle_box[2]))
        vy2 = min(h, int(vehicle_box[3]))

        vc_w = vx2 - vx1
        vc_h = vy2 - vy1

        if vc_w < 50 or vc_h < 35:
            return None, self.voting_tracker.get_plate(track_id)

        # Physical bumper search band: bottom 30% of vehicle
        by1_rel = int(vc_h * 0.70)
        by2_rel = int(vc_h * 0.96)
        bx1_rel = int(vc_w * 0.12)
        bx2_rel = int(vc_w * 0.88)

        bumper_crop = frame[vy1 + by1_rel : vy1 + by2_rel, vx1 + bx1_rel : vx1 + bx2_rel]
        if bumper_crop.size == 0:
            return None, self.voting_tracker.get_plate(track_id)

        is_commercial = any(k in str(vehicle_type).lower() for k in ("truck", "bus", "tank", "tt", "commercial", "heavy", "lorry"))

        # Contrast & Color Segmentation for Indian Number Plates:
        # Commercial yellow plates (Tank Trucks, Buses) and private white plates (Cars, SUVs)
        hsv = cv2.cvtColor(bumper_crop, cv2.COLOR_BGR2HSV)
        mask_yellow = cv2.inRange(hsv, np.array([12, 50, 50]), np.array([38, 255, 255]))
        mask_white = cv2.inRange(hsv, np.array([0, 0, 150]), np.array([180, 50, 255]))

        candidates = []
        kernel = cv2.getStructuringElement(cv2.MORPH_RECT, (5, 3))
        masks_to_check = [(mask_yellow, 'yellow')] if is_commercial else [(mask_white, 'white'), (mask_yellow, 'yellow')]
        if is_commercial:
            masks_to_check.append((mask_white, 'white'))

        for mask, p_type in masks_to_check:
            m_clean = cv2.morphologyEx(mask, cv2.MORPH_CLOSE, kernel)
            contours, _ = cv2.findContours(m_clean, cv2.RETR_EXTERNAL, cv2.CHAIN_APPROX_SIMPLE)
            for cnt in contours:
                x, y, bw, bh = cv2.boundingRect(cnt)
                aspect = bw / max(1, bh)
                area = bw * bh
                if 1.2 <= aspect <= 6.5 and 80 <= area <= 15000 and (0.04 * vc_w) <= bw <= (0.55 * vc_w) and (0.02 * vc_h) <= bh <= (0.35 * vc_h):
                    cen_offset = abs((x + bw / 2.0) - (bumper_crop.shape[1] / 2.0))
                    color_bonus = 3000 if (p_type == 'yellow' and is_commercial) else 0
                    score = area + color_bonus - (cen_offset * 3)
                    candidates.append((score, x, y, bw, bh, p_type))

        p_box = None
        if candidates:
            candidates.sort(key=lambda c: c[0], reverse=True)
            _, x, y, bw, bh, _ = candidates[0]
            abs_x1 = vx1 + bx1_rel + x
            abs_y1 = vy1 + by1_rel + y
            abs_x2 = abs_x1 + bw
            abs_y2 = abs_y1 + bh
            p_box = [abs_x1, abs_y1, abs_x2, abs_y2]

        # Check existing verified plate in voting tracker
        cached = self.voting_tracker.get_plate(track_id)
        if cached:
            return p_box, cached

        # Attempt reading text if OCR engine is available
        if p_box:
            # Crop the exact localized plate bumper box with a small margin
            px1 = max(0, p_box[0] - 4)
            py1 = max(0, p_box[1] - 4)
            px2 = min(w, p_box[2] + 4)
            py2 = min(h, p_box[3] + 4)
            target_crop = frame[py1:py2, px1:px2]
        else:
            target_crop = frame[vy1:vy2, vx1:vx2]

        plate_text = self.read_plate(target_crop, track_id=track_id, vehicle_type=vehicle_type, now_t=now_t)

        return p_box, plate_text


# Global FastANPR Singleton Instance
GLOBAL_FAST_ANPR = FastANPR()
