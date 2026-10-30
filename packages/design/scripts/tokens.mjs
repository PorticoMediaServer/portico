// Canonical CSS projection for web. Run from any working directory.
import {mkdirSync, readFileSync, writeFileSync} from 'node:fs';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {colors, profileArtColors, spacing, spacingGrid, radii, platformRadii, motion, easings, springs, springTiming, reducedMotion, layout, layers, targets, typography, fixedTypeMetrics, typeScale, watchEndType, componentSpec} from '../src/index.ts';

export const tokenCssPath = fileURLToPath(new URL('../../../web/src/kit/tokens.css', import.meta.url));
const kebab = value => value.replace(/[A-Z]/g, letter => '-' + letter.toLowerCase());
const rem = px => `${Number((px / 16).toFixed(6))}rem`;
const px = value => `${value}px`;
const ms = value => `${value}ms`;
const scalar = value => String(value);
const rgba = (color, alpha) => `rgba(${[1, 3, 5].map(i => parseInt(colors[color].slice(i, i + 2), 16)).join(', ')}, ${alpha})`;

function typeTokens(column) {
  return Object.entries(typeScale[column]).flatMap(([role, value]) => {
    const prefix = `--type-${kebab(role)}`;
    return [`${prefix}-size: ${rem(value.size)}`, `${prefix}-line: ${value.lineHeight === 'normal' ? 'normal' : rem(value.lineHeight)}`, `${prefix}-weight: ${value.weight}`, `${prefix}-tracking: ${value.tracking}em`, `${prefix}-transform: none`];
  });
}

/** Numeric spec leaves are lengths except the explicitly dimensionless/time roles. */
function specValue(value, path) {
  if (typeof value === 'number') {
    const key = path.findLast(part => !['web', 'phone', 'tablet', 'tv'].includes(part));
    if (/Ms$/.test(key)) return ms(value);
    if (/alpha|opacity|fraction|scale|saturation|aspect|At$/i.test(key) || path.some(part => ['scale', 'aspect'].includes(part)) || ['halfDetent', 'maxHeight', 'at', 'maxVisibleRows', 'lines', 'titleLines', 'bodyLines', 'dotAnimated'].includes(key) || path.includes('lines')) return scalar(value);
    return px(value);
  }
  if (typeof value === 'boolean') return value ? '1' : '0';
  if (value === null) return 'none';
  if (Object.hasOwn(colors, value)) return `var(--color-${kebab(value)})`;
  return value;
}
function flattenSpec(object, path = []) {
  return Object.entries(object).flatMap(([key, value]) => {
    const parts = [...path, key];
    if (value && typeof value === 'object') return flattenSpec(value, parts);
    return [`--spec-${parts.map(kebab).join('-')}: ${specValue(value, parts)}`];
  });
}
/** The nine owned end-card roles, identical in every authored scale block. */
function watchEndTokens() {
  return [`--type-watch-end-kicker-size: ${rem(watchEndType.kickerSize)}`,
    `--type-watch-end-title-size: ${rem(watchEndType.titleSize)}`,
    `--type-watch-end-title-compact-size: ${rem(watchEndType.titleCompactSize)}`,
    `--type-watch-end-title-line: ${scalar(watchEndType.titleLine)}`,
    `--type-watch-end-title-tracking: ${watchEndType.titleTracking}em`,
    `--type-watch-end-feature-size: ${rem(watchEndType.featureSize)}`,
    `--type-watch-end-name-size: ${rem(watchEndType.nameSize)}`,
    `--type-watch-end-name-line: ${scalar(watchEndType.nameLine)}`,
    `--type-watch-end-year-size: ${rem(watchEndType.yearSize)}`];
}
function platformTokens(platform) {
  const column = platform === 'web' ? 'desktop' : platform === 'tv' ? 'tenFoot' : 'handheld';
  const {hero, button, card, chip, shelf} = componentSpec;
  return [...typeTokens(column), ...watchEndTokens(),
    `--hero-home-height: ${px(hero.homeHeight[platform])}`, `--hero-title-height: ${px(hero.titleHeight[platform])}`,
    `--hero-ambient-blur: ${px(hero.ambient.blur[platform])}`, `--hero-ambient-alpha: ${hero.ambient.alpha[platform]}`,
    `--button-hero-height: ${px(button.hero.height[platform])}`, `--button-hero-radius: ${px(button.hero.radius[platform])}`,
    `--button-hero-padding-x: ${px(button.hero.paddingX[platform])}`, `--button-hero-gap: ${px(button.hero.gap[platform])}`,
    `--card-poster-width: ${px(card.width.poster[platform])}`, `--card-wide-width: ${px(card.width.wide[platform])}`, `--card-square-width: ${px(card.width.square[platform])}`,
    `--card-radius: ${px(card.radius[platform])}`, `--chip-height: ${px(chip.height[platform])}`, `--chip-radius: ${px(chip.radius[platform])}`,
    `--row-gap: ${px(shelf.gap[platform])}`, `--row-margin-top: ${px(shelf.marginTop[platform])}`,
  ];
}
const block = (selector, lines) => `${selector} {\n${lines.map(line => `  ${line};`).join('\n')}\n}`;

