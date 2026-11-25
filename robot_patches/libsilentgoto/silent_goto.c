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

#define SILENT_GOTO_TRIGGER "/tmp/silent_goto"
#define LOG_FILE "/tmp/silent_goto.log"
#define MMI_PERIPH_PLUGIN "/usr/lib/medusa/plug_mmi_periph.so"

static uint32_t (*real_MdsMotorControl)(void *ctx) = NULL;
static uint32_t (*real_MdsMmiMotorElemCastMotorSetMmiMsg)(void *ctx) = NULL;
static uint32_t (*real_MDSElemCastMsg)(int param_1, const char *param_2, void *param_3) = NULL;
static int (*real_MdsPlayAudio)(void *ctx, uint32_t event) = NULL;

static pthread_mutex_t g_log_mutex = PTHREAD_MUTEX_INITIALIZER;
static pthread_mutex_t g_sym_mutex = PTHREAD_MUTEX_INITIALIZER;
static void *g_periph_handle = NULL;

static void *resolve_periph_sym(const char *name) {
    pthread_mutex_lock(&g_sym_mutex);
    if (!g_periph_handle) {
        g_periph_handle = dlopen(MMI_PERIPH_PLUGIN, RTLD_NOLOAD | RTLD_NOW);
        if (!g_periph_handle) {
            g_periph_handle = dlopen(MMI_PERIPH_PLUGIN, RTLD_NOW);
        }
    }
    void *sym = g_periph_handle ? dlsym(g_periph_handle, name) : NULL;
    if (!sym) {
        sym = dlsym(RTLD_NEXT, name);
    }
    pthread_mutex_unlock(&g_sym_mutex);
    return sym;
}

