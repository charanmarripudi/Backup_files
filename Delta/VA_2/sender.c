#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/stat.h>

int main(int argc, char *argv[]) {
    if (argc < 3) {
        printf("Usage: %s <input_video> <srt_uri>\n", argv[0]);
        printf("Example: %s video.mp4 srt://127.0.0.1:9000\n", argv[0]);
        return 1;
    }
    
    char *input_file = argv[1];
    char *srt_uri = argv[2];
    
    struct stat st;
    if (stat(input_file, &st) != 0) {
        printf("Error: File '%s' not found\n", input_file);
        return 1;
    }
    
    printf("=== GStreamer SRT Sender ===\n");
    printf("Input: %s\n", input_file);
    printf("SRT URI: %s\n", srt_uri);
    printf("\n");
    
    char cmd[2048];
    snprintf(cmd, sizeof(cmd),
        "gst-launch-1.0 -e filesrc location='%s' ! decodebin ! videoconvert ! x264enc bitrate=2000 key-int-max=60 bframes=3 ! h264parse ! rtph264pay ! srtsink uri='%s' latency=2000000",
        input_file, srt_uri
    );
    
    printf("Running: %s\n\n", cmd);
    printf("Starting stream... Press Ctrl+C to stop.\n");
    
    int result = system(cmd);
    
    printf("\nStream ended with exit code: %d\n", result);
    return 0;
}
