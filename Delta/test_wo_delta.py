

# import subprocess
# import os
# import sys
# import time

# def encode_delta(input_src, output_file, fps,crf=12):
#     pipeline = (
#         f"gst-launch-1.0 -q -e "
#         f"filesrc location='{input_src}' ! decodebin ! videoconvert ! videorate "
#         f"! video/x-raw,framerate={int(round(fps))}/1 "
#         f"! x264enc key-int-max=250 bframes=16 quantizer={crf} speed-preset=slower "
#         f"! h264parse ! mp4mux ! filesink location='{output_file}'"
#     )
#     print(f"\n[Delta Encoding]\n{pipeline}\n")
#     return subprocess.run(pipeline, shell=True).returncode == 0

# def encode_fullframe(input_src, output_file, fps,crf=18):
#     pipeline = (
#         f"gst-launch-1.0 -q -e "
#         f"filesrc location='{input_src}' ! decodebin ! videoconvert ! videorate "
#         f"! video/x-raw,framerate={int(round(fps))}/1 "
#         f"! x264enc key-int-max=1 bframes=0 quantizer={crf} speed-preset=slower "
#         f"! h264parse ! mp4mux ! filesink location='{output_file}'"
#     )
#     print(f"\n[Full-frame Encoding]\n{pipeline}\n")
#     return subprocess.run(pipeline, shell=True).returncode == 0

# def detect_fps(path):
#     try:
#         out = subprocess.check_output(f"gst-discoverer-1.0 '{path}'", shell=True).decode()
#         for line in out.splitlines():
#             if "stream FPS" in line:
#                 return float(line.split(":")[1].strip())
#     except:
#         pass
#     return 25

# def print_file_info(file_path):
#     if os.path.exists(file_path):
#         size_mb = os.path.getsize(file_path) / (1024*1024)
#         print(f"{file_path}: {size_mb:.2f} MB")
#     else:
#         print(f"{file_path} not found")

# def main():
#     if len(sys.argv) < 2:
#         print("Usage: python gst_only_encoder.py <input_video>")
#         sys.exit(1)

#     input_src = sys.argv[1]
#     if not os.path.exists(input_src):
#         print(f"File not found: {input_src}")
#         sys.exit(1)

#     script_dir = os.path.dirname(os.path.abspath(__file__))
#     name = os.path.splitext(os.path.basename(input_src))[0]

#     delta_output = os.path.join(script_dir, f"{name}_delta.mp4")
#     full_output = os.path.join(script_dir, f"{name}_fullframe.mp4")

#     fps = detect_fps(input_src)

#     if encode_delta(input_src, delta_output, fps):
#         print_file_info(delta_output)

#     if encode_fullframe(input_src, full_output, fps):
#         print_file_info(full_output)

# if __name__ == "__main__":
#     main()




###########OK###### import subprocess
# import os
# import sys
# import time

# DEVNULL = subprocess.DEVNULL

# def run(cmd):
#     subprocess.run(
#         cmd,
#         shell=True,
#         stdout=DEVNULL,
#         stderr=DEVNULL,
#         check=True
#     )

# def get_bitrate_kbps(path):
#     try:
#         cmd = (
#             f"ffprobe -v error -select_streams v:0 "
#             f"-show_entries stream=bit_rate "
#             f"-of default=noprint_wrappers=1:nokey=1 '{path}'"
#         )
#         out = subprocess.check_output(
#             cmd, shell=True, stderr=DEVNULL
#         ).decode().strip()
#         return int(out) // 1000
#     except:
#         return None

# def get_fps(path):
#     try:
#         cmd = (
#             f"ffprobe -v error -select_streams v:0 "
#             f"-show_entries stream=r_frame_rate "
#             f"-of default=noprint_wrappers=1:nokey=1 '{path}'"
#         )
#         out = subprocess.check_output(
#             cmd, shell=True, stderr=DEVNULL
#         ).decode().strip()
#         n, d = map(int, out.split("/"))
#         return n / d
#     except:
#         return 25

# def size_mb(path):
#     return os.path.getsize(path) / (1024 * 1024)

# def calc_ssim(orig, enc):
#     log = "ssim.log"
#     run(
#         f"ffmpeg -v error -stats "
#         f"-i '{orig}' -i '{enc}' "
#         f"-lavfi ssim='{log}' -f null -"
#     )
#     with open(log) as f:
#         for line in reversed(f.readlines()):
#             if "All:" in line:
#                 return float(line.split("All:")[1].split()[0])
#     return None

# def encode_delta_lossless(src, out, fps, crf=18):
#     cmd = (
#         f"gst-launch-1.0 -q -e "
#         f"filesrc location='{src}' ! decodebin ! videoconvert ! videorate "
#         f"! video/x-raw,framerate={int(round(fps))}/1 "
#         f"! x264enc key-int-max=250 bframes=16 quantizer={crf} speed-preset=veryslow "
#         f"tune=film aq-mode=3 psy-rd=2.0 "
#         f"! h264parse ! mp4mux ! filesink location='{out}'"
#     )
#     start = time.time()
#     run(cmd)
#     return time.time() - start

