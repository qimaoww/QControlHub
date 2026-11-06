import { assert, waitFor } from "./assertions.mjs";

export async function testIPQualityScenarios({ fixture, refresh, card }) {
  const original = structuredClone(fixture.records);
  const base = original[0];
  const makeRecord = (task, family, ip, score, media = "Yes") => {
    const record = structuredClone(base);
    const report = record.result.reports.find((item) => item.Head.IP.includes(":") === (family === 6));
    report.Head.IP = ip;
    report.Score = { IPQS: score, SCAMALYTICS: score, ipapi: score === null ? null : `${score}%` };
    report.Info.Organization = "Example Network";
    report.Media = { Netflix: { Status: media, Region: media === "Yes" ? "HK" : "", Type: media === "Yes" ? "Native" : "" } };
    if (family === 6) delete report.Mail.DNSBlacklist;
    record.task_id = task;
    record.archives = record.archives.filter((archive) => archive.family === family);
    record.result.reports = [report];
    return record;
  };
  const cases = [
    { id: "dual", label: "IPv4 + 完整 IPv6", records: original },
    { id: "ipv4-clean", label: "IPv4 · 低风险 / 全解锁", records: [makeRecord("quality-clean", 4, "192.0.2.1", 0)] },
    { id: "ipv4-medium", label: "IPv4 · 中风险 / 仅 APP", records: [makeRecord("quality-medium", 4, "198.51.100.25", 45, "APP")] },
    { id: "ipv4-high", label: "IPv4 · 高风险 / 屏蔽", records: [makeRecord("quality-high", 4, "203.0.113.254", 95, "No")] },
    { id: "ipv4-missing", label: "IPv4 · 数据缺失", records: [makeRecord("quality-missing", 4, "203.0.113.2", null, "N/A")] },
    { id: "ipv6-short", label: "IPv6 · 压缩地址", records: [makeRecord("quality-vsix-short", 6, "2001:db8::1", 0)] },
    { id: "ipv6-full", label: "IPv6 · 39 字符完整地址", records: [makeRecord("quality-vsix-full", 6, "2001:0db8:1234:5678:90ab:cdef:1234:5678", 5.47)] },
    { id: "ipv6-missing", label: "IPv6 · 数据缺失", records: [makeRecord("quality-vsix-missing", 6, "2001:db8::2", null, "N/A")] },
    { id: "pending", label: "等待检测", records: [{ task_id: "quality-pending", agent_id: "quality-a", status: "pending" }] },
    { id: "failed", label: "检测失败", records: [{ task_id: "quality-failed", agent_id: "quality-a", status: "failed", error: "节点检测超时，未生成报告。" }] },
    { id: "empty", label: "当天无记录", records: [] },
    { id: "legacy", label: "旧记录无图片", records: [{ ...base, task_id: "quality-legacy", archives: [] }] },
  ];
  window.__ipQualityScenarioCases = cases;
  try {
    for (const scenario of cases) {
      fixture.records = structuredClone(scenario.records);
      await refresh();
      const record = scenario.records[0];
      const reports = record?.result?.reports || [];
      const images = [...card().querySelectorAll("img")];
      assert.equal(images.length, record?.archives?.length || 0, `${scenario.id}: stale/missing archive`);
      await waitFor(() => images.every((image) => image.complete && image.naturalWidth > 0), `${scenario.id}: image failed to load`);
      for (const image of images) {
        assert.equal(image.naturalWidth, 608, `${scenario.id}: title widened the 72-column report`);
      }
      assert.equal(card().querySelectorAll("a[download]").length, images.length, `${scenario.id}: download missing`);
      assert.equal(card().querySelector("table"), null, "archive was replaced by a structured report");
      for (let index = 0; index < images.length; index++) {
        const family = record.archives[index].family;
        assert.ok(images[index].src.includes(`${record.task_id}/archives/${family}`), `${scenario.id}: wrong family/task archive`);
        const box = images[index].getBoundingClientRect();
        assert.ok(Math.abs(box.width / box.height - images[index].naturalWidth / images[index].naturalHeight) < .01, `${scenario.id}: distorted report`);
      }
      assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, `${scenario.id}: viewport overflow`);
      if (innerWidth <= 480 && matchMedia("(pointer:coarse)").matches) {
        assert.ok(card().getBoundingClientRect().width >= innerWidth - 40,
          `${scenario.id}: sidebar squeezed the mobile report`);
      }
      if (!images.length) assert.ok(card().querySelector(".a-empty")?.textContent, `${scenario.id}: no-report feedback missing`);
    }
  } finally {
    fixture.records = original;
    await refresh();
    window.__ipQualityScenariosReady = true;
  }
}
