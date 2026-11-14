/*
 * fixture_proto.h -- the production fixture's pt.* protocol, demo subset.
 *
 * This is a second implementation of the protocol the fixture firmware speaks
 * ($BOOT TestCase/porttool/), so the PC panel drives this sketch exactly as it
 * drives that firmware. Which commands, ports and frame fields differ is in
 * $PROD/docs/modules/M4/PORTTOOL-FLOW.md "A.9"; the contract test T4-05 checks
 * both against one Go test.
 *
 * Plain C++ with no Arduino header: the protocol core is also compiled on a PC
 * by T4-05, with the hardware hooks of fixture_hw.h stubbed.
 */

#ifndef FIXTURE_PROTO_H_
#define FIXTURE_PROTO_H_

#include <stdint.h>

/* The fixture firmware's PORTTOOL_VERSION this follows. T4-05 fails when the
 * two differ, so a protocol change on the firmware side is noticed here. */
#define FX_PROTOCOL_VERSION "0.18.9"

/* This sketch's own version, reported as demo= so the panel can tell it from
 * the fixture firmware. Keep in step with OPENPLC_APP_VERSION in the .ino. */
#define FX_DEMO_VERSION "1.0.0"

/* LINE_MAX is one line from the PC (256 fits six different relay values),
 * REPLY_MAX every line sent; same as PORTTOOL_LINE_MAX / PORTTOOL_REPLY_MAX
 * ($PROD/docs/modules/M4/PORTTOOL-FLOW.md A.3). */
#define FX_LINE_MAX      256U
#define FX_REPLY_MAX     192U
#define FX_PERIOD_MIN_MS 50U

/* Prints the start-up banner. */
void fx_begin(void);

/* Hands the protocol one byte from the control port. */
void fx_feed(char c);

/* Runs the sessions that are due and the deadman. Call every loop pass. */
void fx_poll(uint32_t now_ms);

#endif /* FIXTURE_PROTO_H_ */
