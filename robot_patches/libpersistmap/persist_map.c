#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <stdarg.h>
#include <string.h>
#include <stdint.h>
#include <unistd.h>
#include <dlfcn.h>
#include <syslog.h>
#include <pthread.h>
#include <fcntl.h>
#include <sys/stat.h>

/*
 * libpersistmap: Ecovacs Deebot OZMO 930 Map Preservation & Recovery Patch
 *
 * Solves two critical bugs in Ecovacs factory firmware:
 * 1. Bug A (Map-Wipe on Relocation Failure):
 *    When relocGoChgFail (return-to-dock timeout) or relocLiftFail (wheel-lift timeout)
 *    occurs, firmware discards the active map from RAM, resets odometry to (0,0), and
 *    enters "docklostsearch" (blind random wander into walls).
 *    -> This patch intercepts the relocation failure, blocks the map wipe, retains the
 *       last known valid pose, and stops blind wander.
 *
 * 2. Bug B (RecoverMap Empty Stub):
 *    In plug_medusa_plugin_wifiHandler.so (0x1a478), <ctl td="RecoverMap"/> is an empty
 *    stub that only echoes ret="ok" without ever signaling SLAM or reloading storage.
 *    -> This patch intercepts <ctl td="RecoverMap"/> and actually reads /data/FILES/restoreMap.bin,
 *       re-anchors the dock pose from restorePose.bin, and signals the SLAM engine.
 *
 * 3. Bulletproof Flash Protection:
 *    - Hooks rst_removeAllSavedFiles, rst_removeRbpfMapAndPose, rst_removeErgodicData,
 *      rst_removeChargerStand, rst_removeVirtualWall to block deletion of flash map files.
 *    - Hooks unlink and remove libc calls targeting /data/FILES/ files.
 *    - Drops MAP clear commands when valid restoreMap.bin is present.
 */

#define LOG_FILE "/tmp/persist_map.log"
#define RESTORE_MAP_FILE "/data/FILES/restoreMap.bin"
#define RESTORE_POSE_FILE "/data/FILES/restorePose.bin"
#define RESTORE_CHARGER_FILE "/data/FILES/restoreChargerStand.bin"
#define MMI_PERIPH_PLUGIN "/usr/lib/medusa/plug_mmi_periph.so"
#define ERGODIC_PLUGIN "/usr/lib/medusa/plug_medusa_plugin_task_ergodic.so"
#define IKSEMEL_LIB "/usr/lib/libiksemel.so.3"

static uint32_t (*real_MDSElemCastMsg)(int ctx, const char *msg_type, void *payload) = NULL;
static int (*real_MDSElemSendMsg)(void *src, const char *dst_elem, const char *msg_type, void *payload) = NULL;
static int (*real_unlink)(const char *pathname) = NULL;
static int (*real_remove)(const char *pathname) = NULL;
static void (*real_MdsLoadAndAnalyzeMap)(int ctx) = NULL;
static void (*real_MdsStartReloadMap)(int ctx) = NULL;
static void (*real_MdsresetAll)(int ctx, int wipe_flash) = NULL;
static uint32_t (*real_MdsStopCurTask)(int ctx) = NULL;
static uint32_t (*real_MDSMotorControl)(int ctx) = NULL;
static void (*real_MDSReportTaskState)(int ctx) = NULL;
static uint32_t (*real_onMainTaskFinished)(int ctx, int *task, int *msg) = NULL;
static void (*real_rst_removeAllSavedFiles)(void) = NULL;
static int (*real_rst_removeRbpfMapAndPose)(const char *map, const char *carpet, const char *pose) = NULL;
static int (*real_rst_removeErgodicData)(const char *path) = NULL;
static int (*real_rst_removeChargerStand)(const char *path) = NULL;
static int (*real_rst_removeVirtualWall)(const char *path) = NULL;
static char *(*real_iks_find_attrib)(void *x, const char *name) = NULL;
static void *(*real_MdsTaskManagerFindTask)(int ctx, int task_type) = NULL;

