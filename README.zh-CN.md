<!--
  Banner 放在 scm-bench/.github 的 brand/ 下，组织 profile 和上传用的头像也都
  取自那里，全组织只有一份。这里用绝对地址有两个原因：相对路径跨不了仓库；而且
  README 会被打进每个 release 压缩包，那里没有仓库树可供解析。
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

依据 [CIS 软件供应链安全指南](https://www.cisecurity.org/benchmark/software-supply-chain-security)
的 **Source Code** 章节审计 **Bitbucket Data Center**。

bitbucket-bench 以**只读**方式抓取实例快照，用 Rego 编写的策略进行判定，然后告诉你哪里配置有问题
——并给出修复所需的确切设置路径。

15 条规则自动判定；另有 5 条以「明确记录的人工检查」形式保留，使映射关系完整，而不是悄悄地只做一半。
每一条判定都由一套端到端测试在真实的 Bitbucket Data Center 上核对过，所用的三个 token
权限范围各不相同——见[实测验证](#实测验证)。

本仓库是 [scm-bench](https://github.com/scm-bench/scm-bench) 家族中负责 Bitbucket 的那一个。
家族里每个工具审计一个平台，并以同样的形态输出报告；本仓库是家族
[bench contract](https://github.com/scm-bench/scm-bench/blob/main/docs/bench-contract.md)
的参考实现。

[English](README.md) · [贡献指南](CONTRIBUTING.md) · [安全策略](SECURITY.md) · [遵循的规范](#遵循的规范)

---

## 唯一需要先了解的设计决定

**无法判定的规则输出 `MANUAL`，绝不输出 `PASS` 或 `FAIL`。**

如果 token 读不到分支权限、某个用户组的成员列不出来、Bitbucket 不上报最后登录时间戳——
工具会如实说明，并把该规则完全排除在评分之外。分数不会因为「没能问出口的问题」而虚高，
也不会因此被扣分。

这一点比听起来重要。一个把「API 返回 403」悄悄报成 `FAIL` 的基准工具，只会教会大家忽略它的输出。

---

## 安装

**二进制** —— 从 [releases](https://github.com/scm-bench/bitbucket-bench/releases) 下载：

```bash
# 压缩包名里带版本号，所以先取最新的 tag。
VERSION=$(curl -fsSL https://api.github.com/repos/scm-bench/bitbucket-bench/releases/latest |
  sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')

curl -fsSL "https://github.com/scm-bench/bitbucket-bench/releases/download/v${VERSION}/bitbucket-bench_${VERSION}_linux_amd64.tar.gz" | tar xz
./bitbucket-bench version
```

**Docker：**

```bash
docker run --rm ghcr.io/scm-bench/bitbucket-bench:latest \
  scan --url https://bitbucket.example.com --token "$BITBUCKET_TOKEN"
```

**从源码构建**（Go 1.25+；构建会固定到一个已打补丁的 toolchain 并自动获取）：

```bash
go install github.com/scm-bench/bitbucket-bench/cmd/bitbucket-bench@latest
```

### 校验下载的产物

`checksums.txt` 与容器镜像用 [cosign](https://docs.sigstore.dev/) 做了 keyless 签名——
签名身份就是本仓库的 release workflow，因此不存在需要信任、也不会泄露的密钥。

压缩包本身没有逐个签名。每个压缩包的摘要都已经在 `checksums.txt` 里，所以一个签名就覆盖了
整次发布：先验签名，再用它担保的那个文件去验压缩包。压缩包另外带有 SLSA build provenance 证明。

每次发布的 release notes 里都附上了填好身份参数的 `cosign verify-blob` 与
`gh attestation verify` 命令。那几个身份参数才是关键：不带它们，校验只能确认「有人」签过这个
文件。

`go install` 这条路径另有保障——Go module proxy 的 checksum database 锁定了这条路径能取到的内容。

---

## 快速开始

```bash
export BITBUCKET_URL=https://bitbucket.example.com
export BITBUCKET_TOKEN=<只读 HTTP access token>

# 全量扫描
bitbucket-bench scan

# 只扫某个项目 / 某个仓库
bitbucket-bench scan --project PLAT
bitbucket-bench scan --repository PLAT/payments-api

# 机器可读输出
bitbucket-bench scan -o json  --output-file report.json
bitbucket-bench scan -o sarif --output-file report.sarif   # GitHub code scanning
bitbucket-bench scan -o junit --output-file report.xml     # Jenkins, Azure Pipelines, GitLab
```

手边没有实例？二进制里内置了样例，一个参数就能看到第一份报告：

```bash
bitbucket-bench scan --demo
```

在终端里什么都不配置直接运行 `bitbucket-bench scan`，它会交互式地给出同样的选择：现在输入
URL 和 token，或者先看样例。样例同时以 `examples/snapshot.json` 的形式存在仓库里，
从源码检出运行时也可以用 `--snapshot-in` 评估它。

如果你选择输入 URL 和 token，scan 会**询问**（而不是自作主张）是否在它们被验证可用
之后保存下来，让之后的扫描什么都不用再输。保存位置是用户配置目录下的
`instance.yaml`（Linux 上是 `~/.config/bitbucket-bench/`；`BITBUCKET_BENCH_CONFIG_DIR` 可改写
位置），权限 `0600`——token 是一份活的凭据。手敲的 `--url` 或导出的 `BITBUCKET_URL`
永远优先于这个文件，并且每次用到它的扫描都会在 stderr 上说明。删掉文件即忘记。

仓库是并发抓取的——配置文件里的 `scan.concurrency`（默认 8）限制同时抓取的数量，实例
负载高时把它调低是比较客气的做法。`scan.timeout` 限制单个请求（默认 30s）；
`scan.maxDuration` 限制整次扫描，默认不开启，因为「多久算太久」完全取决于实例有多大。
这几项放在配置里而不是 flag 里，因为它们描述的是部署本身，而不是某一次运行——见[配置](#配置)。

### 看着它扫

默认情况下，一次扫描显示一行原地刷新的进度 —— 转轮、当前阶段和已完成请求的实时计数，
从第一刻起就可见，慢的实例不会看起来像卡死的实例 —— 然后是报告。请求日志归
**`--verbose`** 管：
逐条列出每个 `GET` 描述的是「工具做了什么」，而你要看的是「它发现了什么」。请求只在事情看起来
不对劲时才重要，而那正是你会敲 `--verbose` 的时候：

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

这是一个只读 token 在 Bitbucket 10.4 上扫描单个仓库的过程，其中每个 `401` 都在预料之中。
第一个是实例的授权表，任何 token 都读不到——Bitbucket 不允许 token 持有全局权限——而且读不到也
毫无损失：它上面那一行已经向 Bitbucket 问过谁是管理员了。仓库下的两个是该仓库的分支权限和
hook，Bitbucket 只把它们展示给仓库管理员，所以建立在它们之上的四条规则——提交签名、直接 push、
force push、分支删除——会输出 `MANUAL`。query string 只在它是「区分两个请求的关键」时才显示
—— 分页偏移、正在展开哪个组、正在询问哪种权限。没有它，翻一遍用户目录会打出二十二行一模一样的
记录，看着像卡在死循环里。

**最后那行才是重点。** 它统计的是**实际发出去的**方法，而不是「它们都是 GET」这个承诺。
一旦出现非读请求，会报成 `✗ … NOT READ-ONLY`，而不是悄悄并进总数——这个主张本来就该是
可核对的，不可核对的主张一文不值。

请求按仓库分组。由于仓库是并发抓取的，请求本身是交错到达的，所以每个仓库的请求会先攒着，
等它抓完再整块打印。

配置里的 `scan.progress` 控制展示到什么程度：

| 模式 | 展示内容 |
|---|---|
| `compact`（默认） | 一行原地刷新的进度，加最后的审计行 |
| `full`（`--verbose` 隐含开启） | 每一个请求，加最后的审计行 |
| `off` | 只有最后的审计行 |

重定向或在 CI 里，`full` 会自动退回 `off`：没有光标可移动时，一行一个请求就是几千行没人要的
日志。但审计行仍然打印——「这个 token 被用来做了什么」的交代，在 CI 日志里同样有价值。

`--verbose` 同时打开请求日志和 fetcher 自己的日志；它是刚敲下的 flag，优先于配置文件里
环境性的 `progress` 设置。

`scan.maxDuration` 用于放弃跑得太久的扫描，超时以 `2` 退出：

```yaml
scan:
  maxDuration: 20m
```

没有默认值。多久算太久完全取决于实例规模，随手定一个默认值只会把本来合法的长扫描变成失败。
`scan.timeout` 是另一回事——它约束的是单个 HTTP 请求，不是整次扫描。

### Token 需要什么权限

使用 HTTP access token（`--token`，`BITBUCKET_TOKEN`）。它需要什么权限，由 Bitbucket 的两个事实
决定，两者都在 Bitbucket 10.4 上实测过：

- **Token 永远无法持有全局权限。** 无论由谁来创建，Bitbucket 都不允许创建带 `ADMIN` 或
  `SYS_ADMIN` 的 token。本工具也用不着这样的 token：实例级的问题——谁管理这个实例、谁持有
  license、谁能访问各个仓库——都交给 Bitbucket 自己的权限解析来回答；它对任何已认证用户都会
  作答，并且会自己展开用户组。
- **分支权限和 hook 只对仓库管理员可见。** 建立在它们之上的四条规则，要求 token 在所扫描的
  仓库上带有管理员权限。

| Token 权限 | 能回答的规则 |
|---|---|
| `PROJECT_READ` + `REPO_READ` | 15 条自动判定规则中的 11 条：审批数、审批重置、闲置分支、CI 门禁、未完成任务、线性历史、安全策略、闲置账号、实例管理员、仓库管理员、基础访问权限 |
| `PROJECT_ADMIN` 或 `REPO_ADMIN` | 全部 15 条——在上一行的基础上，再加上提交签名、直接 push、force push 和分支删除 |

两种管理员权限有其一即可：持有 `PROJECT_ADMIN` 加 `REPO_READ` 的 token，和持有 `PROJECT_READ`
加 `REPO_ADMIN` 的 token，同样都能读到分支权限和 hook。token 的权限永远不会超出它所属的用户，
所以该用户自己也必须在这些仓库上拥有这项权限。

只读 token 足以起步：这时四条分支保护规则会输出 `MANUAL` 并说明原因，扫描照常进行。admin token
则*具备*管理能力——bitbucket-bench 从不写入，请求日志收尾的那一行也会交代它发出的每一个请求，
但请像保管管理员凭据那样保管它，因为它本来就是。

**扫描能覆盖的，就是 token 所属用户能看到的。** 该用户读不到的项目根本不会出现，而任何工具都
没法报告它从未见过的东西。要审计整个实例，请用一个专用的服务账号运行，让它能读取（或管理）
每一个项目——通常是把某个用户组授权到每个项目上——并把报告里的仓库数和实例上的实际仓库数
对一对。

Bitbucket 10 的 REST API 默认拒绝密码认证（"Basic Authentication has been disabled on this
instance"）；扫描会如实说明，并指引你改用 token。在仍允许密码的实例上，`--username` /
`--password`（`BITBUCKET_USERNAME` / `BITBUCKET_PASSWORD`）可以代替 token；两者给一种即可，
不要同时给。命令行上输入的优先于环境变量提供的，因此 shell profile 里一个过期的
`BITBUCKET_TOKEN` 不会悄悄盖掉你刚输入的凭据——而只给了凭据、没给 URL 时，它也绝不会被拿去
和之前某次运行保存下来的实例配对使用。

写在 URL 里的凭据——`https://user:pw@bitbucket.example.com`——不会被使用，并且在这个 URL
进入快照或报告之前就被剥掉了。

实例不接受的凭据——打错了、过期了、被吊销了，或者属于另一个实例——会让扫描在抓取任何内容
之前就停下，并以 `2` 退出。这一点并不像听起来那么显而易见：Bitbucket 不会拒绝它不认识的
bearer token，而是把请求当作匿名请求来处理。预检请求的是一个匿名调用方读不到的端点，因此
失效的 token 会在这一步被拦下，而不会产出一份「匿名访客能看到的一切」的报告。

bitbucket-bench **只发 `GET` 请求**。这一点由测试强制保证，不只是靠约定。

### 传输

这个 token 能读取实例上的每一个仓库，因此绝不会以明文形式在网络上传输。`http://` 地址在发出
第一个请求之前就会被拒绝——和本工具报告的那些配置问题不同，凭据一旦泄露就收不回来了。确实处在
可信网络中时可用配置里的 `scan.allowPlaintext` 覆盖；loopback 地址本身豁免，无需任何设置。

如果实例的证书来自**内部 CA**，需要配置 `scan.caFile`：一个 PEM 格式的证书包，在系统信任库
之外额外信任，启动时就会读取并校验。`scan.insecure` 会完全跳过证书校验，是最后的手段，而不是
企业环境该用的设置；两者同时配置会被拒绝，因为那样证书包会悄无声息地失去意义。代理取自
`HTTPS_PROXY` / `NO_PROXY`。

重定向只在仍停留于实例原有的 scheme、主机和端口时才会跟随。Go 在重定向到同一主机时会继续
携带 `Authorization` 头——包括从 `https` 降级到 `http` 的重定向——所以跳往别处的重定向会被拒绝，
并指明两端地址，而不会被跟随。

`allowPlaintext` 和 `insecure` 刻意做成配置而不是 flag：削弱传输安全应当是写进文件、能被人
review 的决定，而不是手指的习惯。这两者都会在报告的 Scan warnings 段以及以这种方式抓取的快照中
留下一行记录。明文抓取的扫描、或者没有验证过应答方身份的扫描，和正常扫描不是同一种证据。

---

## 覆盖范围

15 条自动判定的规则：

| CIS | 规则 | 严重度 | 判定依据 |
|---|---|---|---|
| 1.1.3 | 合并需至少两人审批 | HIGH | Minimum approvals 合并检查，包括从项目继承来的设置 |
| 1.1.4 | 源分支更新后重置审批 | MEDIUM | "Unapprove automatically on new changes"——来自需单独安装的 Atlassian Auto Unapprove 应用 |
| 1.1.8 | 清理闲置分支 | LOW | 分支末次提交超过 90 天 |
| 1.1.9 | 合并前 CI 必须通过 | HIGH | 匹配到默认分支的 Required builds，或 Minimum successful builds 合并检查 |
| 1.1.11 | 未解决的任务阻止合并 | LOW | No incomplete tasks 合并检查 |
| 1.1.12 | 验证提交签名 | MEDIUM | 内置的 Verify Commit Signature hook（8.13+），或在 `signatureHookKeys` 中列出的插件 hook |
| 1.1.13 | 要求线性历史 | LOW | 已启用的合并策略：merge commit、fast-forward（无法快进时回退为 merge commit）和 rebase-and-merge 都不是线性的 |
| 1.1.15 | 禁止直接 push 默认分支 | HIGH | `pull-request-only` / `read-only` 限制，以及谁被豁免 |
| 1.1.16 | 禁止 force push | HIGH | 内置的 Reject Force Push hook，或 `fast-forward-only` 限制以及谁被豁免 |
| 1.1.17 | 禁止删除分支 | MEDIUM | `no-deletes` 限制，以及谁被豁免 |
| 1.2.1 | 发布安全策略文件 | LOW | 默认分支上的 `SECURITY.md` |
| 1.3.1 | 定期清理闲置用户 | MEDIUM | 每个活跃且持有 license 的账号的最后认证时间 |
| 1.3.3 | 实例管理员数量受控（2–5） | HIGH | 按 Bitbucket 自己的解析结果，谁持有 `ADMIN` 或 `SYS_ADMIN` |
| 1.3.7 | 每个仓库至少 2 名管理员 | LOW | 仓库自己的管理员（来自仓库与项目授权），不计实例管理员 |
| 1.3.8 | 收紧仓库默认权限 | MEDIUM | 公开访问，以及所有持有 license 的用户在该仓库上能做什么 |

分支匹配器按 Bitbucket 自己的方式解析——模式匹配完整 ref 名的后缀，模型分支取自仓库的分支
模型——扫描无法解析的匹配器会让该规则输出 `MANUAL`，而不是去猜。空仓库在所有与分支有关的规则上
都输出 `NA`；已归档的仓库在所有关于「变更如何进入」的规则上输出 `NA`，但仍会就「谁能读取它」
进行判定（`skipArchivedRepositories` 会把已归档仓库完全排除在外）。

### 配了豁免的限制，不算保护

上面三条分支权限规则判定的是**这条限制到底约束了谁**，而不只是「有没有人配过一条限制」。一条豁免了 `developers` 组的 `no-deletes`，并不能阻止这个组的人删掉分支；把它报成通过，等于宣称这个仓库比实际更安全——而这正是本工具最不该做的事。

豁免的解析和其他依赖版本的东西一样放在 fetcher 里：用户组会被展开成人，所以规则数的是人而不是组名；一个授予团队组的豁免，同样覆盖日后加入这个组的每一个人。

**只有被覆盖该分支的*每一条*限制都豁免的主体才计入。** 分支的保护是这些限制的并集，所以一个人被「禁止任何修改」豁免、却仍受「禁止删除」约束时，他删不掉分支，就不算一个缺口。这也正是 read-only 能被正确理解的原因：给一条冻结分支指定可以写入的发布负责人，本就是这条限制的正确用法。

判定由两项配置决定：

```yaml
thresholds:
  maxBypassPrincipals: 0        # 允许几个主体持有豁免
allowedBypassPrincipals: [release-bot]   # 不计入上面计数的账号
```

先用 `allowedBypassPrincipals`。构建账号通常确实需要越过限制；如果没有地方声明这一点，阈值就只能调高到足以覆盖服务账号——而那个高度也足以把人藏进去。

当持有豁免的用户组展不开时，这个计数是一个**下界**。下界已经超过阈值仍然判 FAIL——看不见的成员只会让它更大；下界没超过则报 `MANUAL`，因为「token 读不到这个组」不等于「这个组里没有人」。

从 `v0.1.0-rc` 版本升级会有意改变这条判定——见[从 v0.1.0-rc 版本升级](#从-v010-rc-版本升级)。

5 条以「明确记录的人工检查」保留——会被报告、会给出原因，且不计入评分：

| CIS | 规则 | 为何不自动化 |
|---|---|---|
| 1.1.6 | Code owners | Bitbucket DC 没有 CODEOWNERS。Default reviewers 若不配合审批数要求只是提示性的，映射过去会夸大实际约束力。 |
| 1.2.2 | 限制仓库创建 | 需要把全局 Project Creator 权限、用户组成员、各项目授权放在一起解读，且「足够收敛」的口径因部署而异。已列为[路线图](#路线图)的下一步。 |
| 1.2.3 | 限制仓库删除 | Bitbucket 未把删除单独暴露为一项权限，任何判定都只是 CIS-1.3.3 与 CIS-1.3.7 的复述。 |
| 1.3.5 | 强制 MFA | 认证委托给外部 IdP（SAML/Crowd/LDAP），Bitbucket API 完全不暴露因子信息，仅凭 Bitbucket 数据下结论等于编造。 |
| 1.3.9 | 组织 Verified 徽章 | 托管 SaaS 概念，自建实例无对应物，输出 `NA`。 |

```bash
bitbucket-bench list-checks          # 全部规则，含严重度与作用域
bitbucket-bench list-checks --json   # 完整元数据，含修复文案
```

### 从 v0.1.0-rc 版本升级

本版本在一个真实的 Bitbucket Data Center 10.4 上做过核对，而这次核对改变了一些判定。每一处
改动都是纠错，没有一处是对基准要求的新看法。

| 规则 | rc 版本的判定 | 现在 |
|---|---|---|
| CIS-1.1.15/16/17 | 只要存在限制就报 `PASS`，无论多少人被豁免 | 判定限制实际约束了谁；整改期间可以用 `thresholds.maxBypassPrincipals: -1` 恢复旧的判定方式 |
| CIS-1.1.12 | 内置的 "Verify Committer" hook 也报 `PASS`，而它根本不校验签名 | 只有在 `signatureHookKeys` 中以完整 key 列出的 hook 才算数；默认值是内置的 Verify Commit Signature hook |
| CIS-1.1.16 | 受 Reject Force Push hook 保护的仓库报 `FAIL` | `PASS` |
| CIS-1.1.13 | 启用 "Fast-forward" 时报 `PASS`，而目标分支一旦有了新提交，它就会生成 merge commit | `FAIL`；如果接受这种策略，从 `nonLinearMergeStrategies` 中移除 `ff` |
| CIS-1.3.7 | 把实例管理员也算在内，因此只要实例有两名管理员，每个仓库都会通过 | 只统计仓库自己的管理员 |
| CIS-1.3.1, 1.3.3, 1.3.7, 1.3.8 | 对任何 token 都报 `MANUAL`，因为 token 读不到授权表 | 只读 token 即可判定，借助的是 Bitbucket 自己的权限解析 |
| CIS-1.2.1 及所有分支相关规则 | 以配置的默认分支为准，即便它实际并不存在；空仓库被判为失败 | 判定实际存在的分支；空仓库为 `NA` |
| 已归档仓库 | 不出现在报告中 | 会被报告，与变更相关的规则为 `NA` |

可能需要你调整的三处：

- **`signatureHookKeys` 现在填的是完整的 hook key**（`plugin-key:module-key`）。按旧的子串匹配
  写的列表（`gpg`、`signature`）会在启动时被拒绝，错误信息里会给出正确的格式。
- **rc 版本的快照会被拒绝，而不是被读取。** 它们是 schema 1，本构建读取 schema 2。升级后重新扫描
  一次即可：`--last` 从下一次扫描起可用；`diff` 的基线则需要重新采集之后才能用于比较。
- **无法担保自身覆盖范围的扫描会以 `2` 退出**——即一个仓库都没有评估到的扫描，或者无法列出
  某个项目下仓库的扫描。见[在 CI 中使用](#在-ci-中使用)。

---

## 评分

```
score = ⌊ Σ weight(通过) / Σ weight(通过 + 失败) × 100 ⌋
```

其中 `HIGH = 3`、`MEDIUM = 2`、`LOW = 1`。`MANUAL` 与 `NA` 不进入分子也不进入分母。
分数向下取整，从不四舍五入：1512 中得 1510 是 99 分，所以只要有一条失败，就不可能显示为 100，
也不可能通过 `failUnder: 100`。

表格输出会打印算式（`weighted 30/57 (HIGH=3, MEDIUM=2, LOW=1; manual and n/a excluded)`），
让这个数字可核对，而不是只能选择相信。

一个刻意设计的边界情况：当**什么都无法判定**时，分数是 `0` 而不是 `100`。
0 除以 0 不应该被读成「一切健康」。

分数请当趋势线看。真正决定结果是否可接受的，是按严重度统计的失败数。

---

## 输出格式

**`table`**（默认）—— 名字叫 table，实际是一份行式的发现报告，形态和 linter、编译器
一样。每条失败是一条自包含的记录，分数块收尾：

```
bitbucket-bench example  ·  https://bitbucket.example.com  ·  2026-01-15 09:00:00 UTC

PLAT/legacy-billing  CIS-1.1.3 HIGH: Pull requests require 0 approval(s); at least 2 independent
    approvals are needed.
    fix: Set "Minimum approvals" to at least 2 at Repository settings -> Pull requests -> Merge
    checks.
    · requiredApprovers = 0

... 每个「资源 × 规则」的失败各一条记录，按严重度降序 ...

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

... 每条需要人来判断的规则各一行，不同原因各占一行 ...

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

... 报告中每条规则各一行，附厂商文档页 ...

Details: rerun with --details for per-resource findings and full remediation steps, or
--details=<resource|control>[,...] to filter; -o json for the full report.

SCORE 52/100   15 passed  14 failed  19 manual  14 n/a
      14 controls failed
      weighted 30/57 (HIGH=3, MEDIUM=2, LOW=1; manual and n/a excluded)
      scored 29 of 48 findings (60%); 19 could not be evaluated
```

上面这段是 `bitbucket-bench scan --snapshot-in examples/snapshot.json` 在 `COLUMNS=100`
下的真实输出，只在标了 `...` 的地方做了省略。

**一条失败就是一条记录，而且记录是自包含的**：首行是 `<资源>  <规则号> <严重度>: <结论>`，
一行修复缩进在下，证据再以暗色 `·` 行排在其后。你不需要把任何东西记在脑子里、再去翻输出
更下面的某张表 —— 这正是这份报告可以直接 grep 的原因：`grep 'HIGH:'` 就是严重发现的清单，
每一条命中都自带上下文。

**需要人来判断的规则各聚合成一行**，按「规则 + 原因」归并：
`<规则号> MANUAL (<n> <资源>): <原因>`。一个需要判断的问题，铺在多少个仓库上都还是一个
问题；而两个不同的原因各占一行。扫描**读不到**的发现是另一回事，折叠得更彻底 —— 收成扫描
告警上方的那一句话：它们共享同一个成因，讲一次胜过每条规则每个资源各讲一遍。

`Rules` 一节收尾，把每条规则的厂商文档页列一次，而不是在它适用的每条记录下重复同一个
URL —— 另外还带上单条记录承载不了的那个提示：在**所有**仓库上都失败的规则，是一个项目级
设置的事，不是 N 个仓库级设置的事，它那一行会直说（"failing on all 4 repositories —
setting it once at Project settings covers them together"）。真正会被拿去执行的那一行
修复，则跟着发现本身走。

**分数块收尾**，所以结论是最后打印的东西，而用来核对它的每个数字都已经在屏幕上了。
两套计数在这里交汇，第二行就是它们之间的桥：分数按 *finding*（一条规则对一个资源）计，
而它下面那行和结尾的退出行按*规则*计 —— "14 failed" 和 "14 controls failed" 是同一事实的
两面（更大的扫描会写成 "6 controls failed across 23 findings"）。`scored N of M findings`
这一行值得在看分数之前先读：无法判定的 finding 不进入分数的分子，也不进入分母 ——
单看每一条规则这是对的，合起来却有误导性，因为分母被缩小了，于是一个读不到多少东西的
token 反而能从很小的样本里得出很高的分数。`scan.maxManual` 就是把这种情况变成一次失败的
运行，而不是一份好看的报告。

**逐资源的细节在 `--details` 里。** 不带值时，按 trivy 的形态渲染每个资源一节 ——
`Control | Severity | Status | Title | Finding` 表格，每格里带证据和一行修复；扫描告警
移到各节之前，让原因仍然先于症状出现。带值时收窄范围：`--details=PLAT/legacy-billing`
按大小写不敏感的子串匹配资源，`--details=CIS-1.1.9`（或 `1.1.9`）精确匹配规则，
两类混用时取交集。带值时必须用 `=`；一个什么都没匹配上的值是一次报错，而不是一份
看起来很干净的报告。修复建议一节会随过滤一起收窄。

```bash
bitbucket-bench scan --details                      # 所有资源、所有发现
bitbucket-bench scan --details=payments-api         # 一个仓库的完整判定
bitbucket-bench scan --details=CIS-1.1.15,CIS-1.1.16  # 两条规则，无论落在哪个仓库
```

**`UNREAD` 与 `MANUAL` 底层都是 `MANUAL`，按成因拆开。** `UNREAD` 是**这次运行**读不到的
东西；`MANUAL` 是**任何 API 都答不了**的规则（metadata 里 `automated: false`），无论 token
多好都需要人来判断。只有后者会给出修复建议：一条扫描根本没看到的规则，并不能说它配错了，
印出「怎么改设置」等于在说反话。JSON 与 SARIF 里两者都是 `MANUAL`，因为那才是规则返回的东西。

**修复跟着发现走，完整段落留给 `--details`。** 行式报告里每条记录都自带一行 `fix:`，
所以处理一条发现不需要翻到别处的清单去；只有文档链接被推迟到 `Rules`
（CIS 基准的落地页每条规则都一样、指认不了任何一条，所以从不占行）。

`--details` 才打印完整的修复段落 —— 设置路径、项目级的等价做法、配置项名称 —— 分成两节，
因为两类条目要的东西不同：`Remediations` 是配置错了、怎么改；`Manual review` 是
API 判定不了、需要人来确认。混成一个清单会把十条读成十个坏东西，其实坏的只有六个。
它的逐资源表格里，每条发现的 `Finding` 单元格还在结论旁边带着证据和一行 `fix:`。

`--no-remediations` 在两种布局下都会把这些一起去掉：行式报告里的 `fix:` 行和 `Rules` 节，
以及 `--details` 的那两节。

宽度在 stdout 是终端时来自终端本身；导出的 `COLUMNS` 可以覆盖它，管道、重定向与
`--output-file` 得到 80。无论来源如何都夹在 60–160 之间。上限约束的是散文：弹性表格列
从不超出内容的自然宽度，所以宽终端上表格会停在自然宽度 —— 足够让最长的规则标题单行
显示 —— 而不是摊开。任何一行都不会超出这个宽度 —— 折了行的边框就不再像边框 ——
所以一个过长的设置路径会在列边缘被切断，而不是把框架顶歪。

颜色只是强化，所以输出被管道、重定向或设置了 `NO_COLOR` 时什么都不会丢。
`--details=<值>` 能过滤表格；更精细的过滤是 JSON 的活：

```bash
bitbucket-bench scan -o json | jq '.findings[] | select(.status == "FAIL")'
bitbucket-bench scan -o json | jq -r '.findings[] | select(.status == "MANUAL") | .checkId'
bitbucket-bench scan 2>&1 >/dev/null                  # 这次扫描自己说了什么
```

**stderr** 上的行 —— 请求日志、解释退出码的那一行 —— 仍然带等宽的
`[INFO]`/`[WARN]`/`[FAIL]`/`[PASS]` 标签，因为它们是和别的程序的输出交错着读的，
也没有表格可归属。stdout 上的报告则不带。

`--max-resources` 限制多少个资源能有自己的 `--details` 一节（默认 `0`，即每个都有）；
行式报告不画逐资源的节，所以它只能与 `--details` 搭配使用。真的截断时，报告会说明省略了
多少个，并指向能拿到全部的格式 —— `3 more resources with findings not shown
(--max-resources 1); use -o json for all of them.`。它只影响这一种格式：`json` 和 `junit`
始终携带完整集合，`sarif` 则在 code scanning 5,000 条结果的上限以内携带全部结果。

通过和不适用的规则只计入汇总，不会逐条列出——报告是一份待办清单。`--show-passed` 会把它们
也列出来，当你的问题是「这个实例已经做对了哪些」时用它。

颜色只在 stdout 是终端时启用，并遵守 `NO_COLOR`；`--no-color` 是显式关掉它，
适用于终端被某个会保留转义序列的东西捕获的场景。

**`json`** —— 完整报告：每条发现、证据、该规则为何存在、修复建议与评分明细。
报告文件按 `0600` 写入，和快照一样——渲染出来的报告同样是一份实例弱点地图。

**`sarif`** —— SARIF 2.1.0，供 GitHub code scanning 及其他 SARIF 消费方使用。只输出失败项与
需人工复核项；通过与 N/A 无需处理，因此省略。code scanning 会丢弃所有不带 `physicalLocation`
的结果——上传显示成功，Security 标签页却一片空白——所以每条结果都带有一个：一条稳定的路径，
依次标明平台、实例和仓库（`bitbucket-dc/bitbucket.example.com/PLAT/payments-api`）。这条路径不必
在你上传到的仓库里真实存在；告警会显示发现所在的位置，旁边的 `logicalLocation` 则指明是哪个
仓库。除此之外：

- `MANUAL` 结果指向一条单独的规则（`CIS-1.3.5/manual`），且不带 `security-severity`，因此
  「需要有人来检查」永远不会显示成一条 High 告警。
- `automationDetails.id` 由 bench 名和实例主机名组成，因此两个实例上传到同一个仓库时，不会
  互相关闭对方的告警。
- 结果数以 code scanning 的上限 5,000 条为限，严重度高的优先保留，并附一条 notification 说明
  省略了多少条；任何 API 都无法判定的规则只输出一条结果，而不是每个仓库一条。
- 扫描告警作为 invocation notification 输出；无法列出某个项目、或者一个仓库都没评估到的扫描会
  报告 `executionSuccessful: false`，因此不完整的扫描绝不会被误当作干净的结果。被接受的发现带有
  SARIF `suppression`，code scanning 会把它显示为已忽略（dismissed）。

**`junit`** —— JUnit XML，供原生支持展示测试结果的 CI 系统使用：Jenkins 的 JUnit publisher、
Azure Pipelines 的 `PublishTestResults`、GitLab 的 test report。每条规则一个 test suite，它评估过的
每个资源一个 test case——每个仓库各一个，实例级规则则是 `instance`——因此测试视图的分组方式与报告
一致。未被接受的 `FAIL` 记为 failure，类型标为它的严重度，并附带证据和修复方法；`MANUAL`、`NA`
和已被接受的失败记为 skipped 并注明原因，因此各项总数加起来正好等于全部发现。无法列出某个项目、
或者根本没有评估到任何仓库的扫描，会额外加入一个属于它自己的失败 test case——它漏掉的那些仓库
根本没有 test case 可以失败。

**被接受的发现**（见[例外](#例外)）会出现在每一种格式里：在 table 报告中有单独的标题，在 JSON
中带 `waiver`，在 SARIF 中是一条 suppression，在 JUnit 中记为 skipped。

工具输出只有英文。文档是双语的，维护者也并非都以英语为母语，所以这是一个决定而非疏漏：
每条规则的判定文字是规则自己生成的，换一种语言不是加一张字符串表，而是在全部二十条规则里
各复制一份消息拼装逻辑。半套翻译——标题是一种语言、发现描述是另一种——比不翻译更难读。

---

## 配置

任何「讲道理的人可能有不同意见」的阈值都可配置；描述部署本身而非某一次运行的设置——
退出阈值、传输、并发、进度——也都住在这里而不是 flag 里。策略里不写死任何数字。

```bash
bitbucket-bench init            # 写出一份带完整注释的 bitbucket-bench.yaml
bitbucket-bench scan            # 自动在工作目录里找到它
```

查找顺序：给了 `--config` 就用它；否则找工作目录的 `bitbucket-bench.yaml`（或
`.bitbucket-bench.yaml`）——项目自己的文件，仓库提交进去给 CI 用的那份；再否则找用户配置
目录（`BITBUCKET_BENCH_CONFIG_DIR` 或平台默认）下的 `config.yaml`。自动找到的文件会在
stderr 上点名——阈值悄悄来自某个文件的扫描，其退出码是无法解释的。`init` 不会覆盖
已存在的文件。

> **从 `v0.1.0-rc` 版本升级。** 这些名字随工具一起改了：`scm-bench.yaml` 变为
> `bitbucket-bench.yaml`，`SCM_BENCH_CONFIG_DIR` 变为 `BITBUCKET_BENCH_CONFIG_DIR`，
> 用户配置目录从 `<config>/scm-bench` 移到 `<config>/bitbucket-bench`。不保留对旧名字的
> 兼容——请重命名文件，或用 `--config` 直接指定。发生变化的判定见
> [从 v0.1.0-rc 版本升级](#从-v010-rc-版本升级)。

一次性的改动用 `--set`，不用碰任何文件——优先级 `--set` > 文件 > 默认值：

```bash
bitbucket-bench scan --set scan.failOn=none          # 只影响这一次
bitbucket-bench scan --set thresholds.minApprovers=1 --set scan.concurrency=2
```

值按 YAML 解析，数字、布尔、时长（`30s`）、流式序列（`exclude=[CIS-1.1.8]`）
都可以；未知的键会和写在文件里一样直接拒绝扫描。

完整带注释的配置见 [`examples/config.yaml`](examples/config.yaml)。最常调整的几项：

```yaml
scan:
  failOn: high           # 达到该严重度即退出 1：high、medium、low、none
  maxManual: -1          # 无法判定的规则超过该百分比即退出 1；-1 关闭
  concurrency: 8         # 并发抓取的仓库数，所有项目共用这一上限
  timeout: 30s           # 单请求超时；maxDuration 限制整次扫描
  caFile: /etc/ssl/certs/corp-root-ca.pem   # 内部 CA，用它代替 insecure
  cache: true            # 留存快照供 --last 复用；false 则不落盘
  allowIncomplete: false # 接受未能列出全部项目的扫描

thresholds:
  minApprovers: 2        # CIS-1.1.3
  staleBranchDays: 90    # CIS-1.1.8
  minOrgAdmins: 2        # CIS-1.3.3
  maxOrgAdmins: 5
  inactiveUserDays: 90   # CIS-1.3.1
  maxBypassPrincipals: 0 # CIS-1.1.15/16/17；-1 关闭该项判定

# 分支限制的豁免本就该落在这些服务账号上，这样上面的阈值对「人」
# 可以一直保持为零。
allowedBypassPrincipals: [release-bot]

# 校验提交签名的 hook 的完整 key。默认值是 Bitbucket 自 8.13 起内置的 hook；
# 更早的版本请加上 Marketplace 插件的 key，并且加的时候把内置的 key 也留在列表里。
signatureHookKeys:
  - com.atlassian.bitbucket.server.bitbucket-bundled-hooks:verify-commit-signature-hook

# 有意公开代码的实例
allowPublicRepositories: false

exclude: [CIS-1.1.13]    # 或用 include: 只跑子集
```

配置文件只需写你要改的部分，未出现的键保持默认。序列是唯一的例外：YAML 会整体替换列表，
因此设置 `signatureHookKeys` 是替换整个列表而非追加。

无法识别的键会直接报错，而不是被忽略。把 `minApprovers` 写成 `minApprover` 一样能解析成功、
什么都不改，却会产出一份读者以为「按我的阈值判定过」的报告——所以扫描宁可拒绝启动。
`include`、`exclude` 以及例外里指向不存在规则的条目同理，负数阈值同理，任何列表里的空条目同理，
hook 列表里不是完整 `plugin-key:module-key` 的条目也同理——这里曾经按子串匹配，结果匹配上了
"Verify Committer"，一个根本不校验任何签名的 hook。

`permissionRank` 是大多数人从不改、但应该知道它存在的一个字段：它定义了 Bitbucket 各个权限名
之间的高低关系，`maxDefaultPermission` 就是拿它来比对的。如果你的 Bitbucket 版本报出一个这张表
里没有的权限，CIS-1.3.8 会输出 `MANUAL`，而不是去猜它排在哪。和上面的序列不同，它是映射，
按键合并而不是整体替换，所以只写一个权限不会影响其余的。

### 例外

每个组织都会有一些决定暂时容忍的发现——比如某个遗留仓库，在迁移完成之前，它的发布工具都
离不开 merge commit。如果没办法把这一点说出来，就只剩两个选择：一条永远是红色的流水线，或者
`failOn: none`——而从那以后，这道门禁什么也拦不住。例外就是把这件事明确写下来，并附上理由和
截止日期：

```yaml
exceptions:
  - control: CIS-1.1.13
    resources: [PLAT/legacy-billing, PLAT/legacy-*]   # glob；实例级规则写 "instance"
    reason: Release tooling needs merge commits until the migration lands
    owner: platform-team@example.com                  # 可选
    expires: 2027-03-31                                # 必填
```

被接受的发现**仍会被报告、保留原有状态、仍计入分数**——分数描述的是实例，而接受一条发现
并不会改变实例。它不再做的，是因 `scan.failOn` 让运行失败；对于已经有人手工复核过的 `MANUAL`
发现，则是不再计入 `scan.maxManual`。报告会把它列在 *Accepted by exceptions* 标题下，并附上
理由、负责人和日期。

理由和到期日是必填项：缺了它们的例外，正是「已接受的风险」变成「被遗忘的风险」的途径。
`expires` 所写日期的次日起，例外即告失效，它覆盖的发现会重新让运行失败；一条什么都没接受的
例外——发现已经修好了，或者仓库改了名——同样会被报告出来，这样例外清单就不会悄无声息地腐烂。
这两种情况无论输出详细程度如何，都会打印到 stderr 上。

---

## 在 CI 中使用

阈值写在与流水线一起提交的 `bitbucket-bench.yaml` 里，这样流水线和本地笔记本读的是同一份文件，
彼此之间无从产生分歧：

```yaml
# bitbucket-bench.yaml
scan:
  failOn: high
  maxManual: 40
```

**GitHub Actions** —— 把 SARIF 上传到 code scanning。扫描发现问题时会以 `1` 退出，这正是它的
用途；但这样一来 job 会在上传之前就结束，所以把失败推迟到最后一步。上传需要在 job 的
`permissions` 里给出 `security-events: write`；私有仓库还需要 `actions: read` 和
`contents: read`。

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

**Jenkins** —— JUnit publisher 会把结果呈现在构建的测试视图里：

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
      // 只负责展示报告；构建结果此前已由扫描的退出码决定。
      junit testResults: 'bitbucket-bench.xml', allowEmptyResults: true, skipMarkingBuildUnstable: true
    }
  }
}
```

**Azure Pipelines** —— 同一个文件，通过 `PublishTestResults` 发布：

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
    failTaskOnFailedTests: false   # 门禁由扫描的退出码决定
```

在每个示例里，决定构建结果的都是扫描的退出码，测试报告只负责展示。测试报告担不起门禁：JUnit
没有严重度的概念，每条未被接受的 `FAIL` 都是一个失败的测试——`LOW` 也不例外——所以如果让
publisher 按失败的测试来判定构建失败，它卡的标准就比 `failOn` 更严。而一旦丢掉扫描的退出码，
突破了 `maxManual` 或 `failUnder` 的扫描就会被放行：要么是一次黄色构建——Jenkins 的 `junit`
步骤把失败的测试标为 `UNSTABLE` 而不是失败——要么在恰好没有测试失败时，干脆是一次绿色构建。

退出码：

| 码 | 含义 |
|---|---|
| `0` | 扫描完成，未触发任何阈值 |
| `1` | 扫描完成，触发了某个阈值 |
| `2` | 扫描没能完成——或者无法担保自己的覆盖范围 |

配置里有三个设置会导致退出码 `1`，它们问的是不同的问题：

| 设置 | 问的是 |
|---|---|
| `scan.failOn` | 有没有达到这个严重度的失败项？`high`（默认）、`medium`、`low`、`none` |
| `scan.failUnder` | 分数可以接受吗？0-100 的数字，`0` 表示关闭 |
| `scan.maxManual` | 这次扫描到底看到了多少，够不够形成判断？百分比，`-1` 表示关闭 |

建议从 `failOn: high` 起步，清完第一轮发现后再收紧；那些不会被修复的发现，交给[例外](#例外)。

`scan.maxManual` 是最值得尽早设上、也最不直观的一个。无法判定的规则是被排除出分数，
而不是计为失败——单看每一条规则这是对的，合起来却有误导性，因为它缩小了分母。
于是一个丢了权限的 token 反而可能比正常的 token 得分**更高**：在自带的示例快照上，
把所有可读字段清空会让分数从 52 涨到 77。`scan.failUnder` 拦不住这种情况，
`scan.maxManual` 可以。

退出码 `2` 还涵盖两种「跑完了、却无法担保覆盖范围」的扫描：一种是一个仓库都没评估到——
`--project` 指向 token 读不到的项目，或者 token 什么都看不到；另一种是无法列出某个项目下的
仓库——这些仓库会从报告中缺席，而报告里没有任何发现会指出这一点。报告照样会写出；如果你确实
有意接受第二种情况，可以设置 `scan.allowIncomplete: true`。`--repository` 指向不存在的仓库时
同样会报错，而不是扫描零个仓库然后判定通过。

### 把「抓取」和「判定」拆开

快照是自包含产物，因此持有凭据的步骤与执行判定的步骤可以是不同步骤、不同机器、不同时间：

```bash
# 在能访问 Bitbucket、持有 token 的 runner 上
bitbucket-bench scan --snapshot-out snapshot.json -o json --set scan.failOn=none

# 之后在任何地方——无需凭据，无需网络；默认的 failOn: high 生效
bitbucket-bench scan --snapshot-in snapshot.json -o sarif
```

对归档快照重跑策略，还能看出换了阈值之后结论会如何变化，而不必再碰实例一次。

快照以 `0600` 写入：它是一份精确描述实例薄弱点的地图。报告同样如此，保存的
`instance.yaml` 更是如此——它装的是一份活的凭据，是同一条理由的最强版本。
这是 Unix 权限位，只在类 Unix 系统上生效——Windows 没有对应的位，Go 只把它映射成只读属性，
文件真正的访问控制来自它从所在目录继承的 ACL。在 Windows 上，请把快照和报告放到一个本身
已经受限的位置，保存 token 之前更要三思。

### 追问，不必再扫一次

总览通常会引出下一个问题——到底是*哪些*仓库没过 CIS-1.1.3？——回答它不该再让实例付出
一次扫描的代价。因此每次联网扫描都会自动留下它的快照（`0600`，每个实例主机一个文件，
放在用户配置目录的 `cache/` 下），`--last` 重新渲染最近的那份：

```bash
bitbucket-bench scan                     # 总览；快照顺手留了下来
bitbucket-bench scan --last --details    # 展开它，完全不碰实例
bitbucket-bench scan --last -o json      # 或者换一种格式再问一遍
```

`--last` 运行会在 stderr 上说明快照来自哪个实例、多久之前——超过一天会升级为警告，
因为把它当作当前状态已经是猜测——退出阈值也照常生效。塑造一次全新抓取的 flag
（`--url`、`--project`、`--snapshot-in` 等）与它同用会被拒绝，理由一如既往：产出的报告
会和那些 flag 描述的扫描看起来一模一样，实际却不是。演示模式永远不会写入缓存，
所以 `--last` 不可能把内置示例冒充成你的实例。

缓存和报告是同一份薄弱点地图，也用同一个 `0600` 保护（目录 `0700`）。如果它压根就
不该存在，配置里 `scan.cache: false` 让此后的快照全部不落盘，删掉缓存目录就忘掉已有的；
`--snapshot-out` 依然是显式形式，用于自己决定快照落在哪里。

### 捕捉姿态回退

分数要当趋势线看，而趋势需要两个点。`diff` 比较两份快照并报告变化：

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

... 每一类变化一张表：New failures、Fixed、Other changes、Gone ...

How to fix the regressions

  CIS-1.1.17  Repository settings -> Branch permissions -> Add restriction: select the default
              branch and enable "Prevent deletion". Then review the restriction's exempt users ...
```

`diff` 仍然是表格，用的正是 `scan --details` 的那个渲染器，所以两个子命令读起来像同一个
程序。比较这件事天然是网格：每一行都是同四项事实、只是换了一条规则 —— 这正是表格胜过
行式的那个场合。
`Gone` 是每个资源一条，而不是每条规则一条：删掉一个仓库是关于这个仓库的一个事实，
把它报二十遍只会把这条命令本该凸显的回退埋掉 —— 它的 Control 列写 `-`，
因为表格没法为某一行去掉一列。

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
bitbucket-bench scan --snapshot-out today.json -o json --set scan.failOn=none
bitbucket-bench diff baseline.json today.json
```

比较来自两个不同实例的快照会被拒绝，除非用 `--allow-other-instance` 表明这是有意为之：
否则每个仓库都会既显示为「已消失」又显示为「新出现」，看起来像结论，其实不是。

---

## 工作原理

```
Bitbucket REST  ──►   fetcher   ──►  snapshot.json  ──►   Rego 策略   ──►   报告
                  (Go，仅 GET)      (归一化)          (每条规则一个)   table/json/sarif/junit
```

这个切分是严格的，也正是这个工具可维护的原因：

- **fetcher 不做任何判定**。它只做归一化，并记录哪些内容没能读到。
- **策略不发 HTTP**。它读一份 JSON 文档，返回一个结论。

包结构、策略契约、怎么新增一条规则、怎么跑那两套测试、怎么发版，都在
[CONTRIBUTING.md](CONTRIBUTING.md)（英文）。

---

## 遵循的规范

上面这一切的形态——四种状态、无法判定就报 `MANUAL` 的铁律、`metadata.json` 的字段、
评分公式、快照 schema、SARIF 指纹键——都写在家族的伞形仓库
[scm-bench](https://github.com/scm-bench/scm-bench) 里。构建期不会从它引入任何东西：
那是规范，不是库，本仓库依然是自包含的。

| 文档 | 约定了什么 |
| --- | --- |
| [bench contract](https://github.com/scm-bench/scm-bench/blob/main/docs/bench-contract.md) | 所有 bench 共通的部分，无论审计什么平台 |
| [SCM 快照 schema](https://github.com/scm-bench/scm-bench/blob/main/docs/scm-snapshot.md) | `snapshot.json` 的结构，与其他审计源代码管理平台的 bench 共享 |
| [配置约定](https://github.com/scm-bench/scm-bench/blob/main/docs/config-conventions.md) | 配置文件如何被发现、各项如何合并 |

规范文档只有英文版。**用**这个工具不需要读它们，**改**这个工具之前值得读一遍：
这里的一个判定必须与任何其他 bench 的判定含义相同，而含义就写在那里。

---

## 实测验证

fetcher 的单元测试能证明它与一个替身 Bitbucket 的行为一致，却证明不了这个替身与真正的
Bitbucket 一致。[`hack/e2e`](hack/e2e) 补上了这道缺口：它用 Atlassian 提供的 timebomb license
在 Docker 里启动一个真实的 Bitbucket Data Center，预置四个项目和十三个仓库，每个仓库都刻意带有
一处偏差——带豁免的限制、Verify Committer hook、后缀模式、模型分支、两个 deploy key、一个已归档的
仓库、一个从未 push 过的默认分支——然后用三个 token 去扫描它：一个具备管理能力的、一个只读的，
以及一个最小权限用户的。预期判定是根据 fixture *实际是什么*写下来的，而不是照抄工具打印的结果；
只要出现任何差异，或者有任何一次扫描不是只读的，这套测试就会失败。

| Bitbucket Data Center | 结果 |
|---|---|
| 10.5.0 | 三个 token 下的所有判定都符合预期 |
| 10.4.1、10.4.3 | 三个 token 下的所有判定都符合预期 |
| 9.4.24（LTS） | 三个 token 下的所有判定都符合预期 |
| 8.19.29（LTS） | 三个 token 下的所有判定都符合预期 |

有一项预期因版本而异，因为这些版本本身就不一样：8.19 和 9.4 不报告账户的创建时间，所以无法区分
一个从未登录的账户是新建的还是休眠的，CIS-1.3.1 在这两个版本上会把它交给人工确认；10.4 和 10.5
会报告创建时间，这条规则就能自动判定。

[从 v0.1.0-rc 版本升级](#从-v010-rc-版本升级)中列出的每一处问题，都是这样发现的。用这套测试去跑
另一个版本——设置 `IMAGE=atlassian/bitbucket:<tag>`，然后按 [hack/e2e](hack/e2e/README.md) 的说明
操作：先准备 license 文件，再依次运行 `up.sh`、`seed.sh` 和 `verify.sh`——并反馈哪里不一样，是最有
价值的贡献。

---

## 路线图

**下一步** —— 以 opt-in 方式支持个人仓库（`~user`），因为 `/projects` 不会列出它们；既然权限解析已不再需要
管理员 token，就根据谁持有 Project Creator 来判定 CIS-1.2.2；把 default reviewers 作为 CIS-1.1.6
的部分信号。

**在别处** —— 其他平台是各自独立的仓库，而不是往这里加 fetcher：
[azure-devops-bench](https://github.com/scm-bench/azure-devops-bench) 和
[jenkins-bench](https://github.com/scm-bench/jenkins-bench)。快照 schema 本就是平台中立的，
规则也声明了各自适用的平台，因此这里写的一条规则可以被 SCM 领域的另一个 bench 直接继承，
而不必重写。

---

## 许可证

Apache 2.0，见 [LICENSE](LICENSE)。