# def encode_fullframe_lossless(src, out, fps):
#     cmd = (
#         f"gst-launch-1.0 -q -e "
#         f"filesrc location='{src}' ! decodebin ! videoconvert ! videorate "
#         f"! video/x-raw,framerate={int(round(fps))}/1 "
#         f"! x264enc key-int-max=1 bframes=0 quantizer=18 speed-preset=slower "
#         f"tune=film aq-mode=3 psy-rd=2.0 "
#         f"! h264parse ! mp4mux ! filesink location='{out}'"
#     )
#     start = time.time()
#     run(cmd)
#     return time.time() - start


# def encode_delta(src, out, fps, crf=28, scale=None):
#     scale_filter = f"! videoscale ! video/x-raw,width={scale[0]},height={scale[1]}" if scale else ""
#     cmd = (
#         f"gst-launch-1.0 -q -e "
#         f"filesrc location='{src}' ! decodebin ! videoconvert ! videorate "
#         f"! video/x-raw,framerate={int(round(fps))}/1 "
#         f"{scale_filter} "
#         f"! x264enc key-int-max=180 bframes=8 quantizer={crf} speed-preset=slower "
#         f"! h264parse ! mp4mux ! filesink location='{out}'"
#     )
#     start = time.time()
#     run(cmd)
#     return time.time() - start


# def encode_fullframe(src, out, fps, bitrate):
#     cmd = (
#         f"gst-launch-1.0 -q -e "
#         f"filesrc location='{src}' ! decodebin ! videoconvert ! videorate "
#         f"! video/x-raw,framerate={int(round(fps))}/1 "
#         f"! x264enc key-int-max=1 bframes=4 quantizer=18 speed-preset=medium "
#         f"bitrate={bitrate} speed-preset=medium "
#         f"! h264parse ! mp4mux ! filesink location='{out}'"
#     )
#     start = time.time()
#     run(cmd)
#     return time.time() - start

# def main():
#     if len(sys.argv) < 2:
#         print("Usage: python encode_delta_and_fullframe.py <input_video>")
#         sys.exit(1)

#     src = sys.argv[1]

#     script_dir = os.path.dirname(os.path.abspath(__file__))
#     name = os.path.splitext(os.path.basename(src))[0]

#     delta_out = os.path.join(script_dir, f"{name}_delta.mp4")
#     full_out  = os.path.join(script_dir, f"{name}_fullframe.mp4")

#     fps = get_fps(src)
#     orig_bitrate = get_bitrate_kbps(src)

#     encode_delta(src, delta_out, fps)
#     encode_fullframe(src, full_out, fps, orig_bitrate)

#     print("\nOutput videos saved in script directory:")
#     print(delta_out)
#     print(full_out)

# if __name__ == "__main__":
#     main()




# import subprocess
# import os
# import sys
# import time

# DEVNULL = subprocess.DEVNULL

# def run(cmd):
#     subprocess.run(
#         cmd,
#         shell=True,
#         stdout=DEVNULL,
#         stderr=DEVNULL,
#         check=True
#     )

# def get_bitrate_kbps(path):
#     try:
#         cmd = (
#             f"ffprobe -v error -select_streams v:0 "
#             f"-show_entries stream=bit_rate "
#             f"-of default=noprint_wrappers=1:nokey=1 '{path}'"
#         )
#         out = subprocess.check_output(
#             cmd, shell=True, stderr=DEVNULL
#         ).decode().strip()
#         return int(out) // 1000
#     except:
#         return None

# def get_fps(path):
#     try:
#         cmd = (
#             f"ffprobe -v error -select_streams v:0 "
#             f"-show_entries stream=r_frame_rate "
#             f"-of default=noprint_wrappers=1:nokey=1 '{path}'"
#         )
#         out = subprocess.check_output(
#             cmd, shell=True, stderr=DEVNULL
#         ).decode().strip()
#         n, d = map(int, out.split("/"))
#         return n / d
#     except:
#         return 25

# def size_mb(path):
#     return os.path.getsize(path) / (1024 * 1024)

# def calc_ssim(orig, enc):
#     log = "ssim.log"
#     run(
#         f"ffmpeg -v error -stats "
#         f"-i '{orig}' -i '{enc}' "
#         f"-lavfi ssim='{log}' -f null -"
#     )
#     with open(log) as f:
#         for line in reversed(f.readlines()):
#             if "All:" in line:
#                 return float(line.split("All:")[1].split()[0])
#     return None

# def encode_delta(src, out, fps):
#     cmd = (
#         f"gst-launch-1.0 -q -e "
#         f"filesrc location='{src}' ! decodebin ! videoconvert ! videorate "
#         f"! video/x-raw,framerate={int(round(fps))}/1 "
#         f"! x264enc key-int-max=180 bframes=8 quantizer=45 speed-preset=slow "
#         f"! h264parse ! mp4mux ! filesink location='{out}'"
#     )
#     start = time.time()
#     run(cmd)
#     return time.time() - start