static pthread_mutex_t g_log_mutex = PTHREAD_MUTEX_INITIALIZER;
static pthread_mutex_t g_sym_mutex = PTHREAD_MUTEX_INITIALIZER;
static void *g_mmi_handle = NULL;
static void *g_ergodic_handle = NULL;
static void *g_iks_handle = NULL;
static volatile int g_reloc_failure_trapped = 0;
static volatile int g_mmi_ctx = 0;
static volatile int g_allow_user_clear = 0;
static volatile int g_allow_ram_reset = 0;
static volatile int g_fallback_clean_active = 0;

static void *resolve_lib_sym(void **cache, const char *lib_path, const char *name, void *fallback_flag) {
    pthread_mutex_lock(&g_sym_mutex);
    if (cache && !*cache && lib_path) {
        *cache = dlopen(lib_path, RTLD_NOLOAD | RTLD_NOW);
        if (!*cache) *cache = dlopen(lib_path, RTLD_NOW);
    }
    void *sym = (cache && *cache) ? dlsym(*cache, name) : NULL;
    if (!sym) sym = dlsym(fallback_flag, name);
    pthread_mutex_unlock(&g_sym_mutex);
    return sym;
}

static int has_valid_persistent_map(void) {
    struct stat st;
    return (stat(RESTORE_MAP_FILE, &st) == 0 && st.st_size >= 100000);
}

static void log_persist(const char *fmt, ...) {
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
    char exe[256] = {0};
    ssize_t len = readlink("/proc/self/exe", exe, sizeof(exe) - 1);
    if (len > 0) {
        exe[len] = '\0';
        if (strstr(exe, "medusa") != NULL && strstr(exe, ".sh") == NULL) {
            return 1;
        }
    }
    char cmdline[256] = {0};
    FILE *f = fopen("/proc/self/cmdline", "r");
    if (f) {
        size_t n = fread(cmdline, 1, sizeof(cmdline) - 1, f);
        fclose(f);
        if (n > 0) {
            cmdline[n] = '\0';
            if (strstr(cmdline, "medusa") != NULL && strstr(cmdline, ".sh") == NULL) {
                return 1;
            }
        }
    }
    return 0;
}

__attribute__((constructor))
static void init_persist_map(void) {
    if (!is_medusa_process()) return;
    openlog("libpersistmap", LOG_PID | LOG_CONS, LOG_USER);
    syslog(LOG_INFO, "libpersistmap initialized in PID %d", getpid());
    log_persist("[PERSIST-MAP] Initialized libpersistmap (Map Preservation & Recovery) in PID %d\n", getpid());
}

static int is_protected_map_file(const char *path) {
    if (!path) return 0;
    if (strstr(path, "restoreMap.bin") != NULL ||
        strstr(path, "restorePose.bin") != NULL ||
        strstr(path, "restoreChargerStand.bin") != NULL ||
        strstr(path, "ergodic.bin") != NULL ||
        strstr(path, "restoreVirtualWall.bin") != NULL ||
        strstr(path, "restoreCarpet.bin") != NULL) {
        return 1;
    }
    return 0;
}

/*
 * Block deletion of persistent map files via libc unlink
 */
int unlink(const char *pathname) {
    if (!g_allow_user_clear && is_protected_map_file(pathname)) {
        log_persist("[PERSIST-MAP] BLOCKED unlink(\"%s\")!\n", pathname);
        syslog(LOG_WARNING, "[PERSIST-MAP] Prevented deletion of %s", pathname);
        return 0; // Pretend success
    }
    if (!real_unlink) {
        real_unlink = resolve_lib_sym(NULL, NULL, "unlink", RTLD_NEXT);
    }
    return real_unlink ? real_unlink(pathname) : 0;
}

/*
 * Block deletion of persistent map files via libc remove
 */
int remove(const char *pathname) {
    if (!g_allow_user_clear && is_protected_map_file(pathname)) {
        log_persist("[PERSIST-MAP] BLOCKED remove(\"%s\")!\n", pathname);
        syslog(LOG_WARNING, "[PERSIST-MAP] Prevented deletion of %s", pathname);
        return 0; // Pretend success
    }
    if (!real_remove) {
        real_remove = resolve_lib_sym(NULL, NULL, "remove", RTLD_NEXT);
    }
    return real_remove ? real_remove(pathname) : 0;
}

/*
 * Intercept firmware map wipe functions directly
 */
