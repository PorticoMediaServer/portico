import test from 'node:test';
import assert from 'node:assert/strict';
import {colors, componentSpec, contrastRatio, spacingGrid, targets, type ColorToken} from '../../design/src/index.ts';

const grid = new Set<number>(spacingGrid);
const hex = (token: ColorToken) => colors[token].slice(0, 7);
/** Composite a token at `alpha` over an opaque surface. */
function over(token: ColorToken, alpha: number, surface: ColorToken): string {
  const a = hex(token), b = hex(surface);
  const mix = [1, 3, 5].map(i => Math.round(parseInt(a.slice(i, i + 2), 16) * alpha + parseInt(b.slice(i, i + 2), 16) * (1 - alpha)));
  return '#' + mix.map(v => v.toString(16).padStart(2, '0')).join('');
}

test('every padding, gap, inset and margin in the component spec is on the spacing grid', () => {
  const offGrid: string[] = [];
  const walk = (value: unknown, path: string) => {
    if (value && typeof value === 'object') { for (const [k, v] of Object.entries(value)) walk(v, `${path}.${k}`); return; }
    if (typeof value !== 'number') return;
    if (!/(padding|gap|inset|margin)/i.test(path) || /Alpha|alpha|Fraction/.test(path)) return;
    if (!grid.has(Math.abs(value))) offGrid.push(`${path} = ${value}`);
  };
  walk(componentSpec, 'spec');
  assert.deepEqual(offGrid, []);
});

test('interactive heights meet the platform minimum targets', () => {
  const {button, row, menu, field, chip} = componentSpec;
  for (const size of ['md', 'lg'] as const) {
    assert.ok(button.height[size].phone >= targets.touch, `button ${size} phone`);
    assert.ok(button.height[size].tv >= targets.tenFoot, `button ${size} tv`);
  }
  assert.ok(button.minHit.phone >= targets.touch && chip.minHit.phone >= targets.touch);
  assert.ok(row.minHeight.single.phone >= targets.touch && row.minHeight.single.tv >= targets.tenFoot);
  assert.ok(menu.row.minHeight.phone >= targets.touch && menu.row.minHeight.web >= targets.menuRowPointer && menu.row.minHeight.tv >= targets.tenFoot);
  assert.ok(field.height.phone >= targets.touch && field.height.tv >= targets.tenFoot);
});

test('nothing focusable on TV is below the ten-foot minimum (APL-SYS-01)', () => {
  const {button, chip, segmented, row, menu, field, toggle} = componentSpec;
  const heights: [string, number][] = [
    ...(['sm', 'md', 'lg'] as const).map(s => [`button.${s}`, button.height[s].tv] as [string, number]),
    ['chip', chip.height.tv], ['segmented', segmented.height.tv], ['row', row.minHeight.single.tv], ['menu row', menu.row.minHeight.tv], ['field', field.height.tv],
  ];
  assert.deepEqual(heights.filter(([, h]) => h < targets.tenFoot), []);
  assert.ok(segmented.minHit.phone >= targets.touch);
  assert.ok(toggle.track.height.tv < targets.tenFoot, 'a switch is never focused on its own on TV: its row is');
});

test('button and chip labels keep 4.5:1 in every interactive state, on page and overlay surfaces', () => {
  const failures: string[] = [];
  for (const surface of ['ink', 'raised', 'overlaySurface'] as const) {
    for (const [variant, states] of Object.entries(componentSpec.button.variants)) {
      for (const [state, fill] of Object.entries(states)) {
        if (state === 'disabled') continue;
        const bg = fill.background === 'transparent' ? hex(surface) : over(fill.background, fill.backgroundAlpha ?? 1, surface);
        const ratio = contrastRatio(hex(fill.foreground), bg);
        if (ratio < 4.5) failures.push(`${variant}/${state} on ${surface}: ${ratio.toFixed(2)}`);
      }
    }
    for (const [state, fill] of Object.entries({rest: componentSpec.chip.rest, hover: componentSpec.chip.hover, selected: componentSpec.chip.selected})) {
      const bg = fill.background === 'transparent' ? hex(surface) : over(fill.background, fill.backgroundAlpha ?? 1, surface);
      const ratio = contrastRatio(hex(fill.foreground), bg);
      if (ratio < 4.5) failures.push(`chip/${state} on ${surface}: ${ratio.toFixed(2)}`);
    }
  }
  assert.deepEqual(failures, []);
});

test('overlays are opaque everywhere except desktop web, and pickers alone carry a close button', () => {
  const {overlay} = componentSpec;
  assert.equal(overlay.surface, 'raised');
  assert.deepEqual(overlay.translucentAllowed, {web: true, phone: false, tablet: false, tv: false});
  assert.deepEqual(overlay.closeButton.kinds, ['picker']);
  assert.deepEqual(overlay.footer.kinds, ['form', 'acknowledge']);
  assert.equal(overlay.closeButton.size.tv, 0);
});

