#!/bin/zsh
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT_DIR"

REFERENCE_DB="${REFERENCE_DB:-$ROOT_DIR/data/braacket.sqlite}"
CANDIDATE_DB="${CANDIDATE_DB:-$ROOT_DIR/.tmp/go-parity.sqlite}"
COOKIE_JAR="${COOKIE_JAR:-$ROOT_DIR/.tmp/go-parity-cookies.json}"
LEAGUE_SLUG="${LEAGUE_SLUG:-comelee}"
SAMPLE_SIZE="${SAMPLE_SIZE:-30}"
MAX_EVENT_ATTEMPTS="${MAX_EVENT_ATTEMPTS:-4}"
RETRY_SLEEP_SECONDS="${RETRY_SLEEP_SECONDS:-20}"
RESET_CANDIDATE_DB="${RESET_CANDIDATE_DB:-1}"

mkdir -p "$ROOT_DIR/.tmp"
if [[ "$RESET_CANDIDATE_DB" == "1" ]]; then
  rm -f "$CANDIDATE_DB" "$COOKIE_JAR"
fi

ids=("${(@f)$(sqlite3 "$REFERENCE_DB" "select braacket_id from tournaments where queue_state='imported' order by tournament_date desc, id desc limit $SAMPLE_SIZE;")}")

export GOCACHE="$ROOT_DIR/.cache/go-build"
export PATH="/usr/local/go/bin:$PATH"

for id in "${ids[@]}"; do
  existing_state=$(sqlite3 "$CANDIDATE_DB" "select queue_state from tournaments where braacket_id='$id';" 2>/dev/null || true)
  if [[ "$existing_state" == "imported" ]]; then
    echo "skip $id already imported"
    continue
  fi

  attempt=1
  while (( attempt <= MAX_EVENT_ATTEMPTS )); do
    echo "import $id attempt $attempt/$MAX_EVENT_ATTEMPTS"
    if BRAACKET_DB_PATH="$CANDIDATE_DB" \
      BRAACKET_COOKIE_JAR_PATH="$COOKIE_JAR" \
      go run ./cmd/sync event "$id" --league "$LEAGUE_SLUG"; then
      break
    fi
    if (( attempt == MAX_EVENT_ATTEMPTS )); then
      echo "failed $id after $MAX_EVENT_ATTEMPTS attempts" >&2
      exit 1
    fi
    echo "retrying $id after ${RETRY_SLEEP_SECONDS}s"
    sleep "$RETRY_SLEEP_SECONDS"
    attempt=$((attempt + 1))
  done
done

go run ./cmd/parity \
  --reference-db "$REFERENCE_DB" \
  --candidate-db "$CANDIDATE_DB" \
  --sample-size "$SAMPLE_SIZE"
