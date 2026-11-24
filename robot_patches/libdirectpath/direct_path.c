#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <stdarg.h>
#include <string.h>
#include <stdint.h>
#include <unistd.h>
#include <dlfcn.h>
#include <pthread.h>

#define LOG_FILE "/tmp/direct_path.log"
#define ERGODIC_PLUGIN "/usr/lib/medusa/plug_medusa_plugin_task_ergodic.so"

#define GRID_W 400
#define GRID_H 400
#define GRID_CELLS (GRID_W * GRID_H)

/* Hard stack limits in stock setAstartWalkPoint02 (0x25b68.c):
 *   int aiStack_fcc[1000]   -> max 999 path cells
 *   float afStack_12ec[200] -> max 100 (x, y) waypoints
 */
#define MAX_RAW_ASTAR_NODES 999
#define MAX_WAYPOINTS       100

/* Integer A* step costs (octile, 100mm/cell grid, OZMO 930 radius ~177mm) */
#define COST_CARDINAL       10
#define COST_DIAGONAL       14
#define PENALTY_CLR_1      250   /* 100mm from obstacle: blocked in pass 0, emergency-only in pass 1 */
#define PENALTY_CLR_2       80   /* 200mm from obstacle: doorway squeeze only */
#define PENALTY_CLR_3       20   /* 300mm from obstacle: keeps DP corners off walls */

typedef int (*astar_exe_G2G_NextRoom02_fn)(
    float dest_x, float dest_y,
    int width, int height,
    int start_mid, int target_mid,
    int start_grid_x, int start_grid_y,
    int target_grid_x, int target_grid_y,
    void *costmap
);

typedef int (*ast_astarInt400_fn)(
    int width, int height,
    int start_grid_x, int start_grid_y,
    int target_grid_x, int target_grid_y,
    void *costmap
);

typedef int (*findpathInt400_fn)(
    int width, int height,
    int sx, int sy,
    int gx, int gy,
    int *out_count,
    void *costmap
);

typedef void (*setAstartWalkPoint02_fn)(float dest_x, float dest_y);

static astar_exe_G2G_NextRoom02_fn real_astar_exe_G2G_NextRoom02 = NULL;
static ast_astarInt400_fn          real_ast_astarInt400          = NULL;
static findpathInt400_fn           real_findpathInt400           = NULL;
static setAstartWalkPoint02_fn     real_setAstartWalkPoint02     = NULL;
static int                        *p_ast_astarPathNodeCount      = NULL;
static int32_t                   **p_ast_path                    = NULL;

static pthread_mutex_t g_log_mutex   = PTHREAD_MUTEX_INITIALIZER;
static pthread_mutex_t g_init_mutex  = PTHREAD_MUTEX_INITIALIZER;
static pthread_mutex_t g_astar_mutex = PTHREAD_MUTEX_INITIALIZER;
static int g_initialized = 0;

typedef struct {
    uint32_t f;
    int32_t  idx;
} HeapNode;

typedef struct {
    uint32_t *g_cost;
    int32_t  *parent;
    int32_t  *heap_pos;   /* >=0: index in heap, -2: closed */
    uint32_t *seen_gen;
    uint8_t  *clr_cache;  /* 0=obstacle, 1=100mm, 2=200mm, 3=300mm, 4=>=400mm */
    uint32_t *clr_gen;
    HeapNode *heap;
    int32_t  *tmp_path;
    uint32_t  cur_gen;
    uint32_t  cur_clr_gen;
    int       heap_size;
} AStarWorkspace;

