// delta_sender_enhanced_rtsp_fixed.c - FIXED RTSP VERSION
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <signal.h>
#include <getopt.h>
#include <stdbool.h>
#include <time.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <fcntl.h>
#include <errno.h>

double time_diff_sec(struct timespec start, struct timespec end) {
    return (end.tv_sec - start.tv_sec) + (end.tv_nsec - start.tv_nsec) / 1e9;
}

volatile sig_atomic_t stop_flag = 0;

void signal_handler(int sig) {
    stop_flag = 1;
    printf("\nSignal %d received, stopping gracefully...\n", sig);
}

void print_usage(const char *prog_name) {
    printf("Usage: %s --input <source> --srt <uri> [options]\n", prog_name);
    printf("\nEnhanced Delta System with RTSP/H265 Support\n");
    printf("Source can be: file (mp4,avi), RTSP stream, RTMP stream\n");
    printf("\nOptions:\n");
    printf("  --input SOURCE    Input video file or stream URL\n");
    printf("                    (file: video.mp4, rtsp://, rtmp://)\n");
    printf("  --srt URI         SRT destination URI (e.g., srt://192.168.1.100:9000)\n");
    printf("  --crf N           CRF value (18-51, lower=better quality) (default: 32)\n");
    printf("  --bitrate N       Target bitrate in kbps (alternative to CRF)\n");
    printf("  --fps N           Force output FPS (default: auto)\n");
    printf("  --latency N       SRT latency in ms (default: 120)\n");
    printf("  --output-dir DIR  Directory for encoded video (default: ./encoded)\n");
    printf("\nCodec Options:\n");
    printf("  --h265            Use H.265/HEVC encoding (default: H.264)\n");
    printf("  --h264            Use H.264 encoding (default)\n");
    printf("\nRTSP Specific Options:\n");
    printf("  --rtsp-latency N  RTSP source latency in ms (default: 200)\n");
    printf("  --rtsp-timeout N  RTSP timeout in seconds (default: 5)\n");
    printf("  --rtsp-tcp        Force TCP for RTSP (default: udp)\n");
    printf("  --rtsp-udp        Force UDP for RTSP (default)\n");
    printf("  --no-audio        Drop audio from stream\n");
    printf("\nOperation Modes:\n");
    printf("  --no-save         Encode and stream without saving file\n");
    printf("  --direct          Direct encode->stream (no intermediate file)\n");
    printf("  --no-encode       Skip encoding, stream input directly\n");
    printf("  --no-stream       Only encode, don't stream\n");
    printf("  --realtime        Use real-time encoding profile (faster)\n");
    printf("  --record          Record stream to file (RTSP/RTMP only)\n");
    printf("  --help            Show this help\n");
    printf("\nExamples:\n");
    printf("  %s --input video.mp4 --srt srt://192.168.1.100:9000 --crf 28\n", prog_name);
    printf("  %s --input rtsp://camera.live --srt srt://:9000 --no-save --rtsp-tcp\n", prog_name);
    printf("  %s --input video.mp4 --srt srt://:9000 --direct --bitrate 2000\n", prog_name);
}

// Improved stream URL detection
bool is_stream_url(const char *input) {
    if (!input) return false;
    
    // Check for URL patterns
    if (strncmp(input, "rtsp://", 7) == 0 ||
        strncmp(input, "rtmp://", 7) == 0 ||
        strncmp(input, "udp://", 6) == 0 ||
        strncmp(input, "http://", 7) == 0 ||
        strncmp(input, "https://", 8) == 0) {
        return true;
    }
    
    // Check for file extension (common video files)
    const char *ext = strrchr(input, '.');
    if (ext) {
        if (strcmp(ext, ".mp4") == 0 || strcmp(ext, ".avi") == 0 ||
            strcmp(ext, ".mkv") == 0 || strcmp(ext, ".mov") == 0 ||
            strcmp(ext, ".flv") == 0 || strcmp(ext, ".ts") == 0 ||
            strcmp(ext, ".m4v") == 0 || strcmp(ext, ".mpg") == 0 ||
            strcmp(ext, ".mpeg") == 0 || strcmp(ext, ".wmv") == 0 ||
            strcmp(ext, ".264") == 0 || strcmp(ext, ".h264") == 0 ||
            strcmp(ext, ".h265") == 0 || strcmp(ext, ".hevc") == 0) {
            return false; // These are files
        }
    }
    
    return false;
}

