import type {LicenceComponent, LicenceData, LicenceGroup, LicenceSource} from '../presentation/licences.ts';

/**
 * Building blocks of the licence generators (web: `web/scripts/licences.ts`; the apps:
 * `apps/native/scripts/licences.mts`). Pure: the scripts read the files and pass their contents
 * in, so the same rules (what counts as bundled, how a text is cleaned, how texts are shared)
 * hold for every client and are tested once.
 */

/** A component as a generator collects it, with its whole licence text (if any). */
export type CollectedComponent = Omit<LicenceComponent, 'text' | 'copyright'> & {
  /** Licence files as found, joined; copyright lines are split off by `buildLicenceData`. */
  text?: string;
  /** Copyright lines known apart from the text (a font's provenance, a POM's organisation). */
  copyright?: string[];
};

/** Line endings, trailing spaces and surrounding blank lines removed; nothing else changes. */
export function normaliseText(text: string): string {
  return text
    .replace(/^﻿/, '')
    .replace(/\r\n?/g, '\n')
    .split('\n')
    .map(line => line.replace(/[ \t]+$/, ''))
    .join('\n')
    .replace(/^\n+/, '')
    .replace(/\n+$/, '');
}

const COPYRIGHT_LINE = /^\s*(copyright\b|\(c\)\s|©)/i;

/**
 * Splits the copyright lines off the head of a licence text: lines before the licence body that
 * start with "Copyright", "(c)" or "©" (with an "All rights reserved." that follows one). A title
 * ("MIT License") and blank lines in the head stay. Copyright lines further down (inside the
 * body, as in Apache-2.0's own wording) are left alone. What is left is the shared licence text,
 * so the same MIT or BSD wording with different owners is stored once.
 */
export function splitCopyright(text: string): {copyright: string[]; body: string} {
  const lines = normaliseText(text).split('\n');
  // The GNU licences' own copyright line (the Free Software Foundation's, on the licence text
  // itself) belongs to the text, not to the component.
  if (/GNU (LESSER |LIBRARY )?GENERAL PUBLIC LICEN[CS]E|GNU AFFERO/i.test(lines.find(l => l.trim()) ?? '')) {
    return {copyright: [], body: normaliseText(text)};
  }
  const copyright: string[] = [];
  const kept: string[] = [];
  let i = 0;
  for (; i < lines.length; i++) {
    const line = lines[i]!;
    if (COPYRIGHT_LINE.test(line)) {
      copyright.push(line.trim());
      continue;
    }
    if (copyright.length && /^\s*all rights reserved\.?\s*$/i.test(line)) {
      copyright[copyright.length - 1] += ' ' + line.trim();
      continue;
    }
    // The head: blank lines and short title lines. Anything longer is the body.
    if (!line.trim() || (line.trim().length <= 60 && !/[.,;:]$/.test(line.trim()))) {
      kept.push(line);
      continue;
    }
    break;
  }
  const body = normaliseText([...kept, ...lines.slice(i)].join('\n')).replace(/\n{3,}/g, '\n\n');
  return {copyright, body};
}

/** Sorted by section, then by name without regard to case, then by version. */
function compareComponents(a: CollectedComponent, b: CollectedComponent, order: readonly LicenceGroup[]): number {
  return order.indexOf(a.group) - order.indexOf(b.group)
    || a.name.localeCompare(b.name, 'en', {sensitivity: 'base'})
    || a.name.localeCompare(b.name, 'en')
    || (a.version ?? '').localeCompare(b.version ?? '', 'en', {numeric: true})
    || a.id.localeCompare(b.id, 'en');
}

const GROUP_ORDER: readonly LicenceGroup[] = ['portico', 'packages', 'engine', 'android', 'apple', 'vega', 'subtitles', 'fonts', 'server'];

/**
 * The file a client loads: components sorted, copyright lines split off, identical texts stored
 * once (in order of first use, so the output is stable). Duplicate ids are a generator error.
 */
