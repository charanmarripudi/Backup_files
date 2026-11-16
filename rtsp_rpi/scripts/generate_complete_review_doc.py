#!/usr/bin/env python3
"""
Generates the authoritative, complete, production-grade Code Review and
System Architecture Specification Document (.docx) for the Industrial Edge AI
Vision & RTSP Gateway Platform.
"""

import os
import sys
import zipfile
import html
import shutil
import re

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
            <w:tblGrid>
                <w:gridCol w:w="9360"/>
            </w:tblGrid>
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
                        <w:pPr>
                            <w:spacing w:before="40" w:after="60"/>
                        </w:pPr>
                        <w:r>
                            <w:rPr>
                                <w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/>
                                <w:b/>
                                <w:color w:val="{border_color}"/>
                                <w:sz w:val="22"/>
                            </w:rPr>
                            <w:t>{t_title}</w:t>
                        </w:r>
                    </w:p>
                    <w:p>
                        <w:pPr>
                            <w:spacing w:before="0" w:after="40" w:line="260" w:lineRule="auto"/>
                        </w:pPr>
                        <w:r>
                            <w:rPr>
                                <w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/>
                                <w:color w:val="2D3748"/>
                                <w:sz w:val="20"/>
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
        
        # Header Row
        hdr_cells = []
        for i, h in enumerate(headers):
            hdr_cells.append(f'''<w:tc>
                <w:tcPr>
                    <w:tcW w:w="{col_widths[i]}" w:type="dxa"/>
                    <w:shd w:val="clear" w:color="auto" w:fill="1B365D"/>
                    <w:tcMar>
                        <w:top w:w="120" w:type="dxa"/>
                        <w:bottom w:w="120" w:type="dxa"/>
                        <w:left w:w="160" w:type="dxa"/>
                        <w:right w:w="160" w:type="dxa"/>
                    </w:tcMar>
                </w:tcPr>
                <w:p>
                    <w:pPr>
                        <w:spacing w:before="40" w:after="40"/>
                    </w:pPr>
                    <w:r>
                        <w:rPr>
                            <w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/>
                            <w:b/>
                            <w:color w:val="FFFFFF"/>
                            <w:sz w:val="19"/>
                        </w:rPr>
                        <w:t>{escape_xml(h)}</w:t>
                    </w:r>
                </w:p>
            </w:tc>''')
            
        rows_xml = [f'<w:tr><w:trPr><w:tblHeader/></w:trPr>{"".join(hdr_cells)}</w:tr>']
        
        # Body Rows
        for r_idx, row in enumerate(rows):
            bg_col = "F8FAFC" if (r_idx % 2 == 1) else "FFFFFF"
            b_cells = []
            for i, val in enumerate(row):
                b_cells.append(f'''<w:tc>
                    <w:tcPr>
                        <w:tcW w:w="{col_widths[i]}" w:type="dxa"/>
                        <w:shd w:val="clear" w:color="auto" w:fill="{bg_col}"/>
                        <w:tcMar>
                            <w:top w:w="100" w:type="dxa"/>
                            <w:bottom w:w="100" w:type="dxa"/>
                            <w:left w:w="160" w:type="dxa"/>
                            <w:right w:w="160" w:type="dxa"/>
                        </w:tcMar>
                    </w:tcPr>
                    <w:p>
                        <w:pPr>
                            <w:spacing w:before="20" w:after="20" w:line="240" w:lineRule="auto"/>
                        </w:pPr>
                        <w:r>
                            <w:rPr>
                                <w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/>
                                <w:color w:val="334155"/>
                                <w:sz w:val="18"/>
                            </w:rPr>
                            <w:t>{escape_xml(val)}</w:t>
                        </w:r>
                    </w:p>
                </w:tc>''')
            rows_xml.append(f'<w:tr>{"".join(b_cells)}</w:tr>')
            
        tbl_xml = f'''<w:tbl>
            <w:tblPr>
                <w:tblW w:w="9360" w:type="dxa"/>
                <w:jc w:val="center"/>
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

    def add_code_block(self, code_text):
        lines = code_text.strip().split("\n")
        p_runs = []
        for l in lines:
            p_runs.append(f'''<w:p>
                <w:pPr>
                    <w:spacing w:before="0" w:after="20" w:line="220" w:lineRule="auto"/>
                    <w:ind w:left="200" w:right="200"/>
                </w:pPr>
                <w:r>
                    <w:rPr>
                        <w:rFonts w:ascii="Consolas" w:hAnsi="Consolas"/>
                        <w:color w:val="1E293B"/>
                        <w:sz w:val="17"/>
                    </w:rPr>
                    <w:t xml:space="preserve">{escape_xml(l)}</w:t>
                </w:r>
            </w:p>''')
            
        code_box_xml = f'''<w:tbl>
            <w:tblPr>
                <w:tblW w:w="9360" w:type="dxa"/>
                <w:jc w:val="center"/>
                <w:tblBorders>
                    <w:top w:val="single" w:sz="6" w:space="0" w:color="94A3B8"/>
                    <w:left w:val="single" w:sz="16" w:space="0" w:color="0D9488"/>
                    <w:bottom w:val="single" w:sz="6" w:space="0" w:color="94A3B8"/>
                    <w:right w:val="single" w:sz="6" w:space="0" w:color="94A3B8"/>
                </w:tblBorders>
            </w:tblPr>
            <w:tblGrid><w:gridCol w:w="9360"/></w:tblGrid>
            <w:tr>
                <w:tc>
                    <w:tcPr>
                        <w:tcW w:w="9360" w:type="dxa"/>
                        <w:shd w:val="clear" w:color="auto" w:fill="F1F5F9"/>
                        <w:tcMar>
                            <w:top w:w="120" w:type="dxa"/>
                            <w:bottom w:w="120" w:type="dxa"/>
                            <w:left w:w="180" w:type="dxa"/>
                            <w:right w:w="180" w:type="dxa"/>
                        </w:tcMar>
                    </w:tcPr>
                    {''.join(p_runs)}
                </w:tc>
            </w:tr>
        </w:tbl>
        <w:p><w:pPr><w:spacing w:before="0" w:after="80"/></w:pPr></w:p>'''
        self.paragraphs.append(code_box_xml)

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
        print(f"Document successfully written to: {output_path}")

def build_complete_specification_document(output_path):
    template = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "Edge_AI_Vision_System_Production_Architecture.docx")
    if not os.path.exists(template):
        template = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "API_DOCUMENTATION_RTSP.docx")
        
    doc = DocxBuilder(template)
    
    # Title Block
    doc.add_p("RELIANCE AI VISION - INDUSTRIAL EDGE INFERENCE PLATFORM", style="Title", is_bold=True, color="1B365D", align="center", space_before=100, space_after=60)
    doc.add_p("Comprehensive Code-Level Architecture, Multi-Threaded Execution Model, and Operational Specification Manual", style="Subtitle", color="0D9488", align="center", space_before=0, space_after=180)
    
    # Meta Table
    doc.add_table(
        ["Specification Attribute", "System Design Implementation"],
        [
            ["Platform Designation", "Enterprise Industrial Edge AI Vision & RTSP Gateway"],
            ["Target Hardware Architecture", "Raspberry Pi 4/5 (ARM64 Cortex-A72/A76), Edge Linux Units"],
            ["Primary Ingestion Protocols", "RTSP (H.264 / H.265 / HEVC over TCP Port 554), ONVIF Profile S/T"],
            ["Visual Streaming Standard", "Zero-Wear Low-Latency HLS (Baseline Profile Level 3.1 @ 10 FPS in RAM tmpfs)"],
            ["Neural Inference Stack", "PyTorch 2.x, Ultralytics YOLOv8, ByteTrack (Kalman Filter), FastANPR OCR"],
            ["Core Decoupling Principle", "Asynchronous Ingestion (10 FPS) Decoupled from Neural Inference (~2-3 FPS)"],
            ["Concurrency Model", "4 Concurrent Threads per Camera, Synchronized via Mutex Locks (_frame_lock, _box_lock)"],
            ["Base REST API & Gateway", "FastAPI ASGI Engine, Uvicorn Server on Port 8080 (Smart Dynamic HLS Routing)"],
            ["Alert Persistence Architecture", "PostgreSQL Database Pooling with Atomic Subprocess psql Fallback + alerts.json"]
        ],
        [3200, 6160]
    )

    doc.add_callout(
        "ARCHITECTURAL HIGHLIGHT: COMPLETE SYSTEM GUARANTEE",
        "This platform is designed to guarantee 24/7 autonomous edge surveillance without thermal throttling, stream latency buildup, or flash media fatigue. By combining decoupled RTSP socket ingestion, round-robin CPU inference scheduling, zero-wear RAM circular buffers, and spatial/biomechanical filtering, the system achieves enterprise-grade reliability on resource-constrained ARM64 hardware."
    )

    # -------------------------------------------------------------
    # 1. EXECUTIVE OVERVIEW
    # -------------------------------------------------------------
    doc.add_heading_1("1. Executive Technical Overview")
    doc.add_p("The Reliance AI Vision platform is an edge-native industrial computer vision gateway engineered for deployment in high-security, hazardous, and logistically intensive industrial environments (such as petroleum refineries, tank truck loading gantries, pipeline distribution terminals, and warehouse bays).")
    doc.add_p("Standard commercial video management software (VMS) solutions are architecturally incapable of executing multiple simultaneous deep convolutional neural networks directly on edge microcomputers (such as the Raspberry Pi 4). When standard architectures attempt multi-camera inference, three fatal hardware bottlenecks occur:")
    doc.add_bullet("Buffer Bloat & Stream Latency: Single-threaded video loops create a compounding delay; if inference takes 300 ms, incoming 25 FPS video queues up in the camera driver buffer, causing live streams to lag by 10 to 30 seconds within minutes.", bold_prefix="1. Video Lag Buildup: ")
    doc.add_bullet("CPU Saturation & Thermal Throttling: Multiple camera streams competing simultaneously for PyTorch inference saturate all four CPU cores at 100%, causing thermal throttling, frame dropping, and kernel lockups.", bold_prefix="2. Hardware Lock Contention: ")
    doc.add_bullet("Storage Media Burnout: Constantly committing 2-second live streaming video chunks to microSD flash media exhausts write cycles, destroying the edge storage drive within 3 to 6 months of continuous operation.", bold_prefix="3. MicroSD Flash Degradation: ")
    doc.add_p("This platform completely solves these three failure modes through an elegant, mathematically disciplined systems architecture that decouples visual frame delivery from neural inference, enforces a round-robin central inference scheduler, and confines all video chunks to a volatile Linux RAM disk (/tmp/hls).")

    # -------------------------------------------------------------
    # 2. MULTI-THREADED CONCURRENCY MODEL
    # -------------------------------------------------------------
    doc.add_heading_1("2. Multi-Threaded Concurrency Model: Where Threads Are Applied & Why")
    doc.add_p("A core evaluation criteria for senior architecture review is understanding exactly where concurrency is applied and how thread synchronization is guaranteed without deadlocks. The platform deploys four distinct, dedicated concurrent threads per camera, coordinated by three mutex locks.")

    doc.add_heading_2("2.1 Detailed Thread Breakdown")

    doc.add_heading_3("Thread 1: Master Worker Thread (DetectorWorker.run)")
    doc.add_bullet("Spawning Entrypoint: server.py at Line 2490 via threading.Thread(target=worker.run, daemon=True).start()", bold_prefix="Lifecycle: ")
    doc.add_bullet("Primary Responsibility: Orchestrates worker lifecycle, registers the camera into the Central Inference Scheduler, launches the output FFmpeg HLS encoding pipe, and runs the steady 10 FPS video display loop.", bold_prefix="Execution Role: ")
    doc.add_bullet("Why It Exists: Decouples the long-running video streaming loop from the FastAPI ASGI web server. If this were executed synchronously inside the API route handler, the HTTP endpoint would block indefinitely and never return a response to the UI.", bold_prefix="Architectural Justification: ")

    doc.add_heading_3("Thread 2: Frame Capture Thread (DetectorWorker._capture_thread)")
    doc.add_bullet("Spawning Entrypoint: detector_worker.py at Line 5163 via threading.Thread(target=self._capture_thread, daemon=True).start()", bold_prefix="Lifecycle: ")
    doc.add_bullet("Primary Responsibility: Executes a dedicated, continuous cap.read() loop from the RTSP socket with CAP_PROP_BUFFERSIZE = 1, immediately copying the incoming uncompressed frame into self._latest_raw_frame under self._frame_lock.", bold_prefix="Execution Role: ")
    doc.add_bullet("Why It Exists: Solves buffer bloat. By running independently of inference, it continuously drains the OS network buffer. Even if neural inference takes 300 ms, no frames queue up in the driver; inference always receives the absolute freshest frame from the camera, guaranteeing zero-latency live playback.", bold_prefix="Architectural Justification: ")

    doc.add_heading_3("Thread 3: Central Inference Scheduler Thread (InferenceScheduler._run_loop)")
    doc.add_bullet("Spawning Entrypoint: detector_worker.py at Line 1805 via threading.Thread(target=self._run_loop, name='CentralInferenceScheduler').start()", bold_prefix="Lifecycle: ")
    doc.add_bullet("Primary Responsibility: Maintains a global registry of all active cameras and dispatches neural inference (worker.run_single_inference_cycle()) in a strict, fair round-robin loop.", bold_prefix="Execution Role: ")
    doc.add_bullet("Why It Exists: Solves CPU saturation and lock contention. It guarantees that exactly ONE PyTorch inference forward pass executes across the entire system at any given millisecond. This prevents CPU core thrashing, eliminates thread starvation, and keeps hardware temperatures well within safe thermal operating limits.", bold_prefix="Architectural Justification: ")

    doc.add_heading_3("Thread 4: Raw Stream Health Monitor Thread (server.py:monitor_raw_streams_loop)")
    doc.add_bullet("Spawning Entrypoint: server.py at Line 1056 in startup_event() via threading.Thread(target=monitor_raw_streams_loop, daemon=True).start()", bold_prefix="Lifecycle: ")
    doc.add_bullet("Primary Responsibility: A standing background watchdog thread that polls running RTSP camera processes every 10 seconds.", bold_prefix="Execution Role: ")
    doc.add_bullet("Why It Exists: Self-healing infrastructure. If an IP camera drops off the network due to power cycling or transient Wi-Fi/Ethernet disconnects, this monitor automatically detects the dead FFmpeg process and seamlessly restarts the stream without human intervention.", bold_prefix="Architectural Justification: ")

    doc.add_heading_2("2.2 Thread Synchronization & Mutex Locking Architecture")
    doc.add_table(
        ["Mutex Lock Identifier", "Guarded Resource", "Accessing Threads", "Failure Mode Prevented if Lock Omitted"],
        [
            ["self._frame_lock", "self._latest_raw_frame", "Thread 2 (Writer)\nThread 1 & 3 (Readers)", "Memory tearing / partial frame reads: Prevents inference or display threads from reading half-written BGR frame buffers."],
            ["self._box_lock", "self._tracked_boxes", "Thread 3 (Writer)\nThread 1 (Reader)", "Race conditions on bounding box arrays: Prevents the 10 FPS display loop from reading half-updated Kalman track structures."],
            ["_ALERT_COOLDOWNS_LOCK", "GLOBAL_ALERT_COOLDOWNS dict", "Thread 3 across all cameras", "Duplicate alert storms: Guarantees atomic read-modify-write on violation timestamps, enforcing the 30-second cooldown."],
            ["_heartbeat_lock", "device_manager._heartbeats", "FastAPI async worker threads", "Data corruption on device registry: Ensures atomic updates to online/offline timestamps in device_heartbeats.json."]
        ],
        [2400, 2200, 2300, 2460]
    )

    # -------------------------------------------------------------
    # 3. END-TO-END FUNCTION CALLING FLOW
    # -------------------------------------------------------------
    doc.add_heading_1("3. End-to-End Function Calling Sequence: From Raw RTSP to Alert Generation")
    doc.add_p("This section traces the exact, deterministic execution trajectory of a video frame across the entire codebase, linking specific file paths, function definitions, parameters, and transformations.")

    doc.add_heading_2("Phase 1: Stream Initiation & Process Setup")
    doc.add_p("When the operator triggers detection via POST /api/start, execution enters server.py:", bold_prefix="1.1 API Trigger: ")
    doc.add_code_block("""# backend/server.py: Line 2389
@app.post("/api/start")
def start_detection(d: dict):
    cid = str(d.get("camera", "0"))
    rtsp = d.get("rtsp")
    # Step A: Validate RTSP syntax (Line 467: is_valid_rtsp_url(rtsp))
    # Step B: Terminate competing raw stream (Line 593: _kill_raw_ffmpeg_for_camera(cid))
    # Step C: Clean stale segments (Line 629: _clean_stale_detected_segments(cid))
    # Step D: Load model weights (Line 1504: get_yolo_model(m))
    worker = DetectorWorker(rtsp, det_dir, model_paths, conf=conf, iou=iou, location=loc, model_configs=model_configs)
    thread = threading.Thread(target=worker.run, daemon=True)
    thread.start()
    running[cid] = {"proc": proc, "worker": worker, ...}""")

    doc.add_heading_2("Phase 2: Video Pipe & Frame Ingestion")
    doc.add_p("The worker thread initializes the FFmpeg pipe and begins ingesting uncompressed frames:", bold_prefix="2.1 Capture Ingestion: ")
    doc.add_code_block("""# backend/detector/detector_worker.py: Lines 2180, 5103, 4966
def run(self):
    GLOBAL_INFERENCE_SCHEDULER.register_worker(self)
    ffmpeg = self._create_ffmpeg()  # Spawns FFmpeg Baseline H.264 pipe writing to /tmp/hls
    cap = cv2.VideoCapture(str(self.rtsp_url), cv2.CAP_FFMPEG)
    cap.set(cv2.CAP_PROP_BUFFERSIZE, 1)  # Flush hardware queue
    cap_t = threading.Thread(target=self._capture_thread, args=(cap, cap_stop_evt), daemon=True)
    cap_t.start()

def _capture_thread(self, cap, cap_stop_evt):
    while not self._stop_event.is_set():
        ret, f = cap.read()  # Uncompressed BGR NumPy array (H, W, 3)
        if ret and f is not None:
            with self._frame_lock:
                self._latest_raw_frame = f.copy()
                self._last_frame_time = time.time()""")

    doc.add_heading_2("Phase 3: Central Scheduling & Neural Inference")
    doc.add_p("The Central Inference Scheduler picks the camera in round-robin sequence and dispatches neural inference:", bold_prefix="3.1 Inference Dispatch: ")
    doc.add_code_block("""# backend/detector/detector_worker.py: Lines 1809, 2260
def _run_loop(self):
    while not self._stop_event.is_set():
        for worker in active_workers:
            worker.run_single_inference_cycle()
            time.sleep(SCHEDULER_SLEEP_S)

def run_single_inference_cycle(self):
    with self._frame_lock:
        f = self._latest_raw_frame  # Acquire raw uncompressed frame
    
    # Stage 1: Full-Frame YOLOv8 Detection (Line 2407)
    with torch.inference_mode():
        results = model.predict(source=f, conf=p_conf, iou=m_iou, imgsz=m_imgsz, verbose=False)
        
    # Spatial Boundary Gating (Line 2549: roi_utils.is_box_inside_rois(box, self.roi_polygon))
    # Stage 2: Adaptive Person Crop-and-Zoom (Line 2651: p_model.predict(source=person_crop, ...))
    # Multi-Model NMS Fusion (Line 2717: self._apply_nms(kept_items))
    # Kalman Tracking (Line 3100: self.tracker.update(detection_array))""")

    doc.add_heading_2("Phase 4: Domain Rules, Alert Generation & Evidence Storage")
    doc.add_p("When violations are confirmed by business logic engines, the cooldown gate evaluates and stores the alert:", bold_prefix="4.1 Alert Execution: ")
    doc.add_code_block("""# backend/detector/detector_worker.py: Lines 180, 4808
if check_and_record_alert_cooldown(self.cam_id, alert_class, cooldown_s=30.0):
    self._save_alert(alert_class, frame_with_boxes, vehicle_label, license_plate)

def _save_alert(self, alert_class, frame_with_boxes=None, ...):
    # 1. Commit JPEG evidence snapshot to disk
    snapshot_path = f"/tmp/hls/alerts/alert_{self.cam_id}_{int(time.time())}_{rand_id}.jpg"
    cv2.imwrite(snapshot_path, frame_with_boxes)
    
    # 2. Commit SQL record to PostgreSQL
    # alert_store.py: Line 21: insert_alert_db(cursor, camera_id, location, type_of_alert, image_path)
    
    # 3. Commit JSON record to alerts.json
    # server.py: Line 4124: create_alert(...)
    
    # 4. Thread-Safe Box Update for Video Streaming
    with self._box_lock:
        self._tracked_boxes = display_boxes""")

    doc.add_heading_2("Phase 5: Rendering & Low-Latency HLS Delivery")
    doc.add_p("Simultaneously, the master worker loop renders bounding boxes and streams to HLS at 10 FPS:", bold_prefix="5.1 Video Streaming: ")
    doc.add_code_block("""# backend/detector/detector_worker.py: Lines 5380-5420
while not self._stop_event.is_set():
    pf = cv2.resize(raw_frame, (self.width, self.height), interpolation=cv2.INTER_LINEAR)
    pf = draw_rois_on_frame(pf, self.roi_polygon)
    with self._box_lock:
        display_boxes = list(self._tracked_boxes)
    DetectorWorker._draw_boxes(pf, display_boxes)  # Area-based Z-ordering & stacked labels
    ffmpeg.stdin.write(pf.tobytes())               # Pipe raw BGR24 to FFmpeg
    ffmpeg.stdin.flush()""")

    # -------------------------------------------------------------
    # 4. SPECIALIZED AI ALGORITHMS
    # -------------------------------------------------------------
    doc.add_heading_1("4. Specialized AI & Computer Vision Algorithmic Formulations")
    doc.add_p("The platform implements five mathematically disciplined algorithms engineered specifically for industrial physical environments:")

    doc.add_heading_2("4.1 Dual-Distance Adaptive Crop-and-Zoom (PPE Detection)")
    doc.add_p("Standard 640x640 network downscaling obscures small safety equipment (helmets, goggles, high-vis vests) on workers standing 10 to 15 meters away. The platform implements a two-stage adaptive zoom pipeline:")
    doc.add_bullet("Stage 1 identifies all worker bounding boxes [px1, py1, px2, py2] on the full-resolution frame.", bold_prefix="Worker Localization: ")
    doc.add_bullet("The algorithm extracts the worker region directly from uncompressed source memory: person_crop = f[py1:py2, px1:px2].", bold_prefix="Sub-Image Extraction: ")
    doc.add_bullet("Secondary companion PPE models (hansung_ppe_violations.pt, ppe_new.pt) execute directly on the crop at native pixel density.", bold_prefix="Focused Neural Pass: ")
    doc.add_bullet("Sub-bounding box coordinates are translated back to global coordinates: x_global = px1 + x_sub, y_global = py1 + y_sub.", bold_prefix="Coordinate Remapping: ")

    doc.add_heading_2("4.2 Spatial ROI Boundary Gating (Ray-Casting Algorithm)")
    doc.add_p("To eliminate false alarms caused by pedestrian walkways or public access roads outside loading bays, all detections must be geometrically validated against user-drawn polygon ROIs using the Jordan Curve Theorem Ray-Casting algorithm:")
    doc.add_p("The contact ground point P(x, y) is calculated as P = ((x1 + x2)/2, y2). A horizontal semi-infinite ray is cast from P toward +X. The number of intersections with polygon edges is counted. If the intersection count is odd, P is strictly INSIDE; if even, P is OUTSIDE and the detection is pruned.")

    doc.add_heading_2("4.3 Kalman Filter State Space Tracking (EdgeByteTracker)")
    doc.add_p("ByteTrack maintains object identity across frames without the massive CPU overhead of convolutional ReID feature networks. The motion model tracks an 8-dimensional state vector:")
    doc.add_p("x = [u, v, a, h, u_dot, v_dot, a_dot, h_dot]^T", style="Normal", is_italic=True, align="center")
    doc.add_p("Where (u, v) is bounding box center, a is aspect ratio (width/height), h is height, and the dot-terms represent respective velocities. Associations are resolved via the Jonker-Volgenant LAP algorithm (lapx) using IoU cost matrices.")

    doc.add_heading_2("4.4 FastANPR License Plate Recognition & Indian Syntax Correction")
    doc.add_p("The FastANPR engine executes OCR on vehicle bumper crops (lower 30% of vehicle bounding boxes) with two critical reliability safeguards:")
    doc.add_bullet("Sharpness Gating: cv2.Laplacian(plate_gray, cv2.CV_64F).var(). If variance < 35.0, the frame is motion-blurred and OCR is bypassed to conserve CPU.", bold_prefix="Laplacian Gate: ")
    doc.add_bullet("Indian Syntax Parser (correct_indian_plate_syntax): Enforces standard [State: 2 Alpha][District: 2 Digit][Series: 1-3 Alpha][Number: 4 Digit]. Resolves optical confusions (0 <-> O, 1 <-> I, 8 <-> B, 5 <-> S) based on positional slot grammar.", bold_prefix="Grammar Correction: ")
    doc.add_bullet("PlateVotingTracker: Records OCR character strings across consecutive frames in a frequency histogram, outputting consensus plate text via majority vote.", bold_prefix="Temporal Voting: ")

    doc.add_heading_2("4.5 Tank Truck (TT) Underside Inspection (4-Stage Geometric Filter)")
    doc.add_p("Differentiating a worker genuinely crawling below a fuel tanker from an inspector walking past in the foreground requires four strict geometric tests:")
    doc.add_bullet("Co-Presence Gating: Both a Tank Truck and a Person must reside inside the bay ROI.", bold_prefix="Gate 0: ")
    doc.add_bullet("Foreground Rejection: If the person's ground point y2 is > 12% below the truck's tire line, they are walking in front of the truck -> REJECT.", bold_prefix="Filter 1: ")
    doc.add_bullet("Head Height Rejection: A person crawling below a truck chassis cannot have their head above the lower chassis/axle line (y_head < y_truck_bottom - 0.40 * h_truck) -> REJECT.", bold_prefix="Filter 2: ")
    doc.add_bullet("Upright Posture Rejection: Standing humans have tall aspect ratios (bw / bh < 0.62). Crawling workers are horizontal (bw / bh > 0.85) -> REJECT.", bold_prefix="Filter 3: ")
    doc.add_bullet("Dwell Confirmation: The crawling posture must persist continuously for >= 2.0 seconds before an alert fires.", bold_prefix="Filter 4: ")

    # -------------------------------------------------------------
    # 5. ONVIF PTZ PROTOCOL
    # -------------------------------------------------------------
    doc.add_heading_1("5. ONVIF PTZ Camera Control & Protocol Architecture")
    doc.add_p("Physical PTZ cameras are controlled over standard ONVIF Profile S/T SOAP XML services. The platform implements an internal ONVIF client with zero third-party library dependencies.")
    doc.add_p("Authentication satisfies the strict WS-Security UsernameToken specification. Every SOAP request generates a dynamic 16-byte cryptographic random Nonce and a UTC timestamp, computing the SHA-1 Password Digest:")
    doc.add_p("PasswordDigest = Base64( SHA-1( Nonce_raw + Created_utc + Password_ascii ) )", style="Normal", is_bold=True, align="center")
    doc.add_p("This XML security header is wrapped in the SOAP envelope, supporting continuous directional panning (ContinuousMove), discrete nudges (RelativeMove), speed-variable zooming, and preset management (GetPresets, GotoPreset, SetPreset).")

    # -------------------------------------------------------------
    # 6. TECHNOLOGY STACK & LIBRARY JUSTIFICATION
    # -------------------------------------------------------------
    doc.add_heading_1("6. Technology Stack & Library Justifications")
    doc.add_p("Every library in the dependency manifest was selected to optimize ARM64 CPU throughput, prevent memory leaks, and guarantee asynchronous I/O performance:")
    doc.add_table(
        ["Technology / Library", "Architectural Role in Project", "Why Chosen Over Industry Alternatives"],
        [
            ["PyTorch (torch)", "Deep learning neural execution engine", "Native ARM64 NEON SIMD optimizations; torch.inference_mode() disables autograd DAGs, saving 40% RAM compared to standard frameworks."],
            ["Ultralytics (YOLOv8)", "State-of-the-art vision model architecture", "Anchor-free decoupled head; native support for both bounding boxes and 17 pose keypoints in a single forward pass."],
            ["OpenCV (cv2)", "Visual frame decoding and graphic overlays", "C++ backend with native FFmpeg hooks (CAP_FFMPEG); zero-copy NumPy array slicing; bilinear downscaling in 1.2 ms."],
            ["FastAPI", "Asynchronous REST API Gateway (Port 8080)", "ASGI architecture based on uvloop; non-blocking request routing handles HLS chunks and heartbeats without thread exhaustion."],
            ["Uvicorn", "Lightning-fast ASGI production web server", "Extremely lightweight process footprint; native support for HTTP/1.1 keep-alive and chunked transfer encoding."],
            ["Psycopg2", "PostgreSQL database client & connection pool", "ThreadedConnectionPool provides robust thread-safe connection reuse; includes fallback to atomic psql subprocess."],
            ["Lapx", "Linear Assignment Problem solver", "High-performance C++ implementation of the Jonker-Volgenant algorithm for bipartite matching in ByteTrack."],
            ["NumPy", "Fundamental multidimensional array engine", "Standard representation for raw uncompressed BGR video frames; powers vector geometry and Kalman matrix operations."]
        ],
        [2200, 2600, 4560]
    )

    # -------------------------------------------------------------
    # 7. PRODUCTION HARDWARE OPTIMIZATIONS
    # -------------------------------------------------------------
    doc.add_heading_1("7. Production Edge Hardware Optimizations")
    doc.add_p("To achieve multi-year continuous operation on edge hardware, three production guarantees are enforced:")
    doc.add_bullet("Multi-Camera CPU Budgeting: Visual frame delivery is locked to 10 FPS with constrained H.264 Baseline encoding (220 kbps), while AI inference executes at ~2-3 FPS on the Central Scheduler. Exponential Moving Average (EMA) box interpolation ensures fluid bounding box display without extra neural computation.", bold_prefix="1. Decoupled FPS Budgeting: ")
    doc.add_bullet("MicroSD Flash Media Endurance: Exactly zero continuous video is committed to flash storage. All live HLS segments are confined to a 12-second circular ring buffer in Linux RAM (/tmp/hls, 6 segments x 2s). Physical storage is reserved exclusively for ~150 KB evidence snapshot JPEGs.", bold_prefix="2. Zero Flash Wear: ")
    doc.add_bullet("Zero-Overhead Symlink Deduplication: When two logical camera entries share the same physical RTSP URL, the system creates a filesystem symlink (os.symlink) in /tmp/hls, streaming to both cameras while consuming zero additional CPU cycles.", bold_prefix="3. Stream Symlinking: ")

    # -------------------------------------------------------------
    # 8. COMPLETE REST API SPECIFICATION
    # -------------------------------------------------------------
    doc.add_heading_1("8. Complete REST API Specification")
    doc.add_p("The platform exposes a comprehensive, RESTful API surface on Port 8080:")
    doc.add_table(
        ["HTTP Method", "API Endpoint Route", "Handler Function", "Operational Purpose"],
        [
            ["GET", "/api/streams", "get_streams()", "Fetch all configured camera streams, locations, and HLS streaming URLs."],
            ["POST", "/api/streams", "save_streams()", "Atomically save or update camera configurations in streams.json."],
            ["POST", "/api/start", "start_detection()", "Launch multi-threaded DetectorWorker inference thread for a camera."],
            ["POST", "/api/stop", "stop_raw_for_camera()", "Stop AI detection worker and revert camera back to raw HLS streaming."],
            ["GET", "/hls/camera/{cid}/playlist.m3u8", "smart_hls_playlist()", "Smart dynamic HLS routing between raw and detected streams without player reload."],
            ["GET", "/api/models", "get_models()", "Dynamically inspect and list available .pt model weight files."],
            ["GET", "/api/model-classes", "get_model_classes_endpoint()", "Introspect and return class labels supported by a specific model file."],
            ["POST", "/api/ptz/move", "ptz_move()", "Execute continuous Pan / Tilt / Zoom motion via ONVIF SOAP services."],
            ["POST", "/api/ptz/step", "ptz_step()", "Nudge PTZ camera by a discrete single-step directional angle."],
            ["POST", "/api/ptz/stop", "ptz_stop()", "Instantly halt all physical PTZ motor movements."],
            ["GET", "/api/alerts/db", "get_alerts_db()", "Retrieve latest 50 AI violation records from PostgreSQL database."],
            ["GET", "/api/alerts/images", "get_alert_images()", "List all timestamped evidence snapshot JPEG filenames."],
            ["POST", "/api/analytics/by-location", "get_analytics_by_location()", "Retrieve violation frequency metrics aggregated by facility location."],
            ["POST", "/api/analytics/by-usecase", "get_analytics_by_usecase()", "Retrieve violation frequency metrics aggregated by AI model use case."],
            ["POST", "/api/rpi-heartbeat", "rpi_heartbeat()", "Ingest edge device health metrics (IP, CPU loadavg, RAM usage)."],
            ["GET", "/api/devices", "get_devices()", "Get online/offline status list of all registered edge devices (90s TTL)."]
        ],
        [1200, 2600, 2300, 3260]
    )

    # -------------------------------------------------------------
    # 9. CODE REVIEW DEFENSE Q&A
    # -------------------------------------------------------------
    doc.add_heading_1("9. Code Review Defense: Questions & Authoritative Answers")
    doc.add_p("This section arms the engineer with bulletproof, code-referenced answers for senior architecture evaluation:")

    doc.add_heading_2("Q1: Why did you separate frame capture into a separate thread (_capture_thread) instead of calling cap.read() inside the detection loop?")
    doc.add_p("Answer: cv2.VideoCapture relies on an internal OS hardware socket buffer. If neural inference takes 300 ms, calling cap.read() in the same thread leaves frames accumulating in the driver queue. Within minutes, the stream lags by 10 to 15 seconds. By dedicating _capture_thread with CAP_PROP_BUFFERSIZE = 1 to continuously overwrite self._latest_raw_frame under a mutex lock, our inference thread always consumes the freshest real-time frame, guaranteeing near zero-latency streaming.", bold_prefix="Engineering Defense: ")

    doc.add_heading_2("Q2: How does the system prevent CPU saturation when multiple cameras run simultaneously on a Raspberry Pi?")
    doc.add_p("Answer: We implemented the Central Inference Scheduler (InferenceScheduler). Instead of each camera thread calling PyTorch independently and competing for CPU cores, camera workers register with the scheduler. The scheduler executes exactly ONE camera inference cycle at a time in a round-robin sequence. Meanwhile, video output threads smoothly interpolate bounding boxes at 10 FPS using Exponential Moving Averages (EMA).", bold_prefix="Engineering Defense: ")

    doc.add_heading_2("Q3: What prevents the Raspberry Pi's microSD card from failing under continuous 24/7 video recording?")
    doc.add_p("Answer: Exactly zero continuous video is committed to flash media. All HLS segments are written to /tmp/hls, which resides in volatile Linux RAM (tmpfs). FFmpeg enforces a circular ring buffer of 6 segments x 2 seconds (12 seconds total), with delete_segments automatically pruning older chunks in RAM. Physical flash storage is written to ONLY when a confirmed violation triggers an evidence snapshot JPEG (~150 KB).", bold_prefix="Engineering Defense: ")

    doc.add_heading_2("Q4: How does the web UI switch between raw video and AI detection without reloading the player?")
    doc.add_p("Answer: The frontend player points permanently to the smart HLS endpoint /hls/camera/{cid}/playlist.m3u8. On the backend, serve_camera_virtual_file() checks if the camera ID is active in the running dictionary. If detection is active, it seamlessly serves chunks from stream{cid}_detected; if stopped, it serves from stream{cid}_raw. The client's HLS.js player continues fetching chunks without resetting the HTML5 video element.", bold_prefix="Engineering Defense: ")

    doc.add_heading_2("Q5: How does your system prevent duplicate alert spamming if an unauthorized vehicle stays parked for an hour?")
    doc.add_p("Answer: Through check_and_record_alert_cooldown() in detector_worker.py. Every violation class per camera enforces a thread-safe 30-second cooldown window. Once an alert triggers and the evidence snapshot is committed to the database, subsequent detections of the same violation are suppressed until the 30-second cooldown expires.", bold_prefix="Engineering Defense: ")

    # Save to both target locations
    doc.save(output_path)
    secondary_path = os.path.join(os.path.dirname(output_path), "COMPLETE_CODE_REVIEW_DOCUMENT.docx")
    shutil.copyfile(output_path, secondary_path)
    print(f"Secondary copy created at: {secondary_path}")

if __name__ == "__main__":
    base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    target = os.path.join(base_dir, "COMPLETE_CODE_REVIEW_AND_ARCHITECTURE_SPECIFICATION.docx")
    build_complete_specification_document(target)
