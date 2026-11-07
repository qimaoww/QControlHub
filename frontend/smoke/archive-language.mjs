import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { archiveDraftDirty, createArchiveDraft } from "../modules/archive-draft-model.js";
import { configDefaults } from "../modules/config-defaults.js";

export async function run() {
  for (const [engine, file] of Object.entries({ mihomo: "mihomo-minimal.yaml", xray: "xray-minimal.json",
    "sing-box": "sing-box-minimal.json", "ss-rust": "shadowsocks-rust-minimal.json" })) {
    assert.equal(configDefaults[engine], await readFile(new URL(`../../examples/configs/${file}`, import.meta.url), "utf8"),
      `${engine} defaults must reuse the canonical example verbatim`);
    const archive = createArchiveDraft({ name: "Archive", engine });
    const template = createArchiveDraft({ name: "Template", engine });
    assert.equal(archive.sources.get(engine).content, configDefaults[engine]);
    assert.equal(archiveDraftDirty(archive), false);
    archive.sources.get(engine).content += "\n";
    assert.equal(archiveDraftDirty(archive), true);
    assert.equal(template.sources.get(engine).content, configDefaults[engine], "forms must not share source state");
    archive.sources.get(engine).content = archive.sources.get(engine).original;
    assert.equal(archiveDraftDirty(archive), false);
    archive.name = "Renamed";
    assert.equal(archiveDraftDirty(archive), true);
    archive.name = "Archive";
    archive.description = "Notes";
    assert.equal(archiveDraftDirty(archive), true);
    if (engine !== "mihomo") assert.ok(JSON.parse(configDefaults[engine]));
  }
  assert.notEqual(configDefaults.xray, configDefaults["sing-box"]);
  assert.notEqual(configDefaults.xray, configDefaults["ss-rust"]);
  assert.notEqual(configDefaults["sing-box"], configDefaults["ss-rust"]);
  console.log("Archive defaults reuse all four canonical examples; draft baselines and forms are independent");
}

// Actual native archive-engines browser checks retain the former language,
// per-engine draft, name/notes, validation/reset and readonly UI assertions.
