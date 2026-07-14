#!/bin/bash
# Ad-hoc verification of v1.0.34 CGI fixes — explicit, NOT a regression suite.
# Scope: bug #1 (POST /api/create with embedded ?), bug #2 (OnlyOffice main index.html content-type)
set -u
HOST=192.168.100.28; PASS=admin; USER=admin
PASS_COUNT=0; FAIL_COUNT=0
declare -a FAILURES
ok()  { echo "[OK]  $1"; PASS_COUNT=$((PASS_COUNT+1)); }
no()  { echo "[FAIL] $1"; FAIL_COUNT=$((FAIL_COUNT+1)); FAILURES+=("$1"); }
run() { sshpass -p "$PASS" ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 "$USER@$HOST" "$1" 2>/dev/null; }

echo "============================================================"
echo "  v1.0.34 — ad-hoc verification (scope: bug #1 + bug #2)"
echo "============================================================"

# ===== A. process / version =====
V=$(curl -s "http://$HOST:10088/api/version")
[[ "$V" == *1.0.34* ]] && ok "[A.1] connector v1.0.34" || no "[A.1] v=$V"
PID=$(run "pgrep -f '/officeeditor-connector --port 10088' | head -1")
[ -n "$PID" ] && ok "[A.2] connector running PID=$PID" || no "[A.2] no connector PID"

# ===== B. CGI deployed =====
CGI_MD5=$(run "md5sum /var/apps/OfficeEditor/target/ui/index.cgi" | awk '{print $1}')
[ "$CGI_MD5" = "2a8c6f9bc479f741a2e8e5ce3f48334c" ] \
  && ok "[B.1] CGI md5 matches v1.0.34 build (2a8c6f9b...)" \
  || no "[B.1] CGI md5=$CGI_MD5"

