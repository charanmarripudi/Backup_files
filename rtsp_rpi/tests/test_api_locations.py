import sys
import os
import json
import requests

base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
backend_dir = os.path.join(base_dir, "backend")
for p in (base_dir, backend_dir):
    if p not in sys.path:
        sys.path.insert(0, p)

BASE_URL = "http://localhost:8080"
locations_path = os.path.join(base_dir, "locations.json")

# Test 1: Read locations.json directly
with open(locations_path, 'r') as f:
    locs_json = json.load(f)
print(" locations.json direct read:")
for loc in locs_json:
    print(f"  - {loc['location']}: {loc['device_status']}")

# Test 2: Call /api/locations endpoint
print("\n Calling /api/locations...")
try:
    response = requests.get(f"{BASE_URL}/api/locations", timeout=10)
    if response.status_code == 200:
        api_data = response.json()
        print("\n /api/locations response:")
        for loc in api_data['locations']:
            print(f"  - {loc['location']}: {loc['device_status']}")
    else:
        print(f" Error: {response.status_code}")
        print(response.text)
except Exception as e:
    print(f" Exception calling endpoint: {e}")