// Check if file exists (for file inputs)
bool file_exists(const char *filename) {
    if (!filename) return false;
    
    // If it's a stream URL, don't check file existence
    if (is_stream_url(filename)) {
        return true; // Assume stream exists
    }
    
    struct stat buffer;
    return (stat(filename, &buffer) == 0);
}

// Safe string copy with truncation
void safe_strcpy(char *dest, const char *src, size_t dest_size) {
    if (!dest || !src || dest_size == 0) return;
    
    size_t src_len = strlen(src);
    size_t copy_len = (src_len < dest_size - 1) ? src_len : dest_size - 1;
    strncpy(dest, src, copy_len);
    dest[copy_len] = '\0';
}

// Test RTSP connection
bool test_rtsp_connection(const char *rtsp_url) {
    printf("Testing RTSP connection to: %s\n", rtsp_url);
    
    char test_cmd[1024];
    snprintf(test_cmd, sizeof(test_cmd),
        "timeout 5 gst-launch-1.0 rtspsrc location=\"%s\" timeout=3000000 ! fakesink 2>&1 | "
        "grep -q 'linked' && echo 'RTSP OK' || echo 'RTSP FAILED'",
        rtsp_url);
    
    FILE *fp = popen(test_cmd, "r");
    if (!fp) {
        printf("Failed to test RTSP connection\n");
        return false;
    }
    
    char buf[128];
    if (fgets(buf, sizeof(buf), fp)) {
        buf[strcspn(buf, "\n")] = 0;
        printf("RTSP test: %s\n", buf);
    }
    pclose(fp);
    
    return (strstr(buf, "OK") != NULL);
}

// Test SRT connection
bool test_srt_connection(const char *srt_uri) {
    printf("Testing local SRT port...\n");
    
    // Extract port
    int port = 9000;
    const char *ptr = strstr(srt_uri, "://");
    if (ptr) {
        ptr += 3;
        const char *last_colon = strrchr(ptr, ':');
        if (last_colon) {
            port = atoi(last_colon + 1);
        }
    }
    
    char test_cmd[256];
    snprintf(test_cmd, sizeof(test_cmd),
        "netstat -tuln | grep ':%d' >/dev/null && echo 'Port %d is free' || echo 'Port %d is in use'",
        port, port, port);
    
    FILE *fp = popen(test_cmd, "r");
    if (fp) {
        char buf[128];
        if (fgets(buf, sizeof(buf), fp)) {
            buf[strcspn(buf, "\n")] = 0;
            printf("SRT port test: %s\n", buf);
        }
        pclose(fp);
    }
    
    return true;
}

// Get FPS for files (streams return default)
double get_fps(const char *input) {
    if (!input) return 25.0;
    
    if (is_stream_url(input)) {
        return 25.0; // Default for streams
    }
    
    // For files, try to get FPS using ffprobe (more reliable)
    char cmd[512];
    snprintf(cmd, sizeof(cmd),
        "ffprobe -v error -select_streams v:0 -show_entries stream=r_frame_rate "
        "-of default=noprint_wrappers=1:nokey=1 \"%s\" 2>/dev/null",
        input
    );
    
    FILE *fp = popen(cmd, "r");
    if (!fp) return 25.0;
    
    char buf[128];
    if (!fgets(buf, sizeof(buf), fp)) {
        pclose(fp);
        return 25.0;
    }
    pclose(fp);
    
    // Parse fraction format (e.g., "30/1" or "30000/1001")
    char *slash = strchr(buf, '/');
    if (slash) {
        *slash = '\0';
        double num = atof(buf);
        double den = atof(slash + 1);
        if (den > 0) {
            return num / den;
        }
    }
    
    double fps = atof(buf);
    return (fps > 0) ? fps : 25.0;
}