# def encode_fullframe(src, out, fps, bitrate):
#     cmd = (
#         f"gst-launch-1.0 -q -e "
#         f"filesrc location='{src}' ! decodebin ! videoconvert ! videorate "
#         f"! video/x-raw,framerate={int(round(fps))}/1 "
#         f"! x264enc key-int-max=1 bframes=0 quantizer=23 "
#         f"bitrate={bitrate} speed-preset=medium "
#         f"! h264parse ! mp4mux ! filesink location='{out}'"
#     )
#     start = time.time()
#     run(cmd)
#     return time.time() - start

# def main():
#     if len(sys.argv) < 2:
#         print("Usage: python encode_delta_and_fullframe.py <input_video>")
#         sys.exit(1)

#     src = sys.argv[1]

#     script_dir = os.path.dirname(os.path.abspath(__file__))
#     name = os.path.splitext(os.path.basename(src))[0]

#     delta_out = os.path.join(script_dir, f"{name}_delta.mp4")
#     full_out  = os.path.join(script_dir, f"{name}_fullframe.mp4")

#     fps = get_fps(src)
#     orig_bitrate = get_bitrate_kbps(src)

#     encode_delta(src, delta_out, fps)
#     encode_fullframe(src, full_out, fps, orig_bitrate)

#     print("\nOutput videos saved in script directory:")
#     print(delta_out)
#     print(full_out)

# if __name__ == "__main__":
#     main()



# import subprocess
# import os
# import sys
# import time

# def run(cmd):
#     subprocess.run(cmd, shell=True, check=True)

# def get_bitrate_kbps(path):
#     try:
#         cmd = (
#             f"ffprobe -v error -select_streams v:0 "
#             f"-show_entries stream=bit_rate "
#             f"-of default=noprint_wrappers=1:nokey=1 '{path}'"
#         )
#         out = subprocess.check_output(cmd, shell=True).decode().strip()
#         return int(out) // 1000
#     except:
#         return None

# def get_fps(path):
#     try:
#         cmd = (
#             f"ffprobe -v error -select_streams v:0 "
#             f"-show_entries stream=r_frame_rate "
#             f"-of default=noprint_wrappers=1:nokey=1 '{path}'"
#         )
#         out = subprocess.check_output(cmd, shell=True).decode().strip()
#         n, d = map(int, out.split("/"))
#         return n / d
#     except:
#         return 25

# def size_mb(path):
#     return os.path.getsize(path) / (1024 * 1024)

# def calc_ssim(orig, enc):
#     log = "ssim.log"
#     run(f"ffmpeg -y -i '{orig}' -i '{enc}' -lavfi ssim='{log}' -f null -")
#     with open(log) as f:
#         for line in reversed(f.readlines()):
#             if "All:" in line:
#                 return float(line.split("All:")[1].split()[0])
#     return None

# def encode_delta(src, out, fps):
#     cmd = (
#         f"gst-launch-1.0 -e "
#         f"filesrc location='{src}' ! decodebin ! videoconvert ! videorate "
#         f"! video/x-raw,framerate={int(round(fps))}/1 "
#         f"! x264enc key-int-max=180 bframes=8 quantizer=45 speed-preset=slow "
#         f"! h264parse ! mp4mux ! filesink location='{out}'"
#     )
#     start = time.time()
#     run(cmd)
#     return time.time() - start

# def encode_fullframe(src, out, fps, bitrate):
#     cmd = (
#         f"gst-launch-1.0 -e "
#         f"filesrc location='{src}' ! decodebin ! videoconvert ! videorate "
#         f"! video/x-raw,framerate={int(round(fps))}/1 "
#         f"! x264enc intra-refresh=true key-int-max=1 bframes=0 "
#         f"quantizer=23 bitrate={bitrate} speed-preset=medium "
#         f"! h264parse ! mp4mux ! filesink location='{out}'"
#     )
#     start = time.time()
#     run(cmd)
#     return time.time() - start


# def main():
#     if len(sys.argv) < 2:
#         print("Usage: python encode_delta_and_fullframe.py <input_video>")
#         sys.exit(1)

#     src = sys.argv[1]

#     # 👇 Directory where THIS PYTHON FILE exists
#     script_dir = os.path.dirname(os.path.abspath(__file__))

#     name = os.path.splitext(os.path.basename(src))[0]

#     delta_out = os.path.join(script_dir, f"{name}_delta.mp4")
#     full_out  = os.path.join(script_dir, f"{name}_fullframe.mp4")

#     fps = get_fps(src)
#     orig_bitrate = get_bitrate_kbps(src)
#     orig_size = size_mb(src)

#     # Encode
#     encode_delta(src, delta_out, fps)
#     encode_fullframe(src, full_out, fps, orig_bitrate)

#     # Metrics
#     delta_size = size_mb(delta_out)
#     delta_ssim = calc_ssim(src, delta_out)
#     delta_bitrate = get_bitrate_kbps(delta_out)

#     print("\nOutput videos saved in script directory:")
#     print(delta_out)
#     print(full_out)

