<!--
  The banner lives in scm-bench/.github (brand/), which is also where the
  organization profile and the uploaded avatar draw from, so there is one copy
  rather than one per repository. The URLs are absolute for two reasons: a
  relative path cannot cross repositories, and README.md ships inside every
  release tarball, where a repository-relative image resolves to nothing.
-->
<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/scm-bench/.github/main/brand/banner-dark-1760x440.png">
    <source media="(prefers-color-scheme: light)" srcset="https://raw.githubusercontent.com/scm-bench/.github/main/brand/banner-light-1760x440.png">
    <img src="https://raw.githubusercontent.com/scm-bench/.github/main/brand/banner-light-1760x440.png" alt="scm-bench — audit source control against the CIS supply chain benchmark" width="880">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/scm-bench/scm-bench/actions/workflows/ci.yml"><img src="https://github.com/scm-bench/scm-bench/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/scm-bench/scm-bench/releases"><img src="https://img.shields.io/github/v/release/scm-bench/scm-bench?include_prereleases&sort=semver" alt="Release"></a>
  <a href="https://goreportcard.com/report/github.com/scm-bench/scm-bench"><img src="https://goreportcard.com/badge/github.com/scm-bench/scm-bench" alt="Go report card"></a>
  <a href="https://pkg.go.dev/github.com/scm-bench/scm-bench"><img src="https://pkg.go.dev/badge/github.com/scm-bench/scm-bench.svg" alt="Go reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue" alt="Apache 2.0"></a>
</p>

