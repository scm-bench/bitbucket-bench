<!--
  The banner lives in scm-bench/.github (brand/), which is also where the
  organization profile and the uploaded avatar draw from, so there is one copy
  rather than one per repository. The URLs are absolute for two reasons: a
  relative path cannot cross repositories, and README.md ships inside every
  release tarball, where a repository-relative image resolves to nothing.
-->
<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/scm-bench/.github/main/brand/banner-bitbucket-bench-dark-1760x440.png">
    <source media="(prefers-color-scheme: light)" srcset="https://raw.githubusercontent.com/scm-bench/.github/main/brand/banner-bitbucket-bench-light-1760x440.png">
    <img src="https://raw.githubusercontent.com/scm-bench/.github/main/brand/banner-bitbucket-bench-light-1760x440.png" alt="bitbucket-bench — audit Bitbucket Data Center against the CIS supply chain benchmark" width="880">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/scm-bench/bitbucket-bench/actions/workflows/ci.yml"><img src="https://github.com/scm-bench/bitbucket-bench/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/scm-bench/bitbucket-bench/releases"><img src="https://img.shields.io/github/v/release/scm-bench/bitbucket-bench?include_prereleases&sort=semver" alt="Release"></a>
  <a href="https://goreportcard.com/report/github.com/scm-bench/bitbucket-bench"><img src="https://goreportcard.com/badge/github.com/scm-bench/bitbucket-bench" alt="Go report card"></a>
  <a href="https://pkg.go.dev/github.com/scm-bench/bitbucket-bench"><img src="https://pkg.go.dev/badge/github.com/scm-bench/bitbucket-bench.svg" alt="Go reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue" alt="Apache 2.0"></a>
</p>