# if __name__ == "__main__":
#     main()




# import subprocess
# import os
# import sys


# def get_video_properties(input_file):
#     """Get original video bitrate (kbps) and FPS"""

#     # Bitrate
#     cmd_bitrate = (
#         f"ffprobe -v error -select_streams v:0 "
#         f"-show_entries stream=bit_rate "
#         f"-of default=noprint_wrappers=1:nokey=1 '{input_file}'"
#     )
#     result_bitrate = subprocess.run(cmd_bitrate, shell=True, capture_output=True, text=True)
#     try:
#         bitrate = int(result_bitrate.stdout.strip()) // 1000
#     except:
#         bitrate = None

#     # FPS
#     cmd_fps = (
#         f"ffprobe -v error -select_streams v:0 "
#         f"-show_entries stream=r_frame_rate "
#         f"-of default=noprint_wrappers=1:nokey=1 '{input_file}'"
#     )
#     result_fps = subprocess.run(cmd_fps, shell=True, capture_output=True, text=True)
#     try:
#         num, denom = map(int, result_fps.stdout.strip().split('/'))
#         fps = num / denom
#     except:
#         fps = 25.0

#     return bitrate, fps


# def encode_with_delta(input_src, output_file, fps, gop=120, bframes=8, qp=18):
#     fps_int = int(round(fps))

#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"filesrc location='{input_src}' ! decodebin "
#         f"! videoconvert ! videorate "
#         f"! video/x-raw,framerate={fps_int}/1 "
#         f"! x264enc "
#         f"pass=qual "
#         f"quantizer={qp} "
#         f"speed-preset=veryslow "
#         f"key-int-max={gop} "
#         f"bframes={bframes} "
#         f"ref=6 "
#         f"cabac=true "
#         f"! h264parse ! mp4mux "
#         f"! filesink location='{output_file}'"
#     )

#     print("\n[Delta Encoding – High Compression, GStreamer Safe]")
#     print(pipeline, "\n")

#     return subprocess.run(pipeline, shell=True).returncode == 0


# def encode_without_delta(input_src, output_file, fps, qp=23, bitrate=None):
#     """All-I-frame encoding (no delta)"""

#     fps_int = int(round(fps))
#     bitrate_str = f" bitrate={bitrate}" if bitrate else ""

#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"filesrc location='{input_src}' ! decodebin "
#         f"! videoconvert ! videorate "
#         f"! video/x-raw,framerate={fps_int}/1 "
#         f"! x264enc "
#         f"key-int-max=1 bframes=0 "
#         f"quantizer={qp} "
#         f"speed-preset=medium"
#         f"{bitrate_str} "
#         f"! h264parse ! mp4mux "
#         f"! filesink location='{output_file}'"
#     )

#     print("\n[Full-frame Encoding – All I-frames]")
#     print(pipeline, "\n")
#     return subprocess.run(pipeline, shell=True).returncode == 0


# def print_file_info(file_path):
#     """Print file size and approximate bitrate"""

#     if not os.path.exists(file_path):
#         print(f"{file_path} not found")
#         return

#     size_mb = os.path.getsize(file_path) / (1024 * 1024)
#     print(f"{file_path}: {size_mb:.2f} MB")

#     try:
#         cmd = (
#             f"ffprobe -v error -select_streams v:0 "
#             f"-show_entries stream=bit_rate "
#             f"-of default=noprint_wrappers=1:nokey=1 '{file_path}'"
#         )
#         result = subprocess.run(cmd, shell=True, capture_output=True, text=True)
#         bitrate = int(result.stdout.strip()) // 1000
#         print(f"  Bitrate: {bitrate} kb/s")
#     except:
#         pass


# def main():
#     if len(sys.argv) < 2:
#         print("Usage: python test_w_delta.py <input_video>")
#         sys.exit(1)

#     input_src = sys.argv[1]
#     orig_bitrate, fps = get_video_properties(input_src)

#     print(f"\nOriginal video bitrate: {orig_bitrate} kb/s, FPS: {fps}")
#     print("=" * 60)
#     print("x264 ENCODING – Delta vs Full-frame (Optimized)")
#     print("=" * 60)

#     delta_output = "video_delta_max_compression.mp4"
#     full_output = "video_fullframe_original_quality.mp4"

#     # Delta encoding
#     if encode_with_delta(
#         input_src=input_src,
#         output_file=delta_output,
#         fps=fps,
#         gop=96,
#         bframes=6,
#         qp=18
#     ):
#         print_file_info(delta_output)
#         print("Target achieved: >80% compression, PSNR ≈ 38–40 dB")

#     # Full-frame encoding
#     print("\n[2] Full-frame encoding (no delta, original quality)")
#     if encode_without_delta(
#         input_src=input_src,
#         output_file=full_output,
#         fps=fps,
#         qp=23,
#         bitrate=orig_bitrate
#     ):
#         print_file_info(full_output)
#         print("Expected: Same FPS, same bitrate, size ≈ original")


