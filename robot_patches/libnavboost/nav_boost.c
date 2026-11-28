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
 * libnavboost.so - Ozmo 930 Navigation, VFH+ Lidar Obstacle Steering & Stuck Recovery
 *
 * Ports core algorithms from Ozmo 950 (libvfh.so VfhPlus & librobot_stuck_handler.so)
 * onto Ozmo 930 (libUnderErg.so & plug_medusa_plugin_task_return.so):
 *
 *   1. VFH+ 72-Sector Polar Histogram Lidar Obstacle Steering (ported from libvfh.so):
 *      - Reads live 360-deg Lidar points from pAdapterDeebot_9 (+0x2be8 count,
 *        +0xa28 array, stride 0x18: +0x04 angle_rad, +0x08 dist_mm, +0x10 px_mm, +0x14 py_mm).
 *      - When driving forward and >=3 confirming Lidar points enter the forward
 *        danger corridor (px in [160, 480] mm, |py| < 210 mm), builds a 72-sector
 *        (5 deg/sector) robot-radius-inflated polar histogram (R_eff = 222 mm),
 *        finds the best open valley within +-70 deg, slows proportionally to proximity,
 *        and smoothly curves around dynamic obstacles before the bumper hits.
 *      - Automatically pauses during docking, reversing, stops, or active detrap.
 *
 *   2. Zero-Displacement Pose-Stall & Wheel-Current Stuck Recovery (ported from
 *      librobot_stuck_handler.so):
 *      - Monitors pAdapterDeebot_9 pose (+0x2c08 x, +0x2c0c y) plus
 *        _alert_isWheelLargeCurrent() and _alert_isSmallWheelStoped().
 *      - Detects high-centering (commanded forward > 2.5s with < 30mm displacement
 *        even without bumper press, or sustained wheel-current/caster stall > 1.2s).
 *      - Records the trap in libdirectpath's spatial stuck heat-map and injects a
 *        brief 320ms alternating reverse-arc unhook pulse.
 *
 *   3. Smooth Wheel Acceleration Slew-Rate Limiter (adjustSpeed hook):
 *      - Immediate pass-through for stops (0,0) and reverse commands.
 *      - Caps forward/turn speed jumps to 4.5 units / 20ms tick to reduce wheel
 *        slip on hardwood/rugs and improve SLAM odometry accuracy.
 *
 *   4. Bump-Cluster Heat-Map Bridge & Bump-Stuck Anti-Deadlock Pulse:
 *      - Feeds repeated bump clusters (>=3 bumps in 300mm / 15s) and dtp_DeTrapInit
 *        entries into libdirectpath's stuck heat-map (nav_record_stuck_cell).
 *      - Injects a 240ms gentle reverse nudge if a jammed bumper spins > 1.6s.
 */

#define LOG_FILE        "/tmp/nav_boost.log"
#define SILENT_GOTO_FILE "/tmp/silent_goto"
#define UNDERERG_LIB    "/usr/lib/libUnderErg.so"
#define ERGODIC_PLUGIN  "/usr/lib/medusa/plug_medusa_plugin_task_ergodic.so"
#define RETURN_PLUGIN   "/usr/lib/medusa/plug_medusa_plugin_task_return.so"

/* Open-floor non-cleaning transit speed boost (Pin & Go + Return-to-Dock A*) */
#define TRANSIT_MIN_CRUISE_SPD  16.0f
#define TRANSIT_MAX_DIFF_SPD    3.5f
#define TRANSIT_RUNWAY_MM       1100.0f  /* Require >= 1.1m clear Lidar runway ahead */
#define TRANSIT_CORRIDOR_Y_MM   250.0f
#define TRANSIT_BOOST_FACTOR    1.22f    /* +22% straightaway cruise boost */
#define TRANSIT_MAX_SPD_CAP     32.5f

/* pAdapterDeebot_9 verified offsets on Ozmo 930 (libUnderErg.so) */
#define OFF_BUMP_FLAG   0x000c
#define OFF_LDS_BASE    0x0a28
#define OFF_LDS_STRIDE  0x0018
#define OFF_LDS_ANGLE   0x0004  /* float radians (0 = straight ahead +X) */
#define OFF_LDS_DIST    0x0008  /* float mm */
#define OFF_LDS_PX      0x0010  /* float mm (+X forward) */
#define OFF_LDS_PY      0x0014  /* float mm (+Y left, -Y right) */
#define OFF_LDS_COUNT   0x2be8  /* int32 point count */
#define OFF_POSE_X      0x2c08  /* float pose X */
#define OFF_POSE_Y      0x2c0c  /* float pose Y */

/* VFH+ 72-sector polar histogram parameters (matching Ozmo 950 libDeebot/libvfh point-to-point transit) */
#define VFH_SECTORS         72
#define VFH_SECTOR_DEG      5.0f
#define VFH_ROBOT_EFF_MM    185.0f  /* 177mm 930 radius + 8mm margin so 70cm doorways remain open at 0 deg */
#define VFH_SCAN_MAX_MM     750.0f  /* Consider obstacles within 750mm for histogram */
#define VFH_TRIGGER_MAX_MM  330.0f  /* Trigger avoidance only when unexpected obstacle < 330mm ahead */
#define VFH_TRIGGER_MIN_MM  155.0f  /* Ignore self-reflections inside chassis */
#define VFH_CORRIDOR_Y_MM   172.0f  /* Strictly inside 350mm chassis width so door jambs do not trigger */
#define VFH_MIN_CONFIRM_PTS 3       /* Require >=3 Lidar hits to avoid single-ray noise */
#define VFH_MAX_STEER_SEC   12      /* +-60 deg (12 * 5 deg) valley search window */
#define VFH_MIN_VALLEY_W    2       /* Minimum contiguous free sectors (10 deg) */
#define VFH_MAX_PTS         720

/* Slew-rate limiter */
#define MAX_ACCEL_STEP      4.5f
#define SPEED_STALE_MS      250

/* Pose-stall & wheel-current stuck detection + T8-inspired Lidar gap escape */
#define STALL_MIN_CMD_SPD   6.0f
#define STALL_POSE_MS       2500
#define STALL_MIN_MOVE_M    0.030f  /* 30mm in meters */
#define STALL_MIN_MOVE_MM   30.0f   /* 30mm in millimeters (auto-detected scale) */
#define STALL_CURR_MS       1200
#define STALL_BACK_MS       240     /* Phase 1: reverse off obstacle */
#define STALL_AIM_MAX_MS    460     /* Phase 2: pivot toward widest Lidar exit gap */
#define STALL_COOLDOWN_MS   6000
#define GAP_OPEN_DIST_MM    520.0f  /* Minimum ray clearance for an open exit sector */
#define GAP_MIN_SECTORS     4       /* Minimum 20-deg open gap width */

