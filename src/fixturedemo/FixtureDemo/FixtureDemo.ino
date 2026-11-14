/*
 * FixtureDemo -- the production fixture's step 6, user-level function test.
 *
 * What it does: speaks the fixture's pt.* protocol on the RS232 port, so the
 *   fixture panel (PortTool) drives this sketch like the fixture firmware,
 *   but every port is reached through the board package API a user's
 *   sketch would use, and AI / AO through this board's calibration.
 * Connect: the fixture's control cable on the RS232 terminals C05 / C06, and
 *   whatever peers the step-6 plan asks for.
 * Expect: nothing on its own -- type pt.caps for the port list, or let the
 *   panel run it. Lines starting with ! are samples, OK / ERR are replies.
 * Which commands and ports it has, and how it differs from the fixture
 *   firmware: OpenPLC_Instruction docs/modules/M4/PORTTOOL-FLOW.md "A.9".
 * Serial Monitor: the RS232 port (terminals C05 / C06, through an RS232 adapter), 115200 baud.
 */

#include <OpenPLC_Ports.h>
#include <OpenPLC_CAN.h>
#include <OpenPLC_Net_Autostart.h>
#include <OpenPLC_SD.h>
#include <OpenPLC_SDRAM.h>

#include "fixture_proto.h"

OPENPLC_APP_VERSION(1, 0, 0);   // keep in step with FX_DEMO_VERSION
OPENPLC_SYSTEM_LOG(OFF);         // USB port 1 is a port under test here, not a log

void setup() {
  RS232.begin(115200);
  fx_begin();
}

void loop() {
  while (RS232.available() > 0) {
    fx_feed((char)RS232.read());
  }
  fx_poll(millis());
}