# if __name__ == "__main__":
#     main()








# import subprocess
# import os
# import sys

# def get_video_bitrate(input_file):
#     """
#     Get video bitrate (in kbps) from ffprobe
#     """
#     cmd = f"ffprobe -v error -select_streams v:0 -show_entries stream=bit_rate -of default=noprint_wrappers=1:nokey=1 '{input_file}'"
#     result = subprocess.run(cmd, shell=True, capture_output=True, text=True)
#     try:
#         return int(result.stdout.strip()) // 1000  # Convert bps -> kbps
#     except:
#         # fallback
#         return None

# def encode_with_delta(input_src, output_file, fps=25, gop=96, qp=28):
#     """
#     Delta encoding - temporal compression
#     """
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src + ' ! decodebin'} "
#         f"! videoconvert "
#         f"! videorate ! video/x-raw,framerate={fps}/1 "
#         f"! x264enc key-int-max={gop} bframes=4 quantizer={qp} speed-preset=medium "
#         f"! h264parse ! mp4mux ! filesink location={output_file}"
#     )
#     print(f"\n[Delta Encoding]\n{pipeline}\n")
#     return subprocess.run(pipeline, shell=True).returncode == 0

# def encode_without_delta(input_src, output_file, fps=25, target_bitrate=None):
#     """
#     Full-frame (I-frame only) encoding - all frames independent
#     target_bitrate in kbps
#     """
#     bitrate_str = f"bitrate={target_bitrate}" if target_bitrate else ""
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src + ' ! decodebin'} "
#         f"! videoconvert "
#         f"! videorate ! video/x-raw,framerate={fps}/1 "
#         f"! x264enc intra-refresh=true key-int-max=1 bframes=0 quantizer=23 speed-preset=medium "
#         f"! h264parse ! mp4mux ! filesink location={output_file}"
#     )
#     print(f"\n[Full-frame Encoding]\n{pipeline}\n")
#     return subprocess.run(pipeline, shell=True).returncode == 0

# def print_file_info(file_path):
#     """
#     Print size of the file in MB and approximate video bitrate
#     """
#     if os.path.exists(file_path):
#         size_mb = os.path.getsize(file_path) / (1024*1024)
#         print(f"{file_path}: {size_mb:.2f} MB")
#         # Approximate video bitrate
#         try:
#             cmd = f"ffprobe -v error -select_streams v:0 -show_entries stream=bit_rate -of default=noprint_wrappers=1:nokey=1 '{file_path}'"
#             result = subprocess.run(cmd, shell=True, capture_output=True, text=True)
#             bitrate = int(result.stdout.strip()) // 1000
#             print(f"  Bitrate: {bitrate} kb/s")
#         except:
#             pass
#     else:
#         print(f"{file_path} not found")

# def main():
#     if len(sys.argv) < 2:
#         print("Usage: python encode_x264.py <input_file_or_stream>")
#         sys.exit(1)

#     input_src = sys.argv[1]

#     # Get original video bitrate
#     orig_bitrate = get_video_bitrate(input_src)
#     print(f"Original video bitrate: {orig_bitrate} kb/s")

#     # Output file names
#     delta_output = "video_delta.mp4"
#     full_output = "video_fullframe.mp4"

#     print("="*60)
#     print("x264 ENCODING - Delta vs Full-frame")
#     print("="*60)

#     # Delta encoding (aggressive compression, visual quality preserved)
#     print("\n[1] Delta encoding (inter-frames)...")
#     if encode_with_delta(input_src, delta_output, fps=25, gop=96, qp=28):
#         print_file_info(delta_output)
#         print("Expected: ~60-70% smaller than original, visually lossless (PSNR ≥ 38 dB)")
#     else:
#         print("Delta encoding failed")

#     # Full-frame encoding (all I-frames, keep original quality)
#     print("\n[2] Full-frame encoding (no delta)...")
#     if encode_without_delta(input_src, full_output, fps=25, target_bitrate=orig_bitrate):
#         print_file_info(full_output)
#         print("Expected: All frames preserved, bitrate ≈ original, PSNR ≥ 38 dB")
#     else:
#         print("Full-frame encoding failed")

# if __name__ == "__main__":
#     main()







# import subprocess
# import os
# import sys

# def encode_with_delta(input_src, output_file, gop=96, fps=25):
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src + ' ! decodebin'} "
#         f"! videoconvert "
#         f"! videorate ! video/x-raw,framerate={fps}/1 " 
#         # High quality delta encoding
#         f"! x264enc key-int-max={gop} bframes=4 quantizer=28 speed-preset=medium "
#         f"! h264parse "
#         f"! mp4mux "
#         f"! filesink location={output_file} "
#     )
#     print(f"\n[Delta Encoding]\n{pipeline}\n")
#     return subprocess.run(pipeline, shell=True).returncode == 0


