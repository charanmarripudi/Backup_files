#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <signal.h>
#include <gst/gst.h>
#include <stdbool.h>
#include <time.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <getopt.h>

// Configuration structure
typedef struct {
    char *srt_uri;
    char *storage_path;
    bool enable_analytics;
    bool use_h265;
    bool decode_video;
    int segment_duration;
    int buffer_size;
    char *analytics_callback;
} ReceiverConfig;

// Global variables
static GstElement *pipeline = NULL;
static volatile sig_atomic_t keep_running = 1;
static int file_counter = 0;
static double total_bytes_received = 0;
static struct timeval start_time;

// Signal handler
void signal_handler(int sig) {
    printf("\nReceived signal %d, shutting down...\n", sig);
    keep_running = 0;
}

// Get current timestamp string
void get_timestamp(char *buffer, size_t size) {
    time_t now = time(NULL);
    struct tm *t = localtime(&now);
    strftime(buffer, size, "%Y%m%d_%H%M%S", t);
}

// Create SRT receiver pipeline
GstElement* create_receiver_pipeline(ReceiverConfig *config) {
    char pipeline_str[8192];
    const char *depay_name = config->use_h265 ? "rtph265depay" : "rtph264depay";
    const char *parse_name = config->use_h265 ? "h265parse" : "h264parse";
    const char *dec_name = config->use_h265 ? "qsvh265dec" : "qsvh264dec";
    const char *mux_name = config->use_h265 ? "mp4mux" : "mp4mux";
    
    char timestamp[32];
    get_timestamp(timestamp, sizeof(timestamp));
    
    char filename[512];
    snprintf(filename, sizeof(filename), "%s/recording_%s_%03d.mp4",
             config->storage_path, timestamp, file_counter++);
    
    printf("Recording to: %s\n", filename);
    
    if (config->enable_analytics && config->decode_video) {
        // Pipeline with storage + analytics
        snprintf(pipeline_str, sizeof(pipeline_str),
            "srtsrc uri=%s latency=2000000 ! "
            "queue max-size-buffers=%d ! "
            "%s ! "
            "%s ! "
            "tee name=t ! "
            "queue ! "
            "%s faststart=true ! "
            "filesink location=%s "
            "t. ! "
            "queue ! "
            "%s ! "
            "videoconvert ! "
            "video/x-raw,format=BGR ! "
            "fakesink name=analytics_sink sync=false",
            config->srt_uri,
            config->buffer_size,
            depay_name,
            parse_name,
            mux_name,
            filename,
            dec_name
        );
    } else if (config->enable_analytics) {
        // Analytics without decoding (just parse)
        snprintf(pipeline_str, sizeof(pipeline_str),
            "srtsrc uri=%s latency=2000000 ! "
            "queue max-size-buffers=%d ! "
            "%s ! "
            "%s ! "
            "tee name=t ! "
            "queue ! "
            "%s faststart=true ! "
            "filesink location=%s "
            "t. ! "
            "queue ! "
            "fakesink name=analytics_sink sync=false",
            config->srt_uri,
            config->buffer_size,
            depay_name,
            parse_name,
            mux_name,
            filename
        );
    } else {
        // Storage only
        snprintf(pipeline_str, sizeof(pipeline_str),
            "srtsrc uri=%s latency=2000000 ! "
            "queue max-size-buffers=%d ! "
            "%s ! "
            "%s ! "
            "%s faststart=true ! "
            "filesink location=%s",
            config->srt_uri,
            config->buffer_size,
            depay_name,
            parse_name,
            mux_name,
            filename
        );
    }
    
    printf("Receiver pipeline created\n");
    
    GError *error = NULL;
    GstElement *pipeline = gst_parse_launch(pipeline_str, &error);
    
    if (error) {
        g_printerr("Failed to create pipeline: %s\n", error->message);
        g_error_free(error);
        return NULL;
    }
    
    return pipeline;
}