/* Bump cluster & bump-stuck recovery */
#define BUMP_CLUSTER_WIN_MS  15000
#define BUMP_CLUSTER_DIST    3
#define BUMP_CLUSTER_TRIG    3
#define STUCK_PULSE_AFTER_MS 1600
#define STUCK_PULSE_DUR_MS   240
#define STUCK_PULSE_COOLDOWN 4000
#define STUCK_PULSE_SPEED    -12.0f

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

typedef void    (*adjustSpeed_fn)(float left, float right);
typedef void    (*dwb_bumpUpdate_fn)(float x, float y, float theta);
typedef int     (*bumpStuckCheck_fn)(void (*on_clear)(void), void (*on_fail)(void),
                                     unsigned int spin_thresh, unsigned int fail_thresh);
typedef void    (*dtp_DeTrapInit_fn)(int ok_cb, int fail_cb);
typedef int     (*coord2idx_fn)(float v);
typedef int     (*alert_bool_fn)(void);
typedef void    (*nav_record_stuck_cell_fn)(int gx, int gy, uint8_t weight);
typedef int     (*startReturn_fn)(void);
typedef uint8_t (*searchdock_fn)(void);
typedef int     (*range_flag_fn)(void);
typedef void    (*void_fn)(void);
typedef void   *(*get_map_fn)(void);
typedef void    (*copy_loc_to_800_fn)(void *map800);
typedef void    (*refresh_800_to_400_fn)(void *map800, void *map400, void *pred_fn);
typedef void    (*mark_vwall_fn)(int a, int b, int v1, int v2, void *map800);
typedef uint32_t (*mc_follow_pt_fn)(void);

static adjustSpeed_fn           real_adjustSpeed           = NULL;
static dwb_bumpUpdate_fn        real_dwb_bumpUpdate        = NULL;
static bumpStuckCheck_fn        real_bumpStuckCheck        = NULL;
static dtp_DeTrapInit_fn        real_dtp_DeTrapInit        = NULL;
static coord2idx_fn             real_x2idx02               = NULL;
static coord2idx_fn             real_y2idx02               = NULL;
static alert_bool_fn            real_isWheelLargeCurrent   = NULL;
static alert_bool_fn            real_isSmallWheelStoped    = NULL;
static void                   **pp_adapter_deebot9         = NULL;
static void                   **pp_robot_lds_pic           = NULL;
static nav_record_stuck_cell_fn fn_nav_record_stuck_cell   = NULL;
static startReturn_fn           real_startReturnToCharger  = NULL;
static searchdock_fn            real_searchdock            = NULL;
static range_flag_fn            real_isUnnecessaryRangeDef = NULL;
static void_fn                  real_removeUnnecessaryByLoc= NULL;
static get_map_fn               real_map_getGridMap        = NULL;
static get_map_fn               real_map_getGridMapReduced = NULL;
static copy_loc_to_800_fn       real_copyLocToInt800       = NULL;
static refresh_800_to_400_fn    real_refresh800To400       = NULL;
static void                    *fn_is8neighbours5Free800   = NULL;
static alert_bool_fn            real_isChargerFrontPoseSet = NULL;
static mark_vwall_fn            real_markVirtualWallToMap  = NULL;
static mc_follow_pt_fn          real_mc_motion_followPoint = NULL;

static __thread int g_follow_point_depth = 0;

static pthread_mutex_t g_log_mutex  = PTHREAD_MUTEX_INITIALIZER;
static pthread_mutex_t g_init_mutex = PTHREAD_MUTEX_INITIALIZER;
static pthread_mutex_t g_nav_mutex  = PTHREAD_MUTEX_INITIALIZER;
static int g_initialized = 0;

/* Slew-rate state */
static float    g_last_l = 0.0f;
static float    g_last_r = 0.0f;
static uint64_t g_last_speed_ms = 0;

/* Pose-stall & current-stall + Lidar gap-escape state */
static float    g_stall_anchor_x  = 0.0f;
static float    g_stall_anchor_y  = 0.0f;
static uint64_t g_stall_anchor_ms = 0;
static uint64_t g_curr_stall_ms   = 0;
static uint64_t g_unhook_start_ms = 0;
static uint64_t g_unhook_last_ms  = 0;
static int      g_unhook_dir      = 1;
static int      g_unhook_aim_ms   = 0;
static int      g_unhook_turn_dir = 1; /* +1 = left (+deg), -1 = right (-deg) */

/* Bump cluster & last pose state */
static int      g_last_gx = -1;
static int      g_last_gy = -1;
static int      g_cluster_gx = -1;
static int      g_cluster_gy = -1;
static int      g_cluster_hits = 0;
static uint64_t g_cluster_start_ms = 0;
static uint64_t g_last_bump_ev_ms  = 0;

/* Bump-stuck anti-deadlock state */
static uint64_t g_stuck_start_ms = 0;
static uint64_t g_pulse_start_ms = 0;
static uint64_t g_last_pulse_ms  = 0;

