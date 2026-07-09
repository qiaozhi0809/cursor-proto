"""
mitmproxy addon: capture Cursor 3.10 API requests to individual .bin files.

Usage:
    mitmdump -s scripts/capture_addon.py --set stream_large_bodies=1

Every request/response to api2.cursor.sh/aiserver.v1.* or agent.v1.* is
dumped as:
    captures/traffic/<timestamp>_<method>_<slug>.req.headers.json
    captures/traffic/<timestamp>_<method>_<slug>.req.body.bin
    captures/traffic/<timestamp>_<method>_<slug>.resp.headers.json
    captures/traffic/<timestamp>_<method>_<slug>.resp.body.bin

Only cursor.sh + cursor.com hosts are captured; everything else is ignored.
"""
import json
import os
import re
import time
from pathlib import Path

from mitmproxy import http

OUT = Path(__file__).parent.parent / "captures" / "traffic"
OUT.mkdir(parents=True, exist_ok=True)


def slug(url: str) -> str:
    # Extract last two path segments as tag
    path = url.split("://", 1)[-1].split("/", 1)[-1] if "://" in url else url
    # Keep last 3 segments
    parts = [p for p in path.split("/") if p]
    tag = "_".join(parts[-3:])[:80]
    return re.sub(r"[^a-zA-Z0-9._-]", "_", tag)


def matches(host: str) -> bool:
    return any(h in host for h in ("cursor.sh", "cursor.com", "authentication.cursor"))


class CursorCapture:
    def __init__(self):
        self.n = 0

    def request(self, flow: http.HTTPFlow):
        if not matches(flow.request.host):
            return
        self.n += 1
        ts = time.strftime("%H%M%S")
        base = OUT / f"{ts}_{self.n:03d}_{flow.request.method}_{slug(flow.request.pretty_url)}"

        headers = dict(flow.request.headers.items())
        (base.with_suffix(".req.headers.json")).write_text(
            json.dumps({
                "method": flow.request.method,
                "url": flow.request.pretty_url,
                "http_version": flow.request.http_version,
                "headers": headers,
            }, indent=2, ensure_ascii=False)
        )
        if flow.request.raw_content:
            (base.with_suffix(".req.body.bin")).write_bytes(flow.request.raw_content)
        print(f"[REQ #{self.n}] {flow.request.method} {flow.request.pretty_url[:100]}")

    def response(self, flow: http.HTTPFlow):
        if not matches(flow.request.host):
            return
        ts = time.strftime("%H%M%S")
        base = OUT / f"{ts}_{self.n:03d}_{flow.request.method}_{slug(flow.request.pretty_url)}"

        headers = dict(flow.response.headers.items())
        (base.with_suffix(".resp.headers.json")).write_text(
            json.dumps({
                "status": flow.response.status_code,
                "reason": flow.response.reason,
                "http_version": flow.response.http_version,
                "headers": headers,
            }, indent=2, ensure_ascii=False)
        )
        if flow.response.raw_content:
            (base.with_suffix(".resp.body.bin")).write_bytes(flow.response.raw_content)
        print(f"[RSP #{self.n}] {flow.response.status_code} {flow.request.pretty_url[:80]}")


addons = [CursorCapture()]
