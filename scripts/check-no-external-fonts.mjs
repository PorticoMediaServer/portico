#!/usr/bin/env node
// Privacy guard: nothing Portico ships may make a viewer's browser contact a font
// service or a public CDN. Scans the built web bundle (also what Hosted web serves)
// and the Cast receiver, and checks that the shipped CSPs keep fonts local.
// The only allowed outside origin is Google's Cast SDK, which Cast requires.
//
// Usage: node scripts/check-no-external-fonts.mjs --dist <built web/dist>
import {readFileSync, readdirSync, statSync, existsSync} from 'node:fs';
import {join, relative, extname, dirname} from 'node:path';
import {fileURLToPath} from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');

export const CAST_SDK = ['https://www.gstatic.com/cast/sdk/', 'https://www.gstatic.com/cv/js/sender/'];
const FORBIDDEN_HOSTS = /\b(?:fonts\.googleapis\.com|fonts\.gstatic\.com|use\.typekit\.net|p\.typekit\.net|typekit\.com|cdn\.jsdelivr\.net|fastly\.jsdelivr\.net|unpkg\.com|cdnjs\.cloudflare\.com|cdnjs\.com)\b/i;
const EXTERNAL = String.raw`["']?\s*(?:https?:)?\/\/`;
const IMPORT_EXTERNAL = new RegExp(String.raw`@import\s*(?:url\(\s*)?` + EXTERNAL, 'i');
const URL_EXTERNAL = new RegExp(String.raw`url\(\s*` + EXTERNAL, 'i');
const FONT_FACE = /@font-face\s*\{[^}]*\}/gi;
const HTML_REFERENCE = /<(?:script|link)\b[^>]*?\b(?:src|href)\s*=\s*["']?((?:https?:)?\/\/[^"'\s>]+)/gi;
const ALLOWED_FONT_SOURCES = new Set(["'self'", 'blob:', 'data:']);

const castSdk = (url) => CAST_SDK.some((prefix) => url.startsWith(prefix));

/** Problems in one shipped file; `name` decides which rules apply. */
export function scanText(name, text) {
  const problems = [], ext = extname(name).toLowerCase();
  const host = text.match(FORBIDDEN_HOSTS);
  if (host) problems.push(`references ${host[0]}`);
  if (IMPORT_EXTERNAL.test(text)) problems.push('@import from another origin');
  for (const face of text.match(FONT_FACE) ?? []) {
    if (URL_EXTERNAL.test(face)) { problems.push('@font-face loads from another origin'); break; }
  }
  if (ext === '.css' && URL_EXTERNAL.test(text)) problems.push('url() from another origin');
  if (ext === '.html') {
    for (const [, url] of text.matchAll(HTML_REFERENCE)) {
      if (!castSdk(url)) problems.push(`loads ${url}`);
    }
  }
  return [...new Set(problems)];
}

/** The font-src a browser enforces: font-src, else default-src. */
export function effectiveFontSources(csp) {
  const directives = new Map(csp.split(';').map((part) => part.trim().split(/\s+/)).filter((words) => words[0]).map(([name, ...values]) => [name.toLowerCase(), values]));
  return directives.get('font-src') ?? directives.get('default-src') ?? ['*'];
}

export function cspProblems(label, csp) {
  const bad = effectiveFontSources(csp).filter((source) => !ALLOWED_FONT_SOURCES.has(source));
  return bad.length ? [`${label}: font sources must stay 'self' (plus blob:/data:), found ${bad.join(' ')}`] : [];
}

function* files(dir) {
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) yield* files(path);
    else if (/\.(?:html?|css|m?js)$/i.test(entry)) yield path;
  }
}

function goConstant(file, name) {
  const match = readFileSync(join(root, file), 'utf8').match(new RegExp(`const ${name} = "((?:[^"\\\\]|\\\\.)*)"`));
  if (!match) throw new Error(`${file}: ${name} not found; update this guard with the CSP's new home`);
  return match[1];
}

