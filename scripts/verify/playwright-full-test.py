"""Full e2e v2 - more careful selector waits"""
import os, json, traceback
from playwright.sync_api import sync_playwright

NAS = "http://192.168.100.28:5666"
OUT = "/tmp/fnos-fulltest"

def log(*a): print("[F]", *a, flush=True)
caps = []
errs = []

def main():
    os.makedirs(OUT, exist_ok=True)
    with sync_playwright() as pw:
        browser = pw.chromium.launch(headless=True,
            args=["--no-sandbox","--disable-setuid-sandbox","--disable-dev-shm-usage","--disable-gpu","--disable-web-security"])
        ctx = browser.new_context(ignore_https_errors=True, viewport={"width":1280,"height":800}, locale="zh-CN")
        def on_response(r):
            try:
                if "/cgi/ThirdParty/OfficeEditor" in r.url or "/officeds" in r.url:
                    caps.append({"url": r.url, "status": r.status,
                                 "method": r.request.method, "ct": r.headers.get("content-type","")})
            except Exception: pass
        ctx.on("response", on_response)
        page = ctx.new_page()
        page.on("console", lambda m: errs.append(f"{m.type}: {m.text[:300]}") if m.type == "error" else None)
        page.on("pageerror", lambda e: errs.append(f"pageerror: {e}"))

        log("Login")
        page.goto(NAS + "/login", wait_until="load", timeout=30000)
        # wait for the form to be visible — could be slow due to fonts
        page.wait_for_load_state("networkidle", timeout=15000)
        page.wait_for_timeout(3000)
        try:
            page.wait_for_selector('input[name="username"]', timeout=20000, state="visible")
        except Exception as e:
            log(f"  selector error: {e}")
            page.screenshot(path=f"{OUT}/login-fail.png")
            log(f"URL: {page.url}")
            log(f"body[:300]: {page.content()[:300]}")
            return
        page.fill('input[name="username"]', "admin")
        page.fill('input[name="password"]', "admin")
        page.screenshot(path=f"{OUT}/login-filled.png")
        page.click('button[type="submit"]', timeout=10000)
        page.wait_for_timeout(8000)
        log(f"  URL after login: {page.url}")
        page.screenshot(path=f"{OUT}/home.png")

        if "/login" in page.url:
            log("  still on login - check screenshot")
            return

        # click office tile
        log("Click tile")
        page.locator('text="office 协作"').first.click(timeout=15000)
        page.wait_for_timeout(8000)
        log(f"  URL: {page.url}")
        # frames
        frames = page.frames
        log(f"  frames: {len(frames)}")
        editor = None
        for f in frames:
            if "OfficeEditor" in f.url and "path=" not in f.url and "index.cgi" in f.url:
                editor = f
                break
        if not editor:
            for f in frames:
                if "OfficeEditor" in f.url:
                    editor = f
                    break
        log(f"  editor frame: {editor.url[:120] if editor else 'NONE'}")

        if editor:
            log("  click history item")
            editor.wait_for_load_state("domcontentloaded", timeout=10000)
            items = editor.query_selector_all(".history-item")
            log(f"  history: {len(items)}")
            if items:
                href = items[0].get_attribute("href")
                log(f"  click href={href}")
                items[0].click(timeout=10000)
                page.wait_for_timeout(20000)
                page.screenshot(path=f"{OUT}/after-history.png")

        log(f"\n=== {len(caps)} captured ===")
        # show last 25
        for c in caps[-25:]:
            log(f"  {c['status']} {c['method']} {c['url'][:200]}")
            log(f"     CT: {c['ct'][:60]}")

        log(f"\n=== errors ({len(errs)}) ===")
        for e in errs[-15:]:
            log(f"  {e}")

        with open(f"{OUT}/captured.json","w") as f: json.dump(caps, f, indent=2)
        browser.close()

if __name__ == "__main__":
    try: main()
    except Exception as e: log("ERR:", e); traceback.print_exc()
