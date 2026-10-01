#!/usr/bin/env python3
"""The verdicts a correct scan of seed.sh's fixture reports, per token.

Derived from what seed.sh configured, not from what the tool printed: each
row says what the instance actually is, so a mismatch is a finding about the
tool until proven otherwise. Writes expected/<profile>.tsv.

Three tokens, three views of one instance:

  admin-full  PROJECT_ADMIN + REPO_ADMIN, held by the system administrator.
              The most a token can see: Bitbucket shows branch permissions,
              hooks and grant tables only to repository and project admins,
              and a token can never carry a global permission.
  admin-read  PROJECT_READ + REPO_READ, same user. Restrictions and hooks
              become unreadable; settings, files and effective permissions
              (through /users?permission=...) do not.
  scanner     PROJECT_READ + REPO_READ for an ordinary user granted read on
              HARD and WEAK only. PUB is public, so it is visible too; PRIV is
              not, and a scan cannot know what it was never shown.
"""
import os

P, F, M, N = "PASS", "FAIL", "MANUAL", "NA"

REPO_CONTROLS = [
    "CIS-1.1.3", "CIS-1.1.4", "CIS-1.1.6", "CIS-1.1.8", "CIS-1.1.9",
    "CIS-1.1.11", "CIS-1.1.12", "CIS-1.1.13", "CIS-1.1.15", "CIS-1.1.16",
    "CIS-1.1.17", "CIS-1.2.1", "CIS-1.3.7", "CIS-1.3.8",
]

# Controls whose answer needs branch permissions or hooks: the two things only
# a repository administrator may read.
NEEDS_ADMIN_TOKEN = {"CIS-1.1.12", "CIS-1.1.15", "CIS-1.1.16", "CIS-1.1.17"}

# CIS-1.1.4 FAILs everywhere: "Unapprove automatically on new changes" comes
# from Atlassian's separately installed Auto Unapprove app, absent from this
# instance, so nothing resets an approval — and Bitbucket reports no setting.
# CIS-1.1.6 is MANUAL by design: Bitbucket has no code owners.

# Truth as seen by a token that can read everything a token can.
FULL = {
    # Hardened at the project: every merge check, the Reject Force Push and
    # Verify Commit Signature hooks, and restrictions on refs/heads/main.
    # Two project administrators (alice, bob); instance admins do not count
    # toward a repository's own administrators.
    "HARD/payments-api": dict(zip(REPO_CONTROLS, [P, F, M, P, P, P, P, P, P, P, P, P, P, P])),
    # Empty: nothing to protect on a branch that does not exist yet, no branch
    # to go stale, no file to find, nothing to merge into. Merge checks that
    # are repository-wide (approvals, tasks, strategies, hooks) still apply.
    "HARD/empty-service": dict(zip(REPO_CONTROLS, [P, F, M, N, N, P, P, P, N, N, N, N, P, P])),
    # Default branch "trunk": the project's restrictions name refs/heads/main,
    # so pushes and deletes are unprotected; force pushes are still refused by
    # the inherited hook. SECURITY.md lives under docs/.
    "HARD/trunk-service": dict(zip(REPO_CONTROLS, [P, F, M, P, P, P, P, P, F, P, F, P, P, P])),
    # Nothing configured, a branch untouched since 2020, one project admin,
    # and every licensed user may write.
    "WEAK/legacy-billing": dict(zip(REPO_CONTROLS, [F, F, M, F, F, F, F, F, F, F, F, F, F, F])),
    # Every restriction exists and every one exempts the five developers.
    "WEAK/bypassed": dict(zip(REPO_CONTROLS, [F, F, M, P, F, F, F, F, F, F, F, P, F, F])),
    # Verify Committer checks who pushed, not whether anything was signed.
    "WEAK/committer-hook": dict(zip(REPO_CONTROLS, [F, F, M, P, F, F, F, F, F, F, F, P, F, F])),
    # No restriction, but the Reject Force Push hook refuses force pushes.
    "WEAK/hook-protected": dict(zip(REPO_CONTROLS, [F, F, M, P, F, F, F, F, F, P, F, P, F, F])),
    # Archived: nothing can be pushed or merged, so every control about change
    # is not applicable, including who administers it; who can read it (1.3.8:
    # every licensed user may write to WEAK) still matters.
    "WEAK/archived-tool": dict(zip(REPO_CONTROLS, [N, N, N, N, N, N, N, N, N, N, N, N, N, F])),
    # Configured default branch "master" was never pushed. With no restriction
    # anywhere, no branch is protected whichever is default; the security
    # policy can only be looked for on a default branch that exists.
    "WEAK/wrong-default": dict(zip(REPO_CONTROLS, [F, F, M, P, F, F, F, F, F, F, F, M, F, F])),
    # Matchers resolved by the fetcher: the suffix pattern covers main, the
    # production model branch is main, the RELEASE category is not; the
    # required build exempts main through a suffix pattern.
    "WEAK/patterns": dict(zip(REPO_CONTROLS, [F, F, M, P, F, F, F, F, P, P, F, P, F, F])),
    # Each direct-push restriction exempts a different deploy key, so nobody
    # is exempt from both; the history-rewrite one lets a key through.
    "WEAK/deploy-keys": dict(zip(REPO_CONTROLS, [F, F, M, P, F, F, F, F, P, F, F, P, F, F])),
    # Public: anonymous users can read it.
    "PUB/docs-site": dict(zip(REPO_CONTROLS, [F, F, M, P, F, F, F, F, F, F, F, P, F, F])),
    # Private, unprotected, a single project admin.
    "PRIV/secret-sauce": dict(zip(REPO_CONTROLS, [F, F, M, P, F, F, F, F, F, F, F, P, F, P])),
}

INSTANCE = {
    "CIS-1.2.2": M,
    "CIS-1.2.3": M,
    # Nobody has gone 90 days without signing in: eve never has, but her
    # account was created minutes ago, so she is new rather than dormant.
    "CIS-1.3.1": P,
    # admin (SYS_ADMIN) plus admin2 and admin3 through bb-admins: 3, within 2-5.
    "CIS-1.3.3": P,
    "CIS-1.3.5": M,
    "CIS-1.3.9": N,
}


def read_only(view):
    """What a token without repository-admin rights can still decide."""
    out = {}
    for repo, verdicts in view.items():
        row = dict(verdicts)
        for control in NEEDS_ADMIN_TOKEN:
            # NA stays NA: an empty or archived repository has nothing to
            # protect, and that is known without reading any restriction.
            if row[control] != N:
                row[control] = M
        out[repo] = row
    return out


PROFILES = {
    "admin-full": FULL,
    "admin-read": read_only(FULL),
    "scanner": {k: v for k, v in read_only(FULL).items() if not k.startswith("PRIV/")},
}

here = os.path.dirname(os.path.abspath(__file__))
os.makedirs(os.path.join(here, "expected"), exist_ok=True)
for profile, view in PROFILES.items():
    rows = []
    for repo, verdicts in view.items():
        rows += [(control, repo, status) for control, status in verdicts.items()]
    rows += [(control, "instance", status) for control, status in INSTANCE.items()]
    with open(os.path.join(here, "expected", f"{profile}.tsv"), "w") as fh:
        fh.write("# generated by expected.py; edit that, not this\n")
        for row in sorted(rows):
            fh.write("\t".join(row) + "\n")
    print(f"expected/{profile}.tsv: {len(rows)} verdicts")
