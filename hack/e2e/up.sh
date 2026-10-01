#!/usr/bin/env bash
# Boot a disposable Bitbucket Data Center instance for the end-to-end suite.
#
# Unattended: the setup.* properties take the place of the setup wizard, and
# the embedded H2 database stands in for PostgreSQL. That is an unsupported
# production configuration and exactly right for a box that lives for an hour.
#
# The license is the one input that cannot be checked in. Atlassian publishes
# "timebomb" licenses for exactly this purpose — testing against a real
# instance — at
#   https://developer.atlassian.com/platform/marketplace/timebomb-licenses-for-testing-server-apps/
# Copy the "10 user Bitbucket Data Center license, expires in 3 hours" key into
# BITBUCKET_LICENSE (or a file named by BITBUCKET_LICENSE_FILE). The three
# hours count from boot, so a fresh `up.sh` is a fresh license.
set -euo pipefail
cd "$(dirname "$0")"

NAME=${NAME:-bitbucket-bench-e2e}
PORT=${PORT:-17990}
IMAGE=${IMAGE:-atlassian/bitbucket:latest}
ADMIN_PASSWORD=${ADMIN_PASSWORD:-adminpw}

if [ -z "${BITBUCKET_LICENSE:-}" ] && [ -n "${BITBUCKET_LICENSE_FILE:-}" ]; then
  BITBUCKET_LICENSE=$(tr -d '[:space:]' <"$BITBUCKET_LICENSE_FILE")
fi
if [ -z "${BITBUCKET_LICENSE:-}" ]; then
  echo "BITBUCKET_LICENSE (or BITBUCKET_LICENSE_FILE) is required; see the comment at the top of $0" >&2
  exit 2
fi

# A fresh volume every time: a scan's expected verdicts are only meaningful
# against the exact fixture seed.sh builds, not against whatever a previous run
# left behind.
docker rm -f "$NAME" >/dev/null 2>&1 || true
docker volume rm "$NAME-home" >/dev/null 2>&1 || true
# FEATURE_WEBSUDO=false: seed.sh administers the instance through a session,
#   and the secure-administrator re-prompt would stop every admin call.
# FEATURE_PUBLIC_ACCESS=true: off by default since Bitbucket 8, on for many
#   older or upgraded instances. The fixture needs a public project so the
#   anonymous-access half of CIS-1.3.8 is exercised rather than assumed.
docker run -d --name "$NAME" -p "$PORT:7990" \
  -v "$NAME-home:/var/atlassian/application-data/bitbucket" \
  -e SEARCH_ENABLED=false \
  -e JVM_MAXIMUM_MEMORY=2g \
  -e FEATURE_WEBSUDO=false \
  -e FEATURE_PUBLIC_ACCESS=true \
  -e SETUP_DISPLAYNAME="bitbucket-bench e2e" \
  -e SETUP_BASEURL="http://localhost:$PORT" \
  -e SETUP_LICENSE="$BITBUCKET_LICENSE" \
  -e SETUP_SYSADMIN_USERNAME=admin \
  -e SETUP_SYSADMIN_PASSWORD="$ADMIN_PASSWORD" \
  -e SETUP_SYSADMIN_DISPLAYNAME="E2E Admin" \
  -e SETUP_SYSADMIN_EMAILADDRESS=admin@example.com \
  "$IMAGE" >/dev/null

for i in $(seq 1 120); do
  if curl -fs "http://localhost:$PORT/status" 2>/dev/null | grep -q '"RUNNING"'; then
    version=$(curl -fs "http://localhost:$PORT/rest/api/latest/application-properties" | jq -r .version)
    echo "Bitbucket $version up on :$PORT after $((i * 5))s"
    exit 0
  fi
  if ! docker ps --format '{{.Names}}' | grep -qx "$NAME"; then
    echo "container exited during startup:" >&2
    docker logs --tail 40 "$NAME" >&2
    exit 1
  fi
  sleep 5
done
echo "Bitbucket did not reach RUNNING within 10 minutes" >&2
docker logs --tail 40 "$NAME" >&2
exit 1
