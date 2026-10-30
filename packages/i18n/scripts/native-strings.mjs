#!/usr/bin/env node
// Canonical producer for OS-owned text. Native build/sync hooks call this after the shared
// packages are copied from a checked revision. Platform resource files are never hand mirrors.
import {readFileSync, writeFileSync, mkdirSync, renameSync, rmSync} from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {supportedLocales, catalogue, enUS} from '../src/catalog/index.ts';
import {parseMessage} from '../src/messageformat.ts';

const packageRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const defaultNativeRoot = path.resolve(packageRoot, '../../apps/native');
const compare = (a, b) => a < b ? -1 : a > b ? 1 : 0;
const ordered = entries => Object.fromEntries([...entries].sort(([a], [b]) => compare(a, b)));
const rowKey = key => key.startsWith('home.row.') || key.startsWith('next.home.');
const phraseKey = key => key.startsWith('system.siri.phrase.');
const argumentName = /^[A-Za-z0-9_]+$/;

/** Parse ICU once, then emit its literal text and plain arguments without ICU quoting. */
export function nativeText(message) {
  const nodes = parseMessage(message);
  if (nodes.some(node => node.kind !== 'text' && node.kind !== 'arg' || node.kind === 'arg' && !argumentName.test(node.name))) return undefined;
  return nodes.map(node => node.kind === 'text' ? node.value : `{${node.name}}`).join('');
}
export const appleLocale = locale => locale === 'en-US' ? 'en' : locale;
export function androidFolder(locale) {
  if (locale === 'en-US') return 'values';
  if (/^[a-z]{2,3}$/.test(locale)) return `values-${locale}`;
  const match = /^([a-z]{2,3})-([A-Z]{2})$/.exec(locale);
  if (!match) throw new Error(`Unsupported Android resource locale: ${locale}`);
  return `values-${match[1]}-r${match[2]}`;
}

/** Complete per-locale tables. Unsafe row titles are omitted on every platform, never
 * flattened into a misleading English approximation. Unsafe system text is a hard error. */
export function nativeTables(locales = supportedLocales, lookup = catalogue, source = enUS) {
  const keys = Object.keys(source).filter(key => key.startsWith('system.') || rowKey(key)).sort(compare);
  const tables = new Map(locales.map(locale => [locale, {}]));
  const skipped = [];
  for (const key of keys) {
    const values = locales.map(locale => {
      const raw = lookup(locale)[key] ?? source[key];
      try { return nativeText(raw); } catch (error) { throw new Error(`${locale} ${key}: ${error.message}`); }
    });
    if (values.some(value => value === undefined)) {
      if (key.startsWith('system.')) throw new Error(`System message is not native-safe: ${key}`);
      skipped.push(key);
      continue;
    }
    locales.forEach((locale, index) => { tables.get(locale)[key] = values[index]; });
  }
  return {tables, skipped};
}

function shortcut(value) { return value.replaceAll('{app}', '${applicationName}').replaceAll('{title}', '${title}'); }
function swiftPhrase(value) { return value.replaceAll('{app}', '\\(.applicationName)').replaceAll('{title}', '\\(\\.$title)'); }
function validatePhrases(tables) {
  for (const [locale, table] of tables) for (const [key, value] of Object.entries(table)) if (phraseKey(key)) {
    const names = parseMessage(value).filter(node => node.kind === 'arg').map(node => node.name);
    if (names.filter(name => name === 'app').length !== 1 || names.filter(name => name === 'title').length > 1 || names.some(name => name !== 'app' && name !== 'title')) throw new Error(`Invalid Siri phrase: ${locale} ${key}`);
  }
}
function stringCatalog(tables, codes, sourceTable, keyFor = key => key, valueFor = value => value) {
  const used = new Set();
  const entries = codes.map(code => {
    const key = keyFor(code, sourceTable[code]);
    if (used.has(key)) throw new Error(`Duplicate Apple source text: ${key}`);
    used.add(key);
    return [key, {
      comment: `${code}. Generated from packages/i18n; do not edit.`,
      extractionState: 'manual',
      localizations: ordered([...tables].map(([locale, table]) => [appleLocale(locale), {stringUnit: {state: 'translated', value: valueFor(table[code])}}])),
    }];
  });
  return JSON.stringify({sourceLanguage: 'en', version: '1.0', strings: ordered(entries)}, null, 2)
    .replace(/^(\s*"(?:\\.|[^"\\])*"):/gm, '$1 :') + '\n';
}
/** AppShortcuts.xcstrings requires iOS 17 even though the app supports 15.1.
 * The dedicated .strings table supports the existing App Intents availability
 * guard and keeps Apple's parameter notation in both keys and translations. */
