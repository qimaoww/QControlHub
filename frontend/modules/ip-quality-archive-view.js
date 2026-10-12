// Display the panel-rendered SVG as an image, never as executable markup or an
// iframe. The bytes come from the panel's own renderer, not from an upstream host.
export function createIPQualityArchiveView({ esc, date }) {
  return (record) => {
    const figures = (record.archives || []).filter((archive) => [4, 6].includes(archive.family)).map((archive) => {
      const source = `/api/v1/ip-quality/${encodeURIComponent(record.task_id)}/archives/${archive.family}`;
      return `<figure class="ip-quality-archive" data-refresh-key="archive-${esc(record.task_id)}-${archive.family}-${esc(archive.sha256)}">
        <figcaption><div><span class="ip-family v${archive.family}">IPv${archive.family}</span><strong>检测报告</strong></div><a class="button small" href="${source}?download=1" download>下载报告</a></figcaption>
        <a href="${source}" target="_blank" rel="noopener noreferrer" aria-label="放大查看 IPv${archive.family} 检测报告"><img src="${source}" alt="IPv${archive.family} IPQuality 检测报告" loading="lazy" referrerpolicy="no-referrer"></a>
        <p class="ip-quality-archive-error" role="alert" hidden>报告图片加载失败，文字结果仍可查看。<button type="button" class="button small" data-ip-quality-image-retry>重试图片</button></p>
        <details><summary>存档信息</summary><p>${esc(date(archive.rendered_at))}</p><p>SHA-256：${esc(archive.sha256)}</p></details>
      </figure>`;
    }).join("");
    return figures ? `<div class="ip-quality-archives">${figures}</div>` : "";
  };
}
