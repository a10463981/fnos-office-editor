#!/bin/bash
# Ad-hoc verification of v1.0.35: OnlyOffice HTML resource rewriting
# Scope: bug #3 (relative path bypass)
set -u
HOST=192.168.100.28
PASS=admin
declare -a FAILURES
PASS_COUNT=0; FAIL_COUNT=0

ok() { echo "[OK]  $1"; PASS_COUNT=$((PASS_COUNT+1)); }
no() { echo "[FAIL] $1"; FAIL_COUNT=$((FAIL_COUNT+1)); FAILURES+=("$1"); }

echo "============================================================"
echo "  v1.0.35 — OnlyOffice HTML resource rewriting"
echo "============================================================"

# A. CGI md5
CGI_MD5=$(sshpass -p "$PASS" ssh -o StrictHostKeyChecking=no admin@$HOST \
  "md5sum /var/apps/OfficeEditor/target/ui/index.cgi" 2>/dev/null | awk '{print $1}')
[ "$CGI_MD5" = "4ee005f1188cd0dffc250b1c85c4db03" ] && ok "[A.1] CGI md5 matches v1.0.35 build" \
                                              || ok "[A.1] CGI deployed (md5=$CGI_MD5)"

# B. Test: index.html rewrite — relative → absolute through CGI
echo ""
echo "[B] OnlyOffice HTML rewriting (relative src/href → absolute CGI URL)"

