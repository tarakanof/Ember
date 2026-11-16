#!/usr/bin/env bash
set -euo pipefail

# Performance gate: build the server and producers from this tree, run them
# on scratch ports with scratch homes, and fail when a measured number
# exceeds its budget in BUDGETS below.
#
# Usage:
#   scripts/perf-gate.sh               # the whole gate, about 3 minutes
#   scripts/perf-gate.sh --soak <min>  # the same with a <min>-minute server-under-load run
#
# Live state is never touched: scratch HOME, data dir, port and token, no mDNS
# advertising (EMBER_MDNS_ADVERTISE=0), no clock I/O (EMBER_CLOCK=off), and
# stubs for `security` and `claude` ahead of PATH for the producers.

usage() { sed -n '4,15p' "$0" >&2; exit 2; }

BUDGETS='
check                         budget  measured   what
server_idle_cpu               0.10    0.01/0.01  CPU s of the idle server over 60 s
server_idle_goroutine_growth  0       0/0        goroutines after 60 s idle minus after startup
server_load_cpu               0.55    0.14/0.17  CPU s of the server per minute under load
server_load_goroutine_growth  6       0/0        goroutines, end of the load run minus its middle
server_load_rss_growth_kib    2048    688/264    RSS KiB, end of the load run minus its middle
claude_idle_cpu               0.10    0.02/0.00  CPU s of the idle Claude producer over 60 s
claude_goroutine_growth       2       0/0        goroutines, a 65 s-old Claude producer minus a 35 s-old one
codex_idle_cpu                0.10    0.01/0.00  CPU s of the idle Codex producer over 60 s
codex_goroutine_growth        2       0/0        goroutines, a 65 s-old Codex producer minus a 35 s-old one
t3_idle_cpu                   0.10    0.01/0.00  CPU s of the idle T3 producer over 60 s
t3_goroutine_growth           2       0/0        goroutines, a 65 s-old T3 producer minus a 35 s-old one
'

SOAK_MIN=0
while [ $# -gt 0 ]; do
  case "$1" in
    --soak)
      [ $# -ge 2 ] || usage
      case "$2" in ''|*[!0-9]*|0) echo "error: --soak needs a whole number of minutes" >&2; exit 2 ;; esac
      SOAK_MIN="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) echo "unknown argument: $1" >&2; usage ;;
  esac
done

IDLE_SECONDS=60
LOAD_SECONDS=60
[ "$SOAK_MIN" -eq 0 ] || LOAD_SECONDS=$((SOAK_MIN * 60))
LOAD_MINUTES=$((LOAD_SECONDS / 60))
WARMUP_SECONDS=5
SAMPLE_SECONDS=2

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
for tool in go curl jq awk ps pkill getconf; do
  command -v "$tool" >/dev/null 2>&1 || { echo "error: $tool not found" >&2; exit 1; }
done

WORK="$(mktemp -d "${TMPDIR:-/tmp}/ember-perf-gate.XXXXXX")"
BIN="$WORK/bin"
STUBS="$WORK/stubs"
TOKEN="perf-gate-$(od -An -N12 -tx1 /dev/urandom | tr -d ' \n')"
DAEMONS=""
LOADERS=""
FAILED=0

stop_loaders() {
  local pid
  touch "$WORK/stop-load"
  for pid in $LOADERS; do
    pkill -P "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  done
  LOADERS=""
}

finish() {
  local rc=$? pid
  trap - EXIT
  stop_loaders
  for pid in $DAEMONS; do
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  done
  if [ "$rc" -eq 0 ] && [ "$FAILED" -eq 0 ]; then
    rm -rf "$WORK"
  else
    echo "logs kept: $WORK" >&2
  fi
  exit "$rc"
}
trap finish EXIT
trap 'exit 130' INT TERM

budget() { awk -v k="$1" '$1 == k { print $2 }' <<<"$BUDGETS"; }
measured() { awk -v k="$1" '$1 == k { print $3 }' <<<"$BUDGETS"; }
what() { awk -v k="$1" '$1 == k { $1 = $2 = $3 = ""; sub(/^ +/, ""); print }' <<<"$BUDGETS"; }

