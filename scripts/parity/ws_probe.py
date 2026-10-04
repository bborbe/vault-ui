#!/usr/bin/env python3
"""WebSocket parity probe.

Connects one client to the Go backend's /ws and one to the Python backend's
/ws, then drives the same two events against both and records the frames each
backend emits, in order, to ws-go.jsonl / ws-py.jsonl in the output directory:

  1. a watcher-originated change — a new task file written into the shared
     fixture vault (both backends watch it);
  2. a route-originated broadcast — the same PATCH flag request to each backend.

A ping/pong round-trip is asserted on both clients first but is not part of the
compared sequence. Exits non-zero (with a message) when a backend does not
answer the ping, so a probe that silently collected nothing cannot pass.
"""

import asyncio
import json
import os
import sys
import urllib.request

import websockets

# vault-cli debounces watcher events for ~100 ms per path; the settle window
# must comfortably exceed that so a frame is not cut off mid-flight.
SETTLE_SECONDS = 1.5
PING_TIMEOUT_SECONDS = 5.0


def write_task(vault: str, name: str) -> None:
    """Write a new task file into the vault's tasks folder."""
    path = os.path.join(vault, "24 Tasks", name)
    with open(path, "w", encoding="utf-8") as handle:
        handle.write("---\nstatus: todo\n---\n\n# WsProbe\n")


def patch_flag(base: str) -> None:
    """Issue the same route-originated broadcast to a backend."""
    request = urllib.request.Request(
        base + "/api/tasks/TaskOne/flag?vault=personal",
        data=json.dumps({"flag": False}).encode("utf-8"),
        method="PATCH",
        headers={"Content-Type": "application/json"},
    )
    # ProxyHandler({}) bypasses the container's HTTP proxy, which rejects
    # loopback connections with 403 Filtered.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(request, timeout=10) as response:
        response.read()


async def drain(ws, frames: list, settle: float) -> None:
    """Collect frames until a full settle window passes with none."""
    while True:
        try:
            message = await asyncio.wait_for(ws.recv(), timeout=settle)
        except (TimeoutError, websockets.ConnectionClosed):
            return
        frames.append(message)


async def drain_discard(ws, settle: float) -> None:
    """Drop frames already queued from earlier harness activity."""
    while True:
        try:
            await asyncio.wait_for(ws.recv(), timeout=settle)
        except (TimeoutError, websockets.ConnectionClosed):
            return


async def await_pong(ws, timeout: float) -> None:
    """Read until the pong arrives, tolerating stale broadcast frames."""
    loop = asyncio.get_running_loop()
    deadline = loop.time() + timeout
    while True:
        remaining = deadline - loop.time()
        if remaining <= 0:
            raise RuntimeError("backend did not answer ping within the timeout")
        message = await asyncio.wait_for(ws.recv(), remaining)
        if message == "pong":
            return


async def run(go_base: str, py_base: str, vault: str, out_dir: str) -> None:
    go_frames: list = []
    py_frames: list = []
    go_url = go_base.replace("http://", "ws://") + "/ws"
    py_url = py_base.replace("http://", "ws://") + "/ws"

    # proxy=None: the container exports an HTTP proxy that rejects loopback
    # connections; the backends are local, so never proxy them.
    async with (
        websockets.connect(go_url, open_timeout=10, proxy=None) as go_ws,
        websockets.connect(py_url, open_timeout=10, proxy=None) as py_ws,
    ):
        # Flush broadcasts queued by earlier harness activity (the mutation
        # section resets the fixture vault, which both watchers observe).
        await asyncio.gather(
            drain_discard(go_ws, SETTLE_SECONDS),
            drain_discard(py_ws, SETTLE_SECONDS),
        )

        # Ping/pong parity: both backends answer a text "ping" with "pong".
        await go_ws.send("ping")
        await await_pong(go_ws, PING_TIMEOUT_SECONDS)
        await py_ws.send("ping")
        await await_pong(py_ws, PING_TIMEOUT_SECONDS)

        # Watcher-originated frame.
        write_task(vault, "WsProbe.md")
        await asyncio.gather(
            drain(go_ws, go_frames, SETTLE_SECONDS),
            drain(py_ws, py_frames, SETTLE_SECONDS),
        )

        # Route-originated broadcast. Issued sequentially (Go then Python) so the
        # two writes to the shared vault cannot interleave; both route frames
        # land well inside the watcher debounce window.
        await asyncio.to_thread(patch_flag, go_base)
        await asyncio.to_thread(patch_flag, py_base)
        await asyncio.gather(
            drain(go_ws, go_frames, SETTLE_SECONDS),
            drain(py_ws, py_frames, SETTLE_SECONDS),
        )

    _write_frames(os.path.join(out_dir, "ws-go.jsonl"), go_frames)
    _write_frames(os.path.join(out_dir, "ws-py.jsonl"), py_frames)


def _write_frames(path: str, frames: list) -> None:
    with open(path, "w", encoding="utf-8") as handle:
        for frame in frames:
            handle.write(frame + "\n")


def main(argv: list) -> int:
    if len(argv) != 5:
        print(
            "usage: ws_probe.py <go_base> <py_base> <vault> <out_dir>",
            file=sys.stderr,
        )
        return 2
    try:
        asyncio.run(run(*argv[1:]))
    except Exception as error:
        print(f"ws probe error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
