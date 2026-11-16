import os
import subprocess
import whisper
import yt_dlp
from googletrans import Translator
from gtts import gTTS

# =========================
# CONFIG
# =========================

youtube_url = "https://www.youtube.com/watch?v=0Pa5sTGc3Is"

video_file = "video.mp4"
audio_file = "audio.wav"
telugu_audio = "telugu_audio.wav"
compressed_video = "compressed_video.mp4"
final_video = "final_telugu_video.mp4"

# =========================
# DOWNLOAD VIDEO
# =========================

print("Downloading YouTube video...")

ydl_opts = {
    "format": "best",
    "outtmpl": video_file
}

with yt_dlp.YoutubeDL(ydl_opts) as ydl:
    ydl.download([youtube_url])

print("Download complete")

# =========================
# EXTRACT AUDIO
# =========================

print("Extracting audio...")

subprocess.run([
    "ffmpeg",
    "-y",
    "-i", video_file,
    "-ac", "1",
    "-ar", "16000",
    audio_file
])

print("Audio extracted")

# =========================
# WHISPER TRANSCRIPTION
# =========================

print("Loading Whisper model...")

model = whisper.load_model("base")

print("Transcribing audio...")

result = model.transcribe(audio_file)

english_text = result["text"]

print("Transcription complete")

# =========================
# TRANSLATE TO TELUGU
# =========================

print("Translating to Telugu...")

translator = Translator()

telugu_text = translator.translate(
    english_text,
    dest="te"
).text

print("Translation complete")

# =========================
# TELUGU VOICE GENERATION
# =========================

print("Generating Telugu voice...")

tts = gTTS(
    text=telugu_text,
    lang="te"
)

tts.save("telugu_audio.mp3")

print("Telugu audio generated")

# =========================
# CONVERT AUDIO TO WAV
# =========================

subprocess.run([
    "ffmpeg",
    "-y",
    "-i", "telugu_audio.mp3",
    "-ar", "16000",
    "-ac", "1",
    telugu_audio
])

# =========================
# GStreamer VIDEO COMPRESSION
# =========================

print("Compressing video using GStreamer...")

gst_command = f"""
gst-launch-1.0 \
filesrc location={video_file} ! decodebin ! x264enc bitrate=800 speed-preset=fast ! mp4mux ! filesink location={compressed_video}
"""

subprocess.run(gst_command, shell=True)

print("Video compression done")

# =========================
# MERGE TELUGU AUDIO
# =========================

print("Merging Telugu audio with compressed video...")

subprocess.run([
    "ffmpeg",
    "-y",
    "-i", compressed_video,
    "-i", telugu_audio,
    "-c:v", "copy",
    "-map", "0:v:0",
    "-map", "1:a:0",
    final_video
])

print("===================================")
print("Telugu dubbed video created!")
print("Output file:", final_video)
print("===================================")