#!/usr/bin/env python3
import subprocess
import time
import os
import sys

def run_receiver():
    """Start receiver in background"""
    cmd = [
        '/Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0',
        'udpsrc', 'port=9000', '!',
        'application/x-rtp,media=video,clock-rate=90000,encoding-name=H264,payload=96', '!',
        'rtph264depay', '!',
        'h264parse', '!',
        'mp4mux', 'faststart=true', '!',
        'filesink', 'location=./received_videos/test_stream.mp4'
    ]
    
    print("Starting receiver...")
    env = os.environ.copy()
    env['GST_PLUGIN_SYSTEM_PATH'] = '/Library/Frameworks/GStreamer.framework/Versions/1.0/lib/gstreamer-1.0'
    env['DYLD_LIBRARY_PATH'] = '/Library/Frameworks/GStreamer.framework/Versions/Current/lib'
    
    receiver = subprocess.Popen(cmd, env=env)
    time.sleep(2)  # Wait for receiver to start
    return receiver

def run_sender(video_file):
    """Start sender"""
    cmd = [
        '/Library/Frameworks/GStreamer.framework/Versions/Current/bin/gst-launch-1.0', '-e',
        'filesrc', f'location={video_file}', '!',
        'decodebin', '!',
        'videoconvert', '!',
        'x264enc', 'bitrate=2000', 'key-int-max=60', 'bframes=3', '!',
        'h264parse', '!',
        'rtph264pay', 'pt=96', '!',
        'udpsink', 'host=127.0.0.1', 'port=9000'
    ]
    
    print("Starting sender...")
    env = os.environ.copy()
    env['GST_PLUGIN_SYSTEM_PATH'] = '/Library/Frameworks/GStreamer.framework/Versions/1.0/lib/gstreamer-1.0'
    env['DYLD_LIBRARY_PATH'] = '/Library/Frameworks/GStreamer.framework/Versions/Current/lib'
    
    sender = subprocess.run(cmd, env=env)
    return sender.returncode

def main():
    if len(sys.argv) < 2:
        print("Usage: python3 streaming_test.py <video_file>")
        sys.exit(1)
    
    video_file = sys.argv[1]
    
    if not os.path.exists(video_file):
        print(f"Error: File '{video_file}' not found")
        sys.exit(1)
    
    # Create output directory
    os.makedirs("./received_videos", exist_ok=True)
    
    # Start receiver
    receiver = run_receiver()
    
    # Start sender
    result = run_sender(video_file)
    
    # Wait a bit, then stop receiver
    time.sleep(2)
    receiver.terminate()
    
    print(f"\nSender exited with code: {result}")
    
    # Check if file was received
    output_file = "./received_videos/test_stream.mp4"
    if os.path.exists(output_file):
        size = os.path.getsize(output_file)
        print(f"SUCCESS! File received: {output_file}")
        print(f"File size: {size} bytes ({size/1024:.2f} KB)")
    else:
        print("ERROR: No file received")

if __name__ == "__main__":
    main()
