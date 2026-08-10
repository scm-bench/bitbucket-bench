<!--
  Banner 放在 scm-bench/.github 的 brand/ 下，组织 profile 和上传用的头像也都
  取自那里，全组织只有一份。这里用绝对地址有两个原因：相对路径跨不了仓库；而且
  README 会被打进每个 release 压缩包，那里没有仓库树可供解析。
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

依据 [CIS 软件供应链安全指南](https://www.cisecurity.org/benchmark/software-supply-chain-security)
的 **Source Code** 章节审计源代码管理平台。

scm-bench 以**只读**方式抓取实例快照，用 Rego 编写的策略进行判定，然后告诉你哪里配置有问题
——并给出修复所需的确切设置路径。

**v0.1 面向 Bitbucket Data Center**，这是该领域工具最少的平台。15 条规则自动判定；另有 5 条
以「明确记录的人工检查」形式保留，使映射关系完整，而不是悄悄地只做一半。

[English](README.md) · [贡献指南](CONTRIBUTING.md) · [安全策略](SECURITY.md)

---

## 唯一需要先了解的设计决定

**无法判定的规则输出 `MANUAL`，绝不输出 `PASS` 或 `FAIL`。**

如果 token 读不到 admin API、required-builds 插件没装、Bitbucket 不上报最后登录时间戳——
工具会如实说明，并把该规则完全排除在评分之外。分数不会因为「没能问出口的问题」而虚高，
也不会因此被扣分。

这一点比听起来重要。一个把「API 返回 403」报成 `FAIL` 的基准工具，只会教会大家忽略它的输出。

---

## 安装

**二进制** —— 从 [releases](https://github.com/scm-bench/scm-bench/releases) 下载：

```bash
# 压缩包名里带版本号，所以先取最新的 tag。
VERSION=$(curl -fsSL https://api.github.com/repos/scm-bench/scm-bench/releases/latest |
  sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')

curl -fsSL "https://github.com/scm-bench/scm-bench/releases/download/v${VERSION}/scm-bench_${VERSION}_linux_amd64.tar.gz" | tar xz
./scm-bench version
```

**Docker：**

```bash
docker run --rm ghcr.io/scm-bench/scm-bench:latest \
  scan --url https://bitbucket.example.com --token "$BITBUCKET_TOKEN"
```

**从源码构建**（Go 1.25+；构建会固定到一个已打补丁的 toolchain 并自动获取）：

```bash
go install github.com/scm-bench/scm-bench/cmd/scm-bench@latest
```

### 校验下载的产物

`checksums.txt` 与容器镜像用 [cosign](https://docs.sigstore.dev/) 做了 keyless 签名——
签名身份就是本仓库的 release workflow，因此不存在需要信任、也不会泄露的密钥。

压缩包本身没有逐个签名。每个压缩包的摘要都已经在 `checksums.txt` 里，所以一个签名就覆盖了
整次发布：先验签名，再用它担保的那个文件去验压缩包。压缩包另外带有 SLSA build provenance 证明。

每次发布的 release notes 里都附上了填好身份参数的 `cosign verify-blob` 与
`gh attestation verify` 命令。那几个身份参数才是关键：不带它们，校验只能确认「有人」签过这个
文件，而这不构成任何有意义的结论。

`go install` 这条路径由 Go module proxy 的 checksum database 单独保证。

---

## 快速开始

```bash
export BITBUCKET_URL=https://bitbucket.example.com
export BITBUCKET_TOKEN=<只读 HTTP access token>

# 全量扫描
scm-bench scan

# 只扫某个项目 / 某个仓库
scm-bench scan --project PLAT
scm-bench scan --repository PLAT/payments-api

# 机器可读输出
scm-bench scan -o json  --output-file report.json
scm-bench scan -o sarif --output-file report.sarif
```

> 工具输出只有英文。文档是双语的，维护者也并非都以英语为母语，所以这是一个决定而非疏漏：
> 每条规则的判定文字是规则自己生成的，换一种语言不是加一张字符串表，而是在二十条规则里
> 各复制一份消息拼装逻辑。半套翻译——标题是一种语言、发现描述是另一种——比不翻译更难读。

手边没有实例？内置样例快照可以直接跑通所有输出格式：

```bash
scm-bench scan --snapshot-in examples/snapshot.json
```

仓库是并发抓取的——`--concurrency`（默认 8）限制同时抓取的数量，实例负载高时把它调低是
比较客气的做法。`--timeout` 限制单个请求（默认 30s）；`--max-duration` 限制整次扫描，
默认不开启，因为「多久算太久」完全取决于实例有多大。

### 看着它扫

默认情况下，一次扫描只显示一行原地刷新的进度，然后是报告。请求日志归 **`--verbose`** 管：
逐条列出每个 `GET` 描述的是「工具做了什么」，而你要看的是「它发现了什么」。请求只在事情看起来
不对劲时才重要，而那正是你会敲 `--verbose` 的时候：

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

query string 只在它是「区分两个请求的关键」时才显示 —— 分页偏移，或者正在展开哪个组。
没有它，翻一遍用户目录会打出二十二行一模一样的记录，看着像卡在死循环里。

**最后那行才是重点。** 它统计的是**实际发出去的**方法，而不是「它们都是 GET」这个承诺。
一旦出现非读请求，会报成 `✗ … NOT READ-ONLY`，而不是悄悄并进总数——这个主张本来就该是
可核对的，不可核对的主张一文不值。

请求按仓库分组。由于仓库是并发抓取的，请求本身是交错到达的，所以每个仓库的请求会先攒着，
等它抓完再整块打印。

`--progress` 控制展示到什么程度：

| 模式 | 展示内容 |
|---|---|
| `compact`（默认） | 一行原地刷新的进度，加最后的审计行 |
| `full`（`--verbose` 隐含开启） | 每一个请求，加最后的审计行 |
| `off` | 只有最后的审计行 |

重定向或在 CI 里，`full` 会自动退回 `off`：没有光标可移动时，一行一个请求就是几千行没人要的
日志。但审计行仍然打印——「这个 token 被用来做了什么」的交代，在 CI 日志里同样有价值。

`--verbose` 同时打开请求日志和 fetcher 自己的日志。显式传 `--progress` 会覆盖这一点，
所以 `--verbose --progress off` 只给日志、不给请求列表。

`--max-duration` 用于放弃跑得太久的扫描，超时以 `2` 退出：

```bash
scm-bench scan --max-duration 20m
```

没有默认值。多久算太久完全取决于实例规模，随手定一个默认值只会把本来合法的长扫描变成失败。
`--timeout` 是另一回事——它约束的是单个 HTTP 请求，不是整次扫描。

### Token 需要什么权限

| 权限 | 可判定的内容 |
|---|---|
| **仓库读取** | 全部 14 条仓库级规则 |
| **额外的管理员读取** | 实例管理员数量（CIS-1.3.3）与闲置账号（CIS-1.3.1） |

普通只读 token 已经足够产出价值。没有管理员读取权限时，两条实例级规则输出 `MANUAL`，
扫描正常继续——不会失败。

推荐的凭据是 HTTP access token（`--token`，`BITBUCKET_TOKEN`）。不支持 token 的实例可以用
basic auth（`--username` / `--password`，或 `BITBUCKET_USERNAME` / `BITBUCKET_PASSWORD`）；
两者给一个即可，不要都给。命令行上显式输入的优先于环境变量提供的，因此 shell profile 里
一个过期的 `BITBUCKET_TOKEN` 不会悄悄盖掉你刚敲进去的凭据。

写在 URL 里的凭据——`https://user:pw@bitbucket.example.com`——不会被使用，并且在这个 URL
进入快照或报告之前就被剥掉了。

但**凭据被实例拒绝**是另一回事：这会在扫描开始前检出并以 `2` 退出。若把它当成「权限不足」，
一个打错的 token 就会产出满屏 `MANUAL`、得分 0 的完整报告——看起来像审计结论，其实只是拼写错误。

scm-bench **只发 `GET` 请求**。这一点由测试强制保证，不只是靠约定。

### 传输

这个 token 能读取实例上的每一个仓库，因此不会以明文形式上网。`http://` 地址在发出第一个
请求之前就会被拒绝——和本工具报告的那些配置问题不同，凭据一旦泄露就收不回来了。确实处在
可信网络中时可用 `--allow-plaintext` 覆盖；loopback 地址本身豁免，无需加任何 flag。

`--insecure` 用于跳过证书校验，适用于「实例在你无法安装的私有 CA 之后」的情况。

这两者都会在报告的 Scan warnings 段以及据此抓取的 snapshot 中留下一行记录。明文抓取的扫描、
或者没有验证过应答方身份的扫描，和正常扫描不是同一种证据。

---

## 覆盖范围

15 条自动判定的规则：

| CIS | 规则 | 严重度 | 判定依据 |
|---|---|---|---|
| 1.1.3 | 合并需至少两人审批 | HIGH | `requiredApprovers` |
| 1.1.4 | 源分支更新后重置审批 | MEDIUM | `unapproveOnUpdate` |
| 1.1.8 | 清理闲置分支 | LOW | 分支末次提交超过 90 天 |
| 1.1.9 | 合并前 CI 必须通过 | HIGH | Required builds 或 `requiredSuccessfulBuilds` |
| 1.1.11 | 未解决的任务阻止合并 | LOW | `requiredAllTasksComplete` |
| 1.1.12 | 验证提交签名 | MEDIUM | 已启用的签名校验 hook |
| 1.1.13 | 要求线性历史 | LOW | 已启用的合并策略 |
| 1.1.15 | 禁止直接 push 默认分支 | HIGH | `pull-request-only` / `read-only` 限制 |
| 1.1.16 | 禁止 force push | HIGH | `fast-forward-only` 限制 |
| 1.1.17 | 禁止删除分支 | MEDIUM | `no-deletes` 限制 |
| 1.2.1 | 发布安全策略文件 | LOW | 默认分支上的 `SECURITY.md` |
| 1.3.1 | 定期清理闲置用户 | MEDIUM | 最后登录时间 + 是否持有仓库权限 |
| 1.3.3 | 实例管理员数量受控（2–5） | HIGH | 全局权限，展开用户组 |
| 1.3.7 | 每个仓库至少 2 名管理员 | LOW | 仓库 + 项目授权，展开用户组 |
| 1.3.8 | 收紧仓库默认权限 | MEDIUM | public 标志 + 项目默认权限 |

5 条以「明确记录的人工检查」保留——会被报告、会给出原因，且不计入评分：

| CIS | 规则 | 为何不自动化 |
|---|---|---|
| 1.1.6 | Code owners | Bitbucket DC 没有 CODEOWNERS。Default reviewers 若不配合审批数要求只是提示性的，映射过去会夸大实际约束力。 |
| 1.2.2 | 限制仓库创建 | 需要把全局 Project Creator 权限、用户组成员、各项目授权放在一起解读，且「足够收敛」的口径因部署而异。计划在 v0.2 处理。 |
| 1.2.3 | 限制仓库删除 | Bitbucket 未把删除单独暴露为一项权限，任何判定都只是 CIS-1.3.3 与 1.3.7 的复述。 |
| 1.3.5 | 强制 MFA | 认证委托给外部 IdP（SAML/Crowd/LDAP），Bitbucket API 完全不暴露因子信息，仅凭 Bitbucket 数据下结论等于编造。 |
| 1.3.9 | 组织 Verified 徽章 | 托管 SaaS 概念，自建实例无对应物，输出 `NA`。 |

```bash
scm-bench list-checks          # 全部规则，含严重度与作用域
scm-bench list-checks --json   # 完整元数据，含修复文案
```

---

## 评分

```
score = Σ weight(通过) / Σ weight(通过 + 失败) × 100
```

其中 `HIGH = 3`、`MEDIUM = 2`、`LOW = 1`。`MANUAL` 与 `NA` 不进入分子也不进入分母。

表格输出会打印算式（`weighted 29/55 (HIGH=3, MEDIUM=2, LOW=1; manual and n/a excluded)`），
让这个数字可核对，而不是只能选择相信。

一个刻意设计的边界情况：当**什么都无法判定**时，分数是 `0` 而不是 `100`。
0 除以 0 不应该被读成「一切健康」。

分数请当趋势线看。真正决定结果是否可接受的，是按严重度统计的失败数。

---

## 输出格式

**`table`**（默认）—— 分数在最前，然后是这次扫描没看到的东西，再是按规则**并按判定结论**
分组的发现，因此「同一处配置问题散布在五十个仓库」读起来是一个问题，而不是五十个：

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

上面这段是 `scm-bench scan --snapshot-in examples/snapshot.json` 在 `COLUMNS=100`
下的真实输出，只在标了 `...` 的地方做了省略。计数的单位是 finding —— 一条规则对一个资源，
所以它们加起来会多于 `list-checks` 报出的 20 条规则。

汇总放在最前，因为终端是从上往下读的。`scored N of M` 这一行值得在看分数之前先读：
无法判定的规则不进入分数的分子，也不进入分母 —— 单看每一条规则这是对的，合起来却有
误导性，因为分母被缩小了，于是一个读不到多少东西的 token 反而能从很小的样本里得出很高的
分数。`--max-manual` 就是把这种情况变成一次失败的运行，而不是一份好看的报告。
`most affected` 是逐规则分组看不出来的那份统计 —— 它指出该由谁去动手。

扫描告警排在发现之前而不是之后，因为它决定了这份报告有多少可信：一个让扫描丢掉整个仓库的
403，正是下面那一串「无法判定」的原因。

**`Not evaluated` 与 `Needs manual review` 都是 `MANUAL`，按成因拆开。** 前者是**这次运行**
读不到的东西 —— 一个读不到的仓库过去会产出一条规则一个条目，用十三种说法讲同一个 403 ——
所以按资源折叠成一条，并列全它牵连的规则。后者是**任何 API 都答不了**的规则
（metadata 里 `automated: false`），无论 token 多好都需要人来判断。只有后者会给出修复建议：
一条扫描根本没看到的规则，并不能说它配错了，印出「怎么改设置」等于在说反话。

行宽跟随 `COLUMNS`，夹在 60–100 之间，未导出时默认 80。折行的续行保留标签列，
并悬挂缩进对齐到首行内容。

每一行都以等宽的标签开头，在终端里带颜色：

| 标签 | 含义 |
|---|---|
| `[PASS]` | 已判定，配置正确 |
| `[FAIL]` | 已判定，配置有问题 |
| `[WARN]` | 需要人介入：工具无法判定的规则（`MANUAL`），或扫描读不到的东西 |
| `[INFO]` | 结构、证据、修复建议、折行的续行，以及不适用的规则——本身从不是判定结论 |

四个标签，与 kube-bench 用的是同一组。`MANUAL` 故意归到 `[WARN]` 而不是 `[INFO]`：
「这一条没人验证过」是这个工具最不肯让它消失的信息，它不该和章节标题共用一列。

每条发现带一行 `fix:` —— 第一步动作，以及在哪里做；完整的修复段落独立成节放在末尾。
这个拆分才是让发现列表可以快速扫读的关键：那些段落写的是设置路径、项目级的等价做法和
配置项名称，把它印在每条规则下面，会让一次二十仓库的扫描变成一堵必须读完才能找到下一条
判定的散文墙。`--no-remediations` 可以整段去掉；一行的 `fix:` 仍然保留。

真正承载信息的是标签，颜色只是强化它。所以输出被管道、重定向，或设置了 `NO_COLOR`
时，什么都不会丢。这也让最自然的用法直接可用：

```bash
scm-bench scan 2>&1 | grep '^\[FAIL\]'   # 实例哪里有问题
scm-bench scan 2>&1 | grep '^\[WARN\]'   # 这次扫描没看到什么
```

一条规则无论覆盖多少个仓库，只贡献一行判定。数量放在最前，因为它决定了该怎么处理：
三个仓库是疏漏，三百个说明这条策略从来没被推行过。同一规则下因**不同原因**失败的仓库
仍然各自成组——那是不同的问题。

`--max-resources` 控制列出多少个名字之后开始汇总（默认 5，`0` 表示全部列出）。它只影响这一种
格式：`json` 与 `sarif` 始终携带完整集合。

通过和不适用的规则只计入汇总，不会逐条列出——报告是一份待办清单。`--show-passed` 会把它们
也列出来，当你的问题是「这个实例已经做对了哪些」时用它。

颜色只在 stdout 是终端时启用，并遵守 `NO_COLOR`；`--no-color` 是显式关掉它，
适用于终端被某个会保留转义序列的东西捕获的场景。

只有 stdout 是终端时才上色，并遵守 `NO_COLOR`。

**`json`** —— 完整报告：每条发现、证据、该规则为何存在、修复建议与评分明细。
报告文件按 `0600` 写入，和 snapshot 一样——渲染出来的报告同样是一份实例弱点地图。

**`sarif`** —— SARIF 2.1.0，供 CI 消费。只输出失败与需人工复核项；通过与 N/A 因不需要行动
而省略。这些发现是「配置事实」而非源码行，所以每条结果携带指向仓库的 `logicalLocation`，
而不是指向并不存在的文件的 `physicalLocation`。扫描告警作为 invocation notification 一并输出，
避免把不完整的扫描误当作干净结果。

---

## 配置

任何「讲道理的人可能有不同意见」的阈值都可配置，策略里不写死任何数字。

```bash
scm-bench scan --config scm-bench.yaml
```

完整带注释的配置见 [`examples/config.yaml`](examples/config.yaml)。最常调整的几项：

```yaml
thresholds:
  minApprovers: 2        # CIS-1.1.3
  staleBranchDays: 90    # CIS-1.1.8
  minOrgAdmins: 2        # CIS-1.3.3
  maxOrgAdmins: 5
  inactiveUserDays: 90   # CIS-1.3.1

# Bitbucket 自身不带签名校验，需指明你用的插件
signatureHookKeys: [signature, gpg, verify-commit]

# 有意公开代码的实例
allowPublicRepositories: false

exclude: [CIS-1.1.13]    # 或用 include: 只跑子集
```

配置文件只需写你要改的部分，未出现的键保持默认。序列是唯一的例外：YAML 会整体替换列表，
因此设置 `signatureHookKeys` 是替换整个列表而非追加。

无法识别的键会直接报错，而不是被忽略。把 `minApprovers` 写成 `minApprover` 一样能解析成功、
什么都不改，却会产出一份读者以为「按我的阈值判定过」的报告——所以扫描宁可拒绝启动。
`include` / `exclude` 中指向不存在控制项的条目同理，负数阈值同理，任何列表里的空条目也同理——
`signatureHookKeys` 里的一个空串是所有 hook 名字的子串，会把第一个启用的 hook（无论它做什么）
报成「已启用提交签名校验」。

`permissionRank` 是大多数人从不改、但应该知道它存在的一个字段：它定义了 Bitbucket 各个权限名
之间的高低关系，`maxDefaultPermission` 就是拿它来比对的。如果你的 Bitbucket 版本报出一个这张表
里没有的权限，CIS-1.3.8 会输出 `MANUAL`，而不是去猜它排在哪。和上面的序列不同，它是映射，
按键合并而不是整体替换，所以只写一个权限不会影响其余的。

---

## 在 CI 中使用

```yaml
- name: Audit Bitbucket
  id: audit
  # 扫描发现问题时会以 1 退出，这正是它的用途——但那样这一步就结束了整个 job，
  # 报告还没来得及上传。所以把「失败」推迟到最后一步。
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

退出码：

| 码 | 含义 |
|---|---|
| `0` | 扫描完成，未触发任何阈值 |
| `1` | 扫描完成，触发了某个阈值 |
| `2` | 扫描本身没能完成 |

有三个 flag 会导致退出码 `1`，它们问的是不同的问题：

| Flag | 问的是 |
|---|---|
| `--fail-on` | 有没有达到这个严重度的失败项？`high`（默认）、`medium`、`low`、`none` |
| `--fail-under` | 分数可以接受吗？0-100 的数字，`0` 表示关闭 |
| `--max-manual` | 这次扫描到底看到了多少，够不够形成判断？百分比，`-1` 表示关闭 |

建议从 `--fail-on high` 起步，清完第一轮发现后再收紧。

`--max-manual` 是最值得尽早设上、也最不直观的一个。无法判定的规则是被排除出分数，
而不是计为失败——单看每一条规则这是对的，合起来却有误导性，因为它缩小了分母。
于是一个丢了权限的 token 反而可能比正常的 token 得分**更高**：在自带的示例快照上，
把所有可读字段清空会让分数从 53 涨到 100。`--fail-under` 拦不住这种情况，`--max-manual` 可以。

关于 SARIF，如果你要上传它：本工具的发现是配置事实，不是源码行，所以每个 result 带的是指向
仓库的 `logicalLocation`，而不是指向某个并不存在的文件的 `physicalLocation`。GitHub code
scanning 用 `physicalLocation` 把告警挂到代码上，因此告警会以「没有关联文件」的形式出现。
在依赖它之前请先在你自己的仓库上实测一次；如果你是要喂给仪表盘而不是 code scanning，
`json` 格式是更合适的选择。

### 把「抓取」和「判定」拆开

快照是自包含产物，因此持有凭据的步骤与执行判定的步骤可以是不同步骤、不同机器、不同时间：

```bash
# 在能访问 Bitbucket、持有 token 的 runner 上
scm-bench scan --snapshot-out snapshot.json -o json --fail-on none

# 之后在任何地方——无需凭据，无需网络
scm-bench scan --snapshot-in snapshot.json -o sarif --fail-on high
```

对归档快照重跑策略，还能看出换了阈值之后结论会如何变化，而不必再碰实例一次。

快照以 `0600` 写入：它是一份精确描述实例薄弱点的地图。报告同样如此。

这是 Unix 权限位，只在类 Unix 系统上生效——Windows 没有对应的位，Go 只把它映射成只读属性，
文件真正的访问控制来自它从所在目录继承的 ACL。在 Windows 上，请把快照和报告放到一个本身
已经受限的位置。

### 捕捉姿态回退

分数要当趋势线看，而趋势需要两个点。`diff` 比较两份快照并报告变化：

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

`diff` 用的是和 `scan` 完全一样的标签列，所以
`scm-bench diff a.json b.json | grep '^\[FAIL\]'` 就能回答「什么变差了」。
`GONE` 是每个资源一条，而不是每条规则一条：删掉一个仓库是关于这个仓库的一个事实，
把它报二十遍只会把这条命令本该凸显的回退埋掉。

它接受和 `scan` 相同的输出 flag —— `-o table|json`、`--output-file`、
`--no-color` —— 外加 `--fail-on-regression`（默认开启）与 `--allow-other-instance`。
不提供 SARIF：一次比较不是一组发现。

**两份快照都会先用当前构建、当前配置各评估一次**再比较。直接 diff 两份已渲染的报告更省事，
但那样差异里会混进「两次运行之间工具或阈值发生的变化」——而这恰恰是回退检查最不能被混淆的东西。

只有 **`PASS → FAIL`** 算回退，也只有它驱动退出码：

| 分类 | 含义 | 是否 exit 1 |
|---|---|---|
| `REGRESSED` | 原本满足，现在不满足 | 是 |
| `NEW FAILURES` | 失败发生在此前不存在的仓库上 | 否 |
| `FIXED` | `FAIL → PASS` | 否 |
| `OTHER CHANGES` | 任何涉及 `MANUAL` 的变化 | 否 |
| `GONE` | 资源已不存在 | 否 |

新仓库带着失败出现不算回退——没有任何东西变差，只是实例变大了。涉及 `MANUAL` 的同样不算：
`PASS → MANUAL` 意味着工具**看不到**这项设置了，通常是 token 掉了权限或插件被卸载，
因为这个让流水线失败，等于把扫描自己的盲区算到实例头上。

`--fail-on-regression=false` 可切换为纯报告模式。其余退出码与 `scan` 一致：
`0` 干净、`1` 有回退、`2` 比较无法完成。

在 CI 里把上一次的快照留作 artifact，然后与之比较：

```bash
scm-bench scan --snapshot-out today.json -o json --fail-on none
scm-bench diff baseline.json today.json
```

比较来自两个不同实例的快照会被拒绝，除非用 `--allow-other-instance` 表明这是有意为之：
否则每个仓库都会既显示为「已消失」又显示为「新出现」，看起来像结论，其实不是。

---

## 工作原理

```
Bitbucket REST  ──►   fetcher   ──►  snapshot.json  ──►   Rego 策略   ──►   报告
                  (Go，仅 GET)      (归一化)          (每条规则一个)   table/json/sarif
```

这个切分是严格的，也正是这个工具可维护的原因：

- **fetcher 不做任何判定**。它只做归一化，并记录哪些内容没能读到。
- **策略不发 HTTP**。它读一份 JSON 文档，返回一个结论。

有些解析刻意放在 Go 而非 Rego 里——glob 语义、Bitbucket 的分支模型、用户组展开。
这些琐碎、随版本变化，而且本质上不属于「策略」。fetcher 把它们解析完，交给 Rego 一个布尔值：
`matchesDefaultBranch`。规则问的是*「默认分支受保护吗？」*，而不是
*「`release/**` 能匹配上 `refs/heads/main` 吗？」*

```
internal/
  scm/                  归一化快照类型（fetcher 与策略之间的契约）
    bitbucketdc/        REST 客户端、fetcher、ref matcher 解析
  checks/policies/      每条规则一个目录：check.rego + metadata.json
  engine/               一次性编译策略包、执行判定、计分
  report/               table、json、sarif
  config/               作为 input.config 传给 Rego 的阈值
```

### 新增一条规则

在 `internal/checks/policies/bitbucketdc/` 下建一个目录即可，无需改 Go 代码：
策略包是嵌入的，加载时自动发现。

`check.rego` 返回单个 `result` 文档：

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

`MANUAL` 分支排在最前是刻意的：只有先确认「拿到了数据」，判断「数据说明了什么」才成立。

写策略时有一条硬性约定：**读取列表一律走 `lib.list`**，不要直接用 `object.get`。
Go 的 nil 切片会被序列化成 JSON `null`，而 `object.get` 只在键**不存在**时才使用默认值——
键存在但值为 null 时返回的就是 null，把它传给 `concat` 或 `sort` 会让整条规则变成 undefined，
最终该规则什么结论都产不出。回归测试
`TestZeroValuedSnapshotProducesAVerdictForEveryControl` 专门守住这一点。

`metadata.json` 承载 ID、严重度、作用域，以及最重要的修复文案 —— 分两种长度写。
`remediation` 是完整段落；`fixSummary` 是它的第一步动作，一行祈使句，也就是发现列表里
印在每条判定旁边的那句。有一条测试强制要求两者都指向一个具体位置：设置路径、需要新增的
文件，或明确说明「无需处理」，并要求 `fixSummary` 不超过 100 个字符。含糊的修复建议
比没有更糟。

---

## 开发

```bash
make check      # fmt、vet、test —— 与 CI 一致
make build      # 构建到 bin/
make snapshot   # 本地跑完整发布流程，不发布
```

测试套件会把整个策略包分别跑在「已加固」「配置松散」「读不到」「空仓库」四种夹具上，
逐条断言每个规则的预期状态——包括「读不到设置时必须输出 `MANUAL` 而不是给出自信的错误结论」。
fetcher 则对着一个仿真 Bitbucket 测试，同时覆盖分页、端点改名、权限拒绝，以及同一个 merge check
在不同版本里一会儿是数字、一会儿是对象的跨版本字段形态。

---

## 发版

两种方式，二选一。推 tag：

```bash
git tag -a v0.1.0 -m "scm-bench v0.1.0" && git push origin v0.1.0
```

或者在 Actions 页面手动运行 **Release** workflow，填入要创建的 tag。后者不需要本地检出，
而且会在创建前校验 tag——非 canonical 的版本会被直接拒绝，而不是安静地产出一个没人装得上的
release。两种方式后续流程相同。

**发布 goreleaser 建好的那个 draft，不要在 Releases 页面另建 release。** goreleaser
会创建 draft 并把全部产物传进去；另建的 release 只有 notes、没有任何文件——`v0.1.0-rc.1`
就是这样变成了两个 release 对象，其中一个什么都下载不到。

Release notes 在那个 draft 里手写。goreleaser 只填带版本号的部分（安装命令与校验区块），
叙述留给人写——那才是值得写的一半。点 **Generate release notes** 会在其上追加 GitHub
自己生成的列表，按 [`.github/release.yml`](.github/release.yml) 的标签分类。那个列表覆盖的是
**已合并的 Pull Request**；直推到 `main` 的提交不是 PR，不会出现在里面。

**tag 必须是完整三段式** —— `v0.1.0`，不能是 `v0.1`。Go 认为 `v0.1` 是合法的 semver
*字符串*，但它不是 canonical 形式，模块系统会忽略这样的 tag，`go install ...@latest`
根本找不到这个版本。goreleaser 不会拦这个错：它会照常构建 `v0.1`，产出一批没人能
`go install` 的产物。`v` 前缀同样不能省。

**release 以 draft 形式创建。** goreleaser 构建并上传全部产物后就停下，由人检查完再手动
发布。这是刻意的：release 一旦公开基本不可逆——模块代理会抓取并缓存该 tag，删掉 GitHub
release 并不会撤销已发布的模块版本。

签名不需要发版的人做任何事。cosign 以 keyless 方式签 `checksums.txt` 和容器 manifest，
用的是 workflow 自己的 OIDC token；同一次运行还会为压缩包记录 SLSA provenance 证明。
没有密钥要保管，所以发版不依赖某一个人的电脑。

发布前值得在 draft 上确认：`checksums.txt.bundle` 在，SBOM 也已附上。
签名步骤被跳过的 release，其他方面看上去仍然是完整的。

**有东西还没验证时用 prerelease tag** —— `v0.1.0-rc.1`。Go 的 `@latest` 只会解析到最新的
*正式*版本，因此 prerelease 必须按名字显式请求，不会落到没有主动选择它的用户头上。需要时
可以一路迭代 `-rc.2`、`-rc.3`，而不用烧掉 `v0.1.0` 这个号。

Docker 没有对应的规则，所以这一点改为在发布配置里强制：`:latest` manifest 带
`skip_push: auto`，遇到 prerelease tag 时不会被改动。一个 rc 只会发布
`ghcr.io/scm-bench/scm-bench:0.1.0-rc.1`，不发布别的，因此 `docker pull` 裸镜像名
拿不到它。

本项目遵循语义化版本——这是 Go 模块系统的硬性要求，而非风格建议。`v0.x` 表示不作兼容性
承诺，Go 也据此特殊对待主版本 0：不需要 `/v2` 这类导入路径后缀，且允许 minor 之间出现
破坏性变更。`v1.0.0` 是实打实的承诺，应当等下面这些接口稳定之后再上。

其中三个接口各自独立演进，把它们绑在一起是个错误：

| 接口 | 当前 | 谁在依赖 |
|---|---|---|
| 工具版本 | git tag | 安装二进制的人 |
| 快照 `schemaVersion` | `1` | 对归档快照重新判定的人 |
| 规则 ID（`CIS-1.1.3`） | 稳定 | 在 `include`/`exclude` 里写它们的人 |

工具从 `v0.1.0` 走到 `v0.9.0`，`schemaVersion` 完全可以一直是 `1`。

## 路线图

**v0.2** —— CIS 1.2.2（仓库创建限制，待 Project Creator 判定口径确定后）、
把 default reviewers 作为 CIS-1.1.6 的部分信号、项目级策略覆盖。

**之后** —— GitHub Enterprise 与 GitLab 的 fetcher。快照 schema 本就是平台中立的，
规则也声明了适用平台，所以这基本只是「再写一个 fetcher」的工作量。

---

## 许可证

Apache 2.0，见 [LICENSE](LICENSE)。
