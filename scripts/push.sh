#!/usr/bin/env bash
# Stage everything, commit with an automatically written message, and push.
#
#   ./scripts/push.sh                  write the message automatically
#   ./scripts/push.sh "some message"   use the given message instead
#   ./scripts/push.sh --dry-run        stage and write the message, but do not commit or push
#
# Works with bash 3.2 (macOS default). The guard can be tested by sourcing this file:
#   bash -c 'source scripts/push.sh; printf "%s\n" "msg" | guard_message'
set -euo pipefail

MAX_FILE_BYTES=$((50 * 1024 * 1024))
DIFF_LIMIT_BYTES=60000
CLAUDE_TIMEOUT_SECONDS=60
BLOCK_PATTERN='claude|anthropic|co-authored-by|generated with|noreply@|🤖'

COMMIT_PROMPT='Write a git commit message for this diff. First line is an imperative summary of at most 72 characters with no trailing full stop. Then a blank line and up to three short bullet points saying what changed and why. Plain text only. Do not mention AI, assistants, Claude, Anthropic or any tool that wrote the code. No Co-Authored-By, no "Generated with", no emojis, no URLs, no sign-off. Output the message only.'

WORK_DIR=""

cleanup() {
  if [[ -n "$WORK_DIR" && -d "$WORK_DIR" ]]; then
    rm -rf "$WORK_DIR"
  fi
}

die() {
  printf 'push.sh: %s\n' "$*" >&2
  exit 1
}

# Reads a message on stdin and writes it to stdout with every blocked line removed
# and trailing blank lines trimmed. Returns 1 if the first line is empty afterwards,
# so the caller knows to use the fallback message.
guard_message() {
  local filtered
  filtered="$(grep -viE "$BLOCK_PATTERN" || true)"
  # $(...) already drops trailing newlines; also drop trailing whitespace-only lines.
  filtered="$(printf '%s\n' "$filtered" | awk '
    { lines[NR] = $0; if ($0 ~ /[^[:space:]]/) last = NR }
    END { for (i = 1; i <= last; i++) print lines[i] }')"
  printf '%s\n' "$filtered"
  local first_line
  first_line="$(printf '%s\n' "$filtered" | head -n 1)"
  [[ "$first_line" =~ [^[:space:]] ]]
}

# Prints the lines of stdin that match a blocked pattern. Exit 0 if any matched.
find_blocked_lines() {
  grep -inE "$BLOCK_PATTERN"
}

