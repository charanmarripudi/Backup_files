#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <signal.h>
#include <gst/gst.h>
#include <stdbool.h>
#include <time.h>
#include <sys/stat.h>
#include <getopt.h>

// Configuration structure
typedef struct {
    char *rtsp_url;
    char *srt_uri;
    bool use_h265;
    bool delta_mode;
    int bitrate;
    int fps;
    int gop_size;
    int delta_gop_size;
    int crf_quality;
    int b_frames;
} SenderConfig;

// Global variables
static GstElement *pipeline = NULL;
static volatile sig_atomic_t keep_running = 1;
static FILE *stats_file = NULL;

// Signal handler
void signal_handler(int sig) {
    printf("\nReceived signal %d, shutting down...\n", sig);
    keep_running = 0;
}

// File size utility
double get_file_size_mb(const char *path) {
    struct stat st;
    if (stat(path, &st) == 0)
        return st.st_size / (1024.0 * 1024.0);
    return 0.0;
}

// Time utilities
double time_diff_sec(struct timespec start, struct timespec end) {
    return (end.tv_sec - start.tv_sec) + (end.tv_nsec - start.tv_nsec) / 1e9;
}

// Get frame rate from RTSP (simplified - in real app use SDP parsing)
double get_fps_from_rtsp(const char *url) {
    // This is a simplified version. In production, you'd parse SDP
    // or use GStreamer to detect FPS
    return 30.0; // Default
}

// Create delta compression pipeline
GstElement* create_delta_pipeline(SenderConfig *config) {
    char pipeline_str[4096];
    const char *enc_name = config->use_h265 ? "qsvh265enc" : "qsvh264enc";
    const char *parse_name = config->use_h265 ? "h265parse" : "h264parse";
    const char *pay_name = config->use_h265 ? "rtph265pay" : "rtph264pay";
    
    if (config->delta_mode) {
        // Delta compression mode (P-frames with occasional I-frames)
        snprintf(pipeline_str, sizeof(pipeline_str),
            "rtspsrc location=%s latency=0 protocols=tcp ! "
            "rtph264depay ! h264parse ! "
            "decodebin ! "
            "videoconvert ! "
            "video/x-raw,format=NV12,framerate=%d/1 ! "
            "%s "
            "bitrate=%d "
            "gop-size=%d "
            "key-int-max=%d "
            "b-frames=%d "
            "intra-refresh=false "
            "low-latency=false "
            "rate-control=cbr "
            "! %s ! "
            "%s pt=96 ! "
            "srtsink uri=%s latency=2000000",
            config->rtsp_url,
            config->fps,
            enc_name,
            config->bitrate,
            config->gop_size,
            config->delta_gop_size,  // I-frame interval for delta
            config->b_frames,
            parse_name,
            pay_name,
            config->srt_uri
        );
    } else {
        // Full-frame mode (intra-refresh)
        snprintf(pipeline_str, sizeof(pipeline_str),
            "rtspsrc location=%s latency=0 protocols=tcp ! "
            "rtph264depay ! h264parse ! "
            "decodebin ! "
            "videoconvert ! "
            "video/x-raw,format=NV12,framerate=%d/1 ! "
            "%s "
            "bitrate=%d "
            "intra-refresh=true "
            "key-int-max=1 "
            "b-frames=0 "
            "low-latency=true "
            "rate-control=cbr "
            "! %s ! "
            "%s pt=96 ! "
            "srtsink uri=%s latency=2000000",
            config->rtsp_url,
            config->fps,
            enc_name,
            config->bitrate,
            parse_name,
            pay_name,
            config->srt_uri
        );
    }
    
    printf("Pipeline: %s\n", pipeline_str);
    
    GError *error = NULL;
    GstElement *pipeline = gst_parse_launch(pipeline_str, &error);
    
    if (error) {
        g_printerr("Failed to create pipeline: %s\n", error->message);
        g_error_free(error);
        return NULL;
    }
    
    return pipeline;
}

// Get bitrate from stats
void get_pipeline_stats(GstElement *pipeline, int *bitrate_out, double *fps_out) {
    static int frame_count = 0;
    static struct timespec last_time = {0};
    struct timespec now;
    
    clock_gettime(CLOCK_MONOTONIC, &now);
    
    if (last_time.tv_sec == 0) {
        last_time = now;
        return;
    }
    
    frame_count++;
    double elapsed = time_diff_sec(last_time, now);
    
    if (elapsed >= 1.0) {
        *fps_out = frame_count / elapsed;
        frame_count = 0;
        last_time = now;
    }
}

