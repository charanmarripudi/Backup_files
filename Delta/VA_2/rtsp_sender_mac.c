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

// Signal handler
void signal_handler(int sig) {
    printf("\nReceived signal %d, shutting down...\n", sig);
    keep_running = 0;
}

// Create delta compression pipeline (macOS compatible)
GstElement* create_delta_pipeline(SenderConfig *config) {
    char pipeline_str[4096];
    
    // Use x264/x265 encoders for macOS compatibility
    const char *enc_name = config->use_h265 ? "x265enc" : "x264enc";
    const char *parse_name = config->use_h265 ? "h265parse" : "h264parse";
    const char *pay_name = config->use_h265 ? "rtph265pay" : "rtph264pay";
    
    if (config->delta_mode) {
        // Delta compression mode
        snprintf(pipeline_str, sizeof(pipeline_str),
            "rtspsrc location=%s latency=0 ! "
            "rtph264depay ! h264parse ! "
            "decodebin ! "
            "videoconvert ! "
            "video/x-raw,framerate=%d/1 ! "
            "%s bitrate=%d key-int-max=%d bframes=%d ! "
            "%s ! "
            "%s ! "
            "srtsink uri=%s latency=2000000",
            config->rtsp_url,
            config->fps,
            enc_name,
            config->bitrate,
            config->delta_gop_size,
            config->b_frames,
            parse_name,
            pay_name,
            config->srt_uri
        );
    } else {
        // Full-frame mode
        snprintf(pipeline_str, sizeof(pipeline_str),
            "rtspsrc location=%s latency=0 ! "
            "rtph264depay ! h264parse ! "
            "decodebin ! "
            "videoconvert ! "
            "video/x-raw,framerate=%d/1 ! "
            "%s bitrate=%d key-int-max=1 ! "
            "%s ! "
            "%s ! "
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

// Run sender
void run_sender(SenderConfig *config) {
    GstBus *bus;
    GstMessage *msg;
    struct timespec start_time;
    
    gst_init(NULL, NULL);
    
    printf("\n=== RTSP Sender with Delta Compression ===\n");
    printf("RTSP URL: %s\n", config->rtsp_url);
    printf("SRT URI: %s\n", config->srt_uri);
    printf("Codec: %s\n", config->use_h265 ? "H.265" : "H.264");
    printf("Mode: %s\n", config->delta_mode ? "Delta (P-frames)" : "Full-frame");
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
    printf("Time    Status\n");
    printf("-------------\n");
    
    int stat_counter = 0;
    
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
                default:
                    break;
            }
            gst_message_unref(msg);
        }
        
        // Update stats every second
        stat_counter++;
        if (stat_counter >= 10) {
            struct timespec now;
            clock_gettime(CLOCK_MONOTONIC, &now);
            double elapsed = (now.tv_sec - start_time.tv_sec) + 
                           (now.tv_nsec - start_time.tv_nsec) / 1e9;
            
            printf("%5.0fs  Streaming...\n", elapsed);
            stat_counter = 0;
        }
    }
    
    // Final stats
    struct timespec end_time;
    clock_gettime(CLOCK_MONOTONIC, &end_time);
    double total_time = (end_time.tv_sec - start_time.tv_sec) + 
                       (end_time.tv_nsec - start_time.tv_nsec) / 1e9;
    
    printf("\n=== Final Statistics ===\n");
    printf("Total runtime: %.2f seconds\n", total_time);
    printf("Compression mode: %s\n", config->delta_mode ? "Delta" : "Full-frame");
    
    // Cleanup
    printf("\nStopping pipeline...\n");
    gst_element_set_state(pipeline, GST_STATE_NULL);
    gst_object_unref(bus);
    gst_object_unref(pipeline);
    
    printf("Sender stopped.\n");
}

// Print usage (same as before)
void print_usage() {
    printf("RTSP Sender with Delta Compression (macOS version)\n");
    printf("Usage: rtsp_sender_mac [OPTIONS]\n\n");
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
    printf("  rtsp_sender_mac --rtsp rtsp://camera/stream --srt srt://192.168.1.100:9000 --delta\n");
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
        .delta_gop_size = 60,
        .crf_quality = 23,
        .b_frames = 3
    };
    
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
    
    if (!config.rtsp_url || !config.srt_uri) {
        fprintf(stderr, "Error: --rtsp and --srt are required\n\n");
        print_usage();
        return 1;
    }
    
    if (!config.delta_mode) {
        config.gop_size = 1;
        config.delta_gop_size = 1;
        config.b_frames = 0;
    }
    
    run_sender(&config);
    return 0;
}
