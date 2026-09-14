# 发布签名与升级校验

QControlHub 的控制面同时是**分发者**：它把 Agent 可执行文件和安装脚本交给全新节点，也把新的 Agent 二进制推给已上线节点。因此控制面不能同时充当自己的信任根——校验和与被校验的文件走同一条连接，谁能改响应谁就能同时改这两样。

本方案把信任根移出控制面：**发布私钥只在发布机（或受保护的 CI 环境）使用，控制面永远拿不到它。** 已固定公钥的 Agent 和可信安装器会拒绝被替换且没有有效签名的下载。

安装器本身必须先从独立可信的发布渠道取得并核对；从已被攻破的控制面下载脚本再直接执行，无法靠脚本内部的验签建立信任。签名认证发布内容，不阻止重放仍由同一密钥签名的历史版本。

这也不是“控制面失陷后节点完全安全”的隔离机制：控制面仍能下发原有权限允许的配置、停启和内核管理任务。本机制约束的是安装资源与 Agent 自升级的发布真实性，不替代控制面访问控制、节点服务沙箱或第三方内核供应链校验。

覆盖范围：

| 链路 | 校验内容 |
| --- | --- |
| 初始安装（`install-agent.sh`） | Agent 二进制 + 全部安装脚本与示例配置，逐个对照已签名清单 |
| 已上线节点升级（`upgrade-agent` 任务） | 新 Agent 二进制的摘要，且**版本必须等于控制面版本** |

## 1. 信任边界

- **私钥**：只在发布机或受保护的 CI secret 里，绝不进入控制面容器、`.env` 或数据库。
- **公钥**：随发布物公开，安装在每个节点的 `QCH_RELEASE_PUBLIC_KEY`（`/etc/qcontrolhub/agent.env`）与安装命令的环境里。
- **签名产物**（可以公开，不含秘密）：
  - `SHA256SUMS`：`sha256sum -c` 格式的清单，安装器用 `openssl pkeyutl -verify` 校验它；
  - `SHA256SUMS.sig`：清单的 Ed25519 分离签名（base64 文本）；
  - `release-manifest.json`：带**版本标签**的签名清单，Agent 升级时用它绑定版本。

> 注意：Ed25519 是 PureEdDSA，验签必须用 `openssl pkeyutl -verify -rawin`。`openssl dgst -sha256` 对 Ed25519 直接报 `Explicit digest not allowed with EdDSA operations`，不会静默通过，但用错命令会浪费排查时间。

## 2. 一次性准备

在**发布机**上生成密钥对：

```bash
make signing-key RELEASE_KEY=release.key
```

产出：

- `release.key`（0600，**不要**提交、不要放进镜像、不要上传到控制面）
- `release.key.pub`（raw base64 公钥）
- `release.key.pub.pem`（PEM 形式，安装器与节点用这个）

把 `release.key.pub.pem` 发布出去（例如随 Release 附件），并在每台节点写入：

```
QCH_RELEASE_PUBLIC_KEY=/etc/qcontrolhub/release-key.pub.pem
```

将该 PEM 路径传给安装命令后，安装器会把验证过的公钥保存到 Agent 配置目录，并写入 `agent.env`（OpenRC 同时写入服务环境）。后续更新默认沿用已保存的公钥，不会因漏传环境变量而关闭验签。Agent 和控制面也兼容早期使用的 inline raw-URL base64 公钥，但安装器使用 PEM 文件。

## 3. 每次发布

```bash
# Docker 部署：导出并签名镜像实际使用的 Agent，不能签名另一份宿主机编译产物。
make release-image-checksums VERSION=1.2.3 RELEASE_KEY=release.key
```

`dist/release/` 下会得到 `SHA256SUMS`、`SHA256SUMS.sig`、`release-manifest.json`。随后用同一版本、同一份产物构建控制面和 web 镜像：

```bash
docker build --target qcontrol-plane \
  --build-arg VERSION=1.2.3 \
  --build-arg RELEASE_ARTIFACTS=dist/release .
docker build --target qcontrol-web \
  --build-arg VERSION=1.2.3 \
  --build-arg RELEASE_ARTIFACTS=dist/release .
```

