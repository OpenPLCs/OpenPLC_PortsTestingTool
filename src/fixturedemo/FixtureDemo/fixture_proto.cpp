/*
 * fixture_proto.cpp -- the pt.* protocol core, demo subset. See fixture_proto.h.
 *
 * Reply texts, refusal texts and frame layouts are copied from the fixture
 * firmware ($BOOT TestCase/porttool/) on purpose: the panel shows ERR text
 * verbatim and parses frames by key, so a difference here is a difference on
 * screen. T4-05 compares the two.
 */

#include "fixture_proto.h"
#include "fixture_hw.h"

#include <stdarg.h>
#include <stdio.h>
#include <string.h>

/* ---- output -------------------------------------------------------------- */

static void say(const char *fmt, ...)
{
    char line[FX_REPLY_MAX + 8];
    va_list ap;

    va_start(ap, fmt);
    vsnprintf(line, sizeof(line) - 2, fmt, ap);
    va_end(ap);
    strcat(line, "\r\n");
    fx_out(line);
}

/* ---- the control line's rate: the rs232 session switches it, as
 * PortTool_LineRequest does on the fixture firmware (PORTTOOL-FLOW.md B.7) -- */

#define LINE_DEFAULT_BAUD 115200U
#define LINE_SILENCE_MS   3000U
static uint32_t line_baud = LINE_DEFAULT_BAUD;
static uint32_t line_next;          /* 0 = nothing to apply after this reply */
static uint32_t line_heard_ms;
static int      line_settled = 1;   /* a byte has arrived at the current rate */
static uint32_t lines_in;           /* command lines taken in */

static void line_request(uint32_t baud)
{
    line_next = (baud == line_baud) ? 0U : baud;
}

/* " line=<baud>" when this command's reply announces a switch, else "". */
static const char *line_suffix(void)
{
    static char buf[24];

    if (line_next == 0U) {
        return "";
    }
    snprintf(buf, sizeof(buf), " line=%lu", (unsigned long)line_next);
    return buf;
}

static void line_set(uint32_t baud)
{
    fx_line_baud(baud);
    line_baud = baud;
}

/* Frames wait for the PC's first byte at the new rate: sent earlier they are noise. */
static void line_apply(uint32_t now)
{
    if (line_next == 0U) {
        return;
    }
    line_set(line_next);
    line_next = 0U;
    line_settled = (line_baud == LINE_DEFAULT_BAUD);
    line_heard_ms = now;
}

static void frame(const char *port, uint32_t now, const char *body)
{
    if (!line_settled) {
        return;
    }
    say("!%s t=%lu %s", port, (unsigned long)now, body);
}

/* A PC that died mid-test would leave the board on a rate nobody knows. */
static void line_check(uint32_t now)
{
    if (line_baud == LINE_DEFAULT_BAUD || line_next != 0U ||
        (uint32_t)(now - line_heard_ms) < LINE_SILENCE_MS) {
        return;
    }
    line_set(LINE_DEFAULT_BAUD);
    line_settled = 1;
    say("!line t=%lu baud=%lu reverted=1", (unsigned long)now, (unsigned long)line_baud);
}

/* Appends to a fixed buffer and never overruns it. */
static void add(char *buf, size_t len, const char *fmt, ...)
{
    size_t n = strlen(buf);
    va_list ap;

    if (n + 1 >= len) {
        return;
    }
    va_start(ap, fmt);
    vsnprintf(buf + n, len - n, fmt, ap);
    va_end(ap);
}

/* ---- k=v arguments, same grammar as the firmware's porttool_cmd.c --------- */

static int is_space(char c)
{
    return c == ' ' || c == '\t' || c == '\r' || c == '\n';
}

static const char *find_key(const char *args, const char *key)
{
    size_t klen = strlen(key);
    const char *p = args;

    while (*p != '\0') {
        while (is_space(*p)) { p++; }
        if (*p == '\0') { break; }
        const char *tok = p;
        int quoted = 0;
        while (*p != '\0' && (quoted || !is_space(*p))) {
            if (*p == '"') { quoted = !quoted; }
            p++;
        }
        if ((size_t)(p - tok) > klen && strncmp(tok, key, klen) == 0 && tok[klen] == '=') {
            return tok + klen + 1;
        }
    }
    return NULL;
}

static int get_str(const char *args, const char *key, char *out, size_t out_len)
{
    const char *v = find_key(args, key);
    size_t n = 0;

    if (v == NULL || out_len == 0) {
        return 0;
    }
    if (*v == '"') {
        v++;
        while (*v != '\0' && *v != '"' && n + 1 < out_len) { out[n++] = *v++; }
    } else {
        while (*v != '\0' && !is_space(*v) && n + 1 < out_len) { out[n++] = *v++; }
    }
    out[n] = '\0';
    return 1;
}

static int parse_u32(const char *s, uint32_t *out)
{
    uint32_t v = 0;
    int digits = 0;

    for (; *s != '\0'; s++) {
        if (*s < '0' || *s > '9') {
            return 0;
        }
        v = v * 10U + (uint32_t)(*s - '0');
        digits++;
    }
    if (digits == 0) {
        return 0;
    }
    *out = v;
    return 1;
}

static int get_u32(const char *args, const char *key, uint32_t *out)
{
    char buf[24];

    return get_str(args, key, buf, sizeof(buf)) && parse_u32(buf, out);
}

static int get_mask(const char *args, const char *key, uint32_t max_ch, uint32_t *mask)
{
    char buf[64];
    uint32_t m = 0, ch = 0;
    int digits = 0;

    if (!get_str(args, key, buf, sizeof(buf))) {
        return 0;
    }
    for (const char *p = buf; ; p++) {
        if (*p >= '0' && *p <= '9') {
            ch = ch * 10U + (uint32_t)(*p - '0');
            digits++;
        } else if (*p == ',' || *p == '\0') {
            if (digits == 0 || ch < 1U || ch > max_ch) {
                return 0;
            }
            m |= 1U << (ch - 1U);
            ch = 0;
            digits = 0;
            if (*p == '\0') { break; }
        } else {
            return 0;
        }
    }
    *mask = m;
    return 1;
}

/* "1:20,5:75", or a single value for every channel. Writes vals only when the
 * whole list is valid. */
static int get_pairs(const char *args, const char *key, uint32_t max_ch,
                     uint32_t val_max, uint32_t *vals)
{
    char buf[96];
    uint32_t scratch[32];
    uint32_t ch = 0, val = 0, seen = 0;
    int ch_digits = 0, val_digits = 0, in_value = 0;

    if (!get_str(args, key, buf, sizeof(buf)) || max_ch == 0U || max_ch > 32U) {
        return 0;
    }
    if (strchr(buf, ':') == NULL) {
        if (!parse_u32(buf, &val) || val > val_max) {
            return 0;
        }
        for (uint32_t i = 0; i < max_ch; i++) { vals[i] = val; }
        return 1;
    }
    for (const char *p = buf; ; p++) {
        if (*p >= '0' && *p <= '9') {
            if (in_value) { val = val * 10U + (uint32_t)(*p - '0'); val_digits++; }
            else          { ch = ch * 10U + (uint32_t)(*p - '0');   ch_digits++; }
        } else if (*p == ':') {
            if (in_value || ch_digits == 0) { return 0; }
            in_value = 1;
        } else if (*p == ',' || *p == '\0') {
            if (!in_value || ch_digits == 0 || val_digits == 0) { return 0; }
            if (ch < 1U || ch > max_ch || val > val_max) { return 0; }
            if ((seen & (1U << (ch - 1U))) != 0U) { return 0; }
            seen |= 1U << (ch - 1U);
            scratch[ch - 1U] = val;
            ch = val = 0;
            ch_digits = val_digits = in_value = 0;
            if (*p == '\0') { break; }
        } else {
            return 0;
        }
    }
    for (uint32_t i = 0; i < max_ch; i++) {
        if ((seen & (1U << i)) != 0U) { vals[i] = scratch[i]; }
    }
    return 1;
}

static void fmt_mask(uint32_t mask, uint32_t max_ch, char *out, size_t len)
{
    out[0] = '\0';
    for (uint32_t i = 0; i < max_ch; i++) {
        if (mask & (1U << i)) {
            add(out, len, "%s%lu", out[0] ? "," : "", (unsigned long)(i + 1U));
        }
    }
}

static void fmt_pairs(const uint32_t *vals, uint32_t mask, uint32_t max_ch, char *out, size_t len)
{
    out[0] = '\0';
    for (uint32_t i = 0; i < max_ch; i++) {
        if (mask & (1U << i)) {
            add(out, len, "%s%lu:%lu", out[0] ? "," : "", (unsigned long)(i + 1U),
                (unsigned long)vals[i]);
        }
    }
}

static const char *word(const char *line, char *out, size_t out_len)
{
    size_t n = 0;

    while (is_space(*line)) { line++; }
    while (*line != '\0' && !is_space(*line) && n + 1 < out_len) { out[n++] = *line++; }
    out[n] = '\0';
    while (is_space(*line)) { line++; }
    return line;
}

static uint32_t clamp_period(uint32_t ms)
{
    return ms < FX_PERIOD_MIN_MS ? FX_PERIOD_MIN_MS : ms;
}

/* Every session's period= is parsed the same way. */
static int apply_period(const char *args, uint32_t *want, char *err, size_t err_len)
{
    char probe[32];
    uint32_t v;

    if (get_str(args, "period", probe, sizeof(probe))) {
        if (!parse_u32(probe, &v)) {
            snprintf(err, err_len, "period=\"%s\" is not a number", probe);
            return 0;
        }
        *want = clamp_period(v);
    }
    return 1;
}

static int apply_mask(const char *args, uint32_t channels, uint32_t *want, char *err, size_t err_len)
{
    char probe[64];
    uint32_t v;

    if (get_str(args, "ch", probe, sizeof(probe))) {
        if (!get_mask(args, "ch", channels, &v) || v == 0U) {
            snprintf(err, err_len, "ch=\"%s\" must be channels 1..%lu separated by commas",
                     probe, (unsigned long)channels);
            return 0;
        }
        *want = v;
    }
    return 1;
}

static int due(uint32_t now, uint32_t *due_ms, uint32_t period)
{
    if ((int32_t)(now - *due_ms) < 0) {
        return 0;
    }
    *due_ms = now + period;
    return 1;
}

/* ---- the echo counter, same rules as PortTool_Echo* ----------------------- */

struct echo_t {
    uint32_t seq, rx, miss, got;
    int have, started;
};

static void echo_reset(echo_t *e)
{
    e->seq = 1U;
    e->rx = e->miss = e->got = 0U;
    e->have = e->started = 0;
}

static void echo_got(echo_t *e, uint32_t v)
{
    e->got = v;
    e->have = 1;
}

/* No reply, or a stale one, leaves seq where it is and counts a miss. */
static void echo_tick(echo_t *e)
{
    if (!e->started) {
        e->started = 1;
        return;
    }
    if (e->have && e->got == e->seq) {
        e->rx = e->got;
        e->seq = e->rx + 1U;
        e->miss = 0U;
    } else {
        e->miss++;
    }
    e->have = 0;
}

static void echo_fields(const echo_t *e, char *out, size_t len)
{
    snprintf(out, len, "seq=%lu rx=%lu miss=%lu", (unsigned long)e->seq,
             (unsigned long)e->rx, (unsigned long)e->miss);
}

/* Numbers in a byte stream, for the ports whose peer echoes over the link. */
struct numscan_t {
    uint32_t acc;
    int in_number;
};

static void numscan(numscan_t *s, echo_t *e, const uint8_t *data, size_t len)
{
    for (size_t i = 0; i < len; i++) {
        if (data[i] >= '0' && data[i] <= '9') {
            s->acc = s->acc * 10U + (uint32_t)(data[i] - '0');
            s->in_number = 1;
        } else if (s->in_number) {
            echo_got(e, s->acc);
            s->acc = 0;
            s->in_number = 0;
        }
    }
}

/* ---- the port table ------------------------------------------------------ */

struct port_t {
    const char *name, *board, *blk, *term, *terms, *params, *loop;
    uint32_t channels;
    int  (*start)(const char *args, char *err, size_t err_len);
    int  (*set)(const char *args, char *err, size_t err_len);
    void (*stop)(void);
    void (*tick)(uint32_t now);
    void (*echo)(uint32_t value);   /* NULL for a port that echoes over its own link */
    void (*caps)(char *out, size_t len);
    void (*limits)(char *out, size_t len);
    int running;
};

#define BODY_MAX FX_REPLY_MAX

/* din -- DI1..DI8 */

#define DIN_COUNT 8U
static uint32_t din_mask = (1U << DIN_COUNT) - 1U, din_period = 200U, din_due;
static echo_t din_echo;

