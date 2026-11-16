// delta_receiver_enhanced_fixed.c - TESTED AND FIXED VERSION
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
#include <errno.h>

#define MAX_FILENAME 512

double time_diff_sec(struct timespec start, struct timespec end) {
    return (end.tv_sec - start.tv_sec) + (end.tv_nsec - start.tv_nsec) / 1e9;
}

volatile sig_atomic_t stop_receiving = 0;

void signal_handler(int sig) {
    stop_receiving = 1;
    printf("\nSignal %d received, stopping gracefully...\n", sig);
}

void print_usage(const char *prog_name) {
    printf("Usage: %s --srt <uri> [options]\n", prog_name);
    printf("\nEnhanced SRT Receiver with H265 Support\n");
    printf("\nOptions:\n");
    printf("  --srt URI         SRT listen URI (e.g., srt://:9000)\n");
    printf("  --output DIR      Output directory (default: ./received)\n");
    printf("  --filename NAME   Custom output filename\n");
    printf("  --buffer N        SRT latency buffer in ms (default: 120)\n");
    printf("  --duration N      Stop after N seconds (0 = infinite)\n");
    printf("  --format FMT      Output format: mp4, mkv, ts (default: mp4)\n");
    printf("  --h265            Expect H.265/HEVC stream (auto-detect if not specified)\n");
    printf("  --h264            Expect H.264 stream (default)\n");
    printf("  --display         Display while receiving\n");
    printf("  --no-save         Display only, don't save to file\n");
    printf("  --analyze         Run analytics after receiving\n");
    printf("  --segment N       Segment duration in seconds (0 = single file)\n");
    printf("  --auto-decode     Auto-detect codec (default: true)\n");
    printf("  --help            Show this help\n");
}

