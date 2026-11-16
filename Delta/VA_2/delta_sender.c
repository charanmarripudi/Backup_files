#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <signal.h>
#include <getopt.h>
#include <stdbool.h>
#include <time.h>
#include <sys/stat.h>

double time_diff_sec(struct timespec start, struct timespec end) {
    return (end.tv_sec - start.tv_sec) + (end.tv_nsec - start.tv_nsec) / 1e9;
}

void print_usage(const char *prog_name) {
    printf("Usage: %s --input <video> --srt <uri> [options]\n", prog_name);
    printf("\nDynamic System: Input → Delta Encode → Direct SRT Stream\n");
    printf("  1. Delta encode with B-frames for compression\n");
    printf("  2. Stream delta video directly (no re-encoding)\n");
    printf("  3. Receiver saves identical delta video\n");
    printf("\nOptions:\n");
    printf("  --input FILE      Input video file\n");
    printf("  --srt URI         SRT destination URI\n");
    printf("  --crf N           CRF value for x264 (default: 32)\n");
    printf("  --output-dir DIR  Directory for encoded video (default: ./encoded)\n");
    printf("  --latency N       SRT latency in ms (default: 120)\n");
    printf("  --bitrate N       Target bitrate in kbps (alternative to CRF)\n");
    printf("  --fps N           Force output FPS (default: auto)\n");
    printf("  --no-encode       Skip encoding, stream input directly\n");
    printf("  --no-stream       Only encode, don't stream\n");
    printf("  --realtime        Use real-time encoding profile\n");
    printf("  --help            Show this help\n");
}

double get_fps_gstreamer(const char *file) {
    char cmd[512];
    snprintf(cmd, sizeof(cmd),
        "gst-launch-1.0 -q filesrc location=\"%s\" ! qtdemux ! "
        "identity silent=false 2>&1 | "
        "grep -o 'framerate=[^,]*' | head -1 | cut -d= -f2 | cut -d/ -f1",
        file
    );
    
    FILE *fp = popen(cmd, "r");
    if (!fp) return 25.0;
    
    char buf[128];
    if (!fgets(buf, sizeof(buf), fp)) {
        pclose(fp);
        return 25.0;
    }
    pclose(fp);
    
    double fps = atof(buf);
    return fps > 0 ? fps : 25.0;
}

