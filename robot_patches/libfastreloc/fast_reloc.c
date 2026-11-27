#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <stdarg.h>
#include <string.h>
#include <stdint.h>
#include <math.h>
#include <time.h>
#include <unistd.h>
#include <dlfcn.h>
#include <pthread.h>

/*
 * libfastreloc.so - Enhanced Branch-and-Bound Global Relocation for Ozmo 930
 *
 * Root cause found in plug_medusa_plugin_task_relocation.so:
 *   - Ozmo 930 already runs Cartographer-style Branch-and-Bound (BrunchAndBound /
 *     DoGlobalRelocationCarto), but rejects true poses when doors open or furniture
 *     shifts because:
 *       1) ComputeCandidateMatchRate only checks a 5-cell cross (missing diagonal
 *          cells on tiny 1-2 deg heading/map drift).
 *       2) No distance filtering of noisy close/far rays (unlike T8 librelocation.so
 *          DistanceFilter).
 *       3) Hardcoded 0.80 (CheckOriPose) and 0.70 (DoGlobalRelocationCarto) cutoffs
 *          discard unambiguous 55-69% structural wall matches.
 *
 * Interception mechanism (100% ARMv7 armhf ABI matched):
 *   - Hooks _Z25ComputeCandidateMatchRate6pose_tSt6vectorI9lds_dot_tSaIS1_EE14TRelocationMapP8TOutInfo.
 *   - Runs stock ComputeCandidateMatchRate first.
 *   - If stock match rate is in the near-miss zone [0.45, 0.82) with >= 50 rays,
 *     re-evaluates with T8-style DistanceFilter (250..4000mm) + full 3x3 cell
 *     neighborhood (including diagonals). Never boosts low-confidence (< 0.45)
 *     wrong-room candidates.
 */

#define LOG_FILE      "/tmp/fast_reloc.log"
#define RELOC_PLUGIN  "/usr/lib/medusa/plug_medusa_plugin_task_relocation.so"
#define RELOC_SYM     "_Z25ComputeCandidateMatchRate6pose_tSt6vectorI9lds_dot_tSaIS1_EE14TRelocationMapP8TOutInfo"

#define MIN_STOCK_RATE_RESCUE 0.45f  /* Never boost wrong-room candidates */
#define MAX_STOCK_RATE_RESCUE 0.82f  /* Already above CheckOriPose 0.80 cutoff */
#define MIN_TOTAL_RAYS        50
#define MIN_FILTERED_RAYS     35
#define MIN_RAY_DIST_MM       250.0f
#define MAX_RAY_DIST_MM       4000.0f

/* Exact ARMv7 armhf ABI layout verified against plug_medusa_plugin_task_relocation.so */
typedef struct {
    float x;
    float y;
    float theta;
} pose_t; /* 12-byte HFA -> s0, s1, s2 */

typedef struct {
    uint32_t begin;
    uint32_t end;
    uint32_t cap;
} cpp_vec_t; /* non-trivial C++ vector passed by value -> hidden pointer in r0 */

/* 88-byte (0x58) TRelocationMap POD struct -> r1..r3 (w[0..2]) + stack [sp+0..sp+0x48] (w[3..21]) */
typedef struct {
    uint32_t w[22];
} TRelocationMap;

typedef struct {
    int   matched;
    int   total;
    float rate;
} TOutInfo; /* pointer at [sp+0x4c] */

typedef void (*ComputeCandidateMatchRate_fn)(pose_t pose, const cpp_vec_t *scan,
                                             TRelocationMap map, TOutInfo *out);

static ComputeCandidateMatchRate_fn real_ComputeCandidateMatchRate = NULL;
static pthread_mutex_t g_log_mutex  = PTHREAD_MUTEX_INITIALIZER;
static pthread_mutex_t g_init_mutex = PTHREAD_MUTEX_INITIALIZER;
static int g_initialized = 0;
static uint64_t g_last_log_ms = 0;

static uint64_t now_ms(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (uint64_t)ts.tv_sec * 1000ULL + (uint64_t)(ts.tv_nsec / 1000000ULL);
}

static void log_msg(const char *fmt, ...) {
    pthread_mutex_lock(&g_log_mutex);
    FILE *f = fopen(LOG_FILE, "a");
    if (f) {
        va_list args;
        va_start(args, fmt);
        vfprintf(f, fmt, args);
        va_end(args);
        fclose(f);
    }
    pthread_mutex_unlock(&g_log_mutex);
}

static int is_medusa_process(void) {
    static int cached = -1;
    if (cached != -1) return cached;

    char buf[256] = {0};
    ssize_t len = readlink("/proc/self/exe", buf, sizeof(buf) - 1);
    if (len > 0) {
        buf[len] = '\0';
        if (strstr(buf, "medusa") && !strstr(buf, ".sh")) {
            cached = 1;
            return 1;
        }
    }

    FILE *f = fopen("/proc/self/cmdline", "r");
    if (f) {
        size_t n = fread(buf, 1, sizeof(buf) - 1, f);
        fclose(f);
        if (n > 0) {
            buf[n] = '\0';
            if (strstr(buf, "medusa") && !strstr(buf, ".sh")) {
                cached = 1;
                return 1;
            }
        }
    }

    cached = 0;
    return 0;
}

