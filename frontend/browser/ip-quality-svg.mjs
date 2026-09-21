import { assert } from "./assertions.mjs";

// Compare actual image pixels under the production CSP. Never insert archive
// markup/styles into the console to inspect it: that changes its font context.
export async function testIPQualitySVG(images) {
  for (const image of images) {
    assert.equal(image.naturalHeight, 658, "native 47-row height changed");
    const box = image.getBoundingClientRect();
    assert.ok(Math.abs(box.width / box.height - image.naturalWidth / image.naturalHeight) < 0.01,
      "page stretched the archive image");
  }
  const reference = new Image();
  reference.src = "/assets/ip-quality-native-reference.svg";
  await reference.decode();
  const actual = images[0];
  assert.equal(actual.naturalWidth, reference.naturalWidth, "native image width changed");
  assert.equal(actual.naturalHeight, reference.naturalHeight, "native image height changed");
  const pixels = (image) => {
    const canvas = document.createElement("canvas");
    canvas.width = image.naturalWidth;
    canvas.height = image.naturalHeight;
    const context = canvas.getContext("2d", { willReadFrequently: true });
    context.drawImage(image, 0, 0);
    return context.getImageData(0, 0, canvas.width, canvas.height).data;
  };
  const got = pixels(actual), expected = pixels(reference);
  let difference = 0;
  for (let i = 0; i < got.length; i++) difference += Math.abs(got[i] - expected[i]);
  const mean = difference / got.length;
  assert.ok(mean < 2, `native image visual regression: mean channel difference ${mean}`);
}
