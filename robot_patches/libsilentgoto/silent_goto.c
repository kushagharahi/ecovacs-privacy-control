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
#include <sys/ioctl.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>

#define SILENT_GOTO_TRIGGER "/tmp/silent_goto"
#define LOG_FILE "/tmp/silent_goto.log"

static void (*real_MdsMotorControl)(void *ctx) = NULL;
static void (*real_MdsMmiMotorElemCastMotorSetMmiMsg)(void *ctx) = NULL;
static uint32_t (*real_MDSElemCastMsg)(int param_1, const char *param_2, void *param_3) = NULL;
static void *(*real_MDSServerFindElemByName)(void *server, const char *name) = NULL;
static int (*real_MDSElemCallProcess)(void *src, void *dst, const char *msg, void *payload) = NULL;
static int (*real_MDSServerRun)(void *server) = NULL;

static void *g_medusa_server = NULL;
static void *g_task_goto = NULL;
static volatile int g_goto_active = 0;
static int g_target_x = 0;
static int g_target_y = 0;
static pthread_mutex_t g_goto_mutex = PTHREAD_MUTEX_INITIALIZER;

static void log_msg(const char *fmt, ...) {
    FILE *f = fopen(LOG_FILE, "a");
    if (!f) return;
    va_list args;
    va_start(args, fmt);
    vfprintf(f, fmt, args);
    va_end(args);
    fclose(f);
}