static int din_apply(const char *args, char *err, size_t err_len)
{
    char probe[32];
    uint32_t mask = din_mask, period = din_period;

    if (!apply_mask(args, DIN_COUNT, &mask, err, err_len)) { return 0; }
    if (get_str(args, "mode", probe, sizeof(probe)) && strcmp(probe, "level") != 0) {
        snprintf(err, err_len, "mode=\"%s\" is not level - the demo has no quadrature counting", probe);
        return 0;
    }
    if (!apply_period(args, &period, err, err_len)) { return 0; }
    din_mask = mask;
    din_period = period;
    return 1;
}

static int din_start(const char *args, char *err, size_t err_len)
{
    if (!din_apply(args, err, err_len)) { return 0; }
    fx_din_init();
    echo_reset(&din_echo);
    din_due = fx_millis();
    return 1;
}

static void din_stop(void) {}

static void din_tick(uint32_t now)
{
    char body[BODY_MAX];
    uint8_t bits = fx_din_read();

    if (!due(now, &din_due, din_period)) { return; }
    echo_tick(&din_echo);
    echo_fields(&din_echo, body, sizeof(body));
    add(body, sizeof(body), " v=0x%02X mode=level", bits);
    for (uint32_t i = 0; i < DIN_COUNT; i++) {
        if (din_mask & (1U << i)) {
            add(body, sizeof(body), " ch%lu=%d", (unsigned long)(i + 1U), (bits >> i) & 1);
        }
    }
    frame("din", now, body);
}

static void din_echo_got(uint32_t v) { echo_got(&din_echo, v); }

static void din_caps(char *out, size_t len)
{
    char sel[32];
    fmt_mask(din_mask, DIN_COUNT, sel, sizeof(sel));
    snprintf(out, len, "ch=%s period=%lu", sel, (unsigned long)din_period);
}

static void din_limits(char *out, size_t len)
{
    snprintf(out, len, "ch:1..%lu period:%lu..", (unsigned long)DIN_COUNT,
             (unsigned long)FX_PERIOD_MIN_MS);
}

/* dout -- DO1..DO8, fully on or off: DO1/DO5, DO2/DO6, DO3/DO7 and DO4/DO8
 * share a timer channel, so analogWrite() on one turns its partner off. */

#define DOUT_COUNT 8U
#define DOUT_FULL  255U
static uint32_t dout_mask = (1U << DOUT_COUNT) - 1U, dout_period = 1000U, dout_due;
static uint32_t dout_duty[DOUT_COUNT];
static int dout_blink, dout_dark;
static echo_t dout_echo;

static void dout_drive(void)
{
    for (uint32_t i = 0; i < DOUT_COUNT; i++) {
        if (dout_mask & (1U << i)) {
            fx_dout_set((int)i + 1, !dout_dark && dout_duty[i] != 0U);
        }
    }
}

static int dout_apply(const char *args, char *err, size_t err_len)
{
    char probe[96];
    uint32_t mask = dout_mask, period = dout_period, v;
    uint32_t duty[DOUT_COUNT];
    int blink = dout_blink;

    memcpy(duty, dout_duty, sizeof(duty));
    if (!apply_mask(args, DOUT_COUNT, &mask, err, err_len)) { return 0; }
    if (get_str(args, "mode", probe, sizeof(probe))) {
        if (strcmp(probe, "hold") == 0)       { blink = 0; }
        else if (strcmp(probe, "blink") == 0) { blink = 1; }
        else {
            snprintf(err, err_len, "mode=\"%s\" is not hold or blink", probe);
            return 0;
        }
    }
    if (get_str(args, "res", probe, sizeof(probe)) && !(parse_u32(probe, &v) && v == 8U)) {
        snprintf(err, err_len, "res=\"%s\" - the demo runs at res=8 only", probe);
        return 0;
    }
    if (get_str(args, "freq", probe, sizeof(probe))) {
        snprintf(err, err_len, "freq=\"%s\" - the demo has no PWM, outputs are fully on or off", probe);
        return 0;
    }
    if (get_str(args, "duty", probe, sizeof(probe))) {
        int ok = get_pairs(args, "duty", DOUT_COUNT, DOUT_FULL, duty);
        for (uint32_t i = 0; ok && i < DOUT_COUNT; i++) {
            ok = duty[i] == 0U || duty[i] == DOUT_FULL;
        }
        if (!ok) {
            snprintf(err, err_len, "duty=\"%s\" must be 0 or 255 per output, like 1:255,5:0 - "
                     "the demo drives an output fully off or fully on", probe);
            return 0;
        }
    }
    if (!apply_period(args, &period, err, err_len)) { return 0; }
    dout_mask = mask;
    dout_period = period;
    dout_blink = blink;
    memcpy(dout_duty, duty, sizeof(duty));
    return 1;
}

static int dout_start(const char *args, char *err, size_t err_len)
{
    if (!dout_apply(args, err, err_len)) { return 0; }
    fx_dout_init();
    dout_dark = 0;
    dout_drive();
    echo_reset(&dout_echo);
    dout_due = fx_millis();
    return 1;
}

static int dout_set(const char *args, char *err, size_t err_len)
{
    if (!dout_apply(args, err, err_len)) { return 0; }
    dout_drive();
    return 1;
}

static void dout_stop(void)
{
    for (uint32_t i = 0; i < DOUT_COUNT; i++) {
        fx_dout_set((int)i + 1, 0);
    }
    dout_dark = 0;
}

static void dout_tick(uint32_t now)
{
    char body[BODY_MAX];

    if (!due(now, &dout_due, dout_period)) { return; }
    if (dout_blink) {
        dout_dark = !dout_dark;
        dout_drive();
    }
    echo_tick(&dout_echo);
    echo_fields(&dout_echo, body, sizeof(body));
    add(body, sizeof(body), " mode=%s res=8", dout_blink ? "blink" : "hold");
    for (uint32_t i = 0; i < DOUT_COUNT; i++) {
        if (dout_mask & (1U << i)) {
            add(body, sizeof(body), " ch%lu=%lu", (unsigned long)(i + 1U),
                (unsigned long)(dout_dark ? 0U : dout_duty[i]));
        }
    }
    frame("dout", now, body);
}

static void dout_echo_got(uint32_t v) { echo_got(&dout_echo, v); }

static void dout_caps(char *out, size_t len)
{
    char sel[32], duty[80];
    fmt_mask(dout_mask, DOUT_COUNT, sel, sizeof(sel));
    fmt_pairs(dout_duty, dout_mask, DOUT_COUNT, duty, sizeof(duty));
    snprintf(out, len, "ch=%s mode=%s res=8 duty=%s period=%lu", sel,
             dout_blink ? "blink" : "hold", duty, (unsigned long)dout_period);
}

static void dout_limits(char *out, size_t len)
{
    snprintf(out, len, "ch:1..%lu mode:hold|blink duty:0|%lu period:%lu..",
             (unsigned long)DOUT_COUNT, (unsigned long)DOUT_FULL, (unsigned long)FX_PERIOD_MIN_MS);
}

/* relay -- RY1..RY6. Same behaviour as $BOOT TestCase/porttool/porttool_relay.c
 * ($PROD/docs/modules/M4/PORTTOOL-FLOW.md B.2): mode=cycle runs each selected
 * relay open-hold, close, close-hold, release (n+1) and stops it released at
 * its count; one frame at start and one per edge, done=1 once all reached. */

#define RELAY_COUNT 6U
/* Datasheet gives no maximum operating frequency, see
 * $PROD/docs/hardware/HARDWARE-FACTS.md "继电器 RY1–RY6". */
#define RELAY_HOLD_MIN_MS   1000U
#define RELAY_COUNT_MAX     1000000U
#define RELAY_HOLD_FRAME_MS 1000U
static uint32_t relay_mask = (1U << RELAY_COUNT) - 1U, relay_due;
static uint32_t relay_on[RELAY_COUNT];
static uint32_t relay_close_ms[RELAY_COUNT] = {2000U, 2000U, 2000U, 2000U, 2000U, 2000U};
static uint32_t relay_open_ms[RELAY_COUNT]  = {2000U, 2000U, 2000U, 2000U, 2000U, 2000U};
static uint32_t relay_count[RELAY_COUNT]    = {10U, 10U, 10U, 10U, 10U, 10U};
static uint32_t relay_n[RELAY_COUNT];
static uint32_t relay_since[RELAY_COUNT];
static uint32_t relay_finished;
static int relay_cycle, relay_frame_now;
static echo_t relay_echo;

static void relay_drive(void)
{
    for (uint32_t i = 0; i < RELAY_COUNT; i++) {
        if (relay_mask & (1U << i)) {
            fx_relay_set((int)i + 1, relay_on[i] != 0U);
        }
    }
}

static void relay_release_all(void)
{
    for (uint32_t i = 0; i < RELAY_COUNT; i++) {
        fx_relay_set((int)i + 1, 0);
    }
}

static void relay_cycle_begin(uint32_t now)
{
    relay_release_all();
    for (uint32_t i = 0; i < RELAY_COUNT; i++) {
        relay_on[i] = 0U;
        relay_n[i] = 0U;
        relay_since[i] = now;
    }
    relay_finished = 0U;
    relay_frame_now = 1;
}

/* close= / open= / count=: one value or ch:value pairs within lo..hi. */
static int relay_get_vals(const char *args, const char *key, uint32_t lo, uint32_t hi,
                          const char *unit, uint32_t *vals, char *err, size_t err_len)
{
    char probe[96], range[40];
    uint32_t tmp[RELAY_COUNT];

    if (!get_str(args, key, probe, sizeof(probe))) { return 1; }
    if (hi == 0xFFFFFFFFU) {
        snprintf(range, sizeof(range), "at least %lu%s", (unsigned long)lo, unit);
    } else {
        snprintf(range, sizeof(range), "%lu..%lu%s", (unsigned long)lo, (unsigned long)hi, unit);
    }
    memcpy(tmp, vals, sizeof(tmp));
    int ok = get_pairs(args, key, RELAY_COUNT, hi, tmp);
    for (uint32_t i = 0; ok && i < RELAY_COUNT; i++) {
        if (tmp[i] < lo) { ok = 0; }
    }
    if (!ok) {
        if (strchr(probe, ':') != NULL) {
            snprintf(err, err_len, "%s= must be like 1:%lu,2:%lu - relays 1..%lu once each, %s",
                     key, (unsigned long)lo, (unsigned long)lo, (unsigned long)RELAY_COUNT, range);
        } else {
            snprintf(err, err_len, "%s=\"%s\" must be %s", key, probe, range);
        }
        return 0;
    }
    memcpy(vals, tmp, sizeof(tmp));
    return 1;
}

static int relay_apply(const char *args, char *err, size_t err_len)
{
    char probe[64];
    uint32_t mask = relay_mask;
    uint32_t on[RELAY_COUNT], cl[RELAY_COUNT], op[RELAY_COUNT], cn[RELAY_COUNT];
    int cycle = relay_cycle;

    memcpy(on, relay_on, sizeof(on));
    memcpy(cl, relay_close_ms, sizeof(cl));
    memcpy(op, relay_open_ms, sizeof(op));
    memcpy(cn, relay_count, sizeof(cn));
    if (!apply_mask(args, RELAY_COUNT, &mask, err, err_len)) { return 0; }
    if (get_str(args, "mode", probe, sizeof(probe))) {
        if (strcmp(probe, "hold") == 0)       { cycle = 0; }
        else if (strcmp(probe, "cycle") == 0) { cycle = 1; }
        else {
            snprintf(err, err_len, "mode=\"%s\" is not hold or cycle", probe);
            return 0;
        }
    }
    if (get_str(args, "on", probe, sizeof(probe))) {
        if (strchr(probe, ':') != NULL) {
            if (!get_pairs(args, "on", RELAY_COUNT, 1U, on)) {
                snprintf(err, err_len,
                         "on=\"%s\" must be like 1:1,2:0 - relays 1..%lu, level 0 or 1, "
                         "each relay named at most once", probe, (unsigned long)RELAY_COUNT);
                return 0;
            }
        } else if (!get_pairs(args, "on", RELAY_COUNT, 1U, on)) {
            snprintf(err, err_len, "on=\"%s\" is not 0 or 1", probe);
            return 0;
        }
    }
    if (!relay_get_vals(args, "close", RELAY_HOLD_MIN_MS, 0xFFFFFFFFU, " ms", cl, err, err_len) ||
        !relay_get_vals(args, "open", RELAY_HOLD_MIN_MS, 0xFFFFFFFFU, " ms", op, err, err_len) ||
        !relay_get_vals(args, "count", 1U, RELAY_COUNT_MAX, "", cn, err, err_len)) {
        return 0;
    }
    relay_mask = mask;
    relay_cycle = cycle;
    memcpy(relay_on, on, sizeof(on));
    memcpy(relay_close_ms, cl, sizeof(cl));
    memcpy(relay_open_ms, op, sizeof(op));
    memcpy(relay_count, cn, sizeof(cn));
    return 1;
}

static int relay_start(const char *args, char *err, size_t err_len)
{
    if (!relay_apply(args, err, err_len)) { return 0; }
    fx_relay_init();
    relay_release_all();
    echo_reset(&relay_echo);
    relay_due = fx_millis();
    if (relay_cycle) { relay_cycle_begin(relay_due); } else { relay_drive(); }
    return 1;
}

