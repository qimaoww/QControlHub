// Display the panel-rendered SVG as an image, never as executable markup or an
// iframe. The bytes come from the panel's own renderer, not from an upstream host.
export function createIPQualityArchiveView({ esc, date }) {
  return (record) => {
    const figures = (record.archives || []).filter((archive) => [4, 6].includes(archive.family)).map((archive) => {
      const source = `/api/v1/ip-quality/${encodeURIComponent(record.task_id)}/archives/${archive.family}`;
      return `<figure class="ip-quality-archive" data-refresh-key="archive-${esc(record.task_id)}-${archive.family}">
        <figcaption><strong>IPv${archive.family} 检测报告</strong><a class="button small" href="${source}?download=1" download>下载报告</a></figcaption>
        <a href="${source}" target="_blank" rel="noopener noreferrer" aria-label="放大查看 IPv${archive.family} 检测报告"><img src="${source}" alt="IPv${archive.family} IPQuality 检测报告" loading="lazy" referrerpolicy="no-referrer"></a>
        <small>面板重绘 · ${esc(date(archive.rendered_at))} · 点击报告放大查看</small>
        <details><summary>存档校验信息</summary><p>SHA-256：${esc(archive.sha256)}</p></details>
      </figure>`;
    }).join("");
    return figures ? `<div class="ip-quality-archives">${figures}</div>` : "";
  };
}
