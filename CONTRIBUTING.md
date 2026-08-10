# Contributing

Thanks for looking. The most useful contributions to this project are usually
not code: a control that fires wrongly against a real Bitbucket instance, or
remediation text that does not match what the UI actually says, is worth more
than a refactor.

## Getting set up

```bash
git clone https://github.com/scm-bench/scm-bench
cd scm-bench
make check        # gofmt, go vet, race-enabled tests — everything CI runs
make build        # binary into bin/
```

Go 1.25+ is required. Nothing else — the policy bundle is embedded in the
binary and there is no code generation step.

No instance to test against? Every code path except the fetcher runs offline:

```bash
./bin/scm-bench scan --snapshot-in examples/snapshot.json
```

## The one rule that matters

**A control that cannot be evaluated reports `MANUAL`, never `PASS` or `FAIL`.**

If the token lacks a permission, if an add-on is not installed, if the API
never returned the field — the answer is "I could not tell", and the control is
excluded from the score. A confident wrong answer is the worst thing this tool
can do, because it teaches people to ignore its output.

Every part of the design follows from that:

- The fetcher records what it could not read in `Available`, and never
  substitutes a zero value for missing data.
- Policies check availability *before* they check the setting. The `MANUAL`
  branch comes first for a reason.
- Read lists through `lib.list`, never `object.get` directly — a nil Go slice
  marshals to JSON `null`, and `object.get` will not substitute its default for
  a key that exists with a null value.

## Adding or changing a control

Create a directory under `internal/checks/policies/bitbucketdc/`. No Go changes
are needed; the bundle is discovered at load time.

Each control is two files:

- `check.rego` — returns a single `result` document with `status`, `details`
  and optional `evidence`.
- `metadata.json` — ID, severity, scope, and the remediation text.

**Remediation is the part people act on.** It must name a concrete place: a
settings path (`Repository settings -> Branch permissions -> Add restriction`),
a file to add, or an explicit statement that nothing applies. A test enforces
this. Vague remediation is worse than none, because it wastes the reader's time
before they discover it does not help.

Then add the control to the coverage table in both READMEs.

## Testing

The suite runs every control against hardened, misconfigured, unreadable and
zero-valued fixtures. If you add a control, add its expected status to each.

The zero-valued case is not optional. It catches the class of bug where a rule
silently produces no verdict at all, which looks like a passing test run.

The fetcher is tested against a stand-in Bitbucket. That verifies the code is
self-consistent — it does **not** verify that Bitbucket behaves the way the
stand-in does. If you have access to a real instance, running against it and
reporting what differed is the single most valuable thing you can do here.

## Pull requests

- One change per pull request.
- Label it — `bug`, `enhancement`, `policy`, `documentation`, `ci`. Release
  notes are grouped by label, so an unlabelled pull request lands under
  "Other changes".
- Explain what you verified, and say plainly what you could not.

Commit messages are plain imperative sentences, not Conventional Commits.
Explain *why* the change is right; the diff already shows what it does.

## Reporting a problem

- **A control is wrong** — tell us the platform version, what the setting
  actually is, and what the tool said. A redacted `--snapshot-out` file is
  ideal.
- **A security issue** — do not open an issue. See [SECURITY.md](SECURITY.md).

## Licence

Contributions are accepted under [Apache 2.0](LICENSE).
