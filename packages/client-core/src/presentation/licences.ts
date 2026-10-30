import type {MessageId} from '../../../i18n/src/index.ts';

/**
 * The "Open-source licences" screen of every first-party client (Settings › About). Each client
 * carries a generated file (`licences.json`) listing what is actually built into it: the npm
 * packages in its JavaScript bundle, the player engine, the Android and Apple libraries, fonts
 * and vendored assets. The file is produced by each repository's licence script from the real
 * inventories (lockfiles, `sources.lock.json`, Gradle and CocoaPods lists) and a check fails
 * when it is out of date. Identical licence texts are stored once (`texts`).
 *
 * This module only reads that file: the order of the sections and what a row and a detail
 * page say. Pure and platform-free.
 */

/** Where a component sits on the screen. Portico's own notice always comes first. */
export type LicenceGroup = 'portico' | 'packages' | 'engine' | 'android' | 'apple' | 'vega' | 'subtitles' | 'fonts' | 'server';

export type LicenceComponent = {
  /** Stable and unique within a file (`npm:react@19.2.8`, `maven:androidx.core:core@1.16.0`). */
  id: string;
  name: string;
  version?: string;
  /** An SPDX expression where one is known ("MIT", "Apache-2.0", "LGPL-2.1-or-later"), else the licence's own name. */
  licence: string;
  group: LicenceGroup;
  /** The copyright lines, as the component's own notice states them. */
  copyright?: string[];
  /** Index into `texts`: the full licence text (copyright lines removed; they are above). */
  text?: number;
  /** Where the component comes from (its project or source). */
  url?: string;
  /** Only in these builds of the client (Android: `google`, `firetv`; Apple: `ios`, `tvos`). Absent: in all. */
  only?: string[];
  /** The text could not be collected where the file was generated (an Apple pod read on a Mac). */
  textPending?: boolean;
  /** The licence's terms are a web page (`url`), not a file in the component (Google's SDK terms). */
  textAtUrl?: boolean;
};

/** Where the corresponding source is published (top of the screen). */
export type LicenceSource = {id: 'portico' | 'portico-apps' | 'media-engine' | 'ffmpeg'; url: string};

export type LicenceData = {
  schema: 1;
  /** `web`, `android`, `apple` or `vega`. */
  client: string;
  sources: LicenceSource[];
  components: LicenceComponent[];
  texts: string[];
  /** SHA-256 of every inventory the file was generated from (the check compares them). */
  inputs: Record<string, string>;
};

export type LicenceSection = {group: LicenceGroup; title: MessageId; components: LicenceComponent[]};

const ORDER: readonly LicenceGroup[] = ['portico', 'packages', 'engine', 'android', 'apple', 'vega', 'subtitles', 'fonts', 'server'];
const TITLES: Record<LicenceGroup, MessageId> = {
  portico: 'licenses.section.portico',
  packages: 'licenses.section.packages',
  engine: 'licenses.section.engine',
  android: 'licenses.section.android',
  apple: 'licenses.section.apple',
  vega: 'licenses.section.vega',
  subtitles: 'licenses.section.subtitles',
  fonts: 'licenses.section.fonts',
  server: 'licenses.section.server',
};

/** Whether a component is in this build (`variant`: `google`/`firetv`, `ios`/`tvos`; none: every build). */
export function inBuild(component: LicenceComponent, variant?: string): boolean {
  return !component.only || !variant || component.only.includes(variant);
}

/** The screen's sections, in their fixed order; empty sections are left out. Components keep the file's order (by name). */
export function licenceSections(data: LicenceData, variant?: string): LicenceSection[] {
  const by = new Map<LicenceGroup, LicenceComponent[]>();
  for (const c of data.components) {
    if (!inBuild(c, variant)) continue;
    const list = by.get(c.group) ?? [];
    list.push(c);
    by.set(c.group, list);
  }
  return ORDER.filter(g => by.has(g)).map(group => ({group, title: TITLES[group], components: by.get(group)!}));
}

/** How many components the screen lists (Portico itself not counted). */
export function licenceCount(data: LicenceData, variant?: string): number {
  return data.components.filter(c => c.group !== 'portico' && inBuild(c, variant)).length;
}

/** A row's secondary line: "19.2.8 · MIT". */
export function licenceRowDetail(component: LicenceComponent): string {
  return [component.version, component.licence].filter(Boolean).join(' · ');
}

export type LicenceDetail = {component: LicenceComponent; copyright: string[]; text?: string};

/** One component with its copyright lines and full text, or undefined when the id is unknown. */
export function licenceDetail(data: LicenceData, id: string): LicenceDetail | undefined {
  const component = data.components.find(c => c.id === id);
  if (!component) return undefined;
  const text = component.text !== undefined ? data.texts[component.text] : undefined;
  return {component, copyright: component.copyright ?? [], ...(text ? {text} : {})};
}

/**
 * A licence text as paragraphs (split at blank lines; a very long paragraph at line ends), so a
 * television can move through it one focusable block at a time and a phone can draw it in a
 * virtualised list instead of one enormous text view.
 */
export function licenceParagraphs(text: string, max = 1200): string[] {
  const out: string[] = [];
  for (const block of text.replace(/\r\n?/g, '\n').split(/\n[ \t]*\n+/)) {
    const trimmed = block.replace(/^\n+|\s+$/g, '');
    if (!trimmed) continue;
    if (trimmed.length <= max) { out.push(trimmed); continue; }
    let current = '';
    for (const line of trimmed.split('\n')) {
      if (current && current.length + line.length + 1 > max) { out.push(current); current = ''; }
      current = current ? current + '\n' + line : line;
    }
    if (current) out.push(current);
  }
  return out;
}

const SOURCE_LABELS: Record<LicenceSource['id'], MessageId> = {
  portico: 'licenses.source.portico',
  'portico-apps': 'licenses.source.apps',
  'media-engine': 'licenses.source.mediaEngine',
  ffmpeg: 'licenses.source.ffmpeg',
};

/** The "where the source is published" rows, in the file's order. */
export function licenceSourceRows(data: LicenceData): {id: LicenceSource['id']; label: MessageId; url: string}[] {
  return data.sources.map(s => ({id: s.id, label: SOURCE_LABELS[s.id], url: s.url}));
}

/** Accepts only what the screen can draw; anything else is treated as unavailable. */
export function parseLicenceData(value: unknown): LicenceData | undefined {
  if (!value || typeof value !== 'object') return undefined;
  const d = value as Partial<LicenceData>;
  if (d.schema !== 1 || !Array.isArray(d.components) || !Array.isArray(d.texts) || !Array.isArray(d.sources)) return undefined;
  return d as LicenceData;
}
