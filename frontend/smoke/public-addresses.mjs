// Keep pure provenance and native DOM/CSS checks ordered.
export async function run(fixtures) {
  await (await import("./public-address-model.mjs")).run(fixtures);
  await (await import("./public-address-dom.mjs")).run(fixtures);
}