int extract_port(const char *srt_uri) {
    if (!srt_uri) return 9000;
    
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

void get_timestamp_str(char *buffer, size_t size) {
    if (!buffer || size == 0) return;
    
    time_t now = time(NULL);
    struct tm *tm_info = localtime(&now);
    if (tm_info) {
        strftime(buffer, size, "%Y-%m-%d %H:%M:%S", tm_info);
    } else {
        strncpy(buffer, "Unknown", size - 1);
        buffer[size - 1] = '\0';
    }
}

// Safe string copy
void safe_strcpy(char *dest, const char *src, size_t dest_size) {
    if (!dest || !src || dest_size == 0) return;
    
    size_t src_len = strlen(src);
    size_t copy_len = (src_len < dest_size - 1) ? src_len : dest_size - 1;
    strncpy(dest, src, copy_len);
    dest[copy_len] = '\0';
}

void create_output_filename(char *output_file, size_t size, 
                           const char *output_dir, const char *custom_name,
                           const char *format, int port, bool is_h265) {
    if (!output_file || !output_dir || !format) return;
    
    char timestamp[32];
    time_t now = time(NULL);
    struct tm *t = localtime(&now);
    if (t) {
        strftime(timestamp, sizeof(timestamp), "%Y%m%d_%H%M%S", t);
    } else {
        strcpy(timestamp, "unknown");
    }
    
    const char *codec = is_h265 ? "h265" : "h264";
    
    if (custom_name && strlen(custom_name) > 0) {
        snprintf(output_file, size - 1, "%s/%s_%s_%s.%s", 
                 output_dir, custom_name, timestamp, codec, format);
    } else {
        snprintf(output_file, size - 1, "%s/received_port%d_%s_%s.%s", 
                 output_dir, port, timestamp, codec, format);
    }
}

// Create segmented output filename pattern
char* create_segmented_filename(const char *output_dir, int port, bool is_h265, const char *format) {
    if (!output_dir || !format) return NULL;
    
    char *pattern = malloc(512);
    if (!pattern) return NULL;
    
    char timestamp[32];
    time_t now = time(NULL);
    struct tm *t = localtime(&now);
    if (t) {
        strftime(timestamp, sizeof(timestamp), "%Y%m%d_%H%M%S", t);
    } else {
        strcpy(timestamp, "unknown");
    }
    
    const char *codec = is_h265 ? "h265" : "h264";
    
    snprintf(pattern, 511, "%s/segment_port%d_%s_%s_%%05d.%s", 
             output_dir, port, timestamp, codec, format);
    
    return pattern;
}

// Create GStreamer pipeline with H265 support (FIXED: simpler and more reliable)
void create_gstreamer_pipeline(char *pipeline, size_t size, 
                               int port, int buffer_ms, int duration,
                               const char *output_file, bool display,
                               bool save_file, bool use_h265, const char *format,
                               int segment_duration, bool auto_decode) {
    if (!pipeline || size == 0) return;
    
    pipeline[0] = '\0';
    
    // Validate format
    char muxer[64];
    if (strcmp(format, "mp4") == 0) {
        strcpy(muxer, "mp4mux");
    } else if (strcmp(format, "mkv") == 0) {
        strcpy(muxer, "matroskamux");
    } else if (strcmp(format, "ts") == 0) {
        strcpy(muxer, "mpegtsmux");
    } else {
        strcpy(muxer, "mp4mux");
    }
    
    // Start with common part
    snprintf(pipeline, size,
        "gst-launch-1.0 -q -e "
        "srtsrc uri=\"srt://0.0.0.0:%d?mode=listener&latency=%d\" "
        "wait-for-connection=true ! "
        "tsdemux name=demux ",
        port, buffer_ms);
    
    size_t current_len = strlen(pipeline);
    
    if (!save_file && display) {
        // Display only mode
        if (auto_decode) {
            strncat(pipeline, "demux. ! queue ! decodebin ! videoconvert ! autovideosink",
                   size - current_len - 1);
        } else if (use_h265) {
            strncat(pipeline, "demux. ! queue ! h265parse ! avdec_h265 ! videoconvert ! autovideosink",
                   size - current_len - 1);
        } else {
            strncat(pipeline, "demux. ! queue ! h264parse ! avdec_h264 ! videoconvert ! autovideosink",
                   size - current_len - 1);
        }
        
    } else if (save_file && !display) {
        // Save only mode
        if (auto_decode) {
            strncat(pipeline, "demux. ! queue ! decodebin ! videoconvert ! x264enc ! h264parse ! ",
                   size - current_len - 1);
        } else if (use_h265) {
            strncat(pipeline, "demux. ! queue ! h265parse ! avdec_h265 ! videoconvert ! x265enc ! h265parse ! ",
                   size - current_len - 1);
        } else {
            strncat(pipeline, "demux. ! queue ! h264parse ! avdec_h264 ! videoconvert ! x264enc ! h264parse ! ",
                   size - current_len - 1);
        }
        
        if (segment_duration > 0 && output_file) {
            char segment_part[512];
            snprintf(segment_part, sizeof(segment_part),
                "splitmuxsink muxer=%s max-size-time=%d000000000 location=\"%s\"",
                muxer, segment_duration, output_file);
            strncat(pipeline, segment_part, size - strlen(pipeline) - 1);
        } else if (output_file) {
            char file_part[512];
            snprintf(file_part, sizeof(file_part),
                "%s ! filesink location=\"%s\" sync=true",
                muxer, output_file);
            strncat(pipeline, file_part, size - strlen(pipeline) - 1);
        }
        
    } else if (save_file && display) {
        // Save and display mode
        strncat(pipeline, "demux. ! queue ! tee name=t ", size - current_len - 1);
        current_len = strlen(pipeline);
        
        // Display branch
        if (auto_decode) {
            strncat(pipeline, "t. ! queue ! decodebin ! videoconvert ! autovideosink ",
                   size - current_len - 1);
        } else if (use_h265) {
            strncat(pipeline, "t. ! queue ! h265parse ! avdec_h265 ! videoconvert ! autovideosink ",
                   size - current_len - 1);
        } else {
            strncat(pipeline, "t. ! queue ! h264parse ! avdec_h264 ! videoconvert ! autovideosink ",
                   size - current_len - 1);
        }
        
        current_len = strlen(pipeline);
        strncat(pipeline, "t. ! queue ! ", size - current_len - 1);
        
        // Save branch
        if (auto_decode) {
            strncat(pipeline, "decodebin ! videoconvert ! x264enc ! h264parse ! ",
                   size - current_len - 1);
        } else if (use_h265) {
            strncat(pipeline, "h265parse ! avdec_h265 ! videoconvert ! x265enc ! h265parse ! ",
                   size - current_len - 1);
        } else {
            strncat(pipeline, "h264parse ! avdec_h264 ! videoconvert ! x264enc ! h264parse ! ",
                   size - current_len - 1);
        }
        
        current_len = strlen(pipeline);
        
        if (segment_duration > 0 && output_file) {
            char segment_part[512];
            snprintf(segment_part, sizeof(segment_part),
                "splitmuxsink muxer=%s max-size-time=%d000000000 location=\"%s\"",
                muxer, segment_duration, output_file);
            strncat(pipeline, segment_part, size - current_len - 1);
        } else if (output_file) {
            char file_part[512];
            snprintf(file_part, sizeof(file_part),
                "%s ! filesink location=\"%s\" sync=true",
                muxer, output_file);
            strncat(pipeline, file_part, size - current_len - 1);
        }
    }
    
    // Add duration if specified
    if (duration > 0) {
        char duration_cmd[256];
        snprintf(duration_cmd, sizeof(duration_cmd), "timeout %d ", duration);
        if (strlen(duration_cmd) + strlen(pipeline) < size) {
            memmove(pipeline + strlen(duration_cmd), pipeline, strlen(pipeline) + 1);
            memcpy(pipeline, duration_cmd, strlen(duration_cmd));
        }
    }
}

// Analyze received video file
void analyze_video_file(const char *filename) {
    if (!filename) return;
    
    printf("\n=== VIDEO ANALYSIS ===\n");
    
    // Check if file exists
    if (access(filename, F_OK) != 0) {
        printf("File not found: %s\n", filename);
        return;
    }
    
    // Get video info using ffprobe
    char cmd[512];
    
    // 1. Basic info
    snprintf(cmd, sizeof(cmd),
        "ffprobe -v error -select_streams v:0 "
        "-show_entries stream=codec_name,width,height,bit_rate,duration,has_b_frames "
        "-of csv=p=0 \"%s\" 2>/dev/null",
        filename);
    
    FILE *fp = popen(cmd, "r");
    if (fp) {
        char buf[256];
        if (fgets(buf, sizeof(buf), fp)) {
            buf[strcspn(buf, "\n")] = 0;
            char *token = strtok(buf, ",");
            int field = 0;
            while (token) {
                switch (field) {
                    case 0: printf("Codec:           %s\n", token); break;
                    case 1: 
                        if (strcmp(token, "N/A") != 0) {
                            printf("Resolution:      %s", token);
                            token = strtok(NULL, ",");
                            field++;
                            if (token && strcmp(token, "N/A") != 0) {
                                printf("x%s\n", token);
                            } else {
                                printf("\n");
                            }
                        } else {
                            token = strtok(NULL, ",");
                            field++;
                            printf("Resolution:      Unknown\n");
                        }
                        break;
                    case 3: 
                        if (strcmp(token, "N/A") != 0) {
                            long bitrate = atol(token);
                            printf("Bitrate:         %.1f Mbps\n", bitrate / 1000000.0);
                        }
                        break;
                    case 4: 
                        if (strcmp(token, "N/A") != 0) {
                            double duration = atof(token);
                            int minutes = (int)duration / 60;
                            int seconds = (int)duration % 60;
                            printf("Duration:        %d:%02d\n", minutes, seconds);
                        }
                        break;
                    case 5: 
                        printf("B-frames:        %s\n", 
                               strcmp(token, "1") == 0 ? "Yes" : "No");
                        break;
                }
                token = strtok(NULL, ",");
                field++;
            }
        }
        pclose(fp);
    }
    
    // 2. File size
    struct stat file_stat;
    if (stat(filename, &file_stat) == 0) {
        double size_mb = (double)file_stat.st_size / (1024 * 1024);
        printf("File size:       %.2f MB\n", size_mb);
    }
    
    // 3. Quick play command
    printf("\nTo play: ffplay -autoexit \"%s\"\n", filename);
}

int main(int argc, char *argv[]) {
    setbuf(stdout, NULL);
    setbuf(stderr, NULL);
    
    printf(" DELTA RECEIVER ENHANCED - Fixed & Tested Version \n");
    printf("Version: 2.1 | Supports H.264 and H.265 streams\n\n");

    // Configuration
    char *srt_uri = NULL;
    char *output_dir = "./received";
    char *custom_filename = NULL;
    char *output_format = "mp4";
    int buffer_ms = 120;
    int duration = 0;
    int segment_duration = 0;
    bool analyze = false;
    bool display = false;
    bool no_save = false;
    bool use_h265 = false;
    bool auto_decode = true;

    // Parse command line
    static struct option long_options[] = {
        {"srt", required_argument, 0, 's'},
        {"output", required_argument, 0, 'o'},
        {"filename", required_argument, 0, 'n'},
        {"format", required_argument, 0, 'm'},
        {"buffer", required_argument, 0, 'b'},
        {"duration", required_argument, 0, 'd'},
        {"segment", required_argument, 0, 'S'},
        {"h265", no_argument, 0, '5'},
        {"h264", no_argument, 0, '4'},
        {"display", no_argument, 0, 'v'},
        {"no-save", no_argument, 0, 'x'},
        {"analyze", no_argument, 0, 'a'},
        {"auto-decode", no_argument, 0, 'A'},
        {"help", no_argument, 0, 'h'},
        {0, 0, 0, 0}
    };

    int opt;
    int option_index = 0;
    while ((opt = getopt_long(argc, argv, "s:o:n:m:b:d:S:54vxaAh", 
                              long_options, &option_index)) != -1) {
        switch (opt) {
            case 's': srt_uri = optarg; break;
            case 'o': output_dir = optarg; break;
            case 'n': custom_filename = optarg; break;
            case 'm': output_format = optarg; break;
            case 'b': buffer_ms = atoi(optarg); break;
            case 'd': duration = atoi(optarg); break;
            case 'S': segment_duration = atoi(optarg); break;
            case '5': use_h265 = true; auto_decode = false; break;
            case '4': use_h265 = false; auto_decode = false; break;
            case 'v': display = true; break;
            case 'x': no_save = true; break;
            case 'a': analyze = true; break;
            case 'A': auto_decode = true; break;
            case 'h': print_usage(argv[0]); return 0;
            default: print_usage(argv[0]); return 1;
        }
    }

    if (!srt_uri) {
        fprintf(stderr, " Error: --srt is required\n");
        print_usage(argv[0]);
        return 1;
    }

    // Validate parameters
    if (buffer_ms < 20) {
        fprintf(stderr, " Warning: Buffer %dms is very low, may cause issues\n", buffer_ms);
        buffer_ms = 120;
    }
    
    if (segment_duration < 0) {
        fprintf(stderr, " Error: Segment duration cannot be negative\n");
        return 1;
    }
    
    // Validate output format
    if (strcmp(output_format, "mp4") != 0 &&
        strcmp(output_format, "mkv") != 0 &&
        strcmp(output_format, "ts") != 0) {
        fprintf(stderr, " Warning: Format '%s' not supported, using mp4\n", output_format);
        output_format = "mp4";
    }

    int port = extract_port(srt_uri);
    
    // Create output directory if saving
    if (!no_save) {
        struct stat st = {0};
        if (stat(output_dir, &st) == -1) {
            if (mkdir(output_dir, 0755) != 0) {
                fprintf(stderr, " Error: Cannot create output directory '%s': %s\n", 
                        output_dir, strerror(errno));
                return 1;
            }
            printf("Created output directory: %s\n", output_dir);
        }
    }

    char output_file[MAX_FILENAME] = "";
    char *output_pattern = NULL;
    
    if (!no_save) {
        if (segment_duration > 0) {
            // Segmented recording
            output_pattern = create_segmented_filename(output_dir, port, use_h265, output_format);
            if (!output_pattern) {
                fprintf(stderr, " Error: Cannot create output pattern\n");
                return 1;
            }
            safe_strcpy(output_file, output_pattern, sizeof(output_file));
        } else {
            // Single file
            create_output_filename(output_file, sizeof(output_file), 
                                 output_dir, custom_filename, output_format, 
                                 port, use_h265);
        }
    }

    struct timespec start, end;
    clock_gettime(CLOCK_MONOTONIC, &start);

    printf("\n=== Configuration ===\n");
    printf("SRT URI:          %s\n", srt_uri);
    printf("Port:             %d\n", port);
    printf("Buffer:           %d ms\n", buffer_ms);
    printf("Output dir:       %s\n", output_dir);
    if (!no_save) {
        if (segment_duration > 0) {
            printf("Output pattern:   %s\n", output_file);
            printf("Segment duration: %d seconds\n", segment_duration);
        } else {
            printf("Output file:      %s\n", output_file);
        }
    }
    printf("Output format:    %s\n", output_format);
    printf("Codec:            %s\n", use_h265 ? "H.265/HEVC" : "H.264");
    printf("Auto decode:      %s\n", auto_decode ? "Yes" : "No");
    printf("Display:          %s\n", display ? "Yes" : "No");
    printf("Save file:        %s\n", no_save ? "No" : "Yes");
    printf("Analyze:          %s\n", analyze ? "Yes" : "No");
    if (duration > 0) printf("Duration:         %d seconds\n", duration);

    printf("\n=== Starting Enhanced Receiver ===\n");
    printf("Supports H.264 and H.265 streams\n");
    printf("Mode: %s%s%s\n", 
           display ? "Display" : "", 
           !no_save ? (display ? " + Save" : "Save") : "",
           segment_duration > 0 ? " (Segmented)" : "");
    
    char timestamp[64];
    get_timestamp_str(timestamp, sizeof(timestamp));
    printf("[%s] Listening on port %d...\n", timestamp, port);
    printf("Waiting for stream...\n");
    
    // Create GStreamer pipeline
    char gst_cmd[8192];
    create_gstreamer_pipeline(gst_cmd, sizeof(gst_cmd),
                             port, buffer_ms, duration,
                             output_file, display,
                             !no_save, use_h265, output_format,
                             segment_duration, auto_decode);
    
    printf("\nGStreamer pipeline:\n%s\n", gst_cmd);
    printf("\nWaiting for SRT connection on port %d...\n", port);
    printf("Press Ctrl+C to stop\n");
    
    // Reset signal handlers for GStreamer
    signal(SIGINT, SIG_DFL);
    signal(SIGTERM, SIG_DFL);
    
    int ret = system(gst_cmd);
    
    clock_gettime(CLOCK_MONOTONIC, &end);
    double elapsed = time_diff_sec(start, end);

    printf("\n=== Receiver Complete ===\n");
    printf("Exit code: %d\n", ret);
    
    // Check and analyze output if saved
    if (!no_save) {
        if (segment_duration > 0 && output_pattern) {
            // Check for segmented files
            printf("\nChecking for recorded segments...\n");
            char base_pattern[512];
            safe_strcpy(base_pattern, output_pattern, sizeof(base_pattern));
            char *percent = strstr(base_pattern, "%05d");
            if (percent) *percent = '\0';
            
            char list_cmd[512];
            snprintf(list_cmd, sizeof(list_cmd), "ls -1 \"%s\"* 2>/dev/null | wc -l", base_pattern);
            FILE *fp = popen(list_cmd, "r");
            if (fp) {
                char buf[32];
                if (fgets(buf, sizeof(buf), fp)) {
                    int file_count = atoi(buf);
                    if (file_count > 0) {
                        printf("\n%d video segments received and saved!\n", file_count);
                        
                        // Get total size
                        snprintf(list_cmd, sizeof(list_cmd), "du -ch \"%s\"* 2>/dev/null | tail -1", base_pattern);
                        FILE *fp2 = popen(list_cmd, "r");
                        if (fp2) {
                            char size_buf[64];
                            if (fgets(size_buf, sizeof(size_buf), fp2)) {
                                size_buf[strcspn(size_buf, "\n")] = 0;
                                printf("Total size:      %s\n", size_buf);
                            }
                            pclose(fp2);
                        }
                        
                        // List first few files
                        snprintf(list_cmd, sizeof(list_cmd), "ls -lh \"%s\"* 2>/dev/null | head -5", base_pattern);
                        printf("First few segments:\n");
                        system(list_cmd);
                        
                        if (file_count > 5) {
                            printf("... and %d more files\n", file_count - 5);
                        }
                        
                        if (elapsed > 0.1) {
                            printf("\nTotal time:      %.2f seconds\n", elapsed);
                            printf("Average segment: %.1f seconds\n", elapsed / file_count);
                        }
                        
                        if (analyze && file_count > 0) {
                            // Analyze first segment
                            snprintf(list_cmd, sizeof(list_cmd), "ls -1 \"%s\"* 2>/dev/null | head -1", base_pattern);
                            FILE *fp3 = popen(list_cmd, "r");
                            if (fp3) {
                                char first_file[512];
                                if (fgets(first_file, sizeof(first_file), fp3)) {
                                    first_file[strcspn(first_file, "\n")] = 0;
                                    printf("\nAnalyzing first segment...\n");
                                    analyze_video_file(first_file);
                                }
                                pclose(fp3);
                            }
                        }
                    } else {
                        printf("\n No segments were saved\n");
                    }
                }
                pclose(fp);
            }
            
            free(output_pattern);
        } else if (strlen(output_file) > 0 && access(output_file, F_OK) == 0) {
            // Single file
            struct stat file_stat;
            if (stat(output_file, &file_stat) == 0) {
                double size_mb = (double)file_stat.st_size / (1024 * 1024);
                
                if (size_mb > 0.01) {
                    printf("\nVideo received and saved!\n");
                    printf("File:      %s\n", output_file);
                    printf("Size:      %.2f MB\n", size_mb);
                    
                    if (elapsed > 0.1) {
                        printf("Time:      %.2f seconds\n", elapsed);
                        printf("Rate:      %.2f MB/s\n", size_mb / elapsed);
                        printf("Bandwidth: %.0f kbps\n", (size_mb * 8 * 1024) / elapsed);
                    }
                    
                    if (analyze) {
                        analyze_video_file(output_file);
                    }
                    
                    printf("\nTo play:\n");
                    printf("  ffplay %s\n", output_file);
                } else {
                    printf("\n File created but is small (%.2f MB)\n", size_mb);
                }
            }
        } else {
            printf("\n No file was saved\n");
        }
    } else {
        printf("\nStream received (not saved to disk)\n");
    }
    
    // Troubleshooting if nothing received
    if (ret != 0 && ret != 130 && ret != 2 && ret != 255) {
        printf("\n Troubleshooting:\n");
        printf("1. Check if sender is running\n");
        printf("2. Test SRT: gst-launch-1.0 srtsrc uri=\"srt://0.0.0.0:%d?mode=listener\" ! fakesink\n", port);
        printf("3. Check firewall settings\n");
        printf("4. Verify network connection\n");
        printf("5. Try increasing buffer: --buffer 500\n");
        printf("6. Try auto-decode mode: --auto-decode\n");
    }
    
    if (ret == 0 || ret == 2 || ret == 130 || ret == 255) {
        printf("\nReceiver completed successfully\n");
        return 0;
    } else {
        printf("\n Receiver exited with code %d\n", ret);
        return 1;
    }
}