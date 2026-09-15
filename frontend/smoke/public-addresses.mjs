// Keep pure provenance checks, DOM/CSS checks and route rendering ordered.
export async function run(fixtures) {
  await (await import("./public-address-model.mjs")).run(fixtures);
  await (await import("./public-address-dom.mjs")).run(fixtures);
  await (await import("./public-address-overview.mjs")).run(fixtures);
}
