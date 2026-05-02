#!/usr/bin/env python3
"""
Nexspence webhook receiver — development / integration testing helper.

Usage:
  python scripts/webhook-receiver.py [--port PORT] [--secret SECRET] [--once]

Options:
  --port PORT      Listen port (default: 8888)
  --secret SECRET  HMAC-SHA256 signing secret; if set, rejects invalid signatures
  --once           Exit after receiving the first valid event
"""
import argparse
import hashlib
import hmac
import http.server
import json
import sys
from datetime import datetime, timezone


def verify_signature(secret: str, body: bytes, header: str) -> bool:
    expected = "sha256=" + hmac.new(
        secret.encode(), body, hashlib.sha256
    ).hexdigest()
    return hmac.compare_digest(expected, header)


def make_handler(secret: "str | None", once: bool, stop: "list[bool]"):
    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, fmt, *args):
            pass  # suppress default request log

        def do_POST(self):
            length = int(self.headers.get("Content-Length", 0))
            body = self.rfile.read(length)

            if secret:
                sig = self.headers.get("X-Nexspence-Signature", "")
                if not verify_signature(secret, body, sig):
                    self.send_response(403)
                    self.end_headers()
                    self.wfile.write(b"invalid signature\n")
                    print(f"[{now()}] REJECTED — bad signature")
                    return

            event = self.headers.get("X-Nexspence-Event", "unknown")
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            self.wfile.write(b"ok\n")

            try:
                payload = json.loads(body)
            except json.JSONDecodeError:
                payload = {"raw": body.decode(errors="replace")}

            color = {
                "artifact.published": "\033[32m",
                "artifact.deleted":   "\033[31m",
                "repo.created":       "\033[34m",
                "proxy.error":        "\033[33m",
                "webhook.test":       "\033[36m",
            }.get(event, "\033[0m")
            reset = "\033[0m" if sys.stdout.isatty() else ""
            c = color if sys.stdout.isatty() else ""

            print(f"\n{c}▶ {event}{reset}  [{now()}]")
            print(json.dumps(payload, indent=2, default=str))

            if once:
                stop.append(True)

    return Handler


def now() -> str:
    return datetime.now(timezone.utc).strftime("%H:%M:%S")


def main():
    p = argparse.ArgumentParser(description="Nexspence webhook receiver")
    p.add_argument("--port", type=int, default=8888)
    p.add_argument("--secret", default=None)
    p.add_argument("--once", action="store_true")
    args = p.parse_args()

    stop: "list[bool]" = []
    handler = make_handler(args.secret, args.once, stop)
    server = http.server.HTTPServer(("", args.port), handler)

    print(f"Listening on :{args.port}" + (" (HMAC verification ON)" if args.secret else ""))
    print("Waiting for webhook events… (Ctrl-C to quit)\n")

    try:
        while not stop:
            server.handle_request()
    except KeyboardInterrupt:
        print("\nBye.")


if __name__ == "__main__":
    main()