static AStarWorkspace g_ws = {0};

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
        void *handle = dlopen(ERGODIC_PLUGIN, RTLD_NOLOAD | RTLD_NOW);
        if (!handle) {
            handle = dlopen(ERGODIC_PLUGIN, RTLD_NOW);
        }

        if (handle) {
            real_astar_exe_G2G_NextRoom02 =
                (astar_exe_G2G_NextRoom02_fn)dlsym(handle, "astar_exe_G2G_NextRoom02");
            real_ast_astarInt400 =
                (ast_astarInt400_fn)dlsym(handle, "ast_astarInt400");
            real_findpathInt400 =
                (findpathInt400_fn)dlsym(handle, "findpathInt400");
            real_setAstartWalkPoint02 =
                (setAstartWalkPoint02_fn)dlsym(handle, "setAstartWalkPoint02");
            p_ast_astarPathNodeCount =
                (int *)dlsym(handle, "ast_astarPathNodeCount");
            p_ast_path =
                (int32_t **)dlsym(handle, "ast_path");

            log_msg("[DIRECT-PATH] Symbols: G2G=%p astar=%p findpath=%p walk=%p nodeCnt=%p ast_path=%p\n",
                    (void *)real_astar_exe_G2G_NextRoom02,
                    (void *)real_ast_astarInt400,
                    (void *)real_findpathInt400,
                    (void *)real_setAstartWalkPoint02,
                    (void *)p_ast_astarPathNodeCount,
                    (void *)p_ast_path);
        } else {
            log_msg("[DIRECT-PATH] ERROR: dlopen(%s) failed: %s\n", ERGODIC_PLUGIN, dlerror());
        }

        if (!real_astar_exe_G2G_NextRoom02) {
            real_astar_exe_G2G_NextRoom02 =
                (astar_exe_G2G_NextRoom02_fn)dlsym(RTLD_NEXT, "astar_exe_G2G_NextRoom02");
        }
        if (!real_findpathInt400) {
            real_findpathInt400 =
                (findpathInt400_fn)dlsym(RTLD_NEXT, "findpathInt400");
        }

        g_initialized = 1;
    }
    pthread_mutex_unlock(&g_init_mutex);
}

static int ensure_workspace(void) {
    if (g_ws.g_cost) return 1;

    g_ws.g_cost    = (uint32_t *)malloc(GRID_CELLS * sizeof(uint32_t));
    g_ws.parent    = (int32_t  *)malloc(GRID_CELLS * sizeof(int32_t));
    g_ws.heap_pos  = (int32_t  *)malloc(GRID_CELLS * sizeof(int32_t));
    g_ws.seen_gen  = (uint32_t *)calloc(GRID_CELLS, sizeof(uint32_t));
    g_ws.clr_cache = (uint8_t  *)malloc(GRID_CELLS * sizeof(uint8_t));
    g_ws.clr_gen   = (uint32_t *)calloc(GRID_CELLS, sizeof(uint32_t));
    g_ws.heap      = (HeapNode *)malloc(GRID_CELLS * sizeof(HeapNode));
    g_ws.tmp_path  = (int32_t  *)malloc(GRID_CELLS * sizeof(int32_t));

    if (!g_ws.g_cost || !g_ws.parent || !g_ws.heap_pos || !g_ws.seen_gen ||
        !g_ws.clr_cache || !g_ws.clr_gen || !g_ws.heap || !g_ws.tmp_path) {
        free(g_ws.g_cost);    g_ws.g_cost = NULL;
        free(g_ws.parent);    g_ws.parent = NULL;
        free(g_ws.heap_pos);  g_ws.heap_pos = NULL;
        free(g_ws.seen_gen);  g_ws.seen_gen = NULL;
        free(g_ws.clr_cache); g_ws.clr_cache = NULL;
        free(g_ws.clr_gen);   g_ws.clr_gen = NULL;
        free(g_ws.heap);      g_ws.heap = NULL;
        free(g_ws.tmp_path);  g_ws.tmp_path = NULL;
        return 0;
    }
    g_ws.cur_gen = 0;
    g_ws.cur_clr_gen = 0;
    return 1;
}

/* Matches libUnderErg::map_isPossibleFreeInt400 (0x19f28.c) */
static inline int cell_is_free(const int32_t *map, int x, int y) {
    if (x < 1 || x >= GRID_W || y < 1 || y >= GRID_H) return 0;
    int32_t v = map[x * GRID_H + y];
    return (v >= 1 && v <= 127);
}

/* Lazy-cached Chebyshev distance (in 100mm cells) to nearest obstacle, capped at 4 */
static uint8_t cell_clearance(const int32_t *map, int x, int y) {
    if (x < 1 || x >= GRID_W || y < 1 || y >= GRID_H) return 0;
    int32_t idx = x * GRID_H + y;
    if (g_ws.clr_gen[idx] == g_ws.cur_clr_gen) {
        return g_ws.clr_cache[idx];
    }

    uint8_t clr = 4;
    if (!cell_is_free(map, x, y)) {
        clr = 0;
    } else {
        for (int r = 1; r <= 3; r++) {
            int hit = 0;
            for (int d = -r; d <= r; d++) {
                if (!cell_is_free(map, x + d, y - r) ||
                    !cell_is_free(map, x + d, y + r) ||
                    !cell_is_free(map, x - r, y + d) ||
                    !cell_is_free(map, x + r, y + d)) {
                    hit = 1;
                    break;
                }
            }
            if (hit) {
                clr = (uint8_t)r;
                break;
            }
        }
    }

    g_ws.clr_gen[idx] = g_ws.cur_clr_gen;
    g_ws.clr_cache[idx] = clr;
    return clr;
}