Audit a source control platform against the **Source Code** section of the
[CIS Software Supply Chain Security Guide](https://www.cisecurity.org/benchmark/software-supply-chain-security).

scm-bench captures a **read-only** snapshot of your instance, evaluates it against
policies written in Rego, and tells you what is misconfigured — along with the exact
settings path to fix it.

**v0.1 targets Bitbucket Data Center**, the platform with the least tooling in this
space. 15 controls are evaluated automatically; 5 more are carried as documented
manual checks so the mapping is complete rather than quietly partial.

[简体中文](README.zh-CN.md) · [Contributing](CONTRIBUTING.md) · [Security](SECURITY.md)

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

**Binary** — download from [releases](https://github.com/scm-bench/scm-bench/releases):

```bash
# The archive name carries the version, so resolve the latest tag first.
VERSION=$(curl -fsSL https://api.github.com/repos/scm-bench/scm-bench/releases/latest |
  sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')

curl -fsSL "https://github.com/scm-bench/scm-bench/releases/download/v${VERSION}/scm-bench_${VERSION}_linux_amd64.tar.gz" | tar xz
./scm-bench version
```

**Docker:**

```bash
docker run --rm ghcr.io/scm-bench/scm-bench:latest \
  scan --url https://bitbucket.example.com --token "$BITBUCKET_TOKEN"
```

**From source** (Go 1.25+; the build pins a patched toolchain and will fetch it):

```bash
go install github.com/scm-bench/scm-bench/cmd/scm-bench@latest
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
scm-bench scan

# Scan one project, or one repository
scm-bench scan --project PLAT
scm-bench scan --repository PLAT/payments-api

# Machine-readable output
scm-bench scan -o json  --output-file report.json
scm-bench scan -o sarif --output-file report.sarif
```

No instance handy? Every report format works against the bundled sample:

```bash
scm-bench scan --snapshot-in examples/snapshot.json
```

Repositories are fetched concurrently — `--concurrency` (default 8) bounds how
many at once, and lowering it is the polite response to an instance under load.
`--timeout` bounds a single request (default 30s); `--max-duration` bounds the
whole scan and is off unless set, because how long is too long depends entirely
on how big the instance is.

### Watching the scan

By default a scan shows one self-overwriting progress line and then the report.
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

`--progress` selects how much of this to show:

| Mode | Shows |
|---|---|
| `compact` (default) | one self-overwriting progress line, plus the closing line |
| `full` (implied by `--verbose`) | every request, plus the closing line |
| `off` | only the closing line |

Redirected or in CI, `full` falls back to `off` on its own: without a cursor to
move, a request per line is thousands of lines nobody asked for. The closing
line is still printed, because an account of what a token was used for belongs
in a CI log too.

`--verbose` turns on the request log and the fetcher's own narration. Passing
`--progress` explicitly overrides that, so `--verbose --progress off` gives the
narration without the requests.

`--max-duration` abandons a scan that runs too long, exiting `2`:

```bash
scm-bench scan --max-duration 20m
```

There is no default. How long is too long depends entirely on the size of the
instance, and a default guess would turn a legitimately long scan into a
failure. `--timeout` is a separate thing — it bounds one HTTP request, not the
whole scan.

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

scm-bench issues **only `GET` requests**. This is enforced by a test, not just by
convention.

### Transport

That token can read every repository on the instance, so it is not put on the
wire in the clear. An `http://` URL is refused before the first request — unlike
the misconfigurations this tool reports, a leaked credential cannot be undone
once it is out. `--allow-plaintext` overrides it for a genuinely trusted
network; loopback addresses are exempt and need no flag.

`--insecure` skips certificate verification, for an instance behind a private CA
you cannot install.

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
| 1.1.15 | No direct pushes to the default branch | HIGH | `pull-request-only` / `read-only` restriction |
| 1.1.16 | No force pushes | HIGH | `fast-forward-only` restriction |
| 1.1.17 | No branch deletion | MEDIUM | `no-deletes` restriction |
| 1.2.1 | A security policy is published | LOW | `SECURITY.md` on the default branch |
| 1.3.1 | Dormant accounts are reviewed | MEDIUM | Last authentication vs. repository access |
| 1.3.3 | Instance administrators are bounded (2–5) | HIGH | Global permissions, groups expanded |
| 1.3.7 | Each repository has ≥2 administrators | LOW | Repository + project grants, groups expanded |
| 1.3.8 | Default repository access is restricted | MEDIUM | Public flag + project default permission |

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
scm-bench list-checks          # all controls, with severity and scope
scm-bench list-checks --json   # full metadata, including remediation text
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

**`table`** (default) — the score first, then what the scan could not see, then
the findings grouped by control **and by verdict**, so one misconfiguration
repeated across fifty repositories reads as one problem rather than fifty:

```
[INFO] scm-bench example  ·  https://bitbucket.example.com  ·  2026-01-15 09:00:00 UTC

[INFO] SCORE 53/100   15 passed  13 failed  19 manual  1 n/a
[INFO]       weighted 29/55 (HIGH=3, MEDIUM=2, LOW=1; manual and n/a excluded)
[INFO]       scored 28 of 47 controls (59%); 19 could not be evaluated
[INFO]       failures by severity: HIGH 4 · MEDIUM 5 · LOW 4
[INFO]       most affected: PLAT/legacy-billing (12 failures)

[INFO] == Scan warnings ==
[WARN] group "contractors" could not be expanded (GET /api/1.0/admin/groups/more-members: 403 You
[INFO]   are not permitted to access this resource); administrator counts are lower bounds

[INFO] == Failed (13) ==
[FAIL] CIS-1.1.3   HIGH    Ensure any change to code receives approval of two strongly authenticated
[INFO]                     users
[INFO]     PLAT/legacy-billing  Pull requests require 0 approval(s); at least 2 independent
[INFO]                          approvals are needed.
[INFO]       · requiredApprovers = 0
[INFO]       fix: Set "Minimum approvals" to at least 2 at Repository settings -> Pull requests ->
[INFO]            Merge checks.

[INFO] == Not evaluated (13) ==
[INFO]     No verdict was reached for these. The scan could not read what the control asks about —
[INFO]     widen the token's access, check the scan warnings above, and run again.
[WARN] PLAT/vendor-mirror  13 controls could not be evaluated
[INFO]     CIS-1.1.3 CIS-1.1.4 CIS-1.1.8 CIS-1.1.9 CIS-1.1.11 CIS-1.1.12 CIS-1.1.13 CIS-1.1.15
[INFO]     CIS-1.1.16 CIS-1.1.17 CIS-1.2.1 CIS-1.3.7 CIS-1.3.8

[INFO] == Needs manual review (6) ==
[WARN] CIS-1.3.5  HIGH    Ensure multi-factor authentication is enforced for the organization
[INFO]     instance  Multi-factor authentication is enforced by the identity provider in front of ...

[INFO] == Remediations (17) ==
[INFO] CIS-1.1.3   Repository settings -> Pull requests -> Merge checks: ...
[INFO] CIS-1.3.5   Enforce MFA at the identity provider that fronts Bitbucket: ...
```

That block is the real output of `scm-bench scan --snapshot-in examples/snapshot.json`
at `COLUMNS=100`, abbreviated only where a line is marked `...`. The counts are of
findings — one control against one resource — which is why they add up to more
than the twenty controls `list-checks` reports.

The summary leads because a terminal is read from its top. The `scored N of M`
line is worth reading before the score above it: controls that could not be
evaluated are excluded from both sides of the fraction, which is right for any
single control and misleading in aggregate, since a token that can read very
little produces a high score from a small sample. `--max-manual` turns that into
a failed run rather than a good-looking one. `most affected` is the tally the
control-by-control grouping cannot show — it names who has to do the work.

Scan warnings come before the findings rather than after them, because they are
what decides how much of the report to believe: a 403 that cost the scan a whole
repository explains a run of unevaluated controls further down.

**`Not evaluated` and `Needs manual review` are both `MANUAL`, split by cause.**
The first is what *this run* could not read — one unreadable repository used to
produce one entry per control, thirteen ways of saying the same 403 — so it is
collapsed to one entry per resource, listing every control it cost. The second is
the controls *no API can answer* (`automated: false` in their metadata), which
need a person no matter how good your token is. Only the second gets remediation
text: a control the scan never saw is not known to be misconfigured, and printing
how to change its settings would say otherwise.

Lines wrap to `COLUMNS`, clamped to 60–100 and defaulting to 80 when it is not
exported. Continuations keep the tag column and hang under their first line.

Every line begins with a fixed-width tag, coloured on a terminal:

| Tag | Means |
|---|---|
| `[PASS]` | evaluated, and the setting is right |
| `[FAIL]` | evaluated, and the setting is wrong |
| `[WARN]` | needs a person: a control the tool could not decide (`MANUAL`), or something the scan could not read |
| `[INFO]` | structure, evidence, remediation, wrapped continuations, and controls that do not apply — never a verdict |

Four tags, the same four kube-bench uses. `MANUAL` is deliberately `[WARN]`
rather than `[INFO]`: "nobody has checked this" is the one thing this tool
refuses to let disappear, and it does not belong in the same column as a
section header.

Each finding carries a one-line `fix:` — the first move, and where — while the
full remediation paragraph lives in a section of its own at the end. That split
is what keeps the findings list scannable: the paragraphs name settings paths,
project-wide variants and config keys, and printing one under every control
turned a twenty-repository scan into prose you had to read to find the next
verdict. `--no-remediations` drops the section entirely; the one-line fixes stay.

The tag is the signal and the colour only reinforces it, so nothing is lost when
output is piped, redirected, or run with `NO_COLOR` set. That also makes the
obvious thing work:

```bash
scm-bench scan 2>&1 | grep '^\[FAIL\]'   # what is wrong with the instance
scm-bench scan 2>&1 | grep '^\[WARN\]'   # what the scan could not see
```

One control contributes exactly one verdict line however many repositories it
covers. The count leads because it is what decides the response: three
repositories is an oversight, three hundred is a policy that was never applied.
Repositories that failed the same control for a *different* reason stay in their
own group — those are different problems.

`--max-resources` sets how many names are listed before the tail is summarised
(default 5; `0` lists every one). It affects only this format: `json` and `sarif`
always carry the full set.

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

Every threshold a reasonable person might disagree with is configurable. Nothing
is hard-coded in a policy.

```bash
scm-bench scan --config scm-bench.yaml
```

See [`examples/config.yaml`](examples/config.yaml) for the annotated full set. The
most commonly adjusted:

```yaml
thresholds:
  minApprovers: 2        # CIS-1.1.3
  staleBranchDays: 90    # CIS-1.1.8
  minOrgAdmins: 2        # CIS-1.3.3
  maxOrgAdmins: 5
  inactiveUserDays: 90   # CIS-1.3.1

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
    scm-bench scan \
      --url "${{ vars.BITBUCKET_URL }}" \
      --token "${{ secrets.BITBUCKET_TOKEN }}" \
      --output sarif --output-file scm-bench.sarif \
      --fail-on high --max-manual 40

- name: Upload to code scanning
  if: always()
  uses: github/codeql-action/upload-sarif@v3
  with:
    sarif_file: scm-bench.sarif

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

Three flags drive exit `1`, and they answer different questions:

| Flag | Asks |
|---|---|
| `--fail-on` | Are there failures this severe? `high` (default), `medium`, `low`, `none` |
| `--fail-under` | Is the score acceptable? A number 0-100; `0` disables |
| `--max-manual` | Did the scan see enough to have an opinion? A percentage; `-1` disables |

Start at `--fail-on high` and tighten once the first round of findings is
cleared.

`--max-manual` is the one worth setting early, and the least obvious. Controls
that could not be evaluated are excluded from the score rather than counted
against it — right for any single control, and misleading in aggregate, because
it shrinks the denominator. A token that has lost a permission can therefore
score *higher* than a working one: on the bundled sample, blanking every
readable field takes the score from 53 to 100. `--fail-under` cannot catch
that. `--max-manual` can.

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
scm-bench scan --snapshot-out snapshot.json -o json --fail-on none

# Anywhere, later — no credentials, no network
scm-bench scan --snapshot-in snapshot.json -o sarif --fail-on high
```

Re-running policies over an archived snapshot also shows how a decision would have
changed under new thresholds, without touching the instance again.

Snapshots are written `0600`: they are a precise map of an instance's weak points.
So are reports. That is a Unix mode, and it is enforced on Unix-like systems
only — Windows has no equivalent bit, so Go maps the mode to the read-only
attribute and the file's actual access control comes from the ACL it inherits
from its directory. On Windows, put snapshots and reports somewhere already
restricted.

### Catching regressions

A score is a trend line, and a trend needs two points. `diff` compares two
snapshots and reports what moved:

```bash
scm-bench diff last-week.json today.json
```

```
[INFO] scm-bench diff  https://bitbucket.example.com  ·  2026-01-08 → 2026-01-15
[INFO] SCORE  53 → 31   (-22)
[INFO]        weighted 29/55 → 25/80

[INFO] REGRESSED (3)
[FAIL]   HIGH    CIS-1.1.15  PLAT/payments-api  PASS → FAIL
[INFO]       Anyone with write access can push directly to main, bypassing pull request review.

[INFO] NEW FAILURES (12)
[FAIL]   HIGH    CIS-1.1.3  PLAT/brand-new  FAIL

[INFO] FIXED (1)
[PASS]   HIGH    CIS-1.1.3  PLAT/legacy-billing  FAIL → PASS

[INFO] GONE (1)
[INFO]   HIGH    PLAT/retired-service  gone
[INFO]       no longer present; it was failing 5 controls of 14 evaluated
```

`diff` writes the same tag column as `scan`, so
`scm-bench diff a.json b.json | grep '^\[FAIL\]'` answers "what got worse".
`GONE` is one entry per resource rather than one per control: deleting a
repository is one fact about the repository, and reporting it twenty times
buried the regressions this command exists to surface.

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
scm-bench scan --snapshot-out today.json -o json --fail-on none
scm-bench diff baseline.json today.json
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

Some resolution is deliberately done in Go rather than Rego — glob semantics,
Bitbucket's branch model, group expansion. These are fiddly, version-dependent,
and are not policy. The fetcher resolves them and hands Rego a boolean:
`matchesDefaultBranch`. A rule asks *"is the default branch protected?"*, not
*"does `release/**` match `refs/heads/main`?"*

```
internal/
  scm/                  normalized snapshot types (the fetcher/policy contract)
    bitbucketdc/        REST client, fetcher, ref-matcher resolution
  checks/policies/      one directory per control: check.rego, check_test.rego,
                        metadata.json
  engine/               compiles the bundle once, evaluates, scores
  report/               table, json, sarif
  diff/                 compares two evaluations; backs `scm-bench diff`
  config/               thresholds handed to Rego as input.config
  cli/                  flags, exit codes, the scan trace
  console/              the tag column and colours both outputs share
```

### Adding a control

Create a directory under `internal/checks/policies/bitbucketdc/`. No Go changes:
the bundle is embedded and discovered at load time.

`check.rego` returns a single `result` document:

```rego
package scmbench.rules.cis_1_1_4

import rego.v1
import data.scmbench.lib

result := {
	"status": "MANUAL",
	"details": "Pull request merge checks could not be read.",
} if {
	not lib.available("pullRequestSettings")
} else := {
	"status": "PASS",
	"details": "Approvals are dismissed when the source branch is updated.",
} if {
	lib.pr_setting("unapproveOnUpdate", false) == true
} else := {
	"status": "FAIL",
	"details": "Approvals survive updates, so unreviewed code can be merged.",
	"evidence": ["unapproveOnUpdate = false"],
}
```

The `MANUAL` branch comes first on purpose. Deciding what the data says is only
sound once you have established that you have the data.

One rule when writing policies: **read every list through `lib.list`**, never
`object.get` directly. A nil Go slice marshals to JSON `null`, and `object.get`
only substitutes its default for an *absent* key — a key present with a null
value comes back as null, and passing that to `concat` or `sort` makes the rule
undefined, so the control reports nothing at all.
`TestZeroValuedSnapshotProducesAVerdictForEveryControl` guards this.

`check_test.rego` sits beside it and is not optional. Every control's PASS,
FAIL and MANUAL branches are covered, and CI holds the bundle at 100% — an
uncovered branch is a verdict nobody has ever seen the rule produce. Run them
with `make policy`.

`metadata.json` carries the ID, severity, scope, and — most importantly — the
remediation text, in two forms. `remediation` is the full paragraph; `fixSummary`
is its first move in one imperative line, which is what the findings list prints
beside each verdict. A test enforces that both name a concrete location — a
settings path, a file to add, or an explicit statement that nothing applies — and
that `fixSummary` stays under 100 characters. Vague remediation is worse than
none.

---

## Development

```bash
make check      # fmt, vet, race-enabled tests, Rego compile + policy tests
make policy     # just the Rego: compile, unit tests, coverage
make vuln       # govulncheck against what this code actually reaches
make build      # binary into bin/
make snapshot   # full release build locally, without publishing
```

`make check` needs [opa](https://www.openpolicyagent.org/docs/latest/#running-opa)
on your PATH for the policy half. Everything else is the Go toolchain.

Two test suites, because the project is written in two languages and `go test
-cover` cannot see Rego at all:

- **Go** covers the fetcher, the engine, the reporters and the CLI. The fetcher
  runs against a stand-in Bitbucket that exercises pagination, renamed
  endpoints, permission denials, and the cross-version field shapes where a
  merge check arrives as a number in one release and an object in another.
  Coverage is measured with `-coverpkg=./...`, since Go otherwise counts each
  package only from its own tests and reports a number for something nobody
  asked about.
- **Rego** covers the controls themselves, one `check_test.rego` per control,
  held at 100%. These are the tests that pin the reasoning: that an under-count
  from an incomplete administrator set is `MANUAL` while an over-count is a
  conclusive `FAIL`, that a branch whose age could not be read is not a fresh
  branch, and that the five controls with no automatable answer stay `MANUAL`
  rather than acquiring a plausible-looking one.

On top of both, the Go suite evaluates the entire bundle against hardened,
misconfigured, unreadable, and empty snapshots, asserting that every control
produces a verdict in each case — including that unreadable settings produce
`MANUAL` rather than a confident wrong answer.

---

## Releasing

Either push a tag:

```bash
git tag -a v0.1.0 -m "scm-bench v0.1.0" && git push origin v0.1.0
```

or run the **Release** workflow from the Actions tab and give it the tag to
create. The manual path needs no local checkout, and it validates the tag before
creating it — a non-canonical version is rejected rather than silently producing
a release nobody can install. Either way the workflow does the rest.

**Publish the draft goreleaser made. Never start a new release from the
Releases page.** goreleaser creates the draft and uploads every artifact into
it; a release created separately gets the notes and none of the files, which is
how `v0.1.0-rc.1` ended up existing twice, once with nothing to download.

Release notes are written by hand in that draft. goreleaser fills in the parts
that carry a version number — the install commands and the verification block —
and leaves the narrative to a person, which is the half worth writing.
Pressing **Generate release notes** adds GitHub's own list on top, categorised
by the labels in [`.github/release.yml`](.github/release.yml). That list covers
merged pull requests; commits pushed straight to `main` are not pull requests
and will not appear in it.

**Tags must have all three version components** — `v0.1.0`, never `v0.1`. Go
accepts `v0.1` as a semver *string* but does not treat it as canonical, so the
module system ignores such a tag and `go install ...@latest` will not find the
release. goreleaser does not catch this: it builds `v0.1` happily and produces
artifacts nobody can `go install`. The leading `v` is also required.

**Releases are created as drafts.** goreleaser builds and uploads every
artifact, then stops. Look at what it produced and publish by hand. This is
deliberate: a published release is effectively permanent, because module
proxies fetch and cache the tag, and deleting the GitHub release does not
un-publish the module version.

Signing needs nothing from whoever cuts the release. cosign signs `checksums.txt`
and the container manifests keylessly, using the workflow's OIDC token, and the
run records a SLSA provenance attestation for the archives. There is no key to
hold, so releasing does not depend on one person's laptop.

Worth checking on the draft before publishing: `checksums.txt.bundle` is
present, and the SBOMs are attached. A release
whose signing step was skipped still looks complete otherwise.

**Use a prerelease tag while something is unverified** — `v0.1.0-rc.1`. Go's
`@latest` resolves to the newest *release* version, so a prerelease has to be
requested by name and will not reach users who have not opted in. Iterate
`-rc.2`, `-rc.3` as needed without spending the `v0.1.0` number.

Docker has no equivalent of that rule, so it is enforced in the release config
instead: the `:latest` manifest carries `skip_push: auto` and is left alone on a
prerelease tag. A release candidate publishes `ghcr.io/scm-bench/scm-bench:0.1.0-rc.1`
and nothing else, so `docker pull` on the bare image name cannot return it.

The project follows semantic versioning, which Go's module system requires
rather than merely encourages. `v0.x` means no compatibility promise, and Go
treats major version 0 accordingly — no `/v2`-style import path suffix, and
breaking changes are allowed between minor versions. `v1.0.0` is a real
commitment; it should wait until the surfaces below have settled.

Three of those surfaces version independently, and tying them together would be
a mistake:

| Surface | Currently | Who depends on it |
|---|---|---|
| Tool version | the git tag | anyone installing the binary |
| Snapshot `schemaVersion` | `1` | anyone re-evaluating an archived snapshot |
| Check IDs (`CIS-1.1.3`) | stable | anyone naming them in `include`/`exclude` |

The tool can go from `v0.1.0` to `v0.9.0` with `schemaVersion` still at `1`.

## Roadmap

**v0.2** — CIS 1.2.2 (repository creation limits, once the Project Creator
interpretation is settled), default reviewers as a partial CIS-1.1.6 signal,
per-project policy overrides.

**Later** — GitHub Enterprise and GitLab fetchers. The snapshot schema is already
platform-neutral, and controls declare which platforms they apply to, so this is
mostly a matter of writing another fetcher.

---

## License

Apache 2.0. See [LICENSE](LICENSE).