static int relay_set(const char *args, char *err, size_t err_len)
{
    int was_cycle = relay_cycle;
    uint32_t was_mask = relay_mask;

    if (!relay_apply(args, err, err_len)) { return 0; }
    if (!relay_cycle) { relay_drive(); }
    else if (!was_cycle || was_mask != relay_mask) { relay_cycle_begin(fx_millis()); }
    return 1;
}

static void relay_stop(void)
{
    relay_release_all();
    memset(relay_on, 0, sizeof(relay_on));
}

static int relay_cycle_step(uint32_t now)
{
    int moved = 0;

    for (uint32_t i = 0; i < RELAY_COUNT; i++) {
        uint32_t bit = 1U << i;
        if ((relay_mask & bit) == 0U || (relay_finished & bit) != 0U) { continue; }
        uint32_t hold = relay_on[i] ? relay_close_ms[i] : relay_open_ms[i];
        if ((uint32_t)(now - relay_since[i]) < hold) { continue; }
        relay_since[i] = now;
        if (relay_on[i]) {
            relay_on[i] = 0U;
            if (++relay_n[i] >= relay_count[i]) { relay_finished |= bit; }
        } else {
            relay_on[i] = 1U;
        }
        moved = 1;
    }
    if (moved) { relay_drive(); }
    return moved;
}

static void relay_tick(uint32_t now)
{
    char body[BODY_MAX];

    if (relay_cycle) {
        int send = relay_frame_now;
        relay_frame_now = 0;
        if (!relay_cycle_step(now) && !send) { return; }
    } else if (!due(now, &relay_due, RELAY_HOLD_FRAME_MS)) {
        return;
    }
    echo_tick(&relay_echo);
    echo_fields(&relay_echo, body, sizeof(body));
    add(body, sizeof(body), " mode=%s", relay_cycle ? "cycle" : "hold");
    for (uint32_t i = 0; i < RELAY_COUNT; i++) {
        if ((relay_mask & (1U << i)) == 0U) { continue; }
        if (relay_cycle) {
            add(body, sizeof(body), " ch%lu=%lu n%lu=%lu", (unsigned long)(i + 1U), (unsigned long)relay_on[i],
                (unsigned long)(i + 1U), (unsigned long)relay_n[i]);
        } else {
            add(body, sizeof(body), " ch%lu=%lu", (unsigned long)(i + 1U), (unsigned long)relay_on[i]);
        }
    }
    if (relay_cycle) {
        add(body, sizeof(body), " done=%d", (relay_finished & relay_mask) == relay_mask ? 1 : 0);
    }
    frame("relay", now, body);
}

static void relay_echo_got(uint32_t v) { echo_got(&relay_echo, v); }

static int relay_same(const uint32_t *vals, uint32_t *value)
{
    int have = 0;

    for (uint32_t i = 0; i < RELAY_COUNT; i++) {
        if ((relay_mask & (1U << i)) == 0U) { continue; }
        if (!have) { *value = vals[i]; have = 1; }
        else if (vals[i] != *value) { return 0; }
    }
    return have;
}

/* close / open / count only while the selected relays share them: six
 * different values do not fit FX_REPLY_MAX (PORTTOOL-FLOW.md B.2). */
static void relay_caps(char *out, size_t len)
{
    char sel[32], on[64];
    uint32_t c, o, n;

    fmt_mask(relay_mask, RELAY_COUNT, sel, sizeof(sel));
    fmt_pairs(relay_on, relay_mask, RELAY_COUNT, on, sizeof(on));
    snprintf(out, len, "ch=%s mode=%s on=%s", sel, relay_cycle ? "cycle" : "hold", on);
    if (relay_same(relay_close_ms, &c) && relay_same(relay_open_ms, &o) && relay_same(relay_count, &n)) {
        add(out, len, " close=%lu open=%lu count=%lu", (unsigned long)c, (unsigned long)o, (unsigned long)n);
    }
}

static void relay_limits(char *out, size_t len)
{
    snprintf(out, len, "ch:1..%lu mode:hold|cycle on:0..1 close:%lu.. open:%lu.. count:1..%lu",
             (unsigned long)RELAY_COUNT, (unsigned long)RELAY_HOLD_MIN_MS,
             (unsigned long)RELAY_HOLD_MIN_MS, (unsigned long)RELAY_COUNT_MAX);
}

/* Shared by ain, aout and temp: the reference has to be up before any of them. */

#define VDDA_MIN_MV 2000U
#define VDDA_MAX_MV 3600U

static int analog_up(char *err, size_t err_len, const char *why)
{
    if (!fx_analog_init()) {
        snprintf(err, err_len, "VREFBUF would not come up, %s", why);
        return 0;
    }
    return 1;
}

static void add_vdda(char *body, size_t len)
{
    uint32_t mv = fx_vdda_mv();
    add(body, len, " vdda=%lu ok=%d", (unsigned long)mv, mv >= VDDA_MIN_MV && mv <= VDDA_MAX_MV);
}

static const char *no_reference =
    "so there is no reference to measure against and every reading would be meaningless";

/* ain -- AI1 (voltage), AI2 (current) */

#define AIN_COUNT 2U
static uint32_t ain_mask = (1U << AIN_COUNT) - 1U, ain_period = 500U, ain_due;
static echo_t ain_echo;

static int ain_apply(const char *args, char *err, size_t err_len)
{
    uint32_t mask = ain_mask, period = ain_period;

    if (!apply_mask(args, AIN_COUNT, &mask, err, err_len)) { return 0; }
    if (!apply_period(args, &period, err, err_len)) { return 0; }
    ain_mask = mask;
    ain_period = period;
    return 1;
}

static int ain_start(const char *args, char *err, size_t err_len)
{
    if (!ain_apply(args, err, err_len) || !analog_up(err, err_len, no_reference)) { return 0; }
    echo_reset(&ain_echo);
    ain_due = fx_millis();
    return 1;
}

static void ain_stop(void) {}

static void ain_tick(uint32_t now)
{
    char body[BODY_MAX];

    if (!due(now, &ain_due, ain_period)) { return; }
    echo_tick(&ain_echo);
    echo_fields(&ain_echo, body, sizeof(body));
    add_vdda(body, sizeof(body));
    for (uint32_t i = 1; i <= AIN_COUNT; i++) {
        uint32_t raw = 0, mv = 0;
        if ((ain_mask & (1U << (i - 1U))) == 0U) { continue; }
        if (fx_ain_read((int)i, &raw, &mv)) {
            add(body, sizeof(body), " ch%lu=%lu/%lu", (unsigned long)i, (unsigned long)raw, (unsigned long)mv);
        } else {
            add(body, sizeof(body), " ch%lu=fail", (unsigned long)i);
        }
    }
    /* The terminal values through the board package's calibrated API. */
    add(body, sizeof(body), " cal=%s", fx_cal_status());
    if (ain_mask & 1U) { add(body, sizeof(body), " ai1_mv=%ld", (long)fx_ai1_mv()); }
    if (ain_mask & 2U) { add(body, sizeof(body), " ai2_ua=%ld", (long)fx_ai2_ua()); }
    frame("ain", now, body);
}

static void ain_echo_got(uint32_t v) { echo_got(&ain_echo, v); }

static void ain_caps(char *out, size_t len)
{
    char sel[16];
    fmt_mask(ain_mask, AIN_COUNT, sel, sizeof(sel));
    snprintf(out, len, "ch=%s period=%lu", sel, (unsigned long)ain_period);
}

static void ain_limits(char *out, size_t len)
{
    snprintf(out, len, "ch:1..%lu period:%lu..", (unsigned long)AIN_COUNT, (unsigned long)FX_PERIOD_MIN_MS);
}

/* aout -- AO1, AO2. mv= is the DAC pin voltage, as on the fixture firmware;
 * fx_aout_set_mv() reaches it through the calibrated API (PORTTOOL-FLOW.md A.9). */

#define AOUT_COUNT  2U
#define AOUT_MV_MAX 2500U
#define DAC_FULL    4095U
#define XTR111_RSET_OHM 1024U
static uint32_t aout_mask = (1U << AOUT_COUNT) - 1U, aout_period = 1000U, aout_due;
static uint32_t aout_mv[AOUT_COUNT], aout_code[AOUT_COUNT];
static echo_t aout_echo;

static uint32_t aout_expected_ua(uint32_t mv)
{
    return (mv * 10000U) / XTR111_RSET_OHM;
}

static void aout_drive(void)
{
    for (uint32_t i = 0; i < AOUT_COUNT; i++) {
        if (aout_mask & (1U << i)) {
            aout_code[i] = fx_aout_set_mv((int)i + 1, aout_mv[i]);
        }
    }
}

static int aout_apply(const char *args, char *err, size_t err_len)
{
    char probe[64];
    uint32_t mask = aout_mask, period = aout_period;
    uint32_t mv[AOUT_COUNT];

    memcpy(mv, aout_mv, sizeof(mv));
    if (!apply_mask(args, AOUT_COUNT, &mask, err, err_len)) { return 0; }
    if (get_str(args, "mv", probe, sizeof(probe)) &&
        !get_pairs(args, "mv", AOUT_COUNT, AOUT_MV_MAX, mv)) {
        if (strchr(probe, ':') != NULL) {
            snprintf(err, err_len, "mv=\"%s\" must be like 1:500,2:1500 - outputs 1..%lu, "
                     "0..%lu mV, each output named at most once",
                     probe, (unsigned long)AOUT_COUNT, (unsigned long)AOUT_MV_MAX);
        } else {
            snprintf(err, err_len, "mv=\"%s\" must be 0..%lu millivolts", probe,
                     (unsigned long)AOUT_MV_MAX);
        }
        return 0;
    }
    if (!apply_period(args, &period, err, err_len)) { return 0; }
    aout_mask = mask;
    aout_period = period;
    memcpy(aout_mv, mv, sizeof(mv));
    return 1;
}

static int aout_start(const char *args, char *err, size_t err_len)
{
    if (!aout_apply(args, err, err_len) ||
        !analog_up(err, err_len, "and the DAC's full scale is VREF+ - the output would not be "
                   "the value asked for")) {
        return 0;
    }
    aout_drive();
    echo_reset(&aout_echo);
    aout_due = fx_millis();
    return 1;
}

static int aout_set(const char *args, char *err, size_t err_len)
{
    if (!aout_apply(args, err, err_len)) { return 0; }
    aout_drive();
    return 1;
}

static void aout_stop(void)
{
    for (uint32_t i = 0; i < AOUT_COUNT; i++) {
        aout_mv[i] = 0U;
        aout_code[i] = fx_aout_set_mv((int)i + 1, 0U);
    }
}

static void aout_tick(uint32_t now)
{
    char body[BODY_MAX];

    if (!due(now, &aout_due, aout_period)) { return; }
    echo_tick(&aout_echo);
    echo_fields(&aout_echo, body, sizeof(body));
    for (uint32_t i = 0; i < AOUT_COUNT; i++) {
        if ((aout_mask & (1U << i)) == 0U) { continue; }
        add(body, sizeof(body), " ch%lu=%lu/%lu/%lu ef%lu=%d", (unsigned long)(i + 1U),
            (unsigned long)aout_mv[i], (unsigned long)(aout_code[i] * AOUT_MV_MAX / DAC_FULL),
            (unsigned long)aout_expected_ua(aout_mv[i]), (unsigned long)(i + 1U),
            fx_aout_fault((int)i + 1));
    }
    add(body, sizeof(body), " cal=%s", fx_cal_status());
    frame("aout", now, body);
}

static void aout_echo_got(uint32_t v) { echo_got(&aout_echo, v); }

static void aout_caps(char *out, size_t len)
{
    char sel[16], mv[48];
    fmt_mask(aout_mask, AOUT_COUNT, sel, sizeof(sel));
    fmt_pairs(aout_mv, aout_mask, AOUT_COUNT, mv, sizeof(mv));
    snprintf(out, len, "ch=%s mv=%s period=%lu", sel, mv, (unsigned long)aout_period);
}

static void aout_limits(char *out, size_t len)
{
    snprintf(out, len, "ch:1..%lu mv:0..%lu period:%lu..", (unsigned long)AOUT_COUNT,
             (unsigned long)AOUT_MV_MAX, (unsigned long)FX_PERIOD_MIN_MS);
}

/* temp -- the two LM50s: 500 mV at 0 C, 10 mV per degree. */

#define TEMP_COUNT 2U
static uint32_t temp_mask = (1U << TEMP_COUNT) - 1U, temp_period = 1000U, temp_due;
static echo_t temp_echo;

static int temp_apply(const char *args, char *err, size_t err_len)
{
    uint32_t mask = temp_mask, period = temp_period;

    if (!apply_mask(args, TEMP_COUNT, &mask, err, err_len)) { return 0; }
    if (!apply_period(args, &period, err, err_len)) { return 0; }
    temp_mask = mask;
    temp_period = period;
    return 1;
}

