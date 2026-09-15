import assert from "node:assert/strict";

import { ConfigFormatError, formatConfigContent } from "../modules/code-format.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run() {
const formattedJson = formatConfigContent(
  '{"proxies":[],"mode":"rule","port":7890,"enabled":true,"empty":null}',
  "JSON",
);
assert.equal(
  formattedJson,
  '{\n  "proxies": [],\n  "mode": "rule",\n  "port": 7890,\n  "enabled": true,\n  "empty": null\n}\n',
  "JSON format applies two-space indentation, preserves order and types, and ends with one newline",
);
assert.equal(
  formatConfigContent('[1,{"nested":[2,3]}]', "JSON"),
  '[\n  1,\n  {\n    "nested": [\n      2,\n      3\n    ]\n  }\n]\n',
  "JSON format keeps array order and nesting",
);
assert.ok(
  formatConfigContent('{"a":1,\n\n\n "b":2}', "JSON").startsWith('{\n  "a": 1'),
  "JSON format is syntax-aware and idempotent across whitespace",
);
assert.throws(
  () => formatConfigContent('{"a":1,}', "JSON"),
  ConfigFormatError,
  "JSON with a trailing comma fails closed",
);
assert.throws(
  () => formatConfigContent('{"a":1} // sing-box comment', "JSON"),
  ConfigFormatError,
  "sing-box extended JSON comments fail closed",
);
assert.throws(
  () =>
    formatConfigContent(
      '{"a":1,"a":2}',
      "JSON",
    ),
  ConfigFormatError,
  "duplicate JSON keys fail closed",
);
assert.equal(
  formatConfigContent('{"big":9007199254740993}', "JSON"),
  '{\n  "big": 9007199254740993\n}\n',
  "unsafe integers are preserved verbatim, never rounded",
);
assert.equal(
  formatConfigContent('{"x":1e400}', "JSON"),
  '{\n  "x": 1e400\n}\n',
  "overflowing exponents are preserved verbatim",
);
assert.equal(
  formatConfigContent('{"x":9.007199254740993e15}', "JSON"),
  '{\n  "x": 9.007199254740993e15\n}\n',
  "high-precision decimals keep their full token",
);
assert.equal(
  formatConfigContent('{"x":0.100000000000000005}', "JSON"),
  '{\n  "x": 0.100000000000000005\n}\n',
  "high-precision decimal literals are not collapsed",
);
assert.equal(
  formatConfigContent('{"x":-0}', "JSON"),
  '{\n  "x": -0\n}\n',
  "negative zero keeps its sign",
);
assert.equal(
  formatConfigContent('{"10":"ten","2":"two","a":1}', "JSON"),
  '{\n  "10": "ten",\n  "2": "two",\n  "a": 1\n}\n',
  "object member order is preserved, including integer-like keys",
);
assert.equal(
  formatConfigContent('{"a\\u0041":1,"b\\n":2}', "JSON"),
  '{\n  "a\\u0041": 1,\n  "b\\n": 2\n}\n',
  "escaped object keys and string escapes are preserved",
);
assert.throws(
  () =>
    formatConfigContent(
      "mixed-port: 7890\n# keep me\nproxies: []\n",
      "YAML",
    ),
  ConfigFormatError,
  "Mihomo YAML with comments fails closed without a comment-preserving parser",
);
assert.throws(
  () => formatConfigContent("   ", "JSON"),
  ConfigFormatError,
  "empty content fails closed",
);

}
