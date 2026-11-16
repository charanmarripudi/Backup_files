#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <signal.h>
#include <time.h>
#include <sys/stat.h>
#include <getopt.h>

volatile sig_atomic_t keep_running = 1;

void signal_handler(int sig) {
    printf("\nReceived signal %d, shutting down...\n", sig);
    keep_running = 0;
}

void print_usage() {
    printf("SRT Receiver\n");
    printf("Usage: ./working_receiver --srt <srt_uri> [--output <dir>]\n");
    printf("Options:\n");
    printf("  --output DIR   Output directory (default ./received_videos)\n");
    printf("  --help         Show help\n");
}

void receive_srt_to_mp4(const char *srt_uri, const char *output_dir) {
    char cmd[4096];

    // Create output dir
    struct stat st = {0};
    if (stat(output_dir, &st) == -1) mkdir(output_dir, 0755);

    // Generate timestamped output
    char timestamp[32];
    time_t now = time(NULL);
    struct tm *t = localtime(&now);
    strftime(timestamp, sizeof(timestamp), "%Y%m%d_%H%M%S", t);

    char output_file[512];
    snprintf(output_file, sizeof(output_file), "%s/received_%s.mp4", output_dir, timestamp);

    snprintf(cmd, sizeof(cmd),
        "gst-launch-1.0 -v srtsrc uri=\"%s?mode=listener\" latency=200 do-timestamp=false ! "
        "rtph264depay ! h264parse ! mp4mux faststart=true ! filesink location=\"%s\"",
        srt_uri, output_file
    );

    printf(">>> Receiving SRT stream and saving to:\n%s\n", output_file);
    printf("Command:\n%s\n", cmd);

    system(cmd);
    printf(">>> Receiver finished.\n");
}

int main(int argc, char *argv[]) {
    char *srt_uri = NULL;
    char *output_dir = "./received_videos";

    static struct option long_options[] = {
        {"srt", required_argument, 0, 's'},
        {"output", required_argument, 0, 'o'},
        {"help", no_argument, 0, 'h'},
        {0,0,0,0}
    };

    int opt, option_index = 0;
    while ((opt = getopt_long(argc, argv, "s:o:h", long_options, &option_index)) != -1) {
        switch(opt) {
            case 's': srt_uri = optarg; break;
            case 'o': output_dir = optarg; break;
            case 'h': print_usage(); return 0;
            default: print_usage(); return 1;
        }
    }

    if (!srt_uri) {
        fprintf(stderr, "Error: --srt is required\n");
        print_usage();
        return 1;
    }

    receive_srt_to_mp4(srt_uri, output_dir);
    return 0;
}