/* Docking, transit boost & VFH state / rate-limited logs */
static uint64_t g_dock_active_until_ms    = 0;
static uint64_t g_return_transit_until_ms = 0;
static uint64_t g_silent_check_ms         = 0;
static int      g_silent_goto_cached      = 0;
static uint64_t g_last_dock_log_ms        = 0;
static uint64_t g_last_vfh_log_ms         = 0;
static uint64_t g_last_boost_log_ms       = 0;

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
        void *h_erg = dlopen(UNDERERG_LIB, RTLD_NOLOAD | RTLD_NOW);
        if (!h_erg) h_erg = dlopen(UNDERERG_LIB, RTLD_NOW);

        if (h_erg) {
            real_adjustSpeed         = (adjustSpeed_fn)dlsym(h_erg, "adjustSpeed");
            real_dwb_bumpUpdate      = (dwb_bumpUpdate_fn)dlsym(h_erg, "dwb_bumpUpdateInGridMapAndReduced");
            real_bumpStuckCheck      = (bumpStuckCheck_fn)dlsym(h_erg, "bumpStuckCheckAndHandle");
            real_dtp_DeTrapInit      = (dtp_DeTrapInit_fn)dlsym(h_erg, "dtp_DeTrapInit");
            real_x2idx02             = (coord2idx_fn)dlsym(h_erg, "x2idx02");
            real_y2idx02             = (coord2idx_fn)dlsym(h_erg, "y2idx02");
            real_isWheelLargeCurrent = (alert_bool_fn)dlsym(h_erg, "_alert_isWheelLargeCurrent");
            real_isSmallWheelStoped  = (alert_bool_fn)dlsym(h_erg, "_alert_isSmallWheelStoped");
            pp_adapter_deebot9       = (void **)dlsym(h_erg, "pAdapterDeebot_9");
            pp_robot_lds_pic         = (void **)dlsym(h_erg, "pRobotLdsPic");
            real_isUnnecessaryRangeDef  = (range_flag_fn)dlsym(h_erg, "map_isUnnecessaryRangeDefinedFlag");
            real_removeUnnecessaryByLoc = (void_fn)dlsym(h_erg, "map_removeUnnecessaryRangeByLocationMap");
            real_map_getGridMap         = (get_map_fn)dlsym(h_erg, "map_getGridMap");
            real_map_getGridMapReduced  = (get_map_fn)dlsym(h_erg, "map_getGridMapReduced");
            real_copyLocToInt800        = (copy_loc_to_800_fn)dlsym(h_erg, "map_copyGridMapFromLocationMapToInt800");
            real_refresh800To400        = (refresh_800_to_400_fn)dlsym(h_erg, "map_refreshGridMapFromInt800ToInt400");
            fn_is8neighbours5Free800    = dlsym(h_erg, "map_is8neighbours5FreeInMapInt800");
            real_mc_motion_followPoint  = (mc_follow_pt_fn)dlsym(h_erg, "mc_motion_followPoint");
        }

        void *h_task_erg = dlopen(ERGODIC_PLUGIN, RTLD_NOLOAD | RTLD_NOW);
        if (h_task_erg) {
            real_isChargerFrontPoseSet = (alert_bool_fn)dlsym(h_task_erg, "erg_isChargerFrontPoseSet");
            real_markVirtualWallToMap  = (mark_vwall_fn)dlsym(h_task_erg, "erg_markVirtualWallToMap");
        }

        void *h_ret = dlopen(RETURN_PLUGIN, RTLD_NOLOAD | RTLD_NOW);
        if (h_ret) {
            real_startReturnToCharger = (startReturn_fn)dlsym(h_ret, "startReturnToCharger");
            real_searchdock           = (searchdock_fn)dlsym(h_ret, "searchdock");
        }

        fn_nav_record_stuck_cell = (nav_record_stuck_cell_fn)dlsym(RTLD_DEFAULT, "nav_record_stuck_cell");

        log_msg("[NAV-BOOST] Init: adjustSpeed=%p bumpUpd=%p bumpStuck=%p deTrap=%p "
                "adapter=%p ldsPic=%p wheelCurr=%p caster=%p heatBridge=%p\n",
                (void *)real_adjustSpeed,
                (void *)real_dwb_bumpUpdate,
                (void *)real_bumpStuckCheck,
                (void *)real_dtp_DeTrapInit,
                (void *)pp_adapter_deebot9,
                (void *)pp_robot_lds_pic,
                (void *)real_isWheelLargeCurrent,
                (void *)real_isSmallWheelStoped,
                (void *)fn_nav_record_stuck_cell);

        g_initialized = 1;
    } else if (!fn_nav_record_stuck_cell) {
        fn_nav_record_stuck_cell = (nav_record_stuck_cell_fn)dlsym(RTLD_DEFAULT, "nav_record_stuck_cell");
    }
    pthread_mutex_unlock(&g_init_mutex);
}

static float clamp_step(float cur, float target, float max_step) {
    float diff = target - cur;
    if (diff > max_step)  return cur + max_step;
    if (diff < -max_step) return cur - max_step;
    return target;
}

static void report_heat(int gx, int gy, uint8_t weight) {
    if (gx < 0 || gx >= 400 || gy < 0 || gy >= 400) return;
    if (!fn_nav_record_stuck_cell) {
        fn_nav_record_stuck_cell = (nav_record_stuck_cell_fn)dlsym(RTLD_DEFAULT, "nav_record_stuck_cell");
    }
    if (fn_nav_record_stuck_cell) {
        fn_nav_record_stuck_cell(gx, gy, weight);
    }
}

/* pAdapterDeebot_9 (+0x2c08/+0x2c0c) and dwb_bumpUpdate pass mm; x2idx02/y2idx02 expect meters */
static inline void mm_to_grid400(float x_mm, float y_mm, int *out_gx, int *out_gy) {
    float xm = x_mm * 0.001f;
    float ym = y_mm * 0.001f;
    if (out_gx) *out_gx = real_x2idx02 ? real_x2idx02(xm) : (int)(xm * 10.0f + 200.0f);
    if (out_gy) *out_gy = real_y2idx02 ? real_y2idx02(ym) : (int)(ym * 10.0f + 200.0f);
}

static inline int get_live_pose_and_grid(float *out_x, float *out_y, int *out_gx, int *out_gy) {
    if (!pp_adapter_deebot9 || !*pp_adapter_deebot9) return 0;
    const uint8_t *base = (const uint8_t *)(*pp_adapter_deebot9);
    float x = *(const float *)(base + OFF_POSE_X);
    float y = *(const float *)(base + OFF_POSE_Y);
    if (!isfinite(x) || !isfinite(y)) return 0;
    if (out_x) *out_x = x;
    if (out_y) *out_y = y;
    mm_to_grid400(x, y, out_gx, out_gy);
    return 1;
}

/* Verify >= min_runway_mm clear Lidar corridor ahead before applying transit boost */
static int is_lidar_runway_clear(float min_runway_mm, float half_width_mm) {
    if (!pp_robot_lds_pic || !*pp_robot_lds_pic) return 0;
    const uint8_t *lds = (const uint8_t *)(*pp_robot_lds_pic);
    if (pp_adapter_deebot9 && *pp_adapter_deebot9) {
        const uint8_t *adp = (const uint8_t *)(*pp_adapter_deebot9);
        if (adp[OFF_BUMP_FLAG] != 0) return 0;
    }
    int n_pts = *(const int32_t *)(lds + OFF_LDS_COUNT);
    if (n_pts < 20 || n_pts > VFH_MAX_PTS) return 0;

    for (int i = 0; i < n_pts; i++) {
        const uint8_t *pt = lds + OFF_LDS_BASE + i * OFF_LDS_STRIDE;
        float dist = *(const float *)(pt + OFF_LDS_DIST);
        if (!isfinite(dist) || dist < VFH_TRIGGER_MIN_MM || dist > min_runway_mm) continue;
        float px = *(const float *)(pt + OFF_LDS_PX);
        float py = *(const float *)(pt + OFF_LDS_PY);
        if (!isfinite(px) || !isfinite(py)) continue;
        if (px >= VFH_TRIGGER_MIN_MM && px <= min_runway_mm && fabsf(py) <= half_width_mm) {
            return 0;
        }
    }
    return 1;
}

