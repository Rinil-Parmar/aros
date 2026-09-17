#!/usr/bin/env bash
# End-to-end test of the CLI flow (init → plan → divide → work → status) using
# mock `claude` / `opencode` binaries, so no API calls are made.
#
# Scenarios covered:
#   1. Happy path: both tasks complete, phase → done.
#   2. Blocked path: task-002 fails once → phase stays "work", status shows the
#      block reason, a second `aros work` retries it and completes.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/aros-e2e-mock.XXXXXX")"
MOCK_BIN="$WORK_DIR/mockbin"
BIN_PATH="$WORK_DIR/aros"
PROJECT_DIR="$WORK_DIR/project"
TASK_PROMPT="${1:-build a tiny feature}"
export AROS_MOCK_FAIL_FLAG="$WORK_DIR/fail-once"

cleanup() {
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

mkdir -p "$MOCK_BIN" "$PROJECT_DIR"

# Mock claude: prompt arrives on stdin (aros passes it there to avoid argv limits),
# but also accept a positional argument for robustness.
cat > "$MOCK_BIN/claude" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

prompt=""
skip_next=0
for arg in "$@"; do
  if [[ $skip_next -eq 1 ]]; then skip_next=0; continue; fi
  case "$arg" in
    --output-format|--model|--tools) skip_next=1 ;;
    -p|--print|--dangerously-skip-permissions) ;;
    -*) ;;
    *) prompt="$arg" ;;
  esac
done
if [[ -z "$prompt" && ! -t 0 ]]; then
  prompt="$(cat)"
fi

if [[ "$prompt" == *"Break the following approved plan into concrete, assignable tasks."* ]] || [[ "$prompt" == *"Break this plan into concrete tasks."* ]]; then
  cat <<'JSON'
{"type":"result","result":"Here are the tasks:\n```json\n[{\"id\":\"task-001\",\"title\":\"Create core package\",\"description\":\"Implement a small core package and expose one function.\",\"assigned_to\":\"Claude\",\"dependencies\":[]},{\"id\":\"task-002\",\"title\":\"Add tests\",\"description\":\"Write tests for the core function and ensure coverage.\",\"assigned_to\":\"gemini\",\"dependencies\":[\"task-001\"]}]\n```","is_error":false}
JSON
elif [[ "$prompt" == *"You are working on task [task-001]"* ]]; then
  cat <<'JSON'
{"type":"result","result":"Implemented task-001 successfully.","is_error":false}
JSON
elif [[ "$prompt" == *"You are working on task [task-002]"* ]]; then
  if [[ -n "${AROS_MOCK_FAIL_FLAG:-}" && -f "$AROS_MOCK_FAIL_FLAG" ]]; then
    rm -f "$AROS_MOCK_FAIL_FLAG"
    cat <<'JSON'
{"type":"result","subtype":"error_during_execution","result":"Simulated provider outage","is_error":true}
JSON
    exit 0
  fi
  if [[ "$prompt" != *"Implemented task-001 successfully."* ]]; then
    echo "task-002 prompt is missing task-001 output" >&2
    exit 3
  fi
  cat <<'JSON'
{"type":"result","result":"Implemented task-002 successfully.","is_error":false}
JSON
elif [[ "$prompt" == *"Synthesize"* ]] || [[ "$prompt" == *"synthesizing"* ]] || [[ "$prompt" == *"evaluating multiple AI-generated plans"* ]]; then
  cat <<'JSON'
{"type":"result","result":"Approved implementation plan: 1) create core package, 2) add tests, 3) verify outputs.","is_error":false}
JSON
else
  cat <<'JSON'
{"type":"result","result":"Agent plan: create package, implement feature, add tests.","is_error":false}
JSON
fi
EOF
chmod +x "$MOCK_BIN/claude"

# Mock opencode: real NDJSON shape ({"type":"text","part":{...}}).
cat > "$MOCK_BIN/opencode" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' '{"type":"step_start"}'
printf '%s\n' '{"type":"text","part":{"type":"text","text":"OpenCode mock plan: implement, then test."}}'
printf '%s\n' '{"type":"step_finish"}'
EOF
chmod +x "$MOCK_BIN/opencode"

echo "Building aros binary..."
(cd "$ROOT_DIR" && go build -o "$BIN_PATH" .)

cd "$PROJECT_DIR"
export PATH="$MOCK_BIN:$PATH"

echo "Initializing project..."
"$BIN_PATH" init "e2e-mock-project"

cat > .aros/config.toml <<'EOF'
[judge]
agent = "claude"

[agents.claude]
enabled = true
model = "mock-claude"
strengths = ["architecture", "planning", "implementation", "testing"]

[agents.opencode]
enabled = true
model = "mock/opencode"
strengths = ["implementation"]

[agents.copilot]
enabled = false

[secondmem]
enabled = false

[work]
max_concurrent = 2
agent_timeout_seconds = 60
EOF

echo "Running plan phase (first answer n + feedback, then y)..."
printf 'n\nmake it shorter\ny\n' | "$BIN_PATH" plan "$TASK_PROMPT"

echo "Running divide phase..."
printf 'y\n' | "$BIN_PATH" divide | tee divide.log
grep -q "reassigned to claude: task-002" divide.log || { echo "expected unknown agent 'gemini' to be reassigned"; exit 1; }

echo "Running work phase with a simulated failure on task-002..."
touch "$AROS_MOCK_FAIL_FLAG"
"$BIN_PATH" work | tee work1.log
grep -q "blocked" work1.log || { echo "expected a blocked task on first run"; exit 1; }

echo "Checking status shows the block reason..."
"$BIN_PATH" status | tee status1.log
grep -q "Simulated provider outage" status1.log || { echo "block reason missing from status"; exit 1; }
grep -q "Phase:    work" status1.log || { echo "phase should remain 'work' while tasks are blocked"; exit 1; }

echo "Running work phase again (retries blocked task)..."
"$BIN_PATH" work | tee work2.log
grep -q "All tasks complete" work2.log || { echo "expected completion on retry"; exit 1; }

echo "Checking final status..."
"$BIN_PATH" status

python3 - <<'PY'
import json
from pathlib import Path

active = json.loads(Path('.aros/active-session.json').read_text())['active_session_id']
base = Path('.aros') / 'sessions' / active
state = json.loads((base / 'state.json').read_text())
manifest = json.loads((base / 'manifest.json').read_text())

assert state['phase'] == 'done', f"expected phase done, got {state['phase']}"
assert state['task'], 'expected non-empty task in state.json'

tasks = manifest.get('tasks', [])
assert len(tasks) == 2, f"expected 2 tasks, got {len(tasks)}"
for t in tasks:
    assert t['status'] == 'done', f"task {t['id']} not done: {t['status']}"
    assert t['output'], f"task {t['id']} missing output"
    assert t['assigned_to'] == 'claude', f"task {t['id']} assigned_to={t['assigned_to']}"
    assert not t.get('block_reason'), f"task {t['id']} still has block_reason"

print('E2E assertions passed.')
PY

echo
echo "Mock E2E completed successfully."
