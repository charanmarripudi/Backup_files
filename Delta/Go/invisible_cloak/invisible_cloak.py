import cv2
import numpy as np
import time

# --------------------------------------------------
# RTSP camera URL
# --------------------------------------------------
rtsp_url = "rtsp://admin:@192.168.96.30:554/ch0_0.264"

# Open RTSP camera
cap = cv2.VideoCapture(rtsp_url, cv2.CAP_FFMPEG)

if not cap.isOpened():
    print("ERROR: Could not open RTSP camera")
    exit()

print("Starting pink invisibility cloak...")
print("Stay OUT of the frame for 3 seconds.")
print("Press 'q' to quit.")

# --------------------------------------------------
# Camera warm-up and background preparation
# --------------------------------------------------
start_time = time.time()

while time.time() - start_time < 3:
    ret, frame = cap.read()

    if not ret:
        print("ERROR: Could not read frame during preparation")
        cap.release()
        exit()

    frame = cv2.flip(frame, 1)

    remaining = 3 - int(time.time() - start_time)

    cv2.putText(
        frame,
        f"Stay OUT of the frame: {remaining}",
        (30, 50),
        cv2.FONT_HERSHEY_SIMPLEX,
        1,
        (0, 255, 255),
        2
    )

    cv2.imshow("Pink Invisibility Cloak", frame)

    if cv2.waitKey(1) & 0xFF == ord("q"):
        cap.release()
        cv2.destroyAllWindows()
        exit()

# --------------------------------------------------
# Capture background
# --------------------------------------------------
print("Capturing background...")

background_frames = []

for _ in range(30):
    ret, frame = cap.read()

    if not ret:
        print("ERROR: Could not capture background")
        cap.release()
        exit()

    frame = cv2.flip(frame, 1)
    background_frames.append(frame)

# Use the average of captured frames as the background
background = np.mean(background_frames, axis=0).astype(np.uint8)

print("Background captured successfully.")
print("Now enter the frame wearing pink.")
print("Press 'q' to quit.")

# --------------------------------------------------
# Pink HSV color range
# --------------------------------------------------
# Standard pink / magenta range
lower_pink = np.array([140, 50, 50])
upper_pink = np.array([179, 255, 255])

# --------------------------------------------------
# Main invisibility-cloak loop
# --------------------------------------------------
while True:
    ret, frame = cap.read()

    if not ret:
        print("WARNING: Could not read frame")
        continue

    # Mirror the live frame
    frame = cv2.flip(frame, 1)

    # Convert BGR image to HSV
    hsv = cv2.cvtColor(frame, cv2.COLOR_BGR2HSV)

    # Detect pink color
    mask = cv2.inRange(hsv, lower_pink, upper_pink)

    # Remove small noise
    kernel = np.ones((5, 5), np.uint8)

    mask = cv2.morphologyEx(
        mask,
        cv2.MORPH_OPEN,
        kernel
    )

    mask = cv2.morphologyEx(
        mask,
        cv2.MORPH_CLOSE,
        kernel
    )

    # Slightly enlarge the detected pink area
    mask = cv2.dilate(mask, kernel, iterations=1)

    # Invert the mask
    inverse_mask = cv2.bitwise_not(mask)

    # Show background where pink is detected
    background_part = cv2.bitwise_and(
        background,
        background,
        mask=mask
    )

    # Show live frame everywhere else
    live_part = cv2.bitwise_and(
        frame,
        frame,
        mask=inverse_mask
    )

    # Combine both parts
    output = cv2.addWeighted(
        background_part,
        1,
        live_part,
        1,
        0
    )

    # Display result
    cv2.imshow("Pink Invisibility Cloak", output)

    # Optional: show pink detection mask
    # cv2.imshow("Pink Mask", mask)

    # Quit when q is pressed
    if cv2.waitKey(1) & 0xFF == ord("q"):
        break

# --------------------------------------------------
# Release resources
# --------------------------------------------------
cap.release()
cv2.destroyAllWindows()
print("Camera closed.")



# # I have added comments as well for better understanding
# #for first 3 seconds after the camera starts , be out of the frame and let it capture the background image


# import cv2
# import numpy as np
# import time

# print("Starting blue invisibility cloak... Press 'q' to quit.")

# # Open camera
# cap = cv2.VideoCapture("rtsp://admin:@192.168.96.30:554/ch0_0.264")

# time.sleep(3)

# # Take a picture of the background before starting
# background = 0

# for i in range(30):
#     ret, background = cap.read()

#     if not ret:
#         print("Couldn't capture the background.")
#         cap.release()
#         exit()

# # Mirror the background
# background = np.flip(background, axis=1)

# while cap.isOpened():

#     ret, img = cap.read()

#     if not ret:
#         break

#     # Mirror the live video
#     img = np.flip(img, axis=1)

#     # HSV makes it easier to detect a particular color
#     hsv = cv2.cvtColor(img, cv2.COLOR_BGR2HSV)

#     # Blue color range
#     lower_blue = np.array([90, 80, 80])
#     upper_blue = np.array([130, 255, 255])

#     # Find the blue parts of the image
#     mask = cv2.inRange(hsv, lower_blue, upper_blue)

#     # Clean up the mask a little
#     kernel = np.ones((3, 3), np.uint8)

#     mask = cv2.morphologyEx(mask, cv2.MORPH_OPEN, kernel)
#     mask = cv2.morphologyEx(mask, cv2.MORPH_DILATE, kernel)

#     # Reverse the mask so we can keep everything except the cloak
#     mask_inv = cv2.bitwise_not(mask)

#     # Put the old background where the blue cloak is
#     background_part = cv2.bitwise_and(
#         background,
#         background,
#         mask=mask
#     )

#     # Keep the current frame everywhere else
#     current_part = cv2.bitwise_and(
#         img,
#         img,
#         mask=mask_inv
#     )

#     # Join both parts together
#     final_output = cv2.addWeighted(
#         background_part,
#         1,
#         current_part,
#         1,
#         0
#     )

#     cv2.imshow("Blue Invisibility Cloak", final_output)

#     # Press q to close
#     if cv2.waitKey(1) & 0xFF == ord('q'):
#         break

# cap.release()
# cv2.destroyAllWindows()