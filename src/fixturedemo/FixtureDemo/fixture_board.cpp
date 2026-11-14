/*
 * fixture_board.cpp -- the hooks of fixture_hw.h, with the board package API.
 *
 * Only this file touches the board. Pins come from the variant header
 * (DIN_1, REL_1, AIN_1, ...); the analog values go through OpenPLC_Ports so
 * this board's calibration applies, which is what step 6 is there to prove.
 */

#include <Arduino.h>
#include <OpenPLC_Ports.h>
#include <openplc_analog.h>
#include <OpenPLC_CAN.h>
#include <OpenPLC_Net_Autostart.h>

#include <OpenPLC_SD.h>
#include <Sd2Card.h>
#include <OpenPLC_SDRAM.h>

#include "lwip/tcp.h"
#include "lwip/netif.h"
#include "lwip/dhcp.h"
#include "usbd_def.h"

#include "fixture_hw.h"

/* ---- control port and clock --------------------------------------------- */

void fx_out(const char *text)
{
    RS232.print(text);
}

uint32_t fx_millis(void)
{
    return millis();
}

void fx_uid(uint32_t words[3])
{
    words[0] = HAL_GetUIDw0();
    words[1] = HAL_GetUIDw1();
    words[2] = HAL_GetUIDw2();
}

/* The reply announcing the switch is already in the TX buffer; end() drains it. */
void fx_line_baud(uint32_t baud)
{
    RS232.end();
    RS232.begin(baud);
}

size_t fx_cal_area(uint8_t *out, size_t len)
{
    size_t n = sizeof(calib_area_t) < len ? sizeof(calib_area_t) : len;

    memcpy(out, (const void *)CALIB_AREA_ADDR, n);
    return n;
}

static const int led_pin = PE2;   /* system LED, on when high */

void fx_led(int on)
{
    pinMode(led_pin, OUTPUT);
    digitalWrite(led_pin, on ? HIGH : LOW);
}

/* ---- digital ------------------------------------------------------------- */

static const int din_pins[] = { DIN_1, DIN_2, DIN_3, DIN_4, DIN_5, DIN_6, DIN_7, DIN_8 };
static const int dout_pins[] = { DOUT_1, DOUT_2, DOUT_3, DOUT_4, DOUT_5, DOUT_6, DOUT_7, DOUT_8 };
static const int relay_pins[] = { REL_1, REL_2, REL_3, REL_4, REL_5, REL_6 };

void fx_din_init(void)
{
    for (int pin : din_pins) {
        pinMode(pin, INPUT);   /* the board has its own pull-ups */
    }
}

uint8_t fx_din_read(void)
{
    uint8_t bits = 0;

    for (int i = 0; i < 8; i++) {
        bits |= (uint8_t)((digitalRead(din_pins[i]) ? 1U : 0U) << i);
    }
    return bits;
}

void fx_dout_init(void)
{
    for (int pin : dout_pins) {
        pinMode(pin, OUTPUT);
    }
}

void fx_dout_set(int ch, int on)
{
    digitalWrite(dout_pins[ch - 1], on ? HIGH : LOW);
}

void fx_relay_init(void)
{
    for (int pin : relay_pins) {
        pinMode(pin, OUTPUT);
    }
}

void fx_relay_set(int ch, int on)
{
    digitalWrite(relay_pins[ch - 1], on ? HIGH : LOW);
}

/* ---- analog -------------------------------------------------------------- */

static uint32_t pin_mv(uint32_t raw)
{
    return raw * OPENPLC_VREF_MV / OPENPLC_ADC_FULL;
}

int fx_analog_init(void)
{
    if (!openplcEnableVref()) {
        return 0;
    }
    analogReadResolution(12);
    pinMode(AOUT_1_EF, INPUT);
    pinMode(AOUT_2_EF, INPUT);
    return 1;
}

/* VREF+ measured against the factory-calibrated internal reference. */
uint32_t fx_vdda_mv(void)
{
    uint32_t raw = analogRead(AVREF);
    return raw ? __LL_ADC_CALC_VREFANALOG_VOLTAGE(raw, LL_ADC_RESOLUTION_12B) : 0U;
}

/* 16 bits like the fixture firmware, so the PC's conversion applies as is;
 * back to 12 for the board package's own calls. */
int fx_ain_read(int ch, uint32_t *raw, uint32_t *mv)
{
    analogReadResolution(16);
    *raw = analogRead(ch == 1 ? AIN_1 : AIN_2);
    analogReadResolution(12);
    *mv = (uint32_t)((uint64_t)*raw * OPENPLC_VREF_MV / 65535U);
    return 1;
}

int32_t fx_ai1_mv(void)
{
    return (int32_t)lroundf(openplcReadAI1_mV());
}

