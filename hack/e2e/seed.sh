#!/usr/bin/env bash
# Build the end-to-end fixture on a fresh instance from up.sh.
#
# Every repository exists to put one or more controls into a known state, so
# that expected/*.tsv can say what a correct scan reports — derived from what
# was configured here, never from what the tool happened to print. A mismatch
# is a finding about the tool until proven otherwise.
#
# This script writes to the instance (it is the fixture, not the scanner). The
# scanner's read-only property is asserted by verify.sh, from its own trace.
#
# Outputs out/tokens.env with the HTTP access tokens verify.sh scans with.
set -euo pipefail
cd "$(dirname "$0")"

PORT=${PORT:-17990}
B="http://localhost:$PORT"
ADMIN_PASSWORD=${ADMIN_PASSWORD:-adminpw}
USER_PASSWORD=${USER_PASSWORD:-userpw}
OUT=out
mkdir -p "$OUT"
chmod 700 "$OUT"

NS=com.atlassian.bitbucket.server
HOOKS=$NS.bitbucket-bundled-hooks
BUILD_HOOKS=$NS.bitbucket-build

# --- sessions --------------------------------------------------------------
# Basic authentication is disabled by default on Bitbucket 10, and HTTP access
# tokens cannot carry global permissions (creating a project, granting ADMIN),
# so the fixture is driven through a logged-in session, as a person would.
login() { # login <user> <password> <cookie-jar>
  rm -f "$3"
  curl -fsS -c "$3" -b "$3" -o /dev/null "$B/login"
  curl -fsS -c "$3" -b "$3" -o /dev/null \
    -H 'Content-Type: application/json' -H 'X-Atlassian-Token: no-check' \
    -X POST "$B/rest/tsv/1.0/authenticate" \
    -d "{\"username\":\"$1\",\"password\":\"$2\",\"rememberMe\":false,\"targetUrl\":\"\"}"
}

ADMIN_JAR=$OUT/admin.cookies
login admin "$ADMIN_PASSWORD" "$ADMIN_JAR"

api() { # api <METHOD> <path> [json-body]
  local method=$1 path=$2 body=${3:-}
  local args=(-sS -b "$ADMIN_JAR" -H 'Content-Type: application/json' -H 'X-Atlassian-Token: no-check' -X "$method" -o "$OUT/last.json" -w '%{http_code}')
  [ -n "$body" ] && args+=(-d "$body")
  local code
  code=$(curl "${args[@]}" "$B$path")
  case "$code" in
  2??) ;;
  *)
    echo "FAILED: $method $path -> $code" >&2
    cat "$OUT/last.json" >&2
    echo >&2
    exit 1
    ;;
  esac
}

# --- users and groups ------------------------------------------------------
for u in admin2 admin3 alice bob carol dave eve svc-ci scanner; do
  api POST "/rest/api/latest/admin/users?name=$u&password=$USER_PASSWORD&displayName=$u&emailAddress=$u@example.com&addToDefaultGroup=true&notify=false"
done

group() { # group <name> <user>...
  local name=$1
  shift
  api POST "/rest/api/latest/admin/groups?name=$name"
  local users
  users=$(printf '%s\n' "$@" | jq -R . | jq -sc .)
  api POST "/rest/api/latest/admin/groups/add-users" "{\"group\":\"$name\",\"users\":$users}"
}
group bb-admins admin2 admin3
group developers alice bob carol dave eve
group release-managers carol

# Three instance administrators in total: admin (SYS_ADMIN, from setup) plus
# the two members of bb-admins. CIS-1.3.3 allows 2-5, so it should PASS — and
# only if the group is expanded, since by name there is just one user.
api PUT "/rest/api/latest/admin/permissions/groups?permission=ADMIN&name=bb-admins"

# --- projects --------------------------------------------------------------
project() { # project <KEY> <name> <public>
  api POST /rest/api/latest/projects "{\"key\":\"$1\",\"name\":\"$2\",\"public\":$3}"
}
repo() { # repo <KEY> <slug> [default-branch]
  # The default branch is set when the repository is created, as the create
  # dialog does. Left out, it falls back to the instance default ("master"),
  # which is how wrong-default ends up pointing at a branch nobody pushed.
  local default=${3:-main}
  api POST "/rest/api/latest/projects/$1/repos" "{\"name\":\"$2\",\"scmId\":\"git\",\"forkable\":true,\"defaultBranch\":\"$default\"}"
}
grant_user() { api PUT "/rest/api/latest/projects/$1/permissions/users?permission=$3&name=$2"; }
grant_group() { api PUT "/rest/api/latest/projects/$1/permissions/groups?permission=$3&name=$2"; }