static int temp_start(const char *args, char *err, size_t err_len)
{
    if (!temp_apply(args, err, err_len) || !analog_up(err, err_len, no_reference)) { return 0; }
    echo_reset(&temp_echo);
    temp_due = fx_millis();
    return 1;
}

static void temp_stop(void) {}

static void temp_tick(uint32_t now)
{
    char body[BODY_MAX];

    if (!due(now, &temp_due, temp_period)) { return; }
    echo_tick(&temp_echo);
    echo_fields(&temp_echo, body, sizeof(body));
    add_vdda(body, sizeof(body));
    for (uint32_t i = 1; i <= TEMP_COUNT; i++) {
        uint32_t mv = 0;
        if ((temp_mask & (1U << (i - 1U))) == 0U) { continue; }
        if (fx_temp_read((int)i, &mv)) {
            add(body, sizeof(body), " ch%lu=%lu/%ld", (unsigned long)i, (unsigned long)mv,
                (long)mv - 500L);
        } else {
            add(body, sizeof(body), " ch%lu=fail", (unsigned long)i);
        }
    }
    frame("temp", now, body);
}

static void temp_echo_got(uint32_t v) { echo_got(&temp_echo, v); }

static void temp_caps(char *out, size_t len)
{
    char sel[16];
    fmt_mask(temp_mask, TEMP_COUNT, sel, sizeof(sel));
    snprintf(out, len, "ch=%s period=%lu", sel, (unsigned long)temp_period);
}

static void temp_limits(char *out, size_t len)
{
    snprintf(out, len, "ch:1..%lu period:%lu..", (unsigned long)TEMP_COUNT, (unsigned long)FX_PERIOD_MIN_MS);
}

/* rs232 -- the control line itself, at a chosen rate (decision 104). loop=self:
 * the echo crosses C05/C06 and the transceiver both ways, so here the counter
 * is the verdict. */

static uint32_t rs232_baud = LINE_DEFAULT_BAUD, rs232_period = 3000U, rs232_due;
static echo_t rs232_echo;

static int rs232_apply(const char *args, char *err, size_t err_len)
{
    char probe[32];
    uint32_t baud = rs232_baud, period = rs232_period, v;

    if (get_str(args, "baud", probe, sizeof(probe))) {
        if (!parse_u32(probe, &v) || (v != 9600U && v != 19200U && v != 38400U &&
                                      v != 57600U && v != 115200U && v != 230400U)) {
            snprintf(err, err_len, "baud=\"%s\" must be one of 9600 19200 38400 57600 115200 230400",
                     probe);
            return 0;
        }
        baud = v;
    }
    if (!apply_period(args, &period, err, err_len)) { return 0; }
    rs232_baud = baud;
    rs232_period = period;
    return 1;
}

static int rs232_start(const char *args, char *err, size_t err_len)
{
    rs232_baud = LINE_DEFAULT_BAUD;   /* a start without baud= runs at the default */
    if (!rs232_apply(args, err, err_len)) { return 0; }
    echo_reset(&rs232_echo);
    rs232_due = fx_millis();
    line_request(rs232_baud);
    return 1;
}

static int rs232_set(const char *args, char *err, size_t err_len)
{
    if (!rs232_apply(args, err, err_len)) { return 0; }
    line_request(rs232_baud);
    return 1;
}

static void rs232_stop(void)
{
    rs232_baud = LINE_DEFAULT_BAUD;
    line_request(rs232_baud);
}

static void rs232_tick(uint32_t now)
{
    char body[BODY_MAX];

    if (!due(now, &rs232_due, rs232_period)) { return; }
    echo_tick(&rs232_echo);
    echo_fields(&rs232_echo, body, sizeof(body));
    add(body, sizeof(body), " rxlines=%lu baud=%lu", (unsigned long)lines_in, (unsigned long)line_baud);
    frame("rs232", now, body);
}

static void rs232_echo_got(uint32_t v) { echo_got(&rs232_echo, v); }

static void rs232_caps(char *out, size_t len)
{
    snprintf(out, len, "baud=%lu period=%lu", (unsigned long)rs232_baud, (unsigned long)rs232_period);
}

static void rs232_limits(char *out, size_t len)
{
    snprintf(out, len, "baud:9600|19200|38400|57600|115200|230400 period:%lu..",
             (unsigned long)FX_PERIOD_MIN_MS);
}

/* rs485 -- the peer sends each number back as a line. */

#define RS485_IN_MAX 24U
#define LINK_WAIT_MS 20U
static uint32_t rs485_baud = 115200U, rs485_period = 3000U, rs485_due;
static uint32_t rs485_rxbytes, rs485_junk, rs485_in_len;
static char rs485_in[RS485_IN_MAX];
static echo_t rs485_echo;

static int rs485_apply(const char *args, char *err, size_t err_len)
{
    char probe[32];
    uint32_t baud = rs485_baud, period = rs485_period, v;

    if (get_str(args, "baud", probe, sizeof(probe))) {
        if (!parse_u32(probe, &v) ||
            (v != 9600U && v != 19200U && v != 38400U && v != 57600U && v != 115200U)) {
            snprintf(err, err_len, "baud=\"%s\" must be one of 9600 19200 38400 57600 115200", probe);
            return 0;
        }
        baud = v;
    }
    if (!apply_period(args, &period, err, err_len)) { return 0; }
    rs485_baud = baud;
    rs485_period = period;
    return 1;
}

static int rs485_start(const char *args, char *err, size_t err_len)
{
    if (!rs485_apply(args, err, err_len)) { return 0; }
    if (!fx_rs485_begin(rs485_baud)) {
        snprintf(err, err_len, "USART2 would not start at %lu baud", (unsigned long)rs485_baud);
        return 0;
    }
    rs485_rxbytes = rs485_junk = rs485_in_len = 0U;
    echo_reset(&rs485_echo);
    rs485_due = fx_millis();
    return 1;
}

static int rs485_set(const char *args, char *err, size_t err_len)
{
    uint32_t was = rs485_baud;

    if (!rs485_apply(args, err, err_len)) { return 0; }
    if (rs485_baud != was && !fx_rs485_begin(rs485_baud)) {
        snprintf(err, err_len, "USART2 would not take %lu baud", (unsigned long)rs485_baud);
        return 0;
    }
    return 1;
}

static void rs485_stop(void)
{
    fx_rs485_end();
}

static void rs485_drain(void)
{
    uint8_t b;

    while (fx_rs485_read(&b)) {
        rs485_rxbytes++;
        if (b == '\r' || b == '\n') {
            uint32_t v;
            if (rs485_in_len == 0U) { continue; }
            rs485_in[rs485_in_len] = '\0';
            if (parse_u32(rs485_in, &v)) { echo_got(&rs485_echo, v); }
            else                         { rs485_junk++; }
            rs485_in_len = 0U;
        } else if (rs485_in_len + 1U < sizeof(rs485_in)) {
            rs485_in[rs485_in_len++] = (char)b;
        } else {
            rs485_in_len = 0U;
            rs485_junk++;
        }
    }
}

static void rs485_tick(uint32_t now)
{
    char body[BODY_MAX], out[RS485_IN_MAX];
    int n;

    rs485_drain();
    if (!due(now, &rs485_due, rs485_period)) { return; }
    echo_tick(&rs485_echo);
    n = snprintf(out, sizeof(out), "%lu\n", (unsigned long)rs485_echo.seq);
    fx_rs485_send(out, (size_t)n);
    for (uint32_t start = fx_millis(); fx_millis() - start < LINK_WAIT_MS && !rs485_echo.have; ) {
        rs485_drain();
    }
    echo_fields(&rs485_echo, body, sizeof(body));
    add(body, sizeof(body), " baud=%lu rxbytes=%lu junk=%lu", (unsigned long)rs485_baud,
        (unsigned long)rs485_rxbytes, (unsigned long)rs485_junk);
    frame("rs485", now, body);
}

static void rs485_caps(char *out, size_t len)
{
    snprintf(out, len, "baud=%lu period=%lu", (unsigned long)rs485_baud, (unsigned long)rs485_period);
}

static void rs485_limits(char *out, size_t len)
{
    snprintf(out, len, "baud:9600|19200|38400|57600|115200 period:%lu..", (unsigned long)FX_PERIOD_MIN_MS);
}

/* can -- frames with ID 0x123 carry the number big-endian in four bytes. In
 * mode=echo this board is the peer: it answers each one with the number + 1. */

#define CAN_ID 0x123U
static const uint32_t can_rates[] = { 125000U, 250000U, 500000U, 1000000U };
static uint32_t can_bps = 500000U, can_period = 1000U, can_due;
static int can_echo_role;
static uint32_t can_tx, can_rx, can_junk, can_replied;
static echo_t can_echo;

static void put_u32(uint8_t *d, uint32_t v)
{
    d[0] = (uint8_t)(v >> 24); d[1] = (uint8_t)(v >> 16); d[2] = (uint8_t)(v >> 8); d[3] = (uint8_t)v;
}

static uint32_t get_u32_be(const uint8_t *d)
{
    return ((uint32_t)d[0] << 24) | ((uint32_t)d[1] << 16) | ((uint32_t)d[2] << 8) | d[3];
}

static int can_apply(const char *args, char *err, size_t err_len)
{
    char probe[64];
    uint32_t bps = can_bps, period = can_period, v;
    int role = can_echo_role;

    if (get_str(args, "baud", probe, sizeof(probe))) {
        int ok = 0;
        if (parse_u32(probe, &v)) {
            for (uint32_t i = 0; i < sizeof(can_rates) / sizeof(can_rates[0]); i++) {
                ok |= v == can_rates[i];
            }
        }
        if (!ok) {
            snprintf(err, err_len, "baud=\"%s\" must be one of 125000, 250000, 500000, 1000000", probe);
            return 0;
        }
        bps = v;
    }
    if (get_str(args, "mode", probe, sizeof(probe))) {
        if (strcmp(probe, "normal") == 0)    { role = 0; }
        else if (strcmp(probe, "echo") == 0) { role = 1; }
        else {
            snprintf(err, err_len, "mode=\"%s\" must be normal or echo - the demo has no "
                     "listen or loopback modes", probe);
            return 0;
        }
    }
    if (!apply_period(args, &period, err, err_len)) { return 0; }
    can_bps = bps;
    can_echo_role = role;
    can_period = period;
    return 1;
}

static int can_start(const char *args, char *err, size_t err_len)
{
    if (!can_apply(args, err, err_len)) { return 0; }
    if (!fx_can_begin(can_bps)) {
        snprintf(err, err_len, "the FDCAN controller would not start at %lu bit/s", (unsigned long)can_bps);
        return 0;
    }
    can_tx = can_rx = can_junk = can_replied = 0U;
    echo_reset(&can_echo);
    can_due = fx_millis();
    return 1;
}

static int can_set(const char *args, char *err, size_t err_len)
{
    char probe[64];

    if (get_str(args, "baud", probe, sizeof(probe)) || get_str(args, "mode", probe, sizeof(probe))) {
        snprintf(err, err_len, "baud and mode are set when the controller opens - stop the "
                 "session and start it again to change them");
        return 0;
    }
    return can_apply(args, err, err_len);
}

static void can_stop(void)
{
    fx_can_end();
}

static void can_collect(void)
{
    uint32_t id;
    uint8_t d[8], len;

    for (int i = 0; i < 64 && fx_can_read(&id, d, &len); i++) {
        if (id != CAN_ID || len < 4U) {
            can_junk++;
            continue;
        }
        can_rx++;
        if (can_echo_role) {
            put_u32(d, get_u32_be(d) + 1U);
            if (fx_can_write(CAN_ID, d, len)) { can_replied++; }
        } else {
            echo_got(&can_echo, get_u32_be(d));
        }
    }
}

static void can_tick(uint32_t now)
{
    char body[BODY_MAX];
    uint8_t d[4];
    uint32_t tec = 0, rec = 0, lec = 0;

    can_collect();
    if (!due(now, &can_due, can_period)) { return; }
    if (!can_echo_role) {
        uint32_t sent = can_echo.seq;
        put_u32(d, sent);
        can_echo.have = 0;
        if (fx_can_write(CAN_ID, d, 4U)) { can_tx++; }
        for (uint32_t start = fx_millis(); fx_millis() - start < LINK_WAIT_MS; ) {
            can_collect();
            if (can_echo.have && can_echo.got == sent) { break; }
        }
    }
    fx_can_counters(&tec, &rec, &lec);
    echo_tick(&can_echo);
    echo_fields(&can_echo, body, sizeof(body));
    add(body, sizeof(body), " bps=%lu mode=%s alive=%d tx=%lu rx_frames=%lu junk=%lu replied=%lu"
        " tec=%lu rec=%lu lec=%lu", (unsigned long)can_bps, can_echo_role ? "echo" : "normal",
        fx_can_alive(), (unsigned long)can_tx, (unsigned long)can_rx, (unsigned long)can_junk,
        (unsigned long)can_replied, (unsigned long)tec, (unsigned long)rec, (unsigned long)lec);
    frame("can", now, body);
}

