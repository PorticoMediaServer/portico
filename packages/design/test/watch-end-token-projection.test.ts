import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {renderTokens, tokenCssPath} from '../scripts/tokens.mjs';
import {layout, typeScale, watchEndType} from '../src/index.ts';

/** Regression source for the nine canonical watch-end type roles and their
 * projection into the authored phone, desktop and TV scale blocks. Every value
 * is derived from packages/design and read back semantically; no checked-in CSS
 * value is copied into this test. */

/** The generator writes rem lengths at six decimal places, so a canonical px
 * value can differ by up to half a 1e-6rem step (8e-6px) after projection; add
 * a little float slack instead of tightening below the declared precision. */
const PX_TOLERANCE = (0.5e-6) * 16 + 1e-9;
const nearPx = (actual: number, expected: number) => Number.isFinite(actual) && Math.abs(actual - expected) <= PX_TOLERANCE;
const nearExact = (actual: number, expected: number) => Number.isFinite(actual) && Math.abs(actual - expected) < 1e-9;
const kebab = (value: string) => value.replace(/[A-Z]/g, letter => `-${letter.toLowerCase()}`);
const remToPx = (value: string) => {
  const match = /^(-?\d+(?:\.\d+)?)rem$/.exec(value);
  assert.ok(match, `expected a rem length, received ${value}`);
  return Number(match[1]) * 16;
};
const watchEndName = (role: string) => `--type-watch-end-${kebab(role)}`;

type Block = Readonly<{selector: string; declarations: ReadonlyMap<string, readonly string[]>}>;

/** Split the generated sheet into its top-level blocks (the generator joins
 * them with a blank line) and index every declaration inside each one. */
function blocks(css: string): readonly Block[] {
  return css.split('\n\n').map(part => {
    const lines = part.split('\n').map(line => line.trim());
    const selector = lines.find(line => line.endsWith('{')) ?? '';
    const declarations = new Map<string, string[]>();
    for (const line of lines) {
      if (!line.startsWith('--') || !line.endsWith(';')) continue;
      const colon = line.indexOf(': ');
      const name = line.slice(0, colon), value = line.slice(colon + 2, -1);
      declarations.set(name, [...(declarations.get(name) ?? []), value]);
    }
    return {selector: selector.slice(0, -1).trim(), declarations};
  });
}

function only(css: string, selector: string): Block {
  const found = blocks(css).filter(block => block.selector === selector);
  assert.equal(found.length, 1, `expected exactly one "${selector}" block`);
  return found[0];
}

function assertWatchEnd(block: Block, context: string) {
  for (const role of Object.keys(watchEndType)) {
    const expected = (watchEndType as Readonly<Record<string, number>>)[role], name = watchEndName(role);
    const values = block.declarations.get(name);
    assert.ok(values, `${context} must declare ${name}`);
    assert.equal(values.length, 1, `${context} must declare ${name} exactly once`);
    const value = values[0];
    assert.ok(!value.includes('var('), `${name} must be an owned literal, not a fallback reference`);
    if (role.endsWith('Size')) assert.ok(nearPx(remToPx(value), expected), `${context} ${name} is ${value}, expected ${expected}px`);
    else if (role.endsWith('Tracking')) assert.ok(nearExact(Number(value.replace(/em$/, '')), expected), `${context} ${name} is ${value}, expected ${expected}em`);
    else assert.ok(nearExact(Number(value), expected), `${context} ${name} is ${value}, expected ${expected}`);
  }
}

function assertTypeScale(block: Block, column: keyof typeof typeScale, context: string) {
  for (const [role, style] of Object.entries(typeScale[column])) {
    const prefix = `--type-${kebab(role)}`;
    const size = block.declarations.get(`${prefix}-size`), line = block.declarations.get(`${prefix}-line`);
    const weight = block.declarations.get(`${prefix}-weight`), tracking = block.declarations.get(`${prefix}-tracking`);
    assert.equal(size?.length, 1, `${context} must keep ${prefix}-size`);
    assert.ok(nearPx(remToPx(size![0]), style.size), `${context} ${prefix}-size is ${size![0]}, expected ${style.size}px`);
    assert.equal(line?.length, 1, `${context} must keep ${prefix}-line`);
    if (style.lineHeight === 'normal') assert.equal(line![0], 'normal');
    else assert.ok(nearPx(remToPx(line![0]), style.lineHeight), `${context} ${prefix}-line is ${line![0]}, expected ${style.lineHeight}px`);
    assert.deepEqual(weight, [String(style.weight)], `${context} must keep ${prefix}-weight`);
    assert.equal(tracking?.length, 1, `${context} must keep ${prefix}-tracking`);
    assert.ok(nearExact(Number(tracking![0].replace(/em$/, '')), style.tracking), `${context} ${prefix}-tracking is ${tracking![0]}, expected ${style.tracking}em`);
    assert.deepEqual(block.declarations.get(`${prefix}-transform`), ['none'], `${context} must keep ${prefix}-transform`);
  }
}

const desktop = `@media (min-width: ${layout.breakpoint.desktop}px)`;
const scales = [':root', desktop, '[data-platform="tv"]'] as const;

test('the generator projects the nine canonical watch-end roles once per authored scale block', () => {
  const css = renderTokens();
  for (const selector of scales) assertWatchEnd(only(css, selector), `generated ${selector}`);
});

test('the published sheet is the exact projected output and keeps those roles', () => {
  const published = readFileSync(tokenCssPath, 'utf8');
  // This is the actual --check parity: a stale unrelated value fails here even
  // when two fresh renderTokens() calls agree with each other.
  assert.equal(published, renderTokens(), 'web/src/kit/tokens.css must equal the canonical projection');
  for (const selector of scales) assertWatchEnd(only(published, selector), `published ${selector}`);
});

test('regeneration is idempotent and preserves every unrelated type role/value', () => {
  const first = renderTokens(), second = renderTokens();
  assert.equal(second, first, 'regeneration must not depend on prior output');
  const columns = [[':root', 'handheld'], [desktop, 'desktop'], ['[data-platform="tv"]', 'tenFoot']] as const;
  for (const [selector, column] of columns) {
    const block = only(first, selector);
    assertTypeScale(block, column, `generated ${selector}`);
    for (const role of Object.keys(watchEndType)) assert.equal(block.declarations.get(watchEndName(role))?.length, 1);
  }
});

test('reduction contexts never redeclare or mask an inherited watch-end role', () => {
  const css = renderTokens();
  const reduction = [`@media (prefers-reduced-motion: reduce)`, '[data-reduce-motion="true"]'];
  const reductionBlocks = blocks(css).filter(block => reduction.includes(block.selector) || block.selector.startsWith('[data-reduce-motion="true"] '));
  assert.ok(reductionBlocks.length >= 2, 'expected the existing reduction blocks');
  for (const block of reductionBlocks) {
    for (const role of Object.keys(watchEndType)) assert.equal(block.declarations.has(watchEndName(role)), false, `${block.selector} must inherit ${watchEndName(role)}`);
  }
  assert.ok(!/var\(--type-watch-end-/.test(css), 'watch-end roles must not be supplied through a var() fallback');
});
