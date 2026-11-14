/*
 * fixture_hw.h -- what the protocol core needs from the board.
 *
 * fixture_board.cpp implements these with the board package API; the T4-05
 * host test implements them with stubs. Nothing above this line touches a pin.
 */

#ifndef FIXTURE_HW_H_
#define FIXTURE_HW_H_

#include <stddef.h>
#include <stdint.h>

/* Control port and clock. */
void     fx_out(const char *text);
uint32_t fx_millis(void);
void     fx_uid(uint32_t words[3]);
void     fx_led(int on);
/* Switches the control line's rate; called once the reply announcing it has
 * gone out (rs232 session). */
void     fx_line_baud(uint32_t baud);
/* The calibration area's bytes as they sit in flash (pt.cal); returns how
 * many were copied. */
size_t   fx_cal_area(uint8_t *out, size_t len);

/* Digital inputs DI1..DI8, bit0 = DI1. */
void    fx_din_init(void);
uint8_t fx_din_read(void);

/* Digital outputs DO1..DO8, fully on or off. */
void fx_dout_init(void);
void fx_dout_set(int ch, int on);

/* Relays RY1..RY6. */
void fx_relay_init(void);
void fx_relay_set(int ch, int on);

/* Analog. fx_analog_init() returns 0 when the 2.5 V reference did not start. */
int         fx_analog_init(void);
uint32_t    fx_vdda_mv(void);
/* raw is a 16-bit reading, as on the fixture firmware. */
int         fx_ain_read(int ch, uint32_t *raw, uint32_t *pin_mv);
int32_t     fx_ai1_mv(void);          /* terminal mV, calibrated */
int32_t     fx_ai2_ua(void);          /* terminal uA, calibrated */
const char *fx_cal_status(void);      /* ok, blank, corrupt or foreign */
uint32_t    fx_aout_set_mv(int ch, uint32_t mv);   /* DAC pin mV; returns the DAC code */
int         fx_aout_fault(int ch);
int         fx_temp_read(int ch, uint32_t *mv);

/* RS485. fx_rs485_read() returns 1 and a byte, or 0 when nothing waits. */
int  fx_rs485_begin(uint32_t baud);
void fx_rs485_end(void);
void fx_rs485_send(const char *data, size_t len);
int  fx_rs485_read(uint8_t *b);

/* CAN, standard IDs, normal mode. */
int  fx_can_begin(uint32_t bps);
void fx_can_end(void);
int  fx_can_alive(void);
int  fx_can_write(uint32_t id, const uint8_t *data, uint8_t len);
int  fx_can_read(uint32_t *id, uint8_t *data, uint8_t *len);
void fx_can_counters(uint32_t *tec, uint32_t *rec, uint32_t *lec);

/* Ethernet: one TCP client at a time on a listening port. fx_eth_read()
 * consumes; fx_eth_write() returns how many bytes it took. */
int    fx_eth_listen(uint16_t port, char *err, size_t err_len);
/* ip NULL = DHCP, else a static address on a /24 with the gateway at .1. */
void   fx_eth_address(const uint8_t *ip);
/* One PHY register over MDIO; 0 when the read failed. */
int    fx_eth_phy_read(uint32_t addr, uint32_t reg, uint16_t *val);
void   fx_eth_close(void);
int    fx_eth_link(void);
int    fx_eth_connected(void);
void   fx_eth_ip(char *out, size_t out_len);
size_t fx_eth_avail(void);
size_t fx_eth_room(void);
size_t fx_eth_read(uint8_t *buf, size_t len);
size_t fx_eth_write(const uint8_t *buf, size_t len);

/* USB CDC, same shape as ethernet. fx_usb_state() is the device state as
 * pt.caps spells it: none, default, addressed, cfg or suspended. */
void        fx_usb_begin(void);
const char *fx_usb_state(void);
size_t      fx_usb_avail(void);
size_t      fx_usb_room(void);
size_t      fx_usb_read(uint8_t *buf, size_t len);
size_t      fx_usb_write(const uint8_t *buf, size_t len);

/* SD card detect switch. */
int fx_sd_detected(void);

/* SD card identification, without the file system. */
typedef struct {
    uint32_t blocks, block_size, card_class;
    int      v2x;
} fx_sd_card_t;
int  fx_sd_identify(fx_sd_card_t *card);              /* 1 when the card answered */
int  fx_sd_read_block(uint32_t lba, uint8_t *buf);    /* one 512-byte block */
void fx_sd_release(void);

/* Files on the mounted card. Returns are FatFs FRESULT, 0 = FR_OK. */
int fx_sd_mount(void);                                /* 1 when mounted */
int fx_sd_open(const char *name, int write);
int fx_sd_write(const uint8_t *buf, uint32_t n, uint32_t *done);
int fx_sd_read(uint8_t *buf, uint32_t n, uint32_t *done);
int fx_sd_sync(void);
int fx_sd_close(void);

/* The whole 64 MiB SDRAM, or NULL when it did not come up. */
volatile uint32_t *fx_sdram(void);

#endif /* FIXTURE_HW_H_ */
