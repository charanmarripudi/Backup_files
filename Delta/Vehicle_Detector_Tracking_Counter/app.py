"""Vehicle detection, tracking, and line-crossing counter for YOLO models."""

from __future__ import annotations

import tempfile
from collections import Counter
from pathlib import Path

import cv2
import numpy as np
import streamlit as st
from ultralytics import YOLO


DEFAULT_CLASSES = ("car", "bike", "truck", "pickup truck")
COLORS = {
    "car": (255, 180, 0),
    "bike": (0, 220, 255),
    "truck": (70, 80, 255),
    "pickup truck": (100, 230, 80),
}


@st.cache_resource(show_spinner="Loading YOLO model…")
def load_model(weights_path: str) -> YOLO:
    return YOLO(weights_path)


def side_of_line(point: tuple[int, int], a: tuple[int, int], b: tuple[int, int]) -> float:
    """Returns which side of the directed line a point lies on."""
    return (b[0] - a[0]) * (point[1] - a[1]) - (b[1] - a[1]) * (point[0] - a[0])


def has_crossed(previous: float, current: float) -> bool:
    """A sign change means the tracked object crossed the counting line."""
    return (previous < 0 < current) or (previous > 0 > current)


class CentroidTracker:
    """Small fallback tracker used only if Ultralytics tracking cannot start."""

    def __init__(self, max_distance: int = 80, max_missing: int = 20) -> None:
        self.max_distance = max_distance
        self.max_missing = max_missing
        self.next_id = 1
        self.objects: dict[int, tuple[tuple[int, int], str, int]] = {}

    def update(self, detections: list[tuple[int, int, int, int, str]]) -> list[int]:
        assigned: list[int] = []
        used: set[int] = set()
        for x1, y1, x2, y2, label in detections:
            centroid = ((x1 + x2) // 2, (y1 + y2) // 2)
            candidates = [
                (np.hypot(centroid[0] - old[0][0], centroid[1] - old[0][1]), object_id)
                for object_id, old in self.objects.items()
                if old[1] == label and object_id not in used
            ]
            distance, object_id = min(candidates, default=(float("inf"), -1))
            if distance > self.max_distance:
                object_id = self.next_id
                self.next_id += 1
            self.objects[object_id] = (centroid, label, 0)
            used.add(object_id)
            assigned.append(object_id)

        self.objects = {
            object_id: (center, label, missing + 1)
            for object_id, (center, label, missing) in self.objects.items()
            if object_id in used or missing + 1 <= self.max_missing
        }
        return assigned


def draw_overlay(
    frame: np.ndarray,
    detections: list[tuple[int, int, int, int, str, float, int]],
    line_start: tuple[int, int],
    line_end: tuple[int, int],
    totals: Counter,
) -> np.ndarray:
    output = frame.copy()
    cv2.line(output, line_start, line_end, (0, 0, 255), 3)
    cv2.putText(output, "COUNTING LINE", (line_start[0] + 8, max(25, line_start[1] - 8)),
                cv2.FONT_HERSHEY_SIMPLEX, 0.65, (0, 0, 255), 2, cv2.LINE_AA)
    for x1, y1, x2, y2, label, confidence, object_id in detections:
        color = COLORS.get(label.lower(), (0, 255, 0))
        cv2.rectangle(output, (x1, y1), (x2, y2), color, 2)
        text = f"{label} #{object_id} {confidence:.0%}"
        cv2.putText(output, text, (x1, max(22, y1 - 7)), cv2.FONT_HERSHEY_SIMPLEX,
                    0.55, color, 2, cv2.LINE_AA)
        cv2.circle(output, ((x1 + x2) // 2, (y1 + y2) // 2), 4, color, -1)

    total = sum(totals.values())
    panel_height = 42 + 27 * len(totals)
    cv2.rectangle(output, (12, 12), (250, panel_height), (20, 20, 20), -1)
    cv2.putText(output, f"TOTAL: {total}", (22, 42), cv2.FONT_HERSHEY_SIMPLEX,
                0.8, (255, 255, 255), 2, cv2.LINE_AA)
    for index, (label, count) in enumerate(totals.items()):
        cv2.putText(output, f"{label}: {count}", (22, 70 + index * 27),
                    cv2.FONT_HERSHEY_SIMPLEX, 0.62, COLORS.get(label.lower(), (255, 255, 255)),
                    2, cv2.LINE_AA)
    return output


def main() -> None:
    st.set_page_config(page_title="Vehicle Counter", page_icon="🚗", layout="wide")
    st.title("🚗 Vehicle Detection, Tracking & Counting")
    st.caption("Upload a video. Each vehicle is counted once when its tracked centre crosses the red line.")

    project_weights = Path(__file__).with_name("best.pt")
    with st.sidebar:
        st.header("Settings")
        weights = st.text_input("YOLO weights", value=str(project_weights))
        confidence = st.slider("Detection confidence", 0.05, 0.95, 0.35, 0.05)
        allowed_classes = st.multiselect("Classes to count", DEFAULT_CLASSES, default=list(DEFAULT_CLASSES))
        st.divider()
        st.subheader("Counting line")
        st.caption("Use percentages so the line adapts to every uploaded video.")
        x1_pct = st.slider("Line start X (%)", 0, 100, 10)
        y1_pct = st.slider("Line start Y (%)", 0, 100, 55)
        x2_pct = st.slider("Line end X (%)", 0, 100, 90)
        y2_pct = st.slider("Line end Y (%)", 0, 100, 55)

    upload = st.file_uploader("Video", type=["mp4", "avi", "mov", "mkv", "mpeg", "mpg"])
    if upload is None:
        st.info("Choose a video file to begin.")
        return
    if not Path(weights).is_file():
        st.error(f"Model weights not found: {weights}")
        return
    if not allowed_classes:
        st.warning("Choose at least one class to process.")
        return

    source_suffix = Path(upload.name).suffix or ".mp4"
    with tempfile.NamedTemporaryFile(delete=False, suffix=source_suffix) as temp_file:
        temp_file.write(upload.getbuffer())
        source_path = temp_file.name

    cap: cv2.VideoCapture | None = None
    writer: cv2.VideoWriter | None = None
    output_path: str | None = None
    try:
        model = load_model(weights)
        cap = cv2.VideoCapture(source_path)
        if not cap.isOpened():
            st.error("This video could not be read. Try an MP4 encoded with H.264.")
            return
        frame_count = int(cap.get(cv2.CAP_PROP_FRAME_COUNT))
        fps = cap.get(cv2.CAP_PROP_FPS) or 30.0
        width = int(cap.get(cv2.CAP_PROP_FRAME_WIDTH))
        height = int(cap.get(cv2.CAP_PROP_FRAME_HEIGHT))
        line_start = (round(width * x1_pct / 100), round(height * y1_pct / 100))
        line_end = (round(width * x2_pct / 100), round(height * y2_pct / 100))
        if line_start == line_end:
            st.error("The start and end points of the counting line must be different.")
            return

        st.caption(f"Video: {width}×{height} · {fps:.1f} FPS · {frame_count} frames")
        video_col, counts_col = st.columns([3, 1])
        player = video_col.empty()
        progress = video_col.progress(0, text="Processing detections and creating a playable video…")
        metric_total = counts_col.empty()
        count_table = counts_col.empty()
        with tempfile.NamedTemporaryFile(delete=False, suffix=".mp4") as output_file:
            output_path = output_file.name
        writer = cv2.VideoWriter(
            output_path,
            cv2.VideoWriter_fourcc(*"mp4v"),
            fps,
            (width, height),
        )
        if not writer.isOpened():
            st.error("Could not create the processed video file.")
            return

        totals = Counter({name: 0 for name in allowed_classes})
        last_side: dict[int, float] = {}
        counted_ids: set[int] = set()
        fallback_tracker = CentroidTracker()
        use_yolo_tracker = True
        frame_no = 0

        while True:
            ok, frame = cap.read()
            if not ok:
                break
            frame_no += 1
            try:
                if use_yolo_tracker:
                    result = model.track(frame, persist=True, conf=confidence, verbose=False)[0]
                else:
                    result = model(frame, conf=confidence, verbose=False)[0]
            except Exception:
                use_yolo_tracker = False
                result = model(frame, conf=confidence, verbose=False)[0]

            current: list[tuple[int, int, int, int, str, float, int]] = []
            raw_detections: list[tuple[int, int, int, int, str]] = []
            boxes = result.boxes
            if boxes is not None:
                box_data = boxes.xyxy.cpu().numpy().astype(int)
                class_data = boxes.cls.cpu().numpy().astype(int)
                conf_data = boxes.conf.cpu().numpy()
                ids = boxes.id.int().cpu().tolist() if use_yolo_tracker and boxes.id is not None else []
                for index, (x1, y1, x2, y2) in enumerate(box_data):
                    label = result.names[class_data[index]]
                    if label.lower() in {item.lower() for item in allowed_classes}:
                        raw_detections.append((x1, y1, x2, y2, label))
                        object_id = ids[index] if index < len(ids) else -1
                        current.append((x1, y1, x2, y2, label, float(conf_data[index]), object_id))
            if not use_yolo_tracker:
                fallback_ids = fallback_tracker.update(raw_detections)
                current = [(*detection[:6], fallback_ids[index]) for index, detection in enumerate(current)]

            for x1, y1, x2, y2, label, _, object_id in current:
                centre = ((x1 + x2) // 2, (y1 + y2) // 2)
                current_side = side_of_line(centre, line_start, line_end)
                if object_id in last_side and object_id not in counted_ids and has_crossed(last_side[object_id], current_side):
                    totals[label] += 1
                    counted_ids.add(object_id)
                # Keep the prior non-zero side. A centre exactly on the line
                # should not erase the sign needed to detect the next frame.
                if current_side != 0:
                    last_side[object_id] = current_side

            annotated = draw_overlay(frame, current, line_start, line_end, totals)
            writer.write(annotated)
            progress.progress(min(frame_no / max(frame_count, 1), 1.0), text=f"Processing frame {frame_no}/{frame_count}")
        cap.release()
        cap = None
        writer.release()
        writer = None
        progress.progress(1.0, text="Complete")
        processed_video = Path(output_path).read_bytes()
        player.video(processed_video, format="video/mp4")
        metric_total.metric("Vehicles crossed", sum(totals.values()))
        count_table.dataframe(
            [{"Vehicle": label, "Count": count} for label, count in totals.items()],
            hide_index=True,
            use_container_width=True,
        )
        video_col.download_button(
            "Download annotated video",
            data=processed_video,
            file_name=f"counted_{Path(upload.name).stem}.mp4",
            mime="video/mp4",
        )
        st.success(f"Finished. Counted {sum(totals.values())} vehicles crossing the line.")
    finally:
        if cap is not None:
            cap.release()
        if writer is not None:
            writer.release()
        Path(source_path).unlink(missing_ok=True)
        if output_path is not None:
            Path(output_path).unlink(missing_ok=True)


if __name__ == "__main__":
    main()
