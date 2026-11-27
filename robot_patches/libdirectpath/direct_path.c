#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <stdarg.h>
#include <string.h>
#include <stdint.h>
#include <unistd.h>
#include <dlfcn.h>
#include <pthread.h>
#include <time.h>

#define LOG_FILE "/tmp/direct_path.log"
#define ERGODIC_PLUGIN "/usr/lib/medusa/plug_medusa_plugin_task_ergodic.so"

#define GRID_W 400
#define GRID_H 400
#define GRID_CELLS (GRID_W * GRID_H)

/* Hard stack limits in stock setAstartWalkPoint02 & Douglas-Peucker simplifier:
 *   int aiStack_fcc[1000]   -> max 999 path cells
 *   float afStack_12ec[200] -> max 100 (x, y) floats / ast_astarPathNode (.bss 200B -> 50 ints)
 */
#define MAX_RAW_ASTAR_NODES 999
#define MAX_WAYPOINTS       50
#define MAX_ASTAR_POPS      120000

/* Integer A* step costs (octile, 100mm/cell grid, OZMO 930 radius ~177mm) */
#define COST_CARDINAL       10
#define COST_DIAGONAL       14
#define PENALTY_CLR_1       160  /* 100mm from obstacle: tight doorway squeeze only */
#define PENALTY_CLR_2       50   /* 200mm from obstacle: gentle wall margin */
#define PENALTY_CLR_3       15   /* 300mm from obstacle: keeps DP corners off walls */
#define PENALTY_DIAG_CORNER 20   /* Extra margin when cutting past a clr==1 diagonal corner */

/* Gentler penalties when ast_astarInt400WitCoef runs with tight coef <= 0.6 (e.g. 0.5f explore/dock) */
#define COEF_LOW_PEN_1      20
#define COEF_LOW_PEN_2      5
#define COEF_LOW_PEN_3      0

/* Spatial stuck heat-map (inspired by Ozmo 950 librobot_stuck_handler) */
#define MAX_HEAT_ZONES      32
#define HEAT_RADIUS_CELLS   2    /* 200mm radius around stuck/bump-trap spot */
#define HEAT_DECAY_SEC      600  /* 10-minute linear decay */
#define HEAT_PENALTY_UNIT   60   /* Extra A* step cost per heat level */

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

typedef int (*ast_astarInt400WitCoef_fn)(
    float coef,
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
static ast_astarInt400WitCoef_fn   real_ast_astarInt400WitCoef   = NULL;
static findpathInt400_fn           real_findpathInt400           = NULL;
static setAstartWalkPoint02_fn     real_setAstartWalkPoint02     = NULL;
static int                        *p_ast_astarPathNodeCount      = NULL;
static int32_t                   **p_ast_path                    = NULL;

static pthread_mutex_t g_log_mutex   = PTHREAD_MUTEX_INITIALIZER;
static pthread_mutex_t g_init_mutex  = PTHREAD_MUTEX_INITIALIZER;
static pthread_mutex_t g_astar_mutex = PTHREAD_MUTEX_INITIALIZER;
static int g_initialized = 0;

/* TLS context propagated from ast_astarInt400WitCoef / G2G into findpathInt400 */
static __thread int   g_witcoef_depth = 0;
static __thread float g_witcoef_val   = 2.0f;
static __thread int   g_g2g_depth     = 0;

typedef struct {
    int     gx;
    int     gy;
    uint8_t weight;
    time_t  ts;
} StuckHeatZone;

static pthread_mutex_t g_heat_mutex = PTHREAD_MUTEX_INITIALIZER;
static StuckHeatZone   g_heat_zones[MAX_HEAT_ZONES];
static int             g_heat_count = 0;
static StuckHeatZone   g_heat_snap[MAX_HEAT_ZONES];
static int             g_heat_snap_count = 0;
static time_t          g_heat_snap_now = 0;

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
            real_ast_astarInt400WitCoef =
                (ast_astarInt400WitCoef_fn)dlsym(handle, "ast_astarInt400WitCoef");
            real_findpathInt400 =
                (findpathInt400_fn)dlsym(handle, "findpathInt400");
            real_setAstartWalkPoint02 =
                (setAstartWalkPoint02_fn)dlsym(handle, "setAstartWalkPoint02");
            p_ast_astarPathNodeCount =
                (int *)dlsym(handle, "ast_astarPathNodeCount");
            p_ast_path =
                (int32_t **)dlsym(handle, "ast_path");

            log_msg("[DIRECT-PATH] Symbols: G2G=%p astar=%p witcoef=%p findpath=%p walk=%p nodeCnt=%p ast_path=%p\n",
                    (void *)real_astar_exe_G2G_NextRoom02,
                    (void *)real_ast_astarInt400,
                    (void *)real_ast_astarInt400WitCoef,
                    (void *)real_findpathInt400,
                    (void *)real_setAstartWalkPoint02,
                    (void *)p_ast_astarPathNodeCount,
                    (void *)p_ast_path);
        } else {
            log_msg("[DIRECT-PATH] ERROR: dlopen(%s) failed: %s\n", ERGODIC_PLUGIN, dlerror());
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
    if (x < 0 || x >= GRID_W || y < 0 || y >= GRID_H) return 0;
    int32_t v = map[x * GRID_H + y];
    return (v >= 1 && v <= 127);
}