int32_t fx_ai2_ua(void)
{
    return (int32_t)lroundf(openplcReadAI2_mA() * 1000.0f);
}

const char *fx_cal_status(void)
{
    switch (openplcCalibrationStatus()) {
    case CALIB_OK:      return "ok";
    case CALIB_BLANK:   return "blank";
    case CALIB_CORRUPT: return "corrupt";
    default:            return "foreign";
    }
}

/* mV is the DAC pin voltage the PC already worked out from this board's k, b.
 * It is taken through the calibration forwards and handed to
 * openplcWriteAO_mA(), which inverts it back to the same DAC code: the output
 * equals the fixture firmware's, reached the way a user's app reaches it.
 * XTR111: 102.4 mV per mA. */
uint32_t fx_aout_set_mv(int ch, uint32_t mv)
{
    const calib_area_t *cal = openplc_calib_get(NULL);
    const calib_channel_t *c = &cal->ch[ch == 1 ? CALIB_CH_AO1 : CALIB_CH_AO2];
    float ma = c->gain * ((float)mv / 102.4f) + c->offset;

    openplcWriteAO_mA((uint8_t)ch, ma);
    return openplc_ao_code_from_ma(ma, c);
}

int fx_aout_fault(int ch)
{
    return digitalRead(ch == 1 ? AOUT_1_EF : AOUT_2_EF) ? 1 : 0;
}

int fx_temp_read(int ch, uint32_t *mv)
{
    *mv = pin_mv(analogRead(ch == 1 ? TEMP_SCPROT : TEMP_HSSW));
    return 1;
}

/* ---- RS485 --------------------------------------------------------------- */

static RS485Serial &rs485 = RS485;  /* flips RS485_DIR itself */
static bool rs485_open;

int fx_rs485_begin(uint32_t baud)
{
    if (rs485_open) {
        rs485.end();
    }
    rs485.begin(baud);
    rs485_open = true;
    return 1;
}

void fx_rs485_end(void)
{
    if (rs485_open) {
        rs485.end();
        rs485_open = false;
    }
}

void fx_rs485_send(const char *data, size_t len)
{
    rs485.write((const uint8_t *)data, len);
}

int fx_rs485_read(uint8_t *b)
{
    if (!rs485_open || rs485.available() <= 0) {
        return 0;
    }
    *b = (uint8_t)rs485.read();
    return 1;
}

/* ---- CAN ----------------------------------------------------------------- */

static bool can_open;

int fx_can_begin(uint32_t bps)
{
    can_open = CAN.begin(bps);
    return can_open;
}

void fx_can_end(void)
{
    if (can_open) {
        CAN.end();
        can_open = false;
    }
}

int fx_can_alive(void)
{
    return can_open && HAL_FDCAN_GetState(CAN.handle()) == HAL_FDCAN_STATE_BUSY;
}

int fx_can_write(uint32_t id, const uint8_t *data, uint8_t len)
{
    return can_open && CAN.write(id, data, len);
}

int fx_can_read(uint32_t *id, uint8_t *data, uint8_t *len)
{
    return can_open && CAN.read(*id, data, *len);
}

void fx_can_counters(uint32_t *tec, uint32_t *rec, uint32_t *lec)
{
    FDCAN_ErrorCountersTypeDef ec = {};
    FDCAN_ProtocolStatusTypeDef ps = {};

    if (!can_open) {
        return;
    }
    HAL_FDCAN_GetErrorCounters(CAN.handle(), &ec);
    HAL_FDCAN_GetProtocolStatus(CAN.handle(), &ps);
    *tec = ec.TxErrorCnt;
    *rec = ec.RxErrorCnt;
    *lec = ps.LastErrorCode;
}

/* ---- Ethernet: lwIP raw TCP, serviced by the core between loop() calls --- */

static struct tcp_pcb *eth_listener;
static struct tcp_pcb *eth_client;
static struct pbuf *eth_rx;        /* received, not yet read */
static uint16_t eth_rx_off;

static void eth_drop_client(void)
{
    if (eth_client != NULL) {
        tcp_arg(eth_client, NULL);
        tcp_recv(eth_client, NULL);
        tcp_err(eth_client, NULL);
        tcp_abort(eth_client);
        eth_client = NULL;
    }
    if (eth_rx != NULL) {
        pbuf_free(eth_rx);
        eth_rx = NULL;
    }
    eth_rx_off = 0;
}

static err_t eth_recv_cb(void *arg, struct tcp_pcb *pcb, struct pbuf *p, err_t err)
{
    (void)arg;
    if (pcb != eth_client) {
        if (p != NULL) {
            tcp_recved(pcb, p->tot_len);
            pbuf_free(p);
        }
        return ERR_OK;
    }
    if (p == NULL || err != ERR_OK) {
        eth_drop_client();
        return ERR_ABRT;
    }
    if (eth_rx == NULL) {
        eth_rx = p;
        eth_rx_off = 0;
    } else {
        pbuf_cat(eth_rx, p);
    }
    return ERR_OK;
}

