import os
import json
import cv2
import psycopg2

from insightface.app import FaceAnalysis

# ----------------------------
# CONFIG
# ----------------------------

FACE_FOLDER = "/Users/algofusion/Downloads/Face_rec/faces"

DB_CONFIG = {
    "dbname": "streaming",
    "user": "algofusion",
    "password": "Algofusion",
    "host": "localhost",
    "port": "5432"
}

# ----------------------------
# INSIGHTFACE
# ----------------------------

app = FaceAnalysis(
    name="buffalo_l",
    providers=["CPUExecutionProvider"]
)

app.prepare(
    ctx_id=0,
    det_size=(640, 640)
)

# ----------------------------
# DB
# ----------------------------

conn = psycopg2.connect(**DB_CONFIG)
cursor = conn.cursor()

cursor.execute("""
CREATE TABLE IF NOT EXISTS faces (
    id SERIAL PRIMARY KEY,
    person_name VARCHAR(255),
    image_path TEXT,
    embedding JSONB,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
""")

cursor.execute("DELETE FROM faces")
conn.commit()

# ----------------------------
# PROCESS FACES
# ----------------------------

for person_name in os.listdir(FACE_FOLDER):

    person_dir = os.path.join(
        FACE_FOLDER,
        person_name
    )

    if not os.path.isdir(person_dir):
        continue

    print(f"\nProcessing {person_name}")

    for image_name in os.listdir(person_dir):

        image_path = os.path.join(
            person_dir,
            image_name
        )

        img = cv2.imread(image_path)

        if img is None:
            continue

        faces = app.get(img)

        if len(faces) == 0:
            print(f"No face: {image_name}")
            continue

        embedding = (
            faces[0]
            .embedding
            .astype(float)
            .tolist()
        )

        cursor.execute(
            """
            INSERT INTO faces
            (
                person_name,
                image_path,
                embedding
            )
            VALUES (%s,%s,%s)
            """,
            (
                person_name,
                image_path,
                json.dumps(embedding)
            )
        )

        print(
            f"Inserted: "
            f"{person_name} -> {image_name}"
        )

conn.commit()

cursor.close()
conn.close()

print("\nDatabase Created Successfully")