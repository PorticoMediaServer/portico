// Generates the web app's open-source licence list (Settings › About › Open-source licenses):
//
//   node --experimental-strip-types web/scripts/licences.ts           # write the inventory
//   node --experimental-strip-types web/scripts/licences.ts --check   # fail if it differs from a fresh run
//
// What it lists, from the real inventories:
//   * Portico itself (LICENSE, GPL-3.0-or-later) and where its source is published.
//   * The npm packages the browser bundle is built from: the production packages of the root
//     package-lock.json (the lockfile the release build installs), without development tooling,
//     Portico's own workspace packages and type declarations. Texts from node_modules (the
//     installed version must be the locked one) and, for the patched hls.js, third_party/hls.js.
//   * The browser subtitle renderer and fonts in third_party/ (provenance.json files), the web
//     interface font and the icons.
//   * What the server binary carries (the web app is the server's own screen): Go and the Go
//     modules linked into it on every release platform (`go list -deps`, offline), the vendored
//     server code and fonts, and the qualified FFmpeg build.
//
// Every repository file read is recorded with its SHA-256 in `inputs`; web/test/licences.test.ts
// fails when any of them changes without the file being generated again. Needs node_modules and
// Go's module cache (both present after the usual `npm ci` and a server build); no network.
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync} from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {buildLicenceData, licenceFiles, lockPackages, productionPackages, serialiseLicenceData, type CollectedComponent} from '../../packages/client-core/src/licences/inventory.ts';
import {standardLicenceText} from '../../packages/client-core/src/licences/standard-texts.ts';
import {parseLicenceData, type LicenceData, type LicenceSource} from '../../packages/client-core/src/presentation/licences.ts';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
export const OUTPUT = 'web/src/generated/licences.json';

/** Other-platform optional packages are absent after a normal host npm ci. Retain
 * their already-recorded full notices only when the complete lock is identical. */
export function retainedOptionalNotice(data: LicenceData | undefined, lockText: string, packagePath: string): CollectedComponent | undefined {
  if (!data || data.inputs['package-lock.json'] !== createHash('sha256').update(lockText).digest('hex')) return;
  const entry = JSON.parse(lockText).packages?.[packagePath];
  if (!entry?.optional) return;
  const id = `npm:${packagePath.replace(/^node_modules\//, '')}@${entry.version}`;
  const component = data.components.find(component => component.id === id);
  if (!component || component.version !== entry.version || component.group !== 'packages' || component.licence !== entry.license
    || typeof component.text !== 'number' || !data.texts[component.text]) return;
  return {...component, text: data.texts[component.text], copyright: component.copyright ? [...component.copyright] : undefined};
}

const PORTICO_SOURCE = 'https://github.com/PorticoMediaServer/portico-server';

/** Repository files read, with their digests: the check compares them. */
function reader() {
  const inputs: Record<string, string> = {};
  const read = (rel: string): string => {
    const bytes = readFileSync(path.join(root, rel));
    inputs[rel] = createHash('sha256').update(bytes).digest('hex');
    return bytes.toString('utf8');
  };
  return {inputs, read};
}

/** A file outside the repository (node_modules, Go's module cache): pinned by a lockfile already. */
const readOutside = (file: string) => readFileSync(file, 'utf8');

function folderLicence(dir: string): string | undefined {
  if (!existsSync(dir)) return undefined;
  const names = licenceFiles(readdirSync(dir));
  // LICENSE (or similar) first; a NOTICE or PATENTS file travels with it.
  const main = names.filter(n => !/^NOTICE/i.test(n)).slice(0, 2);
  const extra = [...names.filter(n => /^NOTICE/i.test(n)), ...readdirSync(dir).filter(n => /^PATENTS$/i.test(n))];
  const parts = [...main, ...extra].map(n => readOutside(path.join(dir, n)).trim());
  return parts.length ? parts.join('\n\n') : undefined;
}

