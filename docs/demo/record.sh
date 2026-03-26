#!/usr/bin/env bash
#
# Record wt demo GIFs using shellwright (MCP over HTTP).
# Usage: ./record.sh [gif-name]
#   gif-name: quickstart | multi-repo | hooks | interactive | all (default: all)
#
# Prerequisites:
#   - shellwright running: npx -y @dwmkerr/shellwright --http --cols 90 --rows 18 --font-size 20
#   - Docker image built: docker build -t wt-demo .
#
set -euo pipefail

SHELLWRIGHT_URL="${SHELLWRIGHT_URL:-http://localhost:7498}"
OUTPUT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
MCP_SESSION=""
SHELL_SESSION=""
ID_COUNTER=1

# ── MCP helpers ──────────────────────────────────────────────────────────

next_id() { ID_COUNTER=$((ID_COUNTER + 1)); echo $ID_COUNTER; }

mcp_call() {
  local name="$1" args="$2"
  local id; id=$(next_id)
  curl -s -X POST "$SHELLWRIGHT_URL/mcp" \
    -H "Content-Type: application/json" \
    -H "Accept: application/json, text/event-stream" \
    -H "Mcp-Session-Id: $MCP_SESSION" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":$id,\"method\":\"tools/call\",\"params\":{\"name\":\"$name\",\"arguments\":$args}}"
}

mcp_init() {
  MCP_SESSION=$(curl -s -D - -X POST "$SHELLWRIGHT_URL/mcp" \
    -H "Content-Type: application/json" \
    -H "Accept: application/json, text/event-stream" \
    -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"recorder","version":"0.1"}}}' \
    2>&1 | grep -i "mcp-session-id" | awk '{print $2}' | tr -d '\r')

  curl -s -X POST "$SHELLWRIGHT_URL/mcp" \
    -H "Content-Type: application/json" \
    -H "Accept: application/json, text/event-stream" \
    -H "Mcp-Session-Id: $MCP_SESSION" \
    -d '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' > /dev/null 2>&1

  echo "MCP session: $MCP_SESSION"
}

# ── Shell helpers ────────────────────────────────────────────────────────

shell_start() {
  local docker_args=("$@")
  local result
  result=$(mcp_call "shell_start" "{\"command\":\"docker\",\"args\":[\"run\",\"--rm\",\"-it\",${docker_args[*]}],\"cols\":90,\"rows\":18}")
  SHELL_SESSION=$(echo "$result" | grep -o '"shell_session_id":"[^"]*"' | cut -d'"' -f4)
  echo "Shell session: $SHELL_SESSION"
  sleep 2
}

shell_stop() {
  mcp_call "shell_stop" "{\"session_id\":\"$SHELL_SESSION\"}" > /dev/null 2>&1
}

send() {
  mcp_call "shell_send" "{\"session_id\":\"$SHELL_SESSION\",\"input\":\"$1\"}" > /dev/null 2>&1
}