// Create output filename
char* create_output_filename(const char *input, const char *output_dir, 
                            int crf, int bitrate, bool use_h265, bool is_recording) {
    if (!input || !output_dir) return NULL;
    
    char *output_file = malloc(512);
    if (!output_file) return NULL;
    
    // Get base name from input
    const char *filename = strrchr(input, '/');
    if (filename == NULL) filename = input;
    else filename++;
    
    char base_name[256];
    safe_strcpy(base_name, filename, sizeof(base_name));
    
    // Remove extension
    char *dot = strrchr(base_name, '.');
    if (dot) *dot = '\0';
    
    // Sanitize base name (remove special chars)
    for (char *p = base_name; *p; p++) {
        if (*p == ' ' || *p == ':' || *p == '?' || *p == '&' || *p == '=' || 
            *p == '@' || *p == '/' || *p == '\\') {
            *p = '_';
        }
    }
    
    // Add stream indicator
    const char *type_suffix = "";
    if (strstr(input, "rtsp://")) type_suffix = "_rtsp";
    else if (strstr(input, "rtmp://")) type_suffix = "_rtmp";
    
    // Add codec indicator
    const char *codec_suffix = use_h265 ? "_h265" : "_h264";
    
    // Add mode indicator
    const char *mode_suffix = is_recording ? "_record" : "_delta";
    
    // Add timestamp
    time_t now = time(NULL);
    struct tm *t = localtime(&now);
    char timestamp[32];
    strftime(timestamp, sizeof(timestamp), "%Y%m%d_%H%M%S", t);
    
    // Create full path
    if (bitrate > 0) {
        snprintf(output_file, 511, "%s/%s%s%s%s_%s_%dkbps.mp4", 
                 output_dir, base_name, type_suffix, mode_suffix, codec_suffix, 
                 timestamp, bitrate);
    } else {
        snprintf(output_file, 511, "%s/%s%s%s%s_%s_crf%d.mp4", 
                 output_dir, base_name, type_suffix, mode_suffix, codec_suffix, 
                 timestamp, crf);
    }
    
    return output_file;
}