export function shortcutStrings(source, table) {
  const quote = value => '"' + value.replace(/[\\"\n\r\t\u0000-\u001f\u2028\u2029]/g, char => {
    if (char === '\\' || char === '"') return '\\' + char;
    if (char === '\n') return '\\n';
    if (char === '\r') return '\\r';
    if (char === '\t') return '\\t';
    return '\\U' + char.charCodeAt(0).toString(16).padStart(4, '0');
  }) + '"';
  const used = new Set();
  const entries = Object.keys(source).filter(phraseKey).map(code => {
    const key = shortcut(source[code]);
    if (used.has(key)) throw new Error(`Duplicate Apple source text: ${key}`);
    used.add(key);
    return [key, shortcut(table[code])];
  }).sort(([a], [b]) => compare(a, b));
  return '/* Generated from packages/i18n; do not edit. */\n' + entries.map(([key, value]) => `${quote(key)} = ${quote(value)};`).join('\n') + '\n';
}
export function androidText(value) {
  return value.replaceAll('\\', '\\\\').replaceAll("'", "\\'").replaceAll('"', '\\"')
    .replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('\n', '\\n').replaceAll('\r', '\\r').replaceAll('\t', '\\t')
    .replace(/^([@?])/, '\\$1');
}
const resourceName = code => code.replaceAll('.', '_');
export function nativeOutputs(tables) {
  const source = tables.get('en-US');
  if (!source) throw new Error('Native strings require the en-US source locale.');
  validatePhrases(tables);
  const codes = Object.keys(source).sort(compare), rows = codes.filter(rowKey);
  const names = new Set();
  for (const code of codes) {
    const name = resourceName(code);
    if (!/^[A-Za-z][A-Za-z0-9_]*$/.test(name) || names.has(name)) throw new Error(`Invalid or colliding native resource name: ${code}`);
    names.add(name);
  }
  const outputs = new Map();
  const add = (platform, file, value) => outputs.set(file, {platform, value});
  add('apple', 'ios/Portico/Strings/Localizable.xcstrings', stringCatalog(tables, codes.filter(key => key.startsWith('system.siri.') && !phraseKey(key)), source, (_key, value) => value));
  for (const [locale, table] of tables) add('apple', `ios/Portico/Strings/${appleLocale(locale)}.lproj/AppShortcuts.strings`, shortcutStrings(source, table));
  for (const platform of ['ios', 'tvos']) {
    add('apple', `${platform}/Portico/Strings/InfoPlist.xcstrings`, stringCatalog(tables, [`system.apple.localNetwork.${platform}`], source, () => 'NSLocalNetworkUsageDescription'));
  }
  add('apple', 'tvos/TopShelf/Strings/PorticoSystem.xcstrings', stringCatalog(tables, rows, source));
  const androidCodes = codes.filter(key => key.startsWith('system.android.') || rowKey(key));
  for (const [locale, table] of tables) {
    const xml = '<?xml version="1.0" encoding="utf-8"?>\n<!-- Generated from packages/i18n; do not edit. -->\n<resources>\n' +
      androidCodes.map(code => `  <string name="${resourceName(code)}" formatted="false">${androidText(table[code])}</string>`).join('\n') + '\n</resources>\n';
    add('android', `android/app/src/main/res/${androidFolder(locale)}/portico_system_strings.xml`, xml);
  }
  const kotlin = '// Generated from packages/i18n; do not edit.\npackage tv.getportico.portico.generated\n\nimport tv.getportico.portico.R\n\ninternal object PorticoSystemStrings {\n  val byCode: Map<String, Int> = mapOf(\n' +
    rows.map(code => `    ${JSON.stringify(code)} to R.string.${resourceName(code)},`).join('\n') + '\n  )\n}\n';
  add('android', 'android/app/src/main/java/tv/getportico/portico/generated/PorticoSystemStrings.kt', kotlin);
  return outputs;
}

/** Extract quoted Swift source without treating comments as literals. Keep interpolation
 * escapes verbatim, since App Intents phrases use them as compiler-recognized parameters. */
