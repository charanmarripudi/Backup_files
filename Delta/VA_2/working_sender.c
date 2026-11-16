#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <signal.h>
#include <stdbool.h>
#include <time.h>
#include <sys/stat.h>
#include <getopt.h>

volatile sig_atomic_t keep_running = 1;

void signal_handler(int sig) {
    printf("\nReceived signal %d, shutting down...\n", sig);
    keep_running = 0;
}

// Compute wall time
double time_diff_sec(struct timespec start, struct timespec end) {
    return (end.tv_sec - start.tv_sec) + (end.tv_nsec - start.tv_nsec) / 1e9;
}

// Delta encode function
void encode_delta_to_srt(const char *in, const char *srt_uri, int fps, int crf) {
    char cmd[4096];
    struct timespec start, end;

    clock_gettime(CLOCK_MONOTONIC, &start);

    snprintf(cmd, sizeof(cmd),
        "gst-launch-1.0 -v filesrc location=\"%s\" ! decodebin ! videoconvert ! videorate ! video/x-raw,framerate=%d/1 ! "
        "x264enc key-int-max=250 bframes=8 ref=6 quantizer=%d speed-preset=fast tune=zerolatency ! "
        "h264parse ! rtph264pay ! srtsink uri=\"%s?mode=caller\" latency=200 async=false",
        in, fps, crf, srt_uri
    );

    printf(">>> Delta encoding + streaming over SRT command:\n%s\n", cmd);

    system(cmd);

    clock_gettime(CLOCK_MONOTONIC, &end);
    printf(">>> Total wall-time: %.2f seconds\n", time_diff_sec(start, end));
}

void print_usage() {
    printf("Delta + SRT Sender\n");
    printf("Usage: ./working_sender --input <file> --srt <srt_uri> [options]\n");
    printf("Options:\n");
    printf("  --fps N       Frame rate override (default 30)\n");
    printf("  --crf N       CRF for x264 (default 23)\n");
    printf("  --help        Show help\n");
}

int main(int argc, char *argv[]) {
    char *input_file = NULL;
    char *srt_uri = NULL;
    int fps = 30;
    int crf = 23;

    static struct option long_options[] = {
        {"input", required_argument, 0, 'i'},
        {"srt", required_argument, 0, 's'},
        {"fps", required_argument, 0, 'f'},
        {"crf", required_argument, 0, 'c'},
        {"help", no_argument, 0, 'h'},
        {0,0,0,0}
    };

    int opt, option_index = 0;
    while ((opt = getopt_long(argc, argv, "i:s:f:c:h", long_options, &option_index)) != -1) {
        switch(opt) {
            case 'i': input_file = optarg; break;
            case 's': srt_uri = optarg; break;
            case 'f': fps = atoi(optarg); break;
            case 'c': crf = atoi(optarg); break;
            case 'h': print_usage(); return 0;
            default: print_usage(); return 1;
        }
    }

    if (!input_file || !srt_uri) {
        fprintf(stderr, "Error: --input and --srt are required\n");
        print_usage();
        return 1;
    }

    struct stat st;
    if (stat(input_file, &st) != 0) {
        fprintf(stderr, "Error: Input file '%s' does not exist\n", input_file);
        return 1;
    }

    encode_delta_to_srt(input_file, srt_uri, fps, crf);
    return 0;
}
