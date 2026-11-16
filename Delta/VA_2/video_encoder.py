#!/usr/bin/env python3
"""
Delta Video Sender/Receiver using SRT with proper timing preservation
"""
import os
import sys
import time
import signal
import subprocess
import argparse
import threading
from datetime import datetime
from pathlib import Path

class DeltaSender:
    def __init__(self, input_file, srt_uri, crf=32, fps=None, no_audio=False):
        self.input_file = input_file
        self.srt_uri = srt_uri
        self.crf = crf
        self.fps = fps
        self.no_audio = no_audio
        self.start_time = None
        self.process = None
        
    def get_fps(self):
        """Get FPS from video file using ffprobe"""
        cmd = [
            'ffprobe', '-v', 'error',
            '-select_streams', 'v:0',
            '-show_entries', 'stream=r_frame_rate',
            '-of', 'default=noprint_wrappers=1:nokey=1',
            self.input_file
        ]
        
        try:
            result = subprocess.run(cmd, capture_output=True, text=True, check=True)
            fps_str = result.stdout.strip()
            if '/' in fps_str:
                num, den = map(int, fps_str.split('/'))
                return num / den if den > 0 else 25.0
            return float(fps_str) if fps_str else 25.0
        except:
            return 25.0
    
    def build_pipeline(self):
        """Build GStreamer pipeline for sending"""
        original_fps = self.get_fps()
        fps = self.fps or int(original_fps)
        
        print(f"Original FPS: {original_fps:.2f}")
        print(f"Using FPS: {fps}")
        
        # Base pipeline
        pipeline = [
            'gst-launch-1.0', '-q', '-e',
            'filesrc', f'location={self.input_file}',
            '!', 'decodebin', 'name=dec'
        ]
        
        # Video pipeline with proper timing
        video_parts = [
            'dec.', '!', 'queue',
            '!', 'videoconvert',
            '!', 'videorate',
            f'!', 'video/x-raw,framerate={fps}/1',
            '!', 'x264enc',
            f'key-int-max={fps * 10}',
            'bframes=0',
            'tune=zerolatency',
            'bitrate=2000',
            'pass=quant',
            f'quantizer={self.crf}',
            '!', 'h264parse', 'config-interval=1',
            '!', 'rtph264pay', 'pt=96', 'mtu=1400',
            '!', 'srtsink',
            f'uri={self.srt_uri}?mode=caller',
            'latency=120'
        ]
        
        pipeline.extend(video_parts)
        
        # Add audio if requested
        if not self.no_audio:
            print("Warning: Audio streaming requires more complex pipeline")
            print("Using video-only mode for simplicity")
        
        return pipeline
    
    def run(self):
        """Run the sender"""
        self.start_time = time.time()
        
        print("\n=== Delta Sender ===")
        print(f"Input:      {self.input_file}")
        print(f"SRT URI:    {self.srt_uri}")
        print(f"CRF:        {self.crf}")
        print(f"FPS:        {self.fps or 'auto'}")
        
        pipeline = self.build_pipeline()
        print(f"\nPipeline: {' '.join(pipeline)}")
        
        print("\n=== Starting encode+stream ===")
        
        try:
            self.process = subprocess.Popen(pipeline)
            self.process.wait()
            
            elapsed = time.time() - self.start_time
            print(f"\n=== Complete ===")
            print(f">>> Wall-time: {elapsed:.2f} seconds")
            
            return self.process.returncode == 0
            
        except KeyboardInterrupt:
            print("\nInterrupted by user")
            if self.process:
                self.process.terminate()
            return False
        except Exception as e:
            print(f"Error: {e}")
            return False