# "Update <folders>" plus a bullet per top-level folder with its file count.
# A folder whose name is itself blocked (such as .claude) is described as "agent config".
fallback_message() {
  local counts summary
  counts="$(git diff --cached --name-only | awk -v pat="$BLOCK_PATTERN" '
    {
      if (index($0, "/") > 0) { top = substr($0, 1, index($0, "/") - 1) } else { top = "root files" }
      if (tolower(top) ~ pat) { top = "agent config" }
      n[top]++
    }
    END { for (t in n) printf "%s\t%d\n", t, n[t] }' | sort)"
  summary="Update $(printf '%s\n' "$counts" | cut -f1 | paste -sd, - | sed 's/,/, /g')"
  if (( ${#summary} > 72 )); then
    summary="${summary:0:69}..."
  fi
  printf '%s\n\n' "$summary"
  printf '%s\n' "$counts" | awk -F'\t' '{ printf "- %s: %d file%s\n", $1, $2, ($2 == 1 ? "" : "s") }'
}

# Runs a command with a time limit, using whichever tool this machine has.
run_with_timeout() {
  local seconds="$1"
  shift
  if command -v timeout >/dev/null 2>&1; then
    timeout "$seconds" "$@"
  elif command -v gtimeout >/dev/null 2>&1; then
    gtimeout "$seconds" "$@"
  elif command -v perl >/dev/null 2>&1; then
    perl -e 'alarm shift; exec @ARGV or die "exec failed: $!"' "$seconds" "$@"
  else
    return 127
  fi
}

# Writes a message for the staged diff using the Claude Code CLI in print mode,
# with no tools, no MCP servers and no saved session. Returns non-zero on any failure.
ai_message() {
  local input="$1" output="$2"
  command -v claude >/dev/null 2>&1 || return 1
  # Run from the empty work dir so the CLI picks up no project files.
  (
    cd "$WORK_DIR/empty"
    run_with_timeout "$CLAUDE_TIMEOUT_SECONDS" \
      claude -p --tools "" --strict-mcp-config --no-session-persistence \
      --output-format text "$COMMIT_PROMPT"
  ) <"$input" >"$output" 2>"$WORK_DIR/claude.err" || return 1
  grep -q '[^[:space:]]' "$output"
}

# Reads KEY=value from .env without printing it. Strips "export", whitespace and quotes.
env_value() {
  local key="$1"
  awk -v k="$key" '
    {
      sub(/^[[:space:]]*export[[:space:]]+/, "")
      if (index($0, k "=") == 1) {
        v = substr($0, length(k) + 2)
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", v)
        if (v ~ /^".*"$/ || v ~ /^\047.*\047$/) v = substr(v, 2, length(v) - 2)
        val = v
      }
    }
    END { printf "%s", val }' .env
}

unstage() {
  if git rev-parse --verify -q HEAD >/dev/null; then
    git reset -q HEAD -- "$@"
  else
    git rm -q --cached -- "$@"
  fi
}

usage() {
  sed -n '2,6p' "$0" | sed 's/^# \{0,1\}//'
}

main() {
  local dry_run=0 user_message="" have_message=0
  while (( $# > 0 )); do
    case "$1" in
      --dry-run) dry_run=1 ;;
      -h | --help) usage; exit 0 ;;
      --) shift; [[ $# -gt 0 ]] && { user_message="$1"; have_message=1; } ;;
      -*) die "unknown option: $1" ;;
      *)
        (( have_message == 0 )) || die "give the message as one quoted argument"
        user_message="$1"
        have_message=1
        ;;
    esac
    (( $# > 0 )) && shift
  done

  # Preconditions.
  local root
  root="$(git rev-parse --show-toplevel 2>/dev/null)" || die "not inside a git repository."
  cd "$root"

  if ! git remote get-url origin >/dev/null 2>&1; then
    local name
    name="$(basename "$root")"
    {
      echo "push.sh: no remote named origin. Create and connect a public GitHub repo with:"
      echo "  gh repo create $name --public"
      echo "  git remote add origin https://github.com/<your-github-user>/$name.git"
    } >&2
    exit 1
  fi

  WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/push-sh.XXXXXX")"
  trap cleanup EXIT
  mkdir "$WORK_DIR/empty"

  git add -A

  if git diff --cached --quiet; then
    echo "Nothing to commit."
    exit 0
  fi

  # Safety checks on every staged file that is added or changed.
  local blocked="$WORK_DIR/blocked" path base size
  : >"$blocked"
  while IFS= read -r path; do
    base="$(basename "$path")"
    if [[ ("$base" == ".env" || "$base" == .env.*) && "$base" != ".env.example" ]]; then
      printf '%s\t(.env file)\n' "$path" >>"$blocked"
      continue
    fi
    case "$base" in
      *.db | *.db-journal | *.db-wal | *.db-shm | *.sqlite | *.sqlite3)
        printf '%s\t(database file)\n' "$path" >>"$blocked"
        continue
        ;;
    esac
    size="$(git cat-file -s ":$path" 2>/dev/null || echo 0)"
    if (( size > MAX_FILE_BYTES )); then
      printf '%s\t(%d MB, over the 50 MB limit)\n' "$path" $((size / 1024 / 1024)) >>"$blocked"
    fi
  done < <(git diff --cached --name-only --diff-filter=ACMRT)

  if [[ -s "$blocked" ]]; then
    {
      echo "push.sh: refusing to continue. These staged files must not be committed:"
      awk -F'\t' '{ print "  " $1 "  " $2 }' "$blocked"
      echo "They have been unstaged. Add them to .gitignore, then run this again."
    } >&2
    local -a paths=()
    while IFS= read -r path; do paths+=("$path"); done < <(cut -f1 "$blocked")
    unstage "${paths[@]}"
    exit 1
  fi

  local full_diff="$WORK_DIR/full.diff"
  git diff --cached >"$full_diff"

  # Credentials from .env must not appear anywhere in the staged diff.
  if [[ -f .env ]]; then
    local key value leaked=""
    for key in PAYPAL_CLIENT_SECRET PAYPAL_CLIENT_ID; do
      value="$(env_value "$key")"
      [[ -n "$value" ]] || continue
      # Pass the value through a file, not argv, so it never shows in the process list.
      printf '%s\n' "$value" >"$WORK_DIR/needle"
      if grep -qF -f "$WORK_DIR/needle" "$full_diff"; then
        leaked="$leaked $key"
      fi
      rm -f "$WORK_DIR/needle"
    done
    if [[ -n "$leaked" ]]; then
      {
        echo "push.sh: refusing to continue. The value of$leaked from .env appears in the staged diff."
        echo "Find it with: git diff --cached | grep -n <the value>   (the value is not printed here)"
        echo "Remove it from the file, then run this again."
      } >&2
      exit 1
    fi
  fi

  # Write the message.
  local message fallback
  fallback="$(fallback_message)"
  if (( have_message == 1 )); then
    message="$user_message"
  else
    local input="$WORK_DIR/input" ai_out="$WORK_DIR/ai.txt"
    {
      echo "Diff stat:"
      git diff --cached --stat
      echo
      echo "Diff (may be truncated):"
      head -c "$DIFF_LIMIT_BYTES" "$full_diff"
    } >"$input"
    if ai_message "$input" "$ai_out"; then
      message="$(cat "$ai_out")"
    else
      echo "push.sh: could not write the message with the claude CLI; using the fallback." >&2
      message="$fallback"
    fi
  fi

  if ! message="$(printf '%s\n' "$message" | guard_message)"; then
    message="$(printf '%s\n' "$fallback" | guard_message)" || die "could not build a commit message."
  fi

  echo "Commit message:"
  echo "----------------------------------------"
  printf '%s\n' "$message"
  echo "----------------------------------------"

  if (( dry_run == 1 )); then
    echo "Files that would be committed:"
    git diff --cached --name-status | sed 's/^/  /'
    echo "Dry run: nothing committed or pushed. The files above are still staged."
    exit 0
  fi

  local msg_file="$WORK_DIR/message.txt"
  printf '%s\n' "$message" >"$msg_file"
  git commit -q -F "$msg_file"

  # Check the commit as written, in case a hook or template added a line.
  local matches
  if matches="$(git log -1 --format=%B | find_blocked_lines)"; then
    {
      echo "push.sh: the commit contains a blocked line, so nothing was pushed:"
      printf '%s\n' "$matches" | sed 's/^/  line /'
      echo "Something outside this script added it: check .git/hooks (prepare-commit-msg, commit-msg),"
      echo "core.hooksPath, commit.template, and the attribution setting in .claude/settings.json."
      echo "Fix the cause, then run: git commit --amend   and remove the line. Then run this script again."
    } >&2
    exit 1
  fi

  local branch
  branch="$(git symbolic-ref --short -q HEAD)" || die "detached HEAD; check out a branch first."

  if git rev-parse --abbrev-ref --symbolic-full-name '@{u}' >/dev/null 2>&1; then
    if ! git pull --rebase; then
      {
        echo "push.sh: the rebase stopped, so nothing was pushed. To finish:"
        echo "  1. Fix the conflicted files listed by: git status"
        echo "  2. git add <files>, then: git rebase --continue"
        echo "  3. Run ./scripts/push.sh again."
        echo "Or undo the rebase with: git rebase --abort"
      } >&2
      exit 1
    fi
  fi

  git push -u origin "$branch"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
