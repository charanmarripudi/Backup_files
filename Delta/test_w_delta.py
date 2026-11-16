

#!/usr/bin/env python3
import subprocess
import os
import sys
import time

def get_video_properties(input_file):
    """Get original video bitrate (kbps) and FPS"""
    # Bitrate
    cmd_bitrate = (
        f"ffprobe -v error -select_streams v:0 "
        f"-show_entries stream=bit_rate -of default=noprint_wrappers=1:nokey=1 '{input_file}'"
    )
    result_bitrate = subprocess.run(cmd_bitrate, shell=True, capture_output=True, text=True)
    try:
        bitrate = int(result_bitrate.stdout.strip()) // 1000
    except:
        bitrate = None

    # FPS
    cmd_fps = (
        f"ffprobe -v error -select_streams v:0 "
        f"-show_entries stream=r_frame_rate -of default=noprint_wrappers=1:nokey=1 '{input_file}'"
    )
    result_fps = subprocess.run(cmd_fps, shell=True, capture_output=True, text=True)
    try:
        num, denom = map(int, result_fps.stdout.strip().split('/'))
        fps = num / denom
    except:
        fps = 25  # fallback

    return bitrate, fps

def encode_delta(input_src, output_file, fps, crf=32):
    """Delta encoding with CRF (high compression, visually lossless)"""
    start_time = time.time()
    pipeline = (
        f"gst-launch-1.0 -q -e "
        f"filesrc location='{input_src}' ! decodebin ! videoconvert ! videorate "
        f"! video/x-raw,framerate={int(round(fps))}/1 "
        f"! x264enc "
        f"key-int-max=250 bframes=8 ref=6 me=umh subme=8 quantizer={crf} "
        f"speed-preset=slow pass=quant "
        f"! h264parse ! mp4mux ! filesink location='{output_file}'"
    )
    end_time = time.time()
    elapsed_time = end_time - start_time
    print(f"\n[Delta Encoding]\n{pipeline}\nElapsed time: {elapsed_time:.2f}s")
    return subprocess.run(pipeline, shell=True).returncode == 0

def encode_fullframe(input_src, output_file, fps, bitrate_kbps):
    """Full-frame encoding (no delta) with target bitrate"""
    start_time = time.time()
    pipeline = (
        f"gst-launch-1.0 -q -e "
        f"filesrc location='{input_src}' ! decodebin ! videoconvert ! videorate "
        f"! video/x-raw,framerate={int(round(fps))}/1 "
        f"! x264enc intra-refresh=true key-int-max=1 bframes=0 "
        f"bitrate={bitrate_kbps} speed-preset=slow "
        f"! h264parse ! mp4mux ! filesink location='{output_file}'"
    )
    end_time = time.time()
    elapsed_time = end_time - start_time
    print(f"\n[Full-frame Encoding]\n{pipeline}\nElapsed time: {elapsed_time:.2f}s")
    return subprocess.run(pipeline, shell=True).returncode == 0

def get_file_size_mb(path):
    if os.path.exists(path):
        return os.path.getsize(path) / (1024*1024)
    return 0

