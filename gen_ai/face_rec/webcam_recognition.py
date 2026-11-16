import cv2
import numpy as np
import psycopg2

from insightface.app import FaceAnalysis

# ----------------------------------
# DATABASE
# ----------------------------------

conn = psycopg2.connect(
    dbname="streaming",
    user="algofusion",
    password="Algofusion",
    host="localhost",
    port="5432"
)

cursor = conn.cursor()

cursor.execute("""
SELECT person_name,
       embedding
FROM faces
""")

known_faces = []

for person_name, embedding in cursor.fetchall():

    emb = np.array(
        embedding,
        dtype=np.float32
    )

    emb /= np.linalg.norm(emb)

    known_faces.append(
        (person_name, emb)
    )

print(
    f"Loaded "
    f"{len(known_faces)} embeddings"
)

# ----------------------------------
# INSIGHTFACE
# ----------------------------------

app = FaceAnalysis(
    name="buffalo_l",
    providers=["CPUExecutionProvider"]
)

app.prepare(
    ctx_id=0,
    det_size=(640, 640)
)

# ----------------------------------
# WEBCAM
# ----------------------------------

cap = cv2.VideoCapture(0)

THRESHOLD = 0.55

while True:

    ret, frame = cap.read()

    if not ret:
        break

    faces = app.get(frame)

    for face in faces:

        emb = face.embedding

        emb /= np.linalg.norm(emb)

        best_score = -1
        best_name = "Unknown"

        for (
            person_name,
            db_emb
        ) in known_faces:

            score = np.dot(
                emb,
                db_emb
            )

            if score > best_score:

                best_score = score
                best_name = person_name

        if best_score < THRESHOLD:
            best_name = "Unknown"

        x1, y1, x2, y2 = (
            face.bbox.astype(int)
        )

        cv2.rectangle(
            frame,
            (x1, y1),
            (x2, y2),
            (0, 255, 0),
            2
        )

        cv2.putText(
            frame,
            f"{best_name} {best_score:.2f}",
            (x1, y1 - 10),
            cv2.FONT_HERSHEY_SIMPLEX,
            0.7,
            (0, 255, 0),
            2
        )

    cv2.imshow(
        "InsightFace Recognition",
        frame
    )

    key = cv2.waitKey(1)

    if key == 27:
        break

cap.release()
cv2.destroyAllWindows()

cursor.close()
conn.close()