static inline int near_endpoint(int x, int y, int sx, int sy, int gx, int gy) {
    int dsx = x - sx; if (dsx < 0) dsx = -dsx;
    int dsy = y - sy; if (dsy < 0) dsy = -dsy;
    if (dsx <= 2 && dsy <= 2) return 1;

    int dgx = x - gx; if (dgx < 0) dgx = -dgx;
    int dgy = y - gy; if (dgy < 0) dgy = -dgy;
    return (dgx <= 2 && dgy <= 2);
}

static inline uint32_t octile_h(int x, int y, int gx, int gy) {
    int dx = gx - x; if (dx < 0) dx = -dx;
    int dy = gy - y; if (dy < 0) dy = -dy;
    int mn = (dx < dy) ? dx : dy;
    return (uint32_t)(COST_CARDINAL * (dx + dy) + (COST_DIAGONAL - 2 * COST_CARDINAL) * mn);
}

static inline void heap_swap(int a, int b) {
    HeapNode tmp = g_ws.heap[a];
    g_ws.heap[a] = g_ws.heap[b];
    g_ws.heap[b] = tmp;
    g_ws.heap_pos[g_ws.heap[a].idx] = a;
    g_ws.heap_pos[g_ws.heap[b].idx] = b;
}

static void heap_sift_up(int pos) {
    while (pos > 0) {
        int parent = (pos - 1) >> 1;
        if (g_ws.heap[pos].f >= g_ws.heap[parent].f) break;
        heap_swap(pos, parent);
        pos = parent;
    }
}

static void heap_sift_down(int pos) {
    int n = g_ws.heap_size;
    while (1) {
        int left = (pos << 1) + 1;
        if (left >= n) break;
        int right = left + 1;
        int best = (right < n && g_ws.heap[right].f < g_ws.heap[left].f) ? right : left;
        if (g_ws.heap[pos].f <= g_ws.heap[best].f) break;
        heap_swap(pos, best);
        pos = best;
    }
}

static void heap_push(int32_t idx, uint32_t f) {
    int pos = g_ws.heap_size++;
    g_ws.heap[pos].f = f;
    g_ws.heap[pos].idx = idx;
    g_ws.heap_pos[idx] = pos;
    heap_sift_up(pos);
}

static int32_t heap_pop(void) {
    int32_t top = g_ws.heap[0].idx;
    g_ws.heap_pos[top] = -2;
    g_ws.heap_size--;
    if (g_ws.heap_size > 0) {
        g_ws.heap[0] = g_ws.heap[g_ws.heap_size];
        g_ws.heap_pos[g_ws.heap[0].idx] = 0;
        heap_sift_down(0);
    }
    return top;
}

