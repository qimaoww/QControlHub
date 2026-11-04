// This only describes consent. The server verifies the entire configuration,
// loopback API transport and remaining services before saving or queuing work.
export function xrayMutableAPIServices(engine, content) {
  if (engine !== "xray") return [];
  try {
    const services = JSON.parse(content).api?.services;
    return Array.isArray(services) ? services.filter(name => typeof name === "string" &&
      ["handlerservice", "routingservice"].includes(name.toLowerCase())) : [];
  } catch { return []; }
}
