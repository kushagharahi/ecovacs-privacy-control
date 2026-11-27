#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <stdarg.h>
#include <stdint.h>
#include <string.h>
#include <unistd.h>
#include <dlfcn.h>
#include <syslog.h>
#include <pthread.h>
#include <sys/socket.h>
#include <sys/ioctl.h>
#include <net/if.h>
#include <netinet/in.h>
#include <arpa/inet.h>

/*
 * libwifiguard: Ecovacs OZMO 930 WiFi Power-Gate & LED Sync Patch
 *
 * Fixes three factory firmware bugs:
 * 1. Low-battery MCU frame ('C''H'=1 -> CH_SIG 10 in MdsTaskManagerProcess 0x29d2c)
 *    and on-dock power-switch toggle (KEY (0,3) in MdsKeyMsgHandler 0x2940c) execute
 *    /etc/conf/medusa/disable_wifi.sh, killing wifi_service/wpa_supplicant/wifi_daemon/dhcpcd,
 *    unloading wlan.ko, and pulling gpio274=0 with no re-enable path on docking.
 * 2. /etc/conf/medusa/xmpp_server_status.sh turns the physical WiFi LED solid whenever XMPP
 *    is online (including 127.0.0.1 loopback on-bot controller) even when wlan0 is down.
 * 3. Self-heals gpio274 + WiFi daemons in onRobotEnterCharger (0x29264) if wlan0 was
 *    ever powered down externally prior to docking.
 */

#define LOG_FILE          "/tmp/wifi_guard.log"
#define WIFI_STATUS_FILE  "/tmp/wifi_status.bin"
#define GPIO_WIFI_PWR     "/sys/class/gpio/gpio274/value"
#define MMI_PERIPH_PLUGIN "/usr/lib/medusa/plug_mmi_periph.so"

#define LED_CMD_ON   "mdsctl 935trans '{\"todo\":\"rtctl\",\"cmd\":\"ledCtl\",\"index\":2,\"mode\":\"stable\",\"value\":1}' >/dev/null 2>&1 &"
#define LED_CMD_SLOW "mdsctl 935trans '{\"todo\":\"rtctl\",\"cmd\":\"ledCtl\",\"index\":2,\"mode\":\"flash\",\"value\":15}' >/dev/null 2>&1 &"
#define WIFI_RECOVER_CMD "(echo 1 > /sys/class/gpio/gpio274/value 2>/dev/null; sleep 1; /etc/rc.d/wifi.sh start; /etc/rc.d/wpa_supplicant.sh start; /etc/rc.d/wifi_service.sh start; pidof wifi_daemon >/dev/null || /etc/rc.d/wifi_daemon.sh start) >/dev/null 2>&1 &"

enum {
    WLAN_DOWN       = 0,
    WLAN_AP         = 1,
    WLAN_STA_ONLINE = 2,
};

static int (*real_system)(const char *command) = NULL;
static uint32_t (*real_MDSElemCastMsg)(int ctx, const char *msg_type, void *payload) = NULL;
static void (*real_onRobotEnterCharger)(int ctx) = NULL;

static pthread_mutex_t g_log_mutex = PTHREAD_MUTEX_INITIALIZER;
static pthread_mutex_t g_sym_mutex = PTHREAD_MUTEX_INITIALIZER;
static void *g_periph_handle = NULL;

static void log_wg(const char *fmt, ...) {
    pthread_mutex_lock(&g_log_mutex);
    FILE *f = fopen(LOG_FILE, "a");
    if (f) {
        va_list ap;
        va_start(ap, fmt);
        vfprintf(f, fmt, ap);
        va_end(ap);
        fclose(f);
    }
    pthread_mutex_unlock(&g_log_mutex);
}