// Each rule must still catch what it exists for, so a loosened pattern cannot pass silently.
function selfTest() {
  const caught = [
    ['a.css', "@import url('https://fonts.googleapis.com/css2?family=Inter');"],
    ['a.css', '@import "//example.com/x.css";'],
    ['a.css', "@font-face{font-family:X;src:url(https://example.com/x.woff2)}"],
    ['a.css', '.x{background:url("//example.com/a.png")}'],
    ['a.js', 'const s=`@font-face{font-family:X;src:url("https://cdn.example/x.woff2")}`'],
    ['a.js', 'fetch("https://cdn.jsdelivr.net/npm/x")'],
    ['a.js', 'load("https://unpkg.com/x")'],
    ['a.js', 'load("https://use.typekit.net/abc.css")'],
    ['a.js', 'load("https://cdnjs.cloudflare.com/ajax/libs/x.js")'],
    ['a.html', '<link rel="stylesheet" href="https://example.com/x.css">'],
    ['a.html', '<script src="https://www.gstatic.com/other.js"></script>'],
  ];
  const clean = [
    ['a.css', "@font-face{font-family:Manrope;src:url('assets/manrope.woff2') format('woff2')}"],
    ['a.css', '@import "./local.css";'],
    ['a.js', 'const sender="https://www.gstatic.com/cv/js/sender/v1/cast_sender.js"'],
    ['a.js', String.raw`/@import\s+(?:url\(\s*(?:"((?:\\.|[^"\\])*)")/`],
    ['a.html', '<script src="https://www.gstatic.com/cast/sdk/libs/caf_receiver/v3/cast_receiver_framework.js"></script>'],
    ['a.html', '<link rel="stylesheet" href="/assets/index.css">'],
  ];
  const failures = [
    ...caught.filter(([name, text]) => scanText(name, text).length === 0).map(([, text]) => `missed: ${text}`),
    ...clean.filter(([name, text]) => scanText(name, text).length > 0).map(([, text]) => `false alarm: ${text}`),
  ];
  if (cspProblems('t', "default-src 'self'").length || cspProblems('t', "default-src 'self'; font-src 'self' blob: data:").length) failures.push('CSP: rejected an allowed policy');
  if (!cspProblems('t', "default-src 'self'; font-src 'self' https://fonts.gstatic.com").length || !cspProblems('t', "default-src *").length || !cspProblems('t', "img-src 'self'").length) failures.push('CSP: accepted an outside font source');
  if (failures.length) {
    console.error(`check-no-external-fonts self-test failed:\n- ${failures.join('\n- ')}`);
    process.exit(1);
  }
}

function main() {
  selfTest();
  const at = process.argv.indexOf('--dist');
  const dist = at > 0 ? process.argv[at + 1] : '';
  if (!dist || !existsSync(join(dist, 'index.html'))) {
    console.error('Usage: check-no-external-fonts.mjs --dist <built web/dist containing index.html>');
    process.exit(2);
  }
  const problems = [];
  let scanned = 0;
  for (const [label, dir] of [['web', dist], ['cast receiver', join(root, 'server/receiver/cast')]]) {
    for (const path of files(dir)) {
      if (path.includes(`${join('receiver', 'cast', 'test')}`)) continue;
      scanned++;
      for (const problem of scanText(path, readFileSync(path, 'utf8'))) problems.push(`${label} ${relative(dir, path)}: ${problem}`);
    }
  }
  problems.push(...cspProblems('web CSP (installation.go)', goConstant('server/internal/httpapi/installation.go', 'webContentSecurityPolicy')));
  problems.push(...cspProblems('Cast receiver CSP (social_routes.go)', goConstant('server/internal/httpapi/social_routes.go', 'castReceiverCSP')));
  if (scanned === 0) problems.push('no shipped files were scanned');
  if (problems.length) {
    console.error(`External font or CDN references found (only the Cast SDK may load from outside):\n- ${problems.join('\n- ')}`);
    process.exit(1);
  }
  console.log(`No external fonts or CDNs: ${scanned} shipped files and 2 CSPs checked.`);
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) main();