static int is_medusa_process(void) {
    char exe[256] = {0};
    ssize_t len = readlink("/proc/self/exe", exe, sizeof(exe) - 1);
    if (len > 0) {
        exe[len] = '\0';
        // Only match the actual medusa binary, not wrapper shell scripts
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

static int start_native_goto(int x, int y) {
    pthread_mutex_lock(&g_goto_mutex);

    if (!real_MDSServerFindElemByName) {
        real_MDSServerFindElemByName = dlsym(RTLD_DEFAULT, "MDSServerFindElemByName");
    }
    if (!real_MDSElemCallProcess) {
        real_MDSElemCallProcess = dlsym(RTLD_DEFAULT, "MDSElemCallProcess");
    }

    if (!g_medusa_server) {
        // Fallback: In non-PIE /usr/bin/medusa, server is at fixed address 0x00024c00
        g_medusa_server = (void *)0x00024c00;
        log_msg("[SILENT-GOTO] Fallback: using fixed medusa server address %p\n", g_medusa_server);
    }

    if (!g_task_goto && g_medusa_server && real_MDSServerFindElemByName) {
        g_task_goto = real_MDSServerFindElemByName(g_medusa_server, "taskGoto");
        log_msg("[SILENT-GOTO] Resolved taskGoto: %p\n", g_task_goto);
    }

    if (!g_task_goto || !real_MDSElemCallProcess) {
        log_msg("[SILENT-GOTO] Error: taskGoto (%p) or MDSElemCallProcess (%p) unavailable (server=%p)\n",
                g_task_goto, real_MDSElemCallProcess, g_medusa_server);
        pthread_mutex_unlock(&g_goto_mutex);
        return -1;
    }

    g_target_x = x;
    g_target_y = y;
    g_goto_active = 1;

    // Touch /tmp/silent_goto so all motor suppression hooks stay engaged during transit
    FILE *f = fopen(SILENT_GOTO_TRIGGER, "w");
    if (f) fclose(f);

    int32_t point[2] = { x, y };
    uint32_t status_val = 0;

    struct {
        uint32_t type;    // 0 = single point transit
        int32_t *coords;  // &point
    } subtask = { 0, point };

    struct {
        uint32_t action;  // 0 = start
        void *args;       // &subtask
        uint32_t *status; // &status_val
    } payload = { 0, &subtask, &status_val };

    log_msg("[SILENT-GOTO] Dispatching native taskGoto to point (%d, %d)...\n", x, y);
    int call_ret = real_MDSElemCallProcess(NULL, g_task_goto, "TASK", &payload);
    log_msg("[SILENT-GOTO] Native taskGoto started! call_ret=%d status=%u\n", call_ret, status_val);

    FILE *st = fopen("/tmp/goto_status", "w");
    if (st) {
        fprintf(st, "RUNNING %d %d\n", x, y);
        fclose(st);
    }

    pthread_mutex_unlock(&g_goto_mutex);
    return 0;
}

static int stop_native_goto(void) {
    pthread_mutex_lock(&g_goto_mutex);

    if (g_task_goto && real_MDSElemCallProcess) {
        uint32_t status_val = 0;
        struct {
            uint32_t action;  // 1 = stop
            void *args;
            uint32_t *status;
        } payload = { 1, NULL, &status_val };

        log_msg("[SILENT-GOTO] Calling native taskGoto stop...\n");
        real_MDSElemCallProcess(NULL, g_task_goto, "TASK", &payload);
    }

    g_goto_active = 0;
    unlink(SILENT_GOTO_TRIGGER);
    unlink("/tmp/goto_stop");

    FILE *st = fopen("/tmp/goto_status", "w");
    if (st) {
        fprintf(st, "STOPPED\n");
        fclose(st);
    }

    pthread_mutex_unlock(&g_goto_mutex);
    return 0;
}

static void *goto_control_worker(void *arg) {
    (void)arg;
    int server_fd = socket(AF_INET, SOCK_STREAM | SOCK_CLOEXEC, 0);
    if (server_fd >= 0) {
        int opt = 1;
        setsockopt(server_fd, SOL_SOCKET, SO_REUSEADDR, &opt, sizeof(opt));
        struct sockaddr_in saddr;
        memset(&saddr, 0, sizeof(saddr));
        saddr.sin_family = AF_INET;
        saddr.sin_addr.s_addr = INADDR_ANY;
        saddr.sin_port = htons(2026);
        if (bind(server_fd, (struct sockaddr *)&saddr, sizeof(saddr)) == 0) {
            listen(server_fd, 5);
            int nb = 1;
            ioctl(server_fd, FIONBIO, &nb);
            log_msg("[SILENT-GOTO] Native Go-To TCP control server listening on 0.0.0.0:2026 in medusa (PID %d)\n", getpid());
        } else {
            log_msg("[SILENT-GOTO] Failed to bind 0.0.0.0:2026 in PID %d\n", getpid());
            close(server_fd);
            server_fd = -1;
        }
    }

    while (1) {
        // 1. Accept TCP connections
        if (server_fd >= 0) {
            int client_fd = accept(server_fd, NULL, NULL);
            if (client_fd >= 0) {
                char buf[128] = {0};
                int n = read(client_fd, buf, sizeof(buf) - 1);
                if (n > 0) {
                    buf[n] = '\0';
                    int x = 0, y = 0;
                    if (strncmp(buf, "STOP", 4) == 0) {
                        stop_native_goto();
                        const char *resp = "{\"status\":\"ok\",\"action\":\"stopped\"}\n";
                        write(client_fd, resp, strlen(resp));
                    } else if (sscanf(buf, "GOTO %d %d", &x, &y) == 2) {
                        int ret = start_native_goto(x, y);
                        char resp[128];
                        snprintf(resp, sizeof(resp), "{\"status\":\"ok\",\"action\":\"started\",\"x\":%d,\"y\":%d,\"ret\":%d}\n", x, y, ret);
                        write(client_fd, resp, strlen(resp));
                    } else if (strncmp(buf, "STATUS", 6) == 0) {
                        char resp[128];
                        snprintf(resp, sizeof(resp), "{\"active\":%d,\"x\":%d,\"y\":%d}\n", g_goto_active, g_target_x, g_target_y);
                        write(client_fd, resp, strlen(resp));
                    }
                }
                close(client_fd);
            }
        }

        // 2. Check file trigger: /tmp/goto_target
        if (access("/tmp/goto_target", F_OK) == 0) {
            FILE *f = fopen("/tmp/goto_target", "r");
            if (f) {
                int x = 0, y = 0;
                if (fscanf(f, "%d %d", &x, &y) == 2) {
                    start_native_goto(x, y);
                }
                fclose(f);
            }
            unlink("/tmp/goto_target");
        }

        // 3. Check file trigger: /tmp/goto_stop
        if (access("/tmp/goto_stop", F_OK) == 0) {
            stop_native_goto();
            unlink("/tmp/goto_stop");
        }

        usleep(50000); // 50ms polling loop
    }
    return NULL;
}

__attribute__((constructor))
static void init_silent_goto(void) {
    if (!is_medusa_process()) {
        return;
    }

    openlog("silent_goto", LOG_PID | LOG_CONS, LOG_USER);
    syslog(LOG_INFO, "libsilentgoto hook initialized in medusa PID %d", getpid());
    log_msg("[SILENT-GOTO] Initialized libsilentgoto hook in medusa PID %d\n", getpid());

    pthread_t th;
    pthread_create(&th, NULL, goto_control_worker, NULL);
    pthread_detach(th);
}

/*
 * MDSServerRun in /usr/bin/medusa:
 * Server event loop entry point. Captures global server reference and pre-resolves taskGoto.
 */
int MDSServerRun(void *server) {
    if (!real_MDSServerRun) {
        real_MDSServerRun = dlsym(RTLD_NEXT, "MDSServerRun");
    }
    g_medusa_server = server;
    log_msg("[SILENT-GOTO] Intercepted MDSServerRun! server=%p\n", server);

    if (!real_MDSServerFindElemByName) {
        real_MDSServerFindElemByName = dlsym(RTLD_DEFAULT, "MDSServerFindElemByName");
    }
    if (real_MDSServerFindElemByName && g_medusa_server) {
        g_task_goto = real_MDSServerFindElemByName(g_medusa_server, "taskGoto");
        log_msg("[SILENT-GOTO] MDSServerRun: resolved taskGoto=%p\n", g_task_goto);
    }

    return real_MDSServerRun ? real_MDSServerRun(server) : 0;
}

/*
 * MDSElemCastMsg in /usr/bin/medusa:
 * Universal message dispatcher between medusa elements.
 */
uint32_t MDSElemCastMsg(int param_1, const char *param_2, void *param_3) {
    if (!real_MDSElemCastMsg) {
        real_MDSElemCastMsg = dlsym(RTLD_NEXT, "MDSElemCastMsg");
        if (!real_MDSElemCastMsg) {
            syslog(LOG_ERR, "Failed to resolve real MDSElemCastMsg: %s", dlerror());
            return 0;
        }
    }

    // Cache medusa server reference from any active element context
    if (!g_medusa_server && param_1) {
        g_medusa_server = *(void **)((char *)param_1 + 4);
        if (g_medusa_server) {
            log_msg("[SILENT-GOTO] Captured medusa server reference: %p\n", g_medusa_server);
        }
    }

    // Intercept native taskGoto completion (Event 4)
    if (g_goto_active && param_2 && strcmp(param_2, "TASK") == 0 && param_3 != NULL) {
        uint32_t *ev = (uint32_t *)param_3;
        if (ev[0] == 4) { // Event 4: Task Completed / Arrived at target point
            log_msg("[SILENT-GOTO] Native taskGoto transit finished (arrival event 4)! Target: (%d, %d)\n", g_target_x, g_target_y);
            g_goto_active = 0;
            unlink(SILENT_GOTO_TRIGGER);

            FILE *st = fopen("/tmp/goto_status", "w");
            if (st) {
                fprintf(st, "ARRIVED %d %d\n", g_target_x, g_target_y);
                fclose(st);
            }
        }
    }

    if (access(SILENT_GOTO_TRIGGER, F_OK) == 0) {
        // 1. SILENCE VOICE PROMPTS ("I am starting to clean", chimes, etc.)
        if (param_2 && strcmp(param_2, "PLAYER") == 0) {
            syslog(LOG_INFO, "[GOTO-HOOK] Suppressed PLAYER voice announcement during silent goto");
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
