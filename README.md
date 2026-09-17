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

**v0.1** covers the platform with the least tooling in this space. 15 controls are
evaluated automatically; 5 more are carried as documented manual checks so the
mapping is complete rather than quietly partial.

This is the Bitbucket bench of [scm-bench](https://github.com/scm-bench/scm-bench),
a family of tools that audit one platform each and report in the same shape. It is
the reference implementation of the family's
[bench contract](https://github.com/scm-bench/scm-bench/blob/main/docs/bench-contract.md).

[简体中文](README.zh-CN.md) · [Contributing](CONTRIBUTING.md) · [Security](SECURITY.md) · [Spec](#the-specification-it-implements)

---

## The one design decision worth knowing

**A control that could not be evaluated reports `MANUAL`, never `PASS` or `FAIL`.**

If your token cannot read the admin API, if the required-builds add-on is not
installed, if Bitbucket does not report last-authentication timestamps — the tool
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
bitbucket-bench scan -o sarif --output-file report.sarif
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
[INFO]   GET  /projects/MVCC                                  200   21ms
[INFO]   GET  /projects/MVCC/repos                            200   22ms
[INFO]   GET  /admin/users?start=100                          200  380ms
[INFO]   GET  /admin/groups/more-members?context=developers   200  188ms
[WARN] global user permissions are not readable (401); instance administrator rules will report MANUAL
[INFO]
[INFO]   MVCC/service-a
[INFO]     GET  …/default-branch                              200   31ms
[INFO]     GET  …/settings/pull-requests                      200   26ms
[INFO]     GET  …/settings/hooks                              404   23ms
[INFO]
[INFO] ✓ 59 requests · 59 GET · 0 writes · read-only
```

The query string is shown when it is what tells two requests apart — a page
offset, or which group is being expanded. Without it, paging a user directory
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

| Access | Enables |
|---|---|
| **Repository read** | All 14 repository-scope controls |
| **Admin read** (additionally) | Instance administrator counts (CIS-1.3.3) and dormant accounts (CIS-1.3.1) |

A plain read-only token is enough to get value. Without admin read, the two
instance-scope controls report `MANUAL` and the scan continues normally — it does
not fail.

An HTTP access token (`--token`, `BITBUCKET_TOKEN`) is the expected credential.
Instances with no token support take basic auth instead (`--username` /
`--password`, or `BITBUCKET_USERNAME` / `BITBUCKET_PASSWORD`); give one or the
other, not both. What you type on the command line beats what the environment
supplies, so a stale `BITBUCKET_TOKEN` in a shell profile does not silently
override the credentials you just entered.

Credentials embedded in the URL — `https://user:pw@bitbucket.example.com` — are
not used and are stripped before the URL reaches a snapshot or a report.

A credential the instance *rejects* is a different matter: that is checked before
the scan starts and exits `2`. Treating it like a missing permission would turn a
mistyped token into a full report of `MANUAL` with a score of 0 — which looks
like an audit result rather than a typo.

bitbucket-bench issues **only `GET` requests**. This is enforced by a test, not just by
convention.

### Transport

That token can read every repository on the instance, so it is not put on the
wire in the clear. An `http://` URL is refused before the first request — unlike
the misconfigurations this tool reports, a leaked credential cannot be undone
once it is out. `scan.allowPlaintext` in the config overrides it for a
genuinely trusted network; loopback addresses are exempt and need no setting.

`scan.insecure` skips certificate verification, for an instance behind a
private CA you cannot install. Both are configuration rather than flags on
purpose: weakening transport security should be a decision written into a
file someone can review, not a habit of the fingers.

Both leave a line in the report's scan warnings and in any snapshot captured
that way. A scan taken over cleartext, or without verifying who answered, is not
the same evidence as one that was not.

---

## Coverage

15 controls evaluated automatically:

| CIS | Control | Severity | Decided from |
|---|---|---|---|
| 1.1.3 | Two approvals required to merge | HIGH | `requiredApprovers` |
| 1.1.4 | Approvals reset when the branch is updated | MEDIUM | `unapproveOnUpdate` |
| 1.1.8 | Abandoned branches are pruned | LOW | Branch tips older than 90 days |
| 1.1.9 | CI must pass before merge | HIGH | Required builds, or `requiredSuccessfulBuilds` |
| 1.1.11 | Open tasks block the merge | LOW | `requiredAllTasksComplete` |
| 1.1.12 | Commit signatures are verified | MEDIUM | An enabled signature-verification hook |
| 1.1.13 | Linear history is required | LOW | Enabled merge strategies |
| 1.1.15 | No direct pushes to the default branch | HIGH | `pull-request-only` / `read-only` restriction, and who is exempt from it |
| 1.1.16 | No force pushes | HIGH | `fast-forward-only` restriction, and who is exempt from it |
| 1.1.17 | No branch deletion | MEDIUM | `no-deletes` restriction, and who is exempt from it |
| 1.2.1 | A security policy is published | LOW | `SECURITY.md` on the default branch |
| 1.3.1 | Dormant accounts are reviewed | MEDIUM | Last authentication vs. repository access |
| 1.3.3 | Instance administrators are bounded (2–5) | HIGH | Global permissions, groups expanded |
| 1.3.7 | Each repository has ≥2 administrators | LOW | Repository + project grants, groups expanded |
| 1.3.8 | Default repository access is restricted | MEDIUM | Public flag + project default permission |

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

> **Upgrading from v0.1.** This changes verdicts: a restriction with wide
> exemptions reported `PASS` before and reports `FAIL` now. That is the fix,
> not a regression. `thresholds.maxBypassPrincipals: -1` restores the old
> behaviour if you need to stage the cleanup.
>
> `diff` is not affected: it re-evaluates both snapshots with the running
> build, so a baseline captured under v0.1 is judged by the same rule as
> today's scan rather than producing a wave of false regressions. Snapshots
> captured before this release carry no resolved exemption sets, so these
> three controls report `MANUAL` on them rather than guessing.

5 controls carried as documented manual checks — they are reported, explained, and
excluded from the score:

| CIS | Control | Why it is not automated |
|---|---|---|
| 1.1.6 | Code owners | Bitbucket DC has no CODEOWNERS. Default reviewers are advisory unless paired with an approval count, so mapping them here would overstate enforcement. |
| 1.2.2 | Repository creation is limited | Needs the global Project Creator permission read together with group membership and per-project grants; "sufficiently limited" is deployment-specific. Planned for v0.2. |
| 1.2.3 | Repository deletion is limited | Bitbucket does not expose deletion as its own permission — any verdict would restate CIS-1.3.3 and CIS-1.3.7. |
| 1.3.5 | MFA is enforced | Authentication is delegated to an external IdP (SAML/Crowd/LDAP). Bitbucket's API exposes nothing about factors, so a verdict from Bitbucket data would be invented. |
| 1.3.9 | Organization is verified | A hosted-SaaS concept with no self-hosted equivalent. Reported `NA`. |

```bash
bitbucket-bench list-checks          # all controls, with severity and scope
bitbucket-bench list-checks --json   # full metadata, including remediation text
```

---

## Scoring

```
score = Σ weight(passed) / Σ weight(passed + failed) × 100
```

with `HIGH = 3`, `MEDIUM = 2`, `LOW = 1`. `MANUAL` and `NA` are in neither sum.

The table output prints the arithmetic (`weighted 29/55 (HIGH=3, MEDIUM=2,
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
instance  CIS-1.3.1 MEDIUM: 1 active user(s) with repository access have not authenticated for 90
    days or more: dana.
    fix: Deactivate each listed account at Administration -> Users.
    · dana

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

  fix: rerun with a token that has administrator read access, so the scan can evaluate what it could
       not see.

Rules

  CIS-1.1.3   https://confluence.atlassian.com/bitbucketserver/checks-for-merging-pull-requests-776640039.html
  CIS-1.1.17  https://confluence.atlassian.com/bitbucketserver/using-branch-permissions-776639807.html

... one line per control in the report, with the vendor's documentation page ...

Details: rerun with --details for per-resource findings and full remediation steps, or
--details=<resource|control>[,...] to filter; -o json for the full report.

SCORE 53/100   15 passed  13 failed  19 manual  1 n/a
      13 controls failed
      weighted 29/55 (HIGH=3, MEDIUM=2, LOW=1; manual and n/a excluded)
      scored 28 of 47 findings (59%); 19 could not be evaluated
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
it and the closing exit line count *controls*, so "13 failed" and "13 controls
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
`json` and `sarif` always carry the full set.

Passing and not-applicable controls are summarised but not listed, since a
report is a list of things to do. `--show-passed` lists them too, which is what
you want when the question is "what does this instance already get right".

Colour is used only when stdout is a terminal, and honours `NO_COLOR`;
`--no-color` turns it off explicitly, which is the one to reach for when a
terminal is being captured by something that keeps the escapes.

**`json`** — the full report: every finding, its evidence, why the control exists,
its remediation, and the score breakdown. Reports are written `0600`, like
snapshots: a rendered report is the same map of an instance's weak points.

**`sarif`** — SARIF 2.1.0 for CI ingestion. Failures and manual reviews are
emitted; passes and N/A are omitted as non-actionable. Findings are configuration
facts rather than lines of source, so each result carries a `logicalLocation`
naming the repository instead of a `physicalLocation` pointing into a file that
does not exist. Scan warnings travel as invocation notifications, so a partial
scan is never mistaken for a clean one.

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
> old names — rename the file, or pass `--config` at it.

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
  concurrency: 8         # parallel repository fetches
  timeout: 30s           # per-request bound; maxDuration bounds the whole scan
  cache: true            # keep each scan's snapshot for --last; false keeps it off disk

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

# Bitbucket ships no signature verification, so name the add-on you use.
signatureHookKeys: [signature, gpg, verify-commit]

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
same applies to `include`/`exclude` entries that name no control, to a negative
threshold, and to a blank entry in any list — a blank `signatureHookKeys` entry
is a substring of every hook name, which would report the first enabled hook,
whatever it does, as commit signature verification.

`permissionRank` is the one field most people never touch and should know
exists: it is how Bitbucket's permission names compare to one another, and
`maxDefaultPermission` is checked against it. A permission your Bitbucket
version reports that the table has never heard of makes CIS-1.3.8 report
`MANUAL` rather than guessing where it ranks. Unlike the sequences, it is a map
and is merged rather than replaced, so naming one permission leaves the rest
alone.

---

## In CI

```yaml
- name: Audit Bitbucket
  id: audit
  # The scan exits 1 when it finds something, which is the point — but that
  # would end the job before the report could be uploaded, so the failure is
  # deferred to the last step.
  continue-on-error: true
  run: |
    # bitbucket-bench.yaml, committed to this repository, carries the thresholds:
    #   scan: { failOn: high, maxManual: 40 }
    bitbucket-bench scan \
      --url "${{ vars.BITBUCKET_URL }}" \
      --token "${{ secrets.BITBUCKET_TOKEN }}" \
      --output sarif --output-file bitbucket-bench.sarif

- name: Upload to code scanning
  if: always()
  uses: github/codeql-action/upload-sarif@v3
  with:
    sarif_file: bitbucket-bench.sarif

- name: Fail the job if the audit did
  if: steps.audit.outcome == 'failure'
  run: exit 1
```

Exit codes:

| Code | Meaning |
|---|---|
| `0` | The scan ran and breached no threshold |
| `1` | The scan ran and breached one |
| `2` | The scan itself could not complete |

Three config settings drive exit `1`, and they answer different questions:

| Setting | Asks |
|---|---|
| `scan.failOn` | Are there failures this severe? `high` (default), `medium`, `low`, `none` |
| `scan.failUnder` | Is the score acceptable? A number 0-100; `0` disables |
| `scan.maxManual` | Did the scan see enough to have an opinion? A percentage; `-1` disables |

They are configuration rather than flags so the pipeline and the laptop read
the same committed file and disagree about nothing. Start at `failOn: high`
and tighten once the first round of findings is cleared.

`scan.maxManual` is the one worth setting early, and the least obvious.
Controls that could not be evaluated are excluded from the score rather than
counted against it — right for any single control, and misleading in
aggregate, because it shrinks the denominator. A token that has lost a
permission can therefore score *higher* than a working one: on the bundled
sample, blanking every readable field takes the score from 53 to 100.
`scan.failUnder` cannot catch that. `scan.maxManual` can.

A note on the SARIF, if you upload it: findings are configuration facts, not
lines of source, so each result carries a `logicalLocation` naming the
repository rather than a `physicalLocation` pointing into a file that does not
exist. GitHub code scanning uses `physicalLocation` to place an alert against
code, so alerts appear without a file attached. Verify the behaviour against
your own repository before relying on it; the `json` format is the better
choice if you are feeding a dashboard rather than code scanning.

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
SCORE  56 → 53   (-3)
       weighted 31/55 → 29/55

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
                  (Go, GET only)   (normalized)      (one per control)   table/json/sarif
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

## Roadmap

**v0.2** — CIS 1.2.2 (repository creation limits, once the Project Creator
interpretation is settled), default reviewers as a partial CIS-1.1.6 signal,
and per-project policy overrides.

**Elsewhere** — other platforms are their own repositories now, not fetchers
added here: [azure-devops-bench](https://github.com/scm-bench/azure-devops-bench)
next, then [jenkins-bench](https://github.com/scm-bench/jenkins-bench). The
snapshot schema is platform-neutral and controls declare which platforms they
apply to, so a control written here can be inherited by another bench in the SCM
domain rather than rewritten.

---

## License

Apache 2.0. See [LICENSE](LICENSE).
