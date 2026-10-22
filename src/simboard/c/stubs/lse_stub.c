/*
 * Fake LSE drive (port_lse.c needs the HAL and is not compiled here). Every
 * drive starts; test_lse_fail_drv makes one of them time out, to exercise the
 * fallback to mlow. test_lse_freq_result is what PortLse_MeasureFreq returns;
 * on success the counts give 32767.596 Hz (-12.33 ppm).
 */

#include "port_lse.h"

int test_lse_fail_drv = -1;
int test_lse_freq_result = 1;

static int lse_drv = PORT_LSE_MLOW;
static int lse_ready = 1;

int PortLse_Restart(int drv, uint32_t *start_ms)
{
    *start_ms = 0u;
    if (drv < PORT_LSE_LOW || drv > PORT_LSE_HIGH) {
        return 0;
    }
    lse_drv = drv;
    lse_ready = (drv != test_lse_fail_drv);
    *start_ms = lse_ready ? 900u - 150u * (uint32_t)drv : PORT_LSE_START_TIMEOUT_MS;
    return lse_ready;
}

int PortLse_Drive(void) { return lse_drv; }
int PortLse_Ready(void) { return lse_ready; }

int PortLse_MeasureFreq(uint32_t window_ms, PortLseFreq *f)
{
    if (test_lse_freq_result != 1) {
        return test_lse_freq_result;
    }
    f->tclk_hz = 200000000u;
    f->periods = 32768u;
    f->ticks = 200002468u;
    f->ms = window_ms;
    return 1;
}