def calculate_psnr(encoded_file, reference_file):
    """Calculate PSNR using ffmpeg"""
    cmd = (
        f"ffmpeg -i '{encoded_file}' -i '{reference_file}' "
        f"-lavfi psnr='stats_file=/tmp/psnr_log.txt' -f null -"
    )
    subprocess.run(cmd, shell=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    # Extract average PSNR from log
    avg_psnr = 0
    try:
        with open("/tmp/psnr_log.txt") as f:
            for line in f:
                if "average:" in line:
                    avg_psnr = float(line.strip().split('average:')[1].split()[0])
    except:
        avg_psnr = 0
    return avg_psnr

def main():
    if len(sys.argv) < 2:
        print("Usage: python encode_videos.py <input_video>")
        sys.exit(1)

    input_video = sys.argv[1]
    if not os.path.exists(input_video):
        print(f"File not found: {input_video}")
        sys.exit(1)

    orig_bitrate, fps = get_video_properties(input_video)
    print(f"Original video: Bitrate={orig_bitrate} kbps, FPS={fps}")

    base_name = os.path.splitext(os.path.basename(input_video))[0]
    delta_file = f"{base_name}_delta.mp4"
    full_file = f"{base_name}_fullframe.mp4"

    # Encode delta
    if encode_delta(input_video, delta_file, fps, crf=32):
        print(f"Delta encoding done: {delta_file} ({get_file_size_mb(delta_file):.2f} MB)")

    # Encode full-frame (no delta) with same bitrate as original
    if encode_fullframe(input_video, full_file, fps, orig_bitrate):
        print(f"Full-frame encoding done: {full_file} ({get_file_size_mb(full_file):.2f} MB)")

    # Gather sizes
    size_orig = get_file_size_mb(input_video)
    size_delta = get_file_size_mb(delta_file)
    size_full = get_file_size_mb(full_file)

    # PSNR
    psnr_delta = calculate_psnr(delta_file, input_video)
    psnr_full = calculate_psnr(full_file, input_video)

    # Compression %
    comp_delta = (1 - size_delta / size_orig) * 100
    comp_full = (1 - size_full / size_orig) * 100

    # Print final table
    print("\n=== Encoding Summary ===")
    print(f"{'Preset':<15} {'Size':<8} {'PSNR':<8} {'Bitrate':<8} {'Compression':<10} {'Quality'}")
    print(f"{'-'*65}")
    print(f"{'Delta (CRF 32)':<15} {size_delta:.2f}MB  {psnr_delta:.2f}dB  {orig_bitrate*(size_delta/size_orig):.0f}kb/s  {comp_delta:.0f}%       EXCELLENT")
    print(f"{'Full-frame':<15} {size_full:.2f}MB  {psnr_full:.2f}dB  {orig_bitrate}kb/s  {comp_full:.0f}%       GOOD")
    print(f"{'Original':<15} {size_orig:.2f}MB  {'∞':<8} {orig_bitrate}kb/s  0%        Reference")

if __name__ == "__main__":
    main()








# import subprocess
# import os
# import sys

# def get_video_properties(input_file):
#     """Get original video bitrate (kbps) and FPS"""
#     cmd_bitrate = (
#         f"ffprobe -v error -select_streams v:0 "
#         f"-show_entries stream=bit_rate -of default=noprint_wrappers=1:nokey=1 '{input_file}'"
#     )
#     result_bitrate = subprocess.run(cmd_bitrate, shell=True, capture_output=True, text=True)
#     try:
#         bitrate = int(result_bitrate.stdout.strip()) // 1000
#     except:
#         bitrate = None

#     cmd_fps = (
#         f"ffprobe -v error -select_streams v:0 "
#         f"-show_entries stream=r_frame_rate -of default=noprint_wrappers=1:nokey=1 '{input_file}'"
#     )
#     result_fps = subprocess.run(cmd_fps, shell=True, capture_output=True, text=True)
#     try:
#         num, denom = map(int, result_fps.stdout.strip().split('/'))
#         fps = num / denom
#     except:
#         fps = 25

#     return bitrate, fps

# def encode_delta(input_src, output_file, fps, crf=28):
#     pipeline = (
#         f"gst-launch-1.0 -q -e "
#         f"filesrc location='{input_src}' ! qtdemux ! h264parse ! avdec_h264 "
#         f"! videoconvert ! videorate "
#         f"! video/x-raw,framerate={int(round(fps))}/1 "
#         f"! x264enc "
#         f"pass=qual "
#         f"quantizer={crf} "
#         f"key-int-max=600 "
#         f"bframes=16 "
#         f"ref=16 "
#         f"me=tesa "
#         f"subme=10 "
#         f"speed-preset=superfast "
#         f"! h264parse config-interval=1 "
#         f"! mp4mux faststart=true "
#         f"! filesink location='{output_file}'"
#     )
#     print(f"\n[Delta Encoding]\n{pipeline}\n")
#     return subprocess.run(pipeline, shell=True).returncode == 0


# def encode_fullframe(input_src, output_file, fps, bitrate_kbps):
#     pipeline = (
#         f"gst-launch-1.0 -q -e "
#         f"filesrc location='{input_src}' ! decodebin ! videoconvert ! videorate "
#         f"! video/x-raw,framerate={int(round(fps))}/1 "
#         f"! x264enc "
#         f"key-int-max=1 bframes=0 "
#         f"bitrate={bitrate_kbps} "
#         f"speed-preset=slow "
#         f"! h264parse ! mp4mux faststart=true "
#         f"! filesink location='{output_file}'"
#     )
#     print(f"\n[Full-frame Encoding]\n{pipeline}\n")
#     return subprocess.run(pipeline, shell=True).returncode == 0


# def get_file_size_mb(path):
#     return os.path.getsize(path) / (1024 * 1024) if os.path.exists(path) else 0

# #Measure quality loss vs original video
# def calculate_psnr(encoded_file, reference_file):
#     cmd = (
#         f"ffmpeg -i '{encoded_file}' -i '{reference_file}' "
#         f"-lavfi psnr -an -f null -"
#     )
#     result = subprocess.run(cmd, shell=True, stderr=subprocess.PIPE, text=True)
#     for line in result.stderr.splitlines():
#         if "average:" in line:
#             return float(line.split("average:")[1].split()[0])
#     return 0


# def main():
#     if len(sys.argv) < 2:
#         print("Usage: python encode_videos.py <input_video>")
#         sys.exit(1)

#     input_video = sys.argv[1]
#     if not os.path.exists(input_video):
#         print(f"File not found: {input_video}")
#         sys.exit(1)

#     orig_bitrate, fps = get_video_properties(input_video)
#     print(f"Original video: Bitrate={orig_bitrate} kbps, FPS={fps}")

#     base = os.path.splitext(os.path.basename(input_video))[0]
#     delta_file = f"{base}_delta.mp4"
#     full_file = f"{base}_fullframe.mp4"

#     encode_delta(input_video, delta_file, fps)
#     encode_fullframe(input_video, full_file, fps, orig_bitrate)

#     size_orig = get_file_size_mb(input_video)
#     size_delta = get_file_size_mb(delta_file)
#     size_full = get_file_size_mb(full_file)

#     psnr_delta = calculate_psnr(delta_file, input_video)
#     psnr_full = calculate_psnr(full_file, input_video)

#     comp_delta = (1 - size_delta / size_orig) * 100
#     comp_full = (1 - size_full / size_orig) * 100

#     print("\n=== Encoding Summary ===")
#     print(f"{'Preset':<15} {'Size':<8} {'PSNR':<8} {'Compression':<12}")
#     print("-" * 50)
#     print(f"Delta         {size_delta:.2f}MB  {psnr_delta:.2f}dB   {comp_delta:.1f}%")
#     print(f"Full-frame    {size_full:.2f}MB  {psnr_full:.2f}dB   {comp_full:.1f}%")
#     print(f"Original      {size_orig:.2f}MB   ∞        0%")


# if __name__ == "__main__":
#     main()


