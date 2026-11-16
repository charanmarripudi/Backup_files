import os
import threading
import time
from collections import deque
from datetime import datetime
import cv2
from ultralytics import YOLO
from producer_code import RabbitMQProducer
import base64
import re
import pytesseract
from fastanpr import FastANPR
import asyncio
import numpy as np

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

class ColorExtractor:
    def __init__(self):
        self.color_definitions = {
            'white': {'lower': [0, 0, 200], 'upper': [180, 30, 255]},
            'light_yellow': {'lower': [22, 50, 180], 'upper': [35, 140, 255]},
            'yellow': {'lower': [20, 100, 100], 'upper': [35, 255, 255]},
            'orange': {'lower': [10, 100, 100], 'upper': [20, 255, 255]},
            'brown': {'lower': [10, 100, 20], 'upper': [20, 200, 150]},
            'light_orange': {'lower': [10, 80, 200], 'upper': [20, 180, 255]},
            'dark_orange': {'lower': [10, 150, 100], 'upper': [20, 255, 150]},
            'green': {'lower': [40, 50, 50], 'upper': [85, 255, 255]},
            'light_green': {'lower': [45, 30, 180], 'upper': [70, 100, 255]},
            'dark_green': {'lower': [35, 100, 30], 'upper': [85, 255, 100]},
            'red': {'lower': [[0, 100, 100], [170, 100, 100]], 'upper': [[10, 255, 255], [180, 255, 255]]},
            'light_red': {'lower': [[0, 50, 150], [170, 50, 150]], 'upper': [[10, 150, 255], [180, 150, 255]]},
            'blue': {'lower': [100, 50, 50], 'upper': [130, 255, 255]},
            'light_blue': {'lower': [90, 20, 180], 'upper': [130, 80, 255]},
            'dark_blue': {'lower': [100, 100, 50], 'upper': [130, 255, 150]},
            'black': {'lower': [0, 0, 0], 'upper': [180, 255, 40]},
            'gray': {'lower': [0, 0, 40], 'upper': [180, 30, 180]},
            'silver': {'lower': [0, 0, 150], 'upper': [180, 30, 220]}
        }

        self.color_bgr = {
            'white': (255, 255, 255),
            'light_yellow': (204, 255, 255),
            'yellow': (0, 255, 255),
            'orange': (0, 165, 255),
            'light_orange': (102, 204, 255),
            'dark_orange': (0, 100, 200),
            'brown': (42, 42, 165),
            'green': (0, 255, 0),
            'light_green': (144, 238, 144),
            'dark_green': (0, 100, 0),
            'red': (0, 0, 255),
            'light_red': (255, 102, 102),
            'blue': (255, 0, 0),
            'light_blue': (173, 216, 230),
            'dark_blue': (0, 0, 139),
            'black': (0, 0, 0),
            'gray': (128, 128, 128),
            'silver': (192, 192, 192),
            'unknown': (200, 200, 200),
        }

    def extract_color(self, img):
        if img.size == 0:
            return "unknown"
        img = self._resize_with_aspect(img, 200)
        img = self._preprocess_image(img)
        hsv = cv2.cvtColor(img, cv2.COLOR_BGR2HSV)
        return self._get_dominant_color(hsv)

    def _resize_with_aspect(self, img, max_dim=200):
        h, w = img.shape[:2]
        if max(h, w) > max_dim:
            scale = max_dim / max(h, w)
            return cv2.resize(img, (int(w * scale), int(h * scale)))
        return img

    def _preprocess_image(self, img):
        lab = cv2.cvtColor(img, cv2.COLOR_BGR2LAB)
        l, a, b = cv2.split(lab)
        cl = cv2.createCLAHE(clipLimit=2.0, tileGridSize=(8, 8)).apply(l)
        merged = cv2.merge((cl, a, b))
        return cv2.cvtColor(merged, cv2.COLOR_LAB2BGR)  

    def _get_dominant_color(self, hsv_img):
        color_scores = {}
        for color, params in self.color_definitions.items():
            if color.startswith('red') or color.startswith('light_red'):
                lower1, upper1 = np.array(params['lower'][0]), np.array(params['upper'][0])
                lower2, upper2 = np.array(params['lower'][1]), np.array(params['upper'][1])
                mask = cv2.bitwise_or(cv2.inRange(hsv_img, lower1, upper1), cv2.inRange(hsv_img, lower2, upper2))
            else:
                lower = np.array(params['lower'])
                upper = np.array(params['upper'])
                mask = cv2.inRange(hsv_img, lower, upper)

            coverage = np.count_nonzero(mask) / (hsv_img.shape[0] * hsv_img.shape[1])
            color_scores[color] = coverage

        confident_colors = {k: v for k, v in color_scores.items() if v > 0.08}

        if not confident_colors:
            avg_v = np.mean(hsv_img[:, :, 2])
            avg_s = np.mean(hsv_img[:, :, 1])
            if avg_v < 50 and avg_s < 40:
                return "black"
            elif avg_v > 200 and avg_s < 40:
                return "white"
            elif 50 < avg_v < 180 and avg_s < 40:
                return "gray"
            return "unknown"

        return max(confident_colors.items(), key=lambda x: x[1])[0]

    def get_color_bgr(self, color_name):
        return self.color_bgr.get(color_name, (200, 200, 200))

    def get_text_color(self, color_name):
        light_colors = ['white', 'light_yellow', 'yellow', 'light_orange', 'light_green', 'light_blue', 'light_red', 'silver']
        return (0, 0, 0) if color_name in light_colors else (255, 255, 255)

