# scripts/verify — Verification scripts (NOT regression suite)

These are **ad-hoc, manual** verification scripts used during development.
They are NOT part of CI and NOT meant to be run frequently.

## verify-v1.0.34-cgi.sh
- Tests CGI bug fixes (POST /api/create with embedded ?, OnlyOffice content-type)
- Ad-hoc verification of /tmp/hermes-verify-v1.0.34-cgi.sh scope

## verify-v1.0.35-resources.sh
- Tests OnlyOffice HTML resource rewriting (relative src/href → absolute CGI URL)
- Ad-hoc verification of /tmp/hermes-verify-v1.0.35-resources.sh scope

## playwright-*-test.py
- Real browser (Playwright + headless Chromium) flow tests
- Requires `playwright install chromium`
- Run against test machine; replace `192.168.100.28` as needed
