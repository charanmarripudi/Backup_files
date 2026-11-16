import cv2
import os
import time

# Force FFmpeg options BEFORE VideoCapture
os.environ["OPENCV_FFMPEG_CAPTURE_OPTIONS"] = (
    "rtsp_transport;tcp|"
    "stimeout;10000000|"    
    "max_delay;500000"
)

RTSP_URLS = [
    "rtsp://admin:Algo_1212@192.168.96.231:554/stream1"]

cap = None
working_url = None

for url in RTSP_URLS:
    print(f" Trying: {url}")
    cap = cv2.VideoCapture(url, cv2.CAP_FFMPEG)
    time.sleep(1)

    if cap.isOpened():
        ret, frame = cap.read()
        if ret:
            working_url = url
            print(f" Connected to: {url}")
            break
        else:
            cap.release()

if working_url is None:
    print(" No RTSP URL worked")
    exit(1)

while True:
    ret, frame = cap.read()
    if not ret:
        print(" Frame lost, reconnecting...")
        cap.release()
        time.sleep(2)
        cap = cv2.VideoCapture(working_url, cv2.CAP_FFMPEG)
        continue

    cv2.imshow("Ambicam RTSP Stream", frame)

    if cv2.waitKey(1) & 0xFF == ord("q"):
        break

cap.release()
cv2.destroyAllWindows()




# import cv2

# RTSP_URL = "rtsp://admin:Algo12345678@192.168.96.55:554/ch0_0.264"

# cap = cv2.VideoCapture(RTSP_URL)

# if not cap.isOpened():
#     print("Failed to open RTSP stream")
#     exit()

# print("RTSP stream opened")

# while True:
#     ret, frame = cap.read()
#     if not ret:
#         print("Frame not received")
#         break

#     cv2.imshow("RTSP Stream", frame)

#     if cv2.waitKey(1) & 0xFF == ord('q'):
#         break

#  .release()
# cv2.destroyAllWindows()