// Create segmented receiver pipeline (splits into chunks)
GstElement* create_segmented_receiver_pipeline(ReceiverConfig *config) {
    char pipeline_str[8192];
    const char *depay_name = config->use_h265 ? "rtph265depay" : "rtph264depay";
    const char *parse_name = config->use_h265 ? "h265parse" : "h264parse";
    const char *mux_name = config->use_h265 ? "mp4mux" : "mp4mux";
    
    snprintf(pipeline_str, sizeof(pipeline_str),
        "srtsrc uri=%s latency=2000000 ! "
        "queue max-size-buffers=%d ! "
        "%s ! "
        "%s ! "
        "splitmuxsink name=muxsink "
        "muxer=%s "
        "location=%s/segment_%%05d.mp4 "
        "max-size-time=%d000000000",
        config->srt_uri,
        config->buffer_size,
        depay_name,
        parse_name,
        mux_name,
        config->storage_path,
        config->segment_duration
    );
    
    printf("Segmented receiver pipeline created (%d second segments)\n", 
           config->segment_duration);
    
    GError *error = NULL;
    GstElement *pipeline = gst_parse_launch(pipeline_str, &error);
    
    if (error) {
        g_printerr("Failed to create pipeline: %s\n", error->message);
        g_error_free(error);
        return NULL;
    }
    
    return pipeline;
}

// Run receiver
void run_receiver(ReceiverConfig *config) {
    GstBus *bus;
    GstMessage *msg;
    
    gst_init(NULL, NULL);
    
    printf("\n=== SRT Receiver ===\n");
    printf("SRT URI: %s\n", config->srt_uri);
    printf("Storage path: %s\n", config->storage_path);
    printf("Codec: %s\n", config->use_h265 ? "H.265" : "H.264");
    printf("Analytics: %s\n", config->enable_analytics ? "Enabled" : "Disabled");
    printf("Video decode: %s\n", config->decode_video ? "Yes" : "No");
    
    // Create storage directory
    struct stat st = {0};
    if (stat(config->storage_path, &st) == -1) {
        mkdir(config->storage_path, 0755);
        printf("Created storage directory: %s\n", config->storage_path);
    }
    
    // Create pipeline
    if (config->segment_duration > 0) {
        pipeline = create_segmented_receiver_pipeline(config);
    } else {
        pipeline = create_receiver_pipeline(config);
    }
    
    if (!pipeline) {
        fprintf(stderr, "Failed to create pipeline\n");
        return;
    }
    
    // Set up signal handlers
    signal(SIGINT, signal_handler);
    signal(SIGTERM, signal_handler);
    
    // Start pipeline
    gst_element_set_state(pipeline, GST_STATE_PLAYING);
    gettimeofday(&start_time, NULL);
    
    bus = gst_element_get_bus(pipeline);
    
    printf("\nReceiver running. Press Ctrl+C to stop.\n");
    printf("Time    Segments   Status\n");
    printf("--------------------------\n");
    
    int stat_counter = 0;
    struct timeval last_segment_time = start_time;
    
    // Main loop
    while (keep_running) {
        msg = gst_bus_timed_pop_filtered(bus, 100 * GST_MSECOND, 
            GST_MESSAGE_ERROR | GST_MESSAGE_EOS | 
            GST_MESSAGE_ELEMENT | GST_MESSAGE_STATE_CHANGED);
        
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
                case GST_MESSAGE_ELEMENT: {
                    const GstStructure *structure = gst_message_get_structure(msg);
                    if (structure && gst_structure_has_name(structure, "GstMultiFileSink")) {
                        // Segment created
                        struct timeval now;
                        gettimeofday(&now, NULL);
                        double elapsed = (now.tv_sec - last_segment_time.tv_sec) + 
                                        (now.tv_usec - last_segment_time.tv_usec) / 1e6;
                        last_segment_time = now;
                        
                        const char *filename = gst_structure_get_string(structure, "filename");
                        if (filename) {
                            printf("%5.0fs   %03d        Segment: %s\n", 
                                   (now.tv_sec - start_time.tv_sec),
                                   file_counter, 
                                   strrchr(filename, '/') ? strrchr(filename, '/') + 1 : filename);
                        }
                    }
                    break;
                }
                default:
                    break;
            }
            gst_message_unref(msg);
        }
        
        // Print status periodically
        stat_counter++;
        if (stat_counter >= 20) { // ~2 seconds
            struct timeval now;
            gettimeofday(&now, NULL);
            double elapsed = (now.tv_sec - start_time.tv_sec);
            
            printf("%5.0fs   %03d        Receiving...\n", 
                   elapsed, file_counter);
            
            stat_counter = 0;
        }
    }
    
    // Final stats
    struct timeval end_time;
    gettimeofday(&end_time, NULL);
    double total_time = (end_time.tv_sec - start_time.tv_sec) + 
                       (end_time.tv_usec - start_time.tv_usec) / 1e6;
    
    printf("\n=== Final Statistics ===\n");
    printf("Total runtime: %.2f seconds\n", total_time);
    printf("Segments recorded: %d\n", file_counter);
    printf("Storage location: %s\n", config->storage_path);
    
    // Cleanup
    printf("\nStopping pipeline...\n");
    gst_element_set_state(pipeline, GST_STATE_NULL);
    gst_object_unref(bus);
    gst_object_unref(pipeline);
    
    printf("Receiver stopped.\n");
}

