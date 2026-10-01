# The end-to-end suite

The Go tests prove the fetcher agrees with a stand-in Bitbucket. This proves
it agrees with a real one: a disposable Bitbucket Data Center, a fixture with
one deliberate deviation per repository, and the verdicts a correct scan of
it reports — written down from what the fixture *is*, not from what the tool
printed.

```sh
export BITBUCKET_LICENSE_FILE=~/bitbucket-timebomb.lic   # see below
./up.sh          # boot a fresh instance on :17990 (2-3 minutes)
./seed.sh        # users, groups, 4 projects, 10 repositories, 3 scan tokens
./verify.sh      # scan with each token and diff every verdict
```

`verify.sh` exits 1 on any difference and prints each one with the tool's
own explanation beside it. It also fails if the closing trace line of any
scan reports a write.

## The license

Bitbucket Data Center will not start without one. Atlassian publishes
short-lived licenses for exactly this — testing against a real instance — on
[Timebomb licenses for testing](https://developer.atlassian.com/platform/marketplace/timebomb-licenses-for-testing-server-apps/).
Copy the *10 user Bitbucket Data Center license, expires in 3 hours* into a
file and point `BITBUCKET_LICENSE_FILE` at it. The three hours count from
boot, so every `up.sh` starts a fresh clock.

## The three tokens

| Profile | Token | Sees |
| --- | --- | --- |
| `admin-full` | `PROJECT_ADMIN` + `REPO_ADMIN`, system administrator | everything a token can: Bitbucket shows branch permissions, hooks and grant tables only to repository and project administrators |
| `admin-read` | `PROJECT_READ` + `REPO_READ`, same user | settings, files, branches and effective permissions — not restrictions or hooks |
| `scanner` | `PROJECT_READ` + `REPO_READ`, an ordinary user with read on two projects | the same, for the projects it was granted plus the public one |

No token can carry a global permission (`ADMIN`, `SYS_ADMIN`), whoever holds
it, so `/admin/permissions/*` is unreadable to every one of them.

## The fixture

| Repository | What it puts in a known state |
| --- | --- |
| `HARD/payments-api` | everything configured once at the project: merge checks, Reject Force Push and Verify Commit Signature hooks, restrictions on `main`, linear merges, `SECURITY.md` |
| `HARD/empty-service` | an empty repository: branch controls are not applicable |
| `HARD/trunk-service` | default branch `trunk`, which the project's `main` restrictions do not cover; `docs/SECURITY.md` |
| `WEAK/legacy-billing` | nothing configured, a branch untouched since 2020, every licensed user can write |
| `WEAK/bypassed` | every restriction present, every one exempting the whole team |
| `WEAK/committer-hook` | Verify Committer enabled — it checks who pushed, not signatures |
| `WEAK/hook-protected` | no restriction, but Reject Force Push enabled |
| `WEAK/archived-tool` | archived |
| `WEAK/wrong-default` | configured default branch `master`, only `main` pushed |
| `PUB/docs-site` | a public project |
| `PRIV/secret-sauce` | a project the least-privileged token cannot see |

Instance level: three administrators, two of them only through a group;
`eve` holds write access and has never signed in.

The expected verdicts and the reasoning behind each are in
[`expected.py`](expected.py). When a verdict and the tool disagree, read the
reasoning before changing either.

## Cleaning up

```sh
docker rm -f bitbucket-bench-e2e && docker volume rm bitbucket-bench-e2e-home
```

`out/` holds tokens and session cookies for the instance; it is gitignored and
worthless once the container is gone.
