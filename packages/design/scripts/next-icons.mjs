#!/usr/bin/env node
import {readFileSync, writeFileSync, mkdirSync} from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const source = path.resolve(here, '../src/next-icons.svg');
const shared = path.resolve(here, '../src/next-icons.ts');
const tags = {g: 'G', path: 'Path', circle: 'Circle', rect: 'Rect', ellipse: 'Ellipse'};
const attributes = new Set(['d', 'fill', 'stroke', 'stroke-width', 'stroke-linecap', 'stroke-linejoin', 'opacity', 'x', 'y', 'width', 'height', 'cx', 'cy', 'r', 'rx', 'ry']);
const legal = '// Lucide-derived artwork: ISC; Feather-derived artwork: MIT. See packages/design/LICENSE-LUCIDE.\n';

// Only the static geometry subset used by this sprite is accepted. Adding another SVG
// feature requires implementing it for both renderers rather than silently dropping it.
export function parseSprite(svg) {
  const clean = svg.replace(/<!--[\s\S]*?-->/g, '');
  const root = {tag: 'root', attrs: {}, children: []};
  const stack = [root];
  let end = 0;
  for (const token of clean.matchAll(/<([^<>]+)>/g)) {
    if (clean.slice(end, token.index).trim()) throw new Error('Unexpected SVG text');
    end = token.index + token[0].length;
    const text = token[1];
    if (text.startsWith('/')) {
      if (stack.length === 1 || stack.pop().tag !== text.slice(1).trim()) throw new Error('Mismatched SVG closing tag');
      continue;
    }
    const match = /^([a-z]+)\b([\s\S]*?)(\/?)$/.exec(text);
    if (!match) throw new Error('Invalid SVG tag');
    const [, tag, raw, selfClosing] = match;
    if (!['svg', 'defs', 'symbol', ...Object.keys(tags)].includes(tag)) throw new Error(`Unsupported SVG tag: ${tag}`);
    const attrs = {};
    let attrEnd = 0;
    for (const attr of raw.matchAll(/([A-Za-z][\w:-]*)\s*=\s*(["'])(.*?)\2/g)) {
      if (raw.slice(attrEnd, attr.index).trim()) throw new Error('Invalid SVG attribute');
      attrEnd = attr.index + attr[0].length;
      const key = attr[1], value = attr[3];
      const allowed = tag === 'svg' ? key === 'xmlns' : tag === 'symbol' ? ['id', 'viewBox'].includes(key) : tag !== 'defs' && attributes.has(key);
      if (!allowed || Object.hasOwn(attrs, key) || /[<>&{}]/.test(value)) throw new Error(`Unsupported SVG attribute: ${key}`);
      attrs[key] = value;
    }
    if (raw.slice(attrEnd).trim()) throw new Error('Invalid SVG attributes');
    const parent = stack.at(-1);
    const validParent = tag === 'svg' ? parent.tag === 'root' : tag === 'defs' ? parent.tag === 'svg' : tag === 'symbol' ? parent.tag === 'defs' : ['symbol', 'g'].includes(parent.tag);
    if (!validParent) throw new Error(`Invalid SVG nesting: ${tag}`);
    const node = {tag, attrs, children: []};
    parent.children.push(node);
    if (!selfClosing) stack.push(node);
  }
  if (clean.slice(end).trim() || stack.length !== 1 || root.children.length !== 1) throw new Error('Incomplete SVG document');
  const svgRoot = root.children[0];
  if (svgRoot.tag !== 'svg' || svgRoot.children.length !== 1 || svgRoot.children[0].tag !== 'defs') throw new Error('Expected one SVG defs block');
  const icons = svgRoot.children[0].children;
  const ids = new Set();
  for (const icon of icons) {
    const {id, viewBox} = icon.attrs;
    if (icon.tag !== 'symbol' || !/^[a-z][a-z0-9-]*$/.test(id ?? '') || ids.has(id)) throw new Error(`Invalid or duplicate symbol ID: ${id}`);
    const box = viewBox?.split(/\s+/).map(Number);
    if (!box || box.length !== 4 || box.some(n => !Number.isFinite(n)) || box[2] <= 0 || box[3] <= 0 || !icon.children.length) throw new Error(`Invalid symbol geometry: ${id}`);
    ids.add(id);
  }
  if (!icons.length) throw new Error('Empty icon sprite');
  return icons.sort((a, b) => a.attrs.id < b.attrs.id ? -1 : a.attrs.id > b.attrs.id ? 1 : 0);
}

const xml = node => `<${node.tag}${Object.entries(node.attrs).map(([key, value]) => ` ${key}="${value}"`).join('')}>${node.children.map(xml).join('')}</${node.tag}>`;
export function renderShared(icons) {
  return legal + '// Rebuild from next-icons.svg with node packages/design/scripts/next-icons.mjs.\n' +
    'export const nextIcons = {\n' + icons.map(icon => `  ${JSON.stringify(icon.attrs.id)}: {viewBox: ${JSON.stringify(icon.attrs.viewBox)}, body: ${JSON.stringify(icon.children.map(xml).join(''))}},`).join('\n') +
    '\n} as const;\n\nexport type IconName = keyof typeof nextIcons;\n' +
    'export const iconNames = Object.freeze(Object.keys(nextIcons) as IconName[]);\n' +
    'export const nextIconSprite = iconNames.map(id => `<symbol id="${id}" viewBox="${nextIcons[id].viewBox}">${nextIcons[id].body}</symbol>`).join(\'\');\n';
}

const camel = key => key.replace(/-([a-z])/g, (_, letter) => letter.toUpperCase());
const jsx = node => `<${tags[node.tag]}${Object.entries(node.attrs).map(([key, value]) => ` ${camel(key)}=${JSON.stringify(value)}`).join('')}${node.children.length ? `>${node.children.map(jsx).join('')}</${tags[node.tag]}>` : ' />'}`;
export function renderNative(icons) {
  return legal + '// Rebuild from next-icons.svg with next-icons.mjs --native <this file>.\n' +
    "import React from 'react';\nimport {G, Path, Circle, Rect, Ellipse} from 'react-native-svg';\n" +
    "import type {IconName} from '../../../../packages/design/src/next-icons';\n\n" +
    'export function IconGlyph({name}: {name: IconName}) {\n  switch (name) {\n' +
    icons.map(icon => `    case ${JSON.stringify(icon.attrs.id)}: return <G>${icon.children.map(jsx).join('')}</G>;`).join('\n') +
    '\n  }\n}\n';
}

function main() {
  const args = process.argv.slice(2);
  const check = args.includes('--check');
  const nativeIndex = args.indexOf('--native');
  const native = nativeIndex >= 0 ? args[nativeIndex + 1] : null;
  const expected = [...(check ? ['--check'] : []), ...(native ? ['--native', native] : [])];
  if (args.length !== expected.length || args.some(arg => !expected.includes(arg))) throw new Error('Usage: next-icons.mjs [--check] [--native path/to/IconGlyphs.tsx]');
  const icons = parseSprite(readFileSync(source, 'utf8'));
  const outputs = [[shared, renderShared(icons)], ...(native ? [[path.resolve(native), renderNative(icons)]] : [])];
  for (const [file, content] of outputs) {
    if (check) {
      let actual;
      try { actual = readFileSync(file, 'utf8'); } catch { throw new Error(`Missing icon output: ${file}`); }
      if (actual !== content) throw new Error(`Stale icon output: ${file}`);
    } else {
      mkdirSync(path.dirname(file), {recursive: true});
      writeFileSync(file, content);
    }
  }
  console.log(`next-icons: ${icons.length} symbols; ${outputs.length} output(s) ${check ? 'verified' : 'written'}`);
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
