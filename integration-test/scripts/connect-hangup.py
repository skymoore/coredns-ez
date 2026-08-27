#!/usr/bin/env python3
"""POST /api/v1/cluster/connect, then hang up after HTTP headers.

Simulates the admin UI losing TLS after the primary already accepted the
join (large snapshot apply). Prints the status line. Exit 0 only if it
is HTTP 200 — the node must already be secondary before apply finishes.
"""
from __future__ import annotations

import json
import socket
import sys
import urllib.parse


def hangup_connect(base: str, payload: dict) -> str:
    u = urllib.parse.urlparse(base)
    host = u.hostname or "127.0.0.1"
    port = u.port or (443 if u.scheme == "https" else 80)
    body = json.dumps(payload).encode()
    req = (
        f"POST /api/v1/cluster/connect HTTP/1.1\r\n"
        f"Host: {host}:{port}\r\n"
        f"Content-Type: application/json\r\n"
        f"Content-Length: {len(body)}\r\n"
        f"Connection: close\r\n"
        f"\r\n"
    ).encode() + body
    with socket.create_connection((host, port), 20) as s:
        s.sendall(req)
        f = s.makefile("rb")
        status = f.readline().decode("ascii", "replace").strip()
        while True:
            line = f.readline()
            if line in (b"", b"\r\n", b"\n"):
                break
    return status


def main() -> int:
    if len(sys.argv) != 5:
        print("usage: connect-hangup.py SECONDARY_API PRIMARY_API JOIN_TOKEN SECONDARY_IP", file=sys.stderr)
        return 2
    secondary_api, primary_api, token, secondary_ip = sys.argv[1:]
    payload = {
        "url": primary_api,
        "token": token,
        "dns": f"{secondary_ip}:53",
        "api_url": secondary_api,
        "name": "secondary",
    }
    status = hangup_connect(secondary_api, payload)
    print(status)
    parts = status.split()
    if len(parts) >= 2 and parts[1] == "200":
        return 0
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
