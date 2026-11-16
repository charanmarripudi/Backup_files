#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <signal.h>
#include <getopt.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <time.h>
#include <stdbool.h>
#include <pthread.h>
#include <errno.h>

#define MAX_FILENAME 512

double time_diff_sec(struct timespec start, struct timespec end) {
    return (end.tv_sec - start.tv_sec) + (end.tv_nsec - start.tv_nsec) / 1e9;
}

void print_usage(const char *prog_name) {
    printf("Usage: %s --srt <uri> [options]\n", prog_name);
    printf("\nSRT Receiver: Save Delta Video As-Is\n");
    printf("  1. Listen for SRT stream\n");
    printf("  2. Save IDENTICAL delta video from sender\n");
    printf("  3. Preserve B-frames and compression\n");
    printf("\nOptions:\n");
    printf("  --srt URI         SRT listen URI (e.g., srt://:9000)\n");
    printf("  --output DIR      Output directory (default: ./received)\n");
    printf("  --filename NAME   Custom output filename\n");
    printf("  --buffer N        SRT latency buffer in ms (default: 120)\n");
    printf("  --duration N      Stop after N seconds (0 = infinite)\n");
    printf("  --analyze         Run analytics after receiving\n");
    printf("  --display         Display while receiving\n");
    printf("  --help            Show this help\n");
}

int extract_port(const char *srt_uri) {
    int port = 9000;
    const char *ptr = strstr(srt_uri, "://");
    if (!ptr) return port;
    
    ptr += 3;
    const char *last_colon = strrchr(ptr, ':');
    if (last_colon) {
        char *endptr;
        long port_long = strtol(last_colon + 1, &endptr, 10);
        if (endptr > last_colon + 1 && port_long > 0 && port_long <= 65535) {
            port = (int)port_long;
        }
    }
    return port;
}

volatile sig_atomic_t stop_receiving = 0;

void signal_handler(int sig) {
    stop_receiving = 1;
    printf("\nSignal %d received, stopping gracefully...\n", sig);
}

void get_timestamp_str(char *buffer, size_t size) {
    time_t now = time(NULL);
    struct tm *tm_info = localtime(&now);
    strftime(buffer, size, "%Y-%m-%d %H:%M:%S", tm_info);
}

void create_output_filename(char *output_file, size_t size, 
                           const char *output_dir, const char *custom_name,
                           const char *format, int port) {
    char timestamp[32];
    time_t now = time(NULL);
    struct tm *t = localtime(&now);
    strftime(timestamp, sizeof(timestamp), "%Y%m%d_%H%M%S", t);
    
    if (custom_name && strlen(custom_name) > 0) {
        snprintf(output_file, size, "%s/%s_%s.%s", 
                 output_dir, custom_name, timestamp, format);
    } else {
        snprintf(output_file, size, "%s/delta_received_port%d_%s.%s", 
                 output_dir, port, timestamp, format);
    }
}

void create_gstreamer_pipeline(char *pipeline, size_t size, 
                               int port, int buffer_ms, int duration,
                               const char *output_file, bool display) {
    
    if (display) {
        // Display version - still needs some sync
        snprintf(pipeline, size,
            "gst-launch-1.0 -q -e "
            "srtsrc "
            "uri=\"srt://0.0.0.0:%d?mode=listener&latency=%d\" "
            "wait-for-connection=true "
            "messageapi=false "
            "blocksize=1456 ! "
            "tsdemux name=demux "
            "demux. ! queue ! h264parse ! "
            "tee name=t ! "
            "queue ! avdec_h264 ! videoconvert ! autovideosink sync=true "
            "t. ! queue ! h264parse ! "
            "mp4mux faststart=true ! "
            "filesink location=\"%s\" sync=false ",  // File save: NO SYNC
            port, buffer_ms, output_file
        );
    } else {
        // FAST FILE TRANSFER version - NO SYNC at all
        snprintf(pipeline, size,
            "gst-launch-1.0 -q -e "
            "srtsrc "
            "uri=\"srt://0.0.0.0:%d?mode=listener&latency=%d\" "
            "wait-for-connection=true "
            "messageapi=false "      // Use streaming API
            "blocksize=1456 "        // Match sender
            "fc=8192 "               // Flow control window
            "rwmin=6000 ! "          // Minimum receiver window
            "queue max-size-buffers=0 max-size-bytes=0 max-size-time=0 ! "
            "filesink "
            "location=\"%s\" "
            "sync=false "            // CRITICAL: No sync for fast transfer
            "buffer-mode=2 "         // Unbuffered mode
            "async=false",           // No async
            port, buffer_ms, output_file
        );
    }
    
    // Add duration if specified
    if (duration > 0) {
        char duration_cmd[256];
        snprintf(duration_cmd, sizeof(duration_cmd),
            "timeout %d ", duration
        );
        memmove(pipeline + strlen(duration_cmd), pipeline, strlen(pipeline) + 1);
        memcpy(pipeline, duration_cmd, strlen(duration_cmd));
    }
}