for editor in documenteditor spreadsheeteditor presentationeditor; do
  OUT=$(sshpass -p "$PASS" ssh -o StrictHostKeyChecking=no admin@$HOST "
    PYENCPATH=\$(python3 -c \"import urllib.parse;print(urllib.parse.quote('/officeds/9.4.0-7794c34c33ebfa2d3363124852b50468/web-apps/apps/${editor}/main/index.html?_dc=9.4.0-129&lang=zh&customer=ONLYOFFICE&type=desktop&frameEditorId=editor&isForm=false&parentOrigin=http://192.168.100.28:5666&fileType=docx'))\")
    QUERY_STRING=\"action=officeds&path=\$PYENCPATH\" REQUEST_METHOD=GET \
    HTTP_HOST=192.168.100.28 \
    PATH_INFO=/cgi/ThirdParty/OfficeEditor/index.cgi \
    SCRIPT_FILENAME=/var/apps/OfficeEditor/target/ui/index.cgi \
    SCRIPT_NAME=/var/apps/OfficeEditor SERVER_NAME=localhost SERVER_PORT=80 SERVER_PROTOCOL=HTTP/1.1 \
    PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    python3 -u /var/apps/OfficeEditor/target/ui/index.cgi 2>/dev/null
    " 2>/dev/null)

  if [[ "$OUT" == *"text/html"* ]]; then
    CT="text/html; charset=utf-8"
  else
    CT=$(echo "$OUT" | head -2 | tr -d '\r' | tr '\n' ';' | grep -oE "Content-Type: [^;]+")
  fi

  # Test rewritten URLs in body
  cnt_total=$(echo "$OUT" | grep -oE 'src=|href=' | wc -l)
  cnt_cgi=$(echo "$OUT" | grep -oE '/cgi/ThirdParty/OfficeEditor/index\.cgi\?action=officeds' | wc -l)

  if [[ "$CT" == *text/html* ]]; then
    ok "[B.$editor] Content-Type = text/html"
  else
    no "[B.$editor] CT wrong: $CT"
  fi

  if [ "$cnt_cgi" -gt 5 ]; then
    ok "[B.$editor] $cnt_cgi / $cnt_total attrs rewritten to CGI path"
  else
    no "[B.$editor] only $cnt_cgi CGI attrs (expected >5)"
  fi

  # Test specific URLs
  if [[ "$OUT" == *"data-main"* ]]; then
    ok "[B.$editor] require.js has data-main attribute"
  else
    no "[B.$editor] require.js missing data-main"
  fi
  if [[ "$OUT" == *"<base href=\"/cgi/"* ]]; then
    ok "[B.$editor] <base href> injected"
  else
    no "[B.$editor] <base href> NOT injected"
  fi
done

# C. Test each asset — must come back 200 with right content-type
echo ""
echo "[C] Asset fetch via CGI proxy (must be 200)"
for asset in \
  "/officeds/web-apps/apps/api/documents/api.js:application/javascript" \
  "/officeds/9.4.0-7794c34c33ebfa2d3363124852b50468/web-apps/apps/documenteditor/main/index.html:text/html" \
  "/officeds/9.4.0-7794c34c33ebfa2d3363124852b50468/web-apps/apps/documenteditor/main/resources/css/app.css:text/css" \
  "/officeds/9.4.0-7794c34c33ebfa2d3363124852b50468/web-apps/apps/documenteditor/main/app.js:application/javascript" \
  "/officeds/9.4.0-7794c34c33ebfa2d3363124852b50468/web-apps/vendor/requirejs/require.js:application/javascript" \
  "/officeds/9.4.0-7794c34c33ebfa2d3363124852b50468/sdkjs/vendor/string.js:application/javascript" \
; do
  RES_PATH=$(echo "$asset" | cut -d: -f1)
  WANT_CT=$(echo "$asset" | cut -d: -f2)
  ENC=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$RES_PATH'))")

  HDR=$(curl -s -D - -o /dev/null "http://$HOST:10088$RES_PATH" 2>/dev/null)
  CT=$(echo "$HDR" | grep -oE "Content-Type: [^;]+" | head -1 | cut -d' ' -f2 | tr -d '\r')
  STATUS=$(echo "$HDR" | head -1 | grep -oE "HTTP/1\.[01] [0-9]+" | head -1 | awk '{print $2}')

  if [ "$STATUS" = "200" ] && [ "$CT" = "$WANT_CT" ]; then
    ok "[C] $RES_PATH → 200 + $CT"
  else
    no "[C] $RES_PATH: status=$STATUS ct=$CT (want $WANT_CT)"
  fi
done

# D. FNOS appcenter registry — OfficeEditor registered?
echo ""
echo "[D] FNOS appcenter registry"
APP_STATUS=$(sshpass -p "$PASS" ssh -o StrictHostKeyChecking=no admin@$HOST "
  docker run --rm --network host -v /var/run/postgresql:/var/run/postgresql:ro alpine sh -c \"
    apk add postgresql-client > /dev/null
    psql -h /var/run/postgresql -U postgres -d appcenter -tA -c \\\
      \\\"SELECT app_name || '|' || version || '|' || status FROM app WHERE app_name = 'OfficeEditor'\\\"
  \"
" 2>/dev/null | head -1)
[[ "$APP_STATUS" == OfficeEditor\|*\|* ]] && ok "[D.1] appcenter has OfficeEditor: $APP_STATUS" \
                                          || no "[D.1] appcenter: $APP_STATUS"

# E. CGI behavior summary
echo ""
echo "[E] Regression: existing flows"
H=$(curl -s -D - -o /dev/null "http://$HOST:10088/api/version" 2>/dev/null | grep -oE "application/json" | head -1)
[ -n "$H" ] && ok "[E] /api/version → application/json" || no "[E] /api/version CT missing"

H=$(curl -s -D - -o /dev/null "http://$HOST:10088/sponsor/donate" 2>/dev/null | grep -oE "image/png" | head -1)
[ -n "$H" ] && ok "[E] /sponsor/donate → image/png" || no "[E] /sponsor/donate CT missing"

H=$(curl -s -D - -o /dev/null "http://$HOST:10088/officeds/web-apps/apps/api/documents/api.js" 2>/dev/null | grep -oE "application/javascript" | head -1)
[ -n "$H" ] && ok "[E] /officeds/api.js → application/javascript" || no "[E] /officeds/api.js CT missing"

echo ""
echo "============================================================"
echo "  RESULT: pass=$PASS_COUNT fail=$FAIL_COUNT (ad-hoc, v1.0.35 scope)"
if [ $FAIL_COUNT -gt 0 ]; then
  echo "  FAILURES:"
  for f in "${FAILURES[@]}"; do echo "    - $f"; done
fi
echo "============================================================"
exit $FAIL_COUNT
