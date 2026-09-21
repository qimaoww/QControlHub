import { assert } from "./assertions.mjs";

// Production keeps archives in <img>. Mount only these test-owned SVGs in an
// isolated shadow tree to measure glyph origins without console styles.
function loadSVG(url) {
  return new Promise((resolve, reject) => {
    const request = new XMLHttpRequest();
    request.open("GET", url);
    request.responseType = "document";
    request.timeout = 4000;
    request.onload = () => request.status === 200 && request.responseXML
      ? resolve(document.importNode(request.responseXML.documentElement, true)) : reject(new Error("SVG fixture failed to load"));
    request.onerror = request.ontimeout = () => reject(new Error("SVG fixture request failed"));
    request.send();
  });
}

export async function testIPQualitySVG(images) {
  for (const image of images) {
    const rows = image.src.endsWith("/6") ? 46 : 47;
    assert.equal(image.naturalHeight, rows * 14, "complete report height changed");
    const box = image.getBoundingClientRect();
    assert.ok(Math.abs(box.width / box.height - image.naturalWidth / image.naturalHeight) < 0.01,
      "page stretched the archive image");
    const svg = await loadSVG(image.src);
    const host = document.createElement("div");
    host.style.all = "initial";
    host.attachShadow({ mode: "open" }).append(svg);
    document.body.append(host);
    try {
      const runs = [...svg.querySelectorAll("text")];
      assert.equal(new Set(runs.map((run) => run.getAttribute("y"))).size, rows, "missing report rows");
      assert.equal(svg.querySelector('[font-style="italic"], [font-style="oblique"]'), null, "slanted text returned");
      for (const family of ["monospace", "DejaVu Sans Mono", "sans-serif"]) {
        svg.setAttribute("font-family", family);
        for (const run of runs) {
          assert.equal(getComputedStyle(run).fontStyle, "normal", "report text is not upright");
          const characters = [...run.textContent];
          const step = Number(run.getAttribute("textLength")) / characters.length;
          const start = Number.parseFloat(run.getAttribute("x"));
          for (let index = 0; index < characters.length; index++) {
            const actual = run.getStartPositionOfChar(index).x;
            assert.ok(Math.abs(actual - (start + index * step)) < 0.2,
              `${family}: ${run.textContent} character ${index} drifted off its column`);
            assert.ok(run.getExtentOfChar(index).width <= step + 0.2,
              `${family}: ${run.textContent} character ${index} overlaps the next column`);
          }
        }
      }
    } finally {
      host.remove();
    }
  }
}