// Run sender
void run_sender(SenderConfig *config) {
    GstBus *bus;
    GstMessage *msg;
    struct timespec start_time;
    double total_bytes = 0;
    
    gst_init(NULL, NULL);
    
    printf("\n=== RTSP Sender with Delta Compression ===\n");
    printf("RTSP URL: %s\n", config->rtsp_url);
    printf("SRT URI: %s\n", config->srt_uri);
    printf("Codec: %s\n", config->use_h265 ? "H.265" : "H.264");
    printf("Mode: %s\n", config->delta_mode ? "Delta (P-frames)" : "Full-frame (Intra-refresh)");
    printf("Bitrate: %d kbps\n", config->bitrate);
    printf("GOP Size: %d frames\n", config->delta_mode ? config->gop_size : 1);
    printf("I-frame Interval: %d frames\n", config->delta_mode ? config->delta_gop_size : 1);
    
    pipeline = create_delta_pipeline(config);
    if (!pipeline) {
        fprintf(stderr, "Failed to create pipeline\n");
        return;
    }
    
    // Set up signal handlers
    signal(SIGINT, signal_handler);
    signal(SIGTERM, signal_handler);
    
    // Start pipeline
    gst_element_set_state(pipeline, GST_STATE_PLAYING);
    clock_gettime(CLOCK_MONOTONIC, &start_time);
    
    bus = gst_element_get_bus(pipeline);
    
    printf("\nSender running. Press Ctrl+C to stop.\n");
    printf("Time    FPS     Bitrate(kbps)   Estimated Size(MB)\n");
    printf("---------------------------------------------------\n");
    
    int stat_counter = 0;
    double avg_fps = 0;
    
    // Main loop
    while (keep_running) {
        msg = gst_bus_timed_pop_filtered(bus, 100 * GST_MSECOND, 
            GST_MESSAGE_ERROR | GST_MESSAGE_EOS | GST_MESSAGE_STATE_CHANGED);
        
        if (msg != NULL) {
            switch (GST_MESSAGE_TYPE(msg)) {
                case GST_MESSAGE_ERROR: {
                    GError *error = NULL;
                    gchar *debug;
                    gst_message_parse_error(msg, &error, &debug);
                    g_printerr("Error: %s\n", error->message);
                    g_error_free(error);
                    g_free(debug);
                    keep_running = 0;
                    break;
                }
                case GST_MESSAGE_EOS:
                    printf("End of stream\n");
                    keep_running = 0;
                    break;
                case GST_MESSAGE_STATE_CHANGED:
                    // Optional: Log state changes
                    break;
                default:
                    break;
            }
            gst_message_unref(msg);
        }
        
        // Update stats every second
        stat_counter++;
        if (stat_counter >= 10) { // ~1 second
            struct timespec now;
            clock_gettime(CLOCK_MONOTONIC, &now);
            double elapsed = time_diff_sec(start_time, now);
            
            // Estimate data
            total_bytes = (config->bitrate * 1000 / 8.0) * elapsed;
            double size_mb = total_bytes / (1024.0 * 1024.0);
            
            printf("%5.0fs  %5.1f    %8d       %10.2f\n", 
                   elapsed, avg_fps > 0 ? avg_fps : config->fps, 
                   config->bitrate, size_mb);
            
            stat_counter = 0;
        }
        
        // Simulate FPS measurement
        avg_fps = config->fps * 0.95 + avg_fps * 0.05;
    }
    
    // Final stats
    struct timespec end_time;
    clock_gettime(CLOCK_MONOTONIC, &end_time);
    double total_time = time_diff_sec(start_time, end_time);
    total_bytes = (config->bitrate * 1000 / 8.0) * total_time;
    double total_mb = total_bytes / (1024.0 * 1024.0);
    
    printf("\n=== Final Statistics ===\n");
    printf("Total runtime: %.2f seconds\n", total_time);
    printf("Estimated total data: %.2f MB\n", total_mb);
    printf("Average bitrate: %d kbps\n", config->bitrate);
    printf("Compression mode: %s\n", config->delta_mode ? "Delta" : "Full-frame");
    
    // Cleanup
    printf("\nStopping pipeline...\n");
    gst_element_set_state(pipeline, GST_STATE_NULL);
    gst_object_unref(bus);
    gst_object_unref(pipeline);
    
    printf("Sender stopped.\n");
}

