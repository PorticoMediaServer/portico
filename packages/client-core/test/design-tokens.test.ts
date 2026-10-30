import test from 'node:test';
import assert from 'node:assert/strict';
import {colors, contrastRatio, icons, iconIds, isIconId, radii, platformRadii, typeScale, typography, motion, easings, springs, springPosition, springTiming, reducedMotion, motionRules, layout, componentSpec} from '../../design/src/index.ts';

// Surfaces text can sit on. "raised" is the hover/selected surface; overlay is every sheet, menu and dialog.
const surfaces = {canvas: colors.ink, slate: colors.raised, raised: colors.raisedHigh, overlay: colors.overlaySurface} as const;
const textTones = {text: colors.text, textSecondary: colors.textSecondary, textTertiary: colors.textTertiary, muted: colors.muted, placeholder: colors.placeholder, accent: colors.accent, accentStrong: colors.accentStrong, danger: colors.danger, amber: colors.amber, healthy: colors.healthy, record: colors.record} as const;

test('every text tone is at least 4.5:1 on canvas, slate, raised and overlay surfaces (X-06, X-07)', () => {
  for (const [tone, fg] of Object.entries(textTones)) {
    for (const [surface, bg] of Object.entries(surfaces)) {
      const ratio = contrastRatio(fg, bg);
      assert.ok(ratio >= 4.5, `${tone} on ${surface} is ${ratio.toFixed(2)}:1`);
    }
  }
});

test('primary, secondary and tertiary text stay readable on the brightest ordinary surface', () => {
  for (const tone of ['text', 'textSecondary', 'textTertiary'] as const) {
    const ratio = contrastRatio(colors[tone], colors.raisedBright);
    assert.ok(ratio >= 4.5, `${tone} on raisedBright is ${ratio.toFixed(2)}:1`);
  }
});

test('action ink reads on every accent state, including pressed (X-11)', () => {
  for (const state of ['accent', 'accentStrong', 'accentDeep'] as const) {
    const ratio = contrastRatio(colors.actionInk, colors[state]);
    assert.ok(ratio >= 4.5, `actionInk on ${state} is ${ratio.toFixed(2)}:1`);
  }
});

test('control boundaries and focus meet the 3:1 non-text minimum (X-11, WCAG 1.4.11)', () => {
  for (const [surface, bg] of Object.entries({canvas: colors.ink, slate: colors.raised, raised: colors.raisedHigh})) {
    assert.ok(contrastRatio(colors.lineControl, bg) >= 3, `lineControl on ${surface}`);
    assert.ok(contrastRatio(colors.focus, bg) >= 3, `focus on ${surface}`);
  }
});

