// Canonical CSS projection for web. Run from any working directory.
import {mkdirSync, readFileSync, writeFileSync} from 'node:fs';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {colors, profileArtColors, spacing, spacingGrid, radii, platformRadii, motion, easings, springs, springTiming, reducedMotion, layout, layers, targets, typography, fixedTypeMetrics, watchEndType, componentSpec, frameSpec, formScale, emphasis} from '../src/index.ts';

export const tokenCssPath = fileURLToPath(new URL('../../../web/src/kit/tokens.css', import.meta.url));
const kebab = value => value.replace(/[A-Z]/g, letter => '-' + letter.toLowerCase());
const rem = px => `${Number((px / 16).toFixed(6))}rem`;
const px = value => `${value}px`;
const ms = value => `${value}ms`;
const scalar = value => String(value);
const rgba = (color, alpha) => `rgba(${[1, 3, 5].map(i => parseInt(colors[color].slice(i, i + 2), 16)).join(', ')}, ${alpha})`;

/** One form's type roles (R2), each with the colour of its level of emphasis (R3). */
function typeTokens(form) {
  return Object.entries(formScale.type[form]).flatMap(([role, value]) => {
    const prefix = `--type-${kebab(role)}`;
    return [`${prefix}-size: ${rem(value.size)}`, `${prefix}-line: ${value.lineHeight === 'normal' ? 'normal' : rem(value.lineHeight)}`, `${prefix}-weight: ${value.weight}`, `${prefix}-tracking: ${value.tracking}em`, `${prefix}-transform: none`,
      `${prefix}-color: var(--emphasis-${formScale.emphasis[form][role] ?? 'primary'})`];
  });
}
/** R3: the levels and what a control's state takes, the same on every form. */
function emphasisTokens() {
  const color = token => `var(--color-${kebab(token)})`;
  return [...Object.entries(emphasis.level).map(([level, token]) => `--emphasis-${level}: ${color(token)}`),
    `--emphasis-value: var(--emphasis-${emphasis.value.level})`, `--emphasis-value-weight: ${emphasis.value.weight}`,
    `--emphasis-value-chosen: var(--emphasis-${emphasis.valueChosen.level})`, `--emphasis-value-chosen-weight: ${emphasis.valueChosen.weight}`,
    `--emphasis-active: var(--emphasis-${emphasis.active})`, `--emphasis-on: var(--emphasis-${emphasis.on})`, `--emphasis-off: var(--emphasis-${emphasis.off})`,
    `--emphasis-progress-fill: ${color(emphasis.progress.fill)}`, `--emphasis-progress-track: ${rgba(emphasis.progress.track, emphasis.progress.trackAlpha)}`,
    `--emphasis-badge-background: ${color(emphasis.badge.background)}`, `--emphasis-badge-foreground: ${color(emphasis.badge.foreground)}`,
    ...['primary', 'secondary', 'tertiary'].map(name => `--icon-${name}-color: var(--emphasis-${formScale.icon[name].level})`)];
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
/** What a form decides (R2, R4): `phone`, `medium`, `web` or `tv`. What the form scale does not hold comes from the
 * component spec's column for it (`tablet` for `medium`). */
function platformTokens(form) {
  const platform = form === 'medium' ? 'tablet' : form;
  const {hero, chip, shelf} = componentSpec, {card, button, row, icon} = formScale, caption = card.caption;
  const buttons = Object.entries({hero: button.standard, small: button.small, xs: button.extraSmall}).flatMap(([name, size]) =>
    [`--button-${name}-height: ${px(size.height[form])}`, `--button-${name}-radius: ${px(size.radius[form])}`, `--button-${name}-padding-x: ${px(size.paddingX[form])}`, `--button-${name}-gap: ${px(size.gap[form])}`, `--button-${name}-icon: ${px(size.icon[form])}`]);
  return [...typeTokens(form), ...watchEndTokens(),
    `--hero-home-height: ${px(hero.homeHeight[platform])}`, `--hero-title-height: ${px(hero.titleHeight[platform])}`,
    `--hero-ambient-blur: ${px(hero.ambient.blur[platform])}`, `--hero-ambient-alpha: ${hero.ambient.alpha[platform]}`,
    ...buttons,
    `--card-poster-width: ${px(card.poster[form])}`, `--card-wide-width: ${px(card.wide[form])}`, `--card-square-width: ${px(card.square[form])}`, `--card-artist-width: ${px(card.artist[form])}`,
    `--card-radius: ${px(card.radius[form])}`, `--chip-height: ${px(chip.height[platform])}`, `--chip-radius: ${px(chip.radius[platform])}`,
    `--row-gap: ${px(card.gap[form])}`, `--row-margin-top: ${px(shelf.marginTop[platform])}`,
    `--card-caption-gap: ${px(caption.gap[form])}`, `--card-caption-title-line: ${px(caption.titleLine[form])}`, `--card-caption-subtitle-gap: ${px(caption.subtitleGap[form])}`, `--card-caption-subtitle-line: ${px(caption.subtitleLine[form])}`,
    `--card-caption-height: ${px(caption.gap[form] + caption.titleLine[form] + caption.subtitleGap[form] + caption.subtitleLine[form])}`,
    `--list-row-one-line: ${px(row.oneLine[form])}`, `--list-row-two-line: ${px(row.twoLine[form])}`, `--list-row-padding-x: ${px(row.paddingX[form])}`, `--list-row-padding-y: ${px(row.paddingY[form])}`,
    `--list-row-tile: ${px(row.tile[form])}`, `--list-row-tile-radius: ${px(row.tileRadius[form])}`, `--list-row-select: ${px(row.select[form])}`, `--list-row-chip: ${px(row.chip[form])}`, `--list-row-tab: ${px(row.tab[form])}`,
    ...['primary', 'secondary', 'tertiary'].flatMap(name => [`--icon-${name}: ${px(icon[name].size[form])}`, `--icon-${name}-box: ${px(icon.box[name][form])}`]),
  ];
}
/** The screen frames' measurements for one form (packages/design/src/frames.ts). `web` takes the browser's own
 * columns where the rules give one: the 64 gutter, the web type column, the web hero and settings widths. */
function frameTokens(form, web) {
  const f = frameSpec;
  const pick = (value, name = 'web') => web && form === 'expanded' && value[name] !== undefined ? value[name] : value[form];
  const hero = (name, value) => [
    `--frame-hero-${name}-height: ${px(web && form === 'phone' ? value.height.mobileWeb : pick(value.height))}`,
    `--frame-hero-${name}-lockup: ${rem(pick(value.lockup))}`,
    `--frame-hero-${name}-copy-bottom: ${px(pick(value.copyBottom))}`,
    `--frame-hero-${name}-synopsis-lines: ${value.synopsisLines[form]}`,
  ];
  // The audio player's wide forms, its icon classes and the video transport's (R4 and R6), each leaf a number or a per-form table.
  const nested = (prefix, object) => Object.entries(object).flatMap(([key, value]) => typeof value === 'number' ? [`--${prefix}-${kebab(key)}: ${px(value)}`]
    : value && typeof value === 'object' && form in value ? [`--${prefix}-${kebab(key)}: ${px(pick(value))}`] : []);
  return [
    ...nested('frame-audio-wide', f.audio.wide), ...nested('frame-audio-icon', f.audio.icons), ...nested('frame-player-transport', f.player.transport),
    `--frame-player-track: ${px(f.player.track[form])}`, `--frame-player-panel-width: ${px(f.player.panel.width[form])}`, `--frame-player-knob: ${px(pick(f.player.knob))}`, `--frame-player-knob-held: ${px(f.player.knobHeld[form])}`,
    `--frame-card-padding: ${px(f.card.padding[form])}`, `--frame-card-radius: ${px(f.card.radius[form])}`,
    `--frame-section-gap: ${px(f.rhythm.section[form])}`, `--frame-heading-gap: ${px(f.rhythm.headingGap[form])}`, `--frame-header-gap: ${px(f.rhythm.headerGap[form])}`,
    `--frame-row-section: ${px(pick(f.rhythm.rowSection))}`, `--frame-row-heading-gap: ${px(pick(f.rhythm.rowHeadingGap))}`, `--frame-row-bleed: ${px(pick(f.rhythm.rowBleed))}`,
    `--frame-title-top: ${px(pick(f.rhythm.titleTop))}`, `--frame-bottom-clear: ${px(f.rhythm.bottomClear[form])}`, `--frame-row-two-line: ${px(f.rhythm.rowTwoLine[form])}`,
    ...hero('home', f.hero.home), ...hero('title', f.hero.title),
    `--frame-hero-home-row-offset: ${px(pick(f.hero.home.rowOffset))}`,
    `--frame-hero-home-lockup-long: ${rem(web && form === 'expanded' ? f.hero.home.lockup.webLong : f.hero.home.lockup[form])}`,
    `--frame-title-action-gap: ${px(f.hero.title.actionGap[form])}`,
    `--frame-settings-list: ${px(web && form === 'expanded' ? f.settings.webListWidth : f.settings.listWidth[form])}`, `--frame-settings-gap: ${px(web && form === 'expanded' ? f.settings.webGap : f.settings.gap[form])}`,
    `--frame-audio-top-bar: ${px(f.audio.topBar[form])}`, `--frame-audio-artwork: ${px(pick(f.audio.artwork))}`, `--frame-audio-inset: ${px(f.audio.inset[form])}`,
    `--frame-audio-title-slot: ${px(f.audio.titleSlot[form])}`, `--frame-audio-scrubber: ${px(f.audio.scrubber[form])}`, `--frame-audio-transport: ${px(f.audio.transport[form])}`,
    `--frame-audio-next-line: ${px(f.audio.nextLine[form])}`, `--frame-audio-bottom-row: ${px(f.audio.bottomRow[form])}`, `--frame-audio-controls: ${px(pick(f.audio.controlsWidth))}`,
    `--frame-audio-up-next: ${px(pick(f.audio.upNextWidth))}`, `--frame-audio-gap: ${px(pick(f.audio.gap))}`,
    `--frame-state-row-tile: ${px(f.stateRow.tile[form])}`,
    `--frame-pane-width: ${px(pick(f.pane.width))}`, `--frame-guide-per-minute: ${px(pick(f.guide.perMinute))}`, `--frame-guide-channel: ${px(pick(f.guide.channel))}`, `--frame-guide-row: ${px(pick(f.guide.row))}`,
    `--frame-row-art-recording: ${px(f.rowArt.recording[form])}`, `--frame-row-art-download: ${px(f.rowArt.download[form])}`,
    `--frame-player-edge: ${px(f.player.edge[form])}`, `--frame-player-top: ${px(f.player.top[form])}`, `--frame-player-bottom: ${px(f.player.bottom[form])}`,
    `--frame-player-times-gap: ${px(f.player.timesGap[form])}`, `--frame-player-controls-gap: ${px(f.player.controlsGap[form])}`, `--frame-player-end-gap: ${px(f.player.endGap[form])}`, `--frame-player-title-gap: ${px(f.player.titleGap[form])}`,
    `--frame-player-pill-height: ${px(f.player.pill.height[form])}`, `--frame-player-pill-padding: ${px(f.player.pill.paddingX[form])}`,
    `--frame-player-preview-width: ${px(f.player.preview.width[form])}`, `--frame-player-preview-height: ${px(f.player.preview.height[form])}`, `--frame-player-preview-radius: ${px(f.player.preview.radius[form])}`, `--frame-player-preview-lift: ${px(f.player.preview.lift[form])}`,
    `--frame-player-prompt-height: ${px(f.player.prompt.height[form])}`, `--frame-player-prompt-radius: ${px(f.player.prompt.radius[form])}`, `--frame-player-prompt-padding: ${px(f.player.prompt.paddingX[form])}`, `--frame-player-prompt-dismiss: ${px(f.player.prompt.dismiss[form])}`, `--frame-player-prompt-bottom: ${px(f.player.prompt.bottom[form])}`,
  ];
}
/** The same for every form. */
function frameConstants() {
  const f = frameSpec;
  const scrim = (edge, stops) => `linear-gradient(${edge}, ${stops.map(([alpha, at]) => `${alpha ? `color-mix(in srgb, var(--color-live-player-scrim) ${alpha * 100}%, transparent)` : 'transparent'} ${px(at)}`).join(', ')})`;
  return [`--frame-player-scrim: ${scrim('180deg', f.player.scrim.top)}, ${scrim('0deg', f.player.scrim.bottom)}`, `--frame-player-volume: ${px(f.player.volumeWidth)}`, `--frame-player-pill-icon: ${px(f.player.pillIcon)}`, `--frame-player-prompt-icon: ${px(f.player.promptIcon)}`,
    `--frame-player-portrait-aspect: ${f.player.portrait.aspect}`, `--frame-player-portrait-shade: ${f.player.portrait.shade * 100}%`, `--frame-player-portrait-top: ${px(f.player.portrait.top)}`, `--frame-player-portrait-prompt-height: ${px(f.player.portrait.promptHeight)}`, `--frame-player-portrait-prompt-padding-x: ${px(f.player.portrait.promptPaddingX)}`, `--frame-player-portrait-prompt-right: ${px(f.player.portrait.promptRight)}`, `--frame-player-portrait-edge: ${px(f.player.portrait.edge)}`, `--frame-player-portrait-bottom: ${px(f.player.portrait.bottom)}`, `--frame-player-portrait-time-inset: ${px(f.player.portrait.timeInset)}`, `--frame-player-portrait-scrubber: ${px(f.player.portrait.scrubber)}`, `--frame-player-portrait-control: ${px(f.player.portrait.control)}`, `--frame-player-portrait-control-icon: ${px(f.player.portrait.controlIcon)}`, `--frame-player-portrait-primary: ${px(f.player.portrait.primary)}`, `--frame-player-portrait-primary-icon: ${px(f.player.portrait.primaryIcon)}`, `--frame-title-credits-gap: ${px(f.hero.title.creditsGap)}`, `--frame-player-prompt-gap: ${px(f.player.prompt.gap)}`,
    ...Object.entries(f.player.transport.upright.phone).map(([key, value]) => `--frame-player-upright-${kebab(key)}: ${px(value)}`), `--frame-player-tint-open: ${f.player.tint.open * 100}%`,
    `--frame-player-sideways-max-height: ${px(f.player.sidewaysMaxHeight)}`, `--frame-sheet-full-top: ${px(f.sheet.fullTop)}`, `--frame-audio-book-art-share: ${f.audio.bookArtShare}`, `--frame-sheet-full-radius: ${px(f.sheet.fullRadius)}`,
    `--frame-card-inset: ${px(f.card.phoneInset)}`, `--frame-hero-lockup-line: ${f.hero.lockupLine}`, `--frame-hero-lockup-tracking: ${f.hero.lockupTracking}em`,
    `--frame-state-row-icon-share: ${f.stateRow.iconShare}`, `--frame-state-row-error-tint: ${f.stateRow.errorTint * 100}%`,
    `--frame-guide-ruler: ${px(f.guide.ruler)}`, `--frame-guide-gap: ${px(f.guide.gap)}`,
    `--frame-header-bar: ${px(f.header.bar)}`, `--frame-header-back-row: ${px(f.header.backRow)}`, `--frame-settings-page-max: ${px(f.settings.pageMax)}`, `--frame-paragraph-max: ${px(f.settings.paragraphMax)}`,
    `--frame-sheet-footer-clear: ${px(f.sheet.footerClear)}`, `--frame-placeholder-icon-share: ${f.placeholder.iconShare}`, `--frame-placeholder-line: ${rgba('text', f.placeholder.lineAlpha)}`];
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
    `--glass-blur: ${px(componentSpec.button.glass.blur)}`, ...flattenSpec(componentSpec), ...emphasisTokens(), ...platformTokens('phone'), ...frameConstants(), ...frameTokens('phone', true),
  ];
  const reduced = [`.portico-motion`, `[data-portico-motion]`].join(', ');
  return `/* Canonical projection of packages/design. Run node packages/design/scripts/tokens.mjs. */\n` +
    block(':root', root) + '\n\n' +
    `@media (min-width: ${layout.breakpoint.phone}px) {\n${block(':root', [...platformTokens('medium'), `--page-gutter: ${px(layout.gutter.tablet)}`, ...frameTokens('medium', true)])}\n}\n\n` +
    `@media (min-width: ${layout.breakpoint.desktop}px) {\n${block(':root', [...platformTokens('web'), '--page-gutter: clamp(32px, calc(7.6923076923vw - 46.7692307692px), 64px)', ...frameTokens('expanded', true)])}\n}\n\n` +
    block('[data-platform="tv"]', [...platformTokens('tv'), `--page-gutter: ${px(layout.gutter.tv)}`, ...frameTokens('tv', false)]) + '\n\n' +
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