export function buildLicenceData(input: {client: string; sources: LicenceSource[]; components: CollectedComponent[]; inputs: Record<string, string>}): LicenceData {
  const seen = new Set<string>();
  for (const c of input.components) {
    if (seen.has(c.id)) throw new Error(`licences: ${c.id} is listed twice`);
    seen.add(c.id);
  }
  const sorted = [...input.components].sort((a, b) => compareComponents(a, b, GROUP_ORDER));
  const texts: string[] = [];
  const index = new Map<string, number>();
  const components = sorted.map(c => {
    const {text, copyright: known, ...rest} = c;
    const out: LicenceComponent = {...rest};
    const lines = [...(known ?? [])];
    if (text && text.trim()) {
      const split = splitCopyright(text);
      for (const line of split.copyright) if (!lines.includes(line)) lines.push(line);
      let at = index.get(split.body);
      if (at === undefined) {
        at = texts.length;
        texts.push(split.body);
        index.set(split.body, at);
      }
      out.text = at;
    }
    if (lines.length) out.copyright = lines;
    return out;
  });
  const inputs = Object.fromEntries(Object.entries(input.inputs).sort(([a], [b]) => a.localeCompare(b, 'en')));
  return {schema: 1, client: input.client, sources: input.sources, components, texts, inputs};
}

/** The generated file as written to disk: stable key order, one trailing newline. */
export function serialiseLicenceData(data: LicenceData): string {
  return JSON.stringify(data, null, 1) + '\n';
}

// ---------------------------------------------------------------------------------------------
// npm

export type LockPackage = {path: string; name: string; version: string; license?: string; resolved?: string; dev: boolean; link: boolean};

/** Every installed package of an npm lockfile (v2/v3), keyed by its `node_modules/...` path. */
export function lockPackages(lock: {packages?: Record<string, {name?: string; version?: string; license?: string; resolved?: string; dev?: boolean; devOptional?: boolean; link?: boolean}>}): Map<string, LockPackage> {
  const out = new Map<string, LockPackage>();
  for (const [path, entry] of Object.entries(lock.packages ?? {})) {
    if (!path.includes('node_modules/')) continue;
    const name = entry.name ?? path.slice(path.lastIndexOf('node_modules/') + 'node_modules/'.length);
    out.set(path, {
      path, name, version: entry.version ?? '', dev: !!(entry.dev || entry.devOptional), link: !!entry.link,
      ...(entry.license ? {license: entry.license} : {}),
      ...(entry.resolved ? {resolved: entry.resolved} : {}),
    });
  }
  return out;
}

/**
 * The production packages of a lockfile: everything not marked dev (or dev-optional), minus
 * workspace links (Portico's own packages) and type declarations (`@types/*`, never in a bundle).
 */
export function productionPackages(packages: Map<string, LockPackage>): LockPackage[] {
  return [...packages.values()].filter(p => !p.dev && !p.link && !p.name.startsWith('@types/') && !!p.version);
}

/** The lockfile path (`node_modules/a/node_modules/b`) of a file inside node_modules, from its path relative to the app. */
export function packagePathOf(relativeFile: string): string | undefined {
  const parts = relativeFile.split('/');
  let last = -1;
  for (let i = 0; i < parts.length; i++) if (parts[i] === 'node_modules') last = i;
  if (last < 0) return undefined;
  const nameParts = parts[last + 1]?.startsWith('@') ? 2 : 1;
  if (parts.length < last + 1 + nameParts) return undefined;
  return parts.slice(0, last + 1 + nameParts).join('/');
}

/** Licence-like files in a package folder listing, best first (LICENSE, then LICENCE, COPYING, NOTICE). */
export function licenceFiles(names: readonly string[]): string[] {
  const rank = (n: string) => {
    const u = n.toUpperCase();
    if (/^(LICENSE|LICENCE)(\.(MD|TXT|MARKDOWN|RST))?$/.test(u)) return 0;
    if (/^(LICENSE|LICENCE)[-_.]/.test(u)) return 1;
    if (/^(COPYING|UNLICENSE)/.test(u)) return 2;
    if (/^NOTICE/.test(u)) return 3;
    return -1;
  };
  return names.filter(n => rank(n) >= 0).sort((a, b) => rank(a) - rank(b) || a.localeCompare(b, 'en'));
}

// ---------------------------------------------------------------------------------------------
// Maven (Android)