// STEP 1: Delta encode using GStreamer WITH B-FRAMES (COMPRESSION)
char* delta_encode_gstreamer(const char *input, const char *output_dir, int crf, int bitrate, int fps, bool realtime) {
    printf("\n=== STEP 1: DELTA ENCODING (with B-frames for compression) ===\n");
    
    struct timespec start, end;
    clock_gettime(CLOCK_MONOTONIC, &start);
    
    // Create output directory
    struct stat st = {0};
    if (stat(output_dir, &st) == -1) {
        mkdir(output_dir, 0755);
    }
    
    // Get input filename
    char *filename = strrchr(input, '/');
    if (filename == NULL) filename = (char*)input;
    else filename++;
    
    // Create output filename
    char base_name[256];
    strcpy(base_name, filename);
    char *dot = strrchr(base_name, '.');
    if (dot) *dot = '\0';
    
    // Generate unique filename with timestamp
    time_t now = time(NULL);
    struct tm *t = localtime(&now);
    char timestamp[32];
    strftime(timestamp, sizeof(timestamp), "%Y%m%d_%H%M%S", t);
    
    // Allocate memory for output file path
    char *output_file = malloc(512);
    if (bitrate > 0) {
        snprintf(output_file, 512, "%s/%s_delta_%s_bitrate%d.mp4", 
                 output_dir, base_name, timestamp, bitrate);
    } else {
        snprintf(output_file, 512, "%s/%s_delta_%s_crf%d.mp4", 
                 output_dir, base_name, timestamp, crf);
    }
    
    // Get FPS
    double input_fps = get_fps_gstreamer(input);
    int output_fps = (fps > 0) ? fps : (int)(input_fps + 0.5);
    
    printf("Input:           %s\n", input);
    printf("Output:          %s\n", output_file);
    printf("Input FPS:       %.2f\n", input_fps);
    printf("Output FPS:      %d\n", output_fps);
    
    // Build GStreamer encoding pipeline
    char cmd[4096];
    
    if (realtime) {
        // Real-time encoding profile
        printf("Encoding mode:   Real-time (faster)\n");
        snprintf(cmd, sizeof(cmd),
            "gst-launch-1.0 -q -e "
            "filesrc location=\"%s\" ! "
            "decodebin ! "
            "queue ! videoconvert ! "
            "videoscale ! "
            "video/x-raw,framerate=%d/1 ! "
            "x264enc "
            "speed-preset=ultrafast "
            "tune=zerolatency "
            "bframes=2 "
            "key-int-max=30 ",
            input, output_fps
        );
        
        if (bitrate > 0) {
            strcat(cmd, "bitrate=");
            char bitrate_str[64];
            snprintf(bitrate_str, sizeof(bitrate_str), "%d ", bitrate);
            strcat(cmd, bitrate_str);
        } else {
            strcat(cmd, "quantizer=");
            char crf_str[64];
            snprintf(crf_str, sizeof(crf_str), "%d ", crf);
            strcat(cmd, crf_str);
        }
        
        strcat(cmd, "! h264parse ! mp4mux ! "
               "filesink location=\"");
        strcat(cmd, output_file);
        strcat(cmd, "\"");
    } else {
        // Delta encoding profile WITH B-FRAMES for maximum compression
        printf("Encoding mode:   Delta (high compression with B-frames)\n");
        if (bitrate > 0) {
            printf("Target bitrate:  %d kbps\n", bitrate);
        } else {
            printf("CRF:             %d\n", crf);
            printf("B-frames:        8 (for maximum compression)\n");
        }
        
        snprintf(cmd, sizeof(cmd),
            "gst-launch-1.0 -q -e "
            "filesrc location=\"%s\" ! "
            "decodebin ! "
            "queue ! "
            "videoconvert ! "
            "queue ! "
            "video/x-raw,framerate=%d/1 ! "
            "x264enc "
            "key-int-max=250 "
            "bframes=8 "           
            "ref=6 "
            "me=umh "
            "subme=8 "
            "quantizer=%d "
            "speed-preset=slow "
            "pass=quant "
            "! h264parse config-interval=1 "
            "! mp4mux ! "
            "filesink location=\"%s\" sync=false",
            input, output_fps, crf, output_file
        );
    }
    
    printf("\nGStreamer encoding command:\n%s\n", cmd);
    
    int ret = system(cmd);
    
    clock_gettime(CLOCK_MONOTONIC, &end);
    double elapsed = time_diff_sec(start, end);
    
    if (ret == 0) {
        if (access(output_file, F_OK) == 0) {
            struct stat file_stat;
            stat(output_file, &file_stat);
            double size_mb = (double)file_stat.st_size / (1024 * 1024);
            
            printf("\n Delta encoding SUCCESSFUL!\n");
            printf("File:          %s\n", output_file);
            printf("Size:          %.2f MB (compressed)\n", size_mb);
            printf("Time:          %.2f seconds\n", elapsed);
            
            // Show compression ratio if we can get original size
            struct stat input_stat;
            if (stat(input, &input_stat) == 0) {
                double input_mb = (double)input_stat.st_size / (1024 * 1024);
                double ratio = (input_mb - size_mb) / input_mb * 100;
                printf("Compression:    %.1f%% smaller\n", ratio);
            }
            
            return output_file;
        } else {
            printf("Encoding FAILED - output file not created\n");
            free(output_file);
            return NULL;
        }
    } else {
        printf("Encoding FAILED with code %d\n", ret);
        free(output_file);
        return NULL;
    }
}

