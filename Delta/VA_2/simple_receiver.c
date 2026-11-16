
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
    char *srt_uri;
    char *output_dir;
    bool use_h265;
} ReceiverConfig;

// Global variables
static GstElement *pipeline = NULL;
static volatile sig_atomic_t keep_running = 1;

// Signal handler
void signal_handler(int sig) {
    printf("\nReceived signal %d, shutting down...\n", sig);
    keep_running = 0;
}

// Create receiver pipeline
GstElement* create_receiver_pipeline(ReceiverConfig *config) {
    char pipeline_str[4096];
    const char *depay_name = config->use_h265 ? "rtph265depay" : "rtph264depay";
    const char *parse_name = config->use_h265 ? "h265parse" : "h264parse";
    
    // Create output filename with timestamp
    char timestamp[32];
    time_t now = time(NULL);
    struct tm *t = localtime(&now);
    strftime(timestamp, sizeof(timestamp), "%Y%m%d_%H%M%S", t);
    
    char output_file[512];
    snprintf(output_file, sizeof(output_file), "%s/received_%s.mp4", 
             config->output_dir, timestamp);
    
    printf("Will save to: %s\n", output_file);
    
    snprintf(pipeline_str, sizeof(pipeline_str),
        "srtsink uri=\"%s\" latency=2000000 async=false ! "
        "queue ! "
        "%s ! "
        "%s ! "
        "mp4mux faststart=true ! "
        "filesink location=\"%s\"",
        config->srt_uri,
        depay_name,
        parse_name,
        output_file
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

// Run receiver
void run_receiver(ReceiverConfig *config) {
    GstBus *bus;
    GstMessage *msg;
    
    gst_init(NULL, NULL);
    
    printf("\n=== SRT Receiver ===\n");
    printf("SRT URI: %s\n", config->srt_uri);
    printf("Output directory: %s\n", config->output_dir);
    printf("Codec: %s\n", config->use_h265 ? "H.265" : "H.264");
    
    // Create output directory if it doesn't exist
    struct stat st = {0};
    if (stat(config->output_dir, &st) == -1) {
        mkdir(config->output_dir, 0755);
        printf("Created output directory: %s\n", config->output_dir);
    }
    
    pipeline = create_receiver_pipeline(config);
    if (!pipeline) {
        fprintf(stderr, "Failed to create pipeline\n");
        return;
    }
    
    // Set up signal handlers
    signal(SIGINT, signal_handler);
    signal(SIGTERM, signal_handler);
    
    // Start pipeline
    gst_element_set_state(pipeline, GST_STATE_PLAYING);
    time_t start_time = time(NULL);
    
    bus = gst_element_get_bus(pipeline);
    
    printf("\nReceiver running. Waiting for stream... Press Ctrl+C to stop.\n");
    printf("Time    Status\n");
    printf("-------------\n");
    
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
        
        // Show status
        time_t current_time = time(NULL);
        printf("%5lds  Waiting for stream...\n", current_time - start_time);
    }
    
    // Cleanup
    printf("\nStopping pipeline...\n");
    gst_element_set_state(pipeline, GST_STATE_NULL);
    gst_object_unref(bus);
    gst_object_unref(pipeline);
    
    printf("Receiver stopped.\n");
}

// Print usage
void print_usage() {
    printf("Simple SRT Receiver\n");
    printf("===================\n");
    printf("Usage: simple_receiver [OPTIONS]\n\n");
    printf("Required:\n");
    printf("  --srt URI          SRT URI to listen on (e.g., srt://:9000)\n\n");
    printf("Options:\n");
    printf("  --output DIR       Output directory (default: ./received)\n");
    printf("  --h265             Expect H.265 stream (default: H.264)\n");
    printf("  --help             Show this help\n\n");
    printf("Examples:\n");
    printf("  ./simple_receiver --srt srt://:9000\n");
    printf("  ./simple_receiver --srt srt://:9000 --output ./my_videos\n");
}

int main(int argc, char *argv[]) {
    ReceiverConfig config = {
        .srt_uri = NULL,
        .output_dir = "./received",
        .use_h265 = false
    };
    
    static struct option long_options[] = {
        {"srt", required_argument, 0, 's'},
        {"output", required_argument, 0, 'o'},
        {"h265", no_argument, 0, '5'},
        {"help", no_argument, 0, 'h'},
        {0, 0, 0, 0}
    };
    
    int opt;
    int option_index = 0;
    
    while ((opt = getopt_long(argc, argv, "s:o:5h", 
                              long_options, &option_index)) != -1) {
        switch (opt) {
            case 's':
                config.srt_uri = optarg;
                break;
            case 'o':
                config.output_dir = optarg;
                break;
            case '5':
                config.use_h265 = true;
                break;
            case 'h':
                print_usage();
                return 0;
            default:
                print_usage();
                return 1;
        }
    }
    
    if (!config.srt_uri) {
        fprintf(stderr, "Error: --srt is required\n\n");
        print_usage();
        return 1;
    }
    
    run_receiver(&config);
    return 0;
}
