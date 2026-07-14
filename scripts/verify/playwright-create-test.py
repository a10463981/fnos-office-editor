#!/usr/bin/env python3
"""Test full create new doc flow."""
import sys, os, json, time, traceback
from playwright.sync_api import sync_playwright

NAS = "http://192.168.100.28:5666"
OUT = "/tmp/fnos-create"

def log(*a): print("[C]", *a, flush=True)

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
                if any(x in r.url for x in ["/cgi/ThirdParty/OfficeEditor", "/officeds", "/api/create"]):
                    captured.append({
                        "url": r.url, "status": r.status, "method": r.request.method,
                        "ct": r.headers.get("content-type",""),
                        "location": r.headers.get("location",""),
                        "req_body": r.request.post_data[:200] if r.request.post_data else "",
                    })
            except Exception: pass
        ctx.on("response", on_response)

        page = ctx.new_page()
        log("Login...")
        page.goto(NAS, wait_until="domcontentloaded", timeout=30000)
        # May already be logged in from prior session
        try:
            page.wait_for_selector('input[name="username"]', timeout=5000)
            log("  filling credentials")
            page.fill('input[name="username"]', "admin")
            page.fill('input[name="password"]', "admin")
            page.click('button[type="submit"], button')
            page.wait_for_timeout(8000)
        except Exception as e:
            log(f"  (need to wait for login...; re-check: {e})")
            # Last resort - try again with longer timeout
            try:
                page.wait_for_url(lambda u: "/login" not in u, timeout=15000)
            except Exception:
                page.wait_for_selector('input[name="username"]', timeout=15000)
                page.fill('input[name="username"]', "admin")
                page.fill('input[name="password"]', "admin")
                page.click('button[type="submit"], button')
                page.wait_for_timeout(8000)
        log(f"URL: {page.url}")
        page.screenshot(path=f"{OUT}/01-home.png", full_page=True)

        log("Click 'office 协作'")
        page.click('text="office 协作"')
        page.wait_for_timeout(5000)
        page.screenshot(path=f"{OUT}/02-tile.png", full_page=True)

        # Switch to office 协作 frame
        frames = page.frames
        target_frame = None
        for f in frames:
            if "OfficeEditor" in f.url and "path=" not in f.url:
                target_frame = f
                break
        if not target_frame and len(frames) > 1:
            target_frame = frames[1]
        log(f"Target frame: {target_frame.url[:120] if target_frame else 'NONE'}")

        if target_frame:
            target_frame.wait_for_load_state("domcontentloaded", timeout=15000)
            # Click Word button (or any of the 3)
            log("Click 'Word 文档' button")
            try:
                btn = target_frame.query_selector('button:has-text("Word")')
                if btn:
                    btn.click()
                    page.wait_for_timeout(8000)
                    log(f"After click URL: {page.url}")
                    page.screenshot(path=f"{OUT}/03-after-create.png", full_page=True)
                    frames = page.frames
                    log(f"Frames after click: {len(frames)}")
                    for f in frames:
                        log(f"  {f.url[:140]}")
                else:
                    log("Word button NOT FOUND")
                    # Print all buttons
                    btns = target_frame.query_selector_all("button")
                    log(f"buttons: {len(btns)}")
                    for b in btns[:5]:
                        log(f"  {b.inner_text()[:40]}")
            except Exception as e:
                log(f"err: {e}")

        log(f"\n--- captured {len(captured)} ---")
        for c in captured:
            log(f"  {c['status']} {c['method']} {c['url'][:200]}")
            log(f"     CT: {c['ct'][:60]}")
            if c.get("location"): log(f"     LOCATION: {c['location']}")

        with open(f"{OUT}/captured.json","w") as f: json.dump(captured, f, indent=2)
        log(f"saved {OUT}/captured.json")
        browser.close()

if __name__ == "__main__":
    try: main()
    except Exception as e: log("ERR:", e); traceback.print_exc()
