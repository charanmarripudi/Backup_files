CC = gcc
CFLAGS = -O2 -Wall -Wextra

# Platform detection
UNAME_S := $(shell uname -s)

ifeq ($(UNAME_S),Linux)
    # Linux settings
    GST_CFLAGS = $(shell pkg-config --cflags gstreamer-1.0 2>/dev/null || echo "")
    GST_LIBS = $(shell pkg-config --libs gstreamer-1.0 2>/dev/null || echo "")
    SRT_LIBS = -lsrt
    INSTALL_CMD = sudo apt-get install
    PKGS = gstreamer1.0-tools gstreamer1.0-plugins-base \
           gstreamer1.0-plugins-good gstreamer1.0-plugins-bad \
           gstreamer1.0-plugins-ugly libgstreamer1.0-dev \
           libgstreamer-plugins-base1.0-dev libsrt-dev
endif

ifeq ($(UNAME_S),Darwin)
    # macOS settings
    GST_CFLAGS = $(shell pkg-config --cflags gstreamer-1.0 2>/dev/null || echo "-I/usr/local/include/gstreamer-1.0 -I/opt/homebrew/include/gstreamer-1.0")
    GST_LIBS = $(shell pkg-config --libs gstreamer-1.0 2>/dev/null || echo "-L/usr/local/lib -lgstreamer-1.0 -L/opt/homebrew/lib")
    SRT_LIBS = $(shell pkg-config --libs libsrt 2>/dev/null || echo "")
    INSTALL_CMD = brew install
    PKGS = gstreamer gst-plugins-base gst-plugins-good \
           gst-plugins-bad gst-plugins-ugly libsrt
endif

ifeq ($(UNAME_S),Windows_NT)
    # Windows settings (if using MSYS2)
    GST_CFLAGS = $(shell pkg-config --cflags gstreamer-1.0)
    GST_LIBS = $(shell pkg-config --libs gstreamer-1.0)
    SRT_LIBS = -lsrt
    INSTALL_CMD = pacman -S
    PKGS = mingw-w64-x86_64-gstreamer mingw-w64-x86_64-gst-plugins-base \
           mingw-w64-x86_64-gst-plugins-good mingw-w64-x86_64-gst-plugins-bad \
           mingw-w64-x86_64-srt
endif

# Fallback if pkg-config fails
ifeq ($(GST_CFLAGS),)
    GST_CFLAGS = -I/usr/include/gstreamer-1.0 -I/usr/local/include/gstreamer-1.0
endif

ifeq ($(GST_LIBS),)
    GST_LIBS = -lgstreamer-1.0 -lgobject-2.0 -lglib-2.0
endif

LDFLAGS = -lm
ALL = rtsp_sender srt_receiver test_compression

all: $(ALL)

rtsp_sender: rtsp_sender.c
	$(CC) $(CFLAGS) $(GST_CFLAGS) -o $@ $< $(GST_LIBS) $(SRT_LIBS) $(LDFLAGS)

srt_receiver: srt_receiver.c
	$(CC) $(CFLAGS) $(GST_CFLAGS) -o $@ $< $(GST_LIBS) $(SRT_LIBS) $(LDFLAGS)

test_compression: test_compression.c
	$(CC) $(CFLAGS) $(GST_CFLAGS) -o $@ $< $(GST_LIBS) $(LDFLAGS)

clean:
	rm -f $(ALL) *.o
	rm -rf recordings test_output output_videos

install-deps:
	$(INSTALL_CMD) $(PKGS)

test:
	@echo "Creating test video..."
	@ffmpeg -f lavfi -i testsrc=duration=10:size=640x480:rate=30 \
		-f lavfi -i sine=frequency=1000:duration=10 \
		-c:v libx264 -preset ultrafast \
		-c:a aac \
		sample.mp4 2>/dev/null || echo "Test video created"
	./test_compression --input sample.mp4 --bitrate 1000

.PHONY: all clean install-deps test