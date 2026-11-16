# Vehicle Detection, Tracking & Counting

A Streamlit interface for the custom YOLO model in `best.pt`. It detects `car`, `bike`, `truck`, and `pickup truck`, maintains a tracking ID for each vehicle, and counts that ID once when its centre crosses the configurable red line.

## Features

- Upload MP4, AVI, MOV, MKV, MPEG, or MPG videos.
- Detect the four custom vehicle classes with `best.pt`.
- Track vehicles with persistent IDs.
- Draw an adjustable line using percentage-based controls that fit any video resolution.
- Count every tracked vehicle once when it crosses the line in either direction.
- Play, seek, fullscreen, and download the annotated output MP4.

## Start the app

Install Python 3.10 or newer, then run these commands from this folder:

```powershell
py -m pip install -r requirements.txt
py -m streamlit run app.py
```

If the `py` command is unavailable after installing Python, close and reopen PowerShell, then try again.

## Use it

1. Open the browser address Streamlit prints (normally `http://localhost:8501`).
2. Upload a video.
3. Move the four sidebar sliders to put the two endpoints of the red counting line in the desired position.
4. Set confidence and select the vehicle classes if needed.

The app creates an annotated MP4, then opens it in a normal video player with play/pause, seek, playback speed, and fullscreen controls. It also offers a download button for that processed video and displays overall and class-wise crossing counts. The count is directional-agnostic: a vehicle crossing either way is counted once.

`best.pt` is selected automatically. You can enter another model path in the sidebar if required.

## Project files

| File | Purpose |
| --- | --- |
| `app.py` | Streamlit application and vehicle counting logic. |
| `best.pt` | Trained YOLO model used by default. |
| `requirements.txt` | Python dependencies. |
| `.gitignore` | Excludes local environments, uploads, output videos, and `last.pt`. |

## GitHub note

The repository includes `best.pt` so a clone can run immediately. It is about 40 MB, which is below GitHub's normal 100 MB per-file limit. If you later replace it with a model larger than 100 MB, store the weight file with Git LFS or a release asset instead.