export function renderTokens() {
  const entries = (object, prefix, format) => Object.entries(object).map(([key, value]) => `--${prefix}-${kebab(key)}: ${format(value)}`);
  const springEntries = Object.entries(springs).flatMap(([name, spring]) => {
    const timing = springTiming(spring);
    return [`--spring-${name}-duration: ${ms(timing.durationMs)}`, `--spring-${name}-easing: ${timing.css}`,
      ...Object.entries(spring).map(([key, value]) => `--spring-${name}-${key}: ${value}`)];
  });
  const root = [...entries(fixedTypeMetrics, 'type-fixed', scalar), ...entries(colors, 'color', scalar), ...entries(profileArtColors, 'profile-art', scalar), ...entries(spacing, 'space', px),
    ...spacingGrid.map(value => `--space-px-${value}: ${px(value)}`), ...entries(radii, 'radius', px),
    ...Object.entries(platformRadii).flatMap(([platform, values]) => entries(values, `radius-${platform}`, px)),
    ...entries(motion, 'motion', ms), ...entries(easings, 'ease', scalar), ...springEntries,
    `--motion-reduced: ${ms(reducedMotion.durationMs)}`, `--motion-reduced-easing: ${reducedMotion.easing}`,
    ...entries(layers, 'layer', scalar), ...entries(targets, 'target', px),
    `--font: ${typography.webFamily}`, `--font-mono: ${typography.monoFamily}`, `--page-gutter: ${px(layout.gutter.phone)}`, ...entries(layout.gutter, 'gutter', px),
    '--text-transform: none', `--glass-fill: ${rgba('text', componentSpec.button.glass.alpha)}`, `--glass-hover-fill: ${rgba('text', componentSpec.button.glass.hoverAlpha)}`,
    `--glass-blur: ${px(componentSpec.button.glass.blur)}`, ...flattenSpec(componentSpec), ...platformTokens('phone'),
  ];
  const reduced = [`.portico-motion`, `[data-portico-motion]`].join(', ');
  return `/* Canonical projection of packages/design. Run node packages/design/scripts/tokens.mjs. */\n` +
    block(':root', root) + '\n\n' +
    `@media (min-width: ${layout.breakpoint.desktop}px) {\n${block(':root', [...platformTokens('web'), '--page-gutter: clamp(32px, calc(7.6923076923vw - 46.7692307692px), 64px)'])}\n}\n\n` +
    block('[data-platform="tv"]', [...platformTokens('tv'), `--page-gutter: ${px(layout.gutter.tv)}`]) + '\n\n' +
    // Consumers opt their moving layers into this contract; static geometry is unaffected.
    block(reduced, ['transition-property: transform, opacity', 'transition-duration: var(--portico-motion-duration, var(--motion-standard))', 'transition-timing-function: var(--portico-motion-easing, var(--ease-out))']) + '\n\n' +
    `@media (prefers-reduced-motion: reduce) {\n${block(':root', ['--portico-motion-duration: var(--motion-reduced)', '--portico-motion-easing: var(--motion-reduced-easing)'])}\n${block(`${reduced}, .portico-motion::before, .portico-motion::after`, ['transition-property: opacity !important', 'transition-duration: var(--motion-reduced) !important', 'transition-timing-function: var(--motion-reduced-easing) !important', 'animation: none !important'])}\n${block('.portico-motion:not([data-motion-gesture]), [data-portico-motion]:not([data-motion-gesture])', ['transform: none !important'])}\n}\n\n` +
    block('[data-reduce-motion="true"]', ['--portico-motion-duration: var(--motion-reduced)', '--portico-motion-easing: var(--motion-reduced-easing)']) + '\n' +
    block('[data-reduce-motion="true"] .portico-motion, [data-reduce-motion="true"] [data-portico-motion], [data-reduce-motion="true"] .portico-motion::before, [data-reduce-motion="true"] .portico-motion::after', ['transition-property: opacity !important', 'transition-duration: var(--motion-reduced) !important', 'transition-timing-function: var(--motion-reduced-easing) !important', 'animation: none !important']) + '\n' + block('[data-reduce-motion="true"] .portico-motion:not([data-motion-gesture]), [data-reduce-motion="true"] [data-portico-motion]:not([data-motion-gesture])', ['transform: none !important']) + '\n';
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  if (args.length > 1 || args.some(arg => arg !== '--check')) {
    console.error('Usage: node packages/design/scripts/tokens.mjs [--check]');
    process.exitCode = 2;
  } else if (args.includes('--check')) {
    let current;
    try { current = readFileSync(tokenCssPath, 'utf8'); } catch (error) { if (error.code !== 'ENOENT') throw error; }
    if (current !== renderTokens()) {
      console.error('web/src/kit/tokens.css is missing or stale. Run node packages/design/scripts/tokens.mjs.');
      process.exitCode = 1;
    } else console.log('web/src/kit/tokens.css matches the canonical design tokens.');
  } else {
    mkdirSync(dirname(tokenCssPath), {recursive: true});
    writeFileSync(tokenCssPath, renderTokens());
    console.log('Wrote web/src/kit/tokens.css.');
  }
}