class RTSPStream:
    """Handles RTSP stream connection with threading for continuous frame reading."""
    
    def __init__(self, rtsp_url):
        self.rtsp_url = rtsp_url
        self.cap = cv2.VideoCapture(rtsp_url)
        self.cap.set(cv2.CAP_PROP_BUFFERSIZE, 2)
        self.lock = threading.Lock()
        self.frame = None
        self.running = True
        self.thread = threading.Thread(target=self.update, daemon=True)
        self.thread.start()
        self.last_read_time = time.time()

    def update(self):
        """Continuously read frames from RTSP stream in background thread."""
        while self.running:
            ret, frame = self.cap.read()
            if ret:
                with self.lock:
                    self.frame = frame
                    self.last_read_time = time.time()
                time.sleep(0.001)
            else:
                self.cap.release()
                time.sleep(1.0)
                self.cap = cv2.VideoCapture(self.rtsp_url)
                self.cap.set(cv2.CAP_PROP_BUFFERSIZE, 2)

    def read(self):
        """Read the latest frame from stream."""
        with self.lock:
            if (self.frame is not None and 
                (time.time() - self.last_read_time) < 1.0):
                frame_to_return = self.frame
                self.frame = None
                return frame_to_return
            return None

    def release(self):
        """Clean up stream resources."""
        self.running = False
        self.thread.join(timeout=2.0)
        if hasattr(self, 'cap') and self.cap:
            self.cap.release()

class VideoProcessor:
    """Handles video file processing with frame rate control."""
    
    def __init__(self, video_path, target_fps=None):
        self.video_path = video_path
        self.cap = cv2.VideoCapture(video_path)
        if not self.cap.isOpened():
            raise ValueError(f"Cannot open video file: {video_path}")
        
        # Get video properties
        self.original_fps = self.cap.get(cv2.CAP_PROP_FPS)
        self.total_frames = int(self.cap.get(cv2.CAP_PROP_FRAME_COUNT))
        self.duration = self.total_frames / self.original_fps if self.original_fps > 0 else 0
        
        # Set target FPS - use original FPS if target_fps is None
        self.target_fps = target_fps if target_fps is not None else self.original_fps
        self.frame_interval = 1.0 / self.target_fps if self.target_fps > 0 else 0
        
        # Frame skipping calculation - only skip if target_fps is lower than original
        if target_fps is not None and self.original_fps > 0 and target_fps < self.original_fps:
            self.skip_ratio = max(1, int(self.original_fps / target_fps))
        else:
            self.skip_ratio = 1
            
        self.current_frame = 0
        self.last_frame_time = time.time()
        
        print(f"Video initialized: {video_path}")
        print(f"Original FPS: {self.original_fps:.2f}, Target FPS: {self.target_fps:.2f}")
        print(f"Total frames: {self.total_frames}, Duration: {self.duration:.2f}s")
        print(f"Frame skip ratio: {self.skip_ratio}")

    def read(self):
        """Read next frame with frame rate control."""
        # Frame rate control - maintain timing
        if self.frame_interval > 0:
            current_time = time.time()
            elapsed = current_time - self.last_frame_time
            
            if elapsed < self.frame_interval:
                time.sleep(self.frame_interval - elapsed)
            
            self.last_frame_time = time.time()
        
        # Read frame(s) with optional skipping
        ret = False
        frame = None
        
        for _ in range(self.skip_ratio):
            ret, frame = self.cap.read()
            self.current_frame += 1
            
            if not ret:
                return False, None
        
        return ret, frame

    def get_progress(self):
        """Get current processing progress."""
        if self.total_frames > 0:
            progress_percent = (self.current_frame / self.total_frames) * 100
        else:
            progress_percent = 0
            
        return {
            'current_frame': self.current_frame,
            'total_frames': self.total_frames,
            'progress_percent': progress_percent,
            'elapsed_time': self.current_frame / self.original_fps if self.original_fps > 0 else 0
        }

    def release(self):
        """Release video capture resources."""
        if hasattr(self, 'cap') and self.cap:
            self.cap.release()

