#!/usr/bin/env python3
"""
Generates the Definitive Master Project Documentation (.docx) for the
Industrial Edge AI Vision & RTSP Gateway Platform.
Comprehensive, all-in-one document for any stakeholder, engineer, or evaluator.
"""

import os
import sys
import zipfile
import html
import shutil

def escape_xml(s):
    if s is None:
        return ""
    return html.escape(str(s))

class DocxBuilder:
    def __init__(self, template_docx_path):
        self.template_path = template_docx_path
        self.paragraphs = []
        
    def add_p(self, text, style="Normal", bold_prefix="", space_before=0, space_after=120, line_spacing=276, color=None, is_bold=False, is_italic=False, align="left"):
        run_xml = []
        if bold_prefix:
            run_xml.append(f'''<w:r><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/><w:b/><w:color w:val="1F2937"/><w:sz w:val="21"/></w:rPr><w:t xml:space="preserve">{escape_xml(bold_prefix)}</w:t></w:r>''')
        
        t_content = escape_xml(text)
        r_pr = ['<w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/>']
        if is_bold:
            r_pr.append('<w:b/>')
        if is_italic:
            r_pr.append('<w:i/>')
        if color:
            r_pr.append(f'<w:color w:val="{color}"/>')
        else:
            r_pr.append('<w:color w:val="374151"/>')
        r_pr.append('<w:sz w:val="21"/>')
        
        run_xml.append(f'''<w:r><w:rPr>{''.join(r_pr)}</w:rPr><w:t xml:space="preserve">{t_content}</w:t></w:r>''')
        
        jc_xml = f'<w:jc w:val="{align}"/>' if align != "left" else ""
        p_xml = f'''<w:p>
            <w:pPr>
                <w:pStyle w:val="{style}"/>
                <w:spacing w:before="{space_before}" w:after="{space_after}" w:line="{line_spacing}" w:lineRule="auto"/>
                {jc_xml}
            </w:pPr>
            {''.join(run_xml)}
        </w:p>'''
        self.paragraphs.append(p_xml)

    def add_heading_1(self, text):
        t_content = escape_xml(text)
        p_xml = f'''<w:p>
            <w:pPr>
                <w:pStyle w:val="Heading1"/>
                <w:spacing w:before="360" w:after="120"/>
                <w:keepNext/>
            </w:pPr>
            <w:r>
                <w:rPr>
                    <w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/>
                    <w:b/>
                    <w:color w:val="1B365D"/>
                    <w:sz w:val="32"/>
                </w:rPr>
                <w:t>{t_content}</w:t>
            </w:r>
        </w:p>'''
        self.paragraphs.append(p_xml)

    def add_heading_2(self, text):
        t_content = escape_xml(text)
        p_xml = f'''<w:p>
            <w:pPr>
                <w:pStyle w:val="Heading2"/>
                <w:spacing w:before="240" w:after="80"/>
                <w:keepNext/>
            </w:pPr>
            <w:r>
                <w:rPr>
                    <w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/>
                    <w:b/>
                    <w:color w:val="0D9488"/>
                    <w:sz w:val="26"/>
                </w:rPr>
                <w:t>{t_content}</w:t>
            </w:r>
        </w:p>'''
        self.paragraphs.append(p_xml)

    def add_heading_3(self, text):
        t_content = escape_xml(text)
        p_xml = f'''<w:p>
            <w:pPr>
                <w:pStyle w:val="Heading3"/>
                <w:spacing w:before="160" w:after="60"/>
                <w:keepNext/>
            </w:pPr>
            <w:r>
                <w:rPr>
                    <w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/>
                    <w:b/>
                    <w:color w:val="374151"/>
                    <w:sz w:val="22"/>
                </w:rPr>
                <w:t>{t_content}</w:t>
            </w:r>
        </w:p>'''
        self.paragraphs.append(p_xml)

    def add_bullet(self, text, bold_prefix=""):
        run_xml = []
        if bold_prefix:
            run_xml.append(f'''<w:r><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/><w:b/><w:color w:val="1F2937"/><w:sz w:val="21"/></w:rPr><w:t xml:space="preserve">{escape_xml(bold_prefix)}</w:t></w:r>''')
        t_content = escape_xml(text)
        run_xml.append(f'''<w:r><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/><w:color w:val="374151"/><w:sz w:val="21"/></w:rPr><w:t xml:space="preserve">{t_content}</w:t></w:r>''')
        
        p_xml = f'''<w:p>
            <w:pPr>
                <w:pStyle w:val="Normal"/>
                <w:numPr>
                    <w:ilvl w:val="0"/>
                    <w:numId w:val="1"/>
                </w:numPr>
                <w:spacing w:before="40" w:after="80" w:line="260" w:lineRule="auto"/>
            </w:pPr>
            {''.join(run_xml)}
        </w:p>'''
        self.paragraphs.append(p_xml)

    def add_callout(self, title, body, border_color="1B365D", bg_color="F0F4F8"):
        t_title = escape_xml(title)
        t_body = escape_xml(body)
        tbl_xml = f'''<w:tbl>
            <w:tblPr>
                <w:tblW w:w="9360" w:type="dxa"/>
                <w:jc w:val="center"/>
                <w:tblBorders>
                    <w:top w:val="none"/>
                    <w:left w:val="single" w:sz="24" w:space="0" w:color="{border_color}"/>
                    <w:bottom w:val="none"/>
                    <w:right w:val="none"/>
                    <w:insideH w:val="none"/>
                    <w:insideV w:val="none"/>
                </w:tblBorders>
            </w:tblPr>
            <w:tblGrid><w:gridCol w:w="9360"/></w:tblGrid>
            <w:tr>
                <w:tc>
                    <w:tcPr>
                        <w:tcW w:w="9360" w:type="dxa"/>
                        <w:shd w:val="clear" w:color="auto" w:fill="{bg_color}"/>
                        <w:tcMar>
                            <w:top w:w="160" w:type="dxa"/>
                            <w:bottom w:w="160" w:type="dxa"/>
                            <w:left w:w="240" w:type="dxa"/>
                            <w:right w:w="240" w:type="dxa"/>
                        </w:tcMar>
                    </w:tcPr>
                    <w:p>
                        <w:pPr><w:spacing w:before="40" w:after="60"/></w:pPr>
                        <w:r>
                            <w:rPr>
                                <w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/>
                                <w:b/><w:color w:val="{border_color}"/><w:sz w:val="22"/>
                            </w:rPr>
                            <w:t>{t_title}</w:t>
                        </w:r>
                    </w:p>
                    <w:p>
                        <w:pPr><w:spacing w:before="0" w:after="40" w:line="260" w:lineRule="auto"/></w:pPr>
                        <w:r>
                            <w:rPr>
                                <w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/>
                                <w:color w:val="2D3748"/><w:sz w:val="20"/>
                            </w:rPr>
                            <w:t>{t_body}</w:t>
                        </w:r>
                    </w:p>
                </w:tc>
            </w:tr>
        </w:tbl>
        <w:p><w:pPr><w:spacing w:before="0" w:after="80"/></w:pPr></w:p>'''
        self.paragraphs.append(tbl_xml)

    def add_table(self, headers, rows, col_widths=None):
        num_cols = len(headers)
        if not col_widths:
            equal_w = int(9360 / num_cols)
            col_widths = [equal_w] * num_cols
            
        grid_xml = "".join([f'<w:gridCol w:w="{w}"/>' for w in col_widths])
        
        hdr_cells = []
        for i, h in enumerate(headers):
            hdr_cells.append(f'''<w:tc>
                <w:tcPr>
                    <w:tcW w:w="{col_widths[i]}" w:type="dxa"/>
                    <w:shd w:val="clear" w:color="auto" w:fill="1B365D"/>
                    <w:tcMar>
                        <w:top w:w="120" w:type="dxa"/><w:bottom w:w="120" w:type="dxa"/>
                        <w:left w:w="160" w:type="dxa"/><w:right w:w="160" w:type="dxa"/>
                    </w:tcMar>
                </w:tcPr>
                <w:p>
                    <w:pPr><w:spacing w:before="40" w:after="40"/></w:pPr>
                    <w:r>
                        <w:rPr>
                            <w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/>
                            <w:b/><w:color w:val="FFFFFF"/><w:sz w:val="19"/>
                        </w:rPr>
                        <w:t>{escape_xml(h)}</w:t>
                    </w:r>
                </w:p>
            </w:tc>''')
            
        rows_xml = [f'<w:tr><w:trPr><w:tblHeader/></w:trPr>{"".join(hdr_cells)}</w:tr>']
        
        for r_idx, row in enumerate(rows):
            bg_col = "F8FAFC" if (r_idx % 2 == 1) else "FFFFFF"
            b_cells = []
            for i, val in enumerate(row):
                b_cells.append(f'''<w:tc>
                    <w:tcPr>
                        <w:tcW w:w="{col_widths[i]}" w:type="dxa"/>
                        <w:shd w:val="clear" w:color="auto" w:fill="{bg_col}"/>
                        <w:tcMar>
                            <w:top w:w="100" w:type="dxa"/><w:bottom w:w="100" w:type="dxa"/>
                            <w:left w:w="160" w:type="dxa"/><w:right w:w="160" w:type="dxa"/>
                        </w:tcMar>
                    </w:tcPr>
                    <w:p>
                        <w:pPr><w:spacing w:before="20" w:after="20" w:line="240" w:lineRule="auto"/></w:pPr>
                        <w:r>
                            <w:rPr>
                                <w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/>
                                <w:color w:val="334155"/><w:sz w:val="18"/>
                            </w:rPr>
                            <w:t>{escape_xml(val)}</w:t>
                        </w:r>
                    </w:p>
                </w:tc>''')
            rows_xml.append(f'<w:tr>{"".join(b_cells)}</w:tr>')
            
        tbl_xml = f'''<w:tbl>
            <w:tblPr>
                <w:tblW w:w="9360" w:type="dxa"/><w:jc w:val="center"/>
                <w:tblBorders>
                    <w:top w:val="single" w:sz="4" w:space="0" w:color="CBD5E1"/>
                    <w:left w:val="single" w:sz="4" w:space="0" w:color="CBD5E1"/>
                    <w:bottom w:val="single" w:sz="4" w:space="0" w:color="CBD5E1"/>
                    <w:right w:val="single" w:sz="4" w:space="0" w:color="CBD5E1"/>
                    <w:insideH w:val="single" w:sz="4" w:space="0" w:color="E2E8F0"/>
                    <w:insideV w:val="single" w:sz="4" w:space="0" w:color="E2E8F0"/>
                </w:tblBorders>
            </w:tblPr>
            <w:tblGrid>{grid_xml}</w:tblGrid>
            {''.join(rows_xml)}
        </w:tbl>
        <w:p><w:pPr><w:spacing w:before="0" w:after="120"/></w:pPr></w:p>'''
        self.paragraphs.append(tbl_xml)

    def save(self, output_path):
        with zipfile.ZipFile(self.template_path, 'r') as zin:
            content = dict((item.filename, zin.read(item.filename)) for item in zin.infolist())

        doc_xml = content['word/document.xml'].decode('utf-8')
        body_start = doc_xml.find('<w:body>') + len('<w:body>')
        body_end = doc_xml.rfind('<w:sectPr')
        
        sect_pr = doc_xml[body_end:]
        new_doc_xml = doc_xml[:body_start] + "".join(self.paragraphs) + sect_pr

        content['word/document.xml'] = new_doc_xml.encode('utf-8')

        os.makedirs(os.path.dirname(os.path.abspath(output_path)), exist_ok=True)
        with zipfile.ZipFile(output_path, 'w', zipfile.ZIP_DEFLATED) as zout:
            for filename, data in content.items():
                zout.writestr(filename, data)
        print(f"Master project document successfully written to: {output_path}")

