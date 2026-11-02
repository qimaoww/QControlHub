// Record identity stays in IDs/revisions; labels use persisted timestamps only.
export function recordDate(value, format = value => new Date(value).toLocaleString()) {
  if (!value || /^0001-/.test(String(value)) || !Number.isFinite(Date.parse(value))) return "时间未知";
  return format(value);
}