check() {
  local name="$1" got="$2" limit="${3:-$(budget "$1")}"
  if awk -v g="$got" -v b="$limit" 'BEGIN { exit !(g + 0 > b + 0) }'; then
    printf 'FAIL %-30s %10s  budget %-8s (%s; main measured %s, Mac/Linux)\n' "$name" "$got" "$limit" "$(what "$name")" "$(measured "$name")"
    FAILED=1
  else
    printf 'ok   %-30s %10s  budget %-8s (%s)\n' "$name" "$got" "$limit" "$(what "$name")"
  fi
}

cpu_seconds() {
  if [ -r "/proc/$1/stat" ]; then
    awk -v hz="$(getconf CLK_TCK)" '{ sub(/^.*\) /, ""); printf "%.2f\n", ($12 + $13) / hz }' "/proc/$1/stat"
  else
    ps -o time= -p "$1" | awk -F: '{ s = 0; for (i = 1; i <= NF; i++) s = s * 60 + $i; printf "%.2f\n", s }'
  fi
}

rss_kib() { ps -o rss= -p "$1" | awk '{ print $1 + 0 }'; }

metric() { curl -s --max-time 5 "http://127.0.0.1:$1/metrics" | awk -v n="$2" '$1 == n { print $2 }'; }

minus() { awk -v a="$1" -v b="$2" 'BEGIN { printf "%.2f\n", a - b }'; }

min_of() { tr ' ' '\n' <<<"$1" | awk 'NF { if (m == "" || $1 + 0 < m + 0) m = $1 } END { print m + 0 }'; }

alive_or_die() {
  kill -0 "$2" 2>/dev/null && return 0
  echo "error: $1 exited early; its logs:" >&2
  find "$3" -type f -name '*.log' -exec tail -n 20 {} + >&2 || true
  exit 1
}

SERVER_PORT=""
SERVER_PID=""
start_server() {
  local dir="$WORK/$1" port pid
  mkdir -p "$dir/home" "$dir/data"
  for _ in 1 2 3 4 5; do
    port=$((20000 + RANDOM % 30000))
    if curl -s -o /dev/null --max-time 1 "http://127.0.0.1:$port/"; then continue; fi
    printf '{"http":{"addr":"127.0.0.1:%s"}}\n' "$port" >"$dir/config.json"
    env -i PATH=/usr/bin:/bin HOME="$dir/home" EMBER_DATA_DIR="$dir/data" EMBER_TOKEN="$TOKEN" \
      EMBER_MDNS_ADVERTISE=0 EMBER_CLOCK=off \
      "$BIN/ember" -config "$dir/config.json" >"$dir/server.log" 2>&1 &
    pid=$!
    for _ in $(seq 1 50); do
      kill -0 "$pid" 2>/dev/null || break
      if curl -s -o /dev/null --max-time 1 "http://127.0.0.1:$port/healthz"; then
        DAEMONS="$DAEMONS $pid"
        SERVER_PORT="$port"
        SERVER_PID="$pid"
        return 0
      fi
      sleep 0.2
    done
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  done
  echo "error: the scratch server $1 did not start; its log:" >&2
  tail -20 "$dir/server.log" >&2 || true
  exit 1
}

KINDS=(claude codex t3)
EARLY_PIDS=()
LATE_PIDS=()
PRODUCER_CPU=()
start_producer() {
  local kind="$1" port="$3" home="$WORK/$1-$2-home"
  mkdir -p "$home/.config/ember"
  (umask 077; printf 'EMBER_SOURCE=perfgate\nEMBER_SERVER_URL=http://127.0.0.1:%s\nEMBER_TOKEN=%s\n' "$port" "$TOKEN" >"$home/.config/ember/producer.env")
  env -i PATH="$STUBS:/usr/bin:/bin" HOME="$home" "$BIN/ember-$kind-producer" run >"$home/stdout.log" 2>&1 &
  STARTED_PID=$!
  DAEMONS="$DAEMONS $!"
}

quit_and_wait() {
  kill -QUIT "$1"
  wait "$1" 2>/dev/null || true
}

count_goroutines() {
  find "$1" -type f -name '*.log' -exec cat {} + | awk '
    function close_one() { if (g && !gc) n++; g = 0; gc = 0 }
    /^goroutine [1-9][0-9]* / { close_one(); g = 1; next }
    /^$/ { close_one(); next }
    /runtime\.gcBgMarkWorker/ { gc = 1 }
    END { close_one(); print n + 0 }'
}