class DeltaReceiver:
    def __init__(self, srt_uri, output_dir="./received_videos", 
                 fps=None, duration=None, buffer_ms=120):
        self.srt_uri = srt_uri
        self.output_dir = Path(output_dir)
        self.fps = fps
        self.duration = duration
        self.buffer_ms = buffer_ms
        self.start_time = None
        self.process = None
        
    def run(self):
        """Run the receiver using FFmpeg (recommended for timing)"""
        self.start_time = time.time()
        
        # Create output directory
        self.output_dir.mkdir(exist_ok=True)
        
        # Generate output filename
        timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
        output_file = self.output_dir / f"received_{timestamp}.mp4"
        
        print("\n=== Delta Receiver ===")
        print(f"SRT URI:    {self.srt_uri}")
        print(f"Output:     {output_file}")
        print(f"Buffer:     {self.buffer_ms} ms")
        if self.duration:
            print(f"Duration:   {self.duration} seconds")
        if self.fps:
            print(f"Force FPS:  {self.fps}")
        
        # Build FFmpeg command
        cmd = [
            'ffmpeg', '-y', '-loglevel', 'warning',
            '-i', self.srt_uri,
            '-c', 'copy',
            '-movflags', '+faststart'
        ]
        
        if self.duration:
            cmd.insert(4, f'-t {self.duration}')
            cmd.insert(5, '-i')
            
        if self.fps:
            cmd.extend(['-r', str(self.fps)])
            
        cmd.append(str(output_file))
        
        print(f"\nRunning: {' '.join(cmd)}")
        print("\n=== Receiving stream (Ctrl+C to stop) ===\n")
        
        try:
            self.process = subprocess.Popen(cmd)
            self.process.wait()
            
            elapsed = time.time() - self.start_time
            
            if self.process.returncode not in [0, 255]:  # 255 is SIGTERM
                print(f"Error: FFmpeg exited with code {self.process.returncode}")
                return False
            
            print(f"\n=== Complete ===")
            
            # Show file info
            if output_file.exists():
                size_mb = output_file.stat().st_size / (1024 * 1024)
                print(f"Output file:     {output_file}")
                print(f"File size:       {size_mb:.2f} MB")
                
                # Get duration
                duration_cmd = [
                    'ffprobe', '-v', 'error',
                    '-show_entries', 'format=duration',
                    '-of', 'default=noprint_wrappers=1:nokey=1',
                    str(output_file)
                ]
                
                try:
                    result = subprocess.run(duration_cmd, capture_output=True, text=True)
                    if result.stdout:
                        duration_sec = float(result.stdout.strip())
                        print(f"Duration:        {duration_sec:.2f} seconds")
                        
                        if duration_sec > 0:
                            bitrate = (size_mb * 8 * 1024) / duration_sec
                            print(f"Bitrate:         {bitrate:.0f} kbps")
                            
                            rate_mbps = size_mb / duration_sec
                            print(f"Transfer rate:   {rate_mbps:.2f} MB/s")
                except:
                    pass
                
                # Show stream info
                print(f"\nStream information:")
                info_cmd = [
                    'ffprobe', '-v', 'error',
                    '-select_streams', 'v:0',
                    '-show_entries', 'stream=r_frame_rate,avg_frame_rate,codec_name,width,height',
                    '-of', 'default=noprint_wrappers=1',
                    str(output_file)
                ]
                subprocess.run(info_cmd)
                
                print(f"\nPlay with: ffplay \"{output_file}\"")
            
            print(f"\n>>> Total wall-time:  {elapsed:.2f} seconds")
            return True
            
        except KeyboardInterrupt:
            print("\nInterrupted by user")
            if self.process:
                self.process.terminate()
            return False
        except Exception as e:
            print(f"Error: {e}")
            return False

def main():
    parser = argparse.ArgumentParser(description="Delta Video Streaming over SRT")
    subparsers = parser.add_subparsers(dest='command', help='Command to run')
    
    # Sender arguments
    sender_parser = subparsers.add_parser('send', help='Send video over SRT')
    sender_parser.add_argument('--input', required=True, help='Input video file')
    sender_parser.add_argument('--srt', required=True, help='SRT destination URI')
    sender_parser.add_argument('--crf', type=int, default=32, help='CRF value (default: 32)')
    sender_parser.add_argument('--fps', type=int, help='Force output FPS')
    sender_parser.add_argument('--no-audio', action='store_true', help='Disable audio')
    
    # Receiver arguments
    receiver_parser = subparsers.add_parser('receive', help='Receive video over SRT')
    receiver_parser.add_argument('--srt', required=True, help='SRT listen URI')
    receiver_parser.add_argument('--output', default='./received_videos', help='Output directory')
    receiver_parser.add_argument('--fps', type=int, help='Force output FPS')
    receiver_parser.add_argument('--duration', type=int, help='Stop after N seconds')
    receiver_parser.add_argument('--buffer', type=int, default=120, help='SRT buffer in ms')
    
    args = parser.parse_args()
    
    if args.command == 'send':
        sender = DeltaSender(
            input_file=args.input,
            srt_uri=args.srt,
            crf=args.crf,
            fps=args.fps,
            no_audio=args.no_audio
        )
        success = sender.run()
        sys.exit(0 if success else 1)
        
    elif args.command == 'receive':
        receiver = DeltaReceiver(
            srt_uri=args.srt,
            output_dir=args.output,
            fps=args.fps,
            duration=args.duration,
            buffer_ms=args.buffer
        )
        success = receiver.run()
        sys.exit(0 if success else 1)
        
    else:
        parser.print_help()
        sys.exit(1)

if __name__ == '__main__':
    main()