type_cmd() {
  local text="$1"
  for (( i=0; i<${#text}; i++ )); do
    local char="${text:$i:1}"
    case "$char" in
      '"') char='\\"' ;;
      '\\') char='\\\\' ;;
    esac
    send "$char"
    sleep 0.10
  done
}

enter() { send "\\r"; }

type_and_run() {
  type_cmd "$1"
  sleep 0.2
  enter
}

pause() { sleep "${1:-1.5}"; }

setup_prompt() {
  send "export PS1='$1' && clear\\r"
  sleep 1
}

record_start() {
  mcp_call "shell_record_start" "{\"session_id\":\"$SHELL_SESSION\",\"fps\":10}" > /dev/null 2>&1
  sleep 0.5
}

record_stop() {
  local name="$1"
  local result
  result=$(mcp_call "shell_record_stop" "{\"session_id\":\"$SHELL_SESSION\",\"name\":\"$name\"}")
  local url
  url=$(echo "$result" | grep -o '"download_url":"[^"]*"' | cut -d'"' -f4)
  echo "Downloading $name.gif..."
  curl -s -o "$OUTPUT_DIR/$name.gif" "$url"
  echo "Saved to $OUTPUT_DIR/$name.gif ($(du -h "$OUTPUT_DIR/$name.gif" | cut -f1))"
}

# ── GIF recordings ───────────────────────────────────────────────────────

record_quickstart() {
  echo "=== Recording: quickstart ==="
  shell_start "\"wt-demo\",\"bash\",\"--norc\",\"--noprofile\""
  setup_prompt '~/main-app \$ '
  send "cd src/main-app\\r"
  sleep 0.5
  send "export PS1='~/main-app \$ ' && clear\\r"
  sleep 1

  record_start

  # Create a feature worktree
  type_and_run "wt co feat/auth"
  pause 2

  # Create another
  type_and_run "wt co feat/dashboard"
  pause 2

  # List all worktrees
  type_and_run "wt list"
  pause 2.5

  # Switch back to feat/auth
  type_and_run "wt co feat/auth"
  pause 2

  # Remove a worktree
  type_and_run "wt remove feat/dashboard"
  pause 2

  # Final list
  type_and_run "wt list"
  pause 2

  record_stop "wt-quickstart"
  shell_stop
}

record_multi_repo() {
  echo "=== Recording: multi-repo ==="
  # Use custom pattern that groups by branch
  shell_start "\"-e\",\"WORKTREE_ROOT=/home/demo/worktrees\",\"wt-demo\",\"bash\",\"--norc\",\"--noprofile\""

  # Set up config
  send "cat > ~/.config/wt/config.toml << 'EOF'\nstrategy = \\\"custom\\\"\npattern = \\\"{.worktreeRoot}/{.branch}/{.repo.Name}\\\"\nEOF\n"
  sleep 0.5
  send "export PS1='~ \$ ' && clear\\r"
  sleep 1

  record_start

  # Show the config
  type_and_run "cat ~/.config/wt/config.toml"
  pause 2

  # Create worktree in shared-lib
  type_and_run "cd ~/src/shared-lib && wt co feat/auth"
  pause 2

  # Create worktree in main-app
  type_and_run "cd ~/src/main-app && wt co feat/auth"
  pause 2

  # Show the grouped directory structure
  type_and_run "tree ~/worktrees"
  pause 3

  record_stop "wt-multi-repo"
  shell_stop
}

record_hooks() {
  echo "=== Recording: hooks ==="
  shell_start "\"-e\",\"WORKTREE_ROOT=/home/demo/worktrees\",\"wt-demo\",\"bash\",\"--norc\",\"--noprofile\""

  # Write config with hooks
  send "cat > ~/.config/wt/config.toml << 'EOF'\n[hooks]\npost_create = [\\\"test -f \\\\\\\$WT_MAIN/.env && cp \\\\\\\$WT_MAIN/.env \\\\\\\$WT_PATH/.env || true\\\"]\npost_checkout = [\\\"test -f \\\\\\\$WT_MAIN/.env && cp \\\\\\\$WT_MAIN/.env \\\\\\\$WT_PATH/.env || true\\\"]\nEOF\n"
  sleep 0.5
  send "cd ~/src/main-app && export PS1='~/main-app \$ ' && clear\\r"
  sleep 1

  record_start

  # Show the config
  type_and_run "cat ~/.config/wt/config.toml"
  pause 2

  # Show .env exists in main checkout
  type_and_run "cat .env"
  pause 2

  # Create worktree — hook should copy .env
  type_and_run "wt co fix/login-bug"
  pause 2

  # Verify .env was copied by the hook
  type_and_run "cat .env"
  pause 2.5

  record_stop "wt-hooks"
  shell_stop
}

record_interactive() {
  echo "=== Recording: interactive ==="
  shell_start "\"-e\",\"WORKTREE_ROOT=/home/demo/worktrees\",\"wt-demo\",\"bash\",\"--norc\",\"--noprofile\""
  send "cd ~/src/main-app && export PS1='~/main-app \$ ' && clear\\r"
  sleep 1

  record_start

  # Run wt co without args — should show interactive picker
  type_cmd "wt co"
  sleep 0.3
  enter
  pause 2

  # Arrow down twice, then select
  send "\\x1b[B"  # arrow down
  sleep 0.5
  send "\\x1b[B"  # arrow down
  sleep 0.5
  enter  # select
  pause 2

  # Show where we are
  type_and_run "wt list"
  pause 2

  record_stop "wt-interactive"
  shell_stop
}

record_strategies() {
  echo "=== Recording: strategies ==="
  shell_start "\"-e\",\"WORKTREE_ROOT=/home/demo/worktrees\",\"wt-demo\",\"bash\",\"--norc\",\"--noprofile\""
  send "cd ~/src/main-app && export PS1='~/main-app \$ ' && clear\\r"
  sleep 1

  record_start

  # Show current config
  type_and_run "wt info"
  pause 3

  record_stop "wt-strategies"
  shell_stop
}

# ── Main ─────────────────────────────────────────────────────────────────

mcp_init

target="${1:-all}"
case "$target" in
  quickstart)   record_quickstart ;;
  multi-repo)   record_multi_repo ;;
  hooks)        record_hooks ;;
  interactive)  record_interactive ;;
  strategies)   record_strategies ;;
  all)
    record_quickstart
    record_multi_repo
    record_hooks
    record_interactive
    ;;
  *) echo "Unknown target: $target"; exit 1 ;;
esac

echo "=== Done ==="