/** A Maven POM licence name as an SPDX identifier where the name is unambiguous. */
export function spdxFromMavenName(name: string): string {
  const n = name.trim().toLowerCase().replace(/\s+/g, ' ');
  if (/^(the )?apache(\s|-)(software )?licen[sc]e,? (version )?2(\.0)?$/.test(n) || n === 'apache 2.0' || n === 'apache-2' || n === 'apache-2.0' || n === 'apache license 2.0') return 'Apache-2.0';
  if (n === 'mit' || n === 'mit license' || n === 'the mit license') return 'MIT';
  if (n === 'bsd license' || n === 'bsd') return 'BSD';
  if (n === 'bouncy castle licence' || n === 'bouncy castle license') return 'MIT (Bouncy Castle Licence)';
  return name.trim();
}

/** `<licenses>` names and `<organization>`/`<url>` of a POM, read with a few regular expressions (POMs are simple). */
export function readPom(xml: string): {licences: string[]; licenceUrl?: string; organisation?: string; url?: string; parent?: {group: string; artifact: string; version: string}} {
  const licences = [...xml.matchAll(/<license>[\s\S]*?<name>\s*([^<]+?)\s*<\/name>[\s\S]*?<\/license>/g)].map(m => m[1]!);
  const licenceUrl = xml.match(/<license>[\s\S]*?<url>\s*([^<]+?)\s*<\/url>[\s\S]*?<\/license>/)?.[1];
  const organisation = xml.match(/<organization>[\s\S]*?<name>\s*([^<]+?)\s*<\/name>/)?.[1];
  const project = xml.replace(/<parent>[\s\S]*?<\/parent>/, '').replace(/<(licenses|developers|organization|scm|dependencies|dependencyManagement|build|distributionManagement)>[\s\S]*?<\/\1>/g, '');
  const url = project.match(/<url>\s*([^<]+?)\s*<\/url>/)?.[1];
  const p = xml.match(/<parent>([\s\S]*?)<\/parent>/)?.[1];
  const parent = p ? {group: p.match(/<groupId>([^<]+)</)?.[1] ?? '', artifact: p.match(/<artifactId>([^<]+)</)?.[1] ?? '', version: p.match(/<version>([^<]+)</)?.[1] ?? ''} : undefined;
  return {licences, ...(licenceUrl ? {licenceUrl} : {}), ...(organisation ? {organisation} : {}), ...(url ? {url} : {}), ...(parent ? {parent} : {})};
}

/** `group:artifact:version` lines (comments and blanks ignored), as Gradle's check writes them. */
export function readDependencyList(text: string): {group: string; artifact: string; version: string}[] {
  return text.split('\n').map(l => l.trim()).filter(l => l && !l.startsWith('#')).map(l => {
    const [group, artifact, version] = l.split(':');
    if (!group || !artifact || !version) throw new Error(`licences: "${l}" is not group:artifact:version`);
    return {group, artifact, version};
  });
}

// ---------------------------------------------------------------------------------------------
// CocoaPods

/** The pods of a Podfile.lock (top-level names, without subspecs) and their versions. */
export function readPodfileLock(text: string): {name: string; version: string}[] {
  const pods = new Map<string, string>();
  const section = text.split(/\n(?=[A-Z][A-Z ]*:\n)/).find(s => s.startsWith('PODS:')) ?? '';
  for (const m of section.matchAll(/^  - "?([^\s"(]+) \(([^)]+)\)"?:?$/gm)) {
    const name = m[1]!.split('/')[0]!;
    if (!pods.has(name)) pods.set(name, m[2]!);
  }
  return [...pods].map(([name, version]) => ({name, version})).sort((a, b) => a.name.localeCompare(b.name, 'en'));
}

/** Where each pod comes from (`:path` / `:podspec` under EXTERNAL SOURCES); absent for trunk pods. */
export function podSources(text: string): Map<string, {path?: string; podspec?: string}> {
  const out = new Map<string, {path?: string; podspec?: string}>();
  const section = text.split(/\n(?=[A-Z][A-Z ]*:\n)/).find(s => s.startsWith('EXTERNAL SOURCES:')) ?? '';
  let current: string | undefined;
  for (const line of section.split('\n').slice(1)) {
    const name = line.match(/^  "?([^\s":]+)"?:$/)?.[1];
    if (name) { current = name; out.set(name, {}); continue; }
    const kv = line.match(/^    :(path|podspec): "?([^"]+)"?$/);
    if (kv && current) out.get(current)![kv[1] as 'path' | 'podspec'] = kv[2]!;
  }
  return out;
}