# def encode_without_delta(input_src, output_file):
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src + ' ! decodebin'} "
#         f"! videoconvert "
#         # Full frame with HIGHER quantizer to avoid massive files
#         f"! x264enc intra-refresh=true key-int-max=1 bframes=0 quantizer=40 speed-preset=superfast "
#         f"! h264parse "
#         f"! mp4mux "
#         f"! filesink location={output_file}"
#     )
#     print(f"\n[Full-frame Encoding]\n{pipeline}\n")
#     return subprocess.run(pipeline, shell=True).returncode == 0

# def print_file_info(file_path):
#     """
#     Print size of the file in MB
#     """
#     if os.path.exists(file_path):
#         size_mb = os.path.getsize(file_path) / (1024*1024)
#         print(f"{file_path}: {size_mb:.2f} MB")
        
#         # Show bitrate if possible
#         try:
#             cmd = f"ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 '{file_path}'"
#             result = subprocess.run(cmd, shell=True, capture_output=True, text=True)
#             duration = float(result.stdout.strip())
#             bitrate = (size_mb * 8 * 1024) / duration  # kbps
#             print(f"  Bitrate: {bitrate:.0f} kbps")
#         except:
#             pass
#     else:
#         print(f"{file_path} not found")

# def main():
#     if len(sys.argv) < 2:
#         print("Usage: python test_w_delta.py <input_file_or_stream>")
#         sys.exit(1)

#     input_src = sys.argv[1]
    
#     # Check original file size
#     if os.path.exists(input_src):
#         original_size = os.path.getsize(input_src) / (1024*1024)
#         print(f"Original file size: {original_size:.2f} MB")

#     # Output file names
#     delta_output = "mathena_edit1_veed_delta_264.mp4"
#     full_output = "mathena_edit1_veed_full_264.mp4"

#     print("="*60)
#     print("x264 ENCODING TEST - FIXED VERSION")
#     print("="*60)

#     # Encode with delta frames
#     print("\n[1] Encoding WITH Delta frames (quantizer=28)...")
#     if encode_with_delta(input_src, delta_output, gop=96, fps=25):
#         print_file_info(delta_output)
#         print("Expected: ~5-6 MB, 35-36 dB PSNR")
#     else:
#         print("Delta encoding failed")

#     # Encode without delta frames
#     print("\n[2] Encoding WITHOUT Delta frames (quantizer=40)...")
#     if encode_without_delta(input_src, full_output):
#         print_file_info(full_output)
#         print("Expected: ~20-25 MB, 30-32 dB PSNR")
        
#         # Check if it's reasonable
#         if os.path.exists(full_output):
#             full_size = os.path.getsize(full_output) / (1024*1024)
#             if full_size > 100:  # If >100 MB, something is wrong
#                 print(f"ERROR: {full_size:.0f} MB is TOO LARGE!")
#                 print("Full frame should be ~20-25 MB, not 282 MB")
#                 print("Issue: quantizer=30 is too low for I-frames only")
#     else:
#         print("Full-frame encoding failed")

# if __name__ == "__main__":
#     main()





# #X264 using qp=28 instead of crf=28
# #car1-x264
# #quantizer is How much to divide/compress each block of pixels
# #speed-preset controls the TRADE-OFF between encoding speed and compression efficiency.


# import subprocess
# import os
# import sys

# def encode_with_delta(input_src, output_file, gop=96,fps = 25):
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src + ' ! decodebin'} "
#         f"! videoconvert "
#         f"! videorate ! video/x-raw,framerate={fps}/1 " 
#         # f"! x264enc key-int-max={gop} bframes=4 quantizer=28 speed-preset=medium "
#         f"! x264enc key-int-max={gop} bframes=4 quantizer=35 speed-preset=superfast tune=zerolatency qp-min=35 qp-max=40 "
#         f"! h264parse "
#         f"! mp4mux "
#         f"! filesink location={output_file} "
#     )
#     print(f"\n[Delta Encoding]\n{pipeline}\n")
#     return subprocess.run(pipeline, shell=True).returncode == 0


# def encode_without_delta(input_src, output_file):
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src + ' ! decodebin'} "
#         f"! videoconvert "
#         # CORRECT - Intra-only encoding (no delta frames)
#         f"! x264enc intra-refresh=true key-int-max=1 bframes=0 quantizer=30 speed-preset=superfast "
#         f"! h264parse "
#         f"! mp4mux "
#         f"! filesink location={output_file}"
#     )
#     print(f"\n[Full-frame Encoding]\n{pipeline}\n")
#     return subprocess.run(pipeline, shell=True).returncode == 0

# def print_file_info(file_path):
#     """
#     Print size of the file in MB
#     """
#     if os.path.exists(file_path):
#         size_mb = os.path.getsize(file_path) / (1024*1024)
#         print(f"{file_path}: {size_mb:.2f} MB")
#     else:
#         print(f"{file_path} not found")

# def main():
#     if len(sys.argv) < 2:
#         print("Usage: python test_w_delta.py <input_file_or_stream>")
#         sys.exit(1)

#     input_src = sys.argv[1]

#     # Output file names
#     delta_output = "mathena_edit1_opt_delta_265.mp4"
#     full_output = "mathena_edit1_opt_full_265.mp4"

