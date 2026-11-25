// Canonical CSS projection for web. Run from any working directory.
import {existsSync, mkdirSync, readdirSync, readFileSync, statSync, writeFileSync} from 'node:fs';
import {dirname, join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {colors, profileArtColors, spacing, spacingGrid, radii, platformRadii, motion, easings, springs, springTiming, reducedMotion, layout, layers, targets, typography, fixedTypeMetrics, watchEndType, componentSpec, frameSpec, formScale, emphasis, toastCards, toastPill, popoverWidth} from '../src/index.ts';

export const tokenCssPath = fileURLToPath(new URL('../../../web/src/kit/tokens.css', import.meta.url));
const webSrcDir = fileURLToPath(new URL('../../../web/src', import.meta.url));
const kebab = value => value.replace(/[A-Z]/g, letter => '-' + letter.toLowerCase());
const rem = px => `${Number((px / 16).toFixed(6))}rem`;
// A length is a finite number; anything else is a fault in a table, so the script stops instead of writing `[object Object]px`.
const px = value => {if (!Number.isFinite(value)) throw new Error(`tokens.mjs: not a length: ${JSON.stringify(value)}`); return `${value}px`;};
const ms = value => `${value}ms`;
const scalar = value => String(value);
const rgba = (color, alpha) => `rgba(${[1, 3, 5].map(i => parseInt(colors[color].slice(i, i + 2), 16)).join(', ')}, ${alpha})`;

/** Collect every CSS custom property referenced in web/src (excluding the generated tokens.css).
 * Returns null when web/src is absent (e.g., isolated design-package temp-dir tests). */
function collectWebTokenReferences() {
  if (!existsSync(webSrcDir)) return null;
  const refs = new Set();
  let scanned = 0;
  const walk = dir => {
    for (const name of readdirSync(dir)) {
      const full = join(dir, name);
      if (statSync(full).isDirectory()) walk(full);
      else if (/\.(?:tsx?|css|html)$/.test(name) && full !== tokenCssPath) {
        scanned++;
        const text = readFileSync(full, 'utf8');
        for (const match of text.matchAll(/--[A-Za-z0-9_-]+/g)) refs.add(match[0]);
      }
    }
  };
  walk(webSrcDir);
  return scanned > 0 ? refs : null;
}

const CANONICAL_SPEC_TOKENS = new Set([
  '--spec-focus-scale-card-tv',
  '--spec-hero-mask-left-opaque-at',
]);

const ROW_SUBTITLE_TOKENS = new Set([
  '--type-row-subtitle-size',
  '--type-row-subtitle-line',
  '--type-row-subtitle-weight',
  '--type-row-subtitle-tracking',
  '--type-row-subtitle-transform',
]);

const CANONICAL_TYPE_SCALE_TOKENS = new Set([
  ...Object.keys(formScale.type.phone).flatMap(role => {
    const prefix = `--type-${kebab(role)}`;
    return [`${prefix}-size`, `${prefix}-line`, `${prefix}-weight`, `${prefix}-tracking`, `${prefix}-transform`];
  }),
  ...Object.keys(watchEndType).map(role => `--type-watch-end-${kebab(role)}`),
]);

const CANONICAL_COLOR_TOKENS = new Set(Object.keys(colors).map(name => `--color-${kebab(name)}`));

const declName = line => line.slice(0, line.indexOf(': '));
const declValue = line => line.slice(line.indexOf(': ') + 2);
const varRefs = value => [...value.matchAll(/var\(\s*(--[A-Za-z0-9_-]+)/g)].map(match => match[1]);

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
    if (/alpha|opacity|fraction|scale|saturation|brightness|aspect|At$/i.test(key) || path.some(part => ['scale', 'aspect'].includes(part)) || ['halfDetent', 'maxHeight', 'at', 'maxVisibleRows', 'lines', 'titleLines', 'bodyLines', 'dotAnimated'].includes(key) || path.includes('lines')) return scalar(value);
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
  // The audio player's wide forms, its icon classes and the video transport's (R4 and R6): a number, or a per-form table of
  // numbers. Anything else (the transport's `upright` block, read by native only) has no web token.
  const nested = (prefix, object) => Object.entries(object).flatMap(([key, value]) => typeof value === 'number' ? [`--${prefix}-${kebab(key)}: ${px(value)}`]
    : value && typeof value === 'object' && typeof pick(value) === 'number' ? [`--${prefix}-${kebab(key)}: ${px(pick(value))}`] : []);
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
    ...nested('frame-title-cover', f.hero.title.cover), `--frame-title-cover-gap: ${px(f.hero.title.coverGap)}`, `--frame-title-side: ${px(f.hero.title.side)}`,
    `--frame-title-about-label: ${px(f.hero.title.parts.aboutLabel[form])}`, `--frame-title-cast-circle: ${px(f.hero.title.parts.castCircle[form])}`, `--frame-title-number: ${px(f.hero.title.parts.number)}`,
    `--frame-title-numbered-art: ${px(f.hero.title.parts.numberedArt[form])}`, `--frame-title-neighbour-art: ${px(f.hero.title.parts.neighbourArt)}`, `--frame-title-choice-label: ${px(f.hero.title.parts.choiceLabel)}`, `--frame-title-choice-min: ${px(f.hero.title.parts.choiceMin)}`,
    `--frame-settings-list: ${px(web && form === 'expanded' ? f.settings.webListWidth : f.settings.listWidth[form])}`, `--frame-settings-gap: ${px(web && form === 'expanded' ? f.settings.webGap : f.settings.gap[form])}`,
    `--frame-audio-top-bar: ${px(f.audio.topBar[form])}`, `--frame-audio-artwork: ${px(pick(f.audio.artwork))}`, `--frame-audio-inset: ${px(f.audio.inset[form])}`,
    `--frame-audio-title-slot: ${px(f.audio.titleSlot[form])}`, `--frame-audio-scrubber: ${px(f.audio.scrubber[form])}`, `--frame-audio-transport: ${px(f.audio.transport[form])}`,
    `--frame-audio-next-line: ${px(f.audio.nextLine[form])}`, `--frame-audio-bottom-row: ${px(f.audio.bottomRow[form])}`,
    `--frame-audio-up-next: ${px(pick(f.audio.upNextWidth))}`, `--frame-audio-gap: ${px(pick(f.audio.gap))}`,
    `--frame-state-row-tile: ${px(f.stateRow.tile[form])}`,
    `--frame-pane-width: ${px(pick(f.pane.width))}`, `--frame-guide-per-minute: ${px(pick(f.guide.perMinute))}`, `--frame-guide-channel: ${px(pick(f.guide.channel))}`, `--frame-guide-row: ${px(pick(f.guide.row))}`,
    `--frame-row-art-recording: ${px(f.rowArt.recording[form])}`, `--frame-row-art-download: ${px(f.rowArt.download[form])}`, `--frame-quick-tile: ${px(f.quickTile.height[form])}`, `--frame-quick-tile-width: ${px(f.quickTile.width[form])}`, `--frame-quick-tile-gap: ${px(f.quickTile.gap[form])}`,
    `--frame-browse-grid-gap: ${px(f.browse.gridGap[form])}`, `--frame-browse-grid-card: ${px(f.browse.grid.card[form])}`, `--frame-browse-grid-column-gap: ${px(f.browse.grid.columnGap[form])}`, `--frame-browse-grid-row-gap: ${px(f.browse.grid.rowGap[form])}`,
    `--frame-browse-filters: ${px(pick(f.browse.filtersWidth))}`,
    `--frame-player-edge: ${px(f.player.edge[form])}`, `--frame-player-top: ${px(f.player.top[form])}`, `--frame-player-bottom: ${px(f.player.bottom[form])}`,
    `--frame-player-times-gap: ${px(f.player.timesGap[form])}`, `--frame-player-controls-gap: ${px(f.player.controlsGap[form])}`, `--frame-player-end-gap: ${px(f.player.endGap[form])}`, `--frame-player-title-gap: ${px(f.player.titleGap[form])}`,
    `--frame-player-pill-height: ${px(f.player.pill.height[form])}`, `--frame-player-pill-padding: ${px(f.player.pill.paddingX[form])}`,
    `--frame-player-preview-width: ${px(f.player.preview.width[form])}`, `--frame-player-preview-height: ${px(f.player.preview.height[form])}`, `--frame-player-preview-radius: ${px(f.player.preview.radius[form])}`, `--frame-player-preview-lift: ${px(f.player.preview.lift[form])}`,
    `--frame-player-prompt-height: ${px(f.player.prompt.height[form])}`, `--frame-player-prompt-radius: ${px(f.player.prompt.radius[form])}`, `--frame-player-prompt-padding: ${px(f.player.prompt.paddingX[form])}`, `--frame-player-prompt-dismiss: ${px(f.player.prompt.dismiss[form])}`, `--frame-player-prompt-bottom: ${px(f.player.prompt.bottom[form])}`,
    ...toastTokens(toastCards[form]),
  ];
}
/** R12, a toast's card on this form (packages/design `toastCards`); TV keeps its own toast. */
function toastTokens(card) {
  if (!card) return [];
  return [`--toast-radius: ${px(card.radius)}`, `--toast-padding: ${card.padding.map(px).join(' ')}`, `--toast-gap: ${px(card.gap)}`, `--toast-inset: ${card.inset === null ? 'auto' : px(card.inset)}`,
    `--toast-bottom: ${px(card.bottom)}`, `--toast-player-inset: ${card.playerInset === null ? 'auto' : px(card.playerInset)}`, `--toast-action-height: ${px(card.actionHeight)}`];
}
/** The same for every form. */
function frameConstants() {
  const f = frameSpec;
  const scrim = (edge, stops) => `linear-gradient(${edge}, ${stops.map(([alpha, at]) => `${alpha ? `color-mix(in srgb, var(--color-live-player-scrim) ${alpha * 100}%, transparent)` : 'transparent'} ${px(at)}`).join(', ')})`;
  return [`--popover-min-width: ${px(popoverWidth.min.web)}`, `--popover-max-width: ${px(popoverWidth.max)}`, `--toast-pill-height: ${px(toastPill.height)}`, `--toast-pill-radius: ${px(toastPill.radius)}`, `--toast-pill-padding-x: ${px(toastPill.paddingX)}`, `--toast-pill-top: ${px(toastPill.top)}`,
    `--frame-player-scrim: ${scrim('180deg', f.player.scrim.top)}, ${scrim('0deg', f.player.scrim.bottom)}`, `--frame-player-volume: ${px(f.player.volumeWidth)}`, `--frame-player-pill-icon: ${px(f.player.pillIcon)}`, `--frame-player-prompt-icon: ${px(f.player.promptIcon)}`,
    `--frame-player-portrait-aspect: ${f.player.portrait.aspect}`, `--frame-player-portrait-shade: ${f.player.portrait.shade * 100}%`, `--frame-player-portrait-top: ${px(f.player.portrait.top)}`, `--frame-player-portrait-prompt-height: ${px(f.player.portrait.promptHeight)}`, `--frame-player-portrait-prompt-padding-x: ${px(f.player.portrait.promptPaddingX)}`, `--frame-player-portrait-prompt-right: ${px(f.player.portrait.promptRight)}`, `--frame-player-portrait-edge: ${px(f.player.portrait.edge)}`, `--frame-player-portrait-bottom: ${px(f.player.portrait.bottom)}`, `--frame-player-portrait-time-inset: ${px(f.player.portrait.timeInset)}`, `--frame-player-portrait-scrubber: ${px(f.player.portrait.scrubber)}`, `--frame-title-credits-gap: ${px(f.hero.title.creditsGap)}`, `--frame-player-prompt-gap: ${px(f.player.prompt.gap)}`, `--frame-player-drawer: ${px(f.player.drawer)}`, `--frame-player-resume: ${px(f.player.resume.disc)}`, `--frame-player-resume-icon: ${px(f.player.resume.icon)}`,
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
  const rawRoot = [...entries(fixedTypeMetrics, 'type-fixed', scalar), ...entries(colors, 'color', scalar), ...entries(profileArtColors, 'profile-art', scalar), ...entries(spacing, 'space', px),
    ...spacingGrid.map(value => `--space-px-${value}: ${px(value)}`), ...entries(radii, 'radius', px),
    ...Object.entries(platformRadii).flatMap(([platform, values]) => entries(values, `radius-${platform}`, px)),
    ...entries(motion, 'motion', ms), ...entries(easings, 'ease', scalar), ...springEntries,
    `--motion-reduced: ${ms(reducedMotion.durationMs)}`, `--motion-reduced-easing: ${reducedMotion.easing}`,
    ...entries(layers, 'layer', scalar), ...entries(targets, 'target', px),
    `--font: ${typography.webFamily}`, `--font-mono: ${typography.monoFamily}`, `--page-gutter: ${px(layout.gutter.phone)}`, ...entries(layout.gutter, 'gutter', px),
    '--text-transform: none', `--glass-fill: ${rgba('text', componentSpec.button.glass.alpha)}`, `--glass-hover-fill: ${rgba('text', componentSpec.button.glass.hoverAlpha)}`,
    `--glass-blur: ${px(componentSpec.button.glass.blur)}`, ...flattenSpec(componentSpec), ...emphasisTokens(), ...platformTokens('phone'), ...frameConstants(), ...frameTokens('phone', true),
  ];
  const rawMedium = [...platformTokens('medium'), `--page-gutter: ${px(layout.gutter.tablet)}`, ...frameTokens('medium', true)];
  const rawDesktop = [...platformTokens('web'), '--page-gutter: clamp(32px, calc(7.6923076923vw - 46.7692307692px), 64px)', ...frameTokens('expanded', true)];
  const rawTv = [...platformTokens('tv'), `--page-gutter: ${px(layout.gutter.tv)}`, ...frameTokens('tv', false)];
  const webRefs = collectWebTokenReferences();
  let root = rawRoot, medium = rawMedium, desktop = rawDesktop, tv = rawTv;
  if (webRefs) {
    const rootValues = new Map(rawRoot.map(line => [declName(line), declValue(line)]));
    medium = rawMedium.filter(line => {
      const name = declName(line);
      return ROW_SUBTITLE_TOKENS.has(name) || webRefs.has(name) && declValue(line) !== rootValues.get(name);
    });
    const afterMedium = new Map(rootValues);
    for (const line of medium) afterMedium.set(declName(line), declValue(line));
    desktop = rawDesktop.filter(line => {
      const name = declName(line);
      return CANONICAL_TYPE_SCALE_TOKENS.has(name) || webRefs.has(name) && declValue(line) !== afterMedium.get(name);
    });
    const afterDesktop = new Map(afterMedium);
    for (const line of desktop) afterDesktop.set(declName(line), declValue(line));
    tv = rawTv.filter(line => {
      const name = declName(line);
      if (CANONICAL_TYPE_SCALE_TOKENS.has(name)) return true;
      const value = declValue(line);
      return webRefs.has(name) && (value !== rootValues.get(name) || value !== afterMedium.get(name) || value !== afterDesktop.get(name));
    });
    const requiredInRoot = new Set([
      ...webRefs,
      ...CANONICAL_COLOR_TOKENS,
      ...CANONICAL_TYPE_SCALE_TOKENS,
      ...CANONICAL_SPEC_TOKENS,
      '--motion-standard', '--ease-out', '--motion-reduced', '--motion-reduced-easing',
    ]);
    for (const line of [...medium, ...desktop, ...tv]) {
      for (const ref of varRefs(declValue(line))) requiredInRoot.add(ref);
    }
    let changed = true;
    while (changed) {
      changed = false;
      for (const line of rawRoot) {
        const name = declName(line);
        if (!requiredInRoot.has(name)) continue;
        for (const ref of varRefs(declValue(line))) {
          if (!requiredInRoot.has(ref)) {requiredInRoot.add(ref); changed = true;}
        }
      }
    }
    root = rawRoot.filter(line => requiredInRoot.has(declName(line)));
  }
  const reduced = [`.portico-motion`, `[data-portico-motion]`].join(', ');
  return `/* Canonical projection of packages/design. Run node packages/design/scripts/tokens.mjs. */\n` +
    block(':root', root) + '\n\n' +
    `@media (min-width: ${layout.breakpoint.phone}px) {\n${block(':root', medium)}\n}\n\n` +
    `@media (min-width: ${layout.breakpoint.desktop}px) {\n${block(':root', desktop)}\n}\n\n` +
    block('[data-platform="tv"]', tv) + '\n\n' +
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
    const css = renderTokens();
    mkdirSync(dirname(tokenCssPath), {recursive: true});
    writeFileSync(tokenCssPath, css);
    console.log('Wrote web/src/kit/tokens.css.');
  }
}