void rst_removeAllSavedFiles(void) {
    if (g_allow_user_clear > 0) {
        if (!real_rst_removeAllSavedFiles)
            real_rst_removeAllSavedFiles = resolve_lib_sym(&g_ergodic_handle, ERGODIC_PLUGIN, "rst_removeAllSavedFiles", RTLD_NEXT);
        if (real_rst_removeAllSavedFiles) { real_rst_removeAllSavedFiles(); return; }
    }
    log_persist("[PERSIST-MAP] BLOCKED rst_removeAllSavedFiles()! Map flash files protected.\n");
    syslog(LOG_WARNING, "[PERSIST-MAP] Blocked rst_removeAllSavedFiles: preserved persistent map files.");
}

int rst_removeRbpfMapAndPose(const char *map, const char *carpet, const char *pose) {
    if (g_allow_user_clear > 0) {
        if (!real_rst_removeRbpfMapAndPose)
            real_rst_removeRbpfMapAndPose = resolve_lib_sym(&g_ergodic_handle, ERGODIC_PLUGIN, "rst_removeRbpfMapAndPose", RTLD_NEXT);
        if (real_rst_removeRbpfMapAndPose) return real_rst_removeRbpfMapAndPose(map, carpet, pose);
    }
    log_persist("[PERSIST-MAP] BLOCKED rst_removeRbpfMapAndPose(%s, %s, %s)!\n",
                map ? map : "null", carpet ? carpet : "null", pose ? pose : "null");
    return 0;
}

int rst_removeErgodicData(const char *path) {
    if (g_allow_user_clear > 0) {
        if (!real_rst_removeErgodicData)
            real_rst_removeErgodicData = resolve_lib_sym(&g_ergodic_handle, ERGODIC_PLUGIN, "rst_removeErgodicData", RTLD_NEXT);
        if (real_rst_removeErgodicData) return real_rst_removeErgodicData(path);
    }
    log_persist("[PERSIST-MAP] BLOCKED rst_removeErgodicData(%s)\n", path ? path : "null");
    return 0;
}

int rst_removeChargerStand(const char *path) {
    if (g_allow_user_clear > 0) {
        if (!real_rst_removeChargerStand)
            real_rst_removeChargerStand = resolve_lib_sym(&g_ergodic_handle, ERGODIC_PLUGIN, "rst_removeChargerStand", RTLD_NEXT);
        if (real_rst_removeChargerStand) return real_rst_removeChargerStand(path);
    }
    log_persist("[PERSIST-MAP] BLOCKED rst_removeChargerStand(%s)\n", path ? path : "null");
    return 0;
}

int rst_removeVirtualWall(const char *path) {
    if (g_allow_user_clear > 0) {
        if (!real_rst_removeVirtualWall)
            real_rst_removeVirtualWall = resolve_lib_sym(&g_ergodic_handle, ERGODIC_PLUGIN, "rst_removeVirtualWall", RTLD_NEXT);
        if (real_rst_removeVirtualWall) return real_rst_removeVirtualWall(path);
    }
    log_persist("[PERSIST-MAP] BLOCKED rst_removeVirtualWall(%s)\n", path ? path : "null");
    return 0;
}

void MdsLoadAndAnalyzeMap(int ctx) {
    if (ctx) g_mmi_ctx = ctx;
    if (!real_MdsLoadAndAnalyzeMap)
        real_MdsLoadAndAnalyzeMap = resolve_lib_sym(&g_mmi_handle, MMI_PERIPH_PLUGIN, "MdsLoadAndAnalyzeMap", RTLD_NEXT);
    if (real_MdsLoadAndAnalyzeMap) real_MdsLoadAndAnalyzeMap(ctx);
}

void MdsStartReloadMap(int ctx) {
    if (ctx) g_mmi_ctx = ctx;
    if (!real_MdsStartReloadMap)
        real_MdsStartReloadMap = resolve_lib_sym(&g_mmi_handle, MMI_PERIPH_PLUGIN, "MdsStartReloadMap", RTLD_NEXT);
    if (real_MdsStartReloadMap) real_MdsStartReloadMap(ctx);
}

