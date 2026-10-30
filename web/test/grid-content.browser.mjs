import assert from 'node:assert/strict';

/** Caller owns Vite/browser. Focused PERF-WEB-006 cases; no existing family is run.
 * Run geometry/content with contexts at DPR1,1.25,2,3 for real fractional scroll readback. */
export async function checkGridContent(page, origin, {retention = true} = {}) {
  await page.setViewportSize({width: 1280, height: 900});
  await page.goto(`${origin}/test/grid-content.html`, {waitUntil: 'networkidle'});
  const result = await page.evaluate(async () => (await import('/test/fixtures/grid-content.tsx')).checkGridContent());
  assert.equal(result.proof.length, 6);
  if (retention) result.retention = await page.evaluate(async () => (await import('/test/fixtures/grid-content.tsx')).checkGridContentRetention());
  return result;
}

/** Separate strict motion check: currently fails on the untouched baseline too. */
export async function checkGridContentMove(page, origin) {
  await page.goto(`${origin}/test/grid-content.html`, {waitUntil: 'networkidle'});
  return page.evaluate(async () => (await import('/test/fixtures/grid-content.tsx')).checkGridContentMove());
}
