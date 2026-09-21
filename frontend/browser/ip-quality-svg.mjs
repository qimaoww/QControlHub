import { assert } from "./assertions.mjs";

// Only test-owned, Go-generated fixtures are mounted here. Production renders
// archives as <img>; it never inserts report markup into the document.
export async function testIPQualitySVG(images) {
  for (const image of images) {
    // fetch is mocked by the API fixtures; XHR reaches the same local archive
    // endpoint the <img> loaded, including its production content type.
    const svg = await new Promise((resolve, reject) => {
      const request = new XMLHttpRequest();
      request.open("GET", image.src);
      request.responseType = "document";
      request.timeout = 4000;
      request.onload = () => request.status === 200 && request.responseXML
        ? resolve(request.responseXML.documentElement) : reject(new Error("SVG fixture failed to load"));
      request.onerror = request.ontimeout = () => reject(new Error("SVG fixture request failed"));
      request.send();
    });
    document.body.append(svg);
    try {
      assert.equal(svg.localName, "svg", "archive is not valid SVG");
      const runs = [...svg.querySelectorAll("text")];
      assert.ok(runs.length > 0, "archive lost its text");
      assert.ok(runs.map((run) => run.textContent).join("").includes("5.47%|"), "risk score text is missing");
      assert.equal(svg.querySelector("[textLength]"), null, "archive still adjusts run widths");
      // Force both common monospace fallbacks and a proportional fallback.
      // Every interior character must keep its column with each font.
      for (const family of ["monospace", "DejaVu Sans Mono", "sans-serif"]) {
        svg.querySelector("g").setAttribute("font-family", family);
        for (const run of runs) {
          const expected = run.getAttribute("x").split(" ").map(Number);
          assert.equal(run.getNumberOfChars(), expected.length, "fixture did not position every character");
          for (let index = 0; index < expected.length; index++) {
            const actual = run.getStartPositionOfChar(index).x;
            assert.ok(Math.abs(actual - expected[index]) < 0.1,
              `${family}: ${run.textContent} character ${index} drifted from ${expected[index]} to ${actual}`);
          }
        }
      }
    } finally {
      svg.remove();
    }
  }
}