static int is_suppressed_audio_event(uint32_t event, int silent_goto) {
    // Always suppress event 0x28 (Sound 74: "Difficult to locate, starting new cleaning cycle")
    if (event == 0x28) {
        return 1;
    }
    if (!silent_goto) {
        return 0;
    }
    // Suppress default factory announcements during silent goto (mmi_periph FUN_00031ff8):
    // Event 0x0e (14) = Sound 86 ("Returning to dock" / finish clean transit)
    // Event 0x25 (37) = Sound 55 ("I am relocating")
    // Event 0x26 (38) = Sound 71
    // Event 0x1b (27) = Sound 24 ("Unable to locate" / "Relocation failed")
    // Event 0x18 (24) = Sound 63
    // Event 0x15 (21) = Sound 80 ("I am starting the clean")
    // Event 0x14 (20) = Sound 81 ("I am starting the clean" alt)
    // Event 0x13 (19) = Sound 82 ("I am starting the clean" alt2)
    // Event 0x16 (22) = Sound 79, 0x1d (29) = Sound 65, 0x2d (45) = Sound 75
    return (event == 0x0e || event == 0x13 || event == 0x14 || event == 0x15 ||
            event == 0x16 || event == 0x18 || event == 0x1b || event == 0x1d ||
            event == 0x25 || event == 0x26 || event == 0x2d);
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

/*
 * MDSElemCastMsg in /usr/bin/medusa:
 * Universal message dispatcher between medusa elements.
 * Suppresses voice announcements and overrides motor commands when /tmp/silent_goto is present.
 */
uint32_t MDSElemCastMsg(int param_1, const char *param_2, void *param_3) {
    if (!real_MDSElemCastMsg) {
        pthread_mutex_lock(&g_sym_mutex);
        if (!real_MDSElemCastMsg) {
            real_MDSElemCastMsg = dlsym(RTLD_NEXT, "MDSElemCastMsg");
        }
        pthread_mutex_unlock(&g_sym_mutex);
        if (!real_MDSElemCastMsg) {
            syslog(LOG_ERR, "Failed to resolve real MDSElemCastMsg: %s", dlerror());
            return 0;
        }
    }

    int silent_goto = (access(SILENT_GOTO_TRIGGER, F_OK) == 0);

    // 1. SILENCE VOICE PROMPTS ("I am starting to clean", chimes, etc.) on PLAYER cmd=1 only
    if (param_2 && strcmp(param_2, "PLAYER") == 0 && param_3 != NULL) {
        uint32_t *player_msg = (uint32_t *)param_3;
        if (player_msg[0] == 1 && player_msg[1] != 0) {
            uint32_t event = *(uint32_t *)player_msg[1];
            if (is_suppressed_audio_event(event, silent_goto)) {
                syslog(LOG_INFO, "[SILENT-GOTO] Suppressed PLAYER voice announcement event 0x%x", event);
                log_msg("[SILENT-GOTO] Suppressed PLAYER voice announcement event 0x%x\n", event);
                return 0; // Drop transit audio announcement without breaking language queries (cmd 0/2/3)
            }
        }
    }

    if (silent_goto) {
        // 2. SILENCE MOTOR SUCTION & BRUSHES
        if (param_2 && strcmp(param_2, "MOTOR") == 0 && param_3 != NULL) {
            uint32_t *cmd = (uint32_t *)param_3;
            if (*cmd == 0) {
                int **mode_ptr_ptr = (int **)((char *)param_3 + sizeof(void *));
                if (mode_ptr_ptr && *mode_ptr_ptr) {
                    int *mode_ptr = *mode_ptr_ptr;
                    log_msg("[SILENT-GOTO] Overriding MOTOR mode from %d to 2 (idle/silent)\n", *mode_ptr);
                    *mode_ptr = 2; // Force Idle mode (0 RPM suction, 0 RPM main brush, 0 RPM side brush)
                }
            }
        }
    }

    return real_MDSElemCastMsg(param_1, param_2, param_3);
}

/*
 * MdsMotorControl in plug_mmi_periph.so:
 * Hardware motor speed setter.
 */
uint32_t MdsMotorControl(void *ctx) {
    if (!real_MdsMotorControl) {
        real_MdsMotorControl = resolve_periph_sym("MdsMotorControl");
        if (!real_MdsMotorControl) {
            syslog(LOG_ERR, "Failed to resolve real MdsMotorControl: %s", dlerror());
            return 0;
        }
    }

    if (ctx != NULL && access(SILENT_GOTO_TRIGGER, F_OK) == 0) {
        int *mode = (int *)((char *)ctx + 0x3c);
        log_msg("[SILENT-GOTO] MdsMotorControl: Overriding mode from %d to 2\n", *mode);
        *mode = 2; // Force Idle mode (All cleaning motors 0 RPM)
    }

    return real_MdsMotorControl(ctx);
}

/*
 * MdsMmiMotorElemCastMotorSetMmiMsg in plug_mmi_periph.so:
 * Transmits the 18-byte 'MA' motor speed packet over UART to the STM32 microcontroller.
 */
uint32_t MdsMmiMotorElemCastMotorSetMmiMsg(void *ctx) {
    if (!real_MdsMmiMotorElemCastMotorSetMmiMsg) {
        real_MdsMmiMotorElemCastMotorSetMmiMsg = resolve_periph_sym("MdsMmiMotorElemCastMotorSetMmiMsg");
        if (!real_MdsMmiMotorElemCastMotorSetMmiMsg) {
            return 0;
        }
    }

    if (ctx != NULL && access(SILENT_GOTO_TRIGGER, F_OK) == 0) {
        // Zero all motor speed fields before packet is assembled and sent to STM32
        *(uint16_t *)((char *)ctx + 0x2a) = 0; // Main brush: 0 RPM
        *(uint16_t *)((char *)ctx + 0x2e) = 0; // Side brush: 0 RPM
        *(uint16_t *)((char *)ctx + 0x32) = 0; // Suction motor: 0 RPM
        *(uint16_t *)((char *)ctx + 0x36) = 0; // Water pump: 0
        *(uint8_t *)((char *)ctx + 0x35) = 2;
        log_msg("[SILENT-GOTO] MdsMmiMotorElemCastMotorSetMmiMsg: Zeroed all motor targets\n");
    }

    return real_MdsMmiMotorElemCastMotorSetMmiMsg(ctx);
}

/*
 * MdsPlayAudio in plug_mmi_periph.so:
 * Intercepts automated voice prompt events to suppress factory chatter during silent goto
 * (55 "I am relocating", 80/81/82 "I am starting the clean") without touching files.
 */
int MdsPlayAudio(void *ctx, uint32_t event) {
    if (!real_MdsPlayAudio) {
        real_MdsPlayAudio = resolve_periph_sym("MdsPlayAudio");
    }

    int silent_goto = (access(SILENT_GOTO_TRIGGER, F_OK) == 0);
    if (is_suppressed_audio_event(event, silent_goto)) {
        log_msg("[SILENT-GOTO] Suppressed MdsPlayAudio event 0x%x\n", event);
        syslog(LOG_INFO, "[SILENT-GOTO] Suppressed MdsPlayAudio event 0x%x", event);
        return 1; // Return 1 (success) so caller considers audio played
    }

    return real_MdsPlayAudio ? real_MdsPlayAudio(ctx, event) : 1;
}

__attribute__((constructor))
static void silent_goto_init(void) {
    if (!is_medusa_process()) return;
    log_msg("[SILENT-GOTO] Initializing clean motor & audio suppression patch in medusa (PID %d)\n", getpid());
    openlog("silent_goto", LOG_PID | LOG_CONS, LOG_USER);
}
