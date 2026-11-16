from PIL import Image

def convert_signature_to_5_2(
    input_path,
    output_path,
    target_width=1000,
    target_height=400,
    background_color=(255, 255, 255)  # white background
):
    """
    Convert a signature image to 5:2 aspect ratio without distortion
    """

    # Open image
    img = Image.open(input_path).convert("RGBA")

    # Original dimensions
    w, h = img.size
    original_ratio = w / h
    target_ratio = target_width / target_height  # 2.5

    # Resize while keeping aspect ratio
    if original_ratio > target_ratio:
        new_width = target_width
        new_height = int(target_width / original_ratio)
    else:
        new_height = target_height
        new_width = int(target_height * original_ratio)

    img = img.resize((new_width, new_height), Image.LANCZOS)

    # Create background canvas
    canvas = Image.new(
        "RGBA",
        (target_width, target_height),
        background_color + (255,)
    )

    # Center the signature
    x_offset = (target_width - new_width) // 2
    y_offset = (target_height - new_height) // 2
    canvas.paste(img, (x_offset, y_offset), img)

    # Convert to RGB for JPG
    final_img = canvas.convert("RGB")

    # Save output
    final_img.save(output_path, quality=95, subsampling=0)

    print("Signature converted successfully")
    print(f"Saved at: {output_path}")


# -------------------- RUN HERE --------------------
if __name__ == "__main__":
    convert_signature_to_5_2(
        input_path="/Users/algofusion/Downloads/Charan_sign (1).jpg",
        output_path="/Users/algofusion/Downloads/Charan_signature_5_2.jpg"
    )