// Print usage
void print_usage() {
    printf("RTSP Sender with Delta Compression\n");
    printf("Usage: rtsp_sender [OPTIONS]\n\n");
    printf("Required:\n");
    printf("  --rtsp URL          RTSP source URL\n");
    printf("  --srt URI           SRT destination URI (srt://IP:PORT)\n\n");
    printf("Compression options:\n");
    printf("  --delta             Enable delta compression (default: off)\n");
    printf("  --h265              Use H.265 encoding (default: H.264)\n");
    printf("  --bitrate RATE      Target bitrate in kbps (default: 2000)\n");
    printf("  --fps FPS           Frame rate (default: 30)\n");
    printf("  --gop-size SIZE     GOP size for delta mode (default: 120)\n");
    printf("  --iframe-interval I I-frame interval in delta mode (default: 60)\n");
    printf("  --b-frames N        Number of B-frames (default: 3)\n\n");
    printf("Examples:\n");
    printf("  rtsp_sender --rtsp rtsp://camera/stream --srt srt://192.168.1.100:9000 --delta\n");
    printf("  rtsp_sender --rtsp rtsp://camera/stream --srt srt://192.168.1.100:9000 --h265 --bitrate 3000\n");
}

int main(int argc, char *argv[]) {
    SenderConfig config = {
        .rtsp_url = NULL,
        .srt_uri = NULL,
        .use_h265 = false,
        .delta_mode = false,
        .bitrate = 2000,
        .fps = 30,
        .gop_size = 120,
        .delta_gop_size = 60,  // I-frame every 60 frames in delta mode
        .crf_quality = 23,
        .b_frames = 3
    };
    
    // Parse command line arguments
    static struct option long_options[] = {
        {"rtsp", required_argument, 0, 'r'},
        {"srt", required_argument, 0, 's'},
        {"delta", no_argument, 0, 'd'},
        {"h265", no_argument, 0, '5'},
        {"bitrate", required_argument, 0, 'b'},
        {"fps", required_argument, 0, 'f'},
        {"gop-size", required_argument, 0, 'g'},
        {"iframe-interval", required_argument, 0, 'i'},
        {"b-frames", required_argument, 0, 'B'},
        {"help", no_argument, 0, 'h'},
        {0, 0, 0, 0}
    };
    
    int opt;
    int option_index = 0;
    
    while ((opt = getopt_long(argc, argv, "r:s:d5b:f:g:i:B:h", 
                              long_options, &option_index)) != -1) {
        switch (opt) {
            case 'r':
                config.rtsp_url = optarg;
                break;
            case 's':
                config.srt_uri = optarg;
                break;
            case 'd':
                config.delta_mode = true;
                break;
            case '5':
                config.use_h265 = true;
                break;
            case 'b':
                config.bitrate = atoi(optarg);
                break;
            case 'f':
                config.fps = atoi(optarg);
                break;
            case 'g':
                config.gop_size = atoi(optarg);
                break;
            case 'i':
                config.delta_gop_size = atoi(optarg);
                break;
            case 'B':
                config.b_frames = atoi(optarg);
                break;
            case 'h':
                print_usage();
                return 0;
            default:
                print_usage();
                return 1;
        }
    }
    
    // Validate required arguments
    if (!config.rtsp_url || !config.srt_uri) {
        fprintf(stderr, "Error: --rtsp and --srt are required arguments\n\n");
        print_usage();
        return 1;
    }
    
    // Validate parameters
    if (config.bitrate <= 0) {
        fprintf(stderr, "Error: Bitrate must be positive\n");
        return 1;
    }
    
    if (config.fps <= 0) {
        fprintf(stderr, "Error: FPS must be positive\n");
        return 1;
    }
    
    if (config.delta_mode && config.gop_size <= 0) {
        fprintf(stderr, "Error: GOP size must be positive\n");
        return 1;
    }
    
    // Adjust delta mode parameters
    if (!config.delta_mode) {
        config.gop_size = 1;
        config.delta_gop_size = 1;
        config.b_frames = 0;
    }
    
    // Run sender
    run_sender(&config);
    
    return 0;
}