static void can_caps(char *out, size_t len)
{
    snprintf(out, len, "baud=%lu mode=%s period=%lu", (unsigned long)can_bps,
             can_echo_role ? "echo" : "normal", (unsigned long)can_period);
}

static void can_limits(char *out, size_t len)
{
    snprintf(out, len, "baud:125000|250000|500000|1000000 mode:normal|echo period:%lu..",
             (unsigned long)FX_PERIOD_MIN_MS);
}

/* eth and usb share one shape: echo (the peer sends each number back), mirror
 * (every byte goes straight back, the full-rate run) or, on usb only, info
 * (moves nothing, reports the enumeration state). */

#define MIRROR_CHUNK 512U

enum { MODE_ECHO, MODE_MIRROR, MODE_INFO };

static const char *mode_name(int mode)
{
    return mode == MODE_MIRROR ? "mirror" : mode == MODE_INFO ? "info" : "echo";
}

struct stream_t {
    size_t (*avail)(void);
    size_t (*room)(void);
    size_t (*read)(uint8_t *, size_t);
    size_t (*write)(const uint8_t *, size_t);
    int mode;
    uint32_t period, due_ms;
    uint32_t rx_bytes, tx_bytes, window, busy, drop;
    numscan_t scan;
    echo_t echo;
};

static void stream_reset(stream_t *s)
{
    s->rx_bytes = s->tx_bytes = s->window = s->busy = s->drop = 0U;
    s->scan.acc = 0U;
    s->scan.in_number = 0;
    echo_reset(&s->echo);
    s->due_ms = fx_millis();
}

/* Moves what has arrived: straight back in mirror mode, into the number
 * scanner in echo mode, nothing in info mode. Reads no more than can be
 * written back, so the link's own flow control holds the peer rather than a
 * byte being thrown away. */
static void stream_pump(stream_t *s)
{
    uint8_t buf[MIRROR_CHUNK];

    if (s->mode == MODE_INFO) {
        return;
    }
    for (int pass = 0; pass < 8; pass++) {
        size_t n = s->avail();
        if (s->mode == MODE_MIRROR) {
            size_t room = s->room();
            if (n > room) { n = room; }
        }
        if (n > sizeof(buf)) { n = sizeof(buf); }
        if (n == 0U) { return; }
        n = s->read(buf, n);
        s->rx_bytes += n;
        s->window += n;
        if (s->mode == MODE_ECHO) {
            numscan(&s->scan, &s->echo, buf, n);
            continue;
        }
        size_t w = s->write(buf, n);
        s->tx_bytes += w;
        s->window += w;
        s->drop += n - w;
    }
}

/* The echo-mode number for this period, and the throughput since the last.
 * Only echo mode counts misses: the others never ask for an answer. */
static uint32_t stream_period(stream_t *s, uint32_t now, int connected)
{
    char line[16];
    uint32_t kbps;

    if (s->mode == MODE_ECHO) {
        echo_tick(&s->echo);
        if (connected) {
            int n = snprintf(line, sizeof(line), "%lu\n", (unsigned long)s->echo.seq);
            if (s->room() >= (size_t)n && s->write((const uint8_t *)line, (size_t)n) == (size_t)n) {
                s->tx_bytes += (uint32_t)n;
            } else {
                s->busy++;
            }
        }
    }
    (void)now;
    kbps = s->period ? (s->window * 8U) / s->period : 0U;
    s->window = 0U;
    return kbps;
}

static int stream_mode(const char *args, int *mode, int info, char *err, size_t err_len)
{
    char probe[32];

    if (get_str(args, "mode", probe, sizeof(probe))) {
        if (strcmp(probe, "echo") == 0)              { *mode = MODE_ECHO; }
        else if (strcmp(probe, "mirror") == 0)       { *mode = MODE_MIRROR; }
        else if (info && strcmp(probe, "info") == 0) { *mode = MODE_INFO; }
        else if (info) {
            snprintf(err, err_len, "mode=\"%s\" must be echo, mirror or info - the demo has no "
                     "sink, source or file modes", probe);
            return 0;
        } else {
            snprintf(err, err_len, "mode=\"%s\" must be echo or mirror - the demo has no "
                     "sink, source or file modes", probe);
            return 0;
        }
    }
    return 1;
}

/* eth */

static stream_t eth = { fx_eth_avail, fx_eth_room, fx_eth_read, fx_eth_write, MODE_ECHO, 1000U, 0,
                        0, 0, 0, 0, 0, { 0, 0 }, { 0, 0, 0, 0, 0, 0 } };
static uint32_t eth_port = 5000U;
static int eth_static;
static uint8_t eth_static_ip[4];

/* "a.b.c.d" with each part 0..255. */
static int parse_ip(const char *s, uint8_t ip[4])
{
    uint32_t part = 0;
    int i = 0, digits = 0;

    for (;; s++) {
        if (*s >= '0' && *s <= '9') {
            part = part * 10U + (uint32_t)(*s - '0');
            if (++digits > 3 || part > 255U) { return 0; }
        } else if ((*s == '.' || *s == '\0') && digits > 0 && i < 4) {
            ip[i++] = (uint8_t)part;
            part = 0;
            digits = 0;
            if (*s == '\0') { return i == 4; }
        } else {
            return 0;
        }
    }
}

static int eth_apply(const char *args, char *err, size_t err_len)
{
    char probe[32];
    uint32_t port = eth_port, period = eth.period, v;
    int mode = eth.mode, is_static = eth_static;
    uint8_t ip[4];

    memcpy(ip, eth_static_ip, sizeof(ip));
    if (!stream_mode(args, &mode, 0, err, err_len)) { return 0; }
    if (get_str(args, "port", probe, sizeof(probe))) {
        if (!parse_u32(probe, &v) || v == 0U || v > 65535U) {
            snprintf(err, err_len, "port=\"%s\" must be 1..65535", probe);
            return 0;
        }
        port = v;
    }
    if (get_str(args, "ip", probe, sizeof(probe))) {
        if (strcmp(probe, "dhcp") == 0) {
            is_static = 0;
        } else if (parse_ip(probe, ip)) {
            is_static = 1;
        } else {
            snprintf(err, err_len, "ip=\"%s\" must be dhcp or a dotted address like 192.168.1.50", probe);
            return 0;
        }
    }
    if (!apply_period(args, &period, err, err_len)) { return 0; }
    eth.mode = mode;
    eth.period = period;
    eth_port = port;
    eth_static = is_static;
    memcpy(eth_static_ip, ip, sizeof(ip));
    return 1;
}

static int eth_start(const char *args, char *err, size_t err_len)
{
    if (!eth_apply(args, err, err_len)) { return 0; }
    fx_eth_address(eth_static ? eth_static_ip : NULL);
    if (!fx_eth_listen((uint16_t)eth_port, err, err_len)) {
        return 0;
    }
    stream_reset(&eth);
    return 1;
}

static int eth_set(const char *args, char *err, size_t err_len)
{
    uint32_t was = eth_port;

    if (!eth_apply(args, err, err_len)) { return 0; }
    fx_eth_address(eth_static ? eth_static_ip : NULL);
    return eth_port == was || fx_eth_listen((uint16_t)eth_port, err, err_len);
}

static void eth_stop(void)
{
    fx_eth_close();
}

static void eth_tick(uint32_t now)
{
    char body[BODY_MAX], ip[20];
    uint32_t kbps;

    stream_pump(&eth);
    if (!due(now, &eth.due_ms, eth.period)) { return; }
    kbps = stream_period(&eth, now, fx_eth_connected());
    fx_eth_ip(ip, sizeof(ip));
    echo_fields(&eth.echo, body, sizeof(body));
    add(body, sizeof(body), " ip=%s link=%d conn=%d mode=%s port=%lu rx_bytes=%lu tx_bytes=%lu"
        " kbps=%lu drop=%lu", ip, fx_eth_link(), fx_eth_connected(),
        mode_name(eth.mode), (unsigned long)eth_port, (unsigned long)eth.rx_bytes,
        (unsigned long)eth.tx_bytes, (unsigned long)kbps, (unsigned long)eth.drop);
    frame("eth", now, body);
}

static void eth_caps(char *out, size_t len)
{
    char addr[20];

    if (eth_static) {
        snprintf(addr, sizeof(addr), "%u.%u.%u.%u", eth_static_ip[0], eth_static_ip[1],
                 eth_static_ip[2], eth_static_ip[3]);
    } else {
        snprintf(addr, sizeof(addr), "dhcp");
    }
    snprintf(out, len, "mode=%s port=%lu ip=%s period=%lu", mode_name(eth.mode),
             (unsigned long)eth_port, addr, (unsigned long)eth.period);
}

/* ip= takes a dotted address as well as dhcp, which no limit shape expresses,
 * so it is left to the check above - as on the fixture firmware. */
static void eth_limits(char *out, size_t len)
{
    snprintf(out, len, "mode:echo|mirror port:1..65535 period:%lu..", (unsigned long)FX_PERIOD_MIN_MS);
}

/* usb */

static stream_t usb = { fx_usb_avail, fx_usb_room, fx_usb_read, fx_usb_write, MODE_ECHO, 1000U, 0,
                        0, 0, 0, 0, 0, { 0, 0 }, { 0, 0, 0, 0, 0, 0 } };
static const char *usb_last_state;
static uint32_t usb_enum;
static int usb_enum_armed;

static int usb_apply(const char *args, char *err, size_t err_len)
{
    uint32_t period = usb.period;
    int mode = usb.mode;

    if (!stream_mode(args, &mode, 1, err, err_len)) { return 0; }
    if (!apply_period(args, &period, err, err_len)) { return 0; }
    usb.mode = mode;
    usb.period = period;
    return 1;
}

static int usb_start(const char *args, char *err, size_t err_len)
{
    if (!usb_apply(args, err, err_len)) { return 0; }
    fx_usb_begin();
    stream_reset(&usb);
    usb_last_state = fx_usb_state();
    usb_enum = 0U;
    usb_enum_armed = 0;
    return 1;
}

static int usb_set(const char *args, char *err, size_t err_len)
{
    return usb_apply(args, err, err_len);
}

static void usb_stop(void) {}

static void usb_tick(uint32_t now)
{
    char body[BODY_MAX];
    const char *st = fx_usb_state();
    uint32_t kbps;

    /* Counts re-enumerations once the port has been configured the first time. */
    if (strcmp(st, usb_last_state) != 0) {
        usb_last_state = st;
        if (usb_enum_armed) { usb_enum++; }
    }
    if (!usb_enum_armed && strcmp(st, "cfg") == 0) {
        usb_enum_armed = 1;
        usb_enum = 0U;
    }
    stream_pump(&usb);
    if (!due(now, &usb.due_ms, usb.period)) { return; }
    kbps = stream_period(&usb, now, strcmp(st, "cfg") == 0);
    echo_fields(&usb.echo, body, sizeof(body));
    add(body, sizeof(body), " state=%s enum=%lu mode=%s rx_bytes=%lu tx_bytes=%lu kbps=%lu"
        " busy=%lu drop=%lu", st, (unsigned long)usb_enum, mode_name(usb.mode),
        (unsigned long)usb.rx_bytes, (unsigned long)usb.tx_bytes, (unsigned long)kbps,
        (unsigned long)usb.busy, (unsigned long)usb.drop);
    frame("usb", now, body);
}

static void usb_caps(char *out, size_t len)
{
    snprintf(out, len, "mode=%s period=%lu", mode_name(usb.mode), (unsigned long)usb.period);
}

static void usb_limits(char *out, size_t len)
{
    snprintf(out, len, "mode:echo|mirror|info period:%lu..", (unsigned long)FX_PERIOD_MIN_MS);
}

/* sd -- the card-detect switch */

static uint32_t sd_period = 500U, sd_due, sd_changes, sd_in, sd_out;
static int sd_last = -1;
static echo_t sd_echo;

static int sd_apply(const char *args, char *err, size_t err_len)
{
    return apply_period(args, &sd_period, err, err_len);
}

static int sd_start(const char *args, char *err, size_t err_len)
{
    if (!sd_apply(args, err, err_len)) { return 0; }
    sd_changes = sd_in = sd_out = 0U;
    sd_last = -1;
    echo_reset(&sd_echo);
    sd_due = fx_millis();
    return 1;
}

static void sd_stop(void) {}

static void sd_tick(uint32_t now)
{
    char body[BODY_MAX];
    int det = fx_sd_detected() ? 1 : 0;

    if (sd_last < 0) {
        sd_last = det;
    } else if (det != sd_last) {
        sd_changes++;
        if (det) { sd_in++; } else { sd_out++; }
        sd_last = det;
    }
    if (!due(now, &sd_due, sd_period)) { return; }
    echo_tick(&sd_echo);
    echo_fields(&sd_echo, body, sizeof(body));
    add(body, sizeof(body), " detected=%d changes=%lu in=%lu out=%lu", det,
        (unsigned long)sd_changes, (unsigned long)sd_in, (unsigned long)sd_out);
    frame("sd", now, body);
}

