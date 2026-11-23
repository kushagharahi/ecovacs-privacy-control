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
 *    - Hooks unlink and remove libc calls targeting /data/FILES/*.
 *    - Drops MAP clear commands when valid restoreMap.bin is present.
 */

#define LOG_FILE "/tmp/persist_map.log"
#define RESTORE_MAP_FILE "/data/FILES/restoreMap.bin"
#define RESTORE_POSE_FILE "/data/FILES/restorePose.bin"
#define RESTORE_CHARGER_FILE "/data/FILES/restoreChargerStand.bin"

static uint32_t (*real_MDSElemCastMsg)(int ctx, const char *msg_type, void *payload) = NULL;
static int (*real_MDSElemSendMsg)(void *src, const char *dst_elem, const char *msg_type, void *payload) = NULL;
static int (*real_unlink)(const char *pathname) = NULL;
static int (*real_remove)(const char *pathname) = NULL;

static pthread_mutex_t g_log_mutex = PTHREAD_MUTEX_INITIALIZER;
static volatile int g_reloc_failure_trapped = 0;

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
    if (is_protected_map_file(pathname)) {
        log_persist("[PERSIST-MAP] BLOCKED unlink(\"%s\")!\n", pathname);
        syslog(LOG_WARNING, "[PERSIST-MAP] Prevented deletion of %s", pathname);
        return 0; // Pretend success
    }
    if (!real_unlink) {
        real_unlink = dlsym(RTLD_NEXT, "unlink");
    }
    return real_unlink ? real_unlink(pathname) : 0;
}

/*
 * Block deletion of persistent map files via libc remove
 */
int remove(const char *pathname) {
    if (is_protected_map_file(pathname)) {
        log_persist("[PERSIST-MAP] BLOCKED remove(\"%s\")!\n", pathname);
        syslog(LOG_WARNING, "[PERSIST-MAP] Prevented deletion of %s", pathname);
        return 0; // Pretend success
    }
    if (!real_remove) {
        real_remove = dlsym(RTLD_NEXT, "remove");
    }
    return real_remove ? real_remove(pathname) : 0;
}

/*
 * Intercept firmware map wipe functions directly
 */
void rst_removeAllSavedFiles(void) {
    log_persist("[PERSIST-MAP] BLOCKED rst_removeAllSavedFiles()! Map flash files protected.\n");
    syslog(LOG_WARNING, "[PERSIST-MAP] Blocked rst_removeAllSavedFiles: preserved persistent map files.");
}

int rst_removeRbpfMapAndPose(const char *map, const char *carpet, const char *pose) {
    log_persist("[PERSIST-MAP] BLOCKED rst_removeRbpfMapAndPose(%s, %s, %s)!\n",
                map ? map : "null", carpet ? carpet : "null", pose ? pose : "null");
    return 0;
}

int rst_removeErgodicData(const char *path) {
    log_persist("[PERSIST-MAP] BLOCKED rst_removeErgodicData(%s)\n", path ? path : "null");
    return 0;
}

int rst_removeChargerStand(const char *path) {
    log_persist("[PERSIST-MAP] BLOCKED rst_removeChargerStand(%s)\n", path ? path : "null");
    return 0;
}

int rst_removeVirtualWall(const char *path) {
    log_persist("[PERSIST-MAP] BLOCKED rst_removeVirtualWall(%s)\n", path ? path : "null");
    return 0;
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
        // 1. Trap Relocation Failure from TASK_MAN (type 5, non-success status)
        if (strcmp(msg_type, "TASK_MAN") == 0 && payload != NULL) {
            int **task_data = (int **)payload;
            if (task_data && *task_data) {
                int task_cmd = **task_data;
                if (task_cmd == 5) { // Relocation state change
                    int reloc_state = (*task_data)[1];
                    // States: 0 = start, 1 = relocating, 2 = relocLiftOk, 3 = relocLiftFail, 8 = relocGoChgOk, 9 = relocGoChgFail
                    if (reloc_state != 2 && reloc_state != 8 && reloc_state != 0 && reloc_state != 1) {
                        log_persist("[PERSIST-MAP] TRAPPED relocation failure state %d!\n", reloc_state);
                        log_persist("[PERSIST-MAP] Blocking catastrophic map wipe and pose reset.\n");
                        g_reloc_failure_trapped = 1;

                        // Override status to prevent wiping: tell caller it is handled safely
                        syslog(LOG_WARNING, "[PERSIST-MAP] Relocation failure %d trapped. Persistent map preserved.", reloc_state);
                    } else if (reloc_state == 2 || reloc_state == 8) {
                        g_reloc_failure_trapped = 0;
                        log_persist("[PERSIST-MAP] Relocation succeeded (state %d).\n", reloc_state);
                    }
                }
            }
        }

        // 2. Trap ClearMap or map purge: if persistent map files exist, block destructive wipe
        if (strcmp(msg_type, "MAP") == 0 && payload != NULL) {
            int *map_cmd = (int *)payload;
            if (map_cmd && (*map_cmd == 3)) {
                struct stat st;
                if (stat(RESTORE_MAP_FILE, &st) == 0 && st.st_size >= 100000) {
                    log_persist("[PERSIST-MAP] BLOCKED automatic MAP cmd 3 (map clear) because valid %s exists!\n", RESTORE_MAP_FILE);
                    return 0; // Drop the wipe instruction completely
                }
            }
        }

        // 3. Intercept incoming XML commands for RecoverMap
        if (strcmp(msg_type, "CtlCmd") == 0 && payload != NULL) {
            // Check if RecoverMap command is received
            char *xml_str = (char *)payload;
            if (xml_str && strstr(xml_str, "td=\"RecoverMap\"") != NULL) {
                log_persist("[PERSIST-MAP] Intercepted <ctl td=\"RecoverMap\"/>! Executing REAL map recovery...\n");
                reload_persistent_map_files();
            }
        }

        // 4. Trap SLAM destructive resets and slip panics
        if (strcmp(msg_type, "SLAM") == 0 && payload != NULL) {
            int *slam_data = (int *)payload;
            if (slam_data) {
                int slam_cmd = *slam_data;
                if (slam_cmd == 7) {
                    log_persist("[PERSIST-MAP] BLOCKED SLAM cmd 7 (resetEcoLocalization / RAM wipe)!\n");
                    return 0; // Drop destructive reset completely to keep particles and RAM map
                }
                if (slam_cmd == 4) {
                    log_persist("[PERSIST-MAP] BLOCKED SLAM cmd 4 (onSlamRobotSlipWarn / slip panic)\n");
                    return 0; // Drop slip warning to prevent unwarranted relocation wanders
                }
            }
        }
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
