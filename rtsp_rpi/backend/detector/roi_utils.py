"""
ROI Utilities: Geometric Normalization, In-ROI Point & Box Testing, and OpenCV Drawing.
Supports:
- Single rectangle ROI [[x1, y1], [x2, y2]]
- Single polygon ROI [[x1, y1], [x2, y2], [x3, y3], ...]
- Multiple ROIs [ roi_1, roi_2, ... ] (each rectangle or polygon)
- Backward compatibility with all legacy formats
"""

import math
import numpy as np
import cv2


def normalize_roi_polygons(roi_data):
    """
    Normalizes roi_data into a list of polygons, where each polygon is a list of
    normalized [x, y] coordinates in [0.0, 1.0].
    
    Accepts:
    - None or [] -> []
    - Dict with 'rois', 'polygons', or 'roi_polygon' keys
    - Single rectangle: [[x1, y1], [x2, y2]] -> converted to 4-vertex polygon
    - Single polygon: [[x1, y1], [x2, y2], [x3, y3], ...] (len >= 3)
    - List of ROIs: [ [[...]], [[...]] ]
    
    Returns:
    List of polygons: [ [ [x, y], [x, y], ... ], ... ]
    """
    if not roi_data:
        return []
        
    if isinstance(roi_data, dict):
        roi_data = (
            roi_data.get('rois') or
            roi_data.get('polygons') or
            roi_data.get('roi_polygon') or
            []
        )

    if not isinstance(roi_data, (list, tuple)) or len(roi_data) == 0:
        return []

    # Check if this is a single ROI or a list of multiple ROIs
    first = roi_data[0]
    if isinstance(first, (list, tuple)) and len(first) == 2 and isinstance(first[0], (int, float)):
        # Single ROI (either 2-point rectangle or N-point polygon)
        roi_items = [roi_data]
    else:
        roi_items = [r for r in roi_data if isinstance(r, (list, tuple)) and len(r) >= 2]

    normalized = []
    for r in roi_items:
        if len(r) == 2 and isinstance(r[0], (list, tuple)) and isinstance(r[1], (list, tuple)):
            # 2-point bounding rectangle: [[x1, y1], [x2, y2]]
            x1 = max(0.0, min(1.0, float(min(r[0][0], r[1][0]))))
            y1 = max(0.0, min(1.0, float(min(r[0][1], r[1][1]))))
            x2 = max(0.0, min(1.0, float(max(r[0][0], r[1][0]))))
            y2 = max(0.0, min(1.0, float(max(r[0][1], r[1][1]))))
            if abs(x2 - x1) > 0.005 and abs(y2 - y1) > 0.005:
                # Construct 4-vertex rectangle polygon in clockwise order
                poly = [[x1, y1], [x2, y1], [x2, y2], [x1, y2]]
                normalized.append(poly)
        elif len(r) >= 3:
            # Multi-point polygon
            poly = []
            for pt in r:
                if isinstance(pt, (list, tuple)) and len(pt) >= 2:
                    px = max(0.0, min(1.0, float(pt[0])))
                    py = max(0.0, min(1.0, float(pt[1])))
                    poly.append([px, py])
            if len(poly) >= 3:
                normalized.append(poly)

    return normalized