static void sd_echo_got(uint32_t v) { echo_got(&sd_echo, v); }

static void sd_caps(char *out, size_t len)
{
    snprintf(out, len, "period=%lu", (unsigned long)sd_period);
}

static void sd_limits(char *out, size_t len)
{
    snprintf(out, len, "period:%lu..", (unsigned long)FX_PERIOD_MIN_MS);
}

/* ---- pt.run targets, the fixture firmware's porttool_run.c --------------
 *
 * Each measures, then writes the k=v body of its OK line; none prints a
 * verdict (DECISIONS.md 22). Field names and defaults follow the fixture
 * firmware's $BOOT TestCase/{ETH,SD,SDRAM}; the differences are in
 * PORTTOOL-FLOW.md A.9. */

static uint32_t xorshift(uint32_t *state)
{
    uint32_t x = *state;

    x ^= x << 13;
    x ^= x >> 17;
    x ^= x << 5;
    *state = x;
    return x;
}

static uint32_t crc32_update(uint32_t crc, const uint8_t *data, uint32_t len)
{
    for (uint32_t i = 0; i < len; i++) {
        crc ^= data[i];
        for (int b = 0; b < 8; b++) {
            crc = (crc & 1U) ? (crc >> 1) ^ 0xEDB88320UL : (crc >> 1);
        }
    }
    return crc;
}

/* eth.link -- the PHY over MDIO: scanned, not assumed at one address. */

static uint32_t mdio_errors;

static int phy_read(uint32_t addr, uint32_t reg, uint16_t *val)
{
    if (!fx_eth_phy_read(addr, reg, val)) {
        mdio_errors++;
        return 0;
    }
    return 1;
}

static void run_eth_link(const char *args, char *out, size_t len)
{
    uint16_t id1 = 0, id2 = 0, bsr = 0, scsr = 0, bcr = 0;
    uint32_t addr, speed = 0;
    int found = 0, fd = 0;

    (void)args;
    for (addr = 0; addr < 32U && !found; addr++) {
        if (!phy_read(addr, 2U, &id1) || !phy_read(addr, 3U, &id2)) { continue; }
        found = !((id1 == 0xFFFFU && id2 == 0xFFFFU) || (id1 == 0U && id2 == 0U));
    }
    if (found) {
        addr--;
        (void)phy_read(addr, 0U, &bcr);
        (void)phy_read(addr, 1U, &bsr);
        (void)phy_read(addr, 31U, &scsr);
        switch ((scsr >> 2) & 0x7U) {   /* LAN8742A SCSR speed indication */
        case 1: speed = 10;  break;
        case 2: speed = 100; break;
        case 5: speed = 10;  fd = 1; break;
        case 6: speed = 100; fd = 1; break;
        default: break;
        }
    } else {
        addr = 0;
        id1 = id2 = 0;
    }
    snprintf(out, len, "found=%u addr=%u id=0x%08lX link=%u autoneg=%u speed=%u fd=%u"
             " bsr=0x%04X scsr=0x%04X mdio_errors=%lu",
             (unsigned)found, (unsigned)addr, (unsigned long)(((uint32_t)id1 << 16) | id2),
             (unsigned)((bsr >> 2) & 1U), (unsigned)((bsr >> 5) & 1U), (unsigned)speed,
             (unsigned)fd, (unsigned)bsr, (unsigned)scsr, (unsigned long)mdio_errors);
}

/* sd.probe / sd.integrity / sd.speed */

#define SD_FILE      "PLCTEST.BIN"
#define SD_BYTES_DEF 4096U
#define SD_CHUNK     4096U

static uint8_t sd_buf[SD_CHUNK], sd_want[SD_CHUNK];

/* What is on the card, from the boot sector rather than from FatFs, so an
 * exFAT or unformatted card is told from a dead one. */
static const char *sd_fs_from_sector(const uint8_t *s)
{
    if (s[510] != 0x55U || s[511] != 0xAAU)          { return "none"; }
    if (memcmp(&s[3], "EXFAT   ", 8) == 0)           { return "exfat"; }
    if (memcmp(&s[82], "FAT32   ", 8) == 0)          { return "fat32"; }
    if (memcmp(&s[54], "FAT16   ", 8) == 0)          { return "fat16"; }
    if (memcmp(&s[54], "FAT12   ", 8) == 0)          { return "fat12"; }
    if (memcmp(&s[54], "FAT     ", 8) == 0)          { return "fat16"; }
    return "unknown";
}

static uint32_t sd_le32(const uint8_t *p)
{
    return (uint32_t)p[0] | ((uint32_t)p[1] << 8) | ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24);
}

/* Not a boot sector itself: follow the first partition. 0xEE is a GPT disk's
 * protective entry; GPT's first partition is found through its header (LBA 1,
 * entry array LBA at offset 72) and first entry (start LBA at offset 32), as
 * the fixture firmware does ($BOOT TestCase/SD/sd_test.c sd_read_fs_type). */
static const char *sd_fs_type(void)
{
    const char *kind;
    uint32_t lba;

    if (!fx_sd_read_block(0U, sd_buf)) { return "none"; }
    kind = sd_fs_from_sector(sd_buf);
    if (strcmp(kind, "unknown") != 0 || sd_buf[0x1C2] == 0U) { return kind; }
    if (sd_buf[0x1C2] == 0xEEU) {
        if (!fx_sd_read_block(1U, sd_buf) || memcmp(sd_buf, "EFI PART", 8) != 0) { return "unknown"; }
        if (!fx_sd_read_block(sd_le32(&sd_buf[72]), sd_buf)) { return "unknown"; }
        lba = sd_le32(&sd_buf[32]);
    } else {
        lba = sd_le32(&sd_buf[0x1C6]);
    }
    if (lba == 0U || !fx_sd_read_block(lba, sd_buf)) { return "unknown"; }
    return sd_fs_from_sector(sd_buf);
}

static void run_sd_probe(const char *args, char *out, size_t len)
{
    fx_sd_card_t c;
    const char *fs = "none";
    int detected = fx_sd_detected() ? 1 : 0;
    int ready;

    (void)args;
    memset(&c, 0, sizeof(c));
    ready = fx_sd_identify(&c) ? 1 : 0;
    if (ready) {
        fs = sd_fs_type();
    } else {
        memset(&c, 0, sizeof(c));
    }
    fx_sd_release();
    snprintf(out, len, "detected=%u ready=%u blocks=%lu block_size=%lu mib=%lu v2x=%u class=%lu fs=%s",
             (unsigned)detected, (unsigned)ready, (unsigned long)c.blocks,
             (unsigned long)c.block_size,
             (unsigned long)(((uint64_t)c.blocks * c.block_size) / (1024U * 1024U)),
             (unsigned)(c.v2x ? 1 : 0), (unsigned long)c.card_class, fs);
}

struct sd_round_t {
    int mounted, wrote, read_back, identical, fresult;
    uint32_t bytes, write_crc, read_crc;
};

/* One write / read / verify round; the read pass regenerates the data from
 * the seed rather than holding it. */
static int sd_round(uint32_t bytes, sd_round_t *r)
{
    static uint32_t rng;
    uint32_t seed, left, chunk, done = 0;
    uint32_t crc_w = 0xFFFFFFFFUL, crc_r = 0xFFFFFFFFUL;
    int fr, ok = 1;

    memset(r, 0, sizeof(*r));
    r->bytes = bytes;
    if (!fx_sd_mount()) {
        r->fresult = -1;
        return 0;
    }
    r->mounted = 1;
    if (rng == 0U) {
        rng = fx_millis() | 1U;
    }
    seed = rng;
    fr = fx_sd_open(SD_FILE, 1);
    if (fr != 0) {
        r->fresult = fr;
        return 0;
    }
    for (left = bytes; left > 0U && ok; left -= chunk) {
        chunk = left > SD_CHUNK ? SD_CHUNK : left;
        for (uint32_t i = 0; i < chunk; i++) { sd_buf[i] = (uint8_t)xorshift(&rng); }
        crc_w = crc32_update(crc_w, sd_buf, chunk);
        fr = fx_sd_write(sd_buf, chunk, &done);
        if (fr != 0 || done != chunk) { r->fresult = fr; ok = 0; break; }
    }
    (void)fx_sd_close();
    if (!ok) { return 0; }
    r->wrote = 1;
    r->write_crc = crc_w ^ 0xFFFFFFFFUL;

    rng = seed;
    fr = fx_sd_open(SD_FILE, 0);
    if (fr != 0) {
        r->fresult = fr;
        return 0;
    }
    for (left = bytes; left > 0U && ok; left -= chunk) {
        chunk = left > SD_CHUNK ? SD_CHUNK : left;
        memset(sd_buf, 0, chunk);
        fr = fx_sd_read(sd_buf, chunk, &done);
        if (fr != 0 || done != chunk) { r->fresult = fr; ok = 0; break; }
        crc_r = crc32_update(crc_r, sd_buf, chunk);
        for (uint32_t i = 0; i < chunk; i++) { sd_want[i] = (uint8_t)xorshift(&rng); }
        if (memcmp(sd_buf, sd_want, chunk) != 0) { ok = 0; break; }
    }
    (void)fx_sd_close();
    if (!ok) { return 0; }
    r->read_back = 1;
    r->read_crc = crc_r ^ 0xFFFFFFFFUL;
    r->identical = r->read_crc == r->write_crc;
    return r->identical;
}

/* One round keeps the CRCs, many rounds keep the round numbers. */
static void run_sd_integrity(const char *args, char *out, size_t len)
{
    uint32_t bytes = 0, passes = 0, passed = 0, total = 0, first_bad = 0, start;
    sd_round_t r;

    (void)get_u32(args, "bytes", &bytes);
    (void)get_u32(args, "passes", &passes);
    if (bytes == 0U)  { bytes = SD_BYTES_DEF; }
    if (passes == 0U) { passes = 1U; }

    if (passes == 1U) {
        (void)sd_round(bytes, &r);
        snprintf(out, len, "mounted=%u identical=%u passes=1 passed=%u wrote=%u read_back=%u"
                 " bytes_each=%lu bytes_total=%lu write_crc=0x%08lX read_crc=0x%08lX fresult=%d",
                 (unsigned)r.mounted, (unsigned)r.identical, (unsigned)r.identical,
                 (unsigned)r.wrote, (unsigned)r.read_back, (unsigned long)bytes,
                 (unsigned long)bytes, (unsigned long)r.write_crc, (unsigned long)r.read_crc,
                 r.fresult);
        return;
    }
    start = fx_millis();
    memset(&r, 0, sizeof(r));
    for (uint32_t i = 0; i < passes; i++) {
        if (sd_round(bytes, &r)) {
            passed++;
            total += bytes;
        } else {
            if (r.mounted) { first_bad = passes = i + 1U; } else { passes = 0U; }
            break;
        }
    }
    snprintf(out, len, "mounted=%u identical=%u passes=%lu passed=%lu bytes_each=%lu"
             " bytes_total=%lu elapsed_ms=%lu first_bad_pass=%lu fresult=%d",
             (unsigned)r.mounted, (unsigned)(passes > 0U && passed == passes),
             (unsigned long)passes, (unsigned long)passed, (unsigned long)bytes,
             (unsigned long)total, (unsigned long)(fx_millis() - start),
             (unsigned long)first_bad, r.fresult);
}

static uint32_t bps(uint32_t bytes, uint32_t ms)
{
    return ms ? (uint32_t)(((uint64_t)bytes * 1000U) / ms) : 0U;
}

/* How fast the card moves data each way; does not verify content. */
static void run_sd_speed(const char *args, char *out, size_t len)
{
    uint32_t bytes = 0, left, chunk, done = 0, t0, write_ms = 0, read_ms = 0;
    uint32_t write_bps = 0, read_bps = 0;
    int mounted = 0, fresult = 0, ok = 1, fr;

    (void)get_u32(args, "bytes", &bytes);
    if (bytes == 0U) { bytes = SD_BYTES_DEF; }
    if (!fx_sd_mount()) {
        fresult = -1;
        ok = 0;
    } else {
        mounted = 1;
        memset(sd_buf, 0xA5, sizeof(sd_buf));
        fr = fx_sd_open(SD_FILE, 1);
        if (fr != 0) {
            fresult = fr;
            ok = 0;
        } else {
            t0 = fx_millis();
            for (left = bytes; left > 0U && ok; left -= chunk) {
                chunk = left > SD_CHUNK ? SD_CHUNK : left;
                fr = fx_sd_write(sd_buf, chunk, &done);
                if (fr != 0 || done != chunk) { fresult = fr; ok = 0; break; }
            }
            if (ok && fx_sd_sync() != 0) { ok = 0; }
            write_ms = fx_millis() - t0;
            (void)fx_sd_close();
        }
        if (ok) {
            write_bps = bps(bytes, write_ms);
            fr = fx_sd_open(SD_FILE, 0);
            if (fr != 0) {
                fresult = fr;
                ok = 0;
            } else {
                t0 = fx_millis();
                for (left = bytes; left > 0U && ok; left -= chunk) {
                    chunk = left > SD_CHUNK ? SD_CHUNK : left;
                    fr = fx_sd_read(sd_buf, chunk, &done);
                    if (fr != 0 || done != chunk) { fresult = fr; ok = 0; break; }
                }
                read_ms = fx_millis() - t0;
                (void)fx_sd_close();
            }
        }
        if (ok) { read_bps = bps(bytes, read_ms); }
    }
    snprintf(out, len, "mounted=%u bytes=%lu write_ms=%lu read_ms=%lu write_bps=%lu read_bps=%lu fresult=%d",
             (unsigned)mounted, (unsigned long)bytes, (unsigned long)write_ms,
             (unsigned long)read_ms, (unsigned long)write_bps, (unsigned long)read_bps, fresult);
}

