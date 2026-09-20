# 卡片样式统一：审核对比

以节点页为基准，统一 13px 圆角、12px 网格间距、标题与底部操作区。列表使用相同的自适应列宽；图表、日志、IP 原始报告和用户分配表保留阅读宽度。系统设置改为分组卡片网格，Sub-Store 改为按行排列。

**配置页保持原样**：服务端配置、预设、配置存档和客户端配置均不在改动范围。

每张图左侧为修改前，右侧为修改后。截图来自仓库的浏览器测试数据，没有连接生产服务；Sub-Store 和用户管理使用独立功能夹具，因此不显示控制台侧栏。桌面为 1440 × 1000，手机为 390 × 844 触摸设备。修改前采用基线 `f9d5438cd79e2b44e585e90db12e4dc20ef09252` 的样式。

## 桌面

<details><summary>节点（样式基准）</summary>

![节点（样式基准）：左侧修改前，右侧修改后](console-cards/nodes.webp)

</details>

<details><summary>总览</summary>

![总览：左侧修改前，右侧修改后](console-cards/dashboard.webp)

</details>

<details><summary>流量管理</summary>

![流量管理：左侧修改前，右侧修改后](console-cards/traffic.webp)

</details>

<details><summary>BBR / TCP</summary>

![BBR / TCP：左侧修改前，右侧修改后](console-cards/tcp.webp)

</details>

<details><summary>访问限制</summary>

![访问限制：左侧修改前，右侧修改后](console-cards/access.webp)

</details>

<details><summary>系统设置</summary>

![系统设置：左侧修改前，右侧修改后](console-cards/settings.webp)

</details>

<details><summary>Sub-Store</summary>

![Sub-Store：左侧修改前，右侧修改后](console-cards/substore.webp)

</details>

<details><summary>IP 质量</summary>

![IP 质量：左侧修改前，右侧修改后](console-cards/quality.webp)

</details>

<details><summary>用户管理</summary>

![用户管理：左侧修改前，右侧修改后](console-cards/users.webp)

</details>

<details><summary>任务</summary>

![任务：左侧修改前，右侧修改后](console-cards/tasks.webp)

</details>

<details><summary>内核日志</summary>

![内核日志：左侧修改前，右侧修改后](console-cards/logs.webp)

</details>

<details><summary>客户端连接 IP</summary>

![客户端连接 IP：左侧修改前，右侧修改后](console-cards/connections.webp)

</details>


## 手机

<details><summary>节点（样式基准）</summary>

![节点（样式基准）：左侧修改前，右侧修改后](console-cards/nodes-mobile.webp)

</details>

<details><summary>总览</summary>

![总览：左侧修改前，右侧修改后](console-cards/dashboard-mobile.webp)

</details>

<details><summary>流量管理</summary>

![流量管理：左侧修改前，右侧修改后](console-cards/traffic-mobile.webp)

</details>

<details><summary>BBR / TCP</summary>

![BBR / TCP：左侧修改前，右侧修改后](console-cards/tcp-mobile.webp)

</details>

<details><summary>访问限制</summary>

![访问限制：左侧修改前，右侧修改后](console-cards/access-mobile.webp)

</details>

<details><summary>系统设置</summary>

![系统设置：左侧修改前，右侧修改后](console-cards/settings-mobile.webp)

</details>

<details><summary>Sub-Store</summary>

![Sub-Store：左侧修改前，右侧修改后](console-cards/substore-mobile.webp)

</details>


## 深色模式

<details><summary>节点（样式基准）</summary>

![节点（样式基准）：左侧修改前，右侧修改后](console-cards/nodes-dark.webp)

</details>

<details><summary>总览</summary>

![总览：左侧修改前，右侧修改后](console-cards/dashboard-dark.webp)

</details>

<details><summary>流量管理</summary>

![流量管理：左侧修改前，右侧修改后](console-cards/traffic-dark.webp)

</details>

<details><summary>BBR / TCP</summary>

![BBR / TCP：左侧修改前，右侧修改后](console-cards/tcp-dark.webp)

</details>

<details><summary>访问限制</summary>

![访问限制：左侧修改前，右侧修改后](console-cards/access-dark.webp)

</details>

<details><summary>系统设置</summary>

![系统设置：左侧修改前，右侧修改后](console-cards/settings-dark.webp)

</details>

<details><summary>Sub-Store</summary>

![Sub-Store：左侧修改前，右侧修改后](console-cards/substore-dark.webp)

</details>

## 验证

- 样式生成和模块边界检查通过。
- 前端模块 smoke、完整浏览器回归、前端 Go 测试通过。
- 最终改动补跑客户端、Sub-Store、系统设置、配置和 IP 质量的桌面／手机回归。
- 截图检查覆盖 12 个页面；手机和深色模式各覆盖 7 个页面。
- 服务端配置、客户端配置和配置存档分别核对了 98、490、64 个元素的布局与外观属性，修改前后无差异。

如需回滚，撤销本 PR 后运行 `make generate-styles`。