def build_project_master_doc(output_path):
    template = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "Edge_AI_Vision_System_Production_Architecture.docx")
    doc = DocxBuilder(template)

    # Title & Header
    doc.add_p("RELIANCE AI VISION - INDUSTRIAL EDGE INFERENCE PLATFORM", style="Title", is_bold=True, color="1B365D", align="center", space_before=100, space_after=60)
    doc.add_p("Comprehensive Master Project Specification, Operational Capabilities & Technical Manual", style="Subtitle", color="0D9488", align="center", space_before=0, space_after=180)

    # Executive Project Snapshot
    doc.add_table(
        ["Project Metadata", "Enterprise Specification Details"],
        [
            ["Project Title", "Reliance AI Vision - Industrial Edge Inference Platform & RTSP Gateway"],
            ["Deployment Target", "Industrial Edge Microcomputers (Raspberry Pi 4/5 ARM64) & Linux Gateways"],
            ["Industry Domain", "Petroleum Refineries, Tank Truck (TT) Gantries, Hazardous Industrial Facilities"],
            ["Core Technologies", "Python 3.9+, PyTorch 2.x, Ultralytics YOLOv8, OpenCV, ByteTrack, FastANPR, FastAPI"],
            ["Visual Ingestion & Streaming", "Multi-Camera RTSP/ONVIF Ingestion -> Low-Latency HLS (10 FPS Baseline H.264 in RAM)"],
            ["AI & Computer Vision", "YOLOv8 Detection, Biomechanical Pose (17 Keypoints), License Plate OCR, Speed Tracking"],
            ["Storage & Safety Design", "Zero Flash Memory Wear (Linux RAM tmpfs Ring Buffer) + PostgreSQL Event-Driven Snapshots"],
            ["Network Architecture", "Zero-Trust Encrypted WireGuard Mesh (Tailscale Funnel) - No Public Router Ports Opened"]
        ],
        [3200, 6160]
    )

    doc.add_callout(
        "PROJECT MISSION & PHILOSOPHY",
        "To provide autonomous, continuous, 24/7 computer vision safety enforcement and logistical monitoring directly at the edge in hazardous industrial facilities. The platform operates 100% locally on low-cost hardware without sending high-bandwidth video streams to the cloud, eliminating expensive cloud egress fees, network outage vulnerabilities, and video streaming delays."
    )

    # -------------------------------------------------------------
    # 1. WHAT IS THIS PROJECT & PROBLEM IT SOLVES
    # -------------------------------------------------------------
    doc.add_heading_1("1. What is this Project & The Industrial Problem It Solves")
    doc.add_p("The Reliance AI Vision Edge Platform is a complete, production-ready edge intelligence appliance. It transforms standard, passive industrial CCTV and RTSP security cameras into proactive, real-time safety and regulatory enforcement sentinels.")
    
    doc.add_heading_2("1.1 The Industrial Challenge")
    doc.add_p("In heavy industrial environments such as oil refineries, petrochemical distribution terminals, and tank truck loading gantries, safety violations can lead to catastrophic explosions, toxic leaks, and severe personnel injuries. Traditional video monitoring fails for three reasons:")
    doc.add_bullet("Human Operator Fatigue: A security guard watching 16 camera tiles on a wall monitor misses 95% of violations after just 20 minutes of continuous viewing.", bold_prefix="1. Human Inefficiency: ")
    doc.add_bullet("Cloud Video Streaming Prohibitions: Streaming dozens of 1080p camera streams to cloud servers requires hundreds of megabits per second of continuous bandwidth. In remote oil depots, internet connections are metered, fragile, or restricted by cybersecurity air-gap policies.", bold_prefix="2. Cloud Latency & Bandwidth: ")
    doc.add_bullet("Post-Incident Forensics vs Real-Time Prevention: Conventional CCTV records footage to a DVR that is only reviewed after an accident has already occurred, offering zero real-time intervention.", bold_prefix="3. Reactive Monitoring: ")

    doc.add_heading_2("1.2 The Edge AI Solution")
    doc.add_p("This platform runs deep neural networks directly on edge microcomputers (Raspberry Pi 4 / 5) installed on-premise at the loading gantry. It inspects video frames in real time, verifies compliance against safety standard operating procedures (SOPs), tracks vehicle movement, measures speed, reads license plates, and triggers instant alerts and evidence snapshots the moment a violation occurs.")

    # -------------------------------------------------------------
    # 2. COMPLETE CAPABILITIES & INDUSTRIAL USE CASES
    # -------------------------------------------------------------
    doc.add_heading_1("2. Core Platform Capabilities & Industrial Use Cases")
    doc.add_p("The platform incorporates an extensive suite of specialized computer vision models and algorithmic verification engines:")

    doc.add_heading_2("2.1 Personal Protective Equipment (PPE) Compliance")
    doc.add_p("Enforces mandatory safety gear for all personnel entering hazardous operational zones:")
    doc.add_bullet("Monitored Items: Hard hats (helmets), high-visibility safety vests, protective safety glasses, industrial gloves, and steel-toe safety footwear.", bold_prefix="Safety Gear: ")
    doc.add_bullet("Dual-Distance Adaptive Zoom: Uses a 2-stage neural architecture. When a worker is detected far away (10 to 15 meters), the system crops the worker at native resolution and runs companion PPE models (hansung_ppe_violations.pt, ppe_new.pt) directly on the crop, ensuring reliable detection across both near and far distances.", bold_prefix="Distant Worker Inspection: ")

    doc.add_heading_2("2.2 Fire Safety & Equipment Absence Detection")
    doc.add_p("Automates physical inventory verification of required fire suppression equipment in high-risk chemical zones:")
    doc.add_bullet("Wheel Chocks (sand_ext_chocks.pt): Verifies that physical rubber chocks are deployed under vehicle tires before loading begins.", bold_prefix="Wheel Chock Deployment: ")
    doc.add_bullet("Fire Extinguishers & Fire Hose Boxes (firehose.pt): Continuously confirms that emergency fire extinguishers and hose reels are physically present in their designated wall brackets.", bold_prefix="Fire Suppression Verification: ")
    doc.add_bullet("Spark Arrestors (spark_arrestor.pt): Detects whether vehicle exhaust pipes have certified spark arrestors installed to prevent flammable gas ignition.", bold_prefix="Spark Arrestor Compliance: ")
    doc.add_bullet("Absence Dwell Gating: If required equipment is missing from the bay for >= 5 continuous seconds while a vehicle is present, an automated alert fires.", bold_prefix="Automated Absence Alert: ")

    doc.add_heading_2("2.3 Tank Truck (TT) Logistics & Safety Operations")
    doc.add_bullet("Tank Truck Branding (tt_branding.pt): Verifies official company branding and compliance decals on incoming tankers.", bold_prefix="Branding Verification: ")
    doc.add_bullet("Crash Guard Compliance (crash_guard.pt): Inspects structural side and rear crash barriers on fuel transport tankers.", bold_prefix="Structural Guard Check: ")
    doc.add_bullet("TT Crew Non-Availability (tt_crew_non_availability.pt): Monitors driver and helper presence in the bay. If a vehicle is present but crew members abandon the vehicle unattended, an alert triggers.", bold_prefix="Crew Attendance Tracking: ")
    doc.add_bullet("TT Crew Entering Below TT (Underside Inspection): Inspects for workers crawling beneath fuel tankers. Gated by 4 geometric rejection filters to strictly distinguish workers crawling under the chassis from inspectors walking past in the foreground.", bold_prefix="Undercarriage Safety: ")

    doc.add_heading_2("2.4 Fast ANPR (Automatic Number Plate Recognition)")
    doc.add_p("Identifies and registers every vehicle entering the facility using an intelligent multi-stage OCR pipeline:")
    doc.add_bullet("Laplacian Sharpness Gate: Discards motion-blurred frames (variance < 35.0) to avoid wasted OCR computation.", bold_prefix="Image Quality Filtering: ")
    doc.add_bullet("Indian Syntax Parser (correct_indian_plate_syntax): Automatically enforces Indian registration formats ([State: 2 Alpha][District: 2 Digit][Series: 1-3 Alpha][Number: 4 Digit]), correcting optical confusions such as O <-> 0, I <-> 1, 8 <-> B, and S <-> 5.", bold_prefix="Grammar Syntax Correction: ")
    doc.add_bullet("Temporal Voting Tracker: Aggregates OCR observations across video frames in a frequency histogram, outputting verified consensus plate text via majority vote.", bold_prefix="Multi-Frame Voting: ")

    doc.add_heading_2("2.5 Behavioral Kinematics & Biomechanical Pose Estimation")
    doc.add_p("Monitors worker behavior in restricted bays using 17 COCO skeletal keypoints (yolov8n-pose.pt):")
    doc.add_bullet("Cooking Detection: Flags open flame/stove tending postures or low crouching over heat sources.", bold_prefix="Hazardous Cooking: ")
    doc.add_bullet("Smoking Detection: Identifies persistent hand-to-mouth biomechanical gestures combined with companion fire/smoke models.", bold_prefix="Combustible Smoking: ")
    doc.add_bullet("Fighting Altercations: Computes rolling wrist velocity and acceleration between close persons, detecting violent combat guards and punches.", bold_prefix="Physical Altercations: ")
    doc.add_bullet("Lying Down / Fall Detection: Calculates mathematical body orientation angle relative to the ground. If torso angle < 30 degrees and stationary on ground, triggers worker-down alert.", bold_prefix="Worker Incapacitation: ")
    doc.add_bullet("Loitering & Unauthorized Entry: Measures dwell time inside user-drawn polygon boundaries. Dwell >= 30 seconds triggers intrusion alerts.", bold_prefix="Unauthorized Dwell: ")

    doc.add_heading_2("2.6 Traffic & Speed Enforcement")
    doc.add_bullet("Vehicle Speed Tracker (LowCpuCentroidTracker): Tracks vehicles crossing two calibrated virtual lines. Speed is calculated based on pixel transit time and physical distance in km/h. Excess speed triggers instant over-speed alerts with evidence snapshots.", bold_prefix="Speed Estimation: ")
    doc.add_bullet("Wrong Parking Dwell: Measures vehicle parking duration inside restricted zones. If stationary > 30 seconds, flags Wrong Parking with vehicle plate number.", bold_prefix="Parking Enforcement: ")

    doc.add_heading_2("2.7 Crowd Gathering & Density Monitoring")
    doc.add_bullet("Spatial Proximity Clustering: Detects human heads/bodies, measures Euclidean distance between individuals, and groups clusters. If headcount inside an ROI >= threshold (e.g. 4+ persons), flags crowd gathering violation.", bold_prefix="Crowd Density: ")

    doc.add_heading_2("2.8 Physical ONVIF PTZ Camera Control")
    doc.add_bullet("Hardware Movement: Direct Pan/Tilt/Zoom continuous movement, discrete nudging, and preset position management over ONVIF Profile S/T SOAP protocols using WS-Security SHA-1 PasswordDigest authentication.", bold_prefix="PTZ Integration: ")

    doc.add_heading_2("2.9 Video Evaluation Lab (Offline Benchmark Suite)")
    doc.add_bullet("Video Lab: Allows operators to upload raw .mp4 test videos, run offline model benchmarks, interactively tune confidence/IoU sliders, and export frame-by-frame detection logs to CSV.", bold_prefix="Evaluation Suite: ")

    # -------------------------------------------------------------
    # 3. END-TO-END ARCHITECTURE & DATA FLOW
    # -------------------------------------------------------------
    doc.add_heading_1("3. System Architecture & End-to-End Data Flow")
    doc.add_p("The platform employs a decoupled, multi-threaded pipeline designed to maximize throughput on constrained ARM64 CPUs:")

    doc.add_heading_2("3.1 The 4 Concurrent Threads & Mutex Synchronizations")
    doc.add_table(
        ["Thread Name", "Function & File Location", "Primary Responsibility in System"],
        [
            ["Thread 1: Master Worker Loop", "DetectorWorker.run() in detector_worker.py", "Runs at 10 FPS. Resizes frames, draws bounding box overlays, and pipes raw BGR24 frames into FFmpeg stdin."],
            ["Thread 2: Frame Capture Thread", "DetectorWorker._capture_thread() in detector_worker.py", "Continuously drains RTSP socket via cap.read() with buffer size 1. Stores latest frame in RAM under _frame_lock."],
            ["Thread 3: Central Inference Scheduler", "InferenceScheduler._run_loop() in detector_worker.py", "Fair round-robin loop. Executes PyTorch inference across cameras sequentially. Guarantees only 1 inference runs at a time."],
            ["Thread 4: Raw Stream Monitor", "monitor_raw_streams_loop() in server.py", "Watchdog thread running every 10s. Detects camera disconnects and automatically restarts failed FFmpeg processes."]
        ],
        [2200, 3100, 4060]
    )

    doc.add_heading_2("3.2 The Complete 8-Stage Frame Lifecycle")
    doc.add_bullet("Stage 1 (RTSP Ingestion): cv2.VideoCapture connects over TCP port 554, unrolling H.264/H.265 RTP packets into uncompressed BGR NumPy matrices in RAM.", bold_prefix="1. Ingestion: ")
    doc.add_bullet("Stage 2 (Memory Buffering): _capture_thread updates self._latest_raw_frame protected by self._frame_lock, flushing driver queues to maintain zero latency.", bold_prefix="2. Buffering: ")
    doc.add_bullet("Stage 3 (Central Scheduling): InferenceScheduler dispatches worker.run_single_inference_cycle() in round-robin sequence across cameras.", bold_prefix="3. Scheduling: ")
    doc.add_bullet("Stage 4 (Neural Inference): Runs Stage 1 full-frame YOLOv8 detection, gates boxes with Ray-Casting ROI boundaries, and executes Stage 2 crop-and-zoom on distant workers for PPE.", bold_prefix="4. Detection: ")
    doc.add_bullet("Stage 5 (Tracking & Fusion): Fuses companion boxes via _apply_nms() and updates Kalman filter motion states in EdgeByteTracker.", bold_prefix="5. Tracking: ")
    doc.add_bullet("Stage 6 (Domain Rule Evaluation): Evaluates absence rules, FastANPR license plate OCR, wrong parking dwell, pose kinematics, and truck underside geometry.", bold_prefix="6. Rules: ")
    doc.add_bullet("Stage 7 (Alert Persistence): Enforces 30-second cooldown gate via check_and_record_alert_cooldown(), writes JPEG evidence snapshot to disk, and commits record to PostgreSQL.", bold_prefix="7. Alerting: ")
    doc.add_bullet("Stage 8 (HLS Streaming): Worker renders bounding boxes via _draw_boxes() and pipes bytes to FFmpeg stdin, creating a low-latency HLS stream in Linux RAM (/tmp/hls) served to browser dashboards.", bold_prefix="8. Delivery: ")

    # -------------------------------------------------------------
    # 4. HARDWARE OPTIMIZATIONS FOR EDGE LINUX / RASPBERRY PI
    # -------------------------------------------------------------
    doc.add_heading_1("4. Hardware Resource Optimizations & Production Guarantees")
    doc.add_p("To guarantee multi-year continuous operation on edge hardware, the platform enforces three production engineering principles:")
    doc.add_bullet("Frame Decoupling Strategy: Video delivery runs at 10 FPS with constrained H.264 Baseline encoding (220 kbps), while AI inference executes at ~2-3 FPS on the Central Scheduler. Exponential Moving Average (EMA) coordinate interpolation ensures fluid bounding box transitions on intermediate frames without extra neural computation.", bold_prefix="1. CPU Budgeting: ")
    doc.add_bullet("Flash Storage Endurance: Zero continuous video is committed to flash media. All live streaming chunks are confined to a 12-second circular ring buffer in Linux RAM (/tmp/hls, 6 segments x 2s), completely eliminating microSD drive wear. Physical storage is reserved exclusively for ~150 KB evidence snapshot JPEGs.", bold_prefix="2. Zero Flash Wear: ")
    doc.add_bullet("Stream Symlink Deduplication: When two camera entries share the identical RTSP URL, the system attaches a zero-overhead Linux filesystem symlink (os.symlink) to the existing stream directory, serving both cameras while consuming zero additional CPU cycles.", bold_prefix="3. Stream Symlinking: ")
    doc.add_bullet("Zero-Trust Mesh Tunneling: Public web access is established via an encrypted Tailscale WireGuard tunnel (https://raspberrypi.tail43e835.ts.net), allowing secure remote viewing without exposing firewall ports or configuring router NAT.", bold_prefix="4. Encrypted Tunneling: ")

    # -------------------------------------------------------------
    # 5. SOFTWARE DIRECTORY & CODEBASE STRUCTURE
    # -------------------------------------------------------------
    doc.add_heading_1("5. Codebase Structure & Architectural Separation of Concerns")
    doc.add_p("The project repository is structured cleanly into modular functional layers:")
    doc.add_table(
        ["File / Directory Path", "Functional Component", "Core Responsibilities in Codebase"],
        [
            ["server.py", "Root Forwarder", "Entrypoint wrapper forwarding execution to backend.server:main()."],
            ["backend/server.py", "FastAPI Application", "Master REST gateway (120+ functions). Manages streams, PTZ, models, alerts, and smart HLS playlist switching."],
            ["backend/detector/detector_worker.py", "Core Inference Worker", "Heart of the system (5,400+ lines). Multi-threaded capture, Central Scheduler, YOLOv8 inference, crop-zoom, absence, pose, and FFmpeg pipe."],
            ["backend/detector/byte_tracker.py", "Edge ByteTrack Engine", "Kalman filter multi-object tracking (KalmanFilterXYAH, STrack, EdgeByteTracker)."],
            ["backend/detector/roi_utils.py", "Geometric Utilities", "Normalizes polygon coordinates and executes Ray-Casting point-in-polygon tests."],
            ["backend/fast_anpr.py", "License Plate Engine", "Bumper ROI extraction, Laplacian sharpness test, Tesseract/EasyOCR, Indian syntax grammar parser, and voting tracker."],
            ["backend/detector/video_tester.py", "Video Evaluation Lab", "Offline benchmark suite for uploading test .mp4 videos, generating frame-by-frame overlays, and exporting CSV metrics."],
            ["backend/onvif_client.py", "ONVIF PTZ Client", "Direct SOAP/XML client with WS-Security SHA-1 PasswordDigest authentication for physical PTZ camera motion."],
            ["backend/api_manager/devices_onvif_actions.py", "Device Action Router", "APIRouter mapping directional commands (Left, Right, Zoom) to ONVIF controllers."],
            ["backend/device_manager.py", "Device Registry", "Tracks online/offline heartbeat status of edge Raspberry Pi units with 90-second TTL."],
            ["backend/alert_store.py", "Database Storage Layer", "PostgreSQL database connection pool and alert insertion with fail-safe atomic psql fallback."],
            ["crowd_tracker/Crowd.py", "Crowd Density Engine", "Head/body detection with Euclidean proximity clustering and ROI gathering limits."],
            ["vehicle_speed_tracker/speed_tracker.py", "Speed Estimation Engine", "Centroid tracking across calibrated dual lines, estimating vehicle speed in km/h."],
            ["agents/rpi_heartbeat_agent.py", "Edge Health Client", "Daemon script running on edge Pi units, reporting IP, CPU load, and RAM usage every 30s."],
            ["launch.sh", "Master Orchestration Script", "Initializes RAM tmpfs directories, configures thread concurrency limits, launches server, and starts Tailscale Funnel."]
        ],
        [2400, 2200, 4760]
    )

    # -------------------------------------------------------------
    # 6. COMPLETE REST API SPECIFICATION
    # -------------------------------------------------------------
    doc.add_heading_1("6. Complete REST API Endpoint Specification")
    doc.add_p("The backend exposes a unified, standardized RESTful interface on Port 8080:")
    doc.add_table(
        ["Method", "Endpoint Path", "Handler Function", "Operational Description"],
        [
            ["GET", "/api/streams", "get_streams()", "Fetch all configured camera streams, locations, and HLS streaming URLs."],
            ["POST", "/api/streams", "save_streams()", "Save or update camera stream configurations in streams.json."],
            ["DELETE", "/api/streams", "delete_stream()", "Permanently delete a camera stream and stop its running process."],
            ["POST", "/api/start", "start_detection()", "Launch multi-threaded DetectorWorker inference thread for a camera."],
            ["POST", "/api/stop", "stop_raw_for_camera()", "Stop AI detection worker and revert camera back to raw HLS streaming."],
            ["GET", "/api/status", "get_status()", "Query active camera IDs and their running AI models."],
            ["GET", "/hls/camera/{cid}/playlist.m3u8", "smart_hls_playlist()", "Smart HLS routing between raw and detected streams without player reconnect."],
            ["GET", "/api/models", "get_models()", "Scan models/ folder and return all available .pt YOLO weight files."],
            ["GET", "/api/model-classes", "get_model_classes_endpoint()", "Return list of class labels recognized by a specific model file."],
            ["POST", "/api/ptz/move", "ptz_move()", "Execute continuous Pan / Tilt / Zoom motion via ONVIF SOAP services."],
            ["POST", "/api/ptz/step", "ptz_step()", "Nudge PTZ camera by a discrete single-step directional angle."],
            ["POST", "/api/ptz/stop", "ptz_stop()", "Instantly halt all physical PTZ motor movements."],
            ["GET", "/api/ptz/presets", "ptz_get_presets()", "Retrieve saved ONVIF preset position tokens."],
            ["POST", "/api/ptz/presets/goto", "ptz_goto_preset()", "Move camera to a saved preset token."],
            ["GET", "/api/alerts/db", "get_alerts_db()", "Retrieve latest 50 AI violation records from PostgreSQL database."],
            ["GET", "/api/alerts/images", "get_alert_images()", "List evidence snapshot JPEG filenames stored in alerts directory."],
            ["POST", "/api/analytics/by-location", "get_analytics_by_location()", "Retrieve violation frequency metrics aggregated by facility location."],
            ["POST", "/api/analytics/by-usecase", "get_analytics_by_usecase()", "Retrieve violation frequency metrics aggregated by AI model use case."],
            ["POST", "/api/rpi-heartbeat", "rpi_heartbeat()", "Ingest edge device health metrics (IP, CPU loadavg, RAM usage)."],
            ["GET", "/api/devices", "get_devices()", "Get online/offline status list of all registered edge devices (90s TTL)."],
            ["POST", "/api/video-test/process", "process_test_video()", "Submit recorded .mp4 video to Video Lab for offline AI benchmark processing."],
            ["GET", "/api/video-test/csv/{id}", "get_video_test_csv()", "Download frame-by-frame detection metrics in CSV format."]
        ],
        [1200, 2600, 2300, 3260]
    )

    # -------------------------------------------------------------
    # 7. OPERATIONAL GUIDE: HOW TO RUN & DEPLOY
    # -------------------------------------------------------------
    doc.add_heading_1("7. Operational Manual: System Startup, Configuration & Deployment")
    doc.add_heading_2("7.1 Starting the Platform")
    doc.add_p("The platform is launched via the master orchestration script:")
    doc.add_p("./launch.sh start", style="Normal", is_bold=True)
    doc.add_p("What this script does automatically:")
    doc.add_bullet("Sets thread concurrency limits (export TORCH_NUM_THREADS=1, OPENBLAS_NUM_THREADS=1) to prevent CPU core fighting.", bold_prefix="1. Concurrency: ")
    doc.add_bullet("Creates RAM tmpfs directories for HLS video streaming (/tmp/hls) to protect the microSD card.", bold_prefix="2. RAM Disk: ")
    doc.add_bullet("Parses streams.conf and auto-generates streams.json.", bold_prefix="3. Stream Config: ")
    doc.add_bullet("Launches server.py in the background, logging to logs/server.log.", bold_prefix="4. Server Launch: ")
    doc.add_bullet("Initializes Tailscale Funnel to provide an encrypted HTTPS public URL.", bold_prefix="5. Public Access: ")

    doc.add_heading_2("7.2 Checking System Status & Stopping")
    doc.add_p("To check status of running streams, FFmpeg processes, and disk usage:")
    doc.add_p("./launch.sh status", style="Normal", is_bold=True)
    doc.add_p("To cleanly terminate all running video streams, FFmpeg pipes, and server processes:")
    doc.add_p("./launch.sh stop", style="Normal", is_bold=True)

    doc.add_heading_2("7.3 Adding New Cameras or AI Models")
    doc.add_bullet("Adding a Camera: Add the RTSP URL into streams.conf or call POST /api/streams with the camera details. The system auto-detects and begins streaming immediately.", bold_prefix="New Camera: ")
    doc.add_bullet("Adding a Model: Drop any trained YOLOv8 weights (.pt file) into the models/ directory. It is dynamically discoverable via GET /api/models with 0ms downtime.", bold_prefix="New AI Model: ")

    # Save to target paths
    doc.save(output_path)
    alt_path = os.path.join(os.path.dirname(output_path), "RELIANCE_AI_VISION_PROJECT_MANUAL.docx")
    shutil.copyfile(output_path, alt_path)
    print(f"Master project document also saved to: {alt_path}")

if __name__ == "__main__":
    base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    target = os.path.join(base_dir, "PROJECT_MASTER_DOCUMENTATION.docx")
    build_project_master_doc(target)