static int is_medusa_process(void) {
    static int cached = -1;
    if (cached >= 0) return cached;

    char exe[256] = {0};
    ssize_t len = readlink("/proc/self/exe", exe, sizeof(exe) - 1);
    if (len > 0) {
        const char *slash = strrchr(exe, '/');
        const char *base = slash ? slash + 1 : exe;
        cached = (strcmp(base, "medusa") == 0) ? 1 : 0;
        return cached;
    }
    return 0;
}

static int get_wlan0_state(void) {
    int fd = socket(AF_INET, SOCK_DGRAM, 0);
    if (fd < 0) return WLAN_DOWN;

    struct ifreq ifr;
    memset(&ifr, 0, sizeof(ifr));
    strncpy(ifr.ifr_name, "wlan0", IFNAMSIZ - 1);

    if (ioctl(fd, SIOCGIFFLAGS, &ifr) < 0 ||
        (ifr.ifr_flags & (IFF_UP | IFF_RUNNING)) != (IFF_UP | IFF_RUNNING)) {
        close(fd);
        return WLAN_DOWN;
    }

    memset(&ifr, 0, sizeof(ifr));
    strncpy(ifr.ifr_name, "wlan0", IFNAMSIZ - 1);
    if (ioctl(fd, SIOCGIFADDR, &ifr) < 0) {
        close(fd);
        return WLAN_DOWN;
    }
    close(fd);

    uint32_t host_ip = ntohl(((struct sockaddr_in *)&ifr.ifr_addr)->sin_addr.s_addr);
    if (host_ip == 0 || (host_ip & 0xffff0000U) == 0xa9fe0000U) {
        return WLAN_DOWN; // Unassigned or 169.254.x.x link-local fallback
    }
    if (host_ip == 0xc0a80001U) {
        return WLAN_AP;   // 192.168.0.1 provisioning AP
    }
    return WLAN_STA_ONLINE;
}

static int is_wifi_hw_disabled(void) {
    if (access("/sys/class/net/wlan0", F_OK) != 0) return 1;
    FILE *f = fopen(GPIO_WIFI_PWR, "r");
    if (!f) return 0;
    int ch = fgetc(f);
    fclose(f);
    return (ch == '0');
}

static void write_wifi_status(const char *st) {
    FILE *f = fopen(WIFI_STATUS_FILE, "w");
    if (f) {
        fprintf(f, "%s\n", st);
        fclose(f);
    }
}

static void set_wifi_led(int solid) {
    if (!real_system) {
        pthread_mutex_lock(&g_sym_mutex);
        if (!real_system) real_system = dlsym(RTLD_NEXT, "system");
        pthread_mutex_unlock(&g_sym_mutex);
    }
    write_wifi_status(solid ? "on" : "slow_flash");
    if (real_system) {
        real_system(solid ? LED_CMD_ON : LED_CMD_SLOW);
    }
}

int system(const char *command) {
    if (!real_system) {
        pthread_mutex_lock(&g_sym_mutex);
        if (!real_system) real_system = dlsym(RTLD_NEXT, "system");
        pthread_mutex_unlock(&g_sym_mutex);
    }
    if (!command || !is_medusa_process()) {
        return real_system ? real_system(command) : -1;
    }

    // 1. Block destructive disable_wifi.sh (CH_SIG 10 low-battery & on-dock switch toggle)
    if (strstr(command, "disable_wifi.sh") != NULL) {
        log_wg("[WIFI-GUARD] Blocked disable_wifi.sh execution: %s\n", command);
        syslog(LOG_WARNING, "[WIFI-GUARD] Blocked disable_wifi.sh to preserve wlan0");
        return 0;
    }

    // 2. Gate xmpp_server_status.sh LED state on real wlan0 station IPv4 connectivity
    if (strstr(command, "xmpp_server_status.sh") != NULL) {
        int st = get_wlan0_state();
        if (st == WLAN_AP) {
            return 0; // Preserve fast-flash LED during AP provisioning
        }
        if (strstr(command, "STATUS=online") != NULL && st != WLAN_STA_ONLINE) {
            log_wg("[WIFI-GUARD] Loopback XMPP online while wlan0 down; forcing slow_flash LED\n");
            set_wifi_led(0);
            return 0;
        }
        if (strstr(command, "STATUS=offline") != NULL && st != WLAN_STA_ONLINE) {
            set_wifi_led(0);
            return 0;
        }
    }

    return real_system(command);
}