原生二进制部署可用 `make release-checksums VERSION=1.2.3` 签名 `bin/qagent`；也可用 `RELEASE_AGENT=/path/to/qagent` 指定真正要分发的文件。Docker 流程从与运行镜像相同的构建阶段导出 Agent，固定构建镜像摘要，避免宿主机与容器 Go 版本不同导致摘要不一致。

**清单范围**：清单包含 systemd 和 OpenRC 安装资源的并集。节点仅下载自己使用的一套，验签后提取所需条目逐个校验；所需条目缺失、重复或内容不符都会失败，不使用会忽略必需文件的 `--ignore-missing`。`deploy/` 含 `deploy/tests` 等非发布工具，不能整体签进去。

`release-checksums` 依赖 `check-install-assets`，后者运行 `deploy/tests/release-assets.sh`：从 `deploy/remote/install-agent.sh` 自己的下载循环推导资产列表，与可签名集合逐条比对，不一致就拒绝出包。往安装器里新增一个下载而不更新发布集合，会被这里挡住。

控制面镜像自动携带并读取 `/usr/local/lib/qcontrolhub/release/release-manifest.json`，标准 Compose 部署不需要额外挂载清单。启动时检查签名、实际分发的 Agent 摘要、大小和控制面版本；错版本或错文件会使启动失败，而不是先通过健康检查、等节点升级时才报错。没有独立固定控制面公钥时，使用清单内公钥的检查仅用于发现打包错误，**不是发布者身份认证**；节点仍用独立部署的固定公钥验签。

原生部署或需要覆盖镜像清单时，设置 `QCH_RELEASE_MANIFEST_PATH` 指向对应文件；可同时挂载公钥并设置控制面的 `QCH_RELEASE_PUBLIC_KEY`。显式清单路径不存在、公钥错误，或固定公钥却没有清单，都会在启动时失败。

```bash
make check-install-assets        # 只校验：安装器下载集 == 发布资产集
bash deploy/tests/release-assets.sh --files   # 打印本次要签的文件
```

**版本一致性**：`release-checksums` 把 `VERSION` 同时写进 `-release` 与 `-agent-version`，而控制面把自身构建版本通过已认证的 WSS 策略下发给 Agent。升级时 Agent 要求签名清单里的 Agent 版本**等于**面板报告的版本，否则拒绝安装。这能发现混装、错签或版本标签被改写，但失陷控制面仍能同时重放旧清单并报告旧版本，因此不等于防回退；节点本地最低版本与密钥撤销策略不在本方案内。

**开发兼容性**：全新检出没有 `dist/release/` 时，直接构建 web 镜像和 `make dev-up` 仍可运行未签名开发版本；固定公钥的节点不会接受这种发布。如果目录存在，则必须包含完整三件套，缺任一文件会使镜像构建失败。不要把上次版本遗留的 `dist/release/` 混入新构建。`dist/` 已被 gitignore，不要提交。

当前清单只描述一个平台的 Agent 二进制，节点必须与分发镜像的平台一致；多架构发布需要分别导出并签名每个平台的 Agent，不能复用另一架构的摘要。

## 4. 在 GitHub Actions 里签名

现有 CI 的正式发布路径为 `main → images → sign-release → publish → promote`：

1. PR 构建使用一次性测试密钥，验证真实镜像的安装资源、节点注册、WSS 版本策略和 Agent 下载验签；不会使用或发布生产密钥。
2. `sign-release` 进入 `release-signing` 环境，导出镜像实际使用的 Agent 并生成一份签名包。**没有 `RELEASE_SIGNING_KEY` 就在发布任何镜像前失败**，不再只跳过 web。
3. `publish` 的三个镜像任务只下载同一份公开签名包，分别发布 commit SHA 标签；它们不接触私钥。
4. 三个镜像全部发布成功后，`promote` 才更新 `latest`。镜像仓库不能原子更新三个标签，生产部署应把 `QCH_IMAGE_TAG` 固定为同一个完整 commit SHA，避免拉取到正在晋升的不同版本。

