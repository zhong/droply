#!/bin/sh
# Keep standalone website downloads identical to their maintenance sources.
set -eu
project_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
mode=${1:-write}
case "$mode" in write|--check) ;; *) echo "usage: $0 [--check]" >&2; exit 2 ;; esac
for name in install.sh setup.sh; do
    source="$project_dir/scripts/$name"
    target="$project_dir/website/$name"
    if [ "$mode" = --check ]; then
        if ! cmp -s "$source" "$target"; then
            echo "website/$name is stale; run make website" >&2
            exit 1
        fi
    else
        cp "$source" "$target"
    fi
done
