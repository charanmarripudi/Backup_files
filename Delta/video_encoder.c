
// 9 seconds delta encode 
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <time.h>


double get_file_size_mb(const char *path) {
    struct stat st;
    if (stat(path, &st) == 0)
        return st.st_size / (1024.0 * 1024.0);
    return 0.0;
}

int get_bitrate_kbps(const char *file) {
    char cmd[512];
    snprintf(cmd, sizeof(cmd),
        "ffprobe -v error -select_streams v:0 "
        "-show_entries stream=bit_rate "
        "-of default=noprint_wrappers=1:nokey=1 \"%s\"",
        file
    );

    FILE *fp = popen(cmd, "r");
    if (!fp) return 0;

    char buf[128];
    fgets(buf, sizeof(buf), fp);
    pclose(fp);

    return atoi(buf) / 1000;
}

double time_diff_sec(struct timespec start, struct timespec end) {
    return (end.tv_sec - start.tv_sec)
         + (end.tv_nsec - start.tv_nsec) / 1e9;
}

double get_fps(const char *file) {
    char cmd[512];
    snprintf(cmd, sizeof(cmd),
        "ffprobe -v error -select_streams v:0 "
        "-show_entries stream=r_frame_rate "
        "-of default=noprint_wrappers=1:nokey=1 \"%s\"",
        file
    );

    FILE *fp = popen(cmd, "r");
    if (!fp) return 25.0;

    char buf[128];
    fgets(buf, sizeof(buf), fp);
    pclose(fp);

    int num = 0, den = 1;
    sscanf(buf, "%d/%d", &num, &den);
    if (den == 0) return 25.0;

    return (double)num / den;
}


void encode_delta(const char *in, const char *out, int fps, int crf) {
    char cmd[2048];
    struct timespec start, end;

    clock_gettime(CLOCK_MONOTONIC, &start);

    snprintf(cmd, sizeof(cmd),
        "gst-launch-1.0 -q -e "
        "filesrc location=\"%s\" ! decodebin ! queue ! "
        "videoconvert ! queue ! "
        "video/x-raw,framerate=%d/1 ! "
        "x264enc "
        "key-int-max=250 bframes=8 ref=6 "
        "me=umh subme=8 quantizer=%d "
        "speed-preset=slow pass=quant "
        "! h264parse ! mp4mux ! "
        "filesink location=\"%s\" sync=false",
        in, fps, crf, out
    );

    system(cmd);

    clock_gettime(CLOCK_MONOTONIC, &end);

    printf(">>> Delta encode wall-time: %.2f seconds\n",
           time_diff_sec(start, end));
}

void encode_fullframe(const char *in, const char *out, int fps, int bitrate) {
    char cmd[2048];
    struct timespec start, end;

    clock_gettime(CLOCK_MONOTONIC, &start);

    snprintf(cmd, sizeof(cmd),
        "gst-launch-1.0 -q -e "
        "filesrc location=\"%s\" ! decodebin ! queue ! "
        "videoconvert ! queue ! "
        "video/x-raw,framerate=%d/1 ! "
        "x264enc "
        "intra-refresh=true key-int-max=1 bframes=0 "
        "bitrate=%d speed-preset=slow tune=zerolatency "
        "! h264parse ! mp4mux ! "
        "filesink location=\"%s\" sync=false",
        in, fps, bitrate, out
    );

    system(cmd);

    clock_gettime(CLOCK_MONOTONIC, &end);

    printf(">>> Full-frame encode wall-time: %.2f seconds\n",
           time_diff_sec(start, end));
}


int main(int argc, char *argv[]) {

    if (argc < 2) {
        printf("Usage: %s <video1> <video2> ...\n", argv[0]);
        return 1;
    }

    system("mkdir -p output_videos");

    for (int i = 1; i < argc; i++) {

        const char *input = argv[i];
        char delta[512], full[512];
        char base[256];

        char *slash = strrchr(input, '/');
        strcpy(base, slash ? slash + 1 : input);
        char *dot = strrchr(base, '.');
        if (dot) *dot = '\0';

        snprintf(delta, sizeof(delta),
                 "output_videos/%s_C_delta.mp4", base);
        snprintf(full, sizeof(full),
                 "output_videos/%s_C_full.mp4", base);

        int bitrate = get_bitrate_kbps(input);
        double fps = get_fps(input);

        printf("\nProcessing: %s\n", input);
        printf("Original: Bitrate=%d kb/s, FPS=%.2f\n", bitrate, fps);

        encode_delta(input, delta, (int)(fps + 0.5), 32);
        encode_fullframe(input, full, (int)(fps + 0.5), bitrate);

        double size_orig = get_file_size_mb(input);
        double size_delta = get_file_size_mb(delta);
        double size_full = get_file_size_mb(full);

        printf("\nPreset          Size      Compression\n");
        printf("Delta  %.2f MB   %.0f%%\n",
               size_delta,
               (1 - size_delta / size_orig) * 100);

        printf("Full-frame      %.2f MB   %.0f%%\n",
               size_full,
               (1 - size_full / size_orig) * 100);
    }

    return 0;
}






// old code for reference


// #include <stdio.h>
// #include <stdlib.h>
// #include <string.h>
// #include <sys/stat.h>
// #include <time.h>