上线前由仓库管理员创建 `release-signing` 环境，设置 Required reviewers、禁止自审，并将部署分支限制为 `main`；把 `release.key` 内容保存为该环境的 `RELEASE_SIGNING_KEY`，不要保留仓库级同名私钥副本。仅在 YAML 写出环境名不会自动获得审批保护，审批人仍需核对本次提交及工作流。公钥应通过独立可信渠道分发给节点；工作流不自动修改节点公钥。

私钥临时文件位于构建上下文之外，签名步骤结束时删除。Actions 上传的 `signed-release-<commit>` 只包含公开产物，保留 30 天；长期存档应在发布渠道保留对应签名包与公钥。若尚未准备生产密钥，先保持 PR 未上线，不要以临时生成密钥或跳过验签绕开发布失败。

> 「放在 GitHub 上」本身不是信任根：`raw.githubusercontent.com/.../main/...` 指向可变分支，谁拿到账号或 token 就能改。**可变分支 + 版本号不等于可信**，必须配合签名，或使用不可变的 tag / commit SHA。

## 5. 运维开关

| 变量 | 作用 |
| --- | --- |
| `QCH_RELEASE_PUBLIC_KEY` | 安装器与 Agent 的公钥路径。设置即表示“本部署有签名发布”，此时缺失或无法验证的签名是**硬失败** |
| `QCH_ALLOW_UNSIGNED_RELEASE=true` | 本次安装显式放弃校验并打印告警；不会清除已保存的 Agent 公钥，也不关闭 Agent 后续升级验签 |
| `QCH_RELEASE_MANIFEST_PATH` | 控制面读取 `release-manifest.json` 的路径；供 Agent 升级校验使用 |
| `QCH_RELEASE_PUBLIC_KEY`（控制面侧，可选） | PEM/raw-URL 公钥文件路径，或 inline raw-URL 公钥；与控制面读取的清单做一次启动期自检 |

未设置 `QCH_RELEASE_PUBLIC_KEY` 的节点保持原有行为（升级不验签，但会记录告警），因此既有节点不会因为引入校验而无法升级。**建议在下次重装或轮换时给所有节点加上公钥。**

安装器省略协议时使用 HTTPS。显式远程 HTTP/WS 还必须设置 `QCH_ALLOW_INSECURE_LIVE=true`，与 Agent 的启动规则一致；仅供可信网络。`QCH_TLS_CA_FILE` 只用于 HTTPS/WSS 证书验证，不能加密 HTTP，也不会绕过明文保护。`localhost`、`127.0.0.1` 和带方括号的 `[::1]` 是回环例外，`127.*` 开头的域名不是。

本地可用 `make release-image-test VERSION=test-release` 验证真实镜像链路。它使用一次性密钥、隔离 PostgreSQL 和回环端口，结束后清理测试容器、镜像及密钥，不安装宿主机 Agent 服务。

## 6. 故障排查

| 现象 | 原因 |
| --- | --- |
| `RELEASE SIGNATURE VERIFICATION FAILED` | 清单不是由该公钥签名：轮换密钥后未分发新公钥，或下载被篡改 |
| `do not match the signed checksum list` | 签名有效但某个文件内容与清单不符：控制面分发的文件与签名产物不是同一批 |
| `failed to download the signed checksum list` | 控制面没有提供 `SHA256SUMS`：镜像未带 `dist/release/` 产物 |
| `does not match control plane version` | 签名清单的 Agent 版本与控制面构建版本不一致：用同一个 `VERSION` 重新签名 |
| `invalid Agent release package` | 控制面发现清单缺失、签名无效、实际 Agent 与清单不符或版本混装，检查镜像与挂载文件 |
| `openssl is required to verify the signed release` | 节点缺少 openssl；安装它，或显式设置 `QCH_ALLOW_UNSIGNED_RELEASE=true` |

轮换发布密钥属于一次性人工动作：新公钥要有意识地分发到每个节点，再开始用新私钥签名；否则所有校验都会按设计失败。