static void eth_err_cb(void *arg, err_t err)
{
    (void)arg;
    (void)err;
    eth_client = NULL;   /* lwIP has already freed it */
    if (eth_rx != NULL) {
        pbuf_free(eth_rx);
        eth_rx = NULL;
    }
}

static err_t eth_accept_cb(void *arg, struct tcp_pcb *pcb, err_t err)
{
    (void)arg;
    if (err != ERR_OK || pcb == NULL) {
        return ERR_VAL;
    }
    if (eth_client != NULL) {
        return ERR_MEM;   /* one peer at a time */
    }
    eth_client = pcb;
    tcp_recv(pcb, eth_recv_cb);
    tcp_err(pcb, eth_err_cb);
    return ERR_OK;
}

int fx_eth_listen(uint16_t port, char *err, size_t err_len)
{
    struct tcp_pcb *pcb;

    fx_eth_close();
    pcb = tcp_new();
    if (pcb == NULL) {
        snprintf(err, err_len, "lwIP had no pcb left to listen with");
        return 0;
    }
    if (tcp_bind(pcb, IP_ADDR_ANY, port) != ERR_OK) {
        tcp_abort(pcb);
        snprintf(err, err_len, "port %u is already bound", (unsigned)port);
        return 0;
    }
    eth_listener = tcp_listen(pcb);
    if (eth_listener == NULL) {
        snprintf(err, err_len, "lwIP would not listen on port %u", (unsigned)port);
        return 0;
    }
    tcp_accept(eth_listener, eth_accept_cb);
    return 1;
}

void fx_eth_close(void)
{
    eth_drop_client();
    if (eth_listener != NULL) {
        tcp_close(eth_listener);
        eth_listener = NULL;
    }
}

int fx_eth_link(void)
{
    return Net.linkUp();
}

int fx_eth_connected(void)
{
    return eth_client != NULL;
}

/* The interface's own address: Net.localIP() reports DHCP addresses only. */
void fx_eth_ip(char *out, size_t out_len)
{
    const ip4_addr_t *a = netif_default != NULL ? netif_ip4_addr(netif_default) : NULL;

    if (a == NULL) {
        snprintf(out, out_len, "0.0.0.0");
        return;
    }
    snprintf(out, out_len, "%u.%u.%u.%u", ip4_addr1(a), ip4_addr2(a), ip4_addr3(a), ip4_addr4(a));
}

/* As the fixture firmware: static on a /24 with the gateway at .1, or DHCP
 * restarted when it has no address. */
void fx_eth_address(const uint8_t *ip)
{
    struct netif *nif = netif_default;

    if (nif == NULL) {
        return;
    }
    if (ip != NULL) {
        ip4_addr_t addr, mask, gw;
        IP4_ADDR(&addr, ip[0], ip[1], ip[2], ip[3]);
        IP4_ADDR(&mask, 255, 255, 255, 0);
        IP4_ADDR(&gw, ip[0], ip[1], ip[2], 1);
        dhcp_stop(nif);
        netif_set_addr(nif, &addr, &mask, &gw);
    } else if (!dhcp_supplied_address(nif)) {
        dhcp_start(nif);
    }
}

/* OpenPLC_Net's own MDIO read (ethernetif.c); the core has the MAC up. */
extern "C" int32_t ETH_PHY_IO_ReadReg(uint32_t DevAddr, uint32_t RegAddr, uint32_t *pRegVal);

int fx_eth_phy_read(uint32_t addr, uint32_t reg, uint16_t *val)
{
    uint32_t v = 0;

    if (ETH_PHY_IO_ReadReg(addr, reg, &v) != 0) {
        return 0;
    }
    *val = (uint16_t)v;
    return 1;
}

size_t fx_eth_avail(void)
{
    return eth_rx != NULL ? (size_t)(eth_rx->tot_len - eth_rx_off) : 0U;
}

size_t fx_eth_room(void)
{
    return eth_client != NULL ? tcp_sndbuf(eth_client) : 0U;
}

size_t fx_eth_read(uint8_t *buf, size_t len)
{
    size_t n = fx_eth_avail();

    if (n > len) {
        n = len;
    }
    if (n == 0U) {
        return 0U;
    }
    pbuf_copy_partial(eth_rx, buf, (u16_t)n, eth_rx_off);
    eth_rx_off = (uint16_t)(eth_rx_off + n);
    if (eth_rx_off >= eth_rx->tot_len) {
        pbuf_free(eth_rx);
        eth_rx = NULL;
        eth_rx_off = 0;
    }
    if (eth_client != NULL) {
        tcp_recved(eth_client, (u16_t)n);   /* reopen the window only for what was taken */
    }
    return n;
}

