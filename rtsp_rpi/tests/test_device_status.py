import os
import socket
import sys
import json
import subprocess

base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
backend_dir = os.path.join(base_dir, "backend")
for p in (base_dir, backend_dir):
    if p not in sys.path:
        sys.path.insert(0, p)

from server import check_device_online

# First, let's ping 192.168.96.78 to populate the ARP table
print("Pinging 192.168.96.78 to populate ARP table...")
subprocess.run(["ping", "-c", "2", "-W", "1", "192.168.96.78"], capture_output=True, text=True)

# Now let's check the ARP table for it
print("\nChecking ARP table for 192.168.96.78:")
arp_result = subprocess.run(["arp", "-n", "192.168.96.78"], capture_output=True, text=True)
print(arp_result.stdout)

# Now let's test all entries
print("\nTesting all entries in locations.json:")
with open('locations.json', 'r') as f:
    locations = json.load(f)

for loc in locations:
    ip = loc.get('device_ip', '')
    loc_name = loc.get('location', 'Unknown')
    status = check_device_online(ip)
    print(f" {loc_name:<20} | IP: {ip:<20} | Online: {status}")