// Generates the open-source licence lists of the Portico apps (Settings › About › Open-source
// licenses) from the real inventories:
//
//   node --experimental-strip-types apps/native/scripts/licences.mts            # write the three lists
//   node --experimental-strip-types apps/native/scripts/licences.mts --check    # fail if a fresh run differs
//   node --experimental-strip-types apps/native/scripts/licences.mts --record   # record the JavaScript bundles first (Metro, ~3 min)
//
// Output: apps/native/licences/generated/android.json (Android, Android TV, Fire TV), apple.json
// (iPhone, iPad, Apple TV) and apps/vega/src/licences/vega.json (Vega).
//
// Inputs (all under apps/native/licences/ unless named):
//   js-android.txt, js-ios.txt, js-vega.txt
//       The npm packages actually in each JavaScript bundle, recorded by `--record` from Metro's
//       own module graph (devDependencies such as @babel/runtime's helpers are bundled; most of
//       react-native's dependencies are tooling and are not). Versions must match the lockfiles.
//   android-google.txt, android-firetv.txt
//       The release runtime classpaths, written and checked by Gradle (app/build.gradle,
//       checkReleaseOpenSourceLicences): a release build stops when they no longer match.
//       Licence names come from each artifact's POM and licence files from inside the artifact,
//       both in Gradle's cache; an artifact without a text gets the standard text of its licence.
//   ../ios/Podfile.lock, ../tvos/Podfile.lock
//       The pods. Pods that come from an npm package take that package's licence; the others are
//       read from Pods/<name> after `pod install` on a Mac. Without it (on Linux) a pod is listed
//       with its declared licence and its text is marked pending (the Mac step regenerates).
//   third_party/media-engine/sources.lock.json (Android) and third_party/apple-subtitles/sources.lock.json
//       (Apple), with each build's own notices
//       (MEDIA_ENGINE_OUT_DIR or third_party/media-engine/out, <platform>/notices): the player
//       engine's components and their licence files, including libdovi's Rust crates.
//   assets.json
//       Fonts and other vendored assets with their licence files; the test fails when a font in
//       apps/native/assets is not listed.
//   native-npm.json
//       Explicit native npm modules from the installed React Native autolink configuration.
//       Exact lock/installed versions, upstream repository and licence hashes are checked. Code
//       omitted from the production JS graph still belongs to its Android/Apple native category;
//       packages already represented by JS or their pods are listed once.
//
// Every repository file read is recorded with its SHA-256 in `inputs`; apps/native/test/
// licences.test.ts fails when one changes without the lists being generated again.
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {existsSync, readdirSync, readFileSync, realpathSync, rmSync, writeFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import os from 'node:os';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {buildLicenceData, licenceFiles, lockPackages, packagePathOf, podSources, readDependencyList, readPodfileLock, readPom, serialiseLicenceData, spdxFromMavenName, type CollectedComponent} from '../../../packages/client-core/src/licences/inventory.ts';
import {nativeNpmComponents, type NativeNpmEntry, type NativeClient} from './native-npm-licences.mts';
import {standardLicenceText} from '../../../packages/client-core/src/licences/standard-texts.ts';
import type {LicenceData, LicenceSource} from '../../../packages/client-core/src/presentation/licences.ts';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const APPS_SOURCE = 'https://github.com/PorticoMediaServer/portico-react-native';
const ENGINE_SOURCE = `${APPS_SOURCE}/releases`;
export const OUTPUTS = {android: 'apps/native/licences/generated/android.json', apple: 'apps/native/licences/generated/apple.json', vega: 'apps/vega/src/licences/vega.json'} as const;
type Client = keyof typeof OUTPUTS;
const INVENTORY = 'apps/native/licences';
const BUNDLES = {android: {app: 'apps/native', platform: 'android'}, apple: {app: 'apps/native', platform: 'ios'}, vega: {app: 'apps/vega', platform: 'kepler'}} as const;
const bundleList = (client: Client) => `${INVENTORY}/js-${BUNDLES[client].platform === 'kepler' ? 'vega' : BUNDLES[client].platform}.txt`;

function reader() {
  const inputs: Record<string, string> = {};
  const read = (rel: string): string => {
    const bytes = readFileSync(path.join(root, rel));
    inputs[rel] = createHash('sha256').update(bytes).digest('hex');
    return bytes.toString('utf8');
  };
  return {inputs, read};
}

/** LICENSE (or similar) and any NOTICE of a folder outside the repository (node_modules, a pod). */
function folderLicence(dir: string): string | undefined {
  if (!existsSync(dir)) return undefined;
  const names = licenceFiles(readdirSync(dir));
  const main = names.filter(n => !/^NOTICE/i.test(n)).slice(0, 2);
  const parts = [...main, ...names.filter(n => /^NOTICE/i.test(n))].map(n => readFileSync(path.join(dir, n), 'utf8').trim());
  return parts.length ? parts.join('\n\n') : undefined;
}

// ---------------------------------------------------------------------------------------------
// JavaScript bundles

/** `--record`: the npm packages in each bundle, from Metro's own graph (the source map's files). */
async function recordBundles(clients: readonly Client[]) {
  for (const client of clients) {
    const {app, platform} = BUNDLES[client];
    const appDir = path.join(root, app);
    const require = createRequire(path.join(appDir, 'package.json'));
    const Metro = require('metro');
    const config = await Metro.loadConfig({cwd: appDir, config: path.join(appDir, 'metro.config.js')});
    // A worktree may reach node_modules through a link; Metro must be allowed to read it.
    const modules = path.join(appDir, 'node_modules');
    const realModules = realpathSync(modules);
    const watchFolders = [...config.watchFolders, realModules];
    const out = path.join(os.tmpdir(), `portico-licences-${process.pid}-${platform}`);
    await Metro.runBuild({...config, watchFolders, maxWorkers: 1, resetCache: true}, {entry: path.join(appDir, 'index.js'), platform, dev: false, minify: false, out: out + '.js', sourceMap: true});
    const map = JSON.parse(readFileSync(out + '.map', 'utf8'));
    const lock = lockPackages(JSON.parse(readFileSync(path.join(appDir, 'package-lock.json'), 'utf8')));
    const found = new Map<string, string>();
    for (const source of map.sources as string[]) {
      const abs = path.resolve(path.dirname(out), source);
      const rel = abs.startsWith(realModules + path.sep) ? 'node_modules' + abs.slice(realModules.length) : path.relative(appDir, abs);
      const pkg = packagePathOf(rel.split(path.sep).join('/'));
      if (!pkg) continue;
      const entry = lock.get(pkg);
      if (!entry) throw new Error(`licences: ${pkg} is in the ${platform} bundle but not in ${app}/package-lock.json`);
      found.set(pkg, `${entry.name}@${entry.version}\t${pkg}`);
    }
    for (const f of [out + '.js', out + '.map']) rmSync(f, {force: true});
    const header = `# npm packages in the ${platform} JavaScript bundle (Metro's module graph). Written by apps/native/scripts/licences.mts --record.\n`;
    writeFileSync(path.join(root, bundleList(client)), header + [...found.values()].sort().join('\n') + '\n');
    console.log(`licences: ${bundleList(client)}: ${found.size} packages`);
  }
}

function npmComponents(client: Client, read: (rel: string) => string): CollectedComponent[] {
  const {app} = BUNDLES[client];
  const lock = lockPackages(JSON.parse(read(`${app}/package-lock.json`)));
  const out: CollectedComponent[] = [];
  for (const line of read(bundleList(client)).split('\n')) {
    if (!line.trim() || line.startsWith('#')) continue;
    const [nameVersion, pkg] = line.split('\t');
    const entry = lock.get(pkg!);
    if (!entry || `${entry.name}@${entry.version}` !== nameVersion) throw new Error(`licences: ${bundleList(client)} lists ${nameVersion}, ${app}/package-lock.json has ${entry ? entry.name + '@' + entry.version : 'nothing'} at ${pkg}; run --record`);
    const dir = path.join(root, app, pkg!);
    const installed = JSON.parse(readFileSync(path.join(dir, 'package.json'), 'utf8'));
    if (installed.version !== entry.version && !String(installed.version).startsWith(entry.version + '+')) throw new Error(`licences: ${app}/${pkg} is ${installed.version}, the lockfile says ${entry.version} (npm ci)`);
    const declared = entry.license ?? (typeof installed.license === 'string' ? installed.license : installed.license?.type) ?? 'See text';
    const own = folderLicence(dir);
    const author = typeof installed.author === 'string' ? installed.author.replace(/\s*[<(].*$/, '') : installed.author?.name;
    const text = own ?? standardLicenceText(declared);
    const repo = typeof installed.repository === 'string' ? installed.repository : installed.repository?.url;
    const url = installed.homepage ?? (repo ? String(repo).replace(/^git\+/, '').replace(/\.git$/, '').replace(/^git:\/\//, 'https://').replace(/^github:/, 'https://github.com/') : `https://www.npmjs.com/package/${entry.name}`);
    out.push({
      id: `npm:${pkg!.replace(/^node_modules\//, '')}@${entry.version}`, name: entry.name, version: entry.version, licence: declared, group: 'packages', url,
      ...(text ? {text} : {textPending: true}),
      ...(!own && author ? {copyright: [`Copyright (c) ${author}`]} : {}),
    });
  }
  return out;
}

// ---------------------------------------------------------------------------------------------
// The player engine

function engineComponents(platform: 'android' | 'apple', read: (rel: string) => string): CollectedComponent[] {
  // Apple ships AVPlayer plus subtitle/Opus libraries, independently of Android's mpv build.
  const recipe = platform === 'apple' ? 'third_party/apple-subtitles' : 'third_party/media-engine';
  const lock = JSON.parse(read(`${recipe}/sources.lock.json`));
  const configured = platform === 'apple' ? process.env.APPLE_SUBTITLE_OUT_DIR : process.env.MEDIA_ENGINE_OUT_DIR;
  const outDir = configured ? path.resolve(configured) : path.join(root, recipe, 'out');
  const noticeDirs = (platform === 'apple' ? ['apple'] : ['android', 'apple']).map(p => path.join(outDir, p, 'notices')).filter(d => existsSync(d));
  if (!noticeDirs.length) throw new Error(`licences: no ${platform} notices under ${outDir} (run ${recipe}/build-${platform}.sh, or set ${platform === 'apple' ? 'APPLE_SUBTITLE_OUT_DIR' : 'MEDIA_ENGINE_OUT_DIR'})`);
  if (platform === 'apple') {
    const source = JSON.parse(read(`${recipe}/${lock.components.ffmpeg.sourceLock}`));
    const receipt = JSON.parse(readFileSync(path.join(outDir, 'apple/source/portico-source-receipt.json'), 'utf8'));
    if (source.ffmpeg.commit !== lock.components.ffmpeg.sourceCommit || source.ffmpeg.sha256 !== lock.components.ffmpeg.sha256 || source.ffmpeg.patches.length !== 100 ||
      Object.keys(receipt).length !== 4 || receipt.sourceCommit !== source.ffmpeg.commit || receipt.sourceSha256 !== source.ffmpeg.sha256 || receipt.sourceDateEpoch !== source.sourceDateEpoch || JSON.stringify(receipt.patches) !== JSON.stringify(source.ffmpeg.patches)) {
      throw new Error('licences: Apple corresponding source does not match the checked Jellyfin patch receipt');
    }
  }
  const filesUnder = (dir: string, skip?: string): string[] => readdirSync(dir, {withFileTypes: true}).flatMap(e => {
    const full = path.join(dir, e.name);
    if (e.isDirectory()) return full === skip ? [] : filesUnder(full, skip);
    return [full];
  }).sort();
  const out: CollectedComponent[] = [];
  for (const [name, entry] of Object.entries(lock.components) as [string, {version: string; licence: string; platforms: string[]; url: string; prebuilt?: boolean}][]) {
    if (!entry.platforms.includes(platform)) continue;
    const folder = noticeDirs.map(d => path.join(d, name)).find(d => existsSync(d));
    if (platform === 'apple' && !folder) throw new Error(`licences: missing Apple build notice for ${name}`);
    const licence = entry.licence.replace(/\s*\(.*$/, '');
    // Before the other platform's build has collected a component's files (MoltenVK before the
    // first Mac build), a single standard licence is shown in its standard wording.
    const text = folder ? filesUnder(folder, path.join(folder, 'crates')).map(f => readFileSync(f, 'utf8').trim()).join('\n\n') : standardLicenceText(licence);
    out.push({
      id: `engine:${name}@${entry.version}`, name: displayName(name), version: entry.version, licence, group: 'engine',
      url: entry.url, ...(text ? {text} : {textPending: true}),
    });
    const crates = folder ? path.join(folder, 'crates') : undefined;
    if (crates && existsSync(crates)) {
      for (const crate of readdirSync(crates).sort()) {
        const m = crate.match(/^(.*)-(\d[^-]*)$/);
        const files = filesUnder(path.join(crates, crate));
        const declared = files.find(f => f.endsWith('LICENSE-NAME'));
        const own = files.filter(f => !f.endsWith('LICENSE-NAME')).map(f => readFileSync(f, 'utf8').trim()).join('\n\n');
        const licenceName = declared ? readFileSync(declared, 'utf8').replace(/^license\s*=\s*"?|"?\s*$/g, '') : /Apache License/.test(own) && /Permission is hereby granted/.test(own) ? 'MIT OR Apache-2.0' : /Apache License/.test(own) ? 'Apache-2.0' : /Permission is hereby granted/.test(own) ? 'MIT' : 'See text';
        const cText = own || standardLicenceText(licenceName);
        out.push({id: `crate:${crate}`, name: `${m?.[1] ?? crate} (Rust, in libdovi)`, version: m?.[2], licence: licenceName, group: 'engine', url: `https://crates.io/crates/${m?.[1] ?? crate}`, ...(cText ? {text: cText} : {textPending: true})});
      }
    }
  }
  return out;
}

const ENGINE_NAMES: Record<string, string> = {ffmpeg: 'FFmpeg', openssl: 'OpenSSL', freetype: 'FreeType', harfbuzz: 'HarfBuzz', fribidi: 'FriBidi', lcms2: 'Little CMS', moltenvk: 'MoltenVK'};
const displayName = (name: string) => ENGINE_NAMES[name] ?? name;

// ---------------------------------------------------------------------------------------------
// Android: Maven artifacts from Gradle's cache

const gradleCache = () => path.join(process.env.GRADLE_USER_HOME ?? path.join(os.homedir(), '.gradle'), 'caches/modules-2/files-2.1');

// This exact JitPack artifact omits licence metadata from its POM and AAR.
// Upstream: https://raw.githubusercontent.com/Dimezis/BlurView/version-2.0.4/LICENSE.md
const MAVEN_MISSING_LICENCES: Record<string, {licence: string; url: string}> = {
  'com.github.Dimezis:BlurView:version-2.0.4': {licence: 'Apache-2.0', url: 'https://github.com/Dimezis/BlurView/tree/version-2.0.4'},
};

function artifactFiles(group: string, artifact: string, version: string): string[] {
  const dir = path.join(gradleCache(), group, artifact, version);
  if (!existsSync(dir)) return [];
  return readdirSync(dir).flatMap(hash => readdirSync(path.join(dir, hash)).map(f => path.join(dir, hash, f)));
}

function zipEntries(archive: string): string[] {
  try { return execFileSync('unzip', ['-Z1', archive], {encoding: 'utf8', maxBuffer: 64 << 20}).split('\n').filter(Boolean); } catch { return []; }
}
const zipBytes = (archive: string, member: string) => execFileSync('unzip', ['-p', archive, member], {maxBuffer: 64 << 20});
const zipRead = (archive: string, member: string) => zipBytes(archive, member).toString('utf8');

const guessLicence = (text: string) => /Apache License/.test(text) ? 'Apache-2.0' : /Permission is hereby granted, free of charge/.test(text) ? 'MIT' : /Redistribution and use in source and binary forms/.test(text) ? 'BSD' : 'See text';

/**
 * Google's libraries name the third-party code inside them in third_party_licenses.json (offsets
 * into third_party_licenses.txt). Each becomes its own row, once however many libraries carry it.
 */
function googleNotices(archive: string, entries: string[]): {name: string; text: string}[] {
  if (!entries.includes('third_party_licenses.json') || !entries.includes('third_party_licenses.txt')) return [];
  const index = JSON.parse(zipRead(archive, 'third_party_licenses.json')) as Record<string, {start: number; length: number}>;
  const bytes = zipBytes(archive, 'third_party_licenses.txt');
  return Object.entries(index).map(([name, {start, length}]) => ({name, text: bytes.subarray(start, start + length).toString('utf8')}));
}

function pomOf(group: string, artifact: string, version: string, depth = 0): {licences: string[]; licenceUrl?: string; organisation?: string; url?: string} {
  const pom = artifactFiles(group, artifact, version).find(f => f.endsWith('.pom'));
  if (!pom) throw new Error(`licences: no POM for ${group}:${artifact}:${version} in ${gradleCache()} (build the app once so Gradle fetches it)`);
  const info = readPom(readFileSync(pom, 'utf8'));
  if (!info.licences.length && info.parent && depth < 4) {
    const parent = pomOf(info.parent.group, info.parent.artifact, info.parent.version, depth + 1);
    return {...parent, ...(info.url ? {url: info.url} : {}), ...(info.organisation ? {organisation: info.organisation} : {})};
  }
  return info;
}

function mavenComponents(read: (rel: string) => string): CollectedComponent[] {
  const flavours = {google: readDependencyList(read(`${INVENTORY}/android-google.txt`)), firetv: readDependencyList(read(`${INVENTORY}/android-firetv.txt`))};
  const all = new Map<string, {group: string; artifact: string; version: string; only: string[]}>();
  for (const [flavour, list] of Object.entries(flavours)) for (const d of list) {
    const key = `${d.group}:${d.artifact}:${d.version}`;
    const seen = all.get(key) ?? {...d, only: []};
    seen.only.push(flavour);
    all.set(key, seen);
  }
  const out: CollectedComponent[] = [];
  const bundled = new Map<string, {text: string; only: Set<string>}>();
  for (const [key, d] of all) {
    const pom = pomOf(d.group, d.artifact, d.version);
    const fallback = MAVEN_MISSING_LICENCES[key];
    const licence = pom.licences.length ? pom.licences.map(spdxFromMavenName).join(' OR ') : fallback?.licence ?? 'See text';
    const archive = artifactFiles(d.group, d.artifact, d.version).find(f => /\.(aar|jar)$/.test(f) && !/-(sources|javadoc)\.jar$/.test(f));
    const entries = archive ? zipEntries(archive) : [];
    const pick = (re: RegExp) => entries.filter(e => re.test(e) && !e.endsWith('/') && !e.endsWith('.class'));
    const own = [...pick(/(^|\/)(LICEN[CS]E|COPYING)(\.(txt|md))?$/i), ...pick(/(^|\/)NOTICE(\.(txt|md))?$/i)].map(e => zipRead(archive!, e).trim()).filter(Boolean);
    for (const notice of archive ? googleNotices(archive, entries) : []) {
      const seen = bundled.get(notice.name) ?? {text: notice.text, only: new Set<string>()};
      for (const f of d.only) seen.only.add(f);
      bundled.set(notice.name, seen);
    }
    // Google's own SDK terms are a web page (the POM's licence link), not a file in the library.
    const text = own.length ? own.join('\n\n') : standardLicenceText(licence);
    out.push({
      id: `maven:${key}`, name: `${d.group}:${d.artifact}`, version: d.version, licence, group: 'android',
      // Terms only on the web (Google's SDK terms): the licence's own page, which the screen offers.
      ...(pom.licenceUrl && !text ? {url: pom.licenceUrl} : pom.url || fallback?.url ? {url: pom.url ?? fallback?.url} : {}),
      ...(d.only.length < 2 ? {only: d.only} : {}),
      ...(text ? {text} : pom.licenceUrl ? {textAtUrl: true} : {textPending: true}),
      ...(!own.length && pom.organisation && !/Apache/.test(licence) ? {copyright: [`Copyright (c) ${pom.organisation}`]} : {}),
    });
  }
  for (const [name, {text, only}] of bundled) {
    out.push({id: `google-notice:${name}`, name: `${name} (in Google Play services)`, licence: guessLicence(text), group: 'android', text, ...(only.size < 2 ? {only: [...only]} : {})});
  }
  return out;
}

// ---------------------------------------------------------------------------------------------
// React Native's own native libraries (the third-party podspecs): pods on Apple, compiled into
// react-android on Android. Their texts are read from Pods/ after `pod install` (Mac).

const PODS_DIR = 'apps/native/ios/Pods';
/** Licences of pods from the CocoaPods trunk, whose podspecs are not on disk before `pod install`. */
const TRUNK_LICENCES: Record<string, {licence: string; url: string}> = {
  SocketRocket: {licence: 'BSD-3-Clause', url: 'https://github.com/facebookincubator/SocketRocket'},
  'google-cast-sdk': {licence: 'Google Cast SDK Additional Developer Terms of Service', url: 'https://developers.google.com/cast/docs/terms'},
};

function podspecInfo(file: string): {licence?: string; url?: string} {
  const spec = readFileSync(file, 'utf8');
  return {
    licence: spec.match(/\.license\s*=\s*\{\s*:type\s*=>\s*['"]([^'"]+)['"]/)?.[1] ?? spec.match(/\.license\s*=\s*['"]([^'"]+)['"]/)?.[1],
    url: spec.match(/\.homepage\s*=\s*['"]([^'"]+)['"]/)?.[1],
  };
}

function podText(name: string): string | undefined {
  return folderLicence(path.join(root, PODS_DIR, name));
}

function nativeLibraryComponents(read: (rel: string) => string, group: 'android' | 'apple'): CollectedComponent[] {
  const lock = read('apps/native/ios/Podfile.lock');
  const sources = podSources(lock);
  const pods = readPodfileLock(lock);
  const out: CollectedComponent[] = [];
  for (const pod of pods) {
    const podspec = sources.get(pod.name)?.podspec;
    if (!podspec || !podspec.includes('react-native/third-party-podspecs/')) continue;
    const info = podspecInfo(path.join(root, 'apps/native/ios', podspec));
    const text = podText(pod.name);
    out.push({id: `${group === 'android' ? 'rn-native' : 'pod'}:${pod.name}@${pod.version}`, name: pod.name, version: pod.version, licence: info.licence ?? 'See text', group, ...(info.url ? {url: info.url} : {}), ...(text ? {text} : {textPending: true})});
  }
  return out;
}

function podComponents(read: (rel: string) => string, inJs: Set<string>): CollectedComponent[] {
  const ios = read('apps/native/ios/Podfile.lock');
  const tvos = read('apps/native/tvos/Podfile.lock');
  const where = new Map<string, {version: string; only: string[]}>();
  for (const [variant, lock] of [['ios', ios], ['tvos', tvos]] as const) for (const pod of readPodfileLock(lock)) {
    const seen = where.get(pod.name) ?? {version: pod.version, only: []};
    seen.only.push(variant);
    where.set(pod.name, seen);
  }
  const sources = new Map([...podSources(tvos), ...podSources(ios)]);
  const out: CollectedComponent[] = [...nativeLibraryComponents(read, 'apple')];
  const npmAdded = new Set<string>();
  for (const [name, {version, only}] of where) {
    const source = sources.get(name);
    const located = source?.path ?? source?.podspec;
    const only2 = only.length < 2 ? {only} : {};
    if (located?.includes('react-native/third-party-podspecs/')) continue; // listed above
    if (located?.startsWith('build/generated/')) continue; // React Native's code generated for this app
    const npm = located?.match(/^\.\.\/node_modules\/((?:@[^/]+\/)?[^/]+)/)?.[1];
    if (npm && name !== 'hermes-engine') {
      // The pod is an npm package's own native code: listed once, under that package, with its licence.
      if (inJs.has(npm) || npmAdded.has(npm)) continue;
      npmAdded.add(npm);
      const dir = path.join(root, 'apps/native/node_modules', npm);
      const pkg = JSON.parse(readFileSync(path.join(dir, 'package.json'), 'utf8'));
      const text = folderLicence(dir) ?? standardLicenceText(pkg.license ?? '');
      out.push({id: `pod:${npm}@${pkg.version}`, name: npm, version: pkg.version, licence: pkg.license ?? 'See text', group: 'apple', ...only2, ...(text ? {text} : {textPending: true})});
      continue;
    }
    if (name === 'hermes-engine') {
      const rn = JSON.parse(readFileSync(path.join(root, 'apps/native/node_modules/react-native/package.json'), 'utf8'));
      const text = podText(name);
      out.push({id: `pod:${name}@${version}`, name: 'Hermes', version, licence: rn.license ?? 'MIT', group: 'apple', url: 'https://hermesengine.dev', ...only2, ...(text ? {text} : {textPending: true})});
      continue;
    }
    const trunk = TRUNK_LICENCES[name];
    if (!trunk) throw new Error(`licences: pod ${name} comes from ${located ?? 'the CocoaPods trunk'}; add its licence to TRUNK_LICENCES in apps/native/scripts/licences.mts`);
    const text = podText(name);
    out.push({id: `pod:${name}@${version}`, name, version, licence: trunk.licence, group: 'apple', url: trunk.url, ...only2, ...(text ? {text} : {textPending: true})});
  }
  return out;
}

// ---------------------------------------------------------------------------------------------
// Fonts and vendored assets

function assetComponents(read: (rel: string) => string): CollectedComponent[] {
  const list = JSON.parse(read(`${INVENTORY}/assets.json`)) as {assets: {id: string; name: string; version?: string; licence: string; licenceFile: string; files: string[]; url?: string}[]};
  return list.assets.map(a => {
    for (const f of a.files) if (!existsSync(path.join(root, f))) throw new Error(`licences: ${INVENTORY}/assets.json names ${f}, which does not exist`);
    return {id: a.id, name: a.name, ...(a.version ? {version: a.version} : {}), licence: a.licence, group: 'fonts' as const, text: read(a.licenceFile), ...(a.url ? {url: a.url} : {})};
  });
}

// Native npm libraries are autolinked even when their JS is not in the production graph.
let linkedNative: Map<string, NativeClient[]> | undefined;
function nativeNpm(client: NativeClient, represented: Set<string>, read: (rel: string) => string): CollectedComponent[] {
  if (!linkedNative) {
    const command = path.join(root, 'apps/native/node_modules/@react-native-community/cli/build/bin.js');
    const config = JSON.parse(execFileSync(process.execPath, [command, 'config'], {cwd: path.join(root, 'apps/native'), encoding: 'utf8'}));
    linkedNative = new Map(Object.entries(config.dependencies).flatMap(([name, value]) => {
      const platforms = (value as {platforms: Record<string, unknown>}).platforms;
      const clients: NativeClient[] = [...(platforms.android ? ['android' as const] : []), ...(platforms.ios || platforms.tvos ? ['apple' as const] : [])];
      return clients.length ? [[name, clients] as [string, NativeClient[]]] : [];
    }));
  }
  const inventory = JSON.parse(read(`${INVENTORY}/native-npm.json`)) as NativeNpmEntry[];
  const lock = lockPackages(JSON.parse(read('apps/native/package-lock.json')));
  return nativeNpmComponents(client, inventory, linkedNative, represented, name => {
    const pkg = JSON.parse(readFileSync(path.join(root, 'apps/native/node_modules', name, 'package.json'), 'utf8'));
    if (lock.get('node_modules/' + name)?.version !== pkg.version) throw new Error(`Native npm lock mismatch: ${name}`);
    return pkg;
  }, (name, file) => readFileSync(path.join(root, 'apps/native/node_modules', name, file), 'utf8'));
}

// Apple has separate actual Pod graphs. A library disabled for tvOS must not
// acquire a tvOS native-code notice merely because the iOS CLI links it.
function appleNativeScope(components: CollectedComponent[], read: (rel: string) => string): CollectedComponent[] {
  const variants = new Map<string, string[]>();
  for (const variant of ['ios', 'tvos']) {
    const names = new Set([...podSources(read(`apps/native/${variant}/Podfile.lock`)).values()]
      .map(source => (source.path ?? source.podspec)?.match(/^\.\.\/node_modules\/((?:@[^/]+\/)?[^/]+)/)?.[1])
      .filter((name): name is string => !!name));
    for (const name of names) variants.set(name, [...(variants.get(name) ?? []), variant]);
  }
  return components.map(component => {
    const only = variants.get(component.name);
    if (!only?.length) throw new Error(`Apple native library is absent from both installed Pod graphs: ${component.name}`);
    return only.length === 2 ? component : {...component, only};
  });
}

// ---------------------------------------------------------------------------------------------

export function generate(client: Client): LicenceData {
  const {inputs, read} = reader();
  const sources: LicenceSource[] = [{id: 'portico-apps', url: APPS_SOURCE}, ...(client === 'vega' ? [] : [{id: 'media-engine' as const, url: ENGINE_SOURCE}])];
  const npm = npmComponents(client, read);
  const native = client === 'vega' ? [] : nativeNpm(client, new Set(npm.map(c => c.name)), read);
  const components: CollectedComponent[] = [
    {id: 'portico', name: 'Portico apps', licence: 'GPL-3.0-or-later', group: 'portico', text: read('LICENSE'), url: APPS_SOURCE},
    ...npm,
    ...(client === 'apple' ? appleNativeScope(native, read) : native),
    ...assetComponents(read),
  ];
  if (client === 'android') components.push(...engineComponents('android', read), ...mavenComponents(read), ...nativeLibraryComponents(read, 'android'));
  if (client === 'apple') components.push(...engineComponents('apple', read), ...podComponents(read, new Set([...npm, ...native].map(c => c.name))));
  return buildLicenceData({client, sources, components, inputs});
}

export function selectedClients(args: readonly string[]): Client[] {
  const index = args.indexOf('--client');
  if (index < 0) return Object.keys(OUTPUTS) as Client[];
  const client = args[index + 1];
  if (client !== 'android' && client !== 'apple' && client !== 'vega') throw new Error('--client requires android, apple or vega');
  return [client];
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  const clients = selectedClients(process.argv.slice(2));
  if (process.argv.includes('--record')) await recordBundles(clients);
  let stale = 0;
  for (const client of clients) {
    const text = serialiseLicenceData(generate(client));
    const file = path.join(root, OUTPUTS[client]);
    const data = JSON.parse(text) as LicenceData;
    const pending = data.components.filter(c => c.textPending).map(c => c.name);
    if (process.argv.includes('--check')) {
      if (!existsSync(file) || readFileSync(file, 'utf8') !== text) { console.error(`licences: ${OUTPUTS[client]} is out of date`); stale++; }
      continue;
    }
    writeFileSync(file, text);
    console.log(`licences: wrote ${OUTPUTS[client]} (${data.components.length} components, ${data.texts.length} distinct texts, ${Math.round(text.length / 1024)} KB${pending.length ? `; text pending for ${pending.join(', ')}` : ''})`);
  }
  if (stale) { console.error('Run: node --experimental-strip-types apps/native/scripts/licences.mts'); process.exit(1); }
}
