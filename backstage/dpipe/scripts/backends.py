#!/usr/bin/env python3
"""Dummy backends for the demo: a plaintext HTTP server and a TCP echo server.

Usage:
  backends.py http <port> <name>
  backends.py echo <port>
"""
import socket
import socketserver
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    name = "backend"

    def _respond(self, body: bytes) -> None:
        self.send_response(200)
        self.send_header("Content-Type", "text/plain; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):  # noqa: N802
        body = f"{self.name}: GET {self.path} host={self.headers.get('Host')}\n".encode()
        self._respond(body)

    def do_POST(self):  # noqa: N802
        length = int(self.headers.get("Content-Length") or 0)
        payload = self.rfile.read(length)
        body = (
            f"{self.name}: POST {self.path} host={self.headers.get('Host')} "
            f"body={payload.decode(errors='replace')}\n"
        ).encode()
        self._respond(body)

    def log_message(self, format, *args):  # noqa: A002 - signature fixed by the base class
        sys.stderr.write(f"[{self.name}] {format % args}\n")


class EchoHandler(socketserver.BaseRequestHandler):
    def handle(self):
        while True:
            data = self.request.recv(65536)
            if not data:
                return
            self.request.sendall(data)


class ThreadedTCPServer(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


def main() -> int:
    if len(sys.argv) < 3:
        print(__doc__, file=sys.stderr)
        return 2
    mode, port = sys.argv[1], int(sys.argv[2])
    if mode == "http":
        Handler.name = sys.argv[3] if len(sys.argv) > 3 else "backend"
        HTTPServer(("127.0.0.1", port), Handler).serve_forever()
    elif mode == "echo":
        ThreadedTCPServer(("127.0.0.1", port), EchoHandler).serve_forever()
    else:
        print(__doc__, file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        pass
    except OSError as exc:
        print(f"backend failed: {exc}", file=sys.stderr)
        sys.exit(1)
    finally:
        socket.setdefaulttimeout(None)