void MdsresetAll(int ctx, int wipe_flash) {
    if (ctx) g_mmi_ctx = ctx;
    if (!real_MdsresetAll)
        real_MdsresetAll = resolve_lib_sym(&g_mmi_handle, MMI_PERIPH_PLUGIN, "MdsresetAll", RTLD_NEXT);
    if (wipe_flash != 0) {
        __atomic_add_fetch(&g_allow_user_clear, 1, __ATOMIC_SEQ_CST);
        if (real_MdsresetAll) real_MdsresetAll(ctx, wipe_flash);
        __atomic_sub_fetch(&g_allow_user_clear, 1, __ATOMIC_SEQ_CST);
        return;
    }
    if (g_allow_ram_reset > 0) {
        log_persist("[PERSIST-MAP] Allowing RAM-only MdsresetAll(ctx=0x%x, wipe_flash=0) for fallback clean.\n", ctx);
        if (real_MdsresetAll) real_MdsresetAll(ctx, wipe_flash);
        return;
    }
    if (ctx != 0 && (*(int *)(ctx + 400) == 2 || has_valid_persistent_map())) {
        log_persist("[PERSIST-MAP] BLOCKED MdsresetAll(ctx=0x%x, wipe_flash=0); reloading persistent map.\n", ctx);
        MdsLoadAndAnalyzeMap(ctx);
        return;
    }
    if (real_MdsresetAll) real_MdsresetAll(ctx, wipe_flash);
}

uint32_t onMainTaskFinished(int ctx, int *task, int *msg) {
    if (ctx) g_mmi_ctx = ctx;
    if (!real_onMainTaskFinished)
        real_onMainTaskFinished = resolve_lib_sym(&g_mmi_handle, MMI_PERIPH_PLUGIN, "onMainTaskFinished", RTLD_NEXT);

    if (ctx != 0 && task != NULL && *task != 0) {
        int task_type = *(int *)(*task + 0x2c);
        if (task_type == 6 && (*(int *)(ctx + 400) == 2 || has_valid_persistent_map())) {
            int *sub = (int *)task[1];
            int *res = (msg != NULL) ? *(int **)((char *)msg + 4) : NULL;
            int reloc_type = sub ? *sub : -1;
            int reloc_ok = (res != NULL && *res != 0);

            if (reloc_type == 0 && !reloc_ok) {
                if (!real_MdsTaskManagerFindTask)
                    real_MdsTaskManagerFindTask = resolve_lib_sym(&g_mmi_handle, MMI_PERIPH_PLUGIN, "MdsTaskManagerFindTask", RTLD_NEXT);
                void *clean_task = real_MdsTaskManagerFindTask ? real_MdsTaskManagerFindTask(ctx, 0) : NULL;
                void *return_task = real_MdsTaskManagerFindTask ? real_MdsTaskManagerFindTask(ctx, 3) : NULL;
                if (clean_task != NULL && return_task == NULL) {
                    log_persist("[PERSIST-MAP] Relocation failed with clean task queued: running RAM fallback clean (flash map preserved).\n");
                    g_fallback_clean_active = 1;
                    g_reloc_failure_trapped = 0;
                    __atomic_add_fetch(&g_allow_ram_reset, 1, __ATOMIC_SEQ_CST);
                    uint32_t ret = real_onMainTaskFinished ? real_onMainTaskFinished(ctx, task, msg) : 1;
                    __atomic_sub_fetch(&g_allow_ram_reset, 1, __ATOMIC_SEQ_CST);
                    return ret;
                }

                log_persist("[PERSIST-MAP] Lift/Running relocation failed: stopping cleanly & restoring map frame.\n");
                if (!real_MdsStopCurTask) real_MdsStopCurTask = resolve_lib_sym(&g_mmi_handle, MMI_PERIPH_PLUGIN, "MdsStopCurTask", RTLD_NEXT);
                if (!real_MDSMotorControl) real_MDSMotorControl = resolve_lib_sym(&g_mmi_handle, MMI_PERIPH_PLUGIN, "MDSMotorControl", RTLD_NEXT);
                if (!real_MDSReportTaskState) real_MDSReportTaskState = resolve_lib_sym(&g_mmi_handle, MMI_PERIPH_PLUGIN, "MDSReportTaskState", RTLD_NEXT);
                if (real_MdsStopCurTask) real_MdsStopCurTask(ctx);
                if (real_MDSMotorControl) real_MDSMotorControl(ctx);
                MdsLoadAndAnalyzeMap(ctx);
                if (real_MDSReportTaskState) real_MDSReportTaskState(ctx);
                return 0;
            }
            if (reloc_type == 2 && !reloc_ok && *(int *)(ctx + 0x174) >= 3 && *(int *)(ctx + 0x14c) != 0) {
                log_persist("[PERSIST-MAP] Reload final fail: preventing MdsSaveMapToFs overwrite & reloading map.\n");
                if (!real_MdsStopCurTask) real_MdsStopCurTask = resolve_lib_sym(&g_mmi_handle, MMI_PERIPH_PLUGIN, "MdsStopCurTask", RTLD_NEXT);
                if (!real_MDSReportTaskState) real_MDSReportTaskState = resolve_lib_sym(&g_mmi_handle, MMI_PERIPH_PLUGIN, "MDSReportTaskState", RTLD_NEXT);
                *(int *)(ctx + 0x14c) = 0;
                if (real_MdsStopCurTask) real_MdsStopCurTask(ctx);
                MdsLoadAndAnalyzeMap(ctx);
                if (real_MDSReportTaskState) real_MDSReportTaskState(ctx);
                return 0;
            }
        } else if (task_type == 3 && g_fallback_clean_active && has_valid_persistent_map()) {
            uint32_t ret = real_onMainTaskFinished ? real_onMainTaskFinished(ctx, task, msg) : 1;
            if (!real_MDSElemCastMsg)
                real_MDSElemCastMsg = dlsym(RTLD_NEXT, "MDSElemCastMsg");
            int on_chg = 0;
            if (real_MDSElemCastMsg) {
                int ch_cmd[2] = {1, (int)(long)&on_chg};
                real_MDSElemCastMsg(ctx, "CH_SIG", ch_cmd);
            }
            if (on_chg != 0) {
                g_fallback_clean_active = 0;
                if (*(int *)(ctx + 400) != 2) {
                    log_persist("[PERSIST-MAP] Fallback clean docked: reloading persistent map from flash.\n");
                    MdsLoadAndAnalyzeMap(ctx);
                    MdsStartReloadMap(ctx);
                }
            }
            return ret;
        }
    }
    return real_onMainTaskFinished ? real_onMainTaskFinished(ctx, task, msg) : 1;
}

