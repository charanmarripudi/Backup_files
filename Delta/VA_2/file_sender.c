
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
    char *input_file;
    char *srt_uri;
    bool use_h265;
    int bitrate;
    int fps;
    int gop_size;
    int iframe_interval;
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

// Create delta compression pipeline
GstElement* create_delta_pipeline(SenderConfig *config) {
    char pipeline_str[4096];
    
    // Use x264/x265 encoders for macOS compatibility
    const char *enc_name = config->use_h265 ? "x265enc" : "x264enc";
    const char *parse_name = config->use_h265 ? "h265parse" : "h264parse";
    const char *pay_name = config->use_h265 ? "rtph265pay" : "rtph264pay";
    
    snprintf(pipeline_str, sizeof(pipeline_str),
        "filesrc location=\"%s\" ! "
        "decodebin ! "
        "videoconvert ! "
        "video/x-raw,framerate=%d/1 ! "
        "%s bitrate=%d key-int-max=%d bframes=%d ! "
        "%s ! "
        "%s ! "
        "srtsink uri=\"%s\" latency=2000000",
        config->input_file,
        config->fps,
        enc_name,
        config->bitrate,
        config->iframe_interval,
        config->b_frames,
        parse_name,
        pay_name,
        config->srt_uri
    );
    
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
    
    printf("\n=== File to SRT Sender (Delta Compression) ===\n");
    printf("Input file: %s\n", config->input_file);
    printf("SRT URI: %s\n", config->srt_uri);
    printf("Codec: %s\n", config->use_h265 ? "H.265" : "H.264");
    printf("Bitrate: %d kbps\n", config->bitrate);
    printf("FPS: %d\n", config->fps);
    printf("GOP Size: %d frames\n", config->gop_size);
    printf("I-frame Interval: %d frames\n", config->iframe_interval);
    printf("B-frames: %d\n", config->b_frames);
    
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
            double elapsed = (now.tv_sec - start_time.tv_sec);
            
            printf("%5.0fs  Streaming...\n", elapsed);
            stat_counter = 0;
        }
    }
    
    // Final stats
    struct timespec end_time;
    clock_gettime(CLOCK_MONOTONIC, &end_time);
    double total_time = (end_time.tv_sec - start_time.tv_sec);
    
    printf("\n=== Final Statistics ===\n");
    printf("Total runtime: %.2f seconds\n", total_time);
    printf("Compression: Delta (I+P frames)\n");
    
    // Cleanup
    printf("\nStopping pipeline...\n");
    gst_element_set_state(pipeline, GST_STATE_NULL);
    gst_object_unref(bus);
    gst_object_unref(pipeline);
    
    printf("Sender stopped.\n");
}

// Print usage
void print_usage() {
    printf("File to SRT Sender (Delta Compression Only)\n");
    printf("===========================================\n");
    printf("Usage: file_sender [OPTIONS]\n\n");
    printf("Required:\n");
    printf("  --input FILE       Input video file (MP4, AVI, etc.)\n");
    printf("  --srt URI          SRT destination URI (e.g., srt://127.0.0.1:9000)\n\n");
    printf("Compression options (Delta only):\n");
    printf("  --h265             Use H.265 encoding (default: H.264)\n");
    printf("  --bitrate RATE     Target bitrate in kbps (default: 2000)\n");
    printf("  --fps FPS          Frame rate (default: 30)\n");
    printf("  --gop-size SIZE    GOP size (default: 120)\n");
    printf("  --iframe-interval I I-frame interval (default: 60)\n");
    printf("  --b-frames N       Number of B-frames (default: 3)\n");
    printf("  --help             Show this help\n\n");
    printf("Examples:\n");
    printf("  ./file_sender --input video.mp4 --srt srt://127.0.0.1:9000\n");
    printf("  ./file_sender --input video.mp4 --srt srt://127.0.0.1:9000 --h265 --bitrate 3000\n");
}

int main(int argc, char *argv[]) {
    SenderConfig config = {
        .input_file = NULL,
        .srt_uri = NULL,
        .use_h265 = false,
        .bitrate = 2000,
        .fps = 30,
        .gop_size = 120,
        .iframe_interval = 60,
        .b_frames = 3
    };
    
    static struct option long_options[] = {
        {"input", required_argument, 0, 'i'},
        {"srt", required_argument, 0, 's'},
        {"h265", no_argument, 0, '5'},
        {"bitrate", required_argument, 0, 'b'},
        {"fps", required_argument, 0, 'f'},
        {"gop-size", required_argument, 0, 'g'},
        {"iframe-interval", required_argument, 0, 'I'},
        {"b-frames", required_argument, 0, 'B'},
        {"help", no_argument, 0, 'h'},
        {0, 0, 0, 0}
    };
    
    int opt;
    int option_index = 0;
    
    while ((opt = getopt_long(argc, argv, "i:s:5b:f:g:I:B:h", 
                              long_options, &option_index)) != -1) {
        switch (opt) {
            case 'i':
                config.input_file = optarg;
                break;
            case 's':
                config.srt_uri = optarg;
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
            case 'I':
                config.iframe_interval = atoi(optarg);
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
    
    if (!config.input_file || !config.srt_uri) {
        fprintf(stderr, "Error: --input and --srt are required\n\n");
        print_usage();
        return 1;
    }
    
    // Check if input file exists
    struct stat st;
    if (stat(config.input_file, &st) != 0) {
        fprintf(stderr, "Error: Input file '%s' does not exist\n", config.input_file);
        return 1;
    }
    
    run_sender(&config);
    return 0;
}
