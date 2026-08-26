#!/usr/bin/env bash
# Ensure the consuming project has a justfile that imports the platform's shared
# cluster-ops module (`just cluster ...`). Run from the project root by the devbox
# plugin's init_hook, alongside the .gitignore entries it maintains -- same
# contract: touch the consumer's own committed file as little as possible, and be
# idempotent so repeated shell entries never duplicate anything.
set -euo pipefail

import_path=".just/cluster.just"
line="import '${import_path}'"
comment="# Cluster-ops recipes (\`just cluster ...\`) from the dantofa platform devbox plugin."

# just's search order for the project justfile. Reuse whichever the project
# already has rather than creating a second file that just would then ignore.
jf=""
for candidate in justfile Justfile .justfile .Justfile; do
  if [ -f "$candidate" ]; then
    jf="$candidate"
    break
  fi
done

if [ -z "$jf" ]; then
  printf '%s\n%s\n' "$comment" "$line" > justfile
  echo "dantofa-platform: created justfile importing ${import_path}"
  exit 0
fi

# Match the import PATH, not the whole line, so a project that wrote its own
# variant (double quotes, an optional `import?`) is left alone rather than having
# a second, conflicting import appended. `command` bypasses any grep alias.
if command grep -qF "$import_path" "$jf"; then
  exit 0
fi

# Appending to a file whose last line has no newline would splice onto that line
# -- in a justfile that could land inside a recipe body and silently corrupt it.
sep=""
if [ -s "$jf" ]; then
  if [ "$(tail -c 1 "$jf" | wc -l)" -eq 0 ]; then
    printf '\n' >> "$jf"
  fi
  sep=$'\n'
fi
printf '%s%s\n%s\n' "$sep" "$comment" "$line" >> "$jf"
echo "dantofa-platform: added ${import_path} import to ${jf}"