/*
 * T8-inspired Widest Lidar Exit Gap Finder (ported from librobot_stuck_handler.so
 * IsFindGap / IsFindCorner):
 * Scans all 72 5-deg sectors in pRobotLdsPic to find the widest contiguous open
 * escape corridor (nearest ray > GAP_OPEN_DIST_MM) so the robot aims straight out
 * of tight corners or chair-leg clusters instead of turning blindly.
 * Returns 1 if a valid gap (>= GAP_MIN_SECTORS) was found, writing signed center
 * angle (-180..+180 deg) and gap width (deg).
 */
static int find_widest_lidar_gap_deg(int *out_center_deg, int *out_width_deg) {
    if (!pp_robot_lds_pic || !*pp_robot_lds_pic) return 0;
    const uint8_t *lds = (const uint8_t *)(*pp_robot_lds_pic);
    int n_pts = *(const int32_t *)(lds + OFF_LDS_COUNT);
    if (n_pts < 20 || n_pts > VFH_MAX_PTS) return 0;

    float min_dist[VFH_SECTORS];
    for (int i = 0; i < VFH_SECTORS; i++) min_dist[i] = 4500.0f;

    for (int i = 0; i < n_pts; i++) {
        const uint8_t *pt = lds + OFF_LDS_BASE + i * OFF_LDS_STRIDE;
        float dist = *(const float *)(pt + OFF_LDS_DIST);
        if (!isfinite(dist) || dist < VFH_TRIGGER_MIN_MM || dist > 4500.0f) continue;

        float ang_rad = *(const float *)(pt + OFF_LDS_ANGLE);
        if (!isfinite(ang_rad)) continue;

        float ang_deg = ang_rad * (180.0f / (float)M_PI);
        while (ang_deg < 0.0f)    ang_deg += 360.0f;
        while (ang_deg >= 360.0f) ang_deg -= 360.0f;

        int sec = (int)(ang_deg / VFH_SECTOR_DEG + 0.5f) % VFH_SECTORS;
        if (dist < min_dist[sec]) min_dist[sec] = dist;
    }

    /* Find widest contiguous open run with circular wrap-around (2 * VFH_SECTORS) */
    int best_start = -1, best_len = 0;
    int cur_start = -1, cur_len = 0;

    for (int i = 0; i < VFH_SECTORS * 2; i++) {
        int is_open = (min_dist[i % VFH_SECTORS] >= GAP_OPEN_DIST_MM);
        if (is_open) {
            if (cur_start < 0) {
                cur_start = i;
                cur_len = 1;
            } else if (cur_len < VFH_SECTORS) {
                cur_len++;
            }
            if (cur_len > best_len) {
                best_len = cur_len;
                best_start = cur_start;
            }
        } else {
            cur_start = -1;
            cur_len = 0;
        }
    }

    if (best_len < GAP_MIN_SECTORS || best_start < 0) return 0;

    int center_sec = (best_start + best_len / 2) % VFH_SECTORS;
    int center_deg = center_sec * 5;
    if (center_deg > 180) center_deg -= 360;

    if (out_center_deg) *out_center_deg = center_deg;
    if (out_width_deg)  *out_width_deg  = best_len * 5;
    return 1;
}

/*
 * VFH+ 72-Sector Polar Histogram Dynamic Obstacle Avoidance (ported from Ozmo 950 libvfh.so)
 *
 * Returns 1 if an imminent forward obstacle was detected and wheel speeds were modulated,
 * 0 if the forward corridor is clear.
 */