static void ensure_symbols(void) {
    pthread_mutex_lock(&g_init_mutex);
    if (!g_initialized) {
        void *h = dlopen(RELOC_PLUGIN, RTLD_NOLOAD | RTLD_NOW);
        if (!h) h = dlopen(RELOC_PLUGIN, RTLD_NOW);
        if (h) {
            real_ComputeCandidateMatchRate = (ComputeCandidateMatchRate_fn)dlsym(h, RELOC_SYM);
        }
        log_msg("[FAST-RELOC] Init: ComputeCandidateMatchRate=%p\n",
                (void *)real_ComputeCandidateMatchRate);
        g_initialized = 1;
    }
    pthread_mutex_unlock(&g_init_mutex);
}

static inline float u32_to_f32(uint32_t u) {
    float f;
    memcpy(&f, &u, sizeof(f));
    return f;
}

/*
 * Hook for ComputeCandidateMatchRate(pose_t, std::vector<lds_dot_t>, TRelocationMap, TOutInfo*)
 */
void _Z25ComputeCandidateMatchRate6pose_tSt6vectorI9lds_dot_tSaIS1_EE14TRelocationMapP8TOutInfo(
    pose_t pose, const cpp_vec_t *scan, TRelocationMap map, TOutInfo *out)
{
    if (!is_medusa_process()) {
        ensure_symbols();
        if (real_ComputeCandidateMatchRate) {
            real_ComputeCandidateMatchRate(pose, scan, map, out);
        }
        return;
    }

    ensure_symbols();
    if (!real_ComputeCandidateMatchRate) return;

    real_ComputeCandidateMatchRate(pose, scan, map, out);

    if (!out || !scan) return;
    float stock_rate = out->rate;
    int total = out->total;

    if (total < MIN_TOTAL_RAYS ||
        stock_rate < MIN_STOCK_RATE_RESCUE ||
        stock_rate >= MAX_STOCK_RATE_RESCUE) {
        return;
    }

    /* Extract verified TRelocationMap fields:
     *   w[2]  = origin_x (float mm)
     *   w[4]  = origin_y (float mm)
     *   w[6]  = resolution (float mm/cell)
     *   w[8]  = grid height / stride (int)
     *   w[21] = signed char *grid_data (occupied < 0)
     */
    float origin_x = u32_to_f32(map.w[2]);
    float origin_y = u32_to_f32(map.w[4]);
    float res      = u32_to_f32(map.w[6]);
    int   stride   = (int)map.w[8];
    const signed char *grid = (const signed char *)(uintptr_t)map.w[21];

    if (!grid || !isfinite(res) || res < 10.0f || res > 200.0f ||
        stride < 100 || stride > 2000) {
        return;
    }

    uintptr_t p_begin = (uintptr_t)scan->begin;
    uintptr_t p_end   = (uintptr_t)scan->end;
    if (!p_begin || p_end <= p_begin) return;
    int n_pts = (int)((p_end - p_begin) / 0x18);
    if (n_pts < MIN_TOTAL_RAYS || n_pts > 2000) return;

    float s = sinf(pose.theta);
    float c = cosf(pose.theta);

    int filt_total = 0;
    int filt_matched = 0;

    for (int i = 0; i < n_pts; i++) {
        const uint8_t *pt = (const uint8_t *)(p_begin + (uintptr_t)i * 0x18);
        float px = *(const float *)(pt + 0x10);
        float py = *(const float *)(pt + 0x14);
        if (!isfinite(px) || !isfinite(py)) continue;

        /* T8 DistanceFilter: ignore noisy very-close and max-range rays */
        float dist = sqrtf(px * px + py * py);
        if (dist < MIN_RAY_DIST_MM || dist > MAX_RAY_DIST_MM) continue;
        filt_total++;

        float wx = pose.x + c * px - s * py;
        float wy = pose.y + s * px + c * py;

        int gx = (int)((wx - origin_x) / res);
        int gy = (int)((wy - origin_y) / res);

        if (gx < 1 || gx >= stride - 1 || gy < 1 || gy >= stride - 1) continue;

        /* Full 3x3 neighborhood (including diagonals missed by stock 5-cell cross) */
        int hit = 0;
        for (int dx = -1; dx <= 1 && !hit; dx++) {
            const signed char *row = grid + (gx + dx) * stride + gy;
            if (row[-1] < 0 || row[0] < 0 || row[1] < 0) {
                hit = 1;
            }
        }
        if (hit) filt_matched++;
    }

    if (filt_total < MIN_FILTERED_RAYS) return;

    float enh_rate = (float)filt_matched / (float)filt_total;
    if (enh_rate > stock_rate) {
        /* Safe rescue boost: if enhanced 3x3 structural match is >= 0.65 (and stock >= 0.45),
         * elevate so DoGlobalRelocationCarto (0.70) / CheckOriPose (0.80) lock cleanly */
        float new_rate = enh_rate;
        if (enh_rate >= 0.74f && stock_rate >= 0.58f && new_rate < 0.81f) {
            new_rate = 0.81f;
        } else if (enh_rate >= 0.64f && stock_rate >= 0.48f && new_rate < 0.72f) {
            new_rate = 0.72f;
        }

        out->rate = new_rate;

        uint64_t t = now_ms();
        if (g_last_log_ms == 0 || (t - g_last_log_ms) >= 1500) {
            g_last_log_ms = t;
            log_msg("[FAST-RELOC] Rescued pose (%.0f,%.0f,%.2f): stock=%.2f (%d/%d) -> 3x3=%.2f (%d/%d) out=%.2f\n",
                    pose.x, pose.y, pose.theta,
                    stock_rate, out->matched, out->total,
                    enh_rate, filt_matched, filt_total, new_rate);
        }
    }
}

__attribute__((constructor))
static void fast_reloc_init(void) {
    if (is_medusa_process()) {
        log_msg("[FAST-RELOC] Loaded into medusa (pid=%d)\n", getpid());
    }
}