# ===== C. Bug #1: POST /api/create with embedded ? =====
echo ""
echo "[C] Bug #1: POST /api/create?type=docx&dir=... (path value with embedded ?)"
OUT=$(run '
CONTENT_LENGTH=0 REQUEST_METHOD=POST \
QUERY_STRING="action=api&path=%2Fapi%2Fcreate%3Ftype%3Ddocx%26dir%3D%252Fvol1%252F1000" \
HTTP_HOST=192.168.100.28 HTTP_X_TRIM_USERID=1000 HTTP_X_TRIM_USERNAME=admin HTTP_X_TRIM_ISADMIN=true \
PATH_INFO=/cgi/ThirdParty/OfficeEditor/index.cgi \
SCRIPT_FILENAME=/var/apps/OfficeEditor/target/ui/index.cgi \
SCRIPT_NAME=/var/apps/OfficeEditor SERVER_NAME=localhost SERVER_PORT=80 SERVER_PROTOCOL=HTTP/1.1 \
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
python3 -u /var/apps/OfficeEditor/target/ui/index.cgi 2>/dev/null
')
HDR=$(echo "$OUT" | head -1)
BODY=$(echo "$OUT" | tail -n +4)
BODY=$(echo "$OUT" | tail -n +4 | tr -d '\r')
echo "  HDR: $HDR"
echo "  BODY: $BODY" | head -c 200
echo ""
[ "${HDR%$'\r'}" = "Status: 200 OK" ] && ok "[C.1] POST /api/create returned 200 OK (was 400 in v1.0.33)" \
                              || no "[C.1] hdr=$HDR"
[[ "$BODY" == *'"path":'* ]] && ok "[C.2] response JSON has \"path\"" || no "[C.2] no path field: $BODY"
[[ "$BODY" == *新建Word文档* ]] && ok "[C.3] file name = 新建Word文档_<ts>.docx" || no "[C.3] name missing"

# Regression: same flow for xlsx + pptx
for t in xlsx pptx; do
  OUT=$(run "
CONTENT_LENGTH=0 REQUEST_METHOD=POST \
QUERY_STRING=\"action=api&path=%2Fapi%2Fcreate%3Ftype%3D${t}%26dir%3D%252Fvol1%252F1000\" \
HTTP_HOST=192.168.100.28 HTTP_X_TRIM_USERID=1000 HTTP_X_TRIM_USERNAME=admin HTTP_X_TRIM_ISADMIN=true \
PATH_INFO=/cgi/ThirdParty/OfficeEditor/index.cgi \
SCRIPT_FILENAME=/var/apps/OfficeEditor/target/ui/index.cgi \
SCRIPT_NAME=/var/apps/OfficeEditor SERVER_NAME=localhost SERVER_PORT=80 SERVER_PROTOCOL=HTTP/1.1 \
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
python3 -u /var/apps/OfficeEditor/target/ui/index.cgi 2>/dev/null
")
  HDR=$(echo "$OUT" | head -1)
  [ "${HDR%$'\r'}" = "Status: 200 OK" ] && ok "[C.$t] POST /api/create?type=$t → 200" \
                                || no "[C.$t] hdr=$HDR"
done

# ===== D. Bug #2: OnlyOffice main index.html content-type =====
echo ""
echo "[D] Bug #2: OnlyOffice editor main index.html served as text/html"
for variant in documenteditor spreadsheeteditor presentationeditor; do
  OUT=$(run "
REQUEST_METHOD=GET \
QUERY_STRING=\"action=officeds&path=%2Fofficeds%2F9.4.0-af626e5c71a7e58e3571c03a0cc69ca5%2Fweb-apps%2Fapps%2F${variant}%2Fmain%2Findex.html%3F_dc%3D9.4.0-129%26lang%3Dzh%26customer%3DONLYOFFICE\" \
HTTP_HOST=192.168.100.28 \
PATH_INFO=/cgi/ThirdParty/OfficeEditor/index.cgi \
SCRIPT_FILENAME=/var/apps/OfficeEditor/target/ui/index.cgi \
SCRIPT_NAME=/var/apps/OfficeEditor SERVER_NAME=localhost SERVER_PORT=80 SERVER_PROTOCOL=HTTP/1.1 \
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
python3 -u /var/apps/OfficeEditor/target/ui/index.cgi 2>/dev/null
")
  HDR=$(echo "$OUT" | head -2)
  if [[ "$HDR" == *"Content-Type: text/html; charset=utf-8"* ]]; then
    ok "[D.$variant] OnlyOffice $variant/main/index.html → text/html"
  else
    no "[D.$variant] hdr=$HDR"
  fi
done

# ===== E. Regression: existing routes unchanged =====
echo ""
echo "[E] Regression: existing routes"
check_ct() {
  local desc="$1" url="$2" want="$3"
  local hdr; hdr=$(curl -s -D - -o /dev/null "http://$HOST:10088$url" 2>/dev/null)
  if [[ "$hdr" == *"$want"* ]]; then ok "[E] $desc"; else no "[E] $desc → hdr=${hdr%%$'\r'}"; fi
}
check_ct "sponsor/donate → image/png"   "/sponsor/donate"  "image/png"
check_ct "officeds/api.js → application/javascript" "/officeds/web-apps/apps/api/documents/api.js" "application/javascript"
check_ct "api/version → application/json" "/api/version" "application/json"
check_ct "health → application/json"     "/health"        "application/json"

# ===== F. CGI action=api returns connector's content-type (not hardcoded) =====
echo ""
echo "[F] CGI action=api content-type passthrough"
H=$(run '
REQUEST_METHOD=GET \
QUERY_STRING="action=api&path=%2Fsponsor%2Fdonate" \
HTTP_HOST=192.168.100.28 \
PATH_INFO=/cgi/ThirdParty/OfficeEditor/index.cgi \
SCRIPT_FILENAME=/var/apps/OfficeEditor/target/ui/index.cgi \
SCRIPT_NAME=/var/apps/OfficeEditor SERVER_NAME=localhost SERVER_PORT=80 SERVER_PROTOCOL=HTTP/1.1 \
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
python3 -u /var/apps/OfficeEditor/target/ui/index.cgi 2>/dev/null
' | head -2)
[[ "$H" == *"image/png"* ]] && ok "[F] action=api /sponsor/donate → image/png (passthrough works)" \
                            || no "[F] header: $H"

H=$(run '
REQUEST_METHOD=GET \
QUERY_STRING="action=api&path=%2Fapi%2Fversion" \
HTTP_HOST=192.168.100.28 \
PATH_INFO=/cgi/ThirdParty/OfficeEditor/index.cgi \
SCRIPT_FILENAME=/var/apps/OfficeEditor/target/ui/index.cgi \
SCRIPT_NAME=/var/apps/OfficeEditor SERVER_NAME=localhost SERVER_PORT=80 SERVER_PROTOCOL=HTTP/1.1 \
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
python3 -u /var/apps/OfficeEditor/target/ui/index.cgi 2>/dev/null
' | head -2)
[[ "$H" == *"application/json"* ]] && ok "[F] action=api /api/version → application/json" \
                                 || no "[F] header: $H"

# ===== SUMMARY =====
echo ""
echo "============================================================"
echo "  RESULT: pass=$PASS_COUNT fail=$FAIL_COUNT  (ad-hoc, scope: v1.0.34 CGI fixes only)"
if [ $FAIL_COUNT -gt 0 ]; then
  echo "  FAILURES:"
  for f in "${FAILURES[@]}"; do echo "    - $f"; done
fi
echo "============================================================"
exit $FAIL_COUNT
