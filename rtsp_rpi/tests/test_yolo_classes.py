import os
from ultralytics import YOLO

base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
model_path = os.path.join(base_dir, "models", "firehose.pt")
model = YOLO(model_path)
print(model.names)