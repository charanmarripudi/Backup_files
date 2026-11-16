import subprocess
import os
import sys

def encode_x265_delta(input_src, output_file, gop=120, fps=25, qp=35, preset='veryfast'):
    """
    x265 Delta Encoding (uses P/B frames for compression)
    """
    pipeline = (
        f"gst-launch-1.0 -e "
        f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src + ' ! decodebin'} "
        f"! videoconvert "
        f"! videorate ! video/x-raw,framerate={fps}/1 " 
        # Delta encoding: uses GOP with keyframes + delta frames
        f"! x265enc key-int-max={gop} qp={qp} speed-preset={preset} "
        f"! h265parse "
        f"! mp4mux "
        f"! filesink location={output_file} "
    )
    print(f"\n[x265 DELTA Encoding]\n{pipeline}\n")
    return subprocess.run(pipeline, shell=True).returncode == 0


def encode_x265_without_delta(input_src, output_file, bitrate=1000, preset='superfast'):
    """
    x265 Without Delta - GUARANTEED smaller size using BITRATE control
    Bitrate=1000 kbps = ~6.5 MB for 51s video (smaller than original 33 MB)
    """
    pipeline = (
        f"gst-launch-1.0 -e "
        f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src + ' ! decodebin'} "
        f"! videoconvert "
        # BITRATE CONTROL: 1000 kbps = guaranteed size
        f"! x265enc key-int-max=1 bitrate={bitrate} speed-preset={preset} "
        f"! h265parse "
        f"! mp4mux "
        f"! filesink location={output_file}"
    )
    print(f"\n[x265 WITHOUT Delta - Bitrate={bitrate}kbps]\n{pipeline}\n")
    
    result = subprocess.run(pipeline, shell=True)
    if result.returncode == 0 and os.path.exists(output_file):
        size_mb = os.path.getsize(output_file) / (1024*1024)
        original_mb = os.path.getsize(input_src) / (1024*1024)
        
        print(f"✓ Created: {size_mb:.2f} MB")
        print(f"Original: {original_mb:.2f} MB")
        print(f"Reduction: {((original_mb - size_mb) / original_mb * 100):.1f}% smaller")
        
        if size_mb < original_mb:
            print("SUCCESS: Smaller than original!")
        else:
            print("FAILED: Still larger than original")
        
        return True
    return False

def print_file_info(file_path):
    """
    Print size and basic info of the file
    """
    if os.path.exists(file_path):
        size_mb = os.path.getsize(file_path) / (1024*1024)
        print(f" {file_path}: {size_mb:.2f} MB")
    else:
        print(f" {file_path} not found")