/* Single A* pass. strict=1 blocks clr==1 (100mm from walls) away from start/goal. */
static int run_astar_pass(const int32_t *map, int sx, int sy, int gx, int gy, int strict) {
    const int32_t start_idx = sx * GRID_H + sy;
    const int32_t goal_idx  = gx * GRID_H + gy;

    if (++g_ws.cur_gen == 0) {
        memset(g_ws.seen_gen, 0, GRID_CELLS * sizeof(uint32_t));
        g_ws.cur_gen = 1;
    }
    const uint32_t gen = g_ws.cur_gen;
    g_ws.heap_size = 0;

    g_ws.seen_gen[start_idx] = gen;
    g_ws.g_cost[start_idx]   = 0;
    g_ws.parent[start_idx]   = -1;
    heap_push(start_idx, octile_h(sx, sy, gx, gy));

    static const int dx[8] = {  0,  0, -1,  1, -1,  1, -1,  1 };
    static const int dy[8] = { -1,  1,  0,  0, -1, -1,  1,  1 };

    while (g_ws.heap_size > 0) {
        int32_t cur = heap_pop();
        if (cur == goal_idx) return 1;

        int cx = cur / GRID_H;
        int cy = cur % GRID_H;
        uint32_t cur_g = g_ws.g_cost[cur];

        for (int d = 0; d < 8; d++) {
            int nx = cx + dx[d];
            int ny = cy + dy[d];
            if (nx < 1 || nx >= GRID_W || ny < 1 || ny >= GRID_H) continue;

            int32_t nidx = nx * GRID_H + ny;
            if (g_ws.seen_gen[nidx] == gen && g_ws.heap_pos[nidx] == -2) {
                continue;
            }

            uint8_t clr = cell_clearance(map, nx, ny);
            if (nidx != goal_idx) {
                if (clr == 0) continue;
                if (strict && clr == 1 && !near_endpoint(nx, ny, sx, sy, gx, gy)) {
                    continue;
                }
            }

            /* Prevent diagonal corner-cutting using inflated clearance */
            if (d >= 4) {
                uint8_t c1 = cell_clearance(map, cx + dx[d], cy);
                uint8_t c2 = cell_clearance(map, cx, cy + dy[d]);
                if (c1 == 0 || c2 == 0) continue;
                if (strict && (c1 == 1 || c2 == 1) && !near_endpoint(nx, ny, sx, sy, gx, gy)) {
                    continue;
                }
            }

            uint32_t step = (d < 4) ? COST_CARDINAL : COST_DIAGONAL;
            if (nidx != goal_idx) {
                if (clr == 1)      step += PENALTY_CLR_1;
                else if (clr == 2) step += PENALTY_CLR_2;
                else if (clr == 3) step += PENALTY_CLR_3;
            }

            uint32_t new_g = cur_g + step;

            if (g_ws.seen_gen[nidx] != gen) {
                g_ws.seen_gen[nidx] = gen;
                g_ws.g_cost[nidx]   = new_g;
                g_ws.parent[nidx]   = cur;
                heap_push(nidx, new_g + octile_h(nx, ny, gx, gy));
            } else if (new_g < g_ws.g_cost[nidx]) {
                g_ws.g_cost[nidx] = new_g;
                g_ws.parent[nidx] = cur;
                int pos = g_ws.heap_pos[nidx];
                g_ws.heap[pos].f = new_g + octile_h(nx, ny, gx, gy);
                heap_sift_up(pos);
            }
        }
    }
    return 0;
}

/*
 * Drop-in replacement for findpathInt400 (0x24c24.c).
 */
int findpathInt400(int width, int height,
                   int sx, int sy,
                   int gx, int gy,
                   int *out_count,
                   void *costmap)
{
    if (!is_medusa_process()) {
        ensure_symbols();
        if (real_findpathInt400) {
            return real_findpathInt400(width, height, sx, sy, gx, gy, out_count, costmap);
        }
        if (out_count) *out_count = 0;
        return 0;
    }

    ensure_symbols();
    if (!out_count) return 0;

    if (width != GRID_W || height != GRID_H || !costmap || !p_ast_path) {
        if (real_findpathInt400) {
            return real_findpathInt400(width, height, sx, sy, gx, gy, out_count, costmap);
        }
        *out_count = 0;
        return 0;
    }

    if (sx < 1 || sx >= GRID_W || sy < 1 || sy >= GRID_H ||
        gx < 1 || gx >= GRID_W || gy < 1 || gy >= GRID_H) {
        *out_count = 0;
        return 0;
    }

    pthread_mutex_lock(&g_astar_mutex);

    if (!ensure_workspace()) {
        pthread_mutex_unlock(&g_astar_mutex);
        if (real_findpathInt400) {
            return real_findpathInt400(width, height, sx, sy, gx, gy, out_count, costmap);
        }
        *out_count = 0;
        return 0;
    }

    /* Invalidate clearance cache once per findpathInt400 call (costmap may have updated) */
    if (++g_ws.cur_clr_gen == 0) {
        memset(g_ws.clr_gen, 0, GRID_CELLS * sizeof(uint32_t));
        g_ws.cur_clr_gen = 1;
    }

    const int32_t *map = (const int32_t *)costmap;

    /* Pass 0: strict robot-radius inflation; Pass 1: relaxed fallback for tight doorways */
    int found = run_astar_pass(map, sx, sy, gx, gy, 1);
    if (!found) {
        found = run_astar_pass(map, sx, sy, gx, gy, 0);
    }

    if (!found) {
        *out_count = 0;
        pthread_mutex_unlock(&g_astar_mutex);
        return 0;
    }

    const int32_t goal_idx = gx * GRID_H + gy;
    int len = 0;
    for (int32_t cur = goal_idx; cur != -1 && len < GRID_CELLS; cur = g_ws.parent[cur]) {
        g_ws.tmp_path[len++] = cur;
    }

    int out_len = (len <= MAX_RAW_ASTAR_NODES) ? len : MAX_RAW_ASTAR_NODES;
    int32_t *buf = (int32_t *)malloc((size_t)(out_len + 1) * sizeof(int32_t));
    if (!buf) {
        *out_count = 0;
        pthread_mutex_unlock(&g_astar_mutex);
        return 0;
    }

    if (len <= MAX_RAW_ASTAR_NODES) {
        memcpy(buf, g_ws.tmp_path, (size_t)len * sizeof(int32_t));
    } else {
        for (int i = 0; i < out_len - 1; i++) {
            int src_i = (int)(((int64_t)i * (len - 1)) / (out_len - 1));
            buf[i] = g_ws.tmp_path[src_i];
        }
        buf[out_len - 1] = g_ws.tmp_path[len - 1];
    }
    buf[out_len] = -1;

    if (*p_ast_path) {
        free(*p_ast_path);
    }
    *p_ast_path = buf;
    *out_count  = out_len + 1;

    pthread_mutex_unlock(&g_astar_mutex);
    return 0;
}