static int apply_vfh_plus(float *io_left, float *io_right, uint64_t now) {
    if (!pp_robot_lds_pic || !*pp_robot_lds_pic) return 0;
    const uint8_t *lds = (const uint8_t *)(*pp_robot_lds_pic);

    /* Do not interfere if physical bumper is already pressed */
    if (pp_adapter_deebot9 && *pp_adapter_deebot9) {
        const uint8_t *adp = (const uint8_t *)(*pp_adapter_deebot9);
        if (adp[OFF_BUMP_FLAG] != 0) return 0;
    }

    int n_pts = *(const int32_t *)(lds + OFF_LDS_COUNT);
    if (n_pts < 20 || n_pts > VFH_MAX_PTS) return 0;

    /* Pass 1: Check forward danger corridor (px in [155..460] mm, |py| < 205 mm) */
    int danger_hits = 0;
    float closest_front_mm = VFH_TRIGGER_MAX_MM;

    for (int i = 0; i < n_pts; i++) {
        const uint8_t *pt = lds + OFF_LDS_BASE + i * OFF_LDS_STRIDE;
        float dist = *(const float *)(pt + OFF_LDS_DIST);
        if (!isfinite(dist) || dist < VFH_TRIGGER_MIN_MM || dist > VFH_SCAN_MAX_MM) continue;

        float px = *(const float *)(pt + OFF_LDS_PX);
        float py = *(const float *)(pt + OFF_LDS_PY);
        if (!isfinite(px) || !isfinite(py)) continue;

        if (px >= VFH_TRIGGER_MIN_MM && px <= VFH_TRIGGER_MAX_MM && fabsf(py) <= VFH_CORRIDOR_Y_MM) {
            danger_hits++;
            if (dist < closest_front_mm) closest_front_mm = dist;
        }
    }

    if (danger_hits < VFH_MIN_CONFIRM_PTS) return 0;

    /* Pass 2: Build 72-sector robot-radius-inflated polar obstacle histogram */
    uint8_t blocked[VFH_SECTORS];
    memset(blocked, 0, sizeof(blocked));

    for (int i = 0; i < n_pts; i++) {
        const uint8_t *pt = lds + OFF_LDS_BASE + i * OFF_LDS_STRIDE;
        float dist = *(const float *)(pt + OFF_LDS_DIST);
        if (!isfinite(dist) || dist < VFH_TRIGGER_MIN_MM || dist > VFH_SCAN_MAX_MM) continue;

        float ang_rad = *(const float *)(pt + OFF_LDS_ANGLE);
        if (!isfinite(ang_rad)) continue;

        float ang_deg = ang_rad * (180.0f / (float)M_PI);
        while (ang_deg < 0.0f)    ang_deg += 360.0f;
        while (ang_deg >= 360.0f) ang_deg -= 360.0f;

        int center_sec = (int)(ang_deg / VFH_SECTOR_DEG + 0.5f) % VFH_SECTORS;

        /* Enlargement angle = asin(R_eff / dist) matching VFH+ */
        float ratio = VFH_ROBOT_EFF_MM / dist;
        if (ratio > 0.99f) ratio = 0.99f;
        int enlarge_sec = (int)(ceilf((asinf(ratio) * (180.0f / (float)M_PI)) / VFH_SECTOR_DEG));
        if (enlarge_sec > 18) enlarge_sec = 18;

        for (int s = -enlarge_sec; s <= enlarge_sec; s++) {
            int idx = (center_sec + s + VFH_SECTORS) % VFH_SECTORS;
            blocked[idx] = 1;
        }
    }

    /* Pass 3: Find best free valley within +-70 deg (offsets -14 .. +14 sectors) */
    int best_offset = 999;
    int run_start = -VFH_MAX_STEER_SEC;
    int in_free = 0;

    for (int off = -VFH_MAX_STEER_SEC; off <= VFH_MAX_STEER_SEC + 1; off++) {
        int is_free = 0;
        if (off <= VFH_MAX_STEER_SEC) {
            int sec = (off + VFH_SECTORS) % VFH_SECTORS;
            is_free = !blocked[sec];
        }
        if (is_free && !in_free) {
            run_start = off;
            in_free = 1;
        } else if (!is_free && in_free) {
            int run_end = off - 1;
            int width = run_end - run_start + 1;
            if (width >= VFH_MIN_VALLEY_W) {
                /* Pick candidate inside valley closest to 0 deg (with 1-sector margin) */
                int lo = run_start + 1;
                int hi = run_end - 1;
                int cand = (0 < lo) ? lo : ((0 > hi) ? hi : 0);
                if (abs(cand) < abs(best_offset)) {
                    best_offset = cand;
                }
            }
            in_free = 0;
        }
    }

    float base_spd = 0.5f * (*io_left + *io_right);
    /* Proximity factor: 0.0 at 460mm -> 1.0 at 155mm */
    float prox = (VFH_TRIGGER_MAX_MM - closest_front_mm) / (VFH_TRIGGER_MAX_MM - VFH_TRIGGER_MIN_MM);
    if (prox < 0.0f) prox = 0.0f;
    if (prox > 1.0f) prox = 1.0f;

    if (best_offset == 999) {
        /* Boxed in ahead (wall / corner): slow gently so bumper/edge-follow handles it softly */
        float scale = 1.0f - 0.55f * prox;
        *io_left  *= scale;
        *io_right *= scale;
    } else if (best_offset != 0) {
        /* Smooth non-reversing differential steer toward open valley (+offset = left = +Y -> right faster) */
        float steer_frac = (float)best_offset / (float)VFH_MAX_STEER_SEC; /* [-1.0, +1.0] */
        float fwd = base_spd * (1.0f - 0.35f * prox - 0.15f * fabsf(steer_frac));
        if (fwd < 3.5f) fwd = 3.5f;
        float diff = base_spd * 0.38f * steer_frac * (0.35f + 0.65f * prox);
        if (fabsf(diff) > fwd - 2.0f) {
            diff = (diff >= 0.0f) ? (fwd - 2.0f) : -(fwd - 2.0f);
        }

        *io_left  = fwd - diff;
        *io_right = fwd + diff;

        if (g_last_vfh_log_ms == 0 || (now - g_last_vfh_log_ms) >= 3000) {
            g_last_vfh_log_ms = now;
            log_msg("[NAV-BOOST] VFH+ transit steer: front=%.0fmm hits=%d valley=%+d deg -> L=%.1f R=%.1f\n",
                    closest_front_mm, danger_hits, best_offset * 5, *io_left, *io_right);
        }
    }
    return 1;
}

/*
 * 1. Main Wheel Control Hook: VFH+ Lidar Steering + Pose/Current Stall Recovery + Slew Limiter
 */