test('overlay surface is opaque; only the web translucent variant carries alpha', () => {
  assert.match(colors.overlaySurface, /^#[0-9a-f]{6}$/i);
  assert.match(colors.overlaySurfaceTranslucent, /^#[0-9a-f]{8}$/i);
  assert.ok(parseInt(colors.overlaySurfaceTranslucent.slice(7), 16) >= 0xf0, 'translucency stays very small');
  assert.equal(colors.overlaySurfaceTranslucent.slice(0, 7).toLowerCase(), colors.overlaySurface.toLowerCase());
});

test('muted never lowers contrast below tertiary text (X-06)', () => {
  assert.equal(colors.muted, colors.textTertiary);
});

test('surface radii and platform geometry match the approved kit', () => {
  assert.deepEqual({tag: radii.tag, artwork: radii.artwork, heroButton: radii.heroButton, fallback: radii.fallback, banner: radii.banner}, {tag: 6, artwork: 10, heroButton: 12, fallback: 14, banner: 16});
  assert.equal(platformRadii.tv.artwork, 14);
  assert.equal(platformRadii.phone.heroButton, 10);
  assert.deepEqual(layout.gutter, {desktop: 64, desktopNarrow: 32, phone: 20, tablet: 20, tv: 200, tvRight: 96, tvAuth: 120, tvPlayer: 96});
  assert.deepEqual(layout.breakpoint, {phone: 600, desktop: 1024});
});

test('every original type role keeps its full handheld and ten-foot definition', () => {
  const roles = ['display', 'title', 'heading', 'subheading', 'body', 'bodyStrong', 'caption', 'captionStrong', 'micro', 'label'] as const;
  for (const role of roles) {
    assert.ok(typeScale.tenFoot[role].size > typeScale.handheld[role].size, role);
    const line = typeScale.handheld[role].lineHeight;
    assert.ok(typeof line === 'number' && line >= typeScale.handheld[role].size, role);
  }
  for (const column of Object.values(typeScale)) {
    assert.deepEqual(Object.keys(column).sort(), Object.keys(typeScale.handheld).sort());
    for (const style of Object.values(column)) {
      assert.ok(style.size > 0);
      assert.ok(style.lineHeight === 'normal' || style.lineHeight > 0);
      assert.notEqual(style.uppercase, true, 'all roles use sentence case');
    }
    assert.equal(column.label.tracking, 0);
  }
});

test('lockups, row headings and controls carry exact sizes, weights and tracking', () => {
  assert.deepEqual([typeScale.desktop.lockup.size, typeScale.handheld.lockup.size, typeScale.tenFoot.lockup.size], [84, 46, 120]);
  assert.equal(typeScale.desktop.lockupLong.size, 64);
  for (const column of Object.values(typeScale)) {
    assert.equal(column.lockup.weight, 800);
    assert.equal(column.lockup.tracking, -0.045);
    assert.ok(Math.abs(Number(column.lockup.lineHeight) / column.lockup.size - 0.94) < 1e-10);
  }
  assert.deepEqual(typeScale.desktop.rowHeading, {size: 22, lineHeight: 'normal', weight: 700, tracking: -0.015});
  assert.deepEqual(typeScale.desktop.nav, {size: 15, lineHeight: 'normal', weight: 600, tracking: 0});
  assert.equal(typeScale.desktop.cardTitle.size, 14);
  assert.equal(typeScale.desktop.cardTitle.weight, 600);
  assert.equal(typeScale.desktop.button.size, 17);
  assert.equal(typography.extraBold, 'Manrope-ExtraBold');
});

test('row subtitles preserve regular normal text with the drawn ten-foot size', () => {
  // tv-detail.html:82, f9 (5 titles) and f10 (2 of 4 watched).
  // Native Row's string subtitle consumes this role through createTheme.
  for (const [platform, size] of [['handheld', 14], ['desktop', 14], ['tenFoot', 20]] as const) {
    assert.deepEqual(typeScale[platform].rowSubtitle, {size, lineHeight: 'normal', weight: 400, tracking: 0}, platform);
  }
});

test('the binding motion table supplies all six durations, four curves and reduced-motion rules', () => {
  assert.deepEqual(motion, {quick: 120, standard: 180, hover: 260, slow: 320, morph: 420, hero: 900});
  assert.deepEqual(easings, {out: 'cubic-bezier(.2, .8, .2, 1)', in: 'cubic-bezier(.4, 0, 1, 1)', inOut: 'cubic-bezier(.4, 0, .2, 1)', linear: 'linear'});
  assert.deepEqual(reducedMotion, {durationMs: 160, easing: 'linear', transforms: false, springs: false, parallax: false, heroRotation: false, heroDrift: false, fingerTracking: true});
  assert.deepEqual(motionRules.animatedProperties, ['transform', 'opacity']);
  assert.equal(motionRules.queue, false);
  assert.equal(motionRules.interruptFromCurrent, true);
  assert.equal(motionRules.ambientLiveDots || motionRules.ambientSkeletons || motionRules.ambientLevelMeters, false);
});

test('native springs and the CSS linear samples represent the same damped motion', () => {
  assert.deepEqual(springs, {settle: {damping: 26, stiffness: 260, mass: 1}, player: {damping: 24, stiffness: 200, mass: 1}, snap: {damping: 30, stiffness: 420, mass: 1}});
  for (const [name, spring] of Object.entries(springs)) {
    const {durationMs, css} = springTiming(spring);
    assert.equal(durationMs, {settle: 500, player: 571, snap: 371}[name]);
    const samples = css.slice(7, -1).split(', ').map(Number);
    assert.equal(samples.length, 33);
    assert.equal(samples[0], 0);
    assert.equal(samples.at(-1), 1);
    assert.ok(Math.max(...samples) > 1 && Math.max(...samples) < 1.04);
    for (let i = 0; i < 32; i++) assert.ok(Math.abs(samples[i]! - springPosition(spring, durationMs * i / 32000)) <= 0.00005);
    for (let ms = durationMs; ms <= 3000; ms++) assert.ok(Math.abs(springPosition(spring, ms / 1000) - 1) < .002);
    assert.ok(springPosition(spring, .1, 2) > springPosition(spring, .1), 'release keeps the finger velocity');
  }
  // Critical and overdamped limits stay finite for future physical gestures.
  assert.ok(Number.isFinite(springPosition({damping: 20, stiffness: 100, mass: 1}, .2)));
  assert.ok(Number.isFinite(springPosition({damping: 30, stiffness: 100, mass: 1}, .2)));
});

test('icon registry: every glyph has a meaning and drawable elements', () => {
  assert.ok(iconIds.length >= 110);
  for (const id of iconIds) {
    const icon = icons[id];
    assert.equal(icon.id, id);
    assert.ok(icon.meaning.length > 3, id);
    assert.ok(icon.elements.length > 0, id);
    assert.ok(icon.strokeWidth > 0, id);
  }
});

test('icon registry carries the glyphs the audit found missing (X-17, CON-15/16)', () => {
  for (const id of ['airplay', 'googleCast', 'tv', 'tvDevice', 'keypad', 'flag', 'listPlus', 'sliders', 'watched', 'restart', 'archive', 'markUnread', 'bell', 'mail', 'phone', 'tablet', 'browser', 'wrench', 'logs', 'chevronUpDown', 'maximize', 'camera', 'image', 'error', 'success']) {
    assert.ok(isIconId(id), id);
  }
  assert.equal(isIconId('expand'), false);
  assert.equal(isIconId('constructor'), false);
});

// The generator is safe to run from any cwd.

import {cpSync, mkdtempSync, readFileSync, rmSync, writeFileSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {spawnSync} from 'node:child_process';
import {renderTokens, tokenCssPath} from '../../design/scripts/tokens.mjs';

test('row subtitle CSS projects the TV size without changing desktop or phone text', () => {
  const css = renderTokens();
  const blocks = [...css.matchAll(/(?:^|\n)(:root|\[data-platform="tv"\]) \{\n([\s\S]*?)\n\}/g)]
    .filter(match => match[2]!.includes('--type-row-subtitle-size:'));
  assert.equal(blocks.length, 3, 'phone, desktop and explicit TV token blocks');
  for (const [index, size] of ['0.875rem', '0.875rem', '1.25rem'].entries()) {
    const body = blocks[index]![2]!;
    assert.ok(body.includes(`--type-row-subtitle-size: ${size};`), blocks[index]![1]);
    assert.ok(body.includes('--type-row-subtitle-line: normal;'));
    assert.ok(body.includes('--type-row-subtitle-weight: 400;'));
    assert.ok(body.includes('--type-row-subtitle-tracking: 0em;'));
    assert.ok(body.includes('--type-row-subtitle-transform: none;'));
  }
});

test('web CSS is deterministic, complete and byte-identical to the canonical projection', () => {
  const css = renderTokens();
  assert.equal(css, renderTokens());
  assert.equal(readFileSync(tokenCssPath, 'utf8'), css);
  for (const [name, value] of Object.entries(colors)) {
    const key = name.replace(/[A-Z]/g, c => '-' + c.toLowerCase());
    assert.ok(css.includes(`--color-${key}: ${value};`), name);
  }
  assert.ok(css.includes('--spec-focus-scale-card-tv: 1.08;'));
  assert.ok(css.includes('--spec-hero-mask-left-opaque-at: 0.38;'));
  assert.ok(css.includes('--button-hero-height: 54px;'));
  assert.ok(css.includes('--type-lockup-size: 5.25rem;'));
  assert.ok(css.includes('--type-lockup-line: 4.935rem;'));
  assert.ok(css.includes('--type-row-heading-line: normal;'));
  assert.ok(css.includes('@media (min-width: 1024px)'));
  assert.ok(css.includes('@media (prefers-reduced-motion: reduce)'));
  assert.ok(css.includes('[data-reduce-motion="true"]'));
  assert.ok(css.includes('transition-property: opacity !important'));
  assert.ok(css.includes('transform: none !important'));
  assert.ok(css.includes(':not([data-motion-gesture])'), 'finger tracking survives reduced motion');
  assert.doesNotMatch(css, /uppercase|0\.01ms|web\/src\/ui/);
});

test('generator check rejects missing and stale files without writing', () => {
  const dir = mkdtempSync(join(tmpdir(), 'portico-design-tokens-'));
  try {
    const source = fileURLToPath(new URL('../../design', import.meta.url));
    cpSync(source, join(dir, 'packages/design'), {recursive: true});
    const script = join(dir, 'packages/design/scripts/tokens.mjs');
    const out = join(dir, 'web/src/kit/tokens.css');
    const run = (...args: string[]) => spawnSync(process.execPath, [script, ...args], {cwd: tmpdir(), encoding: 'utf8'});
    assert.equal(run('--check').status, 1);
    assert.throws(() => readFileSync(out), {code: 'ENOENT'});
    assert.equal(run().status, 0);
    assert.equal(run('--check').status, 0);
    const bytes = readFileSync(out, 'utf8');
    writeFileSync(out, bytes + '\n/* stale */\n');
    const stale = run('--check');
    assert.equal(stale.status, 1); assert.match(stale.stderr, /missing or stale/);
    assert.equal(readFileSync(out, 'utf8'), bytes + '\n/* stale */\n');
    assert.equal(run('--unexpected').status, 2);
  } finally { rmSync(dir, {recursive: true, force: true}); }
});
