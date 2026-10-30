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
    # Open the target URL as a launch argument rather than about:blank +
    # Page.navigate: without Page.enable (see below) a navigate on a blank
    # target can leave captureScreenshot waiting on a frame that never comes.
    f"--remote-debugging-port={PORT}", "--remote-allow-origins=*", URL,
])
time.sleep(1.5)

for _ in range(40):
    try:
        # Filter to real page targets -- the list can also contain devtools
        # and service-worker targets, whose sockets never yield a screenshot.
        tabs = [t for t in requests.get(f"http://localhost:{PORT}/json").json()
                if t.get("type") == "page" and "devtools" not in t.get("url", "")]
        if tabs:
            break
    except Exception:
        pass
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


# Deliberately NOT Page.enable: it streams lifecycle/frame events continuously
# for the live pages (Home, task detail -- both hold an open WebSocket), which
# starves this single-socket client and makes Page.captureScreenshot below
# appear to hang forever. Runtime.enable alone is enough to evaluate the click.
send("Runtime.enable")
# The SPA has to boot and fetch before anything is clickable/paintable.
time.sleep(9)

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

# Freeze animations/transitions so the live pages (Home's pulse and ticker)
# settle into a stable frame instead of repainting under the capture.
send("Runtime.evaluate", {"expression": """
    (function() {
        const s = document.createElement('style');
        s.textContent = '*,*::before,*::after{animation:none!important;transition:none!important}';
        document.head.appendChild(s);
        return true;
    })()
"""})
time.sleep(1)

result = send("Page.captureScreenshot", {"format": "png"})
with open(OUT, "wb") as f:
    f.write(base64.b64decode(result["result"]["data"]))

ws.close()
proc.terminate()
print(f"saved {OUT}")