void adjustSpeed(float left, float right) {
    if (!is_medusa_process()) {
        ensure_symbols();
        if (real_adjustSpeed) real_adjustSpeed(left, right);
        return;
    }

    ensure_symbols();
    if (!real_adjustSpeed) return;

    uint64_t t = now_ms();
    float out_l = left;
    float out_r = right;
    int report_stall_gx = -1, report_stall_gy = -1;

    pthread_mutex_lock(&g_nav_mutex);

    /* Never delay emergency stops or full reverse commands */
    if ((left == 0.0f && right == 0.0f) || (left < 0.0f && right < 0.0f)) {
        g_last_l = left;
        g_last_r = right;
        g_last_speed_ms = t;
        g_stall_anchor_ms = 0;
        g_curr_stall_ms = 0;
    } else {
        int docking_active = (g_dock_active_until_ms > t);
        int bump_stuck_active = (g_stuck_start_ms != 0);

        /* Active pose-stall / gap-escape unhook pulse takes priority while running:
         * Phase 1 (0 .. STALL_BACK_MS): gentle reverse to clear obstacle contact
         * Phase 2 (STALL_BACK_MS .. +g_unhook_aim_ms): pivot toward widest Lidar exit gap */
        if (g_unhook_start_ms != 0) {
            uint64_t elapsed = t - g_unhook_start_ms;
            if (elapsed < STALL_BACK_MS) {
                out_l = (g_unhook_dir > 0) ? -13.0f : -10.0f;
                out_r = (g_unhook_dir > 0) ? -10.0f : -13.0f;
                g_last_l = out_l;
                g_last_r = out_r;
                g_last_speed_ms = t;
                pthread_mutex_unlock(&g_nav_mutex);
                real_adjustSpeed(out_l, out_r);
                return;
            } else if (elapsed < (uint64_t)(STALL_BACK_MS + g_unhook_aim_ms)) {
                /* Rotate toward widest Lidar gap (+1 = left -> L=-11, R=+11) */
                out_l = (g_unhook_turn_dir > 0) ? -11.0f :  11.0f;
                out_r = (g_unhook_turn_dir > 0) ?  11.0f : -11.0f;
                g_last_l = out_l;
                g_last_r = out_r;
                g_last_speed_ms = t;
                pthread_mutex_unlock(&g_nav_mutex);
                real_adjustSpeed(out_l, out_r);
                return;
            } else {
                g_unhook_start_ms = 0;
                g_unhook_last_ms = t;
                g_unhook_dir = -g_unhook_dir;
                g_stall_anchor_ms = 0;
                g_curr_stall_ms = 0;
            }
        }

        /* Pose-stall & wheel-current stuck detection (when commanded forward, not docking) */
        if (!docking_active && !bump_stuck_active &&
            left >= STALL_MIN_CMD_SPD && right >= STALL_MIN_CMD_SPD) {
            float px = 0.0f, py = 0.0f;
            int gx = -1, gy = -1;
            int have_pose = get_live_pose_and_grid(&px, &py, &gx, &gy);
            if (have_pose) {
                g_last_gx = gx;
                g_last_gy = gy;
            }

            int trigger_unhook = 0;
            const char *reason = NULL;

            /* Check wheel-current overload or stopped caster wheel */
            int curr_alert = (real_isWheelLargeCurrent && real_isWheelLargeCurrent()) ||
                             (real_isSmallWheelStoped && real_isSmallWheelStoped());
            if (curr_alert) {
                if (g_curr_stall_ms == 0) g_curr_stall_ms = t;
                else if ((t - g_curr_stall_ms) >= STALL_CURR_MS) {
                    trigger_unhook = 1;
                    reason = "wheel-current/caster stall";
                }
            } else {
                g_curr_stall_ms = 0;
            }

            /* Check zero-displacement pose stall (high-centering without bumper) */
            if (!trigger_unhook && have_pose) {
                if (g_stall_anchor_ms == 0) {
                    g_stall_anchor_x = px;
                    g_stall_anchor_y = py;
                    g_stall_anchor_ms = t;
                } else {
                    float dx = px - g_stall_anchor_x;
                    float dy = py - g_stall_anchor_y;
                    float dist = sqrtf(dx * dx + dy * dy);
                    /* Auto-detect meter vs mm pose scale */
                    float thresh = (fabsf(px) > 50.0f || fabsf(py) > 50.0f)
                                   ? STALL_MIN_MOVE_MM : STALL_MIN_MOVE_M;
                    if (dist >= thresh) {
                        g_stall_anchor_x = px;
                        g_stall_anchor_y = py;
                        g_stall_anchor_ms = t;
                    } else if ((t - g_stall_anchor_ms) >= STALL_POSE_MS) {
                        trigger_unhook = 1;
                        reason = "zero-displacement pose stall";
                    }
                }
            }

            if (trigger_unhook &&
                (g_unhook_last_ms == 0 || (t - g_unhook_last_ms) >= STALL_COOLDOWN_MS)) {
                int gap_deg = 0, gap_w = 0;
                int have_gap = find_widest_lidar_gap_deg(&gap_deg, &gap_w);
                if (have_gap && abs(gap_deg) > 15) {
                    g_unhook_turn_dir = (gap_deg >= 0) ? 1 : -1;
                    int aim_ms = (abs(gap_deg) * STALL_AIM_MAX_MS) / 120;
                    if (aim_ms < 120) aim_ms = 120;
                    if (aim_ms > STALL_AIM_MAX_MS) aim_ms = STALL_AIM_MAX_MS;
                    g_unhook_aim_ms = aim_ms;
                } else {
                    g_unhook_aim_ms = 0;
                }
                g_unhook_start_ms = t;
                report_stall_gx = g_last_gx;
                report_stall_gy = g_last_gy;
                out_l = (g_unhook_dir > 0) ? -13.0f : -10.0f;
                out_r = (g_unhook_dir > 0) ? -10.0f : -13.0f;
                g_last_l = out_l;
                g_last_r = out_r;
                g_last_speed_ms = t;
                pthread_mutex_unlock(&g_nav_mutex);

                if (have_gap) {
                    log_msg("[NAV-BOOST] %s near (%d,%d) -> Gap-escape: widest exit %+d deg (width=%d deg), back %dms + aim %dms\n",
                            reason, report_stall_gx, report_stall_gy, gap_deg, gap_w, STALL_BACK_MS, g_unhook_aim_ms);
                } else {
                    log_msg("[NAV-BOOST] %s near (%d,%d) -> %dms reverse unhook\n",
                            reason, report_stall_gx, report_stall_gy, STALL_BACK_MS);
                }
                if (report_stall_gx >= 0 && report_stall_gy >= 0) {
                    report_heat(report_stall_gx, report_stall_gy, 4);
                }
                real_adjustSpeed(out_l, out_r);
                return;
            }
        } else {
            g_stall_anchor_ms = 0;
            g_curr_stall_ms = 0;
        }

        /* VFH+ Lidar dynamic obstacle steering ONLY during point-to-point A* transit
         * (mc_motion_followPoint), matching Ozmo 950 libDeebot architecture so room zig-zag
         * passes and wall-following reach baseboards and turn cleanly without interference. */
        int vfh_steered = 0;
        if (g_follow_point_depth > 0 && !docking_active && !bump_stuck_active &&
            left > 3.0f && right > 3.0f && fabsf(left - right) <= 8.0f) {
            vfh_steered = apply_vfh_plus(&out_l, &out_r, t);
        }

        /* Open-floor non-cleaning transit speed boost (Pin & Go or Return-to-Dock A*) */
        if (g_follow_point_depth > 0 && !docking_active && !bump_stuck_active && !vfh_steered &&
            out_l >= TRANSIT_MIN_CRUISE_SPD && out_r >= TRANSIT_MIN_CRUISE_SPD &&
            fabsf(out_l - out_r) <= TRANSIT_MAX_DIFF_SPD) {
            if (g_silent_check_ms == 0 || (t - g_silent_check_ms) >= 1000) {
                g_silent_check_ms = t;
                g_silent_goto_cached = (access(SILENT_GOTO_FILE, F_OK) == 0);
            }
            if ((g_silent_goto_cached || g_return_transit_until_ms > t) &&
                is_lidar_runway_clear(TRANSIT_RUNWAY_MM, TRANSIT_CORRIDOR_Y_MM)) {
                float orig_avg = 0.5f * (out_l + out_r);
                out_l *= TRANSIT_BOOST_FACTOR;
                out_r *= TRANSIT_BOOST_FACTOR;
                if (out_l > TRANSIT_MAX_SPD_CAP) out_l = TRANSIT_MAX_SPD_CAP;
                if (out_r > TRANSIT_MAX_SPD_CAP) out_r = TRANSIT_MAX_SPD_CAP;
                if (g_last_boost_log_ms == 0 || (t - g_last_boost_log_ms) >= 5000) {
                    g_last_boost_log_ms = t;
                    log_msg("[NAV-BOOST] Transit runway clear (>%.1fm): cruise %.1f -> %.1f\n",
                            TRANSIT_RUNWAY_MM * 0.001f, orig_avg, 0.5f * (out_l + out_r));
                }
            }
        }

        /* Asymmetric slew-rate limiter (fast decel, smooth accel) */
        if (g_last_speed_ms == 0 || (t - g_last_speed_ms) > SPEED_STALE_MS) {
            g_last_l = 0.0f;
            g_last_r = 0.0f;
        }
        float step_l = (fabsf(out_l) < fabsf(g_last_l)) ? (MAX_ACCEL_STEP * 2.0f) : MAX_ACCEL_STEP;
        float step_r = (fabsf(out_r) < fabsf(g_last_r)) ? (MAX_ACCEL_STEP * 2.0f) : MAX_ACCEL_STEP;

        out_l = clamp_step(g_last_l, out_l, step_l);
        out_r = clamp_step(g_last_r, out_r, step_r);
        g_last_l = out_l;
        g_last_r = out_r;
        g_last_speed_ms = t;
    }

    pthread_mutex_unlock(&g_nav_mutex);

    real_adjustSpeed(out_l, out_r);
}