Audit **Bitbucket Data Center** against the **Source Code** section of the
[CIS Software Supply Chain Security Guide](https://www.cisecurity.org/benchmark/software-supply-chain-security).

bitbucket-bench captures a **read-only** snapshot of your instance, evaluates it against
policies written in Rego, and tells you what is misconfigured — along with the exact
settings path to fix it.

15 controls are evaluated automatically; 5 more are carried as documented manual
checks so the mapping is complete rather than quietly partial. Every verdict is
checked against a real Bitbucket Data Center by an end-to-end suite, with three
tokens of different reach — see [Tested against](#tested-against).

This is the Bitbucket bench of [scm-bench](https://github.com/scm-bench/scm-bench),
a family of tools that audit one platform each and report in the same shape. It is
the reference implementation of the family's
[bench contract](https://github.com/scm-bench/scm-bench/blob/main/docs/bench-contract.md).

[简体中文](README.zh-CN.md) · [Contributing](CONTRIBUTING.md) · [Security](SECURITY.md) · [Spec](#the-specification-it-implements)

---

## The one design decision worth knowing

**A control that could not be evaluated reports `MANUAL`, never `PASS` or `FAIL`.**

If your token cannot read branch permissions, if a group's members cannot be
listed, if Bitbucket does not report last-authentication timestamps — the tool
says so, and that control is excluded from the score entirely. A scan is never
inflated by questions it could not ask, and never penalises you for them either.

This matters more than it sounds. A benchmark tool that silently reports `FAIL`
for "the API returned 403" teaches people to ignore its output.

---

## Install

**Binary** — download from [releases](https://github.com/scm-bench/bitbucket-bench/releases):

```bash
# The archive name carries the version, so resolve the latest tag first.
VERSION=$(curl -fsSL https://api.github.com/repos/scm-bench/bitbucket-bench/releases/latest |
  sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')

curl -fsSL "https://github.com/scm-bench/bitbucket-bench/releases/download/v${VERSION}/bitbucket-bench_${VERSION}_linux_amd64.tar.gz" | tar xz
./bitbucket-bench version
```

**Docker:**

```bash
docker run --rm ghcr.io/scm-bench/bitbucket-bench:latest \
  scan --url https://bitbucket.example.com --token "$BITBUCKET_TOKEN"
```

**From source** (Go 1.25+; the build pins a patched toolchain and will fetch it):

```bash
go install github.com/scm-bench/bitbucket-bench/cmd/bitbucket-bench@latest
```

### Verifying what you downloaded

`checksums.txt` and the container images are signed with
[cosign](https://docs.sigstore.dev/), keylessly — the signing identity is this
repository's release workflow, so there is no key to trust or to leak.

The archives are not signed individually. Every archive's digest is already in
`checksums.txt`, so one signature covers the whole release: verify the
signature, then verify the archive against the file it vouches for. Archives
additionally carry a SLSA build provenance attestation.

Every release's notes carry the exact `cosign verify-blob` and
`gh attestation verify` commands, with the identity flags filled in. Those flags
are the part that matters: without them a verification only confirms that
*somebody* signed the file.

`go install` is covered separately — the Go module proxy's checksum database
pins what that path can fetch.

---

## Quick start

```bash
export BITBUCKET_URL=https://bitbucket.example.com
export BITBUCKET_TOKEN=<read-only HTTP access token>

# Scan everything
bitbucket-bench scan

# Scan one project, or one repository
bitbucket-bench scan --project PLAT
bitbucket-bench scan --repository PLAT/payments-api

# Machine-readable output
bitbucket-bench scan -o json  --output-file report.json
bitbucket-bench scan -o sarif --output-file report.sarif   # GitHub code scanning
bitbucket-bench scan -o junit --output-file report.xml     # Jenkins, Azure Pipelines, GitLab
```

No instance handy? A sample ships inside the binary, so the first report is one
flag away:

```bash
bitbucket-bench scan --demo
```

Run `bitbucket-bench scan` bare on a terminal and it offers the same choice
interactively: enter a URL and token, or see the sample first. The sample is
also checked in as `examples/snapshot.json`, which `--snapshot-in` evaluates
from a checkout.

If you enter a URL and token, scan offers — it asks, it does not assume — to
save them once they have proven to work, so later runs need nothing. They go
to `instance.yaml` under your user config directory (`~/.config/bitbucket-bench/` on
Linux; `BITBUCKET_BENCH_CONFIG_DIR` overrides the location), mode `0600` since the
token is a live credential. A typed `--url` or an exported `BITBUCKET_URL`
always wins over the file, and every scan that uses it says so on stderr.
Delete the file to forget it.

Repositories are fetched concurrently — `scan.concurrency` in the config file
(default 8) bounds how many at once, and lowering it is the polite response to
an instance under load. `scan.timeout` bounds a single request (default 30s);
`scan.maxDuration` bounds the whole scan and is off unless set, because how
long is too long depends entirely on how big the instance is. These live in
the config rather than in flags because they describe the deployment, not any
one run — see [Configuration](#configuration).

### Watching the scan

By default a scan shows one self-overwriting progress line — a spinner, the
current phase, and a live count of completed requests, visible from the first
moment so a slow instance never looks like a hung one — and then the report.
The request log is **`--verbose`**: a list of every `GET` describes what the
tool did, and what you came for is what it found. The requests matter when
something looks wrong, which is exactly when you type `--verbose`:

```
[INFO]   GET  /users                                               200   28ms
[INFO]   GET  /users?permission=ADMIN                              200   17ms
[INFO]   GET  /admin/permissions/users                             401   16ms
[INFO]   GET  /users?permission=LICENSED_USER                      200   20ms
[INFO]   GET  /admin/users                                         200   27ms
[INFO]   GET  /projects/HARD                                       200   17ms
[INFO]   GET  /projects/HARD/repos                                 200   20ms
[INFO]
[INFO]   HARD/payments-api
[INFO]     GET  /users?permission.1=LICENSED_USER&permission.2=REPO_READ&start=9 200   21ms
[INFO]     GET  …/branches?details=true                              200   48ms
[INFO]     GET  …/branchmodel                                        200   26ms
[INFO]     GET  …/settings/pull-requests                             200   27ms
[INFO]     GET  …/restrictions                                       401   18ms
[INFO]     GET  …/conditions                                         200   29ms
[INFO]     GET  …/settings/hooks                                     401   22ms
[INFO]     GET  …/browse/SECURITY.md?at=refs/heads/main              200   36ms
[INFO]     GET  /users?permission.1=REPO_ADMIN                       200   22ms
[INFO]
[INFO] ✓ 16 requests · 16 GET · 0 writes · read-only
```

That is a read-only token scanning one repository on Bitbucket 10.4, and each
`401` is expected. The first is the instance's grant table, which no token can
read — Bitbucket will not let a token hold a global permission — and it costs
nothing: the line above it has already asked Bitbucket who the administrators
are. The two under the repository are its branch permissions and hooks, which
Bitbucket shows only to repository administrators, so the four controls built
on them — signed commits, direct pushes, force pushes, branch deletion — will
report `MANUAL`. The query string is shown when it is what
tells two requests apart — a page offset, which group is being expanded, which
permission is being asked about. Without it, paging a user directory
prints the same line twenty-two times and reads like a stuck loop.

**The closing line is the point.** It counts the methods actually sent, not the
promise that they are all `GET`. A non-read request would be reported as
`✗ … NOT READ-ONLY` rather than quietly folded into the total — the claim is
meant to be checkable, and a claim you cannot check is worth nothing.

Requests are grouped by repository. They arrive interleaved, because
repositories are fetched concurrently, so each one's requests are held and
printed together when it finishes.

`scan.progress` in the config selects how much of this to show:

| Mode | Shows |
|---|---|
| `compact` (default) | one self-overwriting progress line, plus the closing line |
| `full` (implied by `--verbose`) | every request, plus the closing line |
| `off` | only the closing line |

Redirected or in CI, `full` falls back to `off` on its own: without a cursor to
move, a request per line is thousands of lines nobody asked for. The closing
line is still printed, because an account of what a token was used for belongs
in a CI log too.

`--verbose` turns on the request log and the fetcher's own narration; being
the flag typed just now, it wins over the config's ambient `progress` setting.

`scan.maxDuration` abandons a scan that runs too long, exiting `2`:

```yaml
scan:
  maxDuration: 20m
```

There is no default. How long is too long depends entirely on the size of the
instance, and a default guess would turn a legitimately long scan into a
failure. `scan.timeout` is a separate thing — it bounds one HTTP request, not
the whole scan.

### What the token needs

Use an HTTP access token (`--token`, `BITBUCKET_TOKEN`). Two facts about
Bitbucket decide what it needs, both measured on Bitbucket 10.4:

- **A token can never hold a global permission.** Bitbucket refuses to create
  one with `ADMIN` or `SYS_ADMIN`, whoever asks. Nothing here needs one: the
  instance-level questions — who administers the instance, who is licensed,
  who can reach each repository — are put to Bitbucket's own permission
  resolution, which answers any authenticated user and expands groups itself.
- **Branch permissions and hooks are shown only to repository
  administrators.** The four controls built on them need the token to carry
  admin permission on the repositories it scans.

| Token permissions | Answers |
|---|---|
| `PROJECT_READ` + `REPO_READ` | 11 of the 15 automated controls: approvals, approval reset, stale branches, CI gating, tasks, linear history, the security policy, dormant accounts, instance administrators, repository administrators, base access |
| `PROJECT_ADMIN`, or `REPO_ADMIN` | all 15 — adding signed commits, direct pushes, force pushes and branch deletion |

Either admin permission is enough: a token holding `PROJECT_ADMIN` with
`REPO_READ`, or `PROJECT_READ` with `REPO_ADMIN`, reads branch permissions and
hooks alike. A token never exceeds its user, so the user needs that permission
on the repositories too.

A read-only token is enough to start; the four branch-protection controls then
report `MANUAL` and say why, and the scan carries on. The admin token is
admin-*capable* — bitbucket-bench never writes, and the closing trace line
accounts for every request it sent, but store that token like the admin
credential it is.

**What the scan covers is what the token's user can see.** A project the user
cannot read does not appear at all, and no tool can report what it was never
shown. For a whole-instance audit, run as a dedicated service account that can
read (or administer) every project — typically through a group granted on each
one — and compare the repository count in the report with the instance's.

Passwords are refused by default on Bitbucket 10's REST API ("Basic
Authentication has been disabled on this instance"); the scan says so and
points at tokens. Where an instance still allows them, `--username` /
`--password` (`BITBUCKET_USERNAME` / `BITBUCKET_PASSWORD`) take the token's
place; give one or the other, not both. What you type on the command line
beats what the environment supplies, so a stale `BITBUCKET_TOKEN` in a shell
profile does not silently override the credentials you just entered — and a
credential arriving without a URL is never paired with an instance saved by an
earlier run.

Credentials embedded in the URL — `https://user:pw@bitbucket.example.com` — are
not used and are stripped before the URL reaches a snapshot or a report.

A credential the instance does not accept — mistyped, expired, revoked, or for
another instance — stops the scan before anything is fetched, and exits `2`.
That is less obvious than it sounds: Bitbucket does not refuse a bearer token
it does not recognise, it serves the request as anonymous. The preflight asks
an endpoint anonymous callers cannot read, so a dead token is caught there
rather than producing a report of everything an anonymous visitor can see.

bitbucket-bench issues **only `GET` requests**. This is enforced by a test, not
just by convention.

### Transport

That token can read every repository on the instance, so it is not put on the
wire in the clear. An `http://` URL is refused before the first request — unlike
the misconfigurations this tool reports, a leaked credential cannot be undone
once it is out. `scan.allowPlaintext` in the config overrides it for a
genuinely trusted network; loopback addresses are exempt and need no setting.

An instance whose certificate comes from an **internal CA** needs
`scan.caFile`: a PEM bundle trusted in addition to the system's, read and
checked at startup. `scan.insecure` skips verification entirely and is the
last resort, not the enterprise setting; the two are refused together, since
the bundle would quietly mean nothing. A proxy is taken from `HTTPS_PROXY` /
`NO_PROXY`.

Redirects are followed only while they stay on the instance's scheme, host and
port. Go forwards the `Authorization` header across a redirect to the same
host — including one from `https` down to `http` — so a redirect elsewhere is
refused with both ends named rather than followed.

`allowPlaintext` and `insecure` are configuration rather than flags on
purpose: weakening transport security should be a decision written into a
file someone can review, not a habit of the fingers. Both leave a line in the
report's scan warnings and in any snapshot captured that way. A scan taken
over cleartext, or without verifying who answered, is not the same evidence as
one that was not.

---

## Coverage

15 controls evaluated automatically:

| CIS | Control | Severity | Decided from |
|---|---|---|---|
| 1.1.3 | Two approvals required to merge | HIGH | The Minimum approvals merge check, inherited from the project too |
| 1.1.4 | Approvals reset when the branch is updated | MEDIUM | "Unapprove automatically on new changes" — Atlassian's separately installed Auto Unapprove app |
| 1.1.8 | Abandoned branches are pruned | LOW | Branch tips older than 90 days |
| 1.1.9 | CI must pass before merge | HIGH | Required builds matched to the default branch, or the Minimum successful builds merge check |
| 1.1.11 | Open tasks block the merge | LOW | The No incomplete tasks merge check |
| 1.1.12 | Commit signatures are verified | MEDIUM | The bundled Verify Commit Signature hook (8.13+), or an add-on's hook named in `signatureHookKeys` |
| 1.1.13 | Linear history is required | LOW | Enabled merge strategies: merge commit, fast-forward with fallback and rebase-and-merge are not linear |
| 1.1.15 | No direct pushes to the default branch | HIGH | `pull-request-only` / `read-only` restriction, and who is exempt from it |
| 1.1.16 | No force pushes | HIGH | The bundled Reject Force Push hook, or a `fast-forward-only` restriction and who is exempt from it |
| 1.1.17 | No branch deletion | MEDIUM | `no-deletes` restriction, and who is exempt from it |
| 1.2.1 | A security policy is published | LOW | `SECURITY.md` on the default branch |
| 1.3.1 | Dormant accounts are reviewed | MEDIUM | Last authentication of every active, licensed account |
| 1.3.3 | Instance administrators are bounded (2–5) | HIGH | Who holds `ADMIN` or `SYS_ADMIN`, as Bitbucket resolves it |
| 1.3.7 | Each repository has ≥2 administrators | LOW | The repository's own administrators — repository and project grants — not counting instance administrators |
| 1.3.8 | Default repository access is restricted | MEDIUM | Public access, and what every licensed user can do on the repository |

Branch matchers are resolved as Bitbucket resolves them — patterns match a
suffix of the qualified ref, model branches come from the repository's branch
model — and a matcher the scan cannot resolve makes the control `MANUAL`
rather than a guess. An empty repository reports `NA` for everything about
branches; an archived one reports `NA` for every control about how a change
arrives, and is still judged on who can read it (`skipArchivedRepositories`
leaves archived repositories out entirely).

### A restriction that exempts people is not protection

The three branch-permission controls above judge **who the restriction actually
binds**, not merely whether somebody configured one. A `no-deletes` restriction
that exempts the `developers` group does not stop the developers deleting the
branch, and reporting that as a pass described the repository as safer than it
is — which is the one thing this tool is built not to do.

Exemptions are resolved the way everything version-dependent is: the fetcher
expands the groups, so a rule counts people rather than group names, and an
exemption granted to a team covers everyone who joins it later.

**Only principals exempt from *every* restriction covering the branch count.**
Protection is the union of those restrictions, so somebody exempt from "Prevent
all changes" but still subject to "Prevent deletion" cannot delete it and is
not a bypass. This is what makes a read-only restriction read correctly:
naming the release managers who may write to an otherwise frozen branch is how
that restriction is meant to be used.

Two settings decide the verdict:

```yaml
thresholds:
  maxBypassPrincipals: 0        # how many principals may hold an exemption
allowedBypassPrincipals: [release-bot]   # the ones that do not count
```

`allowedBypassPrincipals` is the one to reach for first. A build account
usually does need to push past a restriction; without somewhere to say so, the
threshold has to be raised high enough to cover it, which is high enough to
hide the people.

When a group holding an exemption cannot be expanded, the count is a **lower
bound**. A lower bound that already exceeds the threshold still fails — the
missing members can only add to it — and one that does not reports `MANUAL`,
because a group the token could not read is not evidence that nobody is in it.

Upgrading from a `v0.1.0-rc` build changes this verdict on purpose — see
[Upgrading from a v0.1.0-rc build](#upgrading-from-a-v010-rc-build).

5 controls carried as documented manual checks — they are reported, explained, and
excluded from the score:

| CIS | Control | Why it is not automated |
|---|---|---|
| 1.1.6 | Code owners | Bitbucket DC has no CODEOWNERS. Default reviewers are advisory unless paired with an approval count, so mapping them here would overstate enforcement. |
| 1.2.2 | Repository creation is limited | Needs the global Project Creator permission read together with group membership and per-project grants; "sufficiently limited" is deployment-specific. Next on the [roadmap](#roadmap). |
| 1.2.3 | Repository deletion is limited | Bitbucket does not expose deletion as its own permission — any verdict would restate CIS-1.3.3 and CIS-1.3.7. |
| 1.3.5 | MFA is enforced | Authentication is delegated to an external IdP (SAML/Crowd/LDAP). Bitbucket's API exposes nothing about factors, so a verdict from Bitbucket data would be invented. |
| 1.3.9 | Organization is verified | A hosted-SaaS concept with no self-hosted equivalent. Reported `NA`. |

```bash
bitbucket-bench list-checks          # all controls, with severity and scope
bitbucket-bench list-checks --json   # full metadata, including remediation text
```

### Upgrading from a v0.1.0-rc build

This release was checked against a real Bitbucket Data Center 10.4, and that
check changed verdicts. Each change is a correction; none is a new opinion
about what the benchmark asks.

| Control | The rc builds said | Now |
|---|---|---|
| CIS-1.1.15/16/17 | `PASS` when a restriction existed, however many people were exempt | Judges who the restriction binds; `thresholds.maxBypassPrincipals: -1` restores the old reading while you clean up |
| CIS-1.1.12 | `PASS` for the bundled "Verify Committer" hook, which verifies no signature | Only hooks named by full key in `signatureHookKeys` count; the default is the bundled Verify Commit Signature hook |
| CIS-1.1.16 | `FAIL` for a repository protected by the Reject Force Push hook | `PASS` |
| CIS-1.1.13 | `PASS` with "Fast-forward" enabled, which creates merge commits whenever the target moved | `FAIL`; remove `ff` from `nonLinearMergeStrategies` to accept it |
| CIS-1.3.7 | Counted instance administrators, so passed on every repository of any instance with two | Counts the repository's own administrators |
| CIS-1.3.1, 1.3.3, 1.3.7, 1.3.8 | `MANUAL` for every token, which could not read the grant tables | Answered for a read-only token, through Bitbucket's own permission resolution |
| CIS-1.2.1 and every branch control | Judged a configured default branch that did not exist; failed empty repositories | Judge the branch that exists; empty repositories are `NA` |
| Archived repositories | Left out of the report | Reported, with change controls `NA` |

Three things you may have to change:

- **`signatureHookKeys` holds full hook keys now** (`plugin-key:module-key`). A
  list written for the old substring matching (`gpg`, `signature`) is refused
  at startup with the format in the message.
- **Snapshots from an rc build are refused, not read.** They are schema 1; this build
  reads schema 2. Re-scan once after upgrading: `--last` works from the next
  scan, and a `diff` baseline has to be re-captured before it compares.
- **A scan that cannot vouch for its coverage exits `2`** — one that evaluated
  no repository, or could not list a project's repositories. See
  [In CI](#in-ci).

---

## Scoring

```
score = ⌊ Σ weight(passed) / Σ weight(passed + failed) × 100 ⌋
```

with `HIGH = 3`, `MEDIUM = 2`, `LOW = 1`. `MANUAL` and `NA` are in neither sum.
The score is floored, never rounded: 1510 of 1512 is 99, so a failing finding
can never print as 100 or pass `failUnder: 100`.

The table output prints the arithmetic (`weighted 30/57 (HIGH=3, MEDIUM=2,
LOW=1; manual and n/a excluded)`) so the number is checkable rather than
something you have to trust.

One deliberate edge case: when **nothing** was decidable, the score is `0`, not
`100`. An empty numerator over an empty denominator should not read as a clean
bill of health.

Treat the score as a trend line. The failure counts by severity are what actually
decide whether a result is acceptable.

---

## Output formats

**`table`** (default) — despite the name, a line-oriented findings report, the
shape linters and compilers use. Each failure is one self-contained record, and
the score block closes it:

```
bitbucket-bench example  ·  https://bitbucket.example.com  ·  2026-01-15 09:00:00 UTC

PLAT/legacy-billing  CIS-1.1.3 HIGH: Pull requests require 0 approval(s); at least 2 independent
    approvals are needed.
    fix: Set "Minimum approvals" to at least 2 at Repository settings -> Pull requests -> Merge
    checks.
    · requiredApprovers = 0

... one record per failing resource and control, severity descending ...

PLAT/legacy-billing  CIS-1.1.17 MEDIUM: Deletion of master is restricted, but dana, erin, frank,
    grace and 1 more can still delete it, taking the branch and its protections with them.
    fix: Block deletion at Repository settings -> Branch permissions; check who is exempt.
    · exempt from every restriction covering master: dana, erin, frank, grace and 1 more
    · 5 in total; thresholds.maxBypassPrincipals is 0
instance  CIS-1.3.1 MEDIUM: 1 active, licensed account(s) have not authenticated in 90 days or more:
    dana (last authenticated 380 days ago).
    fix: Deactivate each listed account at Administration -> Users.
    · dana (last authenticated 380 days ago)

CIS-1.3.5 MANUAL (instance): Multi-factor authentication is enforced by the identity provider in
    front of Bitbucket Data Center, not by Bitbucket, and cannot be read through its API. Verify
    enforcement in your SSO, Crowd or LDAP configuration.
    fix: Require MFA in the IdP, then disable direct login at Administration -> Authentication.
CIS-1.1.6 MANUAL (3 repositories): Bitbucket Data Center has no native code-owners mechanism.
    Confirm by hand that changes to sensitive paths require review by their owners, typically via
    default reviewers combined with a minimum approval count.
    fix: Add a binding condition at Repository settings -> Default reviewers, approvals required 1
    or more.

... one line per control that needs a person, and per distinct reason ...

13 controls could not be read (Unread) on PLAT/vendor-mirror; see Scan warnings below.

Scan warnings

  - group "contractors" could not be expanded (GET /api/1.0/admin/groups/more-members: 403 You are
    not permitted to access this resource); counts derived from it are lower bounds

  fix: rerun with a token that can read what this one could not: Bitbucket shows branch permissions
       and hooks only to repository administrators, so give the token PROJECT_ADMIN or REPO_ADMIN on
       the repositories it scans.

Rules

  CIS-1.1.3   https://confluence.atlassian.com/bitbucketserver/checks-for-merging-pull-requests-776640039.html
  CIS-1.1.9   https://confluence.atlassian.com/bitbucketserver/checks-for-merging-pull-requests-776640039.html

... one line per control in the report, with the vendor's documentation page ...

Details: rerun with --details for per-resource findings and full remediation steps, or
--details=<resource|control>[,...] to filter; -o json for the full report.

SCORE 52/100   15 passed  14 failed  19 manual  14 n/a
      14 controls failed
      weighted 30/57 (HIGH=3, MEDIUM=2, LOW=1; manual and n/a excluded)
      scored 29 of 48 findings (60%); 19 could not be evaluated
```

That block is the real output of `bitbucket-bench scan --snapshot-in examples/snapshot.json`
at `COLUMNS=100`, abbreviated only where a line is marked `...`.

**A failure is one record, and the record is self-contained**: `<resource>
<CHECK-ID> <SEVERITY>: <details>` on the first line, the one-line fix indented
beneath it, the evidence as dim `·` lines under that. Nothing has to be carried
in your head to a table further down the output, which is what makes the report
greppable — `grep 'HIGH:'` is the list of severe findings, and every hit brings
its own context with it.

**Controls that need a person aggregate to one line each**, keyed by control
and reason: `<CHECK-ID> MANUAL (<n> <resources>): <reason>`. A question needing
judgement is one question however many repositories it spans, and two different
reasons keep their own lines. Findings the scan could not *read* are a
different thing and collapse further still, into the one sentence above the
scan warnings: they share a single cause, and stating it once beats restating
it per control per resource.

`Rules` closes the findings with the vendor's documentation page for each
control, once, rather than repeating a URL under every record it applies to —
plus the one hint a single record cannot carry: a control failing on *every*
repository is one project-level setting, not N repository-level ones, and its
line says so ("failing on all 4 repositories — setting it once at Project
settings covers them together"). The one-line fix that actually gets acted on
rides with the finding instead.

**The score block closes the report**, so the verdict is the last thing printed
and every number above it is already on screen to check it against. Two
countings meet there and the second line is the bridge between them: the score
counts *findings* — one control against one resource — while the line beneath
it and the closing exit line count *controls*, so "14 failed" and "14 controls
failed" are the same fact seen from both sides (on a larger scan it reads "6
controls failed across 23 findings"). The `scored N of M findings` line is
worth reading before the score above it: findings that could not be evaluated
are excluded from both sides of the fraction, which is right for any single
control and misleading in aggregate, since a token that can read very little
produces a high score from a small sample. `scan.maxManual` turns that into a
failed run rather than a good-looking one.

**`--details` is where the per-resource detail lives.** Bare, it renders one
section per resource in the shape trivy uses — a `Control | Severity | Status |
Title | Finding` table with the evidence and one-line fix in each cell, scan
warnings moved above the sections so cause still precedes symptom. With values,
it narrows the sections: `--details=PLAT/legacy-billing` matches resources by
case-insensitive substring, `--details=CIS-1.1.9` (or `1.1.9`) matches a
control exactly, and mixing kinds intersects them. The `=` is required when
passing values; a value that matches nothing is an error rather than a
quietly clean-looking report. The remediation section narrows with the filter.

```bash
bitbucket-bench scan --details                      # every resource, every finding
bitbucket-bench scan --details=payments-api         # one repository's full verdict
bitbucket-bench scan --details=CIS-1.1.15,CIS-1.1.16  # two controls, wherever they land
```

**`UNREAD` and `MANUAL` are both `MANUAL` underneath, split by cause.** `UNREAD`
is what *this run* could not read; `MANUAL` is a control *no API can answer*
(`automated: false` in its metadata), which needs a person no matter how good
your token is. Only the second gets remediation text: a control the scan never
saw is not known to be misconfigured, and printing how to change its settings
would say otherwise. The JSON and the SARIF say `MANUAL` for both, because that
is what the control returned.

**The fix travels with the finding, and the paragraph waits for `--details`.**
In the line report every record carries its one-line `fix:`, so acting on a
finding never means scrolling to a list somewhere else; only the documentation
link is deferred, to `Rules`. (The generic CIS benchmark landing page is on
every control and identifies none of them, so it never earns a line.)

`--details` prints the full remediation paragraphs — settings paths,
project-wide variants, config keys — in two sections, because the entries ask
for two different things: `Remediations` is settings that are wrong and how to
change them, `Manual review` is controls no API can decide, where the ask is a
person's judgement. One undivided list read as ten broken things when six were.
Its per-resource tables carry the evidence and the one-line `fix:` in each
finding's `Finding` cell, beside the verdict.

`--no-remediations` drops all of it in both layouts: the `fix:` lines and
`Rules` from the line report, both sections from `--details`.

Width comes from the terminal itself when stdout is one; an exported `COLUMNS`
overrides it, and pipes, redirects and `--output-file` get 80. Whatever the
source, it is clamped to 60–160. The ceiling constrains prose: a flexed table
column never grows past its content, so on a wide terminal a table stops at
its natural width — wide enough for the longest control title on one line —
rather than sprawling. Nothing ever exceeds that width — a border that wraps
stops reading as a border — so a long settings path is broken at the column
edge rather than pushing the frame out of true.

Colour is reinforcement only, so nothing is lost when output is piped,
redirected, or run with `NO_COLOR` set. `--details=<value>` filters the table;
anything more surgical is a job for the JSON:

```bash
bitbucket-bench scan -o json | jq '.findings[] | select(.status == "FAIL")'
bitbucket-bench scan -o json | jq -r '.findings[] | select(.status == "MANUAL") | .checkId'
bitbucket-bench scan 2>&1 >/dev/null                  # what the scan itself had to say
```

Lines on **stderr** — the request trace, the line explaining an exit code — still
carry a fixed-width `[INFO]`/`[WARN]`/`[FAIL]`/`[PASS]` tag, because they are read
interleaved with other programs' output and have no table to belong to. The
report on stdout does not.

`--max-resources` caps how many resources get a `--details` section of their
own (`0`, the default, gives every one of them a section); the line report
draws no per-resource sections, so it is only accepted alongside `--details`.
When it bites, the report says how many it withheld and points at the format
that carries them all — `3 more resources with findings not shown
(--max-resources 1); use -o json for all of them.` It affects only this format:
`json` and `junit` always carry the full set, and `sarif` everything up to code
scanning's 5,000-result cap.

Passing and not-applicable controls are summarised but not listed, since a
report is a list of things to do. `--show-passed` lists them too, which is what
you want when the question is "what does this instance already get right".

Colour is used only when stdout is a terminal, and honours `NO_COLOR`;
`--no-color` turns it off explicitly, which is the one to reach for when a
terminal is being captured by something that keeps the escapes.

**`json`** — the full report: every finding, its evidence, why the control exists,
its remediation, and the score breakdown. Reports are written `0600`, like
snapshots: a rendered report is the same map of an instance's weak points.

**`sarif`** — SARIF 2.1.0 for GitHub code scanning and other SARIF consumers.
Failures and manual reviews are emitted; passes and N/A are omitted as
non-actionable. Code scanning drops any result without a `physicalLocation` —
the upload succeeds and the Security tab stays empty — so each result carries
one: a stable path naming the platform, the instance and the repository
(`bitbucket-dc/bitbucket.example.com/PLAT/payments-api`). It does not need to
exist in the repository you upload to; the alert shows where the finding is,
and the `logicalLocation` beside it names the repository. Beyond that:

- A `MANUAL` result points at a rule of its own (`CIS-1.3.5/manual`) with no
  `security-severity`, so "a person needs to check this" never displays as a
  High alert.
- `automationDetails.id` is the bench and the instance's host, so two instances
  uploading to one repository do not close each other's alerts.
- Results are capped at code scanning's 5,000, most severe first, with a
  notification saying how many were withheld; a control no API can answer is
  one result, not one per repository.
- Scan warnings travel as invocation notifications, and a scan that could not
  list a project, or evaluated no repository, reports
  `executionSuccessful: false`, so a partial scan is never mistaken for a
  clean one. An accepted finding carries a SARIF
  `suppression`, which code scanning shows as dismissed.

**`junit`** — JUnit XML, for the CI systems that draw test results natively:
Jenkins' JUnit publisher, Azure Pipelines' `PublishTestResults`, GitLab's test
report. One test suite per control and one test case per resource it was
evaluated on — each repository, and `instance` for the instance-level
controls — so the test view groups the way the report does. An unaccepted `FAIL` is a failure,
typed with its severity and carrying the evidence and the fix; `MANUAL`, `NA`
and accepted failures are skipped with the reason, so the totals add up to
every finding. A scan that could not list a project, or evaluated no
repository at all, adds a failing case of its own — the repositories it missed
have no test case to fail.

**Accepted findings** — see [Exceptions](#exceptions) — appear in every
format: under their own heading in the table report, with a `waiver` in the
JSON, as a suppression in SARIF and as skipped in JUnit.

Output is English only. The documentation is bilingual and the maintainers are
not all native English speakers, so this is a decision rather than an oversight:
a control's verdict text is generated by its rule, which means a second language
is not a table of strings but a second copy of the message construction inside
all twenty of them. Half a translation — titles rendered in one language over
findings written in another — reads worse than none.

---

## Configuration

Every threshold a reasonable person might disagree with is configurable, and
the settings that describe the deployment rather than any one run — exit
thresholds, transport, concurrency, progress — live here too rather than in
flags. Nothing is hard-coded in a policy.

```bash
bitbucket-bench init            # writes a commented bitbucket-bench.yaml with every key
bitbucket-bench scan            # finds it in the working directory on its own
```

Discovery order: `--config` when given, else `bitbucket-bench.yaml` (or
`.bitbucket-bench.yaml`) in the working directory — the project's file, the one a
repository commits for CI — else `config.yaml` under the user config directory
(`BITBUCKET_BENCH_CONFIG_DIR`, or the platform default). A discovered file is named
on stderr, because a scan whose thresholds quietly came from a file is a scan
whose exit code makes no sense. `init` refuses to overwrite an existing file.

> **Upgrading from a `v0.1.0-rc` build.** These names changed with the tool's
> own: `scm-bench.yaml` is now `bitbucket-bench.yaml`, `SCM_BENCH_CONFIG_DIR` is
> now `BITBUCKET_BENCH_CONFIG_DIR`, and the user config directory moved from
> `<config>/scm-bench` to `<config>/bitbucket-bench`. There is no fallback to the
> old names — rename the file, or pass `--config` at it. The verdicts that
> changed are in [Upgrading from a v0.1.0-rc build](#upgrading-from-a-v010-rc-build).

For a one-off, `--set` overrides any config key without touching a file —
`--set` beats the file, the file beats the defaults:

```bash
bitbucket-bench scan --set scan.failOn=none          # just this run
bitbucket-bench scan --set thresholds.minApprovers=1 --set scan.concurrency=2
```

The value reads as YAML, so numbers, booleans, durations (`30s`) and flow
sequences (`exclude=[CIS-1.1.8]`) all work, and an unknown key refuses the
scan exactly as it would in the file.

See [`examples/config.yaml`](examples/config.yaml) for the annotated full set. The
most commonly adjusted:

```yaml
scan:
  failOn: high           # exit 1 at or above this severity: high, medium, low, none
  maxManual: -1          # exit 1 when this % of controls went unevaluated; -1 off
  concurrency: 8         # parallel repository fetches, across every project
  timeout: 30s           # per-request bound; maxDuration bounds the whole scan
  caFile: /etc/ssl/certs/corp-root-ca.pem   # an internal CA, instead of insecure
  cache: true            # keep each scan's snapshot for --last; false keeps it off disk
  allowIncomplete: false # accept a scan that could not list every project

thresholds:
  minApprovers: 2        # CIS-1.1.3
  staleBranchDays: 90    # CIS-1.1.8
  minOrgAdmins: 2        # CIS-1.3.3
  maxOrgAdmins: 5
  inactiveUserDays: 90   # CIS-1.3.1
  maxBypassPrincipals: 0 # CIS-1.1.15/16/17; -1 turns the bypass check off

# The service accounts a branch restriction exemption is expected on, so the
# threshold above can stay at zero for people.
allowedBypassPrincipals: [release-bot]

# Full keys of hooks that verify commit signatures. The default is the hook
# Bitbucket bundles since 8.13; add a Marketplace add-on's key on an older one,
# and keep the bundled key in the list if you do.
signatureHookKeys:
  - com.atlassian.bitbucket.server.bitbucket-bundled-hooks:verify-commit-signature-hook

# For instances that intentionally publish code.
allowPublicRepositories: false

exclude: [CIS-1.1.13]    # or `include:` to run only a subset
```

A config file only needs to state what it changes — everything absent keeps its
default. Sequences are the exception: YAML replaces a list wholesale, so setting
`signatureHookKeys` replaces the whole list rather than appending to it.

An unrecognised key is an error, not a shrug. `minApprover` for `minApprovers`
parses perfectly well, changes nothing, and yields a report the reader believes
was evaluated at their threshold — so the scan refuses to start instead. The
same applies to `include`, `exclude` and exception entries that name no
control, to a negative threshold, to a blank entry in any list, and to a hook
list entry that is not a full `plugin-key:module-key` — a substring there once
matched "Verify Committer", a hook that verifies no signature at all.

`permissionRank` is the one field most people never touch and should know
exists: it is how Bitbucket's permission names compare to one another, and
`maxDefaultPermission` is checked against it. A permission your Bitbucket
version reports that the table has never heard of makes CIS-1.3.8 report
`MANUAL` rather than guessing where it ranks. Unlike the sequences, it is a map
and is merged rather than replaced, so naming one permission leaves the rest
alone.

### Exceptions

Every organisation has a finding it has decided to live with for a while — a
legacy repository whose release tooling needs merge commits until a migration
lands. Without a way to say so, the choice is a pipeline that is always red, or
`failOn: none`, after which the gate catches nothing. An exception says so,
with a reason and an end date:

```yaml
exceptions:
  - control: CIS-1.1.13
    resources: [PLAT/legacy-billing, PLAT/legacy-*]   # globs; "instance" for instance controls
    reason: Release tooling needs merge commits until the migration lands
    owner: platform-team@example.com                  # optional
    expires: 2027-03-31                                # required
```

An accepted finding is **still reported, keeps its status, and is still
counted in the score** — the score describes the instance, and accepting a finding does
not change the instance. What it stops doing is failing the run on
`scan.failOn`, or, for a `MANUAL` finding somebody has reviewed by hand,
counting against `scan.maxManual`. The report lists it under *Accepted by
exceptions* with the reason, the owner and the date.

The reason and the expiry are required: an exception without them is how
accepted risk becomes forgotten risk. The day after `expires`, the exception
lapses and its findings fail the run again; an exception that accepts nothing
— the finding was fixed, the repository renamed — is reported too, so the list
cannot rot quietly. Both are printed on stderr whatever the verbosity.

---

## In CI

The thresholds live in a `bitbucket-bench.yaml` committed next to the pipeline,
so the pipeline and a laptop read the same file and disagree about nothing:

```yaml
# bitbucket-bench.yaml
scan:
  failOn: high
  maxManual: 40
```

**GitHub Actions** — upload the SARIF to code scanning. The scan exits `1`
when it finds something, which is the point, but that would end the job before
the upload; the failure is deferred to the last step. The upload needs
`security-events: write` in the job's `permissions` — and, in a private
repository, `actions: read` and `contents: read` too.

```yaml
- name: Audit Bitbucket
  id: audit
  continue-on-error: true
  run: |
    bitbucket-bench scan \
      --url "${{ vars.BITBUCKET_URL }}" \
      --token "${{ secrets.BITBUCKET_TOKEN }}" \
      -o sarif --output-file bitbucket-bench.sarif

- name: Upload to code scanning
  if: always()
  uses: github/codeql-action/upload-sarif@v4
  with:
    sarif_file: bitbucket-bench.sarif

- name: Fail the job if the audit did
  if: steps.audit.outcome == 'failure'
  run: exit 1
```

**Jenkins** — the JUnit publisher draws the result in the build's test view:

```groovy
stage('Audit Bitbucket') {
  steps {
    withCredentials([string(credentialsId: 'bitbucket-bench-token', variable: 'BITBUCKET_TOKEN')]) {
      sh '''
        bitbucket-bench scan --url https://bitbucket.example.com \
          -o junit --output-file bitbucket-bench.xml
      '''
    }
  }
  post {
    always {
      // Draws the report; the scan's exit code has already decided the build.
      junit testResults: 'bitbucket-bench.xml', allowEmptyResults: true, skipMarkingBuildUnstable: true
    }
  }
}
```

**Azure Pipelines** — the same file, through `PublishTestResults`:

```yaml
- script: |
    bitbucket-bench scan --url "$(BITBUCKET_URL)" -o junit --output-file bitbucket-bench.xml
  displayName: Audit Bitbucket
  env:
    BITBUCKET_TOKEN: $(BITBUCKET_TOKEN)
- task: PublishTestResults@2
  condition: succeededOrFailed()
  inputs:
    testResultsFormat: JUnit
    testResultsFiles: bitbucket-bench.xml
    failTaskOnFailedTests: false   # the scan's exit code is the gate
```

In every recipe the scan's exit code decides the build and the report only
draws it. A test report cannot carry the gate: JUnit has no notion of
severity, so every unaccepted `FAIL` is a failing test — `LOW` included —
and a publisher left to fail the build on failing tests gates on something
stricter than `failOn`. And a scan whose exit code is thrown away lets a
breached `maxManual` or `failUnder` through — as a yellow build, since
Jenkins' `junit` step marks failing tests `UNSTABLE` rather than failed, or as
a green one when nothing happened to fail as a test.

Exit codes:

| Code | Meaning |
|---|---|
| `0` | The scan ran and breached no threshold |
| `1` | The scan ran and breached one |
| `2` | The scan could not complete — or cannot vouch for what it covered |

Three config settings drive exit `1`, and they answer different questions:

| Setting | Asks |
|---|---|
| `scan.failOn` | Are there failures this severe? `high` (default), `medium`, `low`, `none` |
| `scan.failUnder` | Is the score acceptable? A number 0-100; `0` disables |
| `scan.maxManual` | Did the scan see enough to have an opinion? A percentage; `-1` disables |

Start at `failOn: high` and tighten once the first round of findings is
cleared; [exceptions](#exceptions) are for the findings that will not be.

`scan.maxManual` is the one worth setting early, and the least obvious.
Controls that could not be evaluated are excluded from the score rather than
counted against it — right for any single control, and misleading in
aggregate, because it shrinks the denominator. A token that has lost a
permission can therefore score *higher* than a working one: on the bundled
sample, blanking every readable field takes the score from 52 to 77.
`scan.failUnder` cannot catch that. `scan.maxManual` can.

Exit `2` also covers two scans that ran but cannot vouch for their coverage:
one that evaluated no repository — a `--project` the token cannot read, a
token that sees nothing — and one that could not list some project's
repositories, which are then missing from the report with no finding to say
so. The report is still written; `scan.allowIncomplete: true` accepts the
second if you mean to. A `--repository` that names nothing is an error too,
rather than a scan of zero repositories that passes.

### Splitting capture from evaluation

The snapshot is a self-contained artifact, which lets the credential-holding step
and the policy-evaluating step be different steps, on different machines, at
different times:

```bash
# On a runner that can reach Bitbucket and holds the token
bitbucket-bench scan --snapshot-out snapshot.json -o json --set scan.failOn=none

# Anywhere, later — no credentials, no network; the default failOn: high applies
bitbucket-bench scan --snapshot-in snapshot.json -o sarif
```

Re-running policies over an archived snapshot also shows how a decision would have
changed under new thresholds, without touching the instance again.

Snapshots are written `0600`: they are a precise map of an instance's weak points.
So are reports, and so is the saved `instance.yaml` — that one holds a live
credential, the strongest version of the same reason. That is a Unix mode, and
it is enforced on Unix-like systems only — Windows has no equivalent bit, so Go
maps the mode to the read-only attribute and the file's actual access control
comes from the ACL it inherits from its directory. On Windows, put snapshots
and reports somewhere already restricted, and think twice before saving the
token there at all.

### Asking the follow-up without another scan

The overview usually raises the next question — *which* repositories fail
CIS-1.1.3? — and answering it should not cost the instance another scan. Every
network scan therefore leaves its snapshot behind automatically (`0600`, one
file per instance host, in `cache/` under the user config directory), and
`--last` re-renders the most recent one:

```bash
bitbucket-bench scan                     # the overview; the snapshot is kept on the way out
bitbucket-bench scan --last --details    # expand it, without touching the instance
bitbucket-bench scan --last -o json      # or re-ask in another format
```

A `--last` run says on stderr which instance the snapshot came from and how
old it is — with a warning past a day, when treating it as current state
becomes a guess — and applies exit thresholds like any other run. Flags that
shape a fresh capture (`--url`, `--project`, `--snapshot-in`, …) are refused
alongside it, for the usual reason: the report would look exactly like the
scan they describe and not be it. The demo never populates the cache, so
`--last` cannot pass the bundled example off as your instance.

The cache is the same map of weak points the report is, kept under the same
`0600` (its directory `0700`). If it should not exist at all, `scan.cache:
false` in the config keeps every future snapshot off disk, and deleting the
cache directory forgets what is already there; `--snapshot-out` remains the
explicit form, for choosing where a snapshot lands.

### Catching regressions

A score is a trend line, and a trend needs two points. `diff` compares two
snapshots and reports what moved:

```bash
bitbucket-bench diff last-week.json today.json
```

```
bitbucket-bench diff  https://bitbucket.example.com  ·  2026-01-08 09:00:00 UTC → 2026-01-15 09:00:00 UTC
SCORE  56 → 52   (-4)
       weighted 32/57 → 30/57

Regressed (1)

┌────────────┬──────────┬─────────────────────┬─────────────┬──────────────────────────────────────┐
│  Control   │ Severity │      Resource       │   Change    │                Detail                │
├────────────┼──────────┼─────────────────────┼─────────────┼──────────────────────────────────────┤
│ CIS-1.1.17 │ MEDIUM   │ PLAT/legacy-billing │ PASS → FAIL │ Deletion of master is restricted,    │
│            │          │                     │             │ but dana, erin, frank, grace and 1   │
│            │          │                     │             │ more can still delete it, taking the │
│            │          │                     │             │ branch and its protections with      │
│            │          │                     │             │ them.                                │
└────────────┴──────────┴─────────────────────┴─────────────┴──────────────────────────────────────┘

... one table per kind of change: New failures, Fixed, Other changes, Gone ...

How to fix the regressions

  CIS-1.1.17  Repository settings -> Branch permissions -> Add restriction: select the default
              branch and enable "Prevent deletion". Then review the restriction's exempt users ...
```

`diff` stays tabular, drawing with the same renderer `scan --details` uses, so
the two subcommands read as one program. A comparison is a grid by nature —
every row is the same four facts about a different control — which is the one
place a table beats a line. `Gone` is one entry per resource rather than one per control:
deleting a repository is one fact about the repository, and reporting it twenty
times buried the regressions this command exists to surface — it shows a `-` in
the Control column, since a table cannot drop a column for one row.

It takes the same output flags as `scan` — `-o table|json`, `--output-file`,
`--no-color` — plus `--fail-on-regression` (on by default) and
`--allow-other-instance`. SARIF is not offered: a comparison is not a set of
findings.

**Both snapshots are evaluated by the running build with the running
configuration** before being compared. Diffing two already-rendered reports
would be easier, but the difference would then include whatever changed about
the tool or its thresholds between the runs — which is exactly what a regression
check must not be confused by.

Only **`PASS → FAIL`** is a regression, and only that drives the exit code:

| Category | Meaning | Exits 1 |
|---|---|---|
| `REGRESSED` | Was satisfied, no longer is | yes |
| `NEW FAILURES` | Failing on a repository that did not exist before | no |
| `FIXED` | `FAIL → PASS` | no |
| `OTHER CHANGES` | Anything involving `MANUAL` | no |
| `GONE` | Resource no longer present | no |

A new repository arriving with failures is not a regression — nothing got worse,
there is simply more instance. Anything involving `MANUAL` is not one either:
`PASS → MANUAL` means the tool stopped being able to see the setting, usually
because a token lost a permission or an add-on was removed, and failing a
pipeline for that would blame the instance for the scan's own blind spot.

`--fail-on-regression=false` makes it report-only. Exit codes otherwise match
`scan`: `0` clean, `1` regression, `2` the comparison could not run.

In CI, keep the previous snapshot as an artifact and compare against it:

```bash
bitbucket-bench scan --snapshot-out today.json -o json --set scan.failOn=none
bitbucket-bench diff baseline.json today.json
```

Comparing snapshots from two different instances is refused unless
`--allow-other-instance` says it is deliberate: every repository would read as
both departed and arrived, which looks like a result and is not.

---

## How it works

```
Bitbucket REST  ──►  fetcher  ──►  snapshot.json  ──►  Rego policies  ──►  report
                  (Go, GET only)   (normalized)      (one per control)   table/json/sarif/junit
```

The split is strict, and it is the reason the tool is maintainable:

- **The fetcher never decides anything.** It normalizes, and it records what it
  could not read.
- **Policies never make HTTP calls.** They read one JSON document and return a
  verdict.

The package layout, the policy contract, how to add a control, how to run the
two test suites and how to cut a release are all in
[CONTRIBUTING.md](CONTRIBUTING.md).

---

## The specification it implements

The shape of everything above — the four statuses, the rule that an unevaluable
control reports `MANUAL`, the `metadata.json` fields, the scoring formula, the
snapshot schema, the SARIF fingerprint — is specified in
[scm-bench](https://github.com/scm-bench/scm-bench), the family's umbrella
repository. Nothing is imported from it at build time; it is a specification, not
a library, and this repository stays self-contained.

| Document | What it fixes |
| --- | --- |
| [bench contract](https://github.com/scm-bench/scm-bench/blob/main/docs/bench-contract.md) | The parts every bench shares, whatever it audits. |
| [SCM snapshot schema](https://github.com/scm-bench/scm-bench/blob/main/docs/scm-snapshot.md) | The `snapshot.json` shape, shared with the other benches that audit source control. |
| [config conventions](https://github.com/scm-bench/scm-bench/blob/main/docs/config-conventions.md) | Where the config file is found and how its keys merge. |

Reading them is optional to *use* this tool and worth it before *changing* it:
a verdict here has to mean the same as a verdict from any other bench, and that
is where what it means is written down.

---

## Tested against

The fetcher's unit tests prove it agrees with a stand-in Bitbucket; they
cannot prove the stand-in agrees with Bitbucket. [`hack/e2e`](hack/e2e)
closes that gap: it boots a real Bitbucket Data Center in Docker with one of
Atlassian's timebomb licenses, seeds four projects and thirteen repositories
with one deliberate deviation each — exempted restrictions, the Verify
Committer hook, a suffix pattern, a model branch, two deploy keys, an archived
repository, a default branch that was never pushed — and scans it with three
tokens: an admin-capable one, a read-only one, and a least-privilege user's.
The expected verdicts are written down from what the fixture *is*, not from
what the tool printed, and the suite fails on any difference and on any scan
that is not read-only.

| Bitbucket Data Center | Result |
|---|---|
| 10.4.1 | every verdict as expected, for all three tokens |

Every finding in [Upgrading from a v0.1.0-rc build](#upgrading-from-a-v010-rc-build) was found this way.
Running the suite against another version — set
`IMAGE=atlassian/bitbucket:<tag>` and follow [hack/e2e](hack/e2e/README.md):
a license file, then `up.sh`, `seed.sh` and `verify.sh` — and reporting what
differed is the most useful contribution there is.

---

## Roadmap

**Next** — run the end-to-end suite against the 8.x and 9.x long-term-support
releases and record the results above; personal repositories (`~user`) as an
opt-in, since `/projects` does not list them; CIS-1.2.2 from who holds Project
Creator, now that permission resolution needs no admin token; default
reviewers as a partial CIS-1.1.6 signal.

**Elsewhere** — other platforms are their own repositories, not fetchers added
here: [azure-devops-bench](https://github.com/scm-bench/azure-devops-bench) and
[jenkins-bench](https://github.com/scm-bench/jenkins-bench). The snapshot
schema is platform-neutral and controls declare which platforms they apply to,
so a control written here can be inherited by another bench in the SCM domain
rather than rewritten.

---

## License

Apache 2.0. See [LICENSE](LICENSE).
