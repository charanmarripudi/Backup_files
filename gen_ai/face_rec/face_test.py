import cv2
from insightface.app import FaceAnalysis

app = FaceAnalysis(
    name="buffalo_l",
    providers=["CPUExecutionProvider"]
)

app.prepare(
    ctx_id=0,
    det_size=(1280, 1280)
)

img = cv2.imread(
    "/Users/algofusion/Downloads/Face_rec/faces/Nikhil/1.jpg"
)

print("Shape:", img.shape)

faces = app.get(img)

print("Faces detected:", len(faces))

cv2.imwrite("debug_face.jpg", img)

cv2.imshow("Face", img)
cv2.waitKey(0)
cv2.destroyAllWindows()