/*
 * 2. Bump cluster tracker -> feeds spatial stuck heat-map in libdirectpath
 */
void dwb_bumpUpdateInGridMapAndReduced(float x, float y, float theta) {
    if (!is_medusa_process()) {
        ensure_symbols();
        if (real_dwb_bumpUpdate) real_dwb_bumpUpdate(x, y, theta);
        return;
    }

    ensure_symbols();
    if (real_dwb_bumpUpdate) {
        real_dwb_bumpUpdate(x, y, theta);
    }

    int gx = -1, gy = -1;
    mm_to_grid400(x, y, &gx, &gy);
    if (gx < 0 || gx >= 400 || gy < 0 || gy >= 400) return;

    uint64_t t = now_ms();
    int trigger_heat = 0;

    pthread_mutex_lock(&g_nav_mutex);
    g_last_gx = gx;
    g_last_gy = gy;

    /* Debounce sustained single bumper contact (350ms) so 1 wall tap != 6 cluster hits */
    if (g_last_bump_ev_ms != 0 && (t - g_last_bump_ev_ms) < 350) {
        pthread_mutex_unlock(&g_nav_mutex);
        return;
    }
    g_last_bump_ev_ms = t;

    if (g_cluster_hits > 0 &&
        (t - g_cluster_start_ms) <= BUMP_CLUSTER_WIN_MS &&
        abs(gx - g_cluster_gx) <= BUMP_CLUSTER_DIST &&
        abs(gy - g_cluster_gy) <= BUMP_CLUSTER_DIST) {
        g_cluster_hits++;
        if (g_cluster_hits == BUMP_CLUSTER_TRIG ||
            (g_cluster_hits > BUMP_CLUSTER_TRIG && (g_cluster_hits % 3) == 0)) {
            trigger_heat = 1;
        }
    } else {
        g_cluster_gx = gx;
        g_cluster_gy = gy;
        g_cluster_hits = 1;
        g_cluster_start_ms = t;
    }
    pthread_mutex_unlock(&g_nav_mutex);

    if (trigger_heat) {
        log_msg("[NAV-BOOST] Bump cluster (%d hits) at grid (%d,%d) -> recording stuck heat\n",
                g_cluster_hits, gx, gy);
        report_heat(gx, gy, 2);
    }
}

/*
 * 3. Trap entry logger & high-weight stuck heat-map marker
 */
void dtp_DeTrapInit(int ok_cb, int fail_cb) {
    if (!is_medusa_process()) {
        ensure_symbols();
        if (real_dtp_DeTrapInit) real_dtp_DeTrapInit(ok_cb, fail_cb);
        return;
    }

    ensure_symbols();

    int gx = -1, gy = -1;
    pthread_mutex_lock(&g_nav_mutex);
    if (!get_live_pose_and_grid(NULL, NULL, &gx, &gy)) {
        gx = g_last_gx;
        gy = g_last_gy;
    }
    pthread_mutex_unlock(&g_nav_mutex);

    log_msg("[NAV-BOOST] dtp_DeTrapInit entered near grid (%d,%d)\n", gx, gy);
    if (gx >= 0 && gy >= 0) {
        report_heat(gx, gy, 4);
    }

    if (real_dtp_DeTrapInit) {
        real_dtp_DeTrapInit(ok_cb, fail_cb);
    }
}

/*
 * 4. Bump-stuck anti-deadlock pulse: brief reverse nudge if one-way spin jams
 */
int bumpStuckCheckAndHandle(void (*on_clear)(void), void (*on_fail)(void),
                            unsigned int spin_thresh, unsigned int fail_thresh)
{
    if (!is_medusa_process()) {
        ensure_symbols();
        if (real_bumpStuckCheck) {
            return real_bumpStuckCheck(on_clear, on_fail, spin_thresh, fail_thresh);
        }
        return 0;
    }

    ensure_symbols();
    if (!real_bumpStuckCheck) return 0;

    int stuck_active = real_bumpStuckCheck(on_clear, on_fail, spin_thresh, fail_thresh);
    uint64_t t = now_ms();

    int do_reverse_nudge = 0;
    int report_gx = -1, report_gy = -1;
    pthread_mutex_lock(&g_nav_mutex);
    if (!stuck_active) {
        g_stuck_start_ms = 0;
        g_pulse_start_ms = 0;
    } else {
        if (g_stuck_start_ms == 0) {
            g_stuck_start_ms = t;
        }
        if (g_pulse_start_ms > 0) {
            if ((t - g_pulse_start_ms) < STUCK_PULSE_DUR_MS) {
                do_reverse_nudge = 1;
            } else {
                g_pulse_start_ms = 0;
                g_last_pulse_ms = t;
            }
        } else if ((t - g_stuck_start_ms) >= STUCK_PULSE_AFTER_MS &&
                   (g_last_pulse_ms == 0 || (t - g_last_pulse_ms) >= STUCK_PULSE_COOLDOWN)) {
            g_pulse_start_ms = t;
            do_reverse_nudge = 1;
            report_gx = g_last_gx;
            report_gy = g_last_gy;
            log_msg("[NAV-BOOST] Bump stuck > %dms -> injecting %dms reverse unhook pulse\n",
                    STUCK_PULSE_AFTER_MS, STUCK_PULSE_DUR_MS);
        }
    }
    pthread_mutex_unlock(&g_nav_mutex);

    if (report_gx >= 0 && report_gy >= 0) {
        report_heat(report_gx, report_gy, 3);
    }

    if (do_reverse_nudge && real_adjustSpeed) {
        real_adjustSpeed(STUCK_PULSE_SPEED, STUCK_PULSE_SPEED);
    }

    return stuck_active;
}