// double get_file_size_mb(const char *path) {
//     struct stat st;
//     if (stat(path, &st) == 0)
//         return st.st_size / (1024.0 * 1024.0);
//     return 0.0;
// }


// int get_bitrate_kbps(const char *file) {
//     char cmd[512];
//     snprintf(cmd, sizeof(cmd),
//         "ffprobe -v error -select_streams v:0 "
//         "-show_entries stream=bit_rate "
//         "-of default=noprint_wrappers=1:nokey=1 \"%s\"",
//         file
//     );

//     FILE *fp = popen(cmd, "r");
//     if (!fp) return 0;

//     char buf[128];
//     fgets(buf, sizeof(buf), fp);
//     pclose(fp);

//     return atoi(buf) / 1000;
// }

// double time_diff_sec(struct timespec start, struct timespec end) {
//     return (end.tv_sec - start.tv_sec)
//          + (end.tv_nsec - start.tv_nsec) / 1e9;
// }

// double get_fps(const char *file) {
//     char cmd[512];
//     snprintf(cmd, sizeof(cmd),
//         "ffprobe -v error -select_streams v:0 "
//         "-show_entries stream=r_frame_rate "
//         "-of default=noprint_wrappers=1:nokey=1 \"%s\"",
//         file
//     );

//     FILE *fp = popen(cmd, "r");
//     if (!fp) return 25.0;

//     char buf[128];
//     fgets(buf, sizeof(buf), fp);
//     pclose(fp);

//     int num = 0, den = 1;
//     sscanf(buf, "%d/%d", &num, &den);
//     if (den == 0) return 25.0;

//     return (double)num / den;
// }


// void encode_delta(const char *in, const char *out, int fps) {
//     char cmd[1024];
//     struct timespec start, end;

//     clock_gettime(CLOCK_MONOTONIC, &start);

//     snprintf(cmd, sizeof(cmd),
//         "gst-launch-1.0 -e "
//         "filesrc location=\"%s\" ! decodebin ! videoconvert ! videorate "
//         "! video/x-raw,framerate=%d/1 "
//         "! x264enc key-int-max=250 bframes=6 ref=8 "
//         "me=umh subme=8 quantizer=32 "
//         "speed-preset=slow pass=quant "
//         "! h264parse ! mp4mux ! filesink location=\"%s\"",
//         in, fps, out
//     );

//     system(cmd);

//     clock_gettime(CLOCK_MONOTONIC, &end);

//     printf(">>> Delta encode wall-time: %.2f seconds\n",
//        time_diff_sec(start, end));

// }


// void encode_fullframe(const char *in, const char *out, int fps, int bitrate) {
//     char cmd[1024];
//     struct timespec start, end;

//     clock_gettime(CLOCK_MONOTONIC, &start);

//     snprintf(cmd, sizeof(cmd),
//         "gst-launch-1.0 -e "
//         "filesrc location=\"%s\" ! decodebin ! videoconvert ! videorate "
//         "! video/x-raw,framerate=%d/1 "
//         "! x264enc intra-refresh=true key-int-max=1 bframes=0 "
//         "bitrate=%d speed-preset=slow "
//         "! h264parse ! mp4mux ! filesink location=\"%s\"",
//         in, fps, bitrate, out
//     );

//     system(cmd);

//     clock_gettime(CLOCK_MONOTONIC, &end);

//     printf(">>> Full-frame encode wall-time: %.2f seconds\n",
//        time_diff_sec(start, end));

// }

// int main(int argc, char *argv[]) {

//     if (argc < 2) {
//         printf("Usage: %s <video1> <video2> ...\n", argv[0]);
//         return 1;
//     }

//     system("mkdir -p output_videos");

//     for (int i = 1; i < argc; i++) {

//         const char *input = argv[i];
//         char delta[512], full[512];
//         char base[256];

//         // Extract filename without extension
//         char *slash = strrchr(input, '/');
//         strcpy(base, slash ? slash + 1 : input);
//         char *dot = strrchr(base, '.');
//         if (dot) *dot = '\0';

//         snprintf(delta, sizeof(delta),
//                  "output_videos/%s_C_delta.mp4", base);
//         snprintf(full, sizeof(full),
//                  "output_videos/%s_C_full.mp4", base);

//         int bitrate = get_bitrate_kbps(input);
//         double fps = get_fps(input);

//         printf("\nProcessing: %s\n", input);
//         printf("Original: Bitrate=%d kb/s, FPS=%.2f\n", bitrate, fps);

//         encode_delta(input, delta, (int)(fps + 0.5));
//         encode_fullframe(input, full, (int)(fps + 0.5), bitrate);

//         double size_orig = get_file_size_mb(input);
//         double size_delta = get_file_size_mb(delta);
//         double size_full = get_file_size_mb(full);

//         printf("\nPreset          Size      Compression\n");
//         printf("Delta  %.2f MB   %.0f%%\n",
//                size_delta,
//                (1 - size_delta / size_orig) * 100);

//         printf("Full-frame      %.2f MB   %.0f%%\n",
//                size_full,
//                (1 - size_full / size_orig) * 100);
//     }

//     return 0;
// }
