import os
import sys

base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
backend_dir = os.path.join(base_dir, "backend")
for p in (base_dir, backend_dir):
    if p not in sys.path:
        sys.path.insert(0, p)

from server import read_locations, check_device_online

# Step 1: Read locations
locations = read_locations()
print(f"Read {len(locations)} locations")

# Step 2: Find akola depot
akola_entry = None
for loc in locations:
    if loc.get("location", "").lower() == "akola depot":
        akola_entry = loc
        break

print(f"Found akola entry: {akola_entry}")

# Step 3: Test check_device_online
if akola_entry:
    ip = akola_entry.get("device_ip", "")
    status = check_device_online(ip)
    print(f"check_device_online({ip}) returned {status}")