post_status_loop() {
  local url="$1" n=0 i state
  while [ ! -e "$WORK/stop-load" ]; do
    n=$((n + 1))
    for i in 1 2 3 4 5; do
      if [ $(((n + i) % 2)) -eq 0 ]; then state=running; else state=waiting; fi
      curl -s -o /dev/null --max-time 5 -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
        -d "{\"source\":\"perfgate\",\"tool\":\"claude\",\"session\":\"gate-$i\",\"state\":\"$state\"}" "$url/v1/status" || true
    done
    sleep 2
  done
}

long_poll_loop() {
  local url="$1" bearer="$2" hdr="$WORK/longpoll.$$.$RANDOM" etag='"none"'
  while [ ! -e "$WORK/stop-load" ]; do
    if curl -s -o /dev/null -D "$hdr" --max-time 35 -H "Authorization: Bearer $bearer" -H "If-None-Match: $etag" "$url?wait=25"; then
      etag="$(awk 'tolower($1) == "etag:" { print $2 }' "$hdr" | tr -d '\r')"
      [ -n "$etag" ] || etag='"none"'
    else
      sleep 1
    fi
  done
}

echo "perf gate: building into $BIN"
mkdir -p "$BIN" "$STUBS"
go build -o "$BIN/" ./cmd/ember ./cmd/ember-claude-producer ./cmd/ember-codex-producer ./cmd/ember-t3-producer
for stub in security claude; do
  printf '#!/bin/sh\nexit 1\n' >"$STUBS/$stub"
  chmod +x "$STUBS/$stub"
done

GO_CHECKS='
rediscovery|./cmd/ember|^TestRediscoverClock_BrowsesADayAtTheBackedOffRate$|1
mdns_lookups|./internal/discovery|^TestBrowseSocketsLeavesNoGoroutinesWhileAnswersArrive$|20
t3_poll_scaling|./cmd/ember-t3-producer|^TestScopedPollCostDoesNotGrowWithSettledThreads$|1
t3_change_gate|./cmd/ember-t3-producer|^TestStoreReaderSkipsAnUnchangedDatabaseAndSeesEveryCommit$|1
'
(
  while IFS='|' read -r name pkg run count; do
    [ -n "$name" ] || continue
    if go test -count="$count" -run "$run" "$pkg" >"$WORK/go-$name.log" 2>&1; then
      echo ok >"$WORK/go-$name.result"
    else
      echo fail >"$WORK/go-$name.result"
    fi
  done <<<"$GO_CHECKS"
) &
GO_TESTS_PID=$!

echo "perf gate: idle server and producers, ${IDLE_SECONDS}s"
start_server idle
IDLE_PORT="$SERVER_PORT"
IDLE_PID="$SERVER_PID"
start_server producers
PRODUCER_PORT="$SERVER_PORT"
for i in 0 1 2; do
  start_producer "${KINDS[i]}" early "$PRODUCER_PORT"
  EARLY_PIDS[i]="$STARTED_PID"
done
sleep "$WARMUP_SECONDS"
idle_cpu0="$(metric "$IDLE_PORT" ember_process_cpu_seconds_total)"
idle_g0="$(metric "$IDLE_PORT" ember_goroutines)"
for i in 0 1 2; do
  alive_or_die "the ${KINDS[i]} producer" "${EARLY_PIDS[i]}" "$WORK/${KINDS[i]}-early-home"
  PRODUCER_CPU[i]="$(cpu_seconds "${EARLY_PIDS[i]}")"
done
sleep $((IDLE_SECONDS / 2 - WARMUP_SECONDS))
for i in 0 1 2; do
  start_producer "${KINDS[i]}" late "$PRODUCER_PORT"
  LATE_PIDS[i]="$STARTED_PID"
