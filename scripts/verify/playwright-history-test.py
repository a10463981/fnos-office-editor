#!/usr/bin/env python3
"""Click history file to test editor launch."""
import sys, os, json, time, traceback
from playwright.sync_api import sync_playwright

NAS = "http://192.168.100.28:5666"
OUT = "/tmp/fnos-history"

def log(*a): print("[H]", *a, flush=True)

def main():
    os.makedirs(OUT, exist_ok=True)
    with sync_playwright() as pw:
        browser = pw.chromium.launch(
            headless=True,
            args=["--no-sandbox","--disable-setuid-sandbox","--disable-dev-shm-usage","--disable-gpu","--disable-web-security"],
        )
        ctx = browser.new_context(ignore_https_errors=True, viewport={"width":1280,"height":800}, locale="zh-CN")

        captured = []

        def on_response(r):
            try:
                if any(x in r.url for x in ["/cgi/ThirdParty/OfficeEditor", "/officeds"]):
                    captured.append({
                        "url": r.url, "status": r.status, "method": r.request.method,
                        "ct": r.headers.get("content-type",""),
                        "location": r.headers.get("location",""),
                        "set_cookie": (r.headers.get("set-cookie","") or "")[:200],
                        "content_disp": r.headers.get("content-disposition",""),
                    })
            except Exception:
                pass
        ctx.on("response", on_response)

        page = ctx.new_page()
        # Track console
        console = []
        page.on("console", lambda m: console.append(f"{m.type}: {m.text[:400]}"))
        page.on("pageerror", lambda e: console.append(f"pageerror: {str(e)[:400]}"))

        log("Login...")
        page.goto(NAS, wait_until="domcontentloaded", timeout=30000)
        page.wait_for_selector('input[name="username"]', timeout=15000)
        page.fill('input[name="username"]', "admin")
        page.fill('input[name="password"]', "admin")
        page.click('button[type="submit"], button')
        page.wait_for_timeout(8000)
        log(f"URL after login: {page.url}")
        page.screenshot(path=f"{OUT}/01-home.png", full_page=True)

        log("Click 'office 协作' tile")
        page.click('text="office 协作"')
        page.wait_for_timeout(4000)
        page.screenshot(path=f"{OUT}/02-tile.png", full_page=True)

        log("Find frame, click history item, see if editor loads")
        # The office 协作 opens in an iframe; switch to it
        frames = page.frames
        log(f"Frames: {len(frames)}")
        for f in frames:
            log(f"  url: {f.url[:120]}")

        target_frame = None
        for f in frames:
            if "OfficeEditor" in f.url and "path=" not in f.url:
                target_frame = f
                break
        if not target_frame and len(frames) > 1:
            target_frame = frames[1]

        if target_frame:
            log(f"  using frame: {target_frame.url[:120]}")
            target_frame.wait_for_load_state("domcontentloaded", timeout=15000)
            # Click first history item
            history_items = target_frame.query_selector_all(".history-item")
            log(f"  history items in frame: {len(history_items)}")
            if history_items:
                href = history_items[0].get_attribute("href")
                log(f"  clicking first item href={href}")
                history_items[0].click()
                page.wait_for_timeout(6000)
                log(f"  After click URL: {page.url}")
                # Check frames again
                frames = page.frames
                for f in frames:
                    log(f"  frame: {f.url[:120]}")
                page.screenshot(path=f"{OUT}/03-after-history.png", full_page=True)
        else:
            log("  ! no suitable frame")

        log(f"\n--- captured {len(captured)} responses ---")
        bad = [c for c in captured if c["status"] >= 400]
        log(f"  bad ones: {len(bad)}")
        for c in bad:
            log(f"   {c['status']} {c['method']} {c['url'][:200]}")
            log(f"      CT: {c['ct'][:80]}")
            if c.get('content_disp'):
                log(f"      DISPOSITION: {c['content_disp']}")
            if c['location']:
                log(f"      LOCATION: {c['location']}")

        log(f"\n--- ALL captured ---")
        for c in captured[:30]:
            log(f"  {c['status']} {c['method']} {c['url'][:120]}")
            log(f"     CT: {c['ct'][:60]}")
            if c.get('content_disp'):
                log(f"     DISP: {c['content_disp']}")

        log(f"\n--- console log (last 30) ---")
        for c in console[-30:]:
            log(f"  {c}")

        with open(f"{OUT}/captured.json","w") as f: json.dump(captured, f, indent=2)
        with open(f"{OUT}/console.json","w") as f: json.dump(console, f, indent=2)
        log("done")

        browser.close()

if __name__ == "__main__":
    try: main()
    except Exception as e: log("ERR:", e); traceback.print_exc()