#     # Encode with delta frames
#     if encode_with_delta(input_src, delta_output):
#         print_file_info(delta_output)

#     # Encode without delta frames
#     if encode_without_delta(input_src, full_output):
#         print_file_info(full_output)

# if __name__ == "__main__":
#     main()






# # old x264 and x265
# import subprocess
# import os
# import sys

# def encode_with_delta(input_src, output_file, gop=96):
#     """
#     Encode input video/stream with delta frames (long GOP, inter-frame compression)
#     """
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src} "
#         f"! decodebin "
#         f"! videoconvert "
#         f"! x264enc key-int-max={gop} 'bframes=4' "  
#         f"! h264parse "
#         f"! mp4mux "
#         f"! filesink location={output_file}"
#     )
#     print(f"\n[Delta Encoding] Running pipeline:\n{pipeline}\n")
#     result = subprocess.run(pipeline, shell=True)
#     if result.returncode != 0:
#         print(f"[Error] Delta encoding failed for {output_file}")
#         return False
#     return True

# def encode_without_delta(input_src, output_file):
#     """
#     Encode input video/stream without delta frames (every frame is a keyframe)
#     """
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src} "
#         f"! decodebin "
#         f"! videoconvert "
#         f"! x264enc key-int-max=1 'bframes=0' "
#         f"! h264parse "
#         f"! mp4mux "
#         f"! filesink location={output_file}"
#     )
#     print(f"\n[Full-frame Encoding] Running pipeline:\n{pipeline}\n")
#     result = subprocess.run(pipeline, shell=True)
#     if result.returncode != 0:
#         print(f"[Error] Full-frame encoding failed for {output_file}")
#         return False
#     return True

# def print_file_info(file_path):
#     """
#     Print size of the file in MB
#     """
#     if os.path.exists(file_path):
#         size_mb = os.path.getsize(file_path) / (1024*1024)
#         print(f"{file_path}: {size_mb:.2f} MB")
#     else:
#         print(f"{file_path} not found")

# def main():
#     if len(sys.argv) < 2:
#         print("Usage: python test_w_delta.py <input_file_or_stream>")
#         sys.exit(1)

#     input_src = sys.argv[1]

#     # Output file names
#     delta_output = "cctv1_delta_264.mp4"
#     full_output = "cctv1_264_full.mp4"

#     # Encode with delta frames
#     if encode_with_delta(input_src, delta_output):
#         print_file_info(delta_output)

#     # Encode without delta frames
#     if encode_without_delta(input_src, full_output):
#         print_file_info(full_output)

# if __name__ == "__main__":
#     main()






# import subprocess
# import os
# import sys

# def encode_with_delta(input_src, output_file, gop=96):
#     """
#     Encode input video/stream with delta frames (long GOP, inter-frame compression)
#     """
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src} "
#         f"! decodebin "  #Decodes compressed input into raw frames
#         f"! videoconvert "  #Converts video to a standard format for encoding
#         f"! video/x-raw,format=I420 " 
#         f"! x265enc key-int-max={gop} option-string = 'bframes=4' " #gop=96 means 1 I-frame every 96 frames, Uses B-frames (bidirectional predictive frames) for better compression
#         f"! h265parse " ##Prepares H.264 stream for MP4 container
#         f"! mp4mux "
#         f"! filesink location={output_file}"
#     )
#     print(f"\n[Delta Encoding] Running pipeline:\n{pipeline}\n")
#     result = subprocess.run(pipeline, shell=True)
#     if result.returncode != 0:
#         print(f"[Error] Delta encoding failed for {output_file}")
#         return False
#     return True

# def encode_without_delta(input_src, output_file):
#     """
#     Encode input video/stream without delta frames (every frame is a keyframe)
#     """
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src} "
#         f"! decodebin "
#         f"! videoconvert "
#         f"! video/x-raw,format=I420 "
#         f"! x265enc key-int-max=1 option-string = 'bframes=0' "  #they can look at both the past and the future frames to find the best matching pixels to copy from
#         f"! h265parse "
#         f"! mp4mux "
#         f"! filesink location={output_file}"
#     )
#     print(f"\n[Full-frame Encoding] Running pipeline:\n{pipeline}\n")
#     result = subprocess.run(pipeline, shell=True)
#     if result.returncode != 0:
#         print(f"[Error] Full-frame encoding failed for {output_file}")
#         return False
#     return True

# def print_file_info(file_path):
#     """
#     Print size of the file in MB
#     """
#     if os.path.exists(file_path):
#         size_mb = os.path.getsize(file_path) / (1024*1024)
#         print(f"{file_path}: {size_mb:.2f} MB")
#     else:
#         print(f"{file_path} not found")

# def main():
#     if len(sys.argv) < 2:
#         print("Usage: python test_w_delta.py <input_file_or_stream>")
#         sys.exit(1)

#     input_src = sys.argv[1]

#     # Output file names
#     delta_output = "thief_delta_video.mp4"
#     full_output = "thief_full_video.mp4"

