#!/usr/bin/env python3
"""Screenshot a page (optionally clicking a text-matched element first) via
headless Chrome's DevTools Protocol. Used to capture docs/img/*.png because
several views (task detail's Logs/Diff tabs) are client-side React state with
no URL param, so a plain `chrome --screenshot` single-shot can't reach them.

Usage: cdp_shot.py <url> [click-text] <output.png>
  click-text: exact text of a button/tab to click after the page loads
              (e.g. "Logs", "Diff"); omit to screenshot as-loaded.

Requires the venv in this repo's scratch tooling, or:
  pip install websocket-client requests
"""
import base64
import json
import subprocess
import sys
import time

import requests
import websocket

URL = sys.argv[1]
CLICK_TEXT = sys.argv[2] if len(sys.argv) > 2 else None
OUT = sys.argv[3] if len(sys.argv) > 3 else sys.argv[2]
if len(sys.argv) <= 3:
    CLICK_TEXT = None

PORT = 9333
proc = subprocess.Popen([
    # --headless=new (not the old --headless) -- the legacy headless mode's
    # Page.captureScreenshot can hang indefinitely on this stack.
    "google-chrome", "--headless=new", "--disable-gpu", "--no-sandbox",
    "--hide-scrollbars", "--window-size=1440,900",
    # --remote-allow-origins=* -- Chrome rejects the devtools websocket
    # handshake from a "foreign" origin without this.
    f"--remote-debugging-port={PORT}", "--remote-allow-origins=*", "about:blank",
])
time.sleep(1.5)

for _ in range(20):
    try:
        tabs = requests.get(f"http://localhost:{PORT}/json").json()
        break
    except Exception:
        time.sleep(0.5)
else:
    raise RuntimeError("chrome devtools not up")

ws = websocket.create_connection(tabs[0]["webSocketDebuggerUrl"], timeout=30)


def send(method, params=None):
    msg_id = int(time.time() * 1000) % 100000
    ws.send(json.dumps({"id": msg_id, "method": method, "params": params or {}}))
    while True:
        resp = json.loads(ws.recv())
        if resp.get("id") == msg_id:
            return resp


send("Page.enable")
send("Runtime.enable")
send("Page.navigate", {"url": URL})
time.sleep(3)

if CLICK_TEXT:
    js = f"""
    (function() {{
        const els = Array.from(document.querySelectorAll('button, div, span, a'));
        const target = els.find(el => el.textContent.trim() === {json.dumps(CLICK_TEXT)});
        if (target) {{ target.click(); return true; }}
        return false;
    }})()
    """
    send("Runtime.evaluate", {"expression": js})
    time.sleep(1.5)

result = send("Page.captureScreenshot", {"format": "png"})
with open(OUT, "wb") as f:
    f.write(base64.b64decode(result["result"]["data"]))

ws.close()
proc.terminate()
print(f"saved {OUT}")