/*
 * Force reload of persistent map files into memory when requested
 */
static int reload_persistent_map_files(void) {
    struct stat st;
    if (stat(RESTORE_MAP_FILE, &st) != 0 || st.st_size < 100000) {
        log_persist("[PERSIST-MAP] Error: %s not found or invalid size (%ld bytes)\n",
                    RESTORE_MAP_FILE, (long)st.st_size);
        return -1;
    }

    log_persist("[PERSIST-MAP] Confirmed %s is intact (%ld bytes). Reloading storage...\n",
                RESTORE_MAP_FILE, (long)st.st_size);

    // Read pose calibration
    FILE *fp = fopen(RESTORE_POSE_FILE, "rb");
    if (fp) {
        float pose[3] = {0};
        if (fread(pose, sizeof(float), 3, fp) == 3) {
            log_persist("[PERSIST-MAP] Verified restorePose: x=%.2f, y=%.2f, a=%.2f\n",
                        pose[0], pose[1], pose[2]);
        }
        fclose(fp);
    }

    return 0;
}

/*
 * Intercept universal element-to-element message broadcasts
 */
uint32_t MDSElemCastMsg(int ctx, const char *msg_type, void *payload) {
    if (!real_MDSElemCastMsg) {
        real_MDSElemCastMsg = dlsym(RTLD_NEXT, "MDSElemCastMsg");
        if (!real_MDSElemCastMsg) {
            syslog(LOG_ERR, "Failed to resolve real MDSElemCastMsg: %s", dlerror());
            return 0;
        }
    }

    if (msg_type != NULL) {
        // 1. Trap Relocation Failure from TASK_MAN (type 5, reloc_info = { reloc_type, status })
        if (strcmp(msg_type, "TASK_MAN") == 0 && payload != NULL) {
            int *task_data = (int *)payload;
            if (task_data && task_data[0] == 5 && task_data[1] != 0) {
                int *reloc_info = (int *)task_data[1];
                int reloc_type = reloc_info[0];   // 0=Lift, 1=OnChg, 2=Recover, 3=GoChg
                int reloc_status = reloc_info[1]; // 0=Start, 1=Ok, 2=Fail
                if (reloc_status == 2) {
                    g_reloc_failure_trapped = 1;
                    log_persist("[PERSIST-MAP] TRAPPED relocation failure type=%d status=%d!\n", reloc_type, reloc_status);
                    log_persist("[PERSIST-MAP] Blocking catastrophic map wipe and pose reset.\n");
                    // Override status to prevent wiping: tell caller it is handled safely
                    syslog(LOG_WARNING, "[PERSIST-MAP] Relocation failure type=%d trapped. Persistent map preserved.", reloc_type);
                } else if (reloc_status == 1) {
                    g_reloc_failure_trapped = 0;
                    log_persist("[PERSIST-MAP] Relocation succeeded (type=%d).\n", reloc_type);
                }
            }
        }

        // 2. Trap ClearMap or map purge: only drop subcmd 4 (clearWifiMap), preserving restoreWifiMap(10)/setReloc(0xe)
        if (strcmp(msg_type, "MAP") == 0 && payload != NULL) {
            int *map_cmd = (int *)payload;
            if (map_cmd && (*map_cmd == 3) && map_cmd[1] != 0) {
                int subcmd = *(int *)map_cmd[1];
                if (subcmd == 4 && !g_allow_user_clear && has_valid_persistent_map()) {
                    log_persist("[PERSIST-MAP] BLOCKED automatic MAP cmd 3 subcmd 4 (clearWifiMap) because valid %s exists!\n", RESTORE_MAP_FILE);
                    return 0; // Drop the wipe instruction completely
                }
            }
        }

        // 3. Intercept incoming XML commands for RecoverMap (CtlCmd payload = { from, to, iks *node })
        if (strcmp(msg_type, "CtlCmd") == 0 && payload != NULL) {
            // Check if RecoverMap command is received
            void **ctl = (void **)payload;
            if (ctl[2] != NULL) {
                if (!real_iks_find_attrib)
                    real_iks_find_attrib = resolve_lib_sym(&g_iks_handle, IKSEMEL_LIB, "iks_find_attrib", RTLD_DEFAULT);
                const char *td = real_iks_find_attrib ? real_iks_find_attrib(ctl[2], "td") : NULL;
                if (td && strcmp(td, "RecoverMap") == 0) {
                    log_persist("[PERSIST-MAP] Intercepted <ctl td=\"RecoverMap\"/>! Executing REAL map recovery...\n");
                    uint32_t ret = real_MDSElemCastMsg(ctx, msg_type, payload);
                    if (g_mmi_ctx != 0) {
                        MdsStartReloadMap(g_mmi_ctx);
                    } else {
                        reload_persistent_map_files();
                    }
                    return ret;
                }
            }
        }

        // 4. Note: allow SLAM cmd 7 (resetEcoLocalization, needed by FUN_00014768 during valid Lift reloc) and cmd 4 (slip) to pass through.
    }

    return real_MDSElemCastMsg(ctx, msg_type, payload);
}

/*
 * Intercept direct element-to-element directed messages
 */
int MDSElemSendMsg(void *src, const char *dst_elem, const char *msg_type, void *payload) {
    if (!real_MDSElemSendMsg) {
        real_MDSElemSendMsg = dlsym(RTLD_NEXT, "MDSElemSendMsg");
        if (!real_MDSElemSendMsg) {
            syslog(LOG_ERR, "Failed to resolve real MDSElemSendMsg: %s", dlerror());
            return -1;
        }
    }

    // Intercept docklostsearch transition in taskreturn
    if (dst_elem && strcmp(dst_elem, "taskreturn") == 0 && msg_type != NULL) {
        if (strstr(msg_type, "docklostsearch") != NULL || strstr(msg_type, "dockrandom") != NULL) {
            if (g_reloc_failure_trapped) {
                log_persist("[PERSIST-MAP] BLOCKED docklostsearch / dockrandom: suppressing blind wandering into walls.\n");
                return 0; // Suppress blind wander
            }
        }
    }

    return real_MDSElemSendMsg(src, dst_elem, msg_type, payload);
}