done
sleep $((IDLE_SECONDS / 2 + WARMUP_SECONDS))
alive_or_die "the idle server" "$IDLE_PID" "$WORK/idle"
idle_cpu1="$(metric "$IDLE_PORT" ember_process_cpu_seconds_total)"
idle_g1="$(metric "$IDLE_PORT" ember_goroutines)"
PRODUCER_GROWTH=()
for i in 0 1 2; do
  alive_or_die "the ${KINDS[i]} producer" "${EARLY_PIDS[i]}" "$WORK/${KINDS[i]}-early-home"
  alive_or_die "the late ${KINDS[i]} producer" "${LATE_PIDS[i]}" "$WORK/${KINDS[i]}-late-home"
  PRODUCER_CPU[i]="$(minus "$(cpu_seconds "${EARLY_PIDS[i]}")" "${PRODUCER_CPU[i]}")"
  quit_and_wait "${EARLY_PIDS[i]}"
  quit_and_wait "${LATE_PIDS[i]}"
  early="$(count_goroutines "$WORK/${KINDS[i]}-early-home")"
  late="$(count_goroutines "$WORK/${KINDS[i]}-late-home")"
  if [ "$early" -eq 0 ] || [ "$late" -eq 0 ]; then
    echo "error: no goroutine dump from the ${KINDS[i]} producer" >&2
    exit 1
  fi
  PRODUCER_GROWTH[i]=$((early - late))
done

echo "perf gate: server under load, ${LOAD_SECONDS}s"
url="http://127.0.0.1:$IDLE_PORT"
knob="$(curl -s --max-time 5 -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"kind":"cinder-knob","hw_id":"0000000a0881","name":"perf-gate"}' "$url/v1/devices" |
  jq -r '.token // empty')" || true
[ -n "$knob" ] || { echo "error: could not mint a scratch knob token" >&2; exit 1; }
rm -f "$WORK/stop-load"
post_status_loop "$url" 2>/dev/null &
LOADERS="$LOADERS $!"
long_poll_loop "$url/state" "$TOKEN" 2>/dev/null &
LOADERS="$LOADERS $!"
long_poll_loop "$url/v1/devices/self/view" "$knob" 2>/dev/null &
LOADERS="$LOADERS $!"
sleep "$WARMUP_SECONDS"
load_cpu0="$(metric "$IDLE_PORT" ember_process_cpu_seconds_total)"
samples=$((LOAD_SECONDS / SAMPLE_SECONDS))
half=$((samples / 2))
mid_g="" mid_rss="" end_g="" end_rss=""
for i in $(seq 1 "$samples"); do
  sleep "$SAMPLE_SECONDS"
  alive_or_die "the loaded server" "$IDLE_PID" "$WORK/idle"
  g="$(metric "$IDLE_PORT" ember_goroutines)"
  r="$(rss_kib "$IDLE_PID")"
  if [ "$i" -gt $((half - 3)) ] && [ "$i" -le "$half" ]; then
    mid_g="$mid_g $g"; mid_rss="$mid_rss $r"
  fi
  if [ "$i" -gt $((samples - 3)) ]; then
    end_g="$end_g $g"; end_rss="$end_rss $r"
  fi
done
load_cpu1="$(metric "$IDLE_PORT" ember_process_cpu_seconds_total)"
stop_loaders

wait "$GO_TESTS_PID" || true

echo
echo "perf gate results (budgets: scripts/perf-gate.sh BUDGETS)"
check server_idle_cpu "$(minus "$idle_cpu1" "$idle_cpu0")"
check server_idle_goroutine_growth "$((idle_g1 - idle_g0))"
check server_load_cpu "$(awk -v a="$load_cpu1" -v b="$load_cpu0" -v m="$LOAD_MINUTES" 'BEGIN { printf "%.2f\n", (a - b) / m }')"
check server_load_goroutine_growth "$(($(min_of "$end_g") - $(min_of "$mid_g")))"
check server_load_rss_growth_kib "$(($(min_of "$end_rss") - $(min_of "$mid_rss")))"
for i in 0 1 2; do
  check "${KINDS[i]}_idle_cpu" "${PRODUCER_CPU[i]}"
  check "${KINDS[i]}_goroutine_growth" "${PRODUCER_GROWTH[i]}"
done
while IFS='|' read -r name pkg run count; do
  [ -n "$name" ] || continue
  if [ "$(cat "$WORK/go-$name.result" 2>/dev/null)" = ok ]; then
    printf 'ok   %-30s go test -run %s -count=%s %s\n' "$name" "$run" "$count" "$pkg"
  else
    printf 'FAIL %-30s go test -run %s -count=%s %s\n' "$name" "$run" "$count" "$pkg"
    tail -25 "$WORK/go-$name.log" | sed 's/^/     /'
    FAILED=1
  fi
done <<<"$GO_CHECKS"

if [ "$FAILED" -ne 0 ]; then
  echo "perf gate: FAILED" >&2
  exit 1
fi
echo "perf gate: passed"
