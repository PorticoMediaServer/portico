#!/usr/bin/env python3
"""Minimal authoritative DNS responder for the G09 fixture network.

It answers A records from a JSON zone file that is re-read when it changes, so a
controlled DNS change is one atomic file write. It runs on the private fixture
network only; the host resolver is untouched.
"""

import argparse
import json
import os
import socket
import socketserver
import struct
import threading
import time


class Zone:
    def __init__(self, path):
        self.path = path
        self.lock = threading.Lock()
        self.mtime = None
        self.records = {}
        self.reload()

    def reload(self):
        try:
            mtime = os.stat(self.path).st_mtime_ns
        except OSError:
            return
        if mtime == self.mtime:
            return
        with self.lock:
            try:
                with open(self.path, "r", encoding="utf-8") as handle:
                    data = json.load(handle)
            except (OSError, ValueError):
                return
            self.records = {name.lower().rstrip("."): address for name, address in data.items()}
            self.mtime = mtime

    def lookup(self, name):
        self.reload()
        with self.lock:
            return self.records.get(name.lower().rstrip("."))


def parse_question(packet):
    if len(packet) < 12:
        return None
    offset = 12
    labels = []
    while offset < len(packet):
        length = packet[offset]
        offset += 1
        if length == 0:
            break
        if length & 0xC0:
            return None
        labels.append(packet[offset:offset + length].decode("ascii", "replace"))
        offset += length
    if offset + 4 > len(packet):
        return None
    qtype, qclass = struct.unpack("!HH", packet[offset:offset + 4])
    return ".".join(labels), qtype, qclass, offset + 4


def answer(zone, packet):
    question = parse_question(packet)
    if question is None:
        return None
    name, qtype, qclass, end = question
    address = zone.lookup(name)
    rcode = 0 if address else 3
    flags = 0x8400 | (packet[2] & 0x01) << 8  # QR + AA, copy RD
    response = bytearray(packet[:2])
    response += struct.pack("!HHHHH", flags, 1, 1 if address and qtype == 1 else 0, 0, 0)
    response += packet[12:end]
    if address and qtype == 1 and qclass == 1:
        try:
            packed = socket.inet_aton(address)
        except OSError:
            return None
        response += b"\xc0\x0c" + struct.pack("!HHIH", 1, 1, 5, len(packed)) + packed
    elif rcode == 3:
        response[3] = (response[3] & 0xF0) | 3
    return bytes(response)


class UDPHandler(socketserver.BaseRequestHandler):
    def handle(self):
        data, sock = self.request
        reply = answer(self.server.zone, data)
        if reply:
            sock.sendto(reply, self.client_address)


class TCPHandler(socketserver.BaseRequestHandler):
    def handle(self):
        self.request.settimeout(5)
        try:
            length = struct.unpack("!H", self.request.recv(2))[0]
            data = b""
            while len(data) < length:
                chunk = self.request.recv(length - len(data))
                if not chunk:
                    return
                data += chunk
            reply = answer(self.server.zone, data)
            if reply:
                self.request.sendall(struct.pack("!H", len(reply)) + reply)
        except (OSError, struct.error):
            return


class UDPServer(socketserver.ThreadingUDPServer):
    allow_reuse_address = True
    daemon_threads = True

    def __init__(self, address, zone):
        self.zone = zone
        super().__init__(address, UDPHandler)


class TCPServer(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True

    def __init__(self, address, zone):
        self.zone = zone
        super().__init__(address, TCPHandler)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--zone", required=True)
    parser.add_argument("--port", type=int, default=53)
    args = parser.parse_args()
    zone = Zone(args.zone)
    udp = UDPServer(("0.0.0.0", args.port), zone)
    tcp = TCPServer(("0.0.0.0", args.port), zone)
    threading.Thread(target=udp.serve_forever, daemon=True).start()
    threading.Thread(target=tcp.serve_forever, daemon=True).start()
    print("dns responder on %d" % args.port, flush=True)
    while True:
        time.sleep(3600)


if __name__ == "__main__":
    main()
