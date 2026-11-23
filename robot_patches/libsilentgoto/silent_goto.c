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

static void (*real_MdsMotorControl)(void *ctx) = NULL;
static void (*real_MdsMmiMotorElemCastMotorSetMmiMsg)(void *ctx) = NULL;
static uint32_t (*real_MDSElemCastMsg)(int param_1, const char *param_2, void *param_3) = NULL;
static int (*real_MdsPlayAudio)(void *ctx, uint32_t event) = NULL;

static pthread_mutex_t g_log_mutex = PTHREAD_MUTEX_INITIALIZER;

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
        real_MDSElemCastMsg = dlsym(RTLD_NEXT, "MDSElemCastMsg");
        if (!real_MDSElemCastMsg) {
            syslog(LOG_ERR, "Failed to resolve real MDSElemCastMsg: %s", dlerror());
            return 0;
        }
    }

    if (access(SILENT_GOTO_TRIGGER, F_OK) == 0) {
        // 1. SILENCE VOICE PROMPTS ("I am starting to clean", chimes, etc.)
        if (param_2 && strcmp(param_2, "PLAYER") == 0) {
            syslog(LOG_INFO, "[SILENT-GOTO] Suppressed PLAYER voice announcement during silent goto");
            log_msg("[SILENT-GOTO] Suppressed PLAYER voice announcement during silent goto\n");
            return 0; // Drop audio announcement completely
        }

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
void MdsMotorControl(void *ctx) {
    if (!real_MdsMotorControl) {
        real_MdsMotorControl = dlsym(RTLD_NEXT, "MdsMotorControl");
        if (!real_MdsMotorControl) {
            syslog(LOG_ERR, "Failed to resolve real MdsMotorControl: %s", dlerror());
            return;
        }
    }

    if (ctx != NULL && access(SILENT_GOTO_TRIGGER, F_OK) == 0) {
        int *mode = (int *)((char *)ctx + 0x3c);
        log_msg("[SILENT-GOTO] MdsMotorControl: Overriding mode from %d to 2\n", *mode);
        *mode = 2; // Force Idle mode (All cleaning motors 0 RPM)
    }

    real_MdsMotorControl(ctx);
}

/*
 * MdsMmiMotorElemCastMotorSetMmiMsg in plug_mmi_periph.so:
 * Transmits the 18-byte 'MA' motor speed packet over UART to the STM32 microcontroller.
 */
void MdsMmiMotorElemCastMotorSetMmiMsg(void *ctx) {
    if (!real_MdsMmiMotorElemCastMotorSetMmiMsg) {
        real_MdsMmiMotorElemCastMotorSetMmiMsg = dlsym(RTLD_NEXT, "MdsMmiMotorElemCastMotorSetMmiMsg");
        if (!real_MdsMmiMotorElemCastMotorSetMmiMsg) {
            return;
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

    real_MdsMmiMotorElemCastMotorSetMmiMsg(ctx);
}

/*
 * MdsPlayAudio in plug_mmi_periph.so:
 * Intercepts automated voice prompt events to suppress factory chatter during silent goto
 * (55 "I am relocating", 80/81/82 "I am starting the clean") without touching files.
 */
int MdsPlayAudio(void *ctx, uint32_t event) {
    if (!real_MdsPlayAudio) {
        real_MdsPlayAudio = dlsym(RTLD_NEXT, "MdsPlayAudio");
    }

    if (access(SILENT_GOTO_TRIGGER, F_OK) == 0) {
        // Suppress default factory announcements during silent goto:
        // Event 0x25 (37) = Sound 55 ("I am relocating")
        // Event 0x18 (24) = Sound 24 ("Unable to locate" / "Relocation failed")
        // Event 0x15 (21) = Sound 80 ("I am starting the clean")
        // Event 0x14 (20) = Sound 81 ("I am starting the clean" alt)
        // Event 0x13 (19) = Sound 82 ("I am starting the clean" alt2)
        if (event == 0x25 || event == 0x18 || event == 0x15 || event == 0x14 || event == 0x13) {
            log_msg("[SILENT-GOTO] Suppressed MdsPlayAudio event 0x%x\n", event);
            syslog(LOG_INFO, "[SILENT-GOTO] Suppressed MdsPlayAudio event 0x%x", event);
            return 1; // Return 1 (success) so caller considers audio played
        }
    }

    return real_MdsPlayAudio ? real_MdsPlayAudio(ctx, event) : 1;
}

__attribute__((constructor))
static void silent_goto_init(void) {
    if (!is_medusa_process()) return;
    log_msg("[SILENT-GOTO] Initializing clean motor & audio suppression patch in medusa (PID %d)\n", getpid());
    openlog("silent_goto", LOG_PID | LOG_CONS, LOG_USER);
}