size_t fx_eth_write(const uint8_t *buf, size_t len)
{
    if (eth_client == NULL || len == 0U ||
        tcp_write(eth_client, buf, (u16_t)len, TCP_WRITE_FLAG_COPY) != ERR_OK) {
        return 0U;
    }
    tcp_output(eth_client);
    return len;
}

/* ---- USB CDC ------------------------------------------------------------- */

extern USBD_HandleTypeDef hUSBD_Device_CDC;

void fx_usb_begin(void)
{
    Serial.begin(115200);
}

const char *fx_usb_state(void)
{
    switch (hUSBD_Device_CDC.dev_state) {
    case USBD_STATE_DEFAULT:    return "default";
    case USBD_STATE_ADDRESSED:  return "addressed";
    case USBD_STATE_CONFIGURED: return "cfg";
    case USBD_STATE_SUSPENDED:  return "suspended";
    default:                    return "none";
    }
}

size_t fx_usb_avail(void)
{
    int n = Serial.available();
    return n > 0 ? (size_t)n : 0U;
}

size_t fx_usb_room(void)
{
    int n = Serial.availableForWrite();
    return n > 0 ? (size_t)n : 0U;
}

size_t fx_usb_read(uint8_t *buf, size_t len)
{
    return Serial.readBytes((char *)buf, len);
}

size_t fx_usb_write(const uint8_t *buf, size_t len)
{
    return Serial.write(buf, len);
}

/* ---- SD ------------------------------------------------------------------ */

int fx_sd_detected(void)
{
    pinMode(SDMMC_CD, INPUT_PULLUP);
    return digitalRead(SDMMC_CD) == LOW;   /* active low, as OpenPLC_SD reads it */
}

static Sd2Card sd_card;
static bool sd_card_up;
static FIL sd_fil;
static bool sd_fil_open;

/* The card on its own, with the file system unmounted. */
int fx_sd_identify(fx_sd_card_t *card)
{
    BSP_SD_CardInfo info;

    SD.end();
    if (!sd_card.init(SDMMC_CD, LOW) || !BSP_SD_GetCardInfo(&info)) {
        sd_card.deinit();
        return 0;
    }
    sd_card_up = true;
    card->blocks = info.LogBlockNbr;
    card->block_size = info.LogBlockSize;
    card->card_class = info.Class;
    card->v2x = info.CardVersion == CARD_V2_X;
    return 1;
}

int fx_sd_read_block(uint32_t lba, uint8_t *buf)
{
    static uint32_t block[128];   /* word-aligned for the SDMMC */
    uint32_t start = millis();

    if (BSP_SD_ReadBlocks(block, lba, 1U, 1000U) != MSD_OK) {
        return 0;
    }
    while (BSP_SD_GetCardState() != 0U) {   /* 0 = back in transfer state */
        if (millis() - start > 1000U) {
            return 0;
        }
    }
    memcpy(buf, block, sizeof(block));
    return 1;
}

void fx_sd_release(void)
{
    if (sd_card_up) {
        sd_card.deinit();
        sd_card_up = false;
    }
}

int fx_sd_mount(void)
{
    sd_fil_open = false;
    return SD.begin() ? 1 : 0;
}

int fx_sd_open(const char *name, int write)
{
    FRESULT fr;

    if (sd_fil_open) {
        f_close(&sd_fil);
    }
    fr = f_open(&sd_fil, name, write ? (FA_CREATE_ALWAYS | FA_WRITE) : FA_READ);
    sd_fil_open = fr == FR_OK;
    return (int)fr;
}

int fx_sd_write(const uint8_t *buf, uint32_t n, uint32_t *done)
{
    UINT d = 0;
    FRESULT fr = f_write(&sd_fil, buf, n, &d);

    *done = d;
    return (int)fr;
}

int fx_sd_read(uint8_t *buf, uint32_t n, uint32_t *done)
{
    UINT d = 0;
    FRESULT fr = f_read(&sd_fil, buf, n, &d);

    *done = d;
    return (int)fr;
}

int fx_sd_sync(void)
{
    return (int)f_sync(&sd_fil);
}

int fx_sd_close(void)
{
    if (!sd_fil_open) {
        return 0;
    }
    sd_fil_open = false;
    return (int)f_close(&sd_fil);
}

/* ---- SDRAM: the whole array, taken once through the library ------------- */

volatile uint32_t *fx_sdram(void)
{
    static volatile uint32_t *mem;

    if (mem == NULL && SDRAM.begin()) {
        mem = (volatile uint32_t *)SDRAM.allocUninitialized(OpenPLC_SDRAM_Class::CAPACITY);
    }
    return mem;
}