/*
 * 5. Cold-boot Return-to-Dock A* Costmap Hydration (fixes factory bug in erg_gotoOriginalLocationInit)
 *
 * On a fresh medusa boot/restart before any clean has run, DAT_00ece548
 * (map_isUnnecessaryRangeDefinedFlag) is 0 even when a valid saved map + dock front
 * pose are loaded in SLAM, causing erg_gotoOriginalLocationInit to abort in 0ms and
 * jump straight to blind IR searchdock(). When called from erg_gotoOriginalLocationInit
 * with flag == 0, we hydrate the 800x800 and 400x400 A* costmaps from the loaded SLAM
 * map (including virtual walls) and initialize the working range so it A*-paths home.
 */
int map_isUnnecessaryRangeDefinedFlag(void) {
    ensure_symbols();
    if (!real_isUnnecessaryRangeDef) return 0;

    int defined = real_isUnnecessaryRangeDef();
    if (defined != 0 || !is_medusa_process()) {
        return defined;
    }

    void *ret_addr = __builtin_return_address(0);
    Dl_info dli;
    if (!ret_addr || !dladdr(ret_addr, &dli) || !dli.dli_sname ||
        strcmp(dli.dli_sname, "erg_gotoOriginalLocationInit") != 0) {
        return 0;
    }

    /* Lazy-resolve task_ergodic symbols if plugin loaded after init */
    if (!real_isChargerFrontPoseSet || !real_markVirtualWallToMap) {
        void *h_task_erg = dlopen(ERGODIC_PLUGIN, RTLD_NOLOAD | RTLD_NOW);
        if (h_task_erg) {
            if (!real_isChargerFrontPoseSet)
                real_isChargerFrontPoseSet = (alert_bool_fn)dlsym(h_task_erg, "erg_isChargerFrontPoseSet");
            if (!real_markVirtualWallToMap)
                real_markVirtualWallToMap = (mark_vwall_fn)dlsym(h_task_erg, "erg_markVirtualWallToMap");
        }
    }

    if (!real_isChargerFrontPoseSet || !real_isChargerFrontPoseSet() ||
        !real_map_getGridMap || !real_map_getGridMapReduced ||
        !real_copyLocToInt800 || !real_refresh800To400 ||
        !fn_is8neighbours5Free800 || !real_removeUnnecessaryByLoc) {
        return 0;
    }

    int32_t *map800 = (int32_t *)real_map_getGridMap();
    int32_t *map400 = (int32_t *)real_map_getGridMapReduced();
    if (!map800 || !map400) return 0;

    real_copyLocToInt800(map800);

    /* Verify SLAM map actually contains valid free floor cells before enabling A* */
    int free_cells = 0;
    for (int i = 0; i < 800 * 800; i++) {
        int32_t v = map800[i];
        if (v >= 1 && v <= 127) {
            if (++free_cells >= 200) break;
        }
    }
    if (free_cells < 200) {
        log_msg("[NAV-BOOST] Cold-boot Return-to-Dock: SLAM map empty (free=%d), keeping default\n", free_cells);
        return 0;
    }

    if (real_markVirtualWallToMap) {
        real_markVirtualWallToMap(4, 4, 0xc1, 0xc1, map800);
    }
    real_refresh800To400(map800, map400, fn_is8neighbours5Free800);
    real_removeUnnecessaryByLoc();

    int now_def = real_isUnnecessaryRangeDef();
    log_msg("[NAV-BOOST] Cold-boot Return-to-Dock: hydrated 800x800/400x400 A* costmaps from SLAM -> rangeDefined=%d\n",
            now_def);
    return now_def;
}

/*
 * 6. Point-to-point waypoint follower & Return-to-dock state trackers
 */
uint32_t mc_motion_followPoint(void) {
    ensure_symbols();
    if (!real_mc_motion_followPoint) return 2;
    if (!is_medusa_process()) return real_mc_motion_followPoint();

    g_follow_point_depth++;
    uint32_t ret = real_mc_motion_followPoint();
    g_follow_point_depth--;
    return ret;
}

int erg_isChargerFrontPoseSet(void) {
    ensure_symbols();
    if (!real_isChargerFrontPoseSet) {
        void *h_task_erg = dlopen(ERGODIC_PLUGIN, RTLD_NOLOAD | RTLD_NOW);
        if (h_task_erg) {
            real_isChargerFrontPoseSet = (alert_bool_fn)dlsym(h_task_erg, "erg_isChargerFrontPoseSet");
        }
    }
    int res = real_isChargerFrontPoseSet ? real_isChargerFrontPoseSet() : 0;
    if (res && is_medusa_process()) {
        void *ret_addr = __builtin_return_address(0);
        Dl_info dli;
        if (ret_addr && dladdr(ret_addr, &dli) && dli.dli_sname &&
            strcmp(dli.dli_sname, "erg_gotoOriginalLocationInit") == 0) {
            g_return_transit_until_ms = now_ms() + 60000;
        }
    }
    return res;
}

int startReturnToCharger(void) {
    ensure_symbols();
    if (!real_startReturnToCharger) {
        void *h_ret = dlopen(RETURN_PLUGIN, RTLD_NOLOAD | RTLD_NOW);
        if (h_ret) {
            real_startReturnToCharger = (startReturn_fn)dlsym(h_ret, "startReturnToCharger");
        }
    }
    if (is_medusa_process()) {
        g_return_transit_until_ms = 0;
        g_dock_active_until_ms = now_ms() + 15000;
        log_msg("[NAV-BOOST] startReturnToCharger invoked (VFH+ paused for dock approach)\n");
    }
    return real_startReturnToCharger ? real_startReturnToCharger() : 0;
}

uint8_t searchdock(void) {
    ensure_symbols();
    if (!real_searchdock) {
        void *h_ret = dlopen(RETURN_PLUGIN, RTLD_NOLOAD | RTLD_NOW);
        if (h_ret) {
            real_searchdock = (searchdock_fn)dlsym(h_ret, "searchdock");
        }
    }
    if (is_medusa_process()) {
        uint64_t t = now_ms();
        g_return_transit_until_ms = 0;
        g_dock_active_until_ms = t + 3000;
        if (g_last_dock_log_ms == 0 || (t - g_last_dock_log_ms) >= 10000) {
            g_last_dock_log_ms = t;
            log_msg("[NAV-BOOST] searchdock active\n");
        }
    }
    return real_searchdock ? real_searchdock() : 0;
}

__attribute__((constructor))
static void nav_boost_init(void) {
    if (is_medusa_process()) {
        log_msg("[NAV-BOOST] Loaded into medusa (pid=%d)\n", getpid());
    }
}
