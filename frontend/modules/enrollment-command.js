export function enrollmentInstallCommand(created, origin = location.origin) {
  const token = String(created?.token || ""), name = String(created?.name || "");
  if (!token || !name) throw new Error("当前节点没有可恢复的部署命令");
  const quote = value => value.replaceAll("'", "'\\''");
  return `curl -fsSL -H 'X-QControlHub-Enrollment: ${quote(token)}' ${origin}/install-agent.sh | sudo sh -s -- ${origin} '${quote(token)}' '${quote(name)}'`;
}
