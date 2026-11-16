#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <sys/stat.h>

int main(int argc, char *argv[]) {
    if (argc < 2) {
        printf("Usage: %s <srt_uri>\n", argv[0]);
        printf("Example: %s srt://:9000\n", argv[0]);
        return 1;
    }
    
    char *srt_uri = argv[1];
    
    struct stat st = {0};
    if (stat("./received_videos", &st) == -1) {
        mkdir("./received_videos", 0755);
    }
    
    char timestamp[32];
    time_t now = time(NULL);
    struct tm *t = localtime(&now);
    strftime(timestamp, sizeof(timestamp), "%Y%m%d_%H%M%S", t);
    
    char output_file[256];
    snprintf(output_file, sizeof(output_file), "./received_videos/received_%s.mp4", timestamp);
    
    printf("=== GStreamer SRT Receiver ===\n");
    printf("Listening on: %s\n", srt_uri);
    printf("Saving to: %s\n", output_file);
    printf("\n");
    
    char cmd[2048];
    snprintf(cmd, sizeof(cmd),
        "gst-launch-1.0 srtsrc uri='%s' latency=2000000 ! queue ! rtph264depay ! h264parse ! mp4mux faststart=true ! filesink location='%s'",
        srt_uri, output_file
    );
    
    printf("Running: %s\n\n", cmd);
    printf("Waiting for stream... Press Ctrl+C to stop.\n");
    
    int result = system(cmd);
    
    printf("\nReceiver stopped. File saved: %s\n", output_file);
    return 0;
}
