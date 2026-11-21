#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <dlfcn.h>
#include <syslog.h>

#define SILENT_GOTO_TRIGGER "/tmp/silent_goto"

static void (*real_MdsMotorControl)(void *ctx) = NULL;
static uint (*real_MDSElemCastMsg)(int param_1, const char *param_2, void *param_3) = NULL;

__attribute__((constructor))
static void init_silent_goto(void) {
    openlog("silent_goto", LOG_PID | LOG_CONS, LOG_USER);
    syslog(LOG_INFO, "libsilentgoto hook initialized in PID %d", getpid());
}

/*
 * MDSElemCastMsg in /usr/bin/medusa:
 * Universal message dispatcher between medusa elements.
 * plug_mmi_periph.so calls:
 *   MDSElemCastMsg(ctx, "PLAYER", &payload) -> Plays chimes and voice prompts (e.g. 0x14 = "Starting to clean")
 *   MDSElemCastMsg(ctx, "MOTOR", &payload)  -> Sets motor state (0 = standard clean, 1 = return/silent)
 */
uint MDSElemCastMsg(int param_1, const char *param_2, void *param_3) {
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
            syslog(LOG_INFO, "[GOTO-HOOK] Suppressed PLAYER voice announcement during silent goto");
            return 0; // Drop audio announcement completely!
        }

        // 2. SILENCE MOTOR SUCTION & BRUSHES
        if (param_2 && strcmp(param_2, "MOTOR") == 0 && param_3 != NULL) {
            // In FUN_00021ed4, local_18 (mode) is at offset +8 from param_3 (&local_10)
            int *mode = (int *)((char *)param_3 + 8);
            if (*mode == 0 || *mode == 3) {
                *mode = 1; // Force Return/Silent mode (Motors 0 RPM)
                syslog(LOG_INFO, "[GOTO-HOOK] Overrode MOTOR cast message mode to 1 (silent transit)");
            }
        }
    }

    return real_MDSElemCastMsg(param_1, param_2, param_3);
}

/*
 * MdsMotorControl in plug_mmi_periph.so:
 * Fallback direct hook on hardware motor speed setter.
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
        if (*mode == 0 || *mode == 3) {
            *mode = 1; // Force Return/Silent mode (Motors 0 RPM)
            syslog(LOG_INFO, "[GOTO-HOOK] MdsMotorControl: Overriding mode to 1 (silent transit)");
        }
    }

    real_MdsMotorControl(ctx);
}
