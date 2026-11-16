import os
import subprocess
import threading
import time
import glob
import shutil
from config import RTSP_URL, HLS_DIR

class StreamService:
    def __init__(self, rtsp_url=RTSP_URL, output_dir=HLS_DIR):
        self.rtsp_url = rtsp_url
        self.output_dir = output_dir
        self.live_dir = os.path.join(self.output_dir, "live")
        self.proc = None
        self._lock = threading.Lock()
        self._running = False

    def start(self):
        with self._lock:
            if self.proc and self.proc.poll() is None:
                return {"status": "already_running", "hls_url": "/hls/live/playlist.m3u8"}

            os.makedirs(self.live_dir, exist_ok=True)
            # Clean old segments
            for f in glob.glob(os.path.join(self.live_dir, "*")):
                try:
                    if os.path.isdir(f): shutil.rmtree(f)
                    else: os.remove(f)
                except: pass

            session_id = int(time.time())
            log_path = os.path.join(self.live_dir, "ffmpeg.log")
            log_file = open(log_path, "w")

            cmd = [
                "ffmpeg", "-hide_banner", "-loglevel", "warning", "-y",
                "-rtsp_transport", "tcp",
                "-probesize", "1M", "-analyzeduration", "1M",
                "-i", self.rtsp_url,
                "-an",
                "-vf", "scale=1280:720:flags=fast_bilinear,format=yuv420p",
                "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
                "-profile:v", "baseline", "-level:v", "3.1",
                "-b:v", "800k", "-maxrate", "1000k", "-bufsize", "2M",
                "-threads", "2",
                "-r", "15",
                "-g", "30", "-keyint_min", "30", "-sc_threshold", "0",
                "-f", "hls",
                "-hls_time", "1",
                "-hls_list_size", "5",
                "-hls_flags", "delete_segments+independent_segments+discont_start+omit_endlist+temp_file",
                "-hls_segment_filename", os.path.join(self.live_dir, f"segment_{session_id}_%d.ts"),
                os.path.join(self.live_dir, "playlist.m3u8")
            ]

            print(f"[STREAM] Starting HLS transcode from {self.rtsp_url} to {self.live_dir}...", flush=True)
            self.proc = subprocess.Popen(cmd, stdout=log_file, stderr=log_file)
            self._running = True
            return {"status": "started", "hls_url": "/hls/live/playlist.m3u8"}

    def stop(self):
        with self._lock:
            if self.proc:
                try:
                    self.proc.terminate()
                    self.proc.wait(timeout=2.0)
                except:
                    self.proc.kill()
                self.proc = None
            self._running = False
            return {"status": "stopped"}

    def is_alive(self):
        return self.proc is not None and self.proc.poll() is None