class VehicleDemographicsDetector:
    """Vehicle detection system using YOLO for real-time RTSP stream analysis."""
    
    #DEFAULT_VEHICLE_CLASSES = ['Car', 'Motorcycle', 'Bus', 'Truck', 'Bicycle']
    
    def __init__(self, data):
        # Model configuration
        self.model_path = data.get('model_path')
        self.save_folder = data.get('save_folder', 'detection_results')

        # Detection parameters
        self.confidence_threshold = data.get('confidence_threshold', 0.5)
        self.nms_threshold = data.get('nms_threshold', 0.45)
        self.image_size = data.get('image_size', 640)

        # Stream and timing configuration
        self.input_source = data.get('input_source', 0)
        self.user_fps = data.get('user_fps', 15)
        self.video_target_fps = data.get('video_target_fps', None)
        self.pre_alert_seconds = data.get('pre_alert_seconds', 5)
        self.post_alert_seconds = data.get('post_alert_seconds', 10)

        # Create directories
        self.videos_folder = os.path.join(self.save_folder, 'videos')
        os.makedirs(self.videos_folder, exist_ok=True)
        os.makedirs(self.save_folder, exist_ok=True)

        # Initialize instance variables
        self.running = True
        self.stream = None
        self.video_processor = None
        self.model = None
        self.is_video_mode = self._is_video_file(self.input_source)
        self.video_filename = None

        # Frame buffers and recording management
        max_buffer_size = int(self.pre_alert_seconds * self.user_fps)
        self.raw_frame_buffer = deque(maxlen=max_buffer_size)
        self.recording_videos = {}
        self.video_writers = {}
        self.recording_start_times = {}

        # Detection tracking
        self.detection_start_times = {}
        self.detection_frames = {}
        self.detection_threshold_seconds = 1.0
        self.alerted_detections = {}
        self.sent_alerts = {}  # Track sent alerts to avoid duplicates

        # FPS calculation
        self.frame_times = deque(maxlen=60)
        self.fps_estimate = 30.0
        self.last_alert_times = {}
        self.alert_cooldown = 60.0
        self.file_path = ''
        self.target_frame_interval = 1.0 / self.user_fps if self.user_fps > 0 else 0
        self.last_process_time = 0

        # RabbitMQ producer for sending alerts
        self.rb_producer = RabbitMQProducer("alert_listener")
        self.rb_producer.connect()

        # ROI configuration
        self.roi = data.get('roi', None)  # Initialize roi from input data
        self.show_roi = data.get('show_roi', True)  

        # Initialize additional attributes
        self.color_extractor = ColorExtractor()
        self.add_number_plate = []
        self.alerted_detections = {}
        self.img_filename = None 
        self.background_processors = {}  # Track background threads
        #.futures.ThreadPoolExecutor(max_workers=2)  # New: thread pool
        self.saved_images =set()

    def _is_video_file(self, source):
        """Check if source is a video file or stream."""
        if isinstance(source, str):
            video_extensions = ['.mp4', '.avi', '.mov', '.mkv', '.flv', '.wmv', '.m4v']
            if any(source.lower().endswith(ext) for ext in video_extensions):
                return True
            if os.path.isfile(source):
                return True
        return False
    
    def initialize_model(self):
        """Initialize the YOLO model for vehicle detection."""
        print('Loading the Vehicle Detection Model...')
        self.model = YOLO(self.model_path)
        print("Model loaded successfully")
    
    def validate_roi(self, roi, frame_width, frame_height):
        """Validate the region of interest coordinates."""
        if roi is None:
            return None
        
        try:
            x1 = max(0, min(int(roi['x1']), frame_width - 1))
            y1 = max(0, min(int(roi['y1']), frame_height - 1))
            x2 = max(x1 + 1, min(int(roi['x2']), frame_width))
            y2 = max(y1 + 1, min(int(roi['y2']), frame_height))
            
            if (x2 - x1) < 10 or (y2 - y1) < 10:
                print("ROI too small, using full frame")
                return None
                
            return {'x1': x1, 'y1': y1, 'x2': x2, 'y2': y2}
        except (KeyError, ValueError, TypeError) as e:
            print(f"Invalid ROI format: {e}")
            return None

    def setup_video(self):
        """Set up video processing with the configured parameters."""
        try:
            self.video_processor = VideoProcessor(self.input_source, target_fps=self.user_fps)

            ret, frame = self.video_processor.read()
            if not ret or frame is None:
                raise ValueError("Failed to read first frame from video")
            
            height, width = frame.shape[:2]
            self.frame_width = 640
            self.frame_height = 480
            self.fps = self.user_fps

            self.roi = self.validate_roi(self.roi, width, height)
            if self.roi:
                print(f"ROI Validated: {self.roi}")
            else:
                print("No ROI set or invalid ROI, using full frame detection")
            
            self.video_processor.cap.set(cv2.CAP_PROP_POS_FRAMES, 0)
            self.video_processor.current_frame = 0

            print(f"Video setup complete: {width}x{height}, Target FPS: {self.fps}")
        
        except Exception as e:
            raise ConnectionError(f"Failed to setup video file: {e}")

    def setup_stream(self):
        """Set up stream processing with the configured parameters."""
        self.stream = RTSPStream(self.input_source)

        frame = None
        start_time = time.time()
        timeout_seconds = 15

        while frame is None:
            frame = self.stream.read()
            if time.time() - start_time > timeout_seconds:
                raise ConnectionError(f"Failed to connect to RTSP stream after {timeout_seconds} seconds")
            time.sleep(0.1)
        
        height, width = frame.shape[:2]
        self.raw_frame_buffer.append((time.time(), frame))

        self.roi = self.validate_roi(self.roi, width, height)
        if self.roi:
            print(f"ROI validated: {self.roi}")
        else:
            print("No ROI set or invalid ROI, using full frame detection")

        print(f"Stream setup complete: {width}x{height}, FPS: {self.user_fps}")

        self.frame_width = 1280
        self.frame_height = 720
        self.fps = self.user_fps
    
    def setup_input_source(self):
        """Configure the appropriate input source (video file or stream)."""
        if self.is_video_mode:
            print(f"Processing video file: {self.input_source}")
            self.setup_video()
        else:
            print(f"Connecting to RTSP stream: {self.input_source}")
            self.setup_stream()
    
    
    def get_contrast_color(self, background_color):
        """Get contrasting text color based on background color brightness."""
        if len(background_color) == 3:
            r, g, b = background_color
        else:
            r, g, b = background_color[:3]
        brightness = (r * 299 + g * 587 + b * 114) / 1000
        return (0, 0, 0) if brightness > 127 else (255, 255, 255)

    def draw_detection_box(self, frame, coords, color, text, confidence):
        """Improved bounding box drawing with proper contrast."""
        x1, y1, x2, y2 = map(int, coords)
        h, w = frame.shape[:2]
        
        # Ensure coordinates are within frame bounds
        x1, y1 = max(0, x1), max(0, y1)
        x2, y2 = min(w, x2), min(h, y2)

        # Get colors
        box_color = self.color_extractor.get_color_bgr(color)
        text_color = self.get_contrast_color(box_color)

        # Draw bounding box
        cv2.rectangle(frame, (x1, y1), (x2, y2), box_color, 2)
        
        # Draw label with background
        label = f"{text} {confidence:.2f}"
        (label_w, label_h), _ = cv2.getTextSize(label, cv2.FONT_HERSHEY_SIMPLEX, 0.5, 1)
        
        label_y = y1 - 10 if y1 > 20 else y2 + 20
        cv2.rectangle(frame, (x1, label_y - label_h - 5), 
                     (x1 + label_w + 5, label_y + 5), box_color, -1)
        cv2.putText(frame, label, (x1 + 2, label_y), 
                    cv2.FONT_HERSHEY_SIMPLEX, 0.5, text_color, 1)
        
        return frame

  
    def save_detection_image(self, frame, current_time, detection_info):
        """Save an image with structured naming, ensuring uniqueness."""
        try:
            vehicle_type = detection_info.get('type', 'Unknown')
            vehicle_color = detection_info.get('color', 'Unknown')
            plate_text = detection_info.get('plate_text', 'NO_PLATE')
            
            # Clean plate text for filename
            clean_plate = re.sub(r'[^A-Z0-9]', '', plate_text.upper())
            if not clean_plate:
                clean_plate = "NO_PLATE"

            # Create a base filename
            base_name = f"{vehicle_type}-{vehicle_color}-{clean_plate}"
            timestamp_str = datetime.fromtimestamp(current_time).strftime("%Y%m%d_%H%M%S_%f")
            filename = f"detection_{base_name}_{timestamp_str}.jpg"
            
            images_folder = os.path.abspath(os.path.join(self.save_folder, "images"))
            os.makedirs(images_folder, exist_ok=True)
            save_path = os.path.join(images_folder, filename)

            # Check for uniqueness
            if (vehicle_type, vehicle_color, clean_plate) in self.saved_images:
                #print(f"Image already saved for {base_name}. Not saving duplicate.")
                return None
            
            cv2.imwrite(save_path, frame)
            print(f"Detection saved: {filename}")
            
            # Add to saved images set
            self.saved_images.add((vehicle_type, vehicle_color, clean_plate))
            self.img_filename = save_path
            
            return save_path
        except Exception as e:
            print(f"Error saving detection image: {e}")
            return None


    def draw_roi(self, frame):
        """Draw the region of interest (ROI) on the frame with improved visibility."""
        if self.roi is None or not self.show_roi:
            return frame

        # Draw ROI rectangle
        x1, y1, x2, y2 = self.roi['x1'], self.roi['y1'], self.roi['x2'], self.roi['y2']
        roi_color = (0, 255, 255)  # Yellow color for ROI
        cv2.rectangle(frame, (x1, y1), (x2, y2), roi_color, 2)

        # Add ROI label with contrasting background
        label = "Detection Area"
        (label_width, label_height), _ = cv2.getTextSize(label, cv2.FONT_HERSHEY_SIMPLEX, 0.6, 2)
        
        # Position label above ROI, or below if there's not enough space
        label_y = y1 - 10 if y1 > label_height + 10 else y2 + label_height + 10
        
        # Draw background rectangle for label
        cv2.rectangle(frame, 
                     (x1, label_y - label_height - 5), 
                     (x1 + label_width + 5, label_y + 5), 
                     roi_color, -1)  # Filled rectangle
        
        # Draw text with contrasting color
        text_color = (0, 0, 0)  # Black text on yellow background
        cv2.putText(frame, label, (x1 + 2, label_y), 
                   cv2.FONT_HERSHEY_SIMPLEX, 0.6, text_color, 2)

        return frame
    async def detect_vehicles(self, frame):
        vehicles = []
        if frame is None:
            return vehicles

        try:
            # If ROI is defined, crop the frame
            if self.roi:
                roi_x1, roi_y1, roi_x2, roi_y2 = self.roi['x1'], self.roi['y1'], self.roi['x2'], self.roi['y2']
                detection_frame = frame[roi_y1:roi_y2, roi_x1:roi_x2]
            else:
                detection_frame = frame.copy()

            # Run vehicle detection
            results = self.model(detection_frame, 
                              conf=self.confidence_threshold,
                              iou=self.nms_threshold,
                              imgsz=self.image_size,
                              verbose=False)[0]

            if results.boxes is not None:
                for box in results.boxes:
                    class_id = int(box.cls)
                    if class_id not in VEHICLE_CLASSES:
                        continue

                    # Get bounding box coordinates
                    x1, y1, x2, y2 = map(int, box.xyxy[0])
                    confidence = float(box.conf)

                    # Adjust coordinates if using ROI
                    if self.roi:
                        x1 += self.roi['x1']
                        y1 += self.roi['y1']
                        x2 += self.roi['x1']
                        y2 += self.roi['y1']

                    # Crop vehicle image for color detection
                    vehicle_crop = frame[y1:y2, x1:x2]
                    if vehicle_crop.size == 0:
                        continue

                    # Detect vehicle color
                    vehicle_color = self.color_extractor.extract_color(vehicle_crop)

                    vehicles.append({
                        'type': VEHICLE_CLASSES[class_id],
                        'bbox': (x1, y1, x2, y2),
                        'color': vehicle_color,
                        'confidence': confidence,
                        'crop': vehicle_crop
                    })

        except Exception as e:
            print(f"Error detecting vehicles: {e}")
        
        return vehicles

    async def detect_license_plates(self, frame):
        """Detect and recognize license plates in the frame."""
        plates = []
        if frame is None or self.roi is None:
            return plates

        try:
            roi_x1, roi_y1, roi_x2, roi_y2 = self.roi['x1'], self.roi['y1'], self.roi['x2'], self.roi['y2']
            roi_frame = frame[roi_y1:roi_y2, roi_x1:roi_x2]
            
            # Use FastANPR for plate detection
            fast_anpr = FastANPR()
            results = await fast_anpr.run([roi_frame])
            
            if results and results[0]:
                for plate in results[0]:
                    x1, y1, x2, y2 = map(int, plate.det_box)
                    plate_text = plate.rec_text if plate.rec_text else "UNKNOWN"
                    
                    # Convert coordinates back to original frame
                    x1 += roi_x1
                    y1 += roi_y1 
                    x2 += roi_x1
                    y2 += roi_y1

                    # Expand plate area slightly for better cropping
                    height_expand = int((y2 - y1) * 0.2)
                    width_expand = 5
                    x1 = max(0, x1 - width_expand)
                    y1 = max(0, y1 - height_expand)
                    x2 = min(frame.shape[1], x2 + width_expand)
                    y2 = min(frame.shape[0], y2 + height_expand)

                    plate_crop = frame[y1:y2, x1:x2]

                    # Validate and format plate number
                    formatted_text, plate_type, status = await self.validate_and_format_plate(plate_text)

                    plates.append({
                        'bbox': (x1, y1, x2, y2),
                        'text': formatted_text if formatted_text else plate_text,
                        'type': plate_type,
                        'status': status,
                        'crop': plate_crop
                    })

        except Exception as e:
            print(f"License plate detection error: {e}")
        
        return plates

    async def validate_and_format_plate(self, plate_text):
        """Validate and format Indian license plates including Bharat series."""
        if not plate_text:
            return None, "Invalid", "Unknown Format"
        
        cleaned = re.sub(r'[^A-Z0-9\s]', '', plate_text.upper())
        normalized = re.sub(r'\s+', '', cleaned)
        
        # Check Bharat series format (XXBHXXXXXX)
        bharat_match = re.match(r'^(\d{2})BH(\d{4})([A-Z]{1,2})$', normalized)
        if bharat_match:
            year, number, letters = bharat_match.groups()
            formatted = f"{year} BH {number} {letters}"
            year_int = int(year)
            current_year = datetime.now().year % 100
            if 15 <= year_int <= current_year + 2:
                return formatted, "Bharat Series", "Valid"
            else:
                return formatted, "Bharat Series", "Invalid Year"
        
        # Check standard Indian plate format (XXYYXX####)
        standard_match = re.match(r'^([A-Z]{2})(\d{2})([A-Z]{1,2})(\d{4})$', normalized)
        if standard_match:
            state, district, series, number = standard_match.groups()
            formatted = f"{state} {district} {series} {number}"
            if state in STATE_CODE_MAP:
                return formatted, STATE_CODE_MAP[state], "Valid"
            else:
                return formatted, "Unknown State", "Invalid State Code"
        
        # Return cleaned text if no match
        return cleaned, "Unknown Format", "Invalid Format"
    
    async def process_frame(self, frame):
        """Main frame processing with improved visualization and additional labels."""
        current_time = time.time()
        self.raw_frame_buffer.append((current_time, frame))
        
        # Update FPS calculation
        self.frame_times.append(current_time)
        if len(self.frame_times) >= 10:
            try:
                time_diff = self.frame_times[-1] - self.frame_times[0]
                self.fps_estimate = (len(self.frame_times) - 1) / time_diff if time_diff > 0 else 30.0
            except IndexError:
                self.fps_estimate = 30.0

        annotated_frame = frame.copy()
        annotated_frame = self.draw_roi(annotated_frame)

        # Detect vehicles and plates in parallel
        vehicles, plates = await asyncio.gather(
            self.detect_vehicles(annotated_frame),
            self.detect_license_plates(annotated_frame)
        )

        # Initialize detection counters
        detection_count = 0
        vehicle_types = set()
        avg_confidence = 0.0

        # Process detections
        for vehicle in vehicles:
            # Find matching plate (if any)
            plate_match = None
            if plates:
                vx1, vy1, vx2, vy2 = vehicle['bbox']
                for plate in plates:
                    px1, py1, px2, py2 = plate['bbox']
                    if (vx1 <= (px1 + px2) // 2 <= vx2 and vy1 <= (py1 + py2) // 2 <= vy2):
                        plate_match = plate
                        plates.remove(plate)  # Prevent matching to multiple vehicles
                        break

            detection_info = {
                'type': vehicle['type'],
                'color': vehicle['color'],
                'confidence': vehicle['confidence'],
                'bbox': vehicle['bbox'],
                'plate_text': plate_match['text'] if plate_match else 'NO_PLATE',
                'plate_status': plate_match['status'] if plate_match else None,
                'crop': vehicle['crop']
            }

            # Update detection metrics
            detection_count += 1
            vehicle_types.add(vehicle['type'])
            avg_confidence += vehicle['confidence']

            # Save detection image only if conditions are met
            if (detection_info['type'] in VEHICLE_CLASSES.values() and
                detection_info['color'] != 'unknown' and
                (plate_match and len(plate_match['text']) >= 6 or plate_match is None)):
                image_path = self.save_detection_image(annotated_frame, current_time, detection_info)

                # Start recording only if image was saved successfully
                if image_path:
                    self.start_recording(detection_info, current_time, image_path)

            # Send alert for every detection
            self.alert_vehicle(detection_info)

            # Draw vehicle with contrasting colors
            label_text = f"{vehicle['type']} ({vehicle['color']})"
            if plate_match:
                label_text += f" - {plate_match['text']}"
            annotated_frame = self.draw_detection_box(
                annotated_frame,
                vehicle['bbox'],
                vehicle['color'],
                label_text,
                vehicle['confidence']
            )

        # Handle unmatched plates
        for plate in plates:
            detection_info = {
                'type': 'Unknown',
                'color': 'Unknown',
                'confidence': 1.0,
                'bbox': plate['bbox'],
                'plate_text': plate['text'],
                'plate_status': plate['status'],
                'crop': plate['crop']
            }
            
            # Save detection image for unmatched plates only if conditions are met
            if (detection_info['type'] == 'Unknown' and
                detection_info['color'] == 'Unknown' and
                len(detection_info['plate_text']) >= 6):
                image_path = self.save_detection_image(annotated_frame, current_time, detection_info)
                self.alert_vehicle(detection_info)  # Send alert for unmatched plates

                # Draw plate with contrasting colors
                plate_color = (0, 255, 0) if plate['status'] == "Valid" else (0, 0, 255)
                text_color = self.get_contrast_color(plate_color)
                cv2.rectangle(annotated_frame, (plate['bbox'][0], plate['bbox'][1]), 
                            (plate['bbox'][2], plate['bbox'][3]), plate_color, 2)
                label = f"{plate['text']} ({plate['type']})"
                cv2.putText(annotated_frame, label, (plate['bbox'][0], plate['bbox'][1] - 10), 
                        cv2.FONT_HERSHEY_SIMPLEX, 0.5, text_color, 1)

        # Update recordings
        self.update_recordings(annotated_frame, current_time)

        # Calculate average confidence if there are detections
        if detection_count > 0:
            avg_confidence /= detection_count

        # Display status information
        status_text = f"{'Video' if self.is_video_mode else 'Stream'} | FPS: {self.fps_estimate:.1f}"
        cv2.putText(annotated_frame, status_text, (20, 20), 
                cv2.FONT_HERSHEY_SIMPLEX, 0.5, (0, 255, 0), 1)
        
        # Display detection information
        detection_text = f"Detections: {detection_count}"
        cv2.putText(annotated_frame, detection_text, (20, 40), 
                cv2.FONT_HERSHEY_SIMPLEX, 0.5, (0, 255, 0), 1)
        
        vehicle_types_text = f"Types: {', '.join(vehicle_types) if vehicle_types else 'None'}"
        cv2.putText(annotated_frame, vehicle_types_text, (20, 60), 
                cv2.FONT_HERSHEY_SIMPLEX, 0.5, (0, 255, 0), 1)
        
        confidence_text = f"Avg Confidence: {avg_confidence:.2f}" if detection_count > 0 else "No detections"
        cv2.putText(annotated_frame, confidence_text, (20, 80), 
                cv2.FONT_HERSHEY_SIMPLEX, 0.5, (0, 255, 0), 1)

        # Display recording status
        if detection_count > 0 and detection_info.get('plate_text') != 'NO_PLATE':
            recording_status = "Recording Active"
        else:
            recording_status = "Not Recording"
        
        cv2.putText(annotated_frame, recording_status, (20, 100), 
                cv2.FONT_HERSHEY_SIMPLEX, 0.5, (0, 255, 0), 1)

        if self.is_video_mode and self.video_processor:
            progress = self.video_processor.get_progress()
            progress_text = f"Progress: {progress['progress_percent']:.1f}%"
            cv2.putText(annotated_frame, progress_text, (20, 120), 
                    cv2.FONT_HERSHEY_SIMPLEX, 0.5, (255, 255, 0), 1)

        return annotated_frame

    def alert_vehicle(self, vehicle):
        """Send alert for vehicle detection."""
        alert_data = {
            "timestamp": datetime.now().isoformat(),
            "eventType": "VEHICLE_DETECTION",
            "vehicle_type": vehicle['type'],
            "vehicle_color": vehicle['color'],
            "license_plate": vehicle.get('plate_text', 'NO_PLATE'),  # Include license plate if available
            "video_path": self.video_filename,
            "image_path": self.img_filename
        }
        # self.rb_producer.send_to_rabbitmq(alert_data)
        # print(f"Alert sent for vehicle detection: {vehicle['type']} - Color: {vehicle['color']} - Plate: {alert_data['license_plate']}")

    def format_data(self, detection_type,video_path,image_path):
        """Format detection data for upload."""
        try:
            with open(video_path, "rb") as f:
                video_file = base64.b64encode(f.read()).decode('utf-8')
            with open(image_path, "rb") as f:
                image_file = base64.b64encode(f.read()).decode('utf-8')
            
            alert_data = {
                "timestamp": datetime.now().isoformat(),
                "deviceSN": "ATPL-911622-AIPTZ",
                "eventType": detection_type,
                "video_file": video_file,
                "image_file": image_file,
                "video_path": self.video_filename,
                "image_path": self.img_filename,
                "camera_details": getattr(self, 'camera_details', {}),
                "severity": getattr(self, 'camera_details', {}).get("severity", "medium")
            }
            print("Warning: format_data is deprecated. Use send_complete_alert instead.")
            detection_info = {'type': detection_type, 'color': 'Unknown', 'confidence': 1.0}
            # self.send_complete_alert(detection_info, video_path, image_path)
            
            # self.rb_producer.send_to_rabbitmq(alert_data)
            # print(f"Alert sent successfully for {detection_type}")
            
        except Exception as e:
            print(f"Failed to send alert for {detection_type}: {e}")

    def is_alert_allowed(self, detection_info, current_time):
        """Check if alert is allowed based on cooldown."""
        alert_type = detection_info['type']
        last_alert = self.last_alert_times.get(alert_type, 0)
        return (current_time - last_alert) >= self.alert_cooldown

    def read_frame(self):
        """Read a frame from the current input source (video or stream)."""
        if self.is_video_mode:
            if self.video_processor:
                ret, frame = self.video_processor.read()
                if ret:
                    return frame
                else:
                    print("End of video file reached")
                    return None
            return None
        else:
            if self.stream:
                frame = self.stream.read()
                if frame is None:
                    print("No frame received from stream")
                return frame
            return None


    def process_video_or_stream(self):
        """Main processing loop for video or stream input."""
        cv2.namedWindow('Vehicle Detection', cv2.WINDOW_NORMAL)
        cv2.resizeWindow('Vehicle Detection', 1280, 720)
        
        try:
            while self.running:
                current_time = time.time()
                
                # Frame rate control
                if self.target_frame_interval > 0:
                    time_since_last_process = current_time - self.last_process_time
                    if time_since_last_process < self.target_frame_interval:
                        sleep_time = self.target_frame_interval - time_since_last_process
                        if sleep_time > 0:  # Only sleep if the time is positive
                            time.sleep(sleep_time)
                        continue
                
                # Read frame
                frame = self.read_frame()
                if frame is None:
                    if self.is_video_mode:
                        print("End of video reached")
                        break
                    time.sleep(0.01)
                    continue

                # Process frame using asyncio
                annotated_frame = asyncio.run(self.process_frame(frame))
                self.last_process_time = current_time
                
                # Display frame
                cv2.imshow('Vehicle Detection', annotated_frame)
                
                # Handle key presses
                key = cv2.waitKey(1) & 0xFF
                if key == ord('q'):
                    break
                elif key == ord('p') and self.is_video_mode:
                    print("Video paused. Press any key to continue...")
                    cv2.waitKey(0)

        except Exception as e:
            print(f"Critical error in processing loop: {e}")
        finally:
            self.cleanup()

    def run(self):
        """Main processing loop."""
        cv2.namedWindow('Vehicle Detection', cv2.WINDOW_NORMAL)
        cv2.resizeWindow('Vehicle Detection', 1280, 720)
        
        try:
            self.initialize_model()
            self.setup_input_source()
            
            while self.running:
                current_time = time.time()
                
                # Frame rate control
                if self.target_frame_interval > 0:
                    time_since_last = current_time - self.last_process_time
                    if time_since_last < self.target_frame_interval:
                        sleep_time = self.target_frame_interval - time_since_last
                        if sleep_time > 0:  # Only sleep if the time is positive
                            time.sleep(sleep_time)
                        continue
                
                # Read frame
                frame = self.read_frame()
                if frame is None:
                    if self.is_video_mode:
                        print("End of video reached")
                        break
                    time.sleep(0.01)
                    continue

                # Process and display frame
                annotated_frame = asyncio.run(self.process_frame(frame))
                cv2.imshow('Vehicle Detection', annotated_frame)
                self.last_process_time = time.time()
                
                # Handle key presses
                key = cv2.waitKey(1) & 0xFF
                if key == ord('q'):
                    break
                elif key == ord('p') and self.is_video_mode:
                    print("Video paused. Press any key to continue...")
                    cv2.waitKey(0)

        except Exception as e:
            print(f"Error in processing loop: {e}")
        finally:
            self.cleanup()
    
    def start_recording(self, detection_info, trigger_time, image_path=None):
        """Start recording video for detected vehicle with proper naming and pre-alert buffer."""
        vehicle_type = detection_info.get('type', 'Unknown')
        vehicle_color = detection_info.get('color', 'Unknown')
        plate_text = detection_info.get('plate_text', 'NO_PLATE')
        
        # Clean plate text for filename
        clean_plate = re.sub(r'[^A-Z0-9]', '', plate_text.upper())
        if not clean_plate:
            clean_plate = "NO_PLATE"

        # Create descriptive key for this detection
        detection_key = f"{vehicle_type}_{vehicle_color}_{clean_plate}"
        
        # Skip if already recording
        if detection_key in self.video_writers:
            return

        # Create timestamped filename
        timestamp_str = datetime.fromtimestamp(trigger_time).strftime("%Y%m%d_%H%M%S")
        video_filename = f"detection_{detection_key}_{timestamp_str}.mp4"
        video_path = os.path.abspath(os.path.join(self.videos_folder, video_filename))

        # Debugging output
        print(f"Video Path: {video_path}")
        print(f"Frame Size: {self.frame_width}x{self.frame_height}, FPS: {self.user_fps}")

        # Ensure video directory exists
        os.makedirs(os.path.dirname(video_path), exist_ok=True)

        # Try multiple GStreamer pipelines in order of preference
        gstreamer_pipelines = [
            # Option 1: Hardware accelerated H.264 with AVC1 (best quality)
            f"appsrc ! videoconvert ! video/x-raw,format=I420 ! x264enc tune=zerolatency speed-preset=ultrafast bitrate=2000 ! h264parse ! mp4mux ! filesink location={video_path}",
            
        ]

        video_writer = None
        successful_pipeline = None
        
        # Try each pipeline until one works
        for i, pipeline in enumerate(gstreamer_pipelines):
            try:
                print(f"Trying GStreamer pipeline {i+1}: {pipeline}")
                video_writer = cv2.VideoWriter(pipeline, cv2.CAP_GSTREAMER, 0, self.user_fps, 
                                            (self.frame_width, self.frame_height))
                
                if video_writer.isOpened():
                    successful_pipeline = pipeline
                    print(f"Successfully opened video writer with pipeline {i+1}")
                    break
                else:
                    print(f"Pipeline {i+1} failed to open")
                    video_writer.release()
                    video_writer = None
            except Exception as e:
                print(f"Pipeline {i+1} error: {e}")
                if video_writer:
                    video_writer.release()
                    video_writer = None

        # If all GStreamer pipelines fail, try OpenCV's built-in codec
        if video_writer is None:
            print("All GStreamer pipelines failed, trying OpenCV built-in codec...")
            try:
                # Try H.264 codec first
                fourcc = cv2.VideoWriter_fourcc(*'avc1')  # H.264 AVC1
                video_writer = cv2.VideoWriter(video_path, fourcc, self.user_fps, 
                                            (self.frame_width, self.frame_height))
                
                if not video_writer.isOpened():
                    # Fallback to MJPEG
                    fourcc = cv2.VideoWriter_fourcc(*'MJPG')
                    video_path = video_path.replace('.mp4', '.avi')
                    video_writer = cv2.VideoWriter(video_path, fourcc, self.user_fps, 
                                                (self.frame_width, self.frame_height))
                    
                if video_writer.isOpened():
                    print(f"Successfully opened video writer with OpenCV codec")
                    successful_pipeline = f"OpenCV codec: {fourcc}"
                else:
                    print("Failed to open video writer with OpenCV codecs")
                    video_writer.release()
                    video_writer = None
                    
            except Exception as e:
                print(f"OpenCV codec error: {e}")
                if video_writer:
                    video_writer.release()
                    video_writer = None

        if video_writer is None:
            print(f"Failed to initialize video writer for {video_path}")
            return

        # Store recording information
        self.video_writers[detection_key] = video_writer
        self.recording_videos[detection_key] = {
            'path': video_path,
            'trigger_time': trigger_time,
            'frames_written': 0,
            'pre_alert_frames_processed': [],
            'detection_info': detection_info,
            'image_path': image_path,
            'start_time': time.time(),
            'pipeline': successful_pipeline
        }

        # Process pre-alert frames (from buffer)
        pre_alert_start_time = trigger_time - self.pre_alert_seconds
        pre_alert_frames = [
            (ts, frame) for ts, frame in self.raw_frame_buffer
            if pre_alert_start_time <= ts <= trigger_time
        ]

        # Write pre-alert frames to video
        for ts, raw_frame in pre_alert_frames:
            processed_frame = self.process_frame_for_recording(raw_frame)
            if processed_frame is not None:
                try:
                    # Ensure frame is the correct size
                    if processed_frame.shape[:2] != (self.frame_height, self.frame_width):
                        processed_frame = cv2.resize(processed_frame, (self.frame_width, self.frame_height))
                    
                    video_writer.write(processed_frame)
                    self.recording_videos[detection_key]['frames_written'] += 1
                except Exception as e:
                    print(f"Error writing pre-alert frame: {e}")
                    self.stop_recording(detection_key)
                    break

        print(f"Started recording: {video_path} using {successful_pipeline}")



    def update_recordings(self, current_annotated_frame, current_time):
        """Update all active video recordings with frame writing and duration checks."""
        for detection_key in list(self.video_writers.keys()):
            recording_info = self.recording_videos.get(detection_key)
            if not recording_info:
                continue

            writer = self.video_writers[detection_key]
            elapsed_time = current_time - recording_info['start_time']
            max_duration = self.pre_alert_seconds + self.post_alert_seconds

            # Stop recording if max duration reached
            if elapsed_time >= max_duration:
                self.stop_recording(detection_key)
                continue

            # Write current frame to video
            if current_annotated_frame is not None:
                try:
                    # Ensure frame is the correct size
                    if current_annotated_frame.shape[:2] != (self.frame_height, self.frame_width):
                        current_annotated_frame = cv2.resize(current_annotated_frame, (self.frame_width, self.frame_height))
                    
                    writer.write(current_annotated_frame)
                    recording_info['frames_written'] += 1
                except Exception as e:
                    print(f"Error writing video frame: {e}")
                    self.stop_recording(detection_key)



    def stop_recording(self, detection_key):
        """Stop recording and clean up resources."""
        if detection_key not in self.video_writers:
            return

        writer = self.video_writers[detection_key]
        recording_info = self.recording_videos.get(detection_key, {})
        
        try:
            writer.release()
            duration = recording_info['frames_written'] / self.user_fps
            print(f"Stopped recording {detection_key}")
            print(f"Frames: {recording_info['frames_written']}, Duration: {duration:.2f}s")
            
            # Send complete alert if needed
            if 'image_path' in recording_info and os.path.exists(recording_info['image_path']):
                self.send_complete_alert(recording_info['detection_info'], 
                                    recording_info['path'], 
                                    recording_info['image_path'])
        except Exception as e:
            print(f"Error stopping recording: {e}")
        finally:
            # Clean up
            if detection_key in self.video_writers:
                del self.video_writers[detection_key]
            if detection_key in self.recording_videos:
                del self.recording_videos[detection_key]

    
    def send_complete_alert(self, detection_info, video_path, image_path):
        """Send alert with complete video and image data."""
        try:
            # Verify both files exist and are readable
            if not os.path.exists(video_path):
                print(f"Video file not found: {video_path}")
                return
            
            if not os.path.exists(image_path):
                print(f"Image file not found: {image_path}")
                return
            
            # Read and encode video file
            with open(video_path, "rb") as f:
                video_file = base64.b64encode(f.read()).decode('utf-8')
            
            # Read and encode image file
            with open(image_path, "rb") as f:
                image_file = base64.b64encode(f.read()).decode('utf-8')
            
            # Create alert data

            #print("image_path", image_path)
            # print('image_file', image_file)
            alert_data = {
                "timestamp": datetime.now().isoformat(),
                "deviceSN": "ATPL-911622-AIPTZ",
                "eventType": "VEHICLE_DETECTION",
                "vehicle_type": detection_info['type'],
                "vehicle_color": detection_info['color'],
                "license_plate": detection_info.get('plate_text', 'NO_PLATE'),
                "confidence": detection_info['confidence'],
                "video_file": video_file,
                "image_file": image_file,
                "video_path": os.path.basename(video_path),
                "image_path": os.path.basename(image_path),
                "camera_details": getattr(self, 'camera_details', {}),
                "severity": getattr(self, 'camera_details', {}).get("severity", "medium")
            }
            
            # Send to RabbitMQ
            # self.rb_producer.send_to_rabbitmq(alert_data)
            # print(f"Complete alert sent successfully for {detection_info['type']} - {detection_info['color']} - {detection_info.get('plate_text', 'NO_PLATE')}")
            
        except Exception as e:
            print(f"Failed to send complete alert: {e}")

    def process_frame_for_recording(self, frame):
        """Prepare frame for recording with all annotations."""
        recorded_frame = frame.copy()
        recorded_frame = self.draw_roi(recorded_frame)  # Draw ROI if applicable
        return recorded_frame

    def cleanup(self):
        """Clean up all resources."""
        print("Cleaning up resources...")
        
        # Stop all active recordings
        for detection_name in list(self.video_writers.keys()):
            self.stop_recording(detection_name)
        
        # Release video sources
        if self.stream:
            self.stream.release()
        if self.video_processor:
            self.video_processor.release()
        
        # Close RabbitMQ connection
        if hasattr(self, 'rb_producer') and self.rb_producer:
            try:
                # Check if the producer has a close method, if not use disconnect
                if hasattr(self.rb_producer, 'close'):
                    self.rb_producer.close()
                elif hasattr(self.rb_producer, 'disconnect'):
                    self.rb_producer.disconnect()
                else:
                    print("RabbitMQ producer doesn't have close or disconnect method")
            except Exception as e:
                print(f"Error closing RabbitMQ connection: {e}")
        
        # Close all OpenCV windows
        cv2.destroyAllWindows()
        print("Cleanup completed")
    
    def check_gstreamer_support(self):
        """Check if GStreamer is available and working."""
        try:
            # Test GStreamer availability
            test_pipeline = "videotestsrc num-buffers=1 ! videoconvert ! x264enc ! fakesink"
            test_writer = cv2.VideoWriter(test_pipeline, cv2.CAP_GSTREAMER, 0, 1, (640, 480))
            
            if test_writer.isOpened():
                test_writer.release()
                print("GStreamer support: Available")
                return True
            else:
                print("GStreamer support: Not available")
                return False
        except Exception as e:
            print(f"GStreamer support check failed: {e}")
            return False


    def stop(self):
        """Stop the detection system gracefully."""
        print("Stopping detection system...")
        self.running = False
        self.cleanup()

def main():
    config = {
        'model_path': 'yolo12n.pt',
        'input_source': "/home/pi4/vehicle_demographics/vehicles1.mp4",
        #'input_source' : '/Users/algofusion/Downloads/vehicles1.mp4',
        'save_folder' : 'vehicle_detections',
        'confidence_threshold': 0.5,
        'nms_threshold': 0.45,
        'image_size': 640,
        'user_fps': 4,
        'video_target_fps': None,
        'pre_alert_seconds': 10,
        'post_alert_seconds': 20,
        # 'roi': {'x1': 27, 'y1': 150, 'x2': 561, 'y2': 904},
        #'roi': {'x1': 5, 'y1': 414, 'x2': 709, 'y2': 1132},
        'roi': {'x1': 2301, 'y1': 1554, 'x2': 2928, 'y2': 1797},
        'show_roi': True
    }

    detector = VehicleDemographicsDetector(data=config)
    
    try:
        detector.run()
    except KeyboardInterrupt:
        print("\nShutting down...")
    finally:
        detector.stop()

if __name__ == "__main__":
    main()




'''gstreamer_pipelines = [
    # Delta (inter-frame compression)
    f"appsrc ! videoconvert ! video/x-raw,format=I420 ! x264enc key-int-max=96 bframes=4 tune=zerolatency speed-preset=ultrafast bitrate=2000 ! h264parse ! mp4mux ! filesink location={video_path_delta}",

    # Full-frame (no delta)
    f"appsrc ! videoconvert ! video/x-raw,format=I420 ! x264enc key-int-max=1 bframes=0 tune=zerolatency speed-preset=ultrafast bitrate=2000 ! h264parse ! mp4mux ! filesink location={video_path_full}",
]'''