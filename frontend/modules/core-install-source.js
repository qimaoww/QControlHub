export function developmentSourceVisible(engine, channel) {
  return engine === "mihomo" && channel === "development";
}

export function coreSourceForInstall(engine, channel, rawSource) {
  return developmentSourceVisible(engine, channel) ? rawSource || "official" : undefined;
}