def main():
    if len(sys.argv) < 2:
        print("Usage: python x265_only.py <input_file_or_stream>")
        print("Example: python x265_only.py video.mp4")
        sys.exit(1)

    input_src = sys.argv[1]
    
    # Get base name without extension
    base_name = os.path.splitext(os.path.basename(input_src))[0]
    
    # Output file names
    delta_output = f"{base_name}_x265_delta.mp4"
    without_delta_output = f"{base_name}_x265_no_delta.mp4"

    print("="*60)
    print("x265 (HEVC) - DELTA vs WITHOUT DELTA")
    print("="*60)
    print(f"Input: {input_src}")

    # 1. Encode WITH Delta frames
    print(f"\n{'='*60}")
    print("1. ENCODING WITH DELTA FRAMES")
    print(f"{'='*60}")
    print("Settings: QP=32, GOP=96, preset=veryfast")
    print("Uses: I-frames (keyframes) + P/B-frames (delta frames)")
    print("Goal: Maximum compression, good quality")
    
    if encode_x265_delta(input_src, delta_output, qp=32, preset='veryfast'):
        print_file_info(delta_output)
        print("Expected: ~4-5 MB, 37-38 dB PSNR")
    else:
        print(" Delta encoding failed")

    # 2. Encode WITHOUT Delta frames (Full-frame)
    print(f"\n{'='*60}")
    print("2. ENCODING WITHOUT DELTA FRAMES")
    print(f"{'='*60}")
    print("Settings: QP=40, GOP=1, preset=superfast")
    print("Uses: Only I-frames (every frame is keyframe)")
    print("Goal: Simple decoding, lower compression")
    
    if encode_x265_without_delta(input_src, without_delta_output, bitrate=1000, preset='superfast'):
        print_file_info(without_delta_output)
        print("Expected: ~15-20 MB, 35-36 dB PSNR")
    else:
        print(" Without Delta encoding failed")

    # Comparison summary
    print(f"\n{'='*60}")
    print("COMPARISON SUMMARY")
    print(f"{'='*60}")
    if os.path.exists(delta_output) and os.path.exists(without_delta_output):
        delta_size = os.path.getsize(delta_output) / (1024*1024)
        no_delta_size = os.path.getsize(without_delta_output) / (1024*1024)
        
        if no_delta_size > 0:
            ratio = no_delta_size / delta_size
            compression = (1 - (delta_size / no_delta_size)) * 100
            
            print(f"Delta encoding: {delta_size:.2f} MB")
            print(f"Without Delta:   {no_delta_size:.2f} MB")
            print(f"Delta is {ratio:.1f}x smaller")
            print(f"Compression: {compression:.1f}% size reduction with delta")
            
            if ratio > 3:
                print("Delta encoding working correctly")
            else:
                print("Delta encoding not providing expected compression")

if __name__ == "__main__":
    main()





# import subprocess
# import os
# import sys

# def encode_x265_delta(input_src, output_file, gop=96, fps=25, qp=32, preset='veryfast'):
#     """
#     x265 Delta Encoding (uses P/B frames for compression)
#     """
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src + ' ! decodebin'} "
#         f"! videoconvert "
#         f"! videorate ! video/x-raw,framerate={fps}/1 " 
#         # Delta encoding: uses GOP with keyframes + delta frames
#         f"! x265enc key-int-max={gop} qp={qp} speed-preset={preset} "
#         f"! h265parse "
#         f"! mp4mux "
#         f"! filesink location={output_file} "
#     )
#     print(f"\n[x265 DELTA Encoding]\n{pipeline}\n")
#     return subprocess.run(pipeline, shell=True).returncode == 0


# def encode_x265_without_delta(input_src, output_file, qp=40, preset='superfast'):
#     """
#     x265 Without Delta Encoding (I-frames only - no P/B frames)
#     """
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src + ' ! decodebin'} "
#         f"! videoconvert "
#         # Without Delta: every frame is a keyframe (I-frame)
#         f"! x265enc key-int-max=1 qp={qp} speed-preset={preset} "
#         f"! h265parse "
#         f"! mp4mux "
#         f"! filesink location={output_file}"
#     )
#     print(f"\n[x265 WITHOUT Delta (Full-frame)]\n{pipeline}\n")
#     return subprocess.run(pipeline, shell=True).returncode == 0

# def print_file_info(file_path):
#     """
#     Print size and basic info of the file
#     """
#     if os.path.exists(file_path):
#         size_mb = os.path.getsize(file_path) / (1024*1024)
#         print(f" {file_path}: {size_mb:.2f} MB")
#     else:
#         print(f" {file_path} not found")

# def main():
#     if len(sys.argv) < 2:
#         print("Usage: python x265_only.py <input_file_or_stream>")
#         print("Example: python x265_only.py video.mp4")
#         sys.exit(1)

#     input_src = sys.argv[1]
    
#     # Get base name without extension
#     base_name = os.path.splitext(os.path.basename(input_src))[0]
    
#     # Output file names
#     delta_output = f"{base_name}_x265_delta.mp4"
#     without_delta_output = f"{base_name}_x265_no_delta.mp4"