// Mode 2: Direct encode to stream (no intermediate file) - SIMPLIFIED VERSION
int direct_encode_to_stream(const char *input, const char *srt_uri, 
                          int latency, int crf, int bitrate, int fps, 
                          bool realtime, bool use_h265, 
                          int rtsp_latency, int rtsp_timeout, bool use_tcp, bool no_audio) {
    printf("\n=== MODE 2: DIRECT ENCODE → STREAM ===\n");
    printf("Encoding directly to SRT stream (no file saved)\n");
    
    if (!input || !srt_uri) {
        printf("Error: Invalid input or SRT URI\n");
        return 1;
    }
    
    struct timespec start, end;
    clock_gettime(CLOCK_MONOTONIC, &start);
    
    // Get FPS
    int output_fps = (fps > 0) ? fps : 25;
    
    printf("Input:           %s\n", input);
    printf("SRT URI:         %s\n", srt_uri);
    printf("Latency:         %d ms\n", latency);
    printf("FPS:             %d\n", output_fps);
    printf("RTSP protocol:   %s\n", use_tcp ? "TCP" : "UDP");
    printf("Audio:           %s\n", no_audio ? "Disabled" : "Enabled");
    
    if (is_stream_url(input)) {
        printf("Input type:      Live Stream\n");
    } else {
        printf("Input type:      File\n");
    }
    
    // Test connections first
    printf("\n=== Testing Connections ===\n");
    
    bool rtsp_ok = false;
    bool srt_ok = false;
    
    if (strstr(input, "rtsp://")) {
        rtsp_ok = test_rtsp_connection(input);
    } else {
        rtsp_ok = true; // Not RTSP, skip test
    }
    
    srt_ok = test_srt_connection(srt_uri);
    
    if (!rtsp_ok) {
        printf("\nWARNING: RTSP connection may fail. Trying anyway...\n");
    }
    
    // Build direct pipeline
    char cmd[8192];
    
    // Check if input is RTSP
    if (strstr(input, "rtsp://")) {
        // Use SIMPLIFIED RTSP to SRT pipeline
        printf("\n=== Building RTSP → SRT Pipeline ===\n");
        
        // Protocol string
        const char *protocols = use_tcp ? "tcp" : "udp";
        
        if (use_h265) {
            // H.265 pipeline
            if (no_audio) {
                snprintf(cmd, sizeof(cmd),
                    "gst-launch-1.0 -q -e "
                    "rtspsrc location=\"%s\" latency=%d "
                    "protocols=%s "
                    "timeout=%d "
                    "retry=10 "
                    "! rtph265depay ! h265parse ! "
                    "avdec_h265 ! "
                    "videoconvert ! videoscale ! "
                    "video/x-raw,format=I420,framerate=%d/1 ! "
                    "x265enc speed-preset=ultrafast tune=zerolatency quantizer=%d ! "
                    "h265parse ! "
                    "mpegtsmux alignment=7 name=mux ! "
                    "srtsink uri=\"%s\" latency=%d wait-for-connection=true sync=true",
                    input, rtsp_latency, protocols, rtsp_timeout * 1000000,
                    output_fps, crf,
                    srt_uri, latency);
            } else {
                snprintf(cmd, sizeof(cmd),
                    "gst-launch-1.0 -q -e "
                    "rtspsrc location=\"%s\" latency=%d "
                    "protocols=%s "
                    "timeout=%d "
                    "retry=10 "
                    "name=source "
                    "source. ! rtph265depay ! h265parse ! "
                    "avdec_h265 ! "
                    "videoconvert ! videoscale ! "
                    "video/x-raw,format=I420,framerate=%d/1 ! "
                    "x265enc speed-preset=ultrafast tune=zerolatency quantizer=%d ! "
                    "h265parse ! mux. "
                    "source. ! rtpopusdepay ! opusparse ! mux. "
                    "mpegtsmux alignment=7 name=mux ! "
                    "srtsink uri=\"%s\" latency=%d wait-for-connection=true sync=true",
                    input, rtsp_latency, protocols, rtsp_timeout * 1000000,
                    output_fps, crf,
                    srt_uri, latency);
            }
        } else {
            // H.264 pipeline - SIMPLER VERSION
            if (no_audio) {
                snprintf(cmd, sizeof(cmd),
                    "gst-launch-1.0 -q -e "
                    "rtspsrc location=\"%s\" latency=%d "
                    "protocols=%s "
                    "timeout=%d "
                    "retry=10 "
                    "! application/x-rtp,media=video ! "
                    "rtph264depay ! h264parse ! "
                    "avdec_h264 ! "
                    "videoconvert ! "
                    "video/x-raw,format=I420,framerate=%d/1 ! "
                    "x264enc speed-preset=ultrafast tune=zerolatency quantizer=%d ! "
                    "h264parse ! "
                    "mpegtsmux alignment=7 ! "
                    "srtsink uri=\"%s\" latency=%d wait-for-connection=true sync=true",
                    input, rtsp_latency, protocols, rtsp_timeout * 1000000,
                    output_fps, crf,
                    srt_uri, latency);
            } else {
                snprintf(cmd, sizeof(cmd),
                    "gst-launch-1.0 -q -e "
                    "rtspsrc location=\"%s\" latency=%d "
                    "protocols=%s "
                    "timeout=%d "
                    "retry=10 "
                    "name=source "
                    "source. ! application/x-rtp,media=video ! rtph264depay ! h264parse ! "
                    "avdec_h264 ! "
                    "videoconvert ! "
                    "video/x-raw,format=I420,framerate=%d/1 ! "
                    "x264enc speed-preset=ultrafast tune=zerolatency quantizer=%d ! "
                    "h264parse ! mux. "
                    "source. ! application/x-rtp,media=audio ! rtpopusdepay ! opusparse ! mux. "
                    "mpegtsmux alignment=7 name=mux ! "
                    "srtsink uri=\"%s\" latency=%d wait-for-connection=true sync=true",
                    input, rtsp_latency, protocols, rtsp_timeout * 1000000,
                    output_fps, crf,
                    srt_uri, latency);
            }
        }
    } else {
        // File or other source
        if (use_h265) {
            snprintf(cmd, sizeof(cmd),
                "gst-launch-1.0 -q -e "
                "filesrc location=\"%s\" ! "
                "qtdemux name=demux "
                "demux.video_0 ! h265parse ! avdec_h265 ! "
                "videoconvert ! "
                "video/x-raw,format=I420,framerate=%d/1 ! "
                "x265enc speed-preset=ultrafast tune=zerolatency quantizer=%d ! "
                "h265parse ! "
                "mpegtsmux alignment=7 ! "
                "srtsink uri=\"%s\" latency=%d wait-for-connection=true sync=true",
                input, output_fps, crf, srt_uri, latency);
        } else {
            snprintf(cmd, sizeof(cmd),
                "gst-launch-1.0 -q -e "
                "filesrc location=\"%s\" ! "
                "qtdemux name=demux "
                "demux.video_0 ! h264parse ! avdec_h264 ! "
                "videoconvert ! "
                "video/x-raw,format=I420,framerate=%d/1 ! "
                "x264enc speed-preset=ultrafast tune=zerolatency quantizer=%d ! "
                "h264parse ! "
                "mpegtsmux alignment=7 ! "
                "srtsink uri=\"%s\" latency=%d wait-for-connection=true sync=true",
                input, output_fps, crf, srt_uri, latency);
        }
    }
    
    printf("\n=== GStreamer Pipeline ===\n%s\n", cmd);
    
    printf("\n=== Starting Stream ===\n");
    printf("1. Make sure receiver is running first\n");
    printf("2. Press Ctrl+C to stop streaming\n");
    printf("3. Starting in 3 seconds...\n");
    
    for (int i = 3; i > 0; i--) {
        printf("%d... ", i);
        fflush(stdout);
        sleep(1);
    }
    printf("GO!\n\n");
    
    // Set up signal handler
    signal(SIGINT, SIG_DFL); // Let GStreamer handle Ctrl+C
    signal(SIGTERM, SIG_DFL);
    
    int ret = system(cmd);
    
    clock_gettime(CLOCK_MONOTONIC, &end);
    double elapsed = time_diff_sec(start, end);
    
    if (elapsed > 0) {
        printf("\n=== Streaming Statistics ===\n");
        printf("Total time:      %.2f seconds\n", elapsed);
        printf("Status:          %s\n", 
               (ret == 0 || ret == 130 || ret == 2 || ret == 255) ? "Completed" : "Failed");
    }
    
    // Check exit codes
    if (ret == 0) return 0;  // Success
    if (ret == 130 || ret == 2 || ret == 255) return 0;  // Ctrl+C or terminated
    
    printf("\n=== Error Analysis ===\n");
    printf("Exit code: %d\n", ret);
    printf("\nPossible issues:\n");
    printf("1. RTSP camera not accessible\n");
    printf("2. Authentication failed\n");
    printf("3. Camera requires specific RTSP path\n");
    printf("4. Network/firewall issues\n");
    printf("5. Try --rtsp-tcp option\n");
    printf("6. Try --no-audio option\n");
    
    return 1;  // Error
}