uint32_t MDSElemCastMsg(int ctx, const char *msg_type, void *payload) {
    if (!real_MDSElemCastMsg) {
        pthread_mutex_lock(&g_sym_mutex);
        if (!real_MDSElemCastMsg) real_MDSElemCastMsg = dlsym(RTLD_NEXT, "MDSElemCastMsg");
        pthread_mutex_unlock(&g_sym_mutex);
        if (!real_MDSElemCastMsg) return 0;
    }

    if (msg_type) {
        // Sync LED solid immediately when plug_network (nt0) reports wlan0 online ("xcRestart")
        if (strcmp(msg_type, "xcRestart") == 0) {
            log_wg("[WIFI-GUARD] wlan0 online notification (xcRestart); setting WiFi LED solid\n");
            set_wifi_led(1);
        }
        // Prevent CH_SIG 10 / KEY (0,3) follow-up LED(2,0,0) from blanking WiFi LED while online
        else if (strcmp(msg_type, "LED") == 0 && payload != NULL) {
            int *led = (int *)payload;
            uint8_t val = *((uint8_t *)payload + 8);
            if (led[0] == 2 && led[1] == 0 && val == 0 && get_wlan0_state() == WLAN_STA_ONLINE) {
                log_wg("[WIFI-GUARD] Suppressed LED(2,0,0) turn-off while wlan0 is online\n");
                return 0;
            }
        }
    }

    return real_MDSElemCastMsg(ctx, msg_type, payload);
}

void onRobotEnterCharger(int ctx) {
    if (!real_onRobotEnterCharger) {
        pthread_mutex_lock(&g_sym_mutex);
        if (!g_periph_handle) {
            g_periph_handle = dlopen(MMI_PERIPH_PLUGIN, RTLD_NOLOAD | RTLD_NOW);
            if (!g_periph_handle) g_periph_handle = dlopen(MMI_PERIPH_PLUGIN, RTLD_NOW);
        }
        void *sym = g_periph_handle ? dlsym(g_periph_handle, "onRobotEnterCharger") : NULL;
        if (!sym || sym == (void *)onRobotEnterCharger) {
            sym = dlsym(RTLD_NEXT, "onRobotEnterCharger");
        }
        real_onRobotEnterCharger = sym;
        pthread_mutex_unlock(&g_sym_mutex);
    }

    if (real_onRobotEnterCharger && real_onRobotEnterCharger != onRobotEnterCharger) {
        real_onRobotEnterCharger(ctx);
    }

    if (get_wlan0_state() != WLAN_AP && is_wifi_hw_disabled()) {
        log_wg("[WIFI-GUARD] Robot docked with wlan0/gpio274 disabled; triggering WiFi recovery\n");
        syslog(LOG_NOTICE, "[WIFI-GUARD] Restoring gpio274 and WiFi daemons on dock entry");
        if (!real_system) {
            pthread_mutex_lock(&g_sym_mutex);
            if (!real_system) real_system = dlsym(RTLD_NEXT, "system");
            pthread_mutex_unlock(&g_sym_mutex);
        }
        if (real_system) real_system(WIFI_RECOVER_CMD);
    }
}

__attribute__((constructor))
static void wifi_guard_init(void) {
    if (is_medusa_process()) {
        log_wg("[WIFI-GUARD] Initialized libwifiguard in medusa (PID %d, wlan_state=%d)\n",
               getpid(), get_wlan0_state());
        syslog(LOG_INFO, "[WIFI-GUARD] Initialized libwifiguard in medusa (PID %d)", getpid());
    }
}