project HARD "Hardened" false
project WEAK "Weak" false
project PUB "Public" true
project PRIV "Private" false

grant_user HARD alice PROJECT_ADMIN
grant_user HARD bob PROJECT_ADMIN
grant_group HARD developers PROJECT_WRITE
grant_user HARD scanner PROJECT_READ

grant_user WEAK carol PROJECT_ADMIN
grant_user WEAK scanner PROJECT_READ
# Every licensed user can write to every repository in WEAK: CIS-1.3.8.
api POST "/rest/api/latest/projects/WEAK/permissions/PROJECT_WRITE/all?allow=true"

grant_user PUB dave PROJECT_ADMIN

# PRIV deliberately grants the scanner nothing. A least-privilege scan cannot
# see it at all — the honest limit of any token, and worth seeing once.
grant_user PRIV alice PROJECT_ADMIN

repo HARD payments-api
repo HARD empty-service
repo HARD trunk-service trunk
for r in legacy-billing bypassed committer-hook hook-protected archived-tool; do repo WEAK "$r"; done
# wrong-default: configured default branch "master", but only "main" is ever
# pushed. Common after an upgrade, when git's own default moved to main and the
# instance's did not. Bitbucket then reports a default branch that does not
# exist, and refuses branches?details=true outright.
repo WEAK wrong-default master
repo PUB docs-site
repo PRIV secret-sauce

# --- content ---------------------------------------------------------------
# Pushed before any restriction or pre-receive hook exists: once the fixture
# is configured, the hardened repositories would refuse these very pushes.
SEED_TOKEN=$(curl -fsS -b "$ADMIN_JAR" -H 'Content-Type: application/json' -H 'X-Atlassian-Token: no-check' \
  -X PUT "$B/rest/access-tokens/latest/users/admin" \
  -d '{"name":"e2e-seed","permissions":["PROJECT_ADMIN","REPO_ADMIN"]}' | jq -r .token)

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

push() { # push <KEY> <slug> <default-branch> <file>... (files relative to a fresh tree)
  local key=$1 slug=$2 branch=$3
  shift 3
  local dir="$WORK/$key-$slug"
  rm -rf "$dir"
  git init -q -b "$branch" "$dir"
  (
    cd "$dir"
    git config user.email e2e@example.com
    git config user.name e2e
    printf '# %s\n' "$slug" >README.md
    for f in "$@"; do
      mkdir -p "$(dirname "$f")"
      printf 'Report vulnerabilities to security@example.com.\n' >"$f"
    done
    git add -A
    git commit -qm "initial"
    git -c http.extraHeader="Authorization: Bearer $SEED_TOKEN" push -q \
      "$B/scm/$(echo "$key" | tr '[:upper:]' '[:lower:]')/$slug.git" "$branch"
  )
}

# branch <KEY> <slug> <name> <iso-date>: a branch whose tip commit is dated
# <iso-date>, author and committer both, so its age is unambiguous.
branch() {
  local key=$1 slug=$2 name=$3 when=$4
  local dir="$WORK/$key-$slug"
  (
    cd "$dir"
    git checkout -q -b "$name"
    echo "$name" >"$(echo "$name" | tr '/' '-').txt"
    git add -A
    GIT_AUTHOR_DATE="$when" GIT_COMMITTER_DATE="$when" git commit -qm "$name"
    git -c http.extraHeader="Authorization: Bearer $SEED_TOKEN" push -q \
      "$B/scm/$(echo "$key" | tr '[:upper:]' '[:lower:]')/$slug.git" "$name"
    git checkout -q -
  )
}

push HARD payments-api main SECURITY.md
branch HARD payments-api feature/recent "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
push HARD trunk-service trunk docs/SECURITY.md
# HARD/empty-service stays empty: no commits, no default branch.
push WEAK legacy-billing main
branch WEAK legacy-billing old/feature-x 2020-01-15T10:00:00Z
push WEAK bypassed main SECURITY.md
push WEAK committer-hook main SECURITY.md
push WEAK hook-protected main SECURITY.md
push WEAK archived-tool main
push WEAK wrong-default main SECURITY.md
push PUB docs-site main SECURITY.md
push PRIV secret-sauce main SECURITY.md

# --- HARD: configured once, at the project, as an operator would -----------
# Linear history only.
api POST /rest/api/latest/projects/HARD/settings/pull-requests/git \
  '{"mergeConfig":{"defaultStrategy":{"id":"ff-only"},"strategies":[{"id":"ff-only"},{"id":"squash-ff-only"},{"id":"rebase-ff-only"}]}}'

