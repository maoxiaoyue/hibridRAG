#!/bin/bash
# log_watch.sh — 定期掃 gateway log，統計外部異常事件，超過閾值印摘要。
#
# 監控項：
#   • TLS handshake errors — 掃描器 / 憑證錯誤 client（噪音，可容忍）
#   • Unauthenticated       — 無 API key 或 key 錯（可能攻擊嘗試）
#   • PermissionDenied      — device binding 衝突（可能 key 洩漏）
#   • ResourceExhausted     — rate limit 命中（可能 flood）
#
# 使用：
#   bash log_watch.sh                  — 一次性摘要（過去 1 小時）
#   bash log_watch.sh --interval 60    — daemon 模式，每 60 秒摘要
#   bash log_watch.sh --systemd        — 印出 systemd timer/service 建議
#
# 部署（一次性 host 設定）：
#   加進 root crontab 每 15 分掃一次：
#     */15 * * * * /path/to/log_watch.sh 2>&1 | logger -t gateway-watch

set -uo pipefail
# 不用 -e：awk 找不到 log pattern 或 count=0 都會回非零，屬正常，不該讓 script 中止

LOG="${GATEWAY_LOG:-/tmp/claude-1000/-home-chris-go-projects-agent/94ab385a-a748-40f9-8ddc-cbf6587c2920/scratchpad/logs/agent_gateway.log}"
WINDOW_MIN="${WINDOW_MIN:-60}"           # 掃過去 N 分鐘
TLS_ERR_THRESHOLD="${TLS_ERR_THRESHOLD:-50}"
AUTH_FAIL_THRESHOLD="${AUTH_FAIL_THRESHOLD:-20}"
DEVICE_MISMATCH_THRESHOLD="${DEVICE_MISMATCH_THRESHOLD:-1}"
RATELIMIT_THRESHOLD="${RATELIMIT_THRESHOLD:-100}"

interval=0
if [[ "${1:-}" == "--interval" ]]; then
  interval=${2:-60}
elif [[ "${1:-}" == "--systemd" ]]; then
  cat <<EOF
# /etc/systemd/system/gateway-watch.service
[Unit]
Description=agent_gateway log watcher
[Service]
Type=oneshot
Environment=GATEWAY_LOG=$LOG
ExecStart=/bin/bash $(realpath "$0")

# /etc/systemd/system/gateway-watch.timer
[Unit]
Description=Run gateway-watch every 15 minutes
[Timer]
OnBootSec=5min
OnUnitActiveSec=15min
[Install]
WantedBy=timers.target

# 啟用：
#   sudo systemctl enable --now gateway-watch.timer
EOF
  exit 0
fi

count_since_min() {
  # $1 = pattern, $2 = window minutes
  local pattern="$1" mins="$2"
  awk -v mins="$mins" -v pat="$pattern" '
    BEGIN {
      # cutoff = now - mins*60 (as epoch)
      "date +%s" | getline now; close("date +%s");
      cutoff = now - mins*60;
    }
    /^[0-9]{4}\/[0-9]{2}\/[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}/ {
      # parse "YYYY/MM/DD HH:MM:SS" → epoch via mktime
      d = $1; t = $2;
      gsub("/", " ", d); gsub(":", " ", t);
      ts = mktime(d " " t);
      if (ts < cutoff) next;
      last_ok = 1;
    }
    last_ok && $0 ~ pat { c++ }
    END { print c+0 }
  ' "$LOG"
}

report() {
  local ts tls unauth denied ratelim tls_ips
  ts=$(date '+%Y-%m-%d %H:%M:%S')
  [[ -f "$LOG" ]] || { echo "[$ts] gateway log 不存在: $LOG"; return; }

  tls=$(count_since_min "TLS handshake error" "$WINDOW_MIN")
  unauth=$(count_since_min "Unauthenticated|api key invalid" "$WINDOW_MIN")
  denied=$(count_since_min "PermissionDenied|different device" "$WINDOW_MIN")
  ratelim=$(count_since_min "ResourceExhausted|rate limit exceeded" "$WINDOW_MIN")

  # 前 5 個 handshake error 的來源 IP（過濾 IPv4/IPv6）
  tls_ips=$(grep -oE 'TLS handshake error from [^:]+' "$LOG" 2>/dev/null | tail -50 \
            | sed 's/TLS handshake error from //' \
            | sort | uniq -c | sort -rn | head -5)

  local alert=0
  [[ $tls -ge $TLS_ERR_THRESHOLD ]]              && alert=1
  [[ $unauth -ge $AUTH_FAIL_THRESHOLD ]]         && alert=1
  [[ $denied -ge $DEVICE_MISMATCH_THRESHOLD ]]   && alert=1
  [[ $ratelim -ge $RATELIMIT_THRESHOLD ]]        && alert=1

  local prefix="ℹ"
  [[ $alert -eq 1 ]] && prefix="⚠"

  echo "[$ts] $prefix gateway-watch (past ${WINDOW_MIN}min)"
  printf "     TLS_handshake_errors=%d  Unauthenticated=%d  DeviceMismatch=%d  RateLimited=%d\n" \
    "$tls" "$unauth" "$denied" "$ratelim"
  if [[ -n "$tls_ips" ]]; then
    echo "     top TLS-error sources:"
    echo "$tls_ips" | sed 's/^/       /'
  fi

  # exit non-zero when there's an alert — 讓 cron 可以配合 MAILTO 或 systemd OnFailure
  return $alert
}

if [[ $interval -gt 0 ]]; then
  echo "gateway-watch daemon: 每 ${interval}s 掃一次過去 ${WINDOW_MIN} 分鐘 (log=$LOG)"
  while :; do
    report || true
    sleep "$interval"
  done
else
  report
fi
