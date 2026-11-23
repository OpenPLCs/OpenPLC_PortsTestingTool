# OpenPLC fixture - how to use it

中文：`README.zh-CN.html`

This folder is everything. No development tools are needed.

## 1. Install one program

**STM32CubeProgrammer** - a free download from ST.

<https://www.st.com/en/development-tools/stm32cubeprog.html>

⚠️ **Not** STM32CubeIDE. What is needed is the small programmer, not the whole development environment. The ST-Link driver is installed with it.

## 2. Double-click `start.cmd`

It is the only entry point. It shows a menu:

```
    1) Flash the firmware  (ST-Link connected, board powered)
    2) Open the test panel (RS232 control port connected)
    3) Both                (flash, then open the panel)
    4) 看使用说明（中文）
    5) Read the guide (English)
    0) Quit
```

Type a number and press Enter. After each job it returns to the menu.

## 3. Test the ports

Choose 2 (or 3) and the browser opens the **fixture test big screen**: one tile per station. "Wiring (by station)" on the left lists each station's cables; click a station to open its wiring, where "Change" picks the RS232 control port, the RS485 / CAN adapters, the reference board and the ST-Link. "+ Add station" below the tiles adds one. Once a board is plugged in the tile moves on by itself: click "Flash" when the board has no fixture firmware or an old one; when the wiring is recognised, ① function test runs by itself and the tile shows which item it is on. On a failure LED3 on the board lights; click "Continue" to redo that step or "Restart" to start from ①. Click a tile for that board's page: one tab per step listing each item's settings, criterion, reading and result, and the shipping sheet at the end. After ①, PortTool writes the aging task into the board and the tile says to unplug it for the oven; powered in the oven the board runs and times it by itself (LED3 blinks slowly), and when it is plugged back ③ reads the result. What each step tests, and which aging items run for how long, is set under "Template" at the top left: there can be several templates, each save raises its version, and boards plugged in after "Use this template" are tested with it. The right side lists boards in the oven and the latest results from every station.

Choose "R&D test" under "Test type" at the top left to open the test panel described below, and "Fixture test" in the same place to go back to the big screen. Whichever you chose opens next time. Click "User guide" at the top of the panel for a detailed guide with screenshots marking where to click at each step.

The "Wiring" column on the left: R&D testing is station 1, and which port each cable is on follows station 1's wiring registration; click the column to open the registration and change a port. At the top, "Boards this PC sees" lists every board this PC sees and the firmware on it; click "Flash" on one without the current fixture firmware (through station 1's ST-Link).

1. **Step 1: connect the control port**: in the wiring registration, pick the board's **RS232 control port** for "Control port RS232" (usually the USB-serial adapter plugged into this PC, terminals C05/C06); click "Find it" if you do not know which. From then on the panel connects by itself whenever the board is plugged in with the fixture firmware running; there is no Connect button. Bluetooth serial ports are not listed
2. **Step 2: connect the other ports**: connect only what the ports you test need. A green dot on a row means it is connected, a gray one means it is not; hover over a gray row to see why, and click Retry next to it to try again. Each row shows only a short value (port name, IP, "Plugged in"); hover over it for the full text. Drag the edge of this left column, or of the log column on the far right of R&D testing, to change its width; the width is kept for next time. This part is grouped by machine: the board under test, the reference board and this PC's own devices, each headed by its name. The rows only show the port the wiring registration chose; there is no dropdown and no Connect button: a test opens the port when it starts and closes it when it ends. RS485 and CAN are recognised and chosen by themselves after connecting; pick them in the wiring registration only if that fails
   - If a test reports "Serial port busy", click "**Force release ports**" on the right: it stops the running tests and closes the ports the panel has open; a port that still will not close is named, held by another program
3. **R&D test** runs items one by one, once or for a set time; on the DI, DO, AI, AO and relay pages pick the **wiring** in the page header: loop-back (the loop-back harness, the board judges itself), reference board, other instrument (for a temperature chamber or EMC lab: DI and AI are judged against the values you set, DO, AO and relays only output the values you set for outside instruments to watch) or by hand, one choice shared by DI, DO and relays and one by AI and AO; make sure DI and DO are good before testing the relays. The fixture test runs on the big screen ("Test type" at the top left): in the functional and user-level tests DI, AI, DO and relays can be tested with the reference board or with outside instruments (each item says which); with the reference board set to "Not used" in the wiring registration, the items that need it start unticked and the test still starts. In the log, each line of data from the board has a line under it saying what every number is, with its unit
4. In the R&D test, pick a port in the middle; its item cards appear on the right, each giving the goal, how to test (steps to follow), what to connect and what counts as a pass
5. Tick the cards to run and click Start. Anything a person has to measure or look at comes up as a question marked 🍍; fill it in

**No commands to remember.**

⚠️ On the R&D test's "Clock (crystal)" page, "Crystal drive level and start-up margin" changes the crystal's drive level. With "All" it switches back to medium-low by itself when it ends or is stopped; **a single level you set stays on the board, even after a reset**: click "Restore factory level" (or run "All" once) to go back.

⚠️ Some pass limits (for example the crystal's deviation, drift and start-up time limits) are not verified yet: the panel gives a default and the card says where it comes from. **Change them on the card; leave one empty to record without judging.**

⚠️ Opening the panel also opens a black window. **Do not close it** while testing: closing it stops the panel.

⚠️ Do not delete the `plans` folder next to it - **what counts as a pass for every test is written there**. Without it the panel shows readings but cannot give a verdict.

⚠️ Do not delete the `stage6` folder either - step 6 (the user-level test) uses the bootloader, IAPTool and demo in it.

## 4. Where a multimeter is needed

Values the board cannot read back itself, such as analog output current or a loaded digital output, make the panel stop and ask what the multimeter reads; it judges what you type. A loaded digital output is judged against "Drop limit" on its card (the default comes from the test plan; you can change it, or leave it empty to only record). Only in the R&D test; the full fixture test reads them on the reference board and asks nobody.

Analog calibration is in the **R&D test** only: type the multimeter reading at each point, the panel fits each channel's k and b, keeps them on this PC, and burns them into the board with the ST-Link.

## 5. On Linux

The Linux files are in this same folder. Everything above applies, except for the points below.

1. **Entry point**: in a terminal, go to this folder and run `bash start.sh`. It shows the same menu as `start.cmd`. Do not double-click it: that usually just opens it as text.
2. **Install the Linux STM32CubeProgrammer**: same download page. Install it in the default place (`~/STMicroelectronics/STM32Cube/STM32CubeProgrammer` or `/usr/local/STMicroelectronics/STM32Cube/STM32CubeProgrammer`), or put its `bin` on PATH, and the menu and the panel find it.
3. **Serial port access**: your user must be in the `dialout` group, or the serial ports will not open. Run `sudo usermod -aG dialout $USER`, then log out and back in.
4. **KNX USB interface**: on Linux it goes through `/dev/hidraw*`, which only root can open by default. Add a udev rule to open it up, for example `SUBSYSTEM=="hidraw", ATTRS{idVendor}=="<vendor id>", MODE="0666"` in `/etc/udev/rules.d/99-knx-usb.rules` (find the vendor id with `lsusb`), then unplug and replug the interface.
5. **The panel runs in the terminal**: after choosing 2 (or 3), the panel runs in the terminal `start.sh` is in. Ctrl+C stops it and returns to the menu. Only press it when testing is done.