hook_project() { # hook_project <KEY> <hook-key> [settings-json]
  api PUT "/rest/api/latest/projects/$1/settings/hooks/$2/enabled" "${3:-}"
}
hook_repo() { # hook_repo <KEY> <slug> <hook-key> [settings-json]
  api PUT "/rest/api/latest/projects/$1/repos/$2/settings/hooks/$3/enabled" "${4:-}"
}
hook_project HARD "$HOOKS:requiredApproversMergeHook" '{"requiredCount":"2"}'
hook_project HARD "$BUILD_HOOKS:requiredBuildsMergeCheck" '{"requiredCount":"1"}'
hook_project HARD "$HOOKS:incomplete-tasks-merge-check"
hook_project HARD "$HOOKS:force-push-hook"
hook_project HARD "$HOOKS:verify-commit-signature-hook"

restrict() { # restrict <scope-path> <type> <matcher-type> <matcher-id> [users-json] [groups-json]
  api POST "/rest/branch-permissions/2.0/$1/restrictions" \
    "{\"type\":\"$2\",\"matcher\":{\"id\":\"$4\",\"type\":{\"id\":\"$3\"}},\"users\":${5:-[]},\"groups\":${6:-[]},\"accessKeys\":[]}"
}
# Project-level restrictions on refs/heads/main. trunk-service's default
# branch is "trunk", so these do not cover it: CIS-1.1.15/1.1.17 must FAIL
# there even though the project looks protected. Its force pushes are still
# refused, by the project's Reject Force Push hook.
for t in pull-request-only fast-forward-only no-deletes; do
  restrict projects/HARD "$t" BRANCH refs/heads/main
done

# A required build on payments-api's default branch.
api POST /rest/required-builds/latest/projects/HARD/repos/payments-api/condition \
  '{"buildParentKeys":["ci-build"],"refMatcher":{"id":"refs/heads/main","type":{"id":"BRANCH"}}}'

# --- WEAK: one deviation per repository -------------------------------------
# bypassed: every restriction is there, and every one exempts the whole team.
for t in pull-request-only fast-forward-only no-deletes; do
  restrict projects/WEAK/repos/bypassed "$t" BRANCH refs/heads/main '[]' '["developers"]'
done
# committer-hook: "Verify Committer" checks that the pusher authored the
# commits. It verifies no signature, so CIS-1.1.12 must not accept it.
hook_repo WEAK committer-hook "$HOOKS:verify-committer-hook"
# hook-protected: no branch restriction at all, but force pushes are refused
# by the bundled Reject Force Push hook, so CIS-1.1.16 is satisfied.
hook_repo WEAK hook-protected "$HOOKS:force-push-hook"
# archived-tool: read-only from here on.
api PUT /rest/api/latest/projects/WEAK/repos/archived-tool '{"archived":true}'

# --- authentication history ------------------------------------------------
# Logging in is what sets lastAuthenticationTimestamp. eve never does: she
# has write access through developers and no recorded authentication at all.
for u in admin2 admin3 alice bob carol dave svc-ci scanner; do
  login "$u" "$USER_PASSWORD" "$OUT/$u.cookies"
done

# --- scan tokens -----------------------------------------------------------
token() { # token <user> <cookie-jar> <name> <permissions-json>
  curl -fsS -b "$2" -H 'Content-Type: application/json' -H 'X-Atlassian-Token: no-check' \
    -X PUT "$B/rest/access-tokens/latest/users/$1" \
    -d "{\"name\":\"$3\",\"permissions\":$4}" | jq -r .token
}
{
  echo "BITBUCKET_URL=$B"
  # Full coverage: Bitbucket shows branch permissions, hooks and grant tables
  # only to repository and project administrators, and a token can never hold
  # a global permission, so this is the most any token can see.
  echo "ADMIN_FULL_TOKEN=$(token admin "$ADMIN_JAR" e2e-admin-full '["PROJECT_ADMIN","REPO_ADMIN"]')"
  # A read-only token held by an instance administrator.
  echo "ADMIN_READ_TOKEN=$(token admin "$ADMIN_JAR" e2e-admin-read '["PROJECT_READ","REPO_READ"]')"
  # Least privilege: an ordinary user granted PROJECT_READ on HARD and WEAK.
  echo "SCANNER_TOKEN=$(token scanner "$OUT/scanner.cookies" e2e-scanner '["PROJECT_READ","REPO_READ"]')"
} >"$OUT/tokens.env"
chmod 600 "$OUT/tokens.env" "$OUT"/*.cookies

echo "seeded: $(curl -fsS -b "$ADMIN_JAR" "$B/rest/api/latest/repos?limit=100" | jq '.size') repositories in 4 projects"
