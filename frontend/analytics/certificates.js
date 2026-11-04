import { certificateOrderPresentation, certificateOrderDiagnostic } from "../modules/certificate-order.js";
import { node, button, actions, stack, grid, section, table, badge, field, muted, list, pathID, notice, code } from "./ui.js";
import { date } from "./operations.js";

const certStatus = value => badge(({ ready: "可用", pending: "等待签发", issuing: "签发中", failed: "失败", expired: "已过期", revoked: "已撤销",
  waiting_dns: "等待 DNS", succeeded: "成功", canceled: "已取消", active: "有效", processing: "处理中" })[value] || value || "未知");
const sourceName = value => ({ acme: "ACME", import: "导入", "self-signed": "自签", caddy: "Caddy", node: "节点证书" })[value] || value;

export async function certificatesPage(ctx) {
  let resource = await ctx.api("/certificates");
  const results = node("div");
  const search = field("搜索证书", "search", "", { type: "search", placeholder: "名称或域名" });
  let modalRead = 0;
  async function reload() { resource = await ctx.api("/certificates"); if (ctx.current()) draw(); }
  const run = (control, action) => ctx.mutate(control, action);
  function pairFields() {
    return [
      field("证书链 PEM", "certificate", "", { multiline: 8, required: true, spellcheck: false }),
      field("私钥 PEM", "private_key", "", { multiline: 8, required: true, spellcheck: false, autocomplete: "off" }),
    ];
  }
  async function create(source) {
    const sequence = ++modalRead;
    const accounts = source === "acme" ? list(await ctx.api("/certificate-accounts")) : [];
    const nodes = source === "acme" ? list(await ctx.api("/certificate-http-nodes")) : [];
    if (!ctx.current() || sequence !== modalRead) return;
    const method = field("认证方式", "method", "dns-api", { options: [["dns-api", "DNS API"], ["dns-manual", "手工 DNS"], ["http-01", "HTTP-01"]] });
    const dns = field("DNS 账号", "dns_account_id", "", { options: [["", "选择 DNS 账号"], ...accounts.filter(account => account.kind === "dns").map(account => [account.id, account.name])] });
    const host = field("认证节点", "agent_id", "", { options: [["", "选择节点"], ...nodes.map(agent => [agent.id, `${agent.name}${agent.available ? "" : " · 暂不可用"}`])] });
    const methodInput = method.querySelector("select");
    const updateMethod = () => {
      dns.hidden = methodInput.value !== "dns-api";
      host.hidden = methodInput.value !== "http-01";
      dns.querySelector("select").required = methodInput.value === "dns-api";
      host.querySelector("select").required = methodInput.value === "http-01";
    };
    methodInput.addEventListener("change", updateMethod);
    updateMethod();
    const form = ctx.form([
      field("名称", "name", "", { required: true, maxlength: 100 }),
      source === "import" ? pairFields() : [
        field("域名", "domains", "", { multiline: 3, required: true, hint: "每行一个域名" }),
        field("密钥类型", "key_type", "ecdsa256", { options: [["ecdsa256", "ECDSA P-256"], ["ecdsa384", "ECDSA P-384"], ["rsa2048", "RSA 2048"], ["rsa4096", "RSA 4096"]] }),
        source === "self-signed" ? field("有效天数", "days", 365, { type: "number", min: 1, max: 3650, required: true }) : [
          field("ACME 账号", "account_id", "", { required: true, options: [["", "选择 ACME 账号"], ...accounts.filter(account => account.kind === "acme").map(account => [account.id, account.name])] }),
          method, dns, host, field("自动续期", "auto_renew", false, { type: "checkbox" }),
        ],
      ],
    ], source === "acme" ? "申请证书" : "保存证书", async (values, element) => {
      const payload = { name: values.name, source,
        ...(source === "import" ? { certificate: values.certificate, private_key: values.private_key } : {
          domains: values.domains.split(/[\s,，]+/).filter(Boolean), key_type: values.key_type,
          ...(source === "self-signed" ? { days: Number(values.days) } : {
            method: values.method, account_id: values.account_id,
            dns_account_id: values.method === "dns-api" ? values.dns_account_id : "",
            agent_id: values.method === "http-01" ? values.agent_id : "",
            auto_renew: element.elements.auto_renew.checked,
          }),
        }),
      };
      const certificate = await ctx.api("/certificates", { method: "POST", body: payload });
      box.close();
      await reload();
      ctx.announce(source === "acme" ? "证书申请已提交" : "证书已保存");
      await detail(certificate.id);
    });
    const box = ctx.dialog(`新建证书 · ${sourceName(source)}`, form, { wide: true });
  }
  async function detail(id) {
    const request = ++modalRead;
    let data = await ctx.api(`/certificates/${pathID(id)}`);
    if (!ctx.current() || request !== modalRead) return;
    const body = node("div", { className: "a-stack" });
    const box = ctx.dialog(data.certificate.name, body, { wide: true });
    async function read() {
      const sequence = ++modalRead;
      const next = await ctx.api(`/certificates/${pathID(id)}`);
      if (!ctx.current() || !box.open || sequence !== modalRead) return;
      data = next;
      drawDetail();
    }
    function drawDetail() {
      const certificate = data.certificate;
      const owned = certificate.owned;
      const control = (label, suffix, method, body, confirmText) => button(label, async event => {
        const button = event.currentTarget;
        if (confirmText && !await ctx.confirm(confirmText, label)) return;
        if (!ctx.current() || !box.open) return;
        await run(button, async () => {
          await ctx.api(`/certificates/${pathID(id)}${suffix}`, { method, body });
          if (method === "DELETE" && !suffix) { box.close(); await reload(); return; }
          ctx.announce("操作已提交");
          await read();
          await reload();
        });
      });
      const tools = actions(
        button("刷新", event => void run(event.currentTarget, read)),
        owned && certificate.source === "acme" && control("立即续期", "/renew", "POST", undefined, "提交证书续期申请？"),
        owned && certificate.source === "acme" && control(certificate.auto_renew ? "关闭自动续期" : "启用自动续期", "/auto-renew", "PUT", { enabled: !certificate.auto_renew }),
        owned && button("替换证书", () => {
          const form = ctx.form(pairFields(), "保存替换", async values => {
            await ctx.api(`/certificates/${pathID(id)}/replace`, { method: "POST", body: values });
            replace.close();
            await read();
            await reload();
            ctx.announce("证书已替换");
          });
          const replace = ctx.dialog("替换证书", form, { wide: true });
        }),
        certificate.version && node("a", {
          className: "a-button", href: `/api/v1/certificates/${pathID(id)}/public-chain`, download: "",
        }, "下载公开证书链"),
        owned && control("删除证书", "", "DELETE", undefined, "删除此证书及其历史记录？正在使用的证书可能无法删除。"));
      const orders = section("签发订单", table(["创建时间", "状态", "阶段", "错误", "操作"], list(data.orders).map(order => [
        date(order.created_at), badge(certificateOrderPresentation(order, date).label, certificateOrderPresentation(order, date).tone),
        certificateOrderPresentation(order, date).info, stack(
          certificateOrderPresentation(order, date).error && notice(certificateOrderPresentation(order, date).error),
          certificateOrderPresentation(order, date).cleanupError && notice(certificateOrderPresentation(order, date).cleanupError, "warn")),
        actions(button("查看认证记录", () => ctx.dialog("域名认证记录", table(["域名", "类型", "名称", "值", "状态"], list(order.challenges).map(challenge => [
          challenge.domain, challenge.type, challenge.name, code(challenge.value), certStatus(challenge.status),
        ])), { wide: true })),
        owned && order.status === "waiting_dns" && control("继续认证", `/orders/${pathID(order.id)}/continue`, "POST"),
        owned && !["succeeded", "failed", "canceled"].includes(order.status) && control("取消订单", `/orders/${pathID(order.id)}`, "DELETE", undefined, "取消此签发订单？")),
      ]), { label: "签发订单" }));
      const bindings = section("部署绑定", table(["节点", "配置", "用户", "内核", "状态", "保存时间"], list(data.bindings).map(binding => [
        binding.agent_name || binding.agent_id, binding.config_name || binding.config_id, binding.username || "管理员",
        binding.engine, certStatus(binding.status), date(binding.saved_at),
      ]), { label: "证书绑定" }));
      const shares = owned && section("证书使用授权", stack(
        table(["用户名", "操作"], list(data.grants).map(grant => [grant.username,
          control("撤销", "/grants", "DELETE", { username: grant.username }, "撤销后停止分发与同步，现有部署保留。确定撤销？")])),
        node("div", { className: "a-card-body" }, button("授权使用", () => {
          const form = ctx.form([field("用户名", "username", "", { required: true })], "保存授权", async values => {
            await ctx.api(`/certificates/${pathID(id)}/grants`, { method: "POST", body: values });
            grantDialog.close();
            await read();
            ctx.announce("证书授权已保存");
          });
          const grantDialog = ctx.dialog("证书使用授权", form);
        }))));
      body.replaceChildren(stack(
        actions(certStatus(certificate.status), badge(sourceName(certificate.source)), badge(owned ? "自有证书" : "获授权使用")),
        table(["项目", "内容"], [["域名", certificate.domains.join(" · ")], ["密钥类型", certificate.key_type],
          ["有效期开始", date(certificate.not_before)], ["有效期截止", date(certificate.not_after)],
          ["保存时间", date(certificate.saved_at || certificate.created_at)], ["指纹", certificate.fingerprint || "—"]]),
        certificate.error && notice(certificateOrderDiagnostic(certificate.error)), tools, orders, bindings, shares,
        section("证书历史", table(["保存时间", "有效期截止", "状态"], list(data.versions).map(version => [
          date(version.saved_at || version.created_at), date(version.not_after), version.version === certificate.version ? badge("当前") : "历史证书",
        ])))));
    }
    drawDetail();
    let timer;
    async function poll() {
      if (!ctx.current() || !box.open) return;
      try {
        if (!document.hidden && document.querySelector("dialog[open]:last-of-type") === box && !document.querySelector("[aria-busy=true]")) await read();
      } catch (error) {
        if (error.status === 404) { box.close(); await reload(); }
      } finally { if (ctx.current() && box.open) timer = setTimeout(poll, 3000); }
    }
    // Grant forms remain stable; the refresh control also updates every order.
    timer = setTimeout(poll, 3000);
    box.addEventListener("close", () => clearTimeout(timer), { once: true });
    ctx.signal.addEventListener("abort", () => clearTimeout(timer), { once: true });
  }
  async function accounts() {
    const request = ++modalRead;
    const [items, nodes, grants] = await Promise.all([
      ctx.api("/certificate-accounts"), ctx.api("/certificate-http-nodes"), ctx.api("/certificate-http-grants"),
    ]);
    if (!ctx.current() || request !== modalRead) return;
    const body = node("div", { className: "a-stack" });
    const box = ctx.dialog("证书账号与认证授权", body, { wide: true });
    body.append(
      section("证书账号", table(["名称", "类型", "CA / DNS 服务商", "操作"], list(items).map(account => [
        account.name, account.kind === "acme" ? "ACME" : "DNS", account.ca || account.provider,
        actions(account.kind === "dns" && button("检查连接", event => void run(event.currentTarget, async () => {
          await ctx.api(`/certificate-accounts/${pathID(account.id)}/check`, { method: "POST" });
          ctx.announce("DNS 读取连接正常");
        })), account.kind === "dns" && button("更新凭据", () => accountForm("dns", account)),
        button("删除", async event => {
          const control = event.currentTarget;
          if (!await ctx.confirm(`删除账号“${account.name}”？`, "删除证书账号") || !ctx.current()) return;
          await run(control, async () => {
            await ctx.api(`/certificate-accounts/${pathID(account.id)}`, { method: "DELETE" });
            box.close();
            await accounts();
          });
        }, { tone: "danger" })),
      ]), { label: "证书账号" }), actions(button("添加 ACME 账号", () => accountForm("acme")), button("添加 DNS 账号", () => accountForm("dns")))),
      section("HTTP-01 授权", stack(table(["节点", "用户", "域名", "操作"], list(grants).map(grant => [
        list(nodes).find(node => node.id === grant.agent_id)?.name || grant.agent_id, grant.username, grant.domain,
        button("撤销", async event => {
          const control = event.currentTarget;
          if (!await ctx.confirm("撤销此域名的 HTTP-01 认证权限？", "撤销授权") || !ctx.current()) return;
          await run(control, async () => {
            await ctx.api("/certificate-http-grants", { method: "DELETE", body: grant });
            box.close();
            await accounts();
          });
        }, { tone: "danger" }),
      ])), node("div", { className: "a-card-body" }, ctx.form([
        field("自有节点", "agent_id", "", { required: true, options: [["", "选择节点"], ...list(nodes).filter(node => node.owned).map(node => [node.id, node.name])] }),
        field("用户名", "username", "", { required: true }), field("域名", "domain", "", { required: true }),
      ], "授权 HTTP-01", async values => {
        await ctx.api("/certificate-http-grants", { method: "POST", body: values });
        box.close();
        await accounts();
        ctx.announce("HTTP-01 授权已保存");
      })))));
  }
  function accountForm(kind, account = null) {
    const provider = field("DNS 服务商", "provider", account?.provider || "cloudflare", { options: [["cloudflare", "Cloudflare"], ["alidns", "阿里云 DNS"], ["dnspod", "DNSPod"]], disabled: Boolean(account) });
    const credentials = ["token", "access_key_id", "access_key_secret", "secret_id", "secret_key"].map(name => {
      const label = ({ token: "API Token", access_key_id: "Access Key ID", access_key_secret: "Access Key Secret", secret_id: "Secret ID", secret_key: "Secret Key" })[name];
      return field(label, name, "", { type: "password", autocomplete: "new-password" });
    });
    const sync = () => {
      const selected = provider.querySelector("select").value;
      credentials.forEach(control => {
        const name = control.querySelector("input").name;
        const relevant = selected === "cloudflare" ? name === "token" : selected === "alidns" ? name.startsWith("access_key") : name.startsWith("secret_");
        control.hidden = !relevant;
        control.querySelector("input").required = relevant;
      });
    };
    provider.querySelector("select").addEventListener("change", sync);
    sync();
    const form = ctx.form([
      field("名称", "name", account?.name || "", { required: true }),
      kind === "acme" ? [
        field("CA", "ca", "letsencrypt-staging", { options: [["letsencrypt-staging", "Let's Encrypt 测试环境"], ["letsencrypt", "Let's Encrypt"], ["zerossl", "ZeroSSL"]] }),
        field("邮箱", "email", "", { type: "email", required: true }),
        field("EAB ID", "eab_id", "", { autocomplete: "off" }),
        field("EAB Key", "eab_key", "", { type: "password", autocomplete: "new-password" }),
        field("同意所选 CA 的服务条款", "accept_terms", false, { type: "checkbox", required: true }),
      ] : [provider, field("Zone", "zone", "", { required: true }), credentials],
    ], "保存账号", async (values, element) => {
      const payload = { name: values.name, kind,
        ...(kind === "acme" ? { ca: values.ca, email: values.email, eab_id: values.eab_id, eab_key: values.eab_key, accept_terms: element.elements.accept_terms.checked } :
          { provider: account?.provider || values.provider, credentials: Object.fromEntries(["zone", "token", "access_key_id", "access_key_secret", "secret_id", "secret_key"].filter(key => values[key]).map(key => [key, values[key]])) }),
      };
      await ctx.api(account ? `/certificate-accounts/${pathID(account.id)}/credentials` : "/certificate-accounts", { method: account ? "PUT" : "POST", body: account ? { name: payload.name, credentials: payload.credentials } : payload });
      box.close();
      ctx.announce("证书账号已保存");
    });
    const box = ctx.dialog(account ? "更新 DNS 凭据" : `添加 ${kind === "acme" ? "ACME" : "DNS"} 账号`, form);
  }
  async function sources() {
    const request = ++modalRead;
    const sourceItems = list(await ctx.api("/certificate-managed-sources"));
    if (!ctx.current() || request !== modalRead) return;
    const content = node("div");
    const box = ctx.dialog("节点证书来源", content, { wide: true });
    content.append(table(["节点", "域名", "内核", "状态", "最近同步", "操作"], sourceItems.map(source => [
      source.agent_name || source.agent_id, source.domain, source.engine, certStatus(source.status), date(source.last_synced),
      actions(...["enroll", "detach", "resume"].map(action => button(({ enroll: "接入", detach: "解除关联", resume: "恢复同步" })[action], async event => {
        const control = event.currentTarget;
        const message = action === "detach" ? "停止自动导入和同步？现有证书与绑定保留。" : "接入后同步节点证书，原有 TLS 配置保持不变，确定继续？";
        if (!await ctx.confirm(message, "更新证书来源") || !ctx.current()) return;
        await run(control, async () => {
          await ctx.api(`/certificate-managed-sources/${pathID(source.id)}`, { method: "PUT", body: { action } });
          box.close();
          await sourcesReload();
        });
      }))),
    ]), { label: "节点证书来源" }));
    async function sourcesReload() { await reload(); await sourcesPage(); }
    async function sourcesPage() { await sources(); }
  }
  function draw() {
    const q = search.querySelector("input").value.toLowerCase();
    results.replaceChildren(table(["名称 / 域名", "来源", "归属", "状态", "到期时间", "操作"], list(resource.items).filter(cert =>
      [cert.name, ...(cert.domains || [])].join(" ").toLowerCase().includes(q)).map(cert => [
      stack(node("strong", {}, cert.name), muted(cert.domains.join(" · "))),
      sourceName(cert.source), cert.owned ? "自有" : "共享授权", certStatus(cert.status), date(cert.not_after),
      button("详情", event => void run(event.currentTarget, () => detail(cert.id))),
    ]), { label: "证书" }));
  }
  draw();
  search.querySelector("input").addEventListener("input", draw);
  return stack(section("证书筛选", node("div", { className: "a-card-body" }, search)),
    section("证书", results, actions(
      button("账号与认证授权", event => void run(event.currentTarget, accounts)),
      button("节点证书来源", event => void run(event.currentTarget, sources)),
      ...["acme", "import", "self-signed"].map(source => button(source === "acme" ? "申请证书" : source === "import" ? "导入证书" : "自签证书",
        event => void run(event.currentTarget, () => create(source)), { disabled: !resource.encryption_ready, tone: source === "acme" ? "primary" : "" })))));
}