def is_box_inside_rois(box, roi_polygons, frame_w, frame_h, min_intersection_ratio=0.15):
    """
    Checks whether a detection box [x1, y1, x2, y2] falls within ANY of the given ROI polygons.
    If roi_polygons is empty or None, returns True (no ROI restriction).
    
    Robust multi-criteria check:
    1. Centroid (cx, cy) inside ROI polygon
    2. Foot point (cx, y2) inside ROI polygon (ground contact point for persons/vehicles)
    3. Any box corner inside ROI polygon
    4. Any polygon vertex inside detection box
    5. Substantial bounding box intersection
    """
    if not roi_polygons:
        return True

    polys = normalize_roi_polygons(roi_polygons)
    if not polys:
        return True

    x1, y1, x2, y2 = float(box[0]), float(box[1]), float(box[2]), float(box[3])
    bw = max(1.0, x2 - x1)
    bh = max(1.0, y2 - y1)
    box_area = bw * bh

    cx = (x1 + x2) / 2.0
    cy = (y1 + y2) / 2.0
    foot_x = cx
    foot_y = y2

    test_points = [(cx, cy), (foot_x, foot_y)]
    corners = [(x1, y1), (x2, y1), (x2, y2), (x1, y2)]

    fw = float(frame_w)
    fh = float(frame_h)

    for poly_norm in polys:
        pts_px = np.array([
            [int(pt[0] * fw), int(pt[1] * fh)]
            for pt in poly_norm
        ], dtype=np.int32)

        if len(pts_px) < 3:
            continue

        # 1. Centroid or Foot contact point inside polygon
        for px, py in test_points:
            if cv2.pointPolygonTest(pts_px, (float(px), float(py)), False) >= 0:
                return True

        # Quick AABB overlap rejection
        poly_min_x = float(np.min(pts_px[:, 0]))
        poly_min_y = float(np.min(pts_px[:, 1]))
        poly_max_x = float(np.max(pts_px[:, 0]))
        poly_max_y = float(np.max(pts_px[:, 1]))

        if x2 < poly_min_x or x1 > poly_max_x or y2 < poly_min_y or y1 > poly_max_y:
            continue

        # 2. Check if any box corner is inside the polygon
        for cpx, cpy in corners:
            if cv2.pointPolygonTest(pts_px, (float(cpx), float(cpy)), False) >= 0:
                return True

        # 3. Check if any polygon vertex is inside the detection box
        for pt in pts_px:
            if x1 <= pt[0] <= x2 and y1 <= pt[1] <= y2:
                return True

        # 4. Check intersection of AABBs
        ix1, iy1 = max(x1, poly_min_x), max(y1, poly_min_y)
        ix2, iy2 = min(x2, poly_max_x), min(y2, poly_max_y)
        if ix2 > ix1 and iy2 > iy1:
            inter_area = (ix2 - ix1) * (iy2 - iy1)
            if (inter_area / box_area) >= min_intersection_ratio:
                return True

    return False


def is_point_inside_rois(pt, roi_polygons, frame_w, frame_h):
    """
    Checks if a single point (x, y) falls inside ANY of the configured ROI polygons.
    If roi_polygons is empty, returns True.
    """
    if not roi_polygons:
        return True

    polys = normalize_roi_polygons(roi_polygons)
    if not polys:
        return True

    px, py = float(pt[0]), float(pt[1])
    fw, fh = float(frame_w), float(frame_h)

    for poly_norm in polys:
        pts_px = np.array([
            [int(p[0] * fw), int(p[1] * fh)]
            for p in poly_norm
        ], dtype=np.int32)
        if len(pts_px) < 3:
            continue
        if cv2.pointPolygonTest(pts_px, (px, py), False) >= 0:
            return True

    return False


def draw_rois_on_frame(frame, roi_polygons, label_prefix="ROI"):
    """
    Draws all configured ROI polygons and rectangles onto an OpenCV frame
    with high-contrast tinted fills and crisp boundary tags.
    """
    if not roi_polygons or frame is None:
        return frame

    polys = normalize_roi_polygons(roi_polygons)
    if not polys:
        return frame

    h, w = frame.shape[:2]
    overlay = frame.copy()

    roi_palette = [
        (0, 240, 255),   # Vibrant Cyan
        (0, 255, 128),   # Emerald Green
        (0, 180, 255),   # Amber/Orange
        (255, 110, 210), # Magenta
        (120, 210, 255)  # Sky Blue
    ]

    for idx, poly_norm in enumerate(polys):
        pts_px = np.array([
            [int(pt[0] * w), int(pt[1] * h)]
            for pt in poly_norm
        ], dtype=np.int32)

        if len(pts_px) < 3:
            continue

        color = roi_palette[idx % len(roi_palette)]
        # Outline boundary only - completely transparent inside (no interior color fill)
        cv2.polylines(frame, [pts_px], True, color, 2, cv2.LINE_AA)

        # Label tag badge
        tag = f"{label_prefix} #{idx + 1}" if len(polys) > 1 else f"{label_prefix} ZONE"
        min_x = int(np.min(pts_px[:, 0]))
        min_y = int(np.min(pts_px[:, 1]))

        (tw, th), _ = cv2.getTextSize(tag, cv2.FONT_HERSHEY_SIMPLEX, 0.42, 1)
        bx1 = max(0, min_x)
        by1 = max(0, min_y - th - 6)
        bx2 = min(w - 1, bx1 + tw + 8)
        by2 = max(th + 6, min_y)

        cv2.rectangle(frame, (bx1, by1), (bx2, by2), color, -1)
        cv2.putText(frame, tag, (bx1 + 4, by2 - 4), cv2.FONT_HERSHEY_SIMPLEX, 0.42, (0, 0, 0), 1, cv2.LINE_AA)

    return frame

