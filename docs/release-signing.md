# 发布签名与升级校验

QControlHub 的控制面同时是**分发者**：它把 Agent 可执行文件和安装脚本交给全新节点，也把新的 Agent 二进制推给已上线节点。因此控制面不能同时充当自己的信任根——校验和与被校验的文件走同一条连接，谁能改响应谁就能同时改这两样。

本方案把信任根移出控制面：**发布私钥只在发布机（或受保护的 CI 环境）使用，控制面永远拿不到它。** 已固定公钥的 Agent 和可信安装器会拒绝被替换且没有有效签名的下载。

安装器本身必须先从独立可信的发布渠道取得并核对；从已被攻破的控制面下载脚本再直接执行，无法靠脚本内部的验签建立信任。签名认证发布内容，不阻止重放仍由同一密钥签名的历史版本。

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

`dist/release/` 下会得到 `SHA256SUMS`、`SHA256SUMS.sig`、`release-manifest.json`。随后构建带这些产物的 web 镜像：

```bash
docker build --target qcontrol-web \
  --build-arg VERSION=1.2.3 \
  --build-arg RELEASE_ARTIFACTS=dist/release .
```

原生二进制部署可用 `make release-checksums VERSION=1.2.3` 签名 `bin/qagent`；也可用 `RELEASE_AGENT=/path/to/qagent` 指定真正要分发的文件。Docker 流程从与运行镜像相同的构建阶段导出 Agent，固定构建镜像摘要，避免宿主机与容器 Go 版本不同导致摘要不一致。

**清单范围**：清单包含 systemd 和 OpenRC 安装资源的并集。节点仅下载自己使用的一套，验签后提取所需条目逐个校验；所需条目缺失、重复或内容不符都会失败，不使用会忽略必需文件的 `--ignore-missing`。`deploy/` 含 `deploy/tests` 等非发布工具，不能整体签进去。

`release-checksums` 依赖 `check-install-assets`，后者运行 `deploy/tests/release-assets.sh`：从 `deploy/remote/install-agent.sh` 自己的下载循环推导资产列表，与可签名集合逐条比对，不一致就拒绝出包。往安装器里新增一个下载而不更新发布集合，会被这里挡住。

控制面还需要只读挂载 `dist/release/release-manifest.json`，并把 `QCH_RELEASE_MANIFEST_PATH` 指向容器内的该文件；可同时挂载公钥并设置 `QCH_RELEASE_PUBLIC_KEY` 做启动期自检。Web 镜像中的校验清单不会自动代替这个 Agent 升级接口。

```bash
make check-install-assets        # 只校验：安装器下载集 == 发布资产集
bash deploy/tests/release-assets.sh --files   # 打印本次要签的文件
```

**版本一致性**：`release-checksums` 把 `VERSION` 同时写进 `-release` 与 `-agent-version`，而控制面把自身构建版本通过已认证的 WSS 策略下发给 Agent。升级时 Agent 要求签名清单里的 Agent 版本**等于**面板版本，否则拒绝安装。这样“面板版本”与“Agent 版本”由签名绑定在一起，控制面无法一边声称版本 1.2.3、一边下发别的构建。

> `qcontrol-web` 阶段会无条件 `COPY dist/release/SHA256SUMS`，所以**必须有签名产物才能构建 web 镜像**。全新检出直接 `docker build --target qcontrol-web .` 会因缺文件失败：先跑一次 `make release-image-checksums`（本地测试可用一次性密钥），或在 CI 中让签名 job 先产出 `dist/release/`。`dist/` 已被 gitignore，不要提交。没有配置 `RELEASE_SIGNING_KEY` 时，当前 CI 不发布 web 镜像；发布前应确认三个镜像使用同一版本。

## 4. 在 GitHub Actions 里签名

私钥放**环境级** secret，而不是仓库级：环境可以要求人工审批，并限制只有 tag 能触发，于是改了 workflow 也拿不到密钥。

```yaml
name: Release
on:
  push:
    tags: ["v*"]

permissions:
  contents: read

jobs:
  sign:
    environment: release-signing      # Required reviewers + 只允许 tag
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v6
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
      - uses: docker/setup-buildx-action@v4
      - name: Build the artifacts to sign
        run: |
          install -m 0600 /dev/null "$RUNNER_TEMP/release.key"
          printf '%s' "$RELEASE_SIGNING_KEY" > "$RUNNER_TEMP/release.key"
          make release-image-checksums VERSION="${GITHUB_REF_NAME}" RELEASE_KEY="$RUNNER_TEMP/release.key"
        env:
          RELEASE_SIGNING_KEY: ${{ secrets.RELEASE_SIGNING_KEY }}
      - name: Publish the signed artifacts
        uses: actions/upload-artifact@v4
        with:
          name: release-artifacts
          path: dist/release
```

`RELEASE_SIGNING_KEY` 的内容就是 `release.key`。签名 job 与发布镜像的 job 分开，发布 job 不需要接触私钥。

> 「放在 GitHub 上」本身不是信任根：`raw.githubusercontent.com/.../main/...` 指向可变分支，谁拿到账号或 token 就能改。**可变分支 + 版本号不等于可信**，必须配合签名，或使用不可变的 tag / commit SHA。

## 5. 运维开关

| 变量 | 作用 |
| --- | --- |
| `QCH_RELEASE_PUBLIC_KEY` | 安装器与 Agent 的公钥路径。设置即表示“本部署有签名发布”，此时缺失或无法验证的签名是**硬失败** |
| `QCH_ALLOW_UNSIGNED_RELEASE=true` | 本次安装显式放弃校验并打印告警；不会清除已保存的 Agent 公钥，也不关闭 Agent 后续升级验签 |
| `QCH_RELEASE_MANIFEST_PATH` | 控制面读取 `release-manifest.json` 的路径；供 Agent 升级校验使用 |
| `QCH_RELEASE_PUBLIC_KEY`（控制面侧，可选） | PEM/raw-URL 公钥文件路径，或 inline raw-URL 公钥；与控制面读取的清单做一次启动期自检 |

未设置 `QCH_RELEASE_PUBLIC_KEY` 的节点保持原有行为（升级不验签，但会记录告警），因此既有节点不会因为引入校验而无法升级。**建议在下次重装或轮换时给所有节点加上公钥。**

## 6. 故障排查

| 现象 | 原因 |
| --- | --- |
| `RELEASE SIGNATURE VERIFICATION FAILED` | 清单不是由该公钥签名：轮换密钥后未分发新公钥，或下载被篡改 |
| `do not match the signed checksum list` | 签名有效但某个文件内容与清单不符：控制面分发的文件与签名产物不是同一批 |
| `failed to download the signed checksum list` | 控制面没有提供 `SHA256SUMS`：镜像未带 `dist/release/` 产物 |
| `does not match control plane version` | 签名清单的 Agent 版本与控制面构建版本不一致：用同一个 `VERSION` 重新签名 |
| `openssl is required to verify the signed release` | 节点缺少 openssl；安装它，或显式设置 `QCH_ALLOW_UNSIGNED_RELEASE=true` |

轮换发布密钥属于一次性人工动作：新公钥要有意识地分发到每个节点，再开始用新私钥签名；否则所有校验都会按设计失败。