/* sdram.probe / sdram.sweep -- the 64 MiB AS4C32M16SB at 0xC0000000. */

#define SDRAM_BASE  0xC0000000UL
#define SDRAM_BYTES 0x04000000UL

/* Walking 1 and walking 0 on D0..D15 at one cell. */
static int sdram_databus(volatile uint32_t *mem)
{
    volatile uint16_t *cell = (volatile uint16_t *)mem + 0x123U;
    int ok = 1;

    for (uint32_t bit = 0; bit < 16U; bit++) {
        uint16_t one = (uint16_t)(1U << bit), zero = (uint16_t)~one;
        *cell = one;
        ok &= *cell == one;
        *cell = zero;
        ok &= *cell == zero;
    }
    return ok;
}

/* A distinct value at every power-of-two word offset and the top word. */
static int sdram_addrbus(volatile uint32_t *mem)
{
    volatile uint16_t *base = (volatile uint16_t *)mem;
    uint32_t top = SDRAM_BYTES / 2U - 1U;
    int ok = 1;

    base[0] = 0x1234U;
    for (uint32_t bit = 0; bit < 25U; bit++) {
        base[1UL << bit] = (uint16_t)(0xA500U | bit);
    }
    base[top] = 0x5A5AU;
    ok &= base[0] == 0x1234U;
    for (uint32_t bit = 0; bit < 25U; bit++) {
        ok &= base[1UL << bit] == (uint16_t)(0xA500U | bit);
    }
    ok &= base[top] == 0x5A5AU;
    return ok;
}

static void run_sdram_probe(const char *args, char *out, size_t len)
{
    volatile uint32_t *mem = fx_sdram();
    int databus = 0, addrbus = 0;

    (void)args;
    if (mem != NULL) {
        databus = sdram_databus(mem);
        addrbus = sdram_addrbus(mem);
    }
    snprintf(out, len, "base=0x%08lX size=%lu ready=%u databus=%u addrbus=%u",
             (unsigned long)SDRAM_BASE, (unsigned long)SDRAM_BYTES, (unsigned)(mem != NULL),
             (unsigned)databus, (unsigned)addrbus);
}

/* The whole array, four patterns: several seconds on the board. */
static void run_sdram_sweep(const char *args, char *out, size_t len)
{
    static const uint32_t patterns[] = { 0x00000000UL, 0xFFFFFFFFUL, 0x55555555UL, 0xAAAAAAAAUL };
    volatile uint32_t *mem = fx_sdram();
    uint32_t words = SDRAM_BYTES / 4U, done = 0, mismatches = 0, first_bad = 0, bad_pattern = 0;
    uint32_t write_ms = 0, verify_ms = 0;

    (void)args;
    for (size_t p = 0; mem != NULL && p < sizeof(patterns) / sizeof(patterns[0]); p++) {
        uint32_t t0 = fx_millis(), v = patterns[p];
        for (uint32_t i = 0; i < words; i++) { mem[i] = v; }
        write_ms += fx_millis() - t0;
        t0 = fx_millis();
        for (uint32_t i = 0; i < words; i++) {
            if (mem[i] != v) {
                if (mismatches == 0U) { first_bad = i * 4U; bad_pattern = v; }
                mismatches++;
            }
        }
        verify_ms += fx_millis() - t0;
        done++;
    }
    snprintf(out, len, "ready=%u patterns=%lu words_each=%lu mismatches=%lu first_bad=0x%08lX"
             " bad_pattern=0x%08lX write_ms=%lu verify_ms=%lu", (unsigned)(mem != NULL),
             (unsigned long)done, (unsigned long)(mem != NULL ? words : 0U),
             (unsigned long)mismatches, (unsigned long)first_bad, (unsigned long)bad_pattern,
             (unsigned long)write_ms, (unsigned long)verify_ms);
}

/* sdram -- the retention session, as the fixture firmware's porttool_sdram.c:
 * random words written, left wait= ms, read back, round after round. Through
 * OpenPLC_SDRAM, which sets the SDRAM refresh itself (decision 184). */

#define SDRAM_WAIT_MIN_MS 1000U   /* below a second a board with no refresh would still pass */
#define SDRAM_HOLD_WORDS  64U

enum { SDRAM_WRITE, SDRAM_WAIT, SDRAM_VERIFY };

static uint32_t sdram_wait = 5000U, sdram_period = 1000U, sdram_due, sdram_until;
static uint32_t sdram_rng, sdram_seed, sdram_cycles, sdram_checked, sdram_failed, sdram_first_bad;
static uint32_t sdram_word[SDRAM_HOLD_WORDS];
static int sdram_phase, sdram_ready;
static echo_t sdram_echo;

static uint32_t sdram_rand(void)
{
    uint32_t x = sdram_rng;
    x ^= x << 13;
    x ^= x >> 17;
    x ^= x << 5;
    sdram_rng = x;
    return x;
}

/* What each word holds: its board address, so a word landing elsewhere shows. */
static uint32_t sdram_signature(uint32_t word) { return (SDRAM_BASE + word * 4U) ^ 0xDEADBEEFUL; }

static int sdram_apply(const char *args, char *err, size_t err_len)
{
    char probe[32];
    uint32_t v, wait = sdram_wait, period = sdram_period;

    if (get_str(args, "wait", probe, sizeof(probe))) {
        if (!parse_u32(probe, &v)) {
            snprintf(err, err_len, "wait=\"%s\" is not a number", probe);
            return 0;
        }
        /* Refused rather than clamped: the plan should learn its wait is wrong. */
        if (v < SDRAM_WAIT_MIN_MS) {
            snprintf(err, err_len,
                     "wait=%lu is below %lu ms - cells hold for tens of "
                     "milliseconds unrefreshed, so a shorter wait passes on a "
                     "board whose refresh never started",
                     (unsigned long)v, (unsigned long)SDRAM_WAIT_MIN_MS);
            return 0;
        }
        wait = v;
    }
    if (!apply_period(args, &period, err, err_len)) { return 0; }
    sdram_wait = wait;
    sdram_period = period;
    return 1;
}

static int sdram_start(const char *args, char *err, size_t err_len)
{
    if (!sdram_apply(args, err, err_len)) { return 0; }
    if (fx_sdram() == NULL) {
        snprintf(err, err_len, "the FMC controller is not brought up, so nothing written "
                 "would mean anything");
        return 0;
    }
    if (sdram_rng == 0U) {
        sdram_rng = fx_millis() | 1U;   /* xorshift needs a non-zero seed */
    }
    sdram_ready = 1;
    sdram_cycles = sdram_checked = sdram_failed = sdram_first_bad = sdram_seed = 0U;
    echo_reset(&sdram_echo);
    sdram_phase = SDRAM_WRITE;
    sdram_due = fx_millis();
    return 1;
}

static void sdram_stop(void) { sdram_ready = 0; }

static void sdram_tick(uint32_t now)
{
    static const char *const phase_name[] = { "write", "wait", "verify" };
    volatile uint32_t *mem = fx_sdram();
    char body[BODY_MAX];

    /* Every pass, so the round's timing does not follow the frame rate. */
    if (sdram_phase == SDRAM_WRITE) {
        sdram_seed = sdram_rng;
        sdram_checked = SDRAM_HOLD_WORDS;
        for (uint32_t i = 0; i < SDRAM_HOLD_WORDS; i++) {
            sdram_word[i] = sdram_rand() % (SDRAM_BYTES / 4U);
            mem[sdram_word[i]] = sdram_signature(sdram_word[i]);
        }
        sdram_until = now + sdram_wait;
        sdram_phase = SDRAM_WAIT;
    } else if (sdram_phase == SDRAM_WAIT) {
        if ((int32_t)(now - sdram_until) >= 0) { sdram_phase = SDRAM_VERIFY; }
    } else {
        for (uint32_t i = 0; i < SDRAM_HOLD_WORDS; i++) {
            if (mem[sdram_word[i]] != sdram_signature(sdram_word[i])) {
                if (sdram_first_bad == 0U) { sdram_first_bad = SDRAM_BASE + sdram_word[i] * 4U; }
                sdram_failed++;   /* summed over the whole run, so one bad round stays visible */
            }
        }
        sdram_cycles++;
        sdram_phase = SDRAM_WRITE;
    }

    if (!due(now, &sdram_due, sdram_period)) { return; }
    echo_tick(&sdram_echo);
    echo_fields(&sdram_echo, body, sizeof(body));
    add(body, sizeof(body), " ready=%u phase=%s cycles=%lu checked=%lu failed=%lu"
        " first_bad=0x%08lX wait_ms=%lu seed=0x%08lX", (unsigned)sdram_ready,
        phase_name[sdram_phase], (unsigned long)sdram_cycles, (unsigned long)sdram_checked,
        (unsigned long)sdram_failed, (unsigned long)sdram_first_bad, (unsigned long)sdram_wait,
        (unsigned long)sdram_seed);
    frame("sdram", now, body);
}

static void sdram_echo_got(uint32_t v) { echo_got(&sdram_echo, v); }

static void sdram_caps(char *out, size_t len)
{
    snprintf(out, len, "wait=%lu period=%lu", (unsigned long)sdram_wait, (unsigned long)sdram_period);
}

static void sdram_limits(char *out, size_t len)
{
    snprintf(out, len, "wait:%lu.. period:%lu..", (unsigned long)SDRAM_WAIT_MIN_MS,
             (unsigned long)FX_PERIOD_MIN_MS);
}

struct run_t {
    const char *name, *port;
    void (*body)(const char *args, char *out, size_t len);
    const char *what;
};

/* Texts as on the fixture firmware's pt.run list. */
static const run_t runs[] = {
    { "sd.probe",     "sd",    run_sd_probe,
      "detect pin, card identification, capacity and class" },
    { "sd.integrity", "sd",    run_sd_integrity,
      "write/read/verify through FatFs, passes= rounds of bytes= each" },
    { "sd.speed",     "sd",    run_sd_speed,
      "how fast the card moves data each way; does not verify content" },
    { "sdram.probe",  "sdram", run_sdram_probe,
      "FMC bring-up, data bus and address bus; reports the window and three flags" },
    { "sdram.sweep",  "sdram", run_sdram_sweep,
      "all 64 MiB, four patterns; reports mismatches and where the first was" },
    { "eth.link",     "eth",   run_eth_link,
      "PHY identity, link, and the speed/duplex auto-negotiation settled on" },
};
#define RUN_COUNT (sizeof(runs) / sizeof(runs[0]))

/* "a,b,c" of one port's targets; returns how many. */
static uint32_t runs_for(const char *port, char *out, size_t len)
{
    uint32_t n = 0;

    out[0] = '\0';
    for (size_t i = 0; i < RUN_COUNT; i++) {
        if (strcmp(runs[i].port, port) == 0) {
            add(out, len, "%s%s", n ? "," : "", runs[i].name);
            n++;
        }
    }
    return n;
}