test('heroes use frame geometry and continuous masks instead of viewport-height rules', () => {
  const {hero} = componentSpec;
  assert.deepEqual(hero.mobileWeb, {homeHeight: 486, titleHeight: 330});
  assert.deepEqual(hero.homeHeight, {web: 720, phone: 560, tablet: 560, tv: 1080});
  assert.deepEqual(hero.titleHeight, {web: 660, phone: 396, tablet: 396, tv: 1080});
  assert.deepEqual(hero.paddingX, {web: 64, phone: 20, tablet: 20, tv: 200});
  assert.deepEqual(hero.mask, {leftOpaqueAt: .38, bottomOpaqueAt: .42, phoneBottomOpaqueAt: .48, composite: 'intersect', webkitComposite: 'source-in'});
  assert.deepEqual(hero.ambient.blur, {web: 90, phone: 60, tablet: 60, tv: 90});
  assert.deepEqual(hero.ambient.alpha, {web: .32, phone: .45, tablet: .45, tv: .3});
  for (const stops of [hero.scrim.phoneOverlay, hero.scrim.tvLeft]) {
    assert.ok(stops.every((s, i) => s.alpha >= 0 && s.alpha <= 1 && s.at >= 0 && s.at <= 1 && (i === 0 || s.at > stops[i - 1]!.at)));
  }
  assert.equal(hero.identity.title, 'lockup');
  assert.equal(Object.hasOwn(hero.facts, 'separator'), false, 'facts are parts, without middle-dot copy');
  assert.deepEqual(hero.backdropWidth, {web: 1920, phone: 800, tablet: 800, tv: 1920});
  assert.equal(hero.rotation.intervalMs, 8000);
  assert.equal(hero.rotation.crossfadeMs, 900);
  assert.equal(hero.rotation.pausesOnHover && hero.rotation.pausesOnFocus, true);
  assert.equal(hero.rotation.reducedRotates, false);
});

test('hero buttons are white, with exact glass material and drawn platform geometry', () => {
  const {button} = componentSpec;
  assert.deepEqual(button.hero.height, {web: 54, phone: 46, tablet: 46, tv: 72});
  assert.deepEqual(button.hero.radius, {web: 12, phone: 10, tablet: 10, tv: 14});
  assert.deepEqual(button.hero.paddingX, {web: 26, phone: 20, tablet: 20, tv: 38});
  assert.deepEqual(button.variants.primary.rest, {background: 'text', foreground: 'ink'});
  assert.deepEqual(button.variants.primary.hover, {background: 'white', foreground: 'ink'});
  assert.deepEqual(button.variants.secondary.rest, {background: 'text', backgroundAlpha: .14, foreground: 'text'});
  assert.equal(button.variants.secondary.hover.backgroundAlpha, .22);
  assert.equal(button.glass.blur, 20);
  assert.deepEqual(button.glass.nativeBlur, {phone: true, appleTv: true, androidTv: false, fireTv: false});
  assert.deepEqual(button.glass.fallback, {background: 'text', backgroundAlpha: .14});
  assert.equal(button.pressedScale.web, .97);
});

test('hover and focus are distinct, with the TV ring, scale and soft glow', () => {
  const {card, focus} = componentSpec;
  assert.deepEqual([card.hover.artScale, card.hover.liveScale, card.hover.episodeScale, card.hover.durationMs], [1.045, 1.03, 1.03, 260]);
  assert.deepEqual(card.hover.shadow, {x: 0, y: 22, blur: 40, spread: -16, color: 'shadow', alpha: .85});
  assert.equal(focus.ringWidth.web, 3); assert.equal(focus.offsetOuter.web, 3);
  assert.equal(focus.ringWidth.tv, 4); assert.equal(focus.offsetOuter.tv, 4);
  assert.equal(focus.scale.card.web, 1); assert.equal(focus.scale.card.tv, 1.08);
  assert.equal(focus.scale.row.tv, 1.02);
  assert.deepEqual(focus.glow, {color: 'accentStrong', alpha: .6, x: 0, y: 26, blur: 64, spread: -12});
  assert.equal(focus.exactlyOneOnTv, true);
});

test('cards, rows, badges and fallbacks cover kit shapes without episode numerals', () => {
  const {card, shelf, episode, banner, generatedArt, chip, feedback} = componentSpec;
  assert.deepEqual([card.width.poster.web, card.width.wide.web, card.width.poster.phone, card.width.wide.phone, card.width.wide.tv], [190, 316, 118, 262, 400]);
  assert.deepEqual(card.aspect, {poster: 2 / 3, wide: 16 / 9, square: 1, circle: 1, magazine: 3 / 4});
  assert.deepEqual([card.radius.web, card.radius.tv, banner.radius, generatedArt.radius], [10, 14, 16, 14]);
  assert.deepEqual(shelf.gap, {web: 16, phone: 12, tablet: 12, tv: 28});
  assert.deepEqual(shelf.marginTop, {web: 44, phone: 28, tablet: 28, tv: 44});
  assert.equal(episode.fallback.numeral, false);
  assert.equal(episode.fallback.labelBelowTitle, true);
  assert.equal(card.live.dotAnimated, false);
  assert.deepEqual(chip.radius, {web: 10, phone: 9, tablet: 9, tv: 14});
  assert.equal(feedback.toast.action.variant, 'secondary');
  assert.equal(feedback.stateView.secondaryAction.variant, 'secondary');
});

test('navigation and activity tokens cover quiet, busy, update, hover, open and focus states', () => {
  const {navigation, activity} = componentSpec;
  assert.equal(navigation.top.height, 76);
  assert.equal(navigation.phone.tabBar.height, 84);
  assert.equal(navigation.mobileWeb.tabBar.height, 56);
  assert.deepEqual([navigation.tv.width, navigation.tv.openWidth, navigation.tv.openItemWidth], [260, 560, 320]);
  assert.equal(activity.glyph, 'levels');
  assert.equal(activity.height, 40); assert.equal(activity.icon, 22);
  assert.deepEqual([activity.hoverAlpha, activity.busyAlpha, activity.busyHoverAlpha, activity.openAlpha], [.1, .14, .22, .12]);
  assert.deepEqual([activity.panel.width, activity.panel.radius, activity.panel.alpha, activity.panel.blur], [420, 18, .94, 30]);
  assert.equal(activity.progress.indeterminate, false);
  assert.deepEqual(activity.panel.motion, {enterMs: 180, exitMs: 120, scaleFrom: .98, translateY: -4, reducedMs: 160});
});