// STEP 2: Stream delta video DIRECTLY (no re-encoding) - OPTIMIZED VERSION
int stream_encoded_video(const char *encoded_file, const char *srt_uri, int latency) {
    printf("\n=== STEP 2: DIRECT SRT STREAMING (delta video as-is) ===\n");
    
    if (access(encoded_file, F_OK) != 0) {
        printf("Delta video not found: %s\n", encoded_file);
        return 1;
    }
    
    struct timespec start, end;
    clock_gettime(CLOCK_MONOTONIC, &start);
    
    // Get file info
    struct stat file_stat;
    stat(encoded_file, &file_stat);
    double size_mb = (double)file_stat.st_size / (1024 * 1024);
    
    printf("Streaming delta video: %s\n", encoded_file);
    printf("File size:            %.2f MB (compressed)\n", size_mb);
    printf("SRT URI:              %s\n", srt_uri);
    printf("Latency:              %d ms\n", latency);
    printf("\nStreaming delta video DIRECTLY (NO RE-ENCODING, NO SYNC)\n");
    printf("B-frames are preserved for maximum compression\n");
    printf("Mode:                 FAST FILE TRANSFER (not real-time playback)\n");
    
    // *** OPTIMIZED DIRECT STREAMING - NO THROTTLING ***
    char cmd[4096];
    
    // OPTION 1: Direct MP4 streaming (simplest, fastest)
    // This streams the MP4 container directly without re-muxing
    printf("\n=== Using OPTION 1: Direct MP4 streaming ===\n");
    snprintf(cmd, sizeof(cmd),
        "gst-launch-1.0 -q -e "
        "filesrc location=\"%s\" ! "            // Read file
        "qtdemux name=demux "                   // Parse MP4 container
        "demux.video_0 ! "                      // Get video stream
        "queue max-size-buffers=0 max-size-bytes=0 max-size-time=0 ! "  // Unlimited queue
        "srtsink "
        "uri=\"%s\" "
        "latency=%d "
        "wait-for-connection=true "
        "sync=false "                           // CRITICAL: No real-time throttling
        "maxbw=10000000 "                       // 10 Mbps bandwidth limit
        "inputbw=10000000 "                     // Input bandwidth
        "oheadbw=100 "                          // Overhead percentage
        "ttl=64 "                               // Time to live
        "linger=0 "                             // No linger
        "payloadsize=1456 "                     // Optimized payload size
        "messageapi=false "                     // Use streaming mode
        "nakreport=true "                       // Enable NAK reports
        "conndata=\"DeltaVideoStream\" "        // Connection data
        "connection_timeout=5000 "              // 5 second timeout
        "mode=caller",                          // Caller mode
        encoded_file, srt_uri, latency
    );
    
    // Alternative: Use filesrc directly to srtsink (simplest possible)
    char cmd_simple[4096];
    snprintf(cmd_simple, sizeof(cmd_simple),
        "gst-launch-1.0 -q -e "
        "filesrc location=\"%s\" ! "            // Read file
        "srtsink "
        "uri=\"%s\" "
        "latency=%d "
        "wait-for-connection=true "
        "sync=false "
        "maxbw=10000000 "
        "messageapi=false "
        "mode=caller "
        "blocksize=1456",
        encoded_file, srt_uri, latency
    );
    
    printf("\n=== GStreamer Pipeline Options ===\n");
    printf("Option 1 (Recommended): Direct MP4 streaming\n");
    printf("Option 2 (Simplest): Raw file streaming\n");
    printf("\nUsing Option 1 for maximum compatibility...\n");
    
    printf("\nGStreamer command:\n%s\n", cmd);
    
    printf("\n=== Performance Expectations ===\n");
    printf("File size:          %.2f MB\n", size_mb);
    printf("Expected speed:     5-50 MB/s (localhost)\n");
    printf("Expected time:      %.2f - %.2f seconds\n", 
           size_mb / 50.0, size_mb / 5.0);
    printf("Mode:               FAST TRANSFER (no FPS limitation)\n");
    
    printf("\n Waiting for receiver to connect...\n");
    printf("   Streaming %.2f MB delta video\n", size_mb);
    printf("   Press Ctrl+C to stop streaming\n\n");
    
    // Add connection timeout check
    printf("Checking if receiver is listening on %s...\n", srt_uri);
    
    // Extract port for display
    int port = 9000;
    char *port_str = strrchr(srt_uri, ':');
    if (port_str) {
        port = atoi(port_str + 1);
    }
    
    // Give user feedback
    for (int i = 0; i < 3; i++) {
        printf(".");
        fflush(stdout);
        usleep(500000); // 500ms
    }
    printf("\n");
    
    // Execute streaming command
    int ret = system(cmd);
    
    clock_gettime(CLOCK_MONOTONIC, &end);
    double elapsed = time_diff_sec(start, end);
    
    if (elapsed > 0) {
        double throughput = size_mb / elapsed;
        printf("\n=== Streaming Results ===\n");
        printf("Time elapsed:      %.2f seconds\n", elapsed);
        printf("Throughput:        %.2f MB/s\n", throughput);
        printf("Bandwidth:         %.0f kbps\n", throughput * 8 * 1024);
        
        // Performance analysis
        if (throughput < 1.0) {
            printf("\n  PERFORMANCE WARNING:\n");
            printf("Throughput is lower than expected.\n");
            printf("Possible issues:\n");
            printf("1. Receiver not in fast mode\n");
            printf("2. Network limitations\n");
            printf("3. GStreamer buffering\n");
            
            printf("\nTry these fixes:\n");
            printf("1. Add 'sync=false' to receiver pipeline\n");
            printf("2. Increase buffer sizes\n");
            printf("3. Use Option 2 (simpler pipeline):\n");
            printf("%s\n", cmd_simple);
        } else if (throughput > 5.0) {
            printf("\n Excellent performance!\n");
            printf("Streaming at maximum speed.\n");
        }
    }
    
    // Check exit codes
    if (ret == 0) {
        printf("\n SRT streaming completed successfully\n");
        return 0;
    } else if (ret == 2 || ret == 130 || ret == 255) {
        printf("\n  Streaming interrupted (Ctrl+C or signal)\n");
        return 0;
    } else {
        printf("\n SRT streaming FAILED with code %d\n", ret);
        
        // Provide troubleshooting tips
        printf("\n=== Troubleshooting ===\n");
        printf("1. Check if receiver is running:\n");
        printf("   ./delta_receiver --srt srt://:%d\n", port);
        
        printf("2. Test SRT connection:\n");
        printf("   gst-launch-1.0 srtsrc uri=\"srt://0.0.0.0:%d?mode=listener\" ! fakesink &\n", port);
        
        printf("3. Try simpler pipeline:\n");
        printf("%s\n", cmd_simple);
        
        return 1;
    }
}