int main(int argc, char *argv[]) {
    setbuf(stdout, NULL);
    setbuf(stderr, NULL);
    
    signal(SIGINT, signal_handler);
    signal(SIGTERM, signal_handler);

    printf("=== DELTA VIDEO RECEIVER - Save Compressed Video As-Is ===\n");
    printf("Receives IDENTICAL delta video from sender (B-frames preserved)\n");

    char *srt_uri = NULL;
    char *output_dir = "./received";
    char *custom_filename = NULL;
    int buffer_ms = 120;
    int duration = 0;
    bool analyze = false;
    bool display = false;

    static struct option long_options[] = {
        {"srt", required_argument, 0, 's'},
        {"output", required_argument, 0, 'o'},
        {"filename", required_argument, 0, 'n'},
        {"buffer", required_argument, 0, 'b'},
        {"duration", required_argument, 0, 'd'},
        {"analyze", no_argument, 0, 'a'},
        {"display", no_argument, 0, 'v'},
        {"help", no_argument, 0, 'h'},
        {0, 0, 0, 0}
    };

    int opt;
    int option_index = 0;
    while ((opt = getopt_long(argc, argv, "s:o:n:b:d:avh", 
                              long_options, &option_index)) != -1) {
        switch (opt) {
            case 's': srt_uri = optarg; break;
            case 'o': output_dir = optarg; break;
            case 'n': custom_filename = optarg; break;
            case 'b': buffer_ms = atoi(optarg); break;
            case 'd': duration = atoi(optarg); break;
            case 'a': analyze = true; break;
            case 'v': display = true; break;
            case 'h': print_usage(argv[0]); return 0;
            default: print_usage(argv[0]); return 1;
        }
    }

    if (!srt_uri) {
        fprintf(stderr, "Error: --srt is required\n");
        print_usage(argv[0]);
        return 1;
    }

    int port = extract_port(srt_uri);
    
    // Create output directory
    struct stat st = {0};
    if (stat(output_dir, &st) == -1) {
        if (mkdir(output_dir, 0755) != 0) {
            fprintf(stderr, "Error: Cannot create output directory '%s': %s\n", 
                    output_dir, strerror(errno));
            return 1;
        }
    }

    char output_file[MAX_FILENAME];
    create_output_filename(output_file, sizeof(output_file), 
                         output_dir, custom_filename, "mp4", port);

    struct timespec start, end;
    clock_gettime(CLOCK_MONOTONIC, &start);

    printf("\n=== Configuration ===\n");
    printf("SRT URI:          %s\n", srt_uri);
    printf("Port:             %d\n", port);
    printf("Buffer:           %d ms\n", buffer_ms);
    printf("Output dir:       %s\n", output_dir);
    printf("Output file:      %s\n", output_file);
    printf("Display:          %s\n", display ? "Yes" : "No");
    printf("Analyze:          %s\n", analyze ? "Yes" : "No");
    if (duration > 0) printf("Duration:         %d seconds\n", duration);

    printf("\n=== Starting Receiver ===\n");
    printf("Will save IDENTICAL delta video from sender\n");
    printf("B-frames preserved for maximum compression\n");
    
    char timestamp[64];
    get_timestamp_str(timestamp, sizeof(timestamp));
    printf("[%s] Listening on port %d...\n", timestamp, port);
    printf("Waiting for delta video stream...\n");
    
    // Create GStreamer pipeline
    char gst_cmd[2048];
    create_gstreamer_pipeline(gst_cmd, sizeof(gst_cmd),
                             port, buffer_ms, duration,
                             output_file, display);
    
    printf("\nGStreamer pipeline:\n%s\n", gst_cmd);
    
    printf("\nWaiting for SRT connection on port %d...\n", port);
    printf("Press Ctrl+C to stop\n");
    
    int ret = system(gst_cmd);
    
    clock_gettime(CLOCK_MONOTONIC, &end);
    double elapsed = time_diff_sec(start, end);

    printf("\n=== Receiver Complete ===\n");
    printf("Exit code: %d\n", ret);
    
    // Check output file
    if (access(output_file, F_OK) == 0) {
        struct stat file_stat;
        stat(output_file, &file_stat);
        double size_mb = (double)file_stat.st_size / (1024 * 1024);
        
        if (size_mb > 0.01) {
            printf("\n Delta video received and saved!\n");
            printf("File:      %s\n", output_file);
            printf("Size:      %.2f MB (compressed)\n", size_mb);
            
            if (elapsed > 0.1) {
                printf("Time:      %.2f seconds\n", elapsed);
                printf("Rate:      %.2f MB/s\n", size_mb / elapsed);
            }
            
            // Verify B-frames are preserved
            printf("\n Verifying B-frames (compression)...\n");
            char verify_cmd[512];
            snprintf(verify_cmd, sizeof(verify_cmd),
                "ffprobe -v error -select_streams v:0 "
                "-show_entries stream=has_b_frames "
                "-of csv=p=0 \"%s\" 2>/dev/null",
                output_file
            );
            FILE *fp = popen(verify_cmd, "r");
            if (fp) {
                char buf[32];
                if (fgets(buf, sizeof(buf), fp)) {
                    buf[strcspn(buf, "\n")] = 0;
                    if (strcmp(buf, "1") == 0 || strcmp(buf, "2") == 0) {
                        printf(" B-frames preserved: Yes (good compression)\n");
                    } else {
                        printf(" Video saved successfully\n");
                    }
                }
                pclose(fp);
            }
            
            printf("\n To play:\n");
            printf("   ffplay %s\n", output_file);
            
        } else {
            printf("\n File created but is small (%.2f MB)\n", size_mb);
        }
        
    } else {
        printf("\n No file was saved\n");
        printf("\n Troubleshooting:\n");
        printf("1. Check if sender is running\n");
        printf("2. Test SRT: gst-launch-1.0 srtsrc uri=\"srt://0.0.0.0:%d?mode=listener\" ! fakesink\n", port);
    }
    
    if (ret == 0 || ret == 2 || ret == 130 || ret == 255) {
        printf("\n Receiver completed\n");
        return 0;
    } else {
        printf("\n Receiver exited with code %d\n", ret);
        return 1;
    }
}