// Print usage
void print_usage() {
    printf("SRT Receiver with Storage and Analytics\n");
    printf("Usage: srt_receiver [OPTIONS]\n\n");
    printf("Required:\n");
    printf("  --srt URI           SRT source URI (srt://:PORT for listener)\n\n");
    printf("Storage options:\n");
    printf("  --storage PATH      Storage directory (default: ./recordings)\n");
    printf("  --segment-dur SEC   Segment duration in seconds (0 = no segmentation)\n");
    printf("  --buffer-size N     Buffer size in packets (default: 200)\n\n");
    printf("Analytics options:\n");
    printf("  --analytics         Enable analytics pipeline\n");
    printf("  --decode-video      Decode video for analytics (default: parse only)\n");
    printf("  --h265              Expect H.265 stream (default: H.264)\n\n");
    printf("Examples:\n");
    printf("  srt_receiver --srt srt://:9000 --storage ./recordings\n");
    printf("  srt_receiver --srt srt://:9000 --analytics --decode-video --segment-dur 60\n");
}

int main(int argc, char *argv[]) {
    ReceiverConfig config = {
        .srt_uri = NULL,
        .storage_path = "./recordings",
        .enable_analytics = false,
        .use_h265 = false,
        .decode_video = false,
        .segment_duration = 0,
        .buffer_size = 200,
        .analytics_callback = NULL
    };
    
    // Parse command line arguments
    static struct option long_options[] = {
        {"srt", required_argument, 0, 's'},
        {"storage", required_argument, 0, 'd'},
        {"analytics", no_argument, 0, 'a'},
        {"decode-video", no_argument, 0, 'v'},
        {"h265", no_argument, 0, '5'},
        {"segment-dur", required_argument, 0, 't'},
        {"buffer-size", required_argument, 0, 'b'},
        {"help", no_argument, 0, 'h'},
        {0, 0, 0, 0}
    };
    
    int opt;
    int option_index = 0;
    
    while ((opt = getopt_long(argc, argv, "s:d:av5t:b:h", 
                              long_options, &option_index)) != -1) {
        switch (opt) {
            case 's':
                config.srt_uri = optarg;
                break;
            case 'd':
                config.storage_path = optarg;
                break;
            case 'a':
                config.enable_analytics = true;
                break;
            case 'v':
                config.decode_video = true;
                break;
            case '5':
                config.use_h265 = true;
                break;
            case 't':
                config.segment_duration = atoi(optarg);
                break;
            case 'b':
                config.buffer_size = atoi(optarg);
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
    if (!config.srt_uri) {
        fprintf(stderr, "Error: --srt is a required argument\n\n");
        print_usage();
        return 1;
    }
    
    // Validate parameters
    if (config.segment_duration < 0) {
        fprintf(stderr, "Error: Segment duration must be non-negative\n");
        return 1;
    }
    
    if (config.buffer_size <= 0) {
        fprintf(stderr, "Error: Buffer size must be positive\n");
        return 1;
    }
    
    // Run receiver
    run_receiver(&config);
    
    return 0;
}