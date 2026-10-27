#!/bin/sh
# Runs the server as PUID:PGID (1000:1000 unless set), making /data theirs
# first: a bind-mounted folder usually belongs to root, and the first thing
# the server does is open the database there. Started as any other user
# (--user), nothing here applies and the command runs as it is.
set -e
if [ "$(id -u)" = "0" ]; then
  PUID="${PUID:-1000}"
  PGID="${PGID:-1000}"
  if [ "$(id -g cineva)" != "$PGID" ]; then groupmod -o -g "$PGID" cineva; fi
  if [ "$(id -u cineva)" != "$PUID" ]; then usermod -o -u "$PUID" cineva; fi
  # Only what is not already theirs, so a big artwork cache is not walked
  # on every start.
  find /data \! -user "$PUID" -exec chown "$PUID:$PGID" {} + 2>/dev/null || true
  # The supplementary groups group_add gave the container (the host's
  # render group for the GPU) come along; root's own group does not.
  extra="$(id -G | tr ' ' '\n' | grep -vx 0 | paste -sd, -)"
  if [ -n "$extra" ]; then
    exec setpriv --reuid="$PUID" --regid="$PGID" --groups="$extra" "$@"
  fi
  exec setpriv --reuid="$PUID" --regid="$PGID" --clear-groups "$@"
fi
exec "$@"
