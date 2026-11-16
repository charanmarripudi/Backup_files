import os
from datetime import datetime

DB_DSN = os.getenv("DATABASE_URL", "postgresql://postgres:Algofusion@localhost:5432/streaming")


def ensure_alerts_schema(cursor):
    """Verifies that the PostgreSQL alerts table and required schema columns exist.

    Creates the table with SERIAL primary key, camera_id, location,
    type_of_alert, image_path, and created_at timestamp if missing.

    Args:
        cursor: Active PostgreSQL database cursor object.
    """
    cursor.execute("""
        CREATE TABLE IF NOT EXISTS alerts (
            alert_id      SERIAL PRIMARY KEY,
            camera_id     VARCHAR(64),
            location      VARCHAR(255),
            type_of_alert VARCHAR(255),
            image_path    TEXT,
            created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        );
    """)
    cursor.execute("ALTER TABLE alerts ADD COLUMN IF NOT EXISTS camera_id VARCHAR(64);")


def insert_alert_db(cursor, camera_id, location, type_of_alert, image_path, created_at=None):
    """Inserts a confirmed AI violation alert record into the PostgreSQL alerts table.

    Args:
        cursor: Active PostgreSQL database cursor.
        camera_id (str): Associated camera identifier (e.g. "0").
        location (str): Physical gantry or bay location name.
        type_of_alert (str): Violation classification label (e.g. "NO_HELMET").
        image_path (str): Relative or absolute path to the evidence snapshot JPEG.
        created_at (datetime, optional): Alert timestamp. Defaults to current time.
    """
    cursor.execute(
        "INSERT INTO alerts (camera_id, location, type_of_alert, image_path, created_at) VALUES (%s, %s, %s, %s, %s)",
        (camera_id, location, type_of_alert, image_path, created_at or datetime.now())
    )


def insert_alert_via_psql(camera_id, location, type_of_alert, image_path, created_at=None, dsn=DB_DSN):
    """Executes a fail-safe fallback alert insertion via psql CLI subprocess.

    Invoked automatically if the primary psycopg2 connection pool experiences
    transient connection loss or deadlock.

    Args:
        camera_id (str): Camera identifier.
        location (str): Physical facility location name.
        type_of_alert (str): Violation classification label.
        image_path (str): Path to evidence snapshot JPEG file.
        created_at (datetime, optional): Timestamp. Defaults to current time.
        dsn (str, optional): PostgreSQL connection DSN string.

    Returns:
        bool: True if psql command executed successfully (returncode 0), False otherwise.
    """
    import subprocess
    try:
        ts_str = (created_at or datetime.now()).strftime("%Y-%m-%d %H:%M:%S.%f")
        safe_cam = str(camera_id).replace("'", "''")
        safe_loc = str(location).replace("'", "''")
        safe_type = str(type_of_alert).replace("'", "''")
        safe_img = str(image_path).replace("'", "''")
        sql = f"INSERT INTO alerts (camera_id, location, type_of_alert, image_path, created_at) VALUES ('{safe_cam}', '{safe_loc}', '{safe_type}', '{safe_img}', '{ts_str}');"
        res = subprocess.run(["psql", dsn, "-c", sql], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=5)
        if res.returncode == 0:
            return True
        else:
            print(f"[ALERT-PSQL-ERR] psql exited {res.returncode}: {res.stderr.strip()}", flush=True)
            return False
    except Exception as e:
        print(f"[ALERT-PSQL-EXC] psql fallback error: {e}", flush=True)
        return False

