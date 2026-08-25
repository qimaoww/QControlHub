import assert from "node:assert/strict";
import test from "node:test";

import { validatePullRequest } from "./check-pr.mjs";

const validBody = `## Summary

Complete the project CI and use a single validation entry point.

## Validation

- [x] \`make check\`

## Risk and rollback

This only affects CI and can be rolled back by reverting the workflow changes.

## Checklist

- [x] No sensitive information is included
`;

test("accepts a conventional title and completed template", () => {
  assert.deepEqual(
    validatePullRequest({ title: "ci(actions): enforce PR policy", body: validBody }),
    [],
  );
});

test("accepts breaking-change markers", () => {
  assert.deepEqual(
    validatePullRequest({ title: "feat(api)!: change the response format", body: validBody }),
    [],
  );
});

test("rejects a free-form title", () => {
  assert.match(
    validatePullRequest({ title: "Improve CI", body: validBody }).join("\n"),
    /Conventional Commit/u,
  );
});

test("rejects missing and template-only sections", () => {
  const body = `## Summary
<!-- Not completed -->

## Validation
- [ ] \`make check\`
`;
  const errors = validatePullRequest({ title: "ci: improve checks", body });
  assert.equal(errors.length, 3);
  assert.match(errors.join("\n"), /Summary/u);
  assert.match(errors.join("\n"), /Validation/u);
  assert.match(errors.join("\n"), /Risk and rollback/u);
});

test("reports an empty GitHub body instead of throwing", () => {
  const errors = validatePullRequest({ title: "ci: improve checks", body: null });
  assert.equal(errors.length, 3);
});

test("rejects non-English title and description characters", () => {
  const titleErrors = validatePullRequest({ title: "ci: 完善检查", body: validBody });
  assert.match(titleErrors.join("\n"), /title must be written in English/u);

  const bodyErrors = validatePullRequest({
    title: "ci: improve checks",
    body: validBody.replace("Complete the project CI", "完善项目 CI"),
  });
  assert.match(bodyErrors.join("\n"), /description must be written in English/u);
});

test("rejects titles longer than 100 characters", () => {
  const errors = validatePullRequest({
    title: `fix: ${"a".repeat(96)}`,
    body: validBody,
  });
  assert.match(errors.join("\n"), /100/u);
});