// Simple test RTSP
int test_rtsp_simple(const char *rtsp_url) {
    printf("\n=== SIMPLE RTSP TEST ===\n");
    printf("Testing: %s\n", rtsp_url);
    
    char test_cmd[1024];
    snprintf(test_cmd, sizeof(test_cmd),
        "timeout 10 gst-launch-1.0 "
        "rtspsrc location=\"%s\" timeout=5000000 ! "
        "rtph264depay ! h264parse ! avdec_h264 ! "
        "autovideosink 2>&1",
        rtsp_url);
    
    printf("Test command: %s\n", test_cmd);
    printf("Running test for 10 seconds...\n");
    
    int ret = system(test_cmd);
    
    if (ret == 0 || ret == 130 || ret == 2 || ret == 255) {
        printf("\nRTSP test PASSED - stream is accessible\n");
        return 0;
    } else {
        printf("\nRTSP test FAILED - cannot access stream\n");
        printf("Exit code: %d\n", ret);
        return 1;
    }
}

int main(int argc, char *argv[]) {
    setbuf(stdout, NULL);
    setbuf(stderr, NULL);
    
    printf("🎬 DELTA SENDER ENHANCED - RTSP Fixed Version 🎬\n");
    printf("Version: 2.2 | Supports: Files, RTSP, RTMP streams\n");
    printf("Codecs: H.264 (default), H.265 (optional)\n\n");

    // Configuration variables
    char *input = NULL;
    char *srt_uri = NULL;
    char *output_dir = "./encoded";
    int crf = 32;
    int bitrate = 0;
    int fps = 0;
    int latency = 120;
    int rtsp_latency = 200;
    int rtsp_timeout = 5;
    
    // Mode flags
    bool use_h265 = false;
    bool no_save = false;
    bool direct_mode = false;
    bool skip_encode = false;
    bool skip_stream = false;
    bool realtime = false;
    bool record_mode = false;
    bool rtsp_tcp = false;
    bool rtsp_udp = true;
    bool no_audio = false;
    bool test_only = false;
    
    // Command line options
    static struct option long_options[] = {
        // Basic options
        {"input", required_argument, 0, 'i'},
        {"srt", required_argument, 0, 's'},
        {"crf", required_argument, 0, 'c'},
        {"bitrate", required_argument, 0, 'b'},
        {"fps", required_argument, 0, 'f'},
        {"latency", required_argument, 0, 'l'},
        {"output-dir", required_argument, 0, 'o'},
        
        // Codec options
        {"h265", no_argument, 0, '5'},
        {"h264", no_argument, 0, '4'},
        
        // RTSP options
        {"rtsp-latency", required_argument, 0, 'R'},
        {"rtsp-timeout", required_argument, 0, 'T'},
        {"rtsp-tcp", no_argument, 0, 't'},
        {"rtsp-udp", no_argument, 0, 'u'},
        {"no-audio", no_argument, 0, 'a'},
        
        // Mode options
        {"no-save", no_argument, 0, 'x'},
        {"direct", no_argument, 0, 'd'},
        {"no-encode", no_argument, 0, 'e'},
        {"no-stream", no_argument, 0, 'n'},
        {"realtime", no_argument, 0, 'r'},
        {"record", no_argument, 0, 'C'},
        {"test", no_argument, 0, 'k'},
        
        // Help
        {"help", no_argument, 0, 'h'},
        {0, 0, 0, 0}
    };

    // Parse command line
    int opt;
    int option_index = 0;
    while ((opt = getopt_long(argc, argv, "i:s:c:b:f:l:o:45R:T:tuanxdenrCkh", 
                              long_options, &option_index)) != -1) {
        switch (opt) {
            case 'i': input = optarg; break;
            case 's': srt_uri = optarg; break;
            case 'c': crf = atoi(optarg); break;
            case 'b': bitrate = atoi(optarg); break;
            case 'f': fps = atoi(optarg); break;
            case 'l': latency = atoi(optarg); break;
            case 'o': output_dir = optarg; break;
            case '5': use_h265 = true; break;
            case '4': use_h265 = false; break;
            case 'R': rtsp_latency = atoi(optarg); break;
            case 'T': rtsp_timeout = atoi(optarg); break;
            case 't': rtsp_tcp = true; rtsp_udp = false; break;
            case 'u': rtsp_udp = true; rtsp_tcp = false; break;
            case 'a': no_audio = true; break;
            case 'x': no_save = true; break;
            case 'd': direct_mode = true; break;
            case 'e': skip_encode = true; break;
            case 'n': skip_stream = true; break;
            case 'r': realtime = true; break;
            case 'C': record_mode = true; break;
            case 'k': test_only = true; break;
            case 'h': print_usage(argv[0]); return 0;
            default: print_usage(argv[0]); return 1;
        }
    }

    // Validate input
    if (!input) {
        fprintf(stderr, "Error: --input is required\n");
        print_usage(argv[0]);
        return 1;
    }

    // Check if input is valid (for files only)
    if (!is_stream_url(input) && !file_exists(input)) {
        fprintf(stderr, "Error: Input file '%s' not found\n", input);
        fprintf(stderr, "   If it's a stream URL, make sure it starts with rtsp://, rtmp://, etc.\n");
        return 1;
    }

    // Check streaming requirements
    if (!skip_stream && !srt_uri && !record_mode && !test_only) {
        fprintf(stderr, "Error: --srt is required for streaming\n");
        print_usage(argv[0]);
        return 1;
    }

    // Validate parameters
    if (crf < 0 || crf > 51) {
        fprintf(stderr, " Warning: CRF should be between 0-51, using default 32\n");
        crf = 32;
    }
    
    if (bitrate < 0) {
        fprintf(stderr, "Error: Bitrate cannot be negative\n");
        return 1;
    }
    
    if (latency < 20) {
        fprintf(stderr, " Warning: Latency %dms is very low, may cause issues\n", latency);
    }
    
    if (rtsp_timeout < 1) {
        fprintf(stderr, " Warning: RTSP timeout too low, using 5 seconds\n");
        rtsp_timeout = 5;
    }

    // Determine protocol
    bool use_tcp = rtsp_tcp;
    if (!rtsp_tcp && !rtsp_udp) {
        use_tcp = true; // Default to TCP for reliability
    }

    // Main processing
    bool is_stream_input = is_stream_url(input);
    
    printf("\n=== Configuration Summary ===\n");
    printf("Input:           %s\n", input);
    printf("Input type:      %s\n", is_stream_input ? "Stream" : "File");
    printf("Codec:           %s\n", use_h265 ? "H.265/HEVC" : "H.264");
    printf("CRF:             %d\n", crf);
    if (bitrate > 0) printf("Bitrate:         %d kbps\n", bitrate);
    if (fps > 0) printf("Output FPS:      %d\n", fps);
    printf("Output dir:      %s\n", output_dir);
    if (srt_uri && !skip_stream) printf("SRT target:      %s\n", srt_uri);
    if (latency != 120) printf("Latency:         %d ms\n", latency);
    if (is_stream_input) {
        printf("RTSP protocol:   %s\n", use_tcp ? "TCP" : "UDP");
        printf("RTSP latency:    %d ms\n", rtsp_latency);
        printf("RTSP timeout:    %d sec\n", rtsp_timeout);
        printf("Audio:           %s\n", no_audio ? "Disabled" : "Enabled");
    }
    
    // Test mode
    if (test_only && strstr(input, "rtsp://")) {
        return test_rtsp_simple(input);
    }
    
    // Determine operation mode
    if (skip_encode) {
        // MODE 3: Stream existing file without encoding
        printf("\n=== MODE: STREAM EXISTING FILE (no encoding) ===\n");
        // Simplified for now
        printf("Not implemented in this version\n");
        return 1;
    } else if ((direct_mode || no_save) && is_stream_input) {
        // MODE: Direct RTSP to SRT streaming
        printf("\n=== MODE: DIRECT RTSP → SRT STREAMING ===\n");
        if (!skip_stream) {
            int ret = direct_encode_to_stream(input, srt_uri, latency, crf, bitrate, 
                                           fps, realtime, use_h265, 
                                           rtsp_latency, rtsp_timeout, use_tcp, no_audio);
            if (ret != 0) {
                printf("\n=== TROUBLESHOOTING ===\n");
                printf("Try these alternative RTSP URLs:\n");
                printf("1. rtsp://admin:Algo12345678@192.168.96.28:554/ch0_0.264\n");
                printf("\nCommand options to try:\n");
                printf("--rtsp-tcp    (force TCP protocol)\n");
                printf("--no-audio    (disable audio)\n");
                printf("--rtsp-latency 500  (increase latency)\n");
                printf("--rtsp-timeout 10   (increase timeout)\n");
            }
            return ret;
        }
    } else if (direct_mode || no_save) {
        // MODE 2: Direct encode to stream (no file saved)
        printf("\n=== MODE: DIRECT ENCODE TO STREAM ===\n");
        if (!skip_stream) {
            return direct_encode_to_stream(input, srt_uri, latency, crf, bitrate, 
                                       fps, realtime, use_h265, 
                                       rtsp_latency, rtsp_timeout, use_tcp, no_audio);
        }
    } else {
        printf("\n=== MODE: ENCODE AND SAVE FILE ===\n");
        printf("Not implemented in this version\n");
        return 1;
    }
    
    return 0;
}