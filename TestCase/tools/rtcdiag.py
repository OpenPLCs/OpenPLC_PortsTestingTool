"""TEMPORARY: how fast the board's RTC runs against NTP time.

Cross-checks rtc.lsefreq, which measures the 32.768 kHz crystal against the
board's own 25 MHz crystal. Remove once that number is settled.

    python TestCase/tools/rtcdiag.py --port COM5 --minutes 60

The panel must not hold the port. rtc.read only gives whole seconds, so each
check polls it until the second changes: that edge lies between the send of
the last old reply and the receipt of the first new one. Several edges are
intersected to narrow it. The PC clock is tied to NTP at every check, so a
PC clock that drifts or steps in between does not count.

Prints the board's offset from NTP time at each check and the rate in ppm
(negative = board slow). Exit 0 = done, 2 = could not open the port.
"""

import argparse
import re
import socket
import struct
import sys
import time
from datetime import datetime

import serial

READ_RE = re.compile(r"OK rtc\.read .*date=(\d+)-(\d+)-(\d+) time=(\d+):(\d+):(\d+)")
NTP_EPOCH = 2208988800  # seconds from 1900 to 1970


def board_seconds(m):
    y, mo, d, h, mi, s = (int(x) for x in m.groups())
    return datetime(2000 + y, mo, d, h, mi, s).timestamp()


def rtc_read(port):
    """One rtc.read: (send time, receive time, board seconds)."""
    port.reset_input_buffer()
    t0 = time.time()
    port.write(b"pt.run rtc.read\r\n")
    deadline = t0 + 1.0
    buf = b""
    while time.time() < deadline:
        buf += port.read(port.in_waiting or 1)
        for line in buf.split(b"\n"):
            m = READ_RE.search(line.decode("ascii", "replace"))
            if m:
                return t0, time.time(), board_seconds(m)
    raise RuntimeError("no rtc.read reply")


def board_offset(port, edges):
    """Board time minus PC time at a seconds edge, as an interval (lo, hi)."""
    lo, hi = -1e18, 1e18
    for _ in range(edges):
        _, _, first = rtc_read(port)
        prev_send = None
        while True:
            send, recv, b = rtc_read(port)
            if b != first:
                break
            prev_send = send
        if prev_send is None:
            continue  # the edge came during the first read: no bracket
        # The edge (board time b) happened after prev_send and before recv.
        lo, hi = max(lo, b - recv), min(hi, b - prev_send)
    return lo, hi


def ntp_offset(server, tries):
    """NTP time minus PC time from the reply with the shortest round trip."""
    best = None
    for _ in range(tries):
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as s:
            s.settimeout(2)
            t1 = time.time()
            try:
                s.sendto(b"\x1b" + 47 * b"\0", (server, 123))
                data, _ = s.recvfrom(48)
            except OSError:
                continue
            t4 = time.time()
        sec, frac = struct.unpack("!II", data[40:48])
        t3 = sec - NTP_EPOCH + frac / 2**32
        sec, frac = struct.unpack("!II", data[32:40])
        t2 = sec - NTP_EPOCH + frac / 2**32
        rtt = (t4 - t1) - (t3 - t2)
        off = ((t2 - t1) + (t3 - t4)) / 2
        if best is None or rtt < best[0]:
            best = (rtt, off)
        time.sleep(0.2)
    if best is None:
        raise RuntimeError("no NTP reply from " + server)
    return best


def check(port, args):
    rtt, ntp = ntp_offset(args.ntp, 8)
    lo, hi = board_offset(port, args.edges)
    t = time.time() + ntp
    # board - NTP = (board - PC) - (NTP - PC)
    return t, lo - ntp, hi - ntp, rtt


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", required=True)
    ap.add_argument("--minutes", type=float, default=60)
    ap.add_argument("--every", type=float, default=10, help="minutes between checks")
    ap.add_argument("--edges", type=int, default=6)
    ap.add_argument("--ntp", default="pool.ntp.org")
    args = ap.parse_args()
    try:
        port = serial.Serial(args.port, 115200, timeout=0.05)
    except serial.SerialException as e:
        print("cannot open %s: %s" % (args.port, e))
        return 2
    first = None
    end = time.time() + args.minutes * 60
    while True:
        t, lo, hi, rtt = check(port, args)
        mid = (lo + hi) / 2
        line = "%s  board-NTP %+.4f s (+-%.1f ms, NTP rtt %.0f ms)" % (
            time.strftime("%H:%M:%S"), mid, (hi - lo) * 500, rtt * 1000)
        if first is None:
            first = (t, lo, hi)
        elif t > first[0]:
            span = t - first[0]
            ppm = (mid - (first[1] + first[2]) / 2) / span * 1e6
            err = ((hi - lo) + (first[2] - first[1])) / 2 / span * 1e6
            line += "  rate %+.1f ppm (+-%.1f) over %.0f min" % (ppm, err, span / 60)
        print(line, flush=True)
        if time.time() >= end:
            return 0
        time.sleep(max(0.0, min(args.every * 60, end - time.time())))


if __name__ == "__main__":
    sys.exit(main())