function npmComponents(read: (rel: string) => string): CollectedComponent[] {
  const lockText = read('package-lock.json'), lock = JSON.parse(lockText);
  const retained = existsSync(path.join(root, OUTPUT)) ? parseLicenceData(JSON.parse(readOutside(path.join(root, OUTPUT)))) ?? undefined : undefined;
  const out: CollectedComponent[] = [];
  for (const p of productionPackages(lockPackages(lock))) {
    if (p.name.startsWith('@portico/') || p.name === 'portico-web') continue;
    const dir = path.join(root, p.path);
    const installed = existsSync(path.join(dir, 'package.json')) ? JSON.parse(readOutside(path.join(dir, 'package.json'))) : undefined;
    if (!installed) {
      const notice = retainedOptionalNotice(retained, lockText, p.path);
      if (!notice) throw new Error(`licences: ${p.path} is not installed and has no identical-lock optional notice`);
      out.push(notice); continue;
    }
    if (installed.version !== p.version) throw new Error(`licences: ${p.path} is ${installed.version}, the lockfile says ${p.version} (run npm ci)`);
    const vendored = p.resolved?.startsWith('file:') && p.name === 'hls.js';
    const own = vendored ? read('third_party/hls.js/LICENSE') : folderLicence(dir);
    // A package that ships no licence file: the standard wording of the licence it declares,
    // with the author it names as the copyright holder.
    const declared = p.license ?? installed.license;
    const text = own ?? (declared ? standardLicenceText(declared) : undefined);
    if (!text) throw new Error(`licences: ${p.name} ${p.version} has no licence file and no standard text for "${declared}"`);
    const author = typeof installed.author === 'string' ? installed.author.replace(/\s*[<(].*$/, '') : installed.author?.name;
    const repo = typeof installed.repository === 'string' ? installed.repository : installed.repository?.url;
    const url = installed.homepage ?? (repo ? String(repo).replace(/^git\+/, '').replace(/\.git$/, '').replace(/^git:\/\//, 'https://') : `https://www.npmjs.com/package/${p.name}`);
    out.push({id: `npm:${p.path.replace(/^node_modules\//, '')}@${p.version}`, name: p.name, version: p.version, licence: p.license ?? installed.license ?? 'See text', group: 'packages', text, url, ...(!own && author ? {copyright: [`Copyright (c) ${author}`]} : {})});
  }
  return out;
}

function subtitleAndFontComponents(read: (rel: string) => string): CollectedComponent[] {
  const out: CollectedComponent[] = [];
  const octopus = JSON.parse(read('third_party/libass-wasm/provenance.json'));
  out.push({
    id: `vendor:libass-wasm@${octopus.version}`, name: 'JavascriptSubtitlesOctopus (libass for browsers)', version: octopus.version,
    licence: octopus.declaredLicense, group: 'subtitles', url: octopus.upstreamRepository,
    text: read('third_party/libass-wasm/LICENSE') + '\n\n' + read('third_party/libass-wasm/COPYRIGHT'),
  });
  const ofl = read('third_party/libass-wasm/OFL-1.1.txt');
  out.push({id: 'font:liberation-sans', name: 'Liberation Sans', version: '2.00.5', licence: 'OFL-1.1', group: 'fonts', text: ofl, url: 'https://github.com/liberationfonts/liberation-fonts'});
  const fonts = JSON.parse(read('third_party/subtitle-fonts/provenance.json'));
  const fontTexts: Record<string, string> = {'OFL-1.1': read('third_party/subtitle-fonts/OFL-1.1.txt')};
  for (const f of fonts.fonts as {family: string; package: string; upstream: string; license: string; copyright?: string}[]) {
    const text = fontTexts[f.license];
    if (!text) throw new Error(`licences: no ${f.license} text for ${f.family}`);
    out.push({id: `font:${f.family}`, name: f.family, version: f.package.split(' ').slice(1).join(' '), licence: f.license, group: 'fonts', text, url: f.upstream.split(' ')[0], ...(f.copyright ? {copyright: [f.copyright]} : {})});
  }
  out.push({id: 'font:manrope', name: 'Manrope', licence: 'OFL-1.1', group: 'fonts', text: read('web/src/kit/fonts/OFL.txt'), url: 'https://github.com/googlefonts/manrope'});
  out.push({id: 'icons:lucide', name: 'Lucide icons', licence: 'ISC', group: 'fonts', text: read('packages/design/LICENSE-LUCIDE'), url: 'https://lucide.dev'});
  return out;
}

/** The licence a Go module's own licence text states, from its wording alone.
 *
 * 2-clause and 3-clause BSD share their first two conditions, so the third
 * (non-endorsement) clause is what separates them. Its wording varies ("Neither
 * the name(s) of ...", first person, "may not"), so it is matched by the
 * endorsement restriction it states rather than one fixed sentence. The original
 * text is preserved separately; this only labels it. */
export function classifyGoLicence(text: string): string {
  const wording = text.replace(/\s+/g, ' ');
  if (/Apache License/.test(wording)) return 'Apache-2.0';
  if (/Permission is hereby granted, free of charge/.test(wording)) return 'MIT';
  if (/Redistribution and use in source and binary forms/.test(wording)) {
    return /(?:may|shall|must)(?: not)? be used to endorse or promote products derived from/i.test(wording)
      ? 'BSD-3-Clause' : 'BSD-2-Clause';
  }
  return 'See text';
}

/** The Go modules linked into the server binary on each release platform. */
function goModules(): {path: string; version: string; dir: string}[] {
  // Same capability list as the official build: an untagged `go list -deps`
  // omits modules behind portico_rar7z/portico_cb7, which would silently drop
  // their licences from Settings › About.
  const tags = execFileSync('python3', [path.join(root, 'scripts/release-build-tags.py'), '--capability'], {encoding: 'utf8'}).trim();
  if (!tags) throw new Error('licences: scripts/release-build-tags.py returned an empty capability list');
  const found = new Map<string, {path: string; version: string; dir: string}>();
  for (const goos of ['linux', 'darwin', 'windows']) for (const goarch of ['amd64', 'arm64']) {
    const out = execFileSync('go', ['list', '-deps', `-tags=${tags}`, '-f', '{{with .Module}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}', './cmd/server'], {
      cwd: path.join(root, 'server'), encoding: 'utf8', env: {...process.env, GOOS: goos, GOARCH: goarch, GOFLAGS: '-mod=readonly', GOPROXY: 'off'},
    });
    for (const line of out.split('\n')) {
      const [p, version, dir] = line.split('\t');
      if (!p || !version || !dir || p.startsWith('portico.local/')) continue;
      found.set(p, {path: p, version, dir});
    }
  }
  return [...found.values()];
}

function serverComponents(read: (rel: string) => string, licenceText: string): CollectedComponent[] {
  read('server/go.mod');
  read('server/go.sum');
  // The tag list decides which modules are linked, so it is a generation input.
  read('scripts/release-build-tags.py');
  const out: CollectedComponent[] = [];
  const goroot = execFileSync('go', ['env', 'GOROOT'], {encoding: 'utf8'}).trim();
  const goVersion = execFileSync('go', ['env', 'GOVERSION'], {encoding: 'utf8'}).trim().replace(/^go/, '');
  const toolchain = read('server/go.mod').match(/^toolchain go(\S+)/m)?.[1] ?? read('server/go.mod').match(/^go (\S+)/m)?.[1];
  out.push({id: 'go:std', name: 'Go standard library', version: toolchain ?? goVersion, licence: 'BSD-3-Clause', group: 'server', text: folderLicence(goroot), url: 'https://go.dev'});
  for (const m of goModules()) {
    const text = folderLicence(m.dir);
    if (!text) throw new Error(`licences: Go module ${m.path} has no licence file in ${m.dir}`);
    const licence = classifyGoLicence(text);
    out.push({id: `go:${m.path}@${m.version}`, name: m.path, version: m.version, licence: m.path === 'modernc.org/sqlite' ? 'BSD-3-Clause; SQLite is public domain' : licence, group: 'server', text, url: `https://pkg.go.dev/${m.path}@${m.version}`});
  }
  out.push({id: 'server:sqlite-driver', name: 'SQLite driver (adapted copy)', licence: 'BSD-3-Clause', group: 'server', text: read('server/internal/thirdparty/sqlite/LICENSE'), url: 'https://gitlab.com/cznic/sqlite'});
  out.push({id: 'server:image-scaler', name: 'Image scaling code from Go', licence: 'BSD-3-Clause', group: 'server', text: read('server/internal/imagework/SCALE_LICENSE'), url: 'https://cs.opensource.google/go/x/image'});
  const fonts: [string, string, string, string][] = [
    ['DejaVu Sans', 'server/internal/mediaexec/fonts/LICENSE', 'Bitstream Vera / Arev font terms; DejaVu changes public domain', 'https://dejavu-fonts.github.io'],
    ['Noto Sans CJK JP', 'server/internal/mediaexec/fonts/NotoCJK-LICENSE', 'OFL-1.1', 'https://github.com/notofonts/noto-cjk'],
    ['Noto Sans Devanagari (server)', 'server/internal/mediaexec/fonts/NotoSansDevanagari-LICENSE', 'OFL-1.1', 'https://github.com/notofonts/devanagari'],
    ['Noto Sans Thai (server)', 'server/internal/mediaexec/fonts/NotoSansThai-LICENSE', 'OFL-1.1', 'https://github.com/notofonts/thai'],
  ];
  for (const [name, file, licence, url] of fonts) out.push({id: `server-font:${name}`, name, licence, group: 'server', text: read(file), url});
  const ffmpeg = JSON.parse(read('third_party/ffmpeg/sources.lock.json'));
  out.push({
    id: 'server:ffmpeg', name: 'FFmpeg (qualified server build)', version: ffmpeg.ffmpeg?.version ?? ffmpeg.releaseTag, licence: 'GPL-3.0-or-later',
    // The bundle installed with the server carries every library's own licence in its licenses folder.
    group: 'server', text: licenceText, url: `${PORTICO_SOURCE}/releases/tag/${ffmpeg.releaseTag}`,
  });
  return out;
}

export function generate() {
  const {inputs, read} = reader();
  const licence = read('LICENSE');
  const ffmpeg = JSON.parse(read('third_party/ffmpeg/sources.lock.json'));
  const sources: LicenceSource[] = [
    {id: 'portico', url: PORTICO_SOURCE},
    {id: 'ffmpeg', url: `${PORTICO_SOURCE}/releases/tag/${ffmpeg.releaseTag}`},
  ];
  const components: CollectedComponent[] = [
    {id: 'portico', name: 'Portico Media Server and web app', licence: 'GPL-3.0-or-later', group: 'portico', text: licence, url: PORTICO_SOURCE},
    ...npmComponents(read),
    ...subtitleAndFontComponents(read),
    ...serverComponents(read, licence),
  ];
  return buildLicenceData({client: 'web', sources, components, inputs});
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  const text = serialiseLicenceData(generate());
  const file = path.join(root, OUTPUT);
  if (process.argv.includes('--check')) {
    const current = existsSync(file) ? readFileSync(file, 'utf8') : '';
    if (current !== text) {
      console.error(`licences: ${OUTPUT} is out of date. Run: node --experimental-strip-types web/scripts/licences.ts`);
      process.exit(1);
    }
    console.log(`licences: ${OUTPUT} is up to date`);
  } else {
    mkdirSync(path.dirname(file), {recursive: true});
    writeFileSync(file, text);
    const data = JSON.parse(text);
    console.log(`licences: wrote ${OUTPUT} (${data.components.length} components, ${data.texts.length} distinct texts, ${Math.round(text.length / 1024)} KB)`);
  }
}