#     print("="*60)
#     print("x265 (HEVC) - DELTA vs WITHOUT DELTA")
#     print("="*60)
#     print(f"Input: {input_src}")

#     # 1. Encode WITH Delta frames
#     print(f"\n{'='*60}")
#     print("1. ENCODING WITH DELTA FRAMES")
#     print(f"{'='*60}")
#     print("Settings: QP=32, GOP=96, preset=veryfast")
#     print("Uses: I-frames (keyframes) + P/B-frames (delta frames)")
#     print("Goal: Maximum compression, good quality")
    
#     if encode_x265_delta(input_src, delta_output, qp=32, preset='veryfast'):
#         print_file_info(delta_output)
#         print("Expected: ~4-5 MB, 37-38 dB PSNR")
#     else:
#         print(" Delta encoding failed")

#     # 2. Encode WITHOUT Delta frames (Full-frame)
#     print(f"\n{'='*60}")
#     print("2. ENCODING WITHOUT DELTA FRAMES")
#     print(f"{'='*60}")
#     print("Settings: QP=40, GOP=1, preset=superfast")
#     print("Uses: Only I-frames (every frame is keyframe)")
#     print("Goal: Simple decoding, lower compression")
    
#     if encode_x265_without_delta(input_src, without_delta_output, qp=40, preset='superfast'):
#         print_file_info(without_delta_output)
#         print("Expected: ~15-20 MB, 35-36 dB PSNR")
#     else:
#         print(" Without Delta encoding failed")

#     # Comparison summary
#     print(f"\n{'='*60}")
#     print("COMPARISON SUMMARY")
#     print(f"{'='*60}")
#     if os.path.exists(delta_output) and os.path.exists(without_delta_output):
#         delta_size = os.path.getsize(delta_output) / (1024*1024)
#         no_delta_size = os.path.getsize(without_delta_output) / (1024*1024)
        
#         if no_delta_size > 0:
#             ratio = no_delta_size / delta_size
#             compression = (1 - (delta_size / no_delta_size)) * 100
            
#             print(f"Delta encoding: {delta_size:.2f} MB")
#             print(f"Without Delta:   {no_delta_size:.2f} MB")
#             print(f"Delta is {ratio:.1f}x smaller")
#             print(f"Compression: {compression:.1f}% size reduction with delta")
            
#             if ratio > 3:
#                 print("Delta encoding working correctly")
#             else:
#                 print("Delta encoding not providing expected compression")

# if __name__ == "__main__":
#     main()





# #X264 using qp=28 instead of crf=28
# #car1-x264
# #quantizer is How much to divide/compress each block of pixels
# #speed-preset controls the TRADE-OFF between encoding speed and compression efficiency.


# import subprocess
# import os
# import sys

# def encode_with_delta(input_src, output_file, gop=96):
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src + ' ! decodebin'} "
#         f"! videoconvert "
#         f"! x265enc key-int-max={gop} option-string=bframes=4 speed-preset=veryfast "
#         f"! h265parse "
#         f"! mp4mux "
#         f"! filesink location={output_file}"
#     )
#     print(f"\n[Delta Encoding]\n{pipeline}\n")
#     return subprocess.run(pipeline, shell=True).returncode == 0


# def encode_without_delta(input_src, output_file):
#     pipeline = (
#         f"gst-launch-1.0 -e "
#         f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src + ' ! decodebin'} "
#         f"! videoconvert "
#         f"! x265enc key-int-max=1 option-string=bframes=0 speed-preset=veryfast "
#         f"! h265parse "
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
#     delta_output = "vehicles_delta_265.mp4"
#     full_output = "vehicles_265_full.mp4"

#     # Encode with delta frames
#     if encode_with_delta(input_src, delta_output):
#         print_file_info(delta_output)

#     # Encode without delta frames
#     if encode_without_delta(input_src, full_output):
#         print_file_info(full_output)

# if __name__ == "__main__":
#     main()