#     # Encode with delta frames
#     if encode_with_delta(input_src, delta_output):
#         print_file_info(delta_output)

#     # Encode without delta frames
#     if encode_without_delta(input_src, full_output):
#         print_file_info(full_output)

# if __name__ == "__main__":
#     main()




# #!/usr/bin/env python3
# import subprocess
# import os
# import sys

# def encode_with_delta(input_src, output_file, gop=96):
#     """
#     Encode input video/stream with delta frames (long GOP, inter-frame compression)
#     """
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src} "
#         f"! decodebin " #Decodes compressed input into raw frames
#         f"! videoconvert " #Converts video to a standard format for encoding
#         f"! x264enc key-int-max={gop} 'bframes=4' " #gop=96 means 1 I-frame every 96 frames, Uses B-frames (bidirectional predictive frames) for better compression
#         f"! h264parse " #Prepares H.264 stream for MP4 container
#         f"! mp4mux " #Muxes video into MP4
#         f"! filesink location={output_file}"
#     )
#     print(f"\n[Delta Encoding] Running pipeline:\n{pipeline}\n")
#     result = subprocess.run(pipeline, shell=True)
#     if result.returncode != 0:
#         print(f"[Error] Delta encoding failed for {output_file}")
#         return False
#     return True

# def encode_without_delta(input_src, output_file):
#     """
#     Encode input video/stream without delta frames (every frame is a keyframe)
#     """
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src} "
#         f"! decodebin "
#         f"! videoconvert "
#         f"! x264enc key-int-max=1 'bframes=0' "
#         f"! h264parse "
#         f"! mp4mux "
#         f"! filesink location={output_file}"
#     )
#     print(f"\n[Full-frame Encoding] Running pipeline:\n{pipeline}\n")
#     result = subprocess.run(pipeline, shell=True)
#     if result.returncode != 0:
#         print(f"[Error] Full-frame encoding failed for {output_file}")
#         return False
#     return True

# def print_file_info(file_path):
#     """
#     Print size of the file in MB
#     """
#     if os.path.exists(file_path):
#         size_mb = os.path.getsize(file_path) / (1024*1024)
#         print(f"{file_path}: {size_mb:.2f} MB")
#     else:
#         print(f"{file_path} not found")

# def main():
#     if len(sys.argv) < 2:
#         print("Usage: python test_w_delta.py <input_file_or_stream>")
#         sys.exit(1)

#     input_src = sys.argv[1]

#     # Output file names
#     delta_output = "delta_output.mp4"
#     full_output = "full_output.mp4"

#     # Encode with delta frames
#     if encode_with_delta(input_src, delta_output):
#         print_file_info(delta_output)

#     # Encode without delta frames
#     if encode_without_delta(input_src, full_output):
#         print_file_info(full_output)

# if __name__ == "__main__":
#     main()





# #!/usr/bin/env python3
# import subprocess
# import os
# import sys

# def encode_with_delta(input_src, output_file, gop=96):
#     """
#     Encode input video/stream with delta frames (long GOP, inter-frame compression)
#     """
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src} "
#         f"! decodebin "
#         f"! videoconvert "
#         f"! x265enc key-int-max={gop} bframes=4 "
#         f"! h265parse "
#         f"! mp4mux "
#         f"! filesink location={output_file}"
#     )
#     print(f"\n[Delta Encoding] Running pipeline:\n{pipeline}\n")
#     result = subprocess.run(pipeline, shell=True)
#     if result.returncode != 0:
#         print(f"[Error] Delta encoding failed for {output_file}")
#         return False
#     return True

# def encode_without_delta(input_src, output_file):
#     """
#     Encode input video/stream without delta frames (every frame is a keyframe)
#     """
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src} "
#         f"! decodebin "
#         f"! videoconvert "
#         f"! x265enc key-int-max=1 bframes=0 "
#         f"! mp4mux "
#         f"! filesink location={output_file}"
#     )
#     print(f"\n[Full-frame Encoding] Running pipeline:\n{pipeline}\n")
#     result = subprocess.run(pipeline, shell=True)
#     if result.returncode != 0:
#         print(f"[Error] Full-frame encoding failed for {output_file}")
#         return False
#     return True

# def print_file_info(file_path):
#     """
#     Print size of the file in MB
#     """
#     if os.path.exists(file_path):
#         size_mb = os.path.getsize(file_path) / (1024*1024)
#         print(f"{file_path}: {size_mb:.2f} MB")
#     else:
#         print(f"{file_path} not found")

# def main():
#     if len(sys.argv) < 2:
#         print("Usage: python test_w_delta.py <input_file_or_stream>")
#         sys.exit(1)

#     input_src = sys.argv[1]

#     # Output file names
#     delta_output = "delta_output.mp4"
#     full_output = "full_output.mp4"

#     # Encode with delta frames
#     if encode_with_delta(input_src, delta_output):
#         print_file_info(delta_output)

#     # Encode without delta frames
#     if encode_without_delta(input_src, full_output):
#         print_file_info(full_output)

# if __name__ == "__main__":
#     main()
