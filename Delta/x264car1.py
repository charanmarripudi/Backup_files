
import subprocess
import os
import sys

def encode_with_delta(input_src, output_file, gop=96,fps = 25):
    pipeline = (
        f"gst-launch-1.0 -e "
        f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src + ' ! decodebin'} "
        f"! videoconvert "
        f"! videorate ! video/x-raw,framerate={fps}/1 " 
        # f"! x264enc key-int-max={gop} bframes=4 quantizer=28 speed-preset=medium "
        f"! x264enc key-int-max={gop} bframes=4 quantizer=35 speed-preset=superfast tune=zerolatency qp-min=35 qp-max=40 "
        f"! h264parse "
        f"! mp4mux "
        f"! filesink location={output_file} "
    )
    print(f"\n[Delta Encoding]\n{pipeline}\n")
    return subprocess.run(pipeline, shell=True).returncode == 0


def encode_without_delta(input_src, output_file):
    pipeline = (
        f"gst-launch-1.0 -e "
        f"{'uridecodebin uri=' + input_src if input_src.startswith('rtsp://') else 'filesrc location=' + input_src + ' ! decodebin'} "
        f"! videoconvert "
        f"! x264enc key-int-max=1 bframes=0 quantizer=30 speed-preset=medium "
        f"! h264parse "
        f"! mp4mux "
        f"! filesink location={output_file}"
    )
    print(f"\n[Full-frame Encoding]\n{pipeline}\n")
    return subprocess.run(pipeline, shell=True).returncode == 0

def print_file_info(file_path):
    """
    Print size of the file in MB
    """
    if os.path.exists(file_path):
        size_mb = os.path.getsize(file_path) / (1024*1024)
        print(f"{file_path}: {size_mb:.2f} MB")
    else:
        print(f"{file_path} not found")

def main():
    if len(sys.argv) < 2:
        print("Usage: python test_w_delta.py <input_file_or_stream>")
        sys.exit(1)

    input_src = sys.argv[1]

    # Output file names
    delta_output = "mathena_edit_opt_delta_264.mp4"
    full_output = "mathena_edit_opt_full_264.mp4"

    # Encode with delta frames
    if encode_with_delta(input_src, delta_output):
        print_file_info(delta_output)

    # Encode without delta frames
    if encode_without_delta(input_src, full_output):
        print_file_info(full_output)

if __name__ == "__main__":
    main()






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
#         f"! x264enc key-int-max={gop} bframes=4 quantizer=28 speed-preset=veryfast "
#         f"! h264parse "
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
#         f"! x264enc key-int-max=1 bframes=0 quantizer=28 speed-preset=veryfast "
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
#     delta_output = "vehicles_delta_264.mp4"
#     full_output = "vehicles_264_full.mp4"

#     # Encode with delta frames
#     if encode_with_delta(input_src, delta_output):
#         print_file_info(delta_output)

#     # Encode without delta frames
#     if encode_without_delta(input_src, full_output):
#         print_file_info(full_output)

# if __name__ == "__main__":
#     main()