/* Lazy-cached Chebyshev distance (in 100mm cells) to nearest obstacle, capped at 4 */
static uint8_t cell_clearance(const int32_t *map, int x, int y) {
    if (x < 0 || x >= GRID_W || y < 0 || y >= GRID_H) return 0;
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

/* Exported bridge called by libnavboost when repeated bump clusters or detraps occur */
void nav_record_stuck_cell(int gx, int gy, uint8_t weight) {
    if (gx < 0 || gx >= GRID_W || gy < 0 || gy >= GRID_H || weight == 0) return;

    time_t now = time(NULL);
    pthread_mutex_lock(&g_heat_mutex);

    /* Merge into nearby active zone if within HEAT_RADIUS_CELLS */
    for (int i = 0; i < g_heat_count; i++) {
        if ((now - g_heat_zones[i].ts) >= HEAT_DECAY_SEC) continue;
        int dx = g_heat_zones[i].gx - gx; if (dx < 0) dx = -dx;
        int dy = g_heat_zones[i].gy - gy; if (dy < 0) dy = -dy;
        if (dx <= HEAT_RADIUS_CELLS && dy <= HEAT_RADIUS_CELLS) {
            unsigned int w = (unsigned int)g_heat_zones[i].weight + (unsigned int)weight;
            g_heat_zones[i].weight = (w > 6) ? 6 : (uint8_t)w;
            g_heat_zones[i].ts = now;
            pthread_mutex_unlock(&g_heat_mutex);
            log_msg("[DIRECT-PATH] Heat zone updated at (%d,%d) weight=%u\n",
                    g_heat_zones[i].gx, g_heat_zones[i].gy, g_heat_zones[i].weight);
            return;
        }
    }

    /* Compact expired zones */
    int write = 0;
    for (int i = 0; i < g_heat_count; i++) {
        if ((now - g_heat_zones[i].ts) < HEAT_DECAY_SEC) {
            if (write != i) g_heat_zones[write] = g_heat_zones[i];
            write++;
        }
    }
    g_heat_count = write;

    /* Evict oldest if full */
    if (g_heat_count >= MAX_HEAT_ZONES) {
        memmove(&g_heat_zones[0], &g_heat_zones[1], (MAX_HEAT_ZONES - 1) * sizeof(StuckHeatZone));
        g_heat_count = MAX_HEAT_ZONES - 1;
    }

    g_heat_zones[g_heat_count].gx = gx;
    g_heat_zones[g_heat_count].gy = gy;
    g_heat_zones[g_heat_count].weight = (weight > 6) ? 6 : weight;
    g_heat_zones[g_heat_count].ts = now;
    g_heat_count++;

    pthread_mutex_unlock(&g_heat_mutex);
    log_msg("[DIRECT-PATH] Heat zone added at (%d,%d) weight=%u (active=%d)\n",
            gx, gy, weight, g_heat_count);
}

static void snapshot_heat_zones(void) {
    time_t now = time(NULL);
    pthread_mutex_lock(&g_heat_mutex);
    int n = 0;
    for (int i = 0; i < g_heat_count; i++) {
        if ((now - g_heat_zones[i].ts) < HEAT_DECAY_SEC) {
            g_heat_snap[n++] = g_heat_zones[i];
        }
    }
    g_heat_snap_count = n;
    g_heat_snap_now = now;
    pthread_mutex_unlock(&g_heat_mutex);
}

static inline uint32_t cell_stuck_heat(int x, int y) {
    if (g_heat_snap_count == 0) return 0;
    uint32_t total = 0;
    for (int i = 0; i < g_heat_snap_count; i++) {
        int dx = x - g_heat_snap[i].gx; if (dx < 0) dx = -dx;
        if (dx > HEAT_RADIUS_CELLS) continue;
        int dy = y - g_heat_snap[i].gy; if (dy < 0) dy = -dy;
        if (dy > HEAT_RADIUS_CELLS) continue;
        time_t age = g_heat_snap_now - g_heat_snap[i].ts;
        if (age < 0) age = 0;
        if (age >= HEAT_DECAY_SEC) continue;
        uint32_t remain = (uint32_t)(HEAT_DECAY_SEC - age);
        uint32_t eff = ((uint32_t)g_heat_snap[i].weight * remain + (HEAT_DECAY_SEC - 1)) / HEAT_DECAY_SEC;
        if (eff == 0) eff = 1;
        total += eff;
    }
    return total;
}

/* Spiral search for nearest free cell (preferring higher obstacle clearance) */
static int snap_to_nearest_free(const int32_t *map, int *io_x, int *io_y, int max_r) {
    int ox = *io_x, oy = *io_y;
    if (cell_is_free(map, ox, oy)) return 1;

    int best_x = -1, best_y = -1;
    uint8_t best_clr = 0;
    int best_d2 = 0x7fffffff;

    for (int r = 1; r <= max_r; r++) {
        for (int dx = -r; dx <= r; dx++) {
            for (int dy = -r; dy <= r; dy++) {
                if (dx != -r && dx != r && dy != -r && dy != r) continue;
                int nx = ox + dx, ny = oy + dy;
                if (!cell_is_free(map, nx, ny)) continue;
                uint8_t clr = cell_clearance(map, nx, ny);
                int d2 = dx * dx + dy * dy;
                if (clr > best_clr || (clr == best_clr && d2 < best_d2)) {
                    best_clr = clr;
                    best_d2 = d2;
                    best_x = nx;
                    best_y = ny;
                }
            }
        }
        if (best_x != -1) {
            *io_x = best_x;
            *io_y = best_y;
            return 1;
        }
    }
    return 0;
}

/* Supercover Bresenham LOS check verifying obstacle clearance & low stuck-heat */
static int line_has_clearance(const int32_t *map, int x0, int y0, int x1, int y1, uint8_t min_clr) {
    int dx = x1 - x0;
    int dy = y1 - y0;
    int sx = (dx >= 0) ? 1 : -1;
    int sy = (dy >= 0) ? 1 : -1;
    if (dx < 0) dx = -dx;
    if (dy < 0) dy = -dy;

    int x = x0, y = y0;
    int err = dx - dy;

    while (1) {
        if (x != x0 || y != y0) {
            if (x != x1 || y != y1) {
                if (cell_clearance(map, x, y) < min_clr) return 0;
                if (cell_stuck_heat(x, y) >= 2) return 0;
            }
        }
        if (x == x1 && y == y1) break;

        int e2 = err << 1;
        int step_x = (e2 > -dy);
        int step_y = (e2 < dx);
        if (step_x && step_y) {
            /* Supercover: check both orthogonal corner cells to prevent corner clipping */
            if (cell_clearance(map, x + sx, y) < min_clr) return 0;
            if (cell_clearance(map, x, y + sy) < min_clr) return 0;
        }
        if (step_x) { err -= dy; x += sx; }
        if (step_y) { err += dx; y += sy; }
    }
    return 1;
}

/*
 * Clearance-aware greedy Line-of-Sight (LOS) string-pulling (inspired by Ozmo 950
 * librobot_jps_cxx smooth_path + Theta*): collapses choppy 45/90-deg grid staircases
 * into clean any-angle straight lines while guaranteeing safe wall clearance.
 */
static int smooth_path_los(const int32_t *map, int32_t *path, int len, uint8_t min_clr) {
    if (len <= 2) return len;

    int write = 0;
    int anchor = 0;
    path[write++] = path[0];

    while (anchor < len - 1) {
        int ax = path[anchor] / GRID_H;
        int ay = path[anchor] % GRID_H;
        int next = anchor + 1;

        for (int j = len - 1; j > anchor + 1; j--) {
            int bx = path[j] / GRID_H;
            int by = path[j] % GRID_H;
            if (line_has_clearance(map, ax, ay, bx, by, min_clr)) {
                next = j;
                break;
            }
        }

        path[write++] = path[next];
        anchor = next;
    }

    return write;
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

/* Single-pass soft-inflated 8-dir A* bounded by MAX_ASTAR_POPS. */
static int run_astar_pass(const int32_t *map, int sx, int sy, int gx, int gy,
                          uint32_t p1, uint32_t p2, uint32_t p3) {
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

    int pops = 0;
    while (g_ws.heap_size > 0) {
        if (++pops > MAX_ASTAR_POPS) {
            return 0;
        }
        int32_t cur = heap_pop();
        if (cur == goal_idx) return 1;

        int cx = cur / GRID_H;
        int cy = cur % GRID_H;
        uint32_t cur_g = g_ws.g_cost[cur];

        for (int d = 0; d < 8; d++) {
            int nx = cx + dx[d];
            int ny = cy + dy[d];
            if (nx < 0 || nx >= GRID_W || ny < 0 || ny >= GRID_H) continue;

            int32_t nidx = nx * GRID_H + ny;
            if (g_ws.seen_gen[nidx] == gen && g_ws.heap_pos[nidx] == -2) {
                continue;
            }

            uint8_t clr = cell_clearance(map, nx, ny);
            if (clr == 0) continue;

            /* Prevent diagonal corner-cutting using inflated clearance */
            uint32_t diag_extra = 0;
            if (d >= 4) {
                uint8_t c1 = cell_clearance(map, cx + dx[d], cy);
                uint8_t c2 = cell_clearance(map, cx, cy + dy[d]);
                if (c1 == 0 || c2 == 0) continue;
                if (p1 > COEF_LOW_PEN_1 && (c1 == 1 || c2 == 1)) {
                    diag_extra = PENALTY_DIAG_CORNER;
                }
            }

            uint32_t step = (d < 4) ? COST_CARDINAL : (COST_DIAGONAL + diag_extra);
            if (clr == 1)      step += p1;
            else if (clr == 2) step += p2;
            else if (clr == 3) step += p3;

            uint32_t heat = cell_stuck_heat(nx, ny);
            if (heat > 0) {
                step += heat * HEAT_PENALTY_UNIT;
            }

            uint32_t new_g = cur_g + step;

            if (g_ws.seen_gen[nidx] != gen) {
                g_ws.seen_gen[nidx] = gen;
                g_ws.g_cost[nidx]   = new_g;
                g_ws.parent[nidx]   = cur;
                heap_push(nidx, new_g + octile_h(nx, ny, gx, gy));
            } else if (new_g < g_ws.g_cost[nidx] && g_ws.heap_pos[nidx] >= 0) {
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

/* Collapse strictly colinear interior steps while preserving endpoints and turn vertices. */
static int compress_colinear_path(int32_t *path, int len) {
    if (len <= 2) return len;
    int write = 1;
    for (int i = 1; i < len - 1; i++) {
        int px = path[write - 1] / GRID_H, py = path[write - 1] % GRID_H;
        int cx = path[i] / GRID_H,         cy = path[i] % GRID_H;
        int nx = path[i + 1] / GRID_H,     ny = path[i + 1] % GRID_H;
        int dx1 = cx - px, dy1 = cy - py;
        int dx2 = nx - cx, dy2 = ny - cy;
        if (dx1 * dy2 == dy1 * dx2 && (dx1 * dx2 + dy1 * dy2) > 0) {
            continue;
        }
        path[write++] = path[i];
    }
    path[write++] = path[len - 1];
    return write;
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

    if (sx < 0 || sx >= GRID_W || sy < 0 || sy >= GRID_H ||
        gx < 0 || gx >= GRID_W || gy < 0 || gy >= GRID_H) {
        *out_count = 0;
        return 0;
    }

    const int32_t *map = (const int32_t *)costmap;

    if (sx == gx && sy == gy) {
        int32_t *buf = (int32_t *)malloc(2 * sizeof(int32_t));
        if (!buf) {
            *out_count = 0;
            return 0;
        }
        buf[0] = sx * GRID_H + sy;
        buf[1] = -1;
        pthread_mutex_lock(&g_astar_mutex);
        if (*p_ast_path) {
            free(*p_ast_path);
        }
        *p_ast_path = buf;
        *out_count = 2;
        pthread_mutex_unlock(&g_astar_mutex);
        return 0;
    }

    /* Upfront goal check avoids full-map flood on unreachable/obstacle goals during zig-zag planning.
     * During cross-room G2G or witCoef (return-to-dock / reloc / bump recovery), allow small spiral snap. */
    int allow_goal_snap = (g_witcoef_depth > 0 || g_g2g_depth > 0);
    if (!allow_goal_snap && !cell_is_free(map, gx, gy)) {
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

    snapshot_heat_zones();

    /* Snap wedged start cell (or G2G/return goal cell) to nearest free cell */
    if (!cell_is_free(map, sx, sy)) {
        if (!snap_to_nearest_free(map, &sx, &sy, 3)) {
            *out_count = 0;
            pthread_mutex_unlock(&g_astar_mutex);
            return 0;
        }
    }
    if (allow_goal_snap && !cell_is_free(map, gx, gy)) {
        if (!snap_to_nearest_free(map, &gx, &gy, 2)) {
            *out_count = 0;
            pthread_mutex_unlock(&g_astar_mutex);
            return 0;
        }
    }

    uint32_t p1 = PENALTY_CLR_1, p2 = PENALTY_CLR_2, p3 = PENALTY_CLR_3;
    if (g_witcoef_depth > 0 && g_witcoef_val <= 0.6f) {
        p1 = COEF_LOW_PEN_1;
        p2 = COEF_LOW_PEN_2;
        p3 = COEF_LOW_PEN_3;
    }

    if (!run_astar_pass(map, sx, sy, gx, gy, p1, p2, p3)) {
        *out_count = 0;
        pthread_mutex_unlock(&g_astar_mutex);
        return 0;
    }

    const int32_t goal_idx = gx * GRID_H + gy;
    int len = 0;
    for (int32_t cur = goal_idx; cur != -1 && len < GRID_CELLS; cur = g_ws.parent[cur]) {
        g_ws.tmp_path[len++] = cur;
    }

    /* Clearance-aware any-angle LOS string-pulling (eliminates 45/90-deg stair-stepping) */
    if (len > 2) {
        uint8_t min_clr = (p1 <= COEF_LOW_PEN_1) ? 1 : 2;
        len = smooth_path_los(map, g_ws.tmp_path, len, min_clr);
    }

    /* Compress colinear segments if path still exceeds stock aiStack_fcc[1000] limit */
    if (len > MAX_RAW_ASTAR_NODES) {
        len = compress_colinear_path(g_ws.tmp_path, len);
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
 * Intercept ast_astarInt400WitCoef (0x3962c / Return / Reloc / Bump recovery)
 * to propagate tight clearance coef via TLS into findpathInt400.
 */
int ast_astarInt400WitCoef(float coef,
                           int width, int height,
                           int start_grid_x, int start_grid_y,
                           int target_grid_x, int target_grid_y,
                           void *costmap)
{
    if (!is_medusa_process()) {
        ensure_symbols();
        if (real_ast_astarInt400WitCoef) {
            return real_ast_astarInt400WitCoef(coef, width, height,
                                               start_grid_x, start_grid_y,
                                               target_grid_x, target_grid_y, costmap);
        }
        return 0;
    }

    ensure_symbols();
    if (!real_ast_astarInt400WitCoef) {
        return 0;
    }

    float prev_val = g_witcoef_val;
    if (g_witcoef_depth == 0) {
        g_witcoef_val = coef;
    }
    g_witcoef_depth++;
    int ret = real_ast_astarInt400WitCoef(coef, width, height,
                                          start_grid_x, start_grid_y,
                                          target_grid_x, target_grid_y, costmap);
    g_witcoef_depth--;
    if (g_witcoef_depth == 0) {
        g_witcoef_val = prev_val;
    }
    return ret;
}

/*
 * Intercept Gate-to-Gate planner (0x251c0.c / FUN_000386b8) to bypass synthetic room doorways.
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
        g_g2g_depth++;
        int raw_nodes = real_ast_astarInt400(width, height,
                                             start_grid_x, start_grid_y,
                                             target_grid_x, target_grid_y,
                                             costmap);
        g_g2g_depth--;

        if (raw_nodes > 0 && raw_nodes <= MAX_RAW_ASTAR_NODES + 1) {
            real_setAstartWalkPoint02(dest_x, dest_y);

            int wp_count = (p_ast_astarPathNodeCount) ? *p_ast_astarPathNodeCount : 1;
            if (wp_count >= 1 && wp_count <= MAX_WAYPOINTS) {
                log_msg("[DIRECT-PATH] Direct LOS-smoothed A* OK: raw_nodes=%d, waypoints=%d\n",
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