function swiftWithoutComments(source) {
  return source.replace(/"(?:\\.|[^"\\])*"|\/\/[^\n]*|\/\*[\s\S]*?\*\//g, token => token.startsWith('"') ? token : token.replace(/[^\n]/g, ' '));
}
const quoted = source => [...source.matchAll(/"((?:\\.|[^"\\])*)"/g)].map(match => match[1]);
function validateAppleSources(nativeRoot, tables) {
  const source = tables.get('en-US');
  const swift = swiftWithoutComments(readFileSync(path.join(nativeRoot, 'ios/Portico/PorticoIntents.swift'), 'utf8'));
  const literals = quoted(swift);
  const titles = Object.keys(source).filter(key => key.startsWith('system.siri.') && !phraseKey(key)).map(key => source[key]);
  const phrases = Object.keys(source).filter(phraseKey).map(key => swiftPhrase(source[key]));
  for (const literal of [...titles, ...phrases]) if (!literals.includes(literal)) throw new Error(`PorticoIntents.swift is missing catalogue literal: ${literal}`);
  const allowed = new Set(titles);
  const contexts = /\b(?:title\s*:\s*LocalizedStringResource\s*=|title\s*:|subtitle\s*:|IntentDescription\s*\(|IntentDialog\s*\(|TypeDisplayRepresentation\s*=)\s*"((?:\\.|[^"\\])*)"/g;
  for (const match of swift.matchAll(contexts)) {
    // AppEntity's media title/subtitle are content. Permit only these existing
    // pure interpolations in DisplayRepresentation, never parameter/intent
    // copy, mixed fixed words, escaped interpolation or another constructor.
    const before = swift.slice(0, match.index);
    const contentTitle = match[1] === '\\(title)' && /\bDisplayRepresentation\s*\(\s*$/.test(before);
    const contentSubtitle = match[1] === '\\(subtitle)' && /\bDisplayRepresentation\s*\(\s*title\s*:\s*"\\\(title\)"\s*,\s*$/.test(before);
    if (!contentTitle && !contentSubtitle && !allowed.has(match[1])) throw new Error(`PorticoIntents.swift has uncatalogued literal: ${match[1]}`);
  }
  for (const block of swift.matchAll(/phrases\s*:\s*\[([\s\S]*?)\]/g)) for (const phrase of quoted(block[1])) if (!phrases.includes(phrase)) throw new Error(`PorticoIntents.swift has uncatalogued phrase: ${phrase}`);
  for (const platform of ['ios', 'tvos']) {
    const plist = readFileSync(path.join(nativeRoot, `${platform}/Portico/Info.plist`), 'utf8');
    const matches = [...plist.matchAll(/<key>NSLocalNetworkUsageDescription<\/key>\s*<string>([\s\S]*?)<\/string>/g)];
    if (matches.length !== 1 || matches[0][1] !== source[`system.apple.localNetwork.${platform}`]) throw new Error(`${platform} Info.plist Local Network text differs from the catalogue.`);
    const project = readFileSync(path.join(nativeRoot, `${platform}/Portico.xcodeproj/project.pbxproj`), 'utf8');
    const regions = /\bknownRegions\s*=\s*\(([\s\S]*?)\);/.exec(project)?.[1];
    const known = new Set((regions ?? '').split(',').map(value => value.trim().replace(/^"|"$/g, '')));
    for (const locale of tables.keys()) if (!known.has(appleLocale(locale))) throw new Error(`${platform} knownRegions is missing ${appleLocale(locale)}.`);
  }
}

/** Native standard-sync hook: run only AFTER the checked canonical packages are copied.
 * No network, SDK or package install is used. --check never writes platform resources. */
export function generateNativeStrings({nativeRoot = defaultNativeRoot, platform, check = false} = {}) {
  if (platform !== undefined && platform !== 'apple' && platform !== 'android') throw new Error('platform must be apple or android');
  const {tables, skipped} = nativeTables();
  validatePhrases(tables);
  validateAppleSources(nativeRoot, tables);
  const outputs = [...nativeOutputs(tables)].filter(([, output]) => !platform || output.platform === platform);
  const stale = [];
  for (const [relative, {value}] of outputs) {
    const file = path.join(nativeRoot, relative);
    let actual;
    try { actual = readFileSync(file, 'utf8'); } catch (error) { if (error.code !== 'ENOENT') throw error; }
    if (actual === value) continue;
    if (check) { stale.push(relative); continue; }
    mkdirSync(path.dirname(file), {recursive: true});
    const temporary = `${file}.tmp-${process.pid}`;
    try { writeFileSync(temporary, value); renameSync(temporary, file); } finally { rmSync(temporary, {force: true}); }
  }
  if (stale.length) throw new Error(`Stale native system strings:\n${stale.join('\n')}`);
  return {outputs: outputs.map(([file]) => file), locales: [...tables.keys()], skipped};
}
function main(args) {
  let platform, check = false;
  for (let i = 0; i < args.length; i++) {
    if (args[i] === '--check' && !check) check = true;
    else if (args[i] === '--platform' && !platform && ['apple', 'android'].includes(args[i + 1])) platform = args[++i];
    else throw new Error('Usage: native-strings.mjs [--platform apple|android] [--check]');
  }
  const result = generateNativeStrings({platform, check});
  console.log(`native-strings: ${result.outputs.length} outputs; ${result.locales.join(', ')}; ${check ? 'current' : 'written'}`);
  if (result.skipped.length) console.log(`native-strings: skipped non-native row titles: ${result.skipped.join(', ')}`);
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { main(process.argv.slice(2)); } catch (error) { console.error(`native-strings: ${error.message}`); process.exitCode = 1; }
}