/*
 * Intercept Gate-to-Gate planner (0x251c0.c) to bypass synthetic room doorways.
 */
int astar_exe_G2G_NextRoom02(float dest_x, float dest_y,
                             int width, int height,
                             int start_mid, int target_mid,
                             int start_grid_x, int start_grid_y,
                             int target_grid_x, int target_grid_y,
                             void *costmap)
{
    if (!is_medusa_process()) {
        ensure_symbols();
        if (real_astar_exe_G2G_NextRoom02) {
            return real_astar_exe_G2G_NextRoom02(dest_x, dest_y, width, height,
                                                 start_mid, target_mid,
                                                 start_grid_x, start_grid_y,
                                                 target_grid_x, target_grid_y, costmap);
        }
        return 0;
    }

    ensure_symbols();

    log_msg("[DIRECT-PATH] G2G call: room %d (%d,%d) -> room %d (%d,%d), target=(%.1f,%.1f)\n",
            start_mid, start_grid_x, start_grid_y,
            target_mid, target_grid_x, target_grid_y,
            dest_x, dest_y);

    if (real_ast_astarInt400 && real_setAstartWalkPoint02 && costmap) {
        int raw_nodes = real_ast_astarInt400(width, height,
                                             start_grid_x, start_grid_y,
                                             target_grid_x, target_grid_y,
                                             costmap);

        if (raw_nodes > 0 && raw_nodes <= MAX_RAW_ASTAR_NODES + 1) {
            real_setAstartWalkPoint02(dest_x, dest_y);

            int wp_count = (p_ast_astarPathNodeCount) ? *p_ast_astarPathNodeCount : 1;
            if (wp_count >= 1 && wp_count <= MAX_WAYPOINTS) {
                log_msg("[DIRECT-PATH] Direct 8-dir inflated A* OK: raw_nodes=%d, waypoints=%d\n",
                        raw_nodes, wp_count);
                return wp_count;
            }

            log_msg("[DIRECT-PATH] Invalid waypoint count (%d), falling back to stock G2G\n", wp_count);
        } else {
            log_msg("[DIRECT-PATH] Direct A* returned %d, falling back to stock G2G\n", raw_nodes);
        }
    } else {
        log_msg("[DIRECT-PATH] Missing symbols or null costmap, falling back to stock G2G\n");
    }

    if (real_astar_exe_G2G_NextRoom02) {
        return real_astar_exe_G2G_NextRoom02(dest_x, dest_y, width, height,
                                             start_mid, target_mid,
                                             start_grid_x, start_grid_y,
                                             target_grid_x, target_grid_y, costmap);
    }

    log_msg("[DIRECT-PATH] ERROR: real_astar_exe_G2G_NextRoom02 is NULL!\n");
    return 0;
}

__attribute__((constructor))
static void libdirectpath_init(void) {
    if (is_medusa_process()) {
        log_msg("[DIRECT-PATH] Initialized libdirectpath (inflated 8-dir A*) in PID %d\n", getpid());
    }
}