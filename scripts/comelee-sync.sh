#!/bin/zsh

set -euo pipefail

repo_root="${0:A:h:h}"
data_dir="${repo_root}/data/comelee"

mkdir -p "${data_dir}"

export BRAACKET_LEAGUE_SLUG="comelee"
export BRAACKET_DB_PATH="${BRAACKET_DB_PATH:-${data_dir}/braacket.sqlite}"
export BRAACKET_COOKIE_JAR_PATH="${BRAACKET_COOKIE_JAR_PATH:-${data_dir}/cookies.json}"

cd "${repo_root}"
exec bun run src/cli.ts sync "$@"