/* Shape fields match the fixture firmware's port table; T4-05 checks them. */
static port_t ports[] = {
    { "din",   "upper",  "D", "D02-D09", NULL, "ch,period", "ctrl", DIN_COUNT,
      din_start, din_apply, din_stop, din_tick, din_echo_got, din_caps, din_limits, 0 },
    { "dout",  "lower",  "A", "A03-A10", NULL, "ch,mode,duty,period", "ctrl", DOUT_COUNT,
      dout_start, dout_set, dout_stop, dout_tick, dout_echo_got, dout_caps, dout_limits, 0 },
    { "relay", "lower",  "B", "B01-B12", "B01+B02,B03+B04,B05+B06,B07+B08,B09+B10,B11+B12",
      "ch,mode,on,close,open,count", "ctrl", RELAY_COUNT,
      relay_start, relay_set, relay_stop, relay_tick, relay_echo_got, relay_caps, relay_limits, 0 },
    { "ain",   "upper",  "D", "D12,D13", "D12,D13", "ch,period", "ctrl", AIN_COUNT,
      ain_start, ain_apply, ain_stop, ain_tick, ain_echo_got, ain_caps, ain_limits, 0 },
    { "aout",  "upper",  "D", "D14,D15", "D14,D15", "ch,mv,period", "ctrl", AOUT_COUNT,
      aout_start, aout_set, aout_stop, aout_tick, aout_echo_got, aout_caps, aout_limits, 0 },
    { "temp",  "lower",  "-", "-", "SC-protect,HS-switch", "ch,period", "ctrl", TEMP_COUNT,
      temp_start, temp_apply, temp_stop, temp_tick, temp_echo_got, temp_caps, temp_limits, 0 },
    { "rs232", "upper",  "C", "C05,C06", "C05+C06", "baud,period", "self", 1U,
      rs232_start, rs232_set, rs232_stop, rs232_tick, rs232_echo_got, rs232_caps, rs232_limits, 0 },
    { "rs485", "upper",  "C", "C10,C11", "C10+C11", "baud,period", "link", 1U,
      rs485_start, rs485_set, rs485_stop, rs485_tick, NULL, rs485_caps, rs485_limits, 0 },
    { "can",   "upper",  "C", "C07,C08", "C07+C08", "baud,mode,period", "link", 1U,
      can_start, can_set, can_stop, can_tick, NULL, can_caps, can_limits, 0 },
    { "eth",   "bridge", "-", "J1", NULL, "mode,port,ip,period", "link", 1U,
      eth_start, eth_set, eth_stop, eth_tick, NULL, eth_caps, eth_limits, 0 },
    { "usb",   "bridge", "-", "J2", NULL, "mode,period", "link", 1U,
      usb_start, usb_set, usb_stop, usb_tick, NULL, usb_caps, usb_limits, 0 },
    { "sd",    "bridge", "-", "J6", NULL, "period", "ctrl", 1U,
      sd_start, sd_apply, sd_stop, sd_tick, sd_echo_got, sd_caps, sd_limits, 0 },    { "sdram", "bridge", "-", "U6", NULL, "wait,period", "ctrl", 1U,
      sdram_start, sdram_apply, sdram_stop, sdram_tick, sdram_echo_got, sdram_caps, sdram_limits, 0 },
};
#define PORT_COUNT (sizeof(ports) / sizeof(ports[0]))

static port_t *find_port(const char *name)
{
    for (size_t i = 0; i < PORT_COUNT; i++) {
        if (strcmp(ports[i].name, name) == 0) {
            return &ports[i];
        }
    }
    return NULL;
}

static void stop_all(void)
{
    for (size_t i = 0; i < PORT_COUNT; i++) {
        if (ports[i].running) {
            ports[i].stop();
        }
        ports[i].running = 0;
    }
}

/* ---- commands ------------------------------------------------------------ */

static void reply_caps(void)
{
    char buf[FX_REPLY_MAX], targets[96];
    uint32_t lines = 0U;

    for (size_t i = 0; i < PORT_COUNT; i++) {
        lines += 3U + (ports[i].terms != NULL ? 1U : 0U);   /* port=, vals=, limits=, terms= */
    }
    say("OK porttool=%s ports=%lu lines=%lu hold=1 led=1 demo=%s", FX_PROTOCOL_VERSION,
        (unsigned long)PORT_COUNT, (unsigned long)lines, FX_DEMO_VERSION);
    for (size_t i = 0; i < PORT_COUNT; i++) {
        const port_t *p = &ports[i];
        snprintf(buf, sizeof(buf), "OK port=%s board=%s kind=session blk=%s term=%s channels=%lu"
                 " loop=%s params=%s running=%d", p->name, p->board, p->blk, p->term,
                 (unsigned long)p->channels, p->loop, p->params, p->running);
        if (runs_for(p->name, targets, sizeof(targets)) > 0U) {
            add(buf, sizeof(buf), " runs=%s", targets);
        }
        say("%s", buf);
        p->caps(buf, sizeof(buf));
        say("OK vals=%s %s", p->name, buf);
        if (p->terms != NULL) {
            say("OK terms=%s %s", p->name, p->terms);
        }
        p->limits(buf, sizeof(buf));
        say("OK limits=%s %s", p->name, buf);
    }
}

static void reply_id(void)
{
    uint32_t uid[3];

    fx_uid(uid);
    say("OK uid=%08lX%08lX%08lX porttool=%s demo=%s", (unsigned long)uid[0], (unsigned long)uid[1],
        (unsigned long)uid[2], FX_PROTOCOL_VERSION, FX_DEMO_VERSION);
}

static void reply_list(void)
{
    int any = 0;

    for (size_t i = 0; i < PORT_COUNT; i++) {
        if (ports[i].running) {
            say("OK running=%s", ports[i].name);
            any = 1;
        }
    }
    if (!any) {
        say("OK running=none");
    }
}

static void cmd_start(const char *rest)
{
    char name[24], err[160];
    const char *args = word(rest, name, sizeof(name));
    port_t *p = find_port(name);

    if (p == NULL) {
        say("ERR no such port \"%s\"", name);
        return;
    }
    err[0] = '\0';
    if (!p->start(args, err, sizeof(err))) {
        say("ERR %s start refused: %s", p->name, err[0] ? err : "no reason given");
        return;
    }
    p->running = 1;
    say("OK started %s%s", p->name, line_suffix());
}

static void cmd_set(const char *rest)
{
    char name[24], err[160];
    const char *args = word(rest, name, sizeof(name));
    port_t *p = find_port(name);

    if (p == NULL) {
        say("ERR no such port \"%s\"", name);
        return;
    }
    if (!p->running) {
        say("ERR %s is not running", p->name);
        return;
    }
    err[0] = '\0';
    if (!p->set(args, err, sizeof(err))) {
        say("ERR %s set refused: %s", p->name, err[0] ? err : "no reason given");
        return;
    }
    say("OK set %s%s", p->name, line_suffix());
}

/* Refused for a loop=link port: its reply must come back over the link under
 * test, or the counter would climb with that link dead. */
static void cmd_echo(const char *rest)
{
    char name[24], value[24];
    const char *args = word(rest, name, sizeof(name));
    port_t *p = find_port(name);
    uint32_t v;

    if (p == NULL) {
        say("ERR no such port \"%s\"", name);
        return;
    }
    if (p->echo == NULL) {
        say("ERR %s takes its echo on its own link, not on this one", p->name);
        return;
    }
    if (!p->running) {
        say("ERR %s is not running", p->name);
        return;
    }
    word(args, value, sizeof(value));
    if (value[0] == '\0') {
        say("ERR %s echo needs a number", p->name);
        return;
    }
    if (!parse_u32(value, &v)) {
        say("ERR %s echo value \"%s\" is not a number", p->name, value);
        return;
    }
    p->echo(v);
    say("OK echo %s %lu", p->name, (unsigned long)v);
}

static void cmd_stop(const char *rest)
{
    char name[24];
    port_t *p;

    word(rest, name, sizeof(name));
    if (strcmp(name, "all") == 0) {
        stop_all();
        say("OK stopped all%s", line_suffix());
        return;
    }
    p = find_port(name);
    if (p == NULL) {
        say("ERR no such port \"%s\"", name);
        return;
    }
    if (p->running) {
        p->stop();
    }
    p->running = 0;
    say("OK stopped %s%s", p->name, line_suffix());
}

/* The deadman: when the PC stops renewing, every output is released and the
 * indicator lit. This decides whether anyone is listening, never whether the
 * board is good (DECISIONS.md 37). */
#define HOLD_MAX_MS 600000U
static uint32_t hold_span, hold_until;
static int hold_armed;

static void cmd_hold(const char *rest)
{
    char w[24];
    uint32_t ms;

    word(rest, w, sizeof(w));
    if (w[0] == '\0') {
        if (!hold_armed) {
            say("OK hold=off");
            return;
        }
        int32_t left = (int32_t)(hold_until - fx_millis());
        say("OK hold=%lu left=%ld", (unsigned long)hold_span, (long)(left < 0 ? 0 : left));
        return;
    }
    if (!parse_u32(w, &ms)) {
        say("ERR hold=\"%s\" is not a number of milliseconds", w);
        return;
    }
    if (ms == 0U) {
        hold_armed = 0;
        hold_span = 0U;
        say("OK hold=off");
        return;
    }
    if (ms > HOLD_MAX_MS) {
        say("ERR hold=%lu is over the %lu ms ceiling", (unsigned long)ms, (unsigned long)HOLD_MAX_MS);
        return;
    }
    hold_span = ms;
    hold_until = fx_millis() + ms;
    hold_armed = 1;
    say("OK hold=%lu", (unsigned long)ms);
}

static void hold_check(uint32_t now)
{
    if (!hold_armed || (int32_t)(now - hold_until) < 0) {
        return;
    }
    hold_armed = 0;
    stop_all();
    fx_led(1);
    say("!hold t=%lu expired=1 span=%lu", (unsigned long)now, (unsigned long)hold_span);
}

static void cmd_led(const char *rest)
{
    char w[24];
    uint32_t on;

    word(rest, w, sizeof(w));
    if (!get_u32(w, "fault", &on) || on > 1U) {
        say("ERR pt.led takes fault=0 or fault=1");
        return;
    }
    fx_led((int)on);
    say("OK led fault=%lu", (unsigned long)on);
}

/* The calibration area's bytes as they sit in flash; the PC decodes them. */
static void reply_cal(void)
{
    static const char digits[] = "0123456789ABCDEF";
    uint8_t area[64];
    char hex[2U * sizeof(area) + 1U];
    size_t n = fx_cal_area(area, sizeof(area));

    for (size_t i = 0; i < n; i++) {
        hex[2U * i] = digits[area[i] >> 4];
        hex[2U * i + 1U] = digits[area[i] & 0x0FU];
    }
    hex[2U * n] = '\0';
    say("OK cal=%s", hex);
}

/* Sessions are left alone; a target answers with one OK line. */
static void cmd_run(const char *rest)
{
    char name[32], body[FX_REPLY_MAX];
    const char *args = word(rest, name, sizeof(name));

    if (name[0] == '\0') {
        for (size_t i = 0; i < RUN_COUNT; i++) {
            say("OK run=%-16s %s", runs[i].name, runs[i].what);
        }
        return;
    }
    for (size_t i = 0; i < RUN_COUNT; i++) {
        if (strcmp(runs[i].name, name) == 0) {
            body[0] = '\0';
            runs[i].body(args, body, sizeof(body));
            say("OK %s %s", runs[i].name, body);
            return;
        }
    }
    say("ERR no such run target \"%s\" - run pt.run with no argument to list them", name);
}

static void dispatch(const char *line)
{
    char verb[24];
    const char *rest = word(line, verb, sizeof(verb));

    if (verb[0] == '\0')                    { return; }
    else if (strcmp(verb, "pt.run") == 0)   { cmd_run(rest); }
    else if (strcmp(verb, "pt.caps") == 0)  { reply_caps(); }
    else if (strcmp(verb, "pt.cal") == 0)   { reply_cal(); }
    else if (strcmp(verb, "pt.id") == 0)    { reply_id(); }
    else if (strcmp(verb, "pt.list") == 0)  { reply_list(); }
    else if (strcmp(verb, "pt.start") == 0) { cmd_start(rest); }
    else if (strcmp(verb, "pt.set") == 0)   { cmd_set(rest); }
    else if (strcmp(verb, "pt.stop") == 0)  { cmd_stop(rest); }
    else if (strcmp(verb, "pt.echo") == 0)  { cmd_echo(rest); }
    else if (strcmp(verb, "pt.hold") == 0)  { cmd_hold(rest); }
    else if (strcmp(verb, "pt.led") == 0)   { cmd_led(rest); }
    else                                    { say("ERR unknown command \"%s\"", verb); }
}

/* ---- entry points -------------------------------------------------------- */

static char line_buf[FX_LINE_MAX];
static size_t line_len;

void fx_begin(void)
{
    say("");
    say("=== port tool demo %s, protocol %s ===", FX_DEMO_VERSION, FX_PROTOCOL_VERSION);
    say("Lines starting with ! are pushed samples; everything else is log.");
    say("  pt.caps                          what this board exposes");
    say("  pt.start din ch=1,3,5 period=200 start a session");
    say("  pt.stop all                      stop every session");
}

void fx_feed(char c)
{
    line_heard_ms = fx_millis();
    line_settled = 1;
    if (c == '\r' || c == '\n') {
        if (line_len > 0U) {
            line_buf[line_len] = '\0';
            lines_in++;
            dispatch(line_buf);
            line_len = 0U;
        }
    } else if (line_len + 1U < sizeof(line_buf)) {
        line_buf[line_len++] = c;
    } else {
        /* Dropped whole rather than truncated into a command that means something else. */
        line_len = 0U;
        say("ERR line too long");
    }
}

void fx_poll(uint32_t now_ms)
{
    hold_check(now_ms);
    line_apply(now_ms);   /* commands were fed before this pass: switch before any frame */
    for (size_t i = 0; line_settled && i < PORT_COUNT; i++) {
        if (ports[i].running) {
            ports[i].tick(now_ms);
        }
    }
    line_check(now_ms);
}