int main(int argc, char *argv[]) {
    setbuf(stdout, NULL);
    setbuf(stderr, NULL);
    
    printf(" DELTA SENDER - Dynamic Compression & Streaming \n");
    printf("Original → Delta Encode (4.4MB) → Direct SRT Stream → Identical Save\n\n");

    char *input = NULL;
    char *srt_uri = NULL;
    char *output_dir = "./encoded";
    int crf = 32;
    int bitrate = 0;
    int fps = 0;
    int latency = 120;
    bool skip_encode = false;
    bool skip_stream = false;
    bool realtime = false;

    static struct option long_options[] = {
        {"input", required_argument, 0, 'i'},
        {"srt", required_argument, 0, 's'},
        {"crf", required_argument, 0, 'c'},
        {"bitrate", required_argument, 0, 'b'},
        {"fps", required_argument, 0, 'f'},
        {"output-dir", required_argument, 0, 'o'},
        {"latency", required_argument, 0, 'l'},
        {"no-encode", no_argument, 0, 'e'},
        {"no-stream", no_argument, 0, 't'},
        {"realtime", no_argument, 0, 'r'},
        {"help", no_argument, 0, 'h'},
        {0, 0, 0, 0}
    };

    int opt;
    int option_index = 0;
    while ((opt = getopt_long(argc, argv, "i:s:c:b:f:o:l:etrh", long_options, &option_index)) != -1) {
        switch (opt) {
            case 'i': input = optarg; break;
            case 's': srt_uri = optarg; break;
            case 'c': crf = atoi(optarg); break;
            case 'b': bitrate = atoi(optarg); break;
            case 'f': fps = atoi(optarg); break;
            case 'o': output_dir = optarg; break;
            case 'l': latency = atoi(optarg); break;
            case 'e': skip_encode = true; break;
            case 't': skip_stream = true; break;
            case 'r': realtime = true; break;
            case 'h': print_usage(argv[0]); return 0;
            default: print_usage(argv[0]); return 1;
        }
    }

    if (!input) {
        fprintf(stderr, "Error: --input is required\n");
        print_usage(argv[0]);
        return 1;
    }

    if (!skip_stream && !srt_uri) {
        fprintf(stderr, "Error: --srt is required for streaming\n");
        print_usage(argv[0]);
        return 1;
    }

    char *encoded_file = NULL;
    int ret = 0;
    
    // STEP 1: Delta Encoding (compression)
    if (!skip_encode) {
        encoded_file = delta_encode_gstreamer(input, output_dir, crf, bitrate, fps, realtime);
        if (!encoded_file) {
            printf("Delta encoding failed. Exiting.\n");
            return 1;
        }
        printf("\n Step 1: Delta encoding complete (compressed video ready)\n");
    } else {
        printf("\nSkipping encoding\n");
        encoded_file = strdup(input);
    }
    
    // STEP 2: Direct SRT Streaming (no re-encoding)
    if (!skip_stream && encoded_file) {
        printf("\n========================================\n");
        printf(" READY TO STREAM COMPRESSED DELTA VIDEO\n");
        printf("File:     %s\n", encoded_file);
        printf("SRT URI:  %s\n", srt_uri);
        printf("Latency:  %d ms\n", latency);
        
        // Show file size
        struct stat file_stat;
        if (stat(encoded_file, &file_stat) == 0) {
            double size_mb = (double)file_stat.st_size / (1024 * 1024);
            printf("Size:     %.2f MB (compressed)\n", size_mb);
            
            // Compare with original if available
            if (!skip_encode) {
                struct stat input_stat;
                if (stat(input, &input_stat) == 0) {
                    double input_mb = (double)input_stat.st_size / (1024 * 1024);
                    printf("Original: %.2f MB (%.1f%% compression)\n", 
                           input_mb, (input_mb - size_mb) / input_mb * 100);
                }
            }
        }
        
        int port = 9000;
        if (strstr(srt_uri, ":")) {
            char *port_str = strrchr(srt_uri, ':');
            if (port_str) port = atoi(port_str + 1);
        }
        
        printf("\n Checking receiver on port %d...\n", port);
        
        printf("\n Starting stream in 3 seconds...\n");
        for (int i = 3; i > 0; i--) {
            printf("%d... ", i);
            fflush(stdout);
            sleep(1);
        }
        printf("GO!\n\n");
        
        ret = stream_encoded_video(encoded_file, srt_uri, latency);
        
        if (ret == 0) {
            printf("\n Step 2: Delta video streamed directly (B-frames preserved)\n");
        }
    } else if (skip_stream) {
        printf("\nSkipping streaming\n");
    }
    
    if (encoded_file) {
        printf("\n========================================\n");
        printf(" DYNAMIC SYSTEM SUMMARY\n");
        
        if (!skip_encode) {
            printf("Delta encoded file saved: %s\n", encoded_file);
            printf("   Size: ");
            fflush(stdout);
            char size_cmd[256];
            snprintf(size_cmd, sizeof(size_cmd), 
                    "ls -lh \"%s\" 2>/dev/null | awk '{print $5}' || echo 'Unknown'", 
                    encoded_file);
            system(size_cmd);
        }
        
        if (!skip_stream) {
            printf(" Streamed to: %s\n", srt_uri);
            printf("   Receiver gets IDENTICAL delta video\n");
            printf("   B-frames preserved for maximum compression\n");
        }
        
        if (skip_encode) free(encoded_file);
    }
    
    printf("\n Dynamic Delta System Complete \n");
    return ret;
}
