import cv2
import os
import yt_dlp
import numpy as np
from PIL import Image
import imagehash

# PDF imports
from reportlab.platypus import SimpleDocTemplate, Image as RLImage, Table, TableStyle
from reportlab.lib.pagesizes import A4
from reportlab.lib import colors

# ========= CONFIG =========
video_url = "https://www.youtube.com/watch?v=xCz1yOmy91Q&t=16s"

BASE_OUTPUT = "Youtube_frames_output"

frame_interval = 60
MIN_FRAME_GAP = 180
DIFF_THRESHOLD = 7
HASH_THRESHOLD = 3

# A4 Layout config
IMAGES_PER_ROW = 2
IMAGE_WIDTH = 250
IMAGE_HEIGHT = 150

os.makedirs(BASE_OUTPUT, exist_ok=True)

# ========= STEP 1: GET VIDEO INFO =========
ydl_opts = {"format": "best"}
with yt_dlp.YoutubeDL(ydl_opts) as ydl:
    info = ydl.extract_info(video_url, download=False)
    video_stream_url = info["url"]
    title = info.get("title", "youtube_video")

safe_title = "".join(c if c.isalnum() else "_" for c in title)[:60]

video_folder = os.path.join(BASE_OUTPUT, safe_title)
frames_folder = os.path.join(video_folder, "frames")
pdf_path = os.path.join(video_folder, f"{safe_title}.pdf")

os.makedirs(frames_folder, exist_ok=True)

print(f"\nProcessing Video: {title}")
print(f"Output Folder: {video_folder}\n")

# ========= STEP 2: FRAME EXTRACTION =========
cap = cv2.VideoCapture(video_stream_url)

frame_count = 0
saved_count = 0

last_saved_frame = None
last_saved_frame_no = -1000
last_hash = None

while True:
    ret, frame = cap.read()
    if not ret:
        break

    if frame_count % frame_interval == 0:

        small_frame = cv2.resize(frame, (320, 180))
        gray = cv2.cvtColor(small_frame, cv2.COLOR_BGR2GRAY)

        save_flag = False

        # First frame
        if last_saved_frame is None:
            save_flag = True
        else:
            diff = cv2.absdiff(gray, last_saved_frame)
            mean_diff = np.mean(diff)

            if mean_diff > DIFF_THRESHOLD:
                save_flag = True

        # Time gap control
        if frame_count - last_saved_frame_no < MIN_FRAME_GAP:
            save_flag = False

        # Hash check
        if save_flag:
            img_hash = imagehash.phash(Image.fromarray(gray))

            if last_hash is not None and abs(img_hash - last_hash) < HASH_THRESHOLD:
                save_flag = False
            else:
                last_hash = img_hash

        # Save frame
        if save_flag:
            last_saved_frame = gray
            last_saved_frame_no = frame_count

            img_full = Image.fromarray(cv2.cvtColor(frame, cv2.COLOR_BGR2RGB))
            img_path = os.path.join(frames_folder, f"frame_{saved_count}.jpg")
            img_full.save(img_path)

            print(f"Saved: {img_path}")
            saved_count += 1

    frame_count += 1

cap.release()

print(f"\nTotal Frames Saved: {saved_count}")

# ========= STEP 3: CREATE A4 PDF =========
pdf = SimpleDocTemplate(pdf_path, pagesize=A4)
elements = []

files = sorted(
    os.listdir(frames_folder),
    key=lambda x: int(x.split("_")[1].split(".")[0])
)

data = []
row = []

for file in files:
    if file.endswith(".jpg"):
        img_path = os.path.join(frames_folder, file)
        img = RLImage(img_path, width=IMAGE_WIDTH, height=IMAGE_HEIGHT)
        row.append(img)

        if len(row) == IMAGES_PER_ROW:
            data.append(row)
            row = []

# Add remaining images
if row:
    data.append(row)

# Create grid table
table = Table(data)
table.setStyle(TableStyle([
    ('GRID', (0,0), (-1,-1), 0.5, colors.grey),
    ('VALIGN', (0,0), (-1,-1), 'MIDDLE')
]))

elements.append(table)
pdf.build(elements)

print(f"\n✅ A4 Optimized PDF saved at: {pdf_path}")


