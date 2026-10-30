import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync, mkdtempSync, rmSync, writeFileSync} from 'node:fs';
import {execFileSync, spawnSync} from 'node:child_process';
import {tmpdir} from 'node:os';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {parseSprite, renderNative, renderShared} from './next-icons.mjs';
import {nextIcons, iconNames, nextIconSprite} from '../src/next-icons.ts';

const here = path.dirname(fileURLToPath(import.meta.url));
const svg = readFileSync(path.resolve(here, '../src/next-icons.svg'), 'utf8');
const icons = parseSprite(svg);
const document = body => `<svg><defs>${body}</defs></svg>`;
const symbol = '<symbol id="i-test" viewBox="0 0 24 24"><path fill="currentColor" d="M0 0h24v24H0z" /></symbol>';

test('every canonical symbol has the same faithful ID and geometry in both outputs', () => {
  const web = renderShared(icons), native = renderNative(icons);
  assert.equal(icons.length, 261);
  assert.equal(new Set(icons.map(i => i.attrs.id)).size, icons.length);
  assert.equal((native.match(/case "/g) || []).length, icons.length);
  for (const icon of icons) {
    assert.ok(web.includes(JSON.stringify(icon.attrs.id) + ': {viewBox: ' + JSON.stringify(icon.attrs.viewBox)));
    assert.ok(native.includes('case ' + JSON.stringify(icon.attrs.id) + ':'));
    const visit = node => {
      for (const [key, value] of Object.entries(node.attrs)) {
        if (key === 'id' || key === 'viewBox') continue;
        assert.ok(web.includes(value), `${icon.attrs.id}: shared ${key}`);
        assert.ok(native.includes(value), `${icon.attrs.id}: native ${key}`);
      }
      node.children.forEach(visit);
    };
    visit(icon);
  }
  assert.equal(icons.find(i => i.attrs.id === 'i-wifi').attrs.viewBox, '0 0 16 12');
  assert.equal(icons.find(i => i.attrs.id === 'i-bat').attrs.viewBox, '0 0 27 13');
  assert.equal(icons.find(i => i.attrs.id === 'i-sig').attrs.viewBox, '0 0 18 12');
});

test('reading navigation headphones preserve the exact source geometry and the existing headphones glyph', () => {
  // tablet-read.html and foldable.html at design/ui-v1 33519933ca5afdd035e14060b881c830defb090c.
  const source = '<symbol id="i-headphones" viewBox="0 0 24 24"><g fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 15v-3a8 8 0 0 1 16 0v3"/><rect x="3.5" y="14" width="4.5" height="6.5" rx="1.6"/><rect x="16" y="14" width="4.5" height="6.5" rx="1.6"/></g></symbol>';
  assert.deepEqual(icons.find(icon => icon.attrs.id === 'i-headphones'), parseSprite(document(source))[0]);
  const body = '<g fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 15v-3a8 8 0 0 1 16 0v3"></path><rect x="3.5" y="14" width="4.5" height="6.5" rx="1.6"></rect><rect x="16" y="14" width="4.5" height="6.5" rx="1.6"></rect></g>';
  assert.deepEqual(nextIcons['i-headphones'], {viewBox: '0 0 24 24', body});
  assert.ok(renderNative(icons).includes('case "i-headphones": return <G><G fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><Path d="M4 15v-3a8 8 0 0 1 16 0v3" /><Rect x="3.5" y="14" width="4.5" height="6.5" rx="1.6" /><Rect x="16" y="14" width="4.5" height="6.5" rx="1.6" /></G></G>;'));
  const original = icons.find(icon => icon.attrs.id === 'i-headph');
  assert.deepEqual(original.children[0].children.slice(1).map(rect => rect.attrs), [
    {height: '7', rx: '1.5', width: '5', x: '3', y: '14'},
    {height: '7', rx: '1.5', width: '5', x: '16', y: '14'},
  ]);
});

test('activity Levels preserves the approved ring and three bars', () => {
  const activity = icons.find(i => i.attrs.id === 'i-act');
  assert.equal(activity.children[0].children[0].tag, 'circle');
  assert.equal(activity.children[0].children[0].attrs.r, '9');
  assert.equal(activity.children[0].children[1].attrs.d, 'M8.25 16v-3.5M12 16V8M15.75 16v-5.5');
});

test('selection queue choices preserve the exact next and later source geometry', () => {
  const next = icons.find(i => i.attrs.id === 'i-pnext');
  assert.equal(next.attrs.viewBox, '0 0 24 24');
  assert.equal(next.children[0].children[0].attrs.d, 'M4 6.5h8M4 12h5M4 17.5h8');
  assert.equal(next.children[1].attrs.d, 'M14.5 8.2v7.6c0 .5.5.8.9.5l5-3.8a.6.6 0 0 0 0-1l-5-3.8c-.4-.3-.9 0-.9.5z');
  const later = icons.find(i => i.attrs.id === 'i-plater');
  assert.equal(later.attrs.viewBox, '0 0 24 24');
  assert.equal(later.children[0].children[0].attrs.d, 'M4 6.5h16M4 11.5h16M4 16.5h8M17 14v6M14 17h6');
});

test('selection Keep only registers the exact drawn album in the shared and native outputs', () => {
  // select.html:1660 at design/ui-v1 8377791ae573e17b0fc5199ae87f7ad3feee0f16.
  const album = icons.find(i => i.attrs.id === 'i-album');
  assert.deepEqual(album, {
    tag: 'symbol', attrs: {id: 'i-album', viewBox: '0 0 24 24'}, children: [{
      tag: 'g', attrs: {fill: 'none', stroke: 'currentColor', 'stroke-width': '2'}, children: [
        {tag: 'rect', attrs: {x: '4', y: '4', width: '16', height: '16', rx: '3'}, children: []},
        {tag: 'circle', attrs: {cx: '12', cy: '12', r: '3.6'}, children: []},
      ],
    }],
  });
  const body = '<g fill="none" stroke="currentColor" stroke-width="2"><rect x="4" y="4" width="16" height="16" rx="3"></rect><circle cx="12" cy="12" r="3.6"></circle></g>';
  assert.deepEqual(nextIcons['i-album'], {viewBox: '0 0 24 24', body});
  assert.equal(iconNames.filter(name => name === 'i-album').length, 1);
  assert.ok(nextIconSprite.includes(`<symbol id="i-album" viewBox="0 0 24 24">${body}</symbol>`));
  assert.ok(renderNative(icons).includes('case "i-album": return <G><G fill="none" stroke="currentColor" strokeWidth="2"><Rect x="4" y="4" width="16" height="16" rx="3" /><Circle cx="12" cy="12" r="3.6" /></G></G>;'));
});

test('context variants preserve pinned selection and guide geometry without replacing global icons', () => {
  // design/ui-v1 8377791ae573e17b0fc5199ae87f7ad3feee0f16:
  // select.html:1657 i-eyeoff; phone-guide.html:375 i-list (List / Guide control).
  // Context IDs are additive: only the ID differs from each literal source symbol.
  const variants = [
    {
      id: 'i-eyeoff-selection', original: 'i-eyeoff',
      source: '<symbol id="i-eyeoff" viewBox="0 0 24 24"><g fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12s3.2-6 9-6c1.6 0 3 .5 4.2 1.2M21 12s-3.2 6-9 6c-1.6 0-3-.5-4.2-1.2"/><circle cx="12" cy="12" r="2.6"/><path d="m4 4 16 16"/></g></symbol>',
      body: '<g fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12s3.2-6 9-6c1.6 0 3 .5 4.2 1.2M21 12s-3.2 6-9 6c-1.6 0-3-.5-4.2-1.2"></path><circle cx="12" cy="12" r="2.6"></circle><path d="m4 4 16 16"></path></g>',
      native: 'case "i-eyeoff-selection": return <G><G fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><Path d="M3 12s3.2-6 9-6c1.6 0 3 .5 4.2 1.2M21 12s-3.2 6-9 6c-1.6 0-3-.5-4.2-1.2" /><Circle cx="12" cy="12" r="2.6" /><Path d="m4 4 16 16" /></G></G>;',
    },
    {
      id: 'i-list-guide', original: 'i-list',
      source: '<symbol id="i-list" viewBox="0 0 24 24"><path fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" d="M9 6.5h11M9 12h11M9 17.5h11M4.5 6.5h.1M4.5 12h.1M4.5 17.5h.1"/></symbol>',
      body: '<path fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" d="M9 6.5h11M9 12h11M9 17.5h11M4.5 6.5h.1M4.5 12h.1M4.5 17.5h.1"></path>',
      native: 'case "i-list-guide": return <G><Path fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" d="M9 6.5h11M9 12h11M9 17.5h11M4.5 6.5h.1M4.5 12h.1M4.5 17.5h.1" /></G>;',
    },
  ];
  const native = renderNative(icons);
  for (const {id, original, source, body, native: glyph} of variants) {
    const literal = source.replace(`id="${original}"`, `id="${id}"`);
    assert.ok(svg.includes(literal), `${id}: exact source bytes with only ID changed`);
    const expected = parseSprite(document(literal))[0];
    assert.deepEqual(icons.find(icon => icon.attrs.id === id), expected);
    assert.deepEqual(nextIcons[id], {viewBox: '0 0 24 24', body});
    assert.equal(iconNames.filter(name => name === id).length, 1);
    assert.ok(nextIconSprite.includes(`<symbol id="${id}" viewBox="0 0 24 24">${body}</symbol>`));
    assert.ok(native.includes(glyph), `${id}: complete native geometry and stroke inheritance`);
    assert.notDeepEqual(nextIcons[id], nextIcons[original], `${original} remains a distinct global glyph`);
  }
  assert.equal(nextIcons['i-eyeoff'].body, '<g fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round" stroke-width="2"><path d="M3.5 3.5l17 17M10.4 6.1A10 10 0 0 1 12 6c6.5 0 10 6 10 6a17 17 0 0 1-2.7 3.4M6.7 6.8C3.8 8.5 2 12 2 12s3.5 6 10 6c1.7 0 3.2-.4 4.5-1M9.9 9.9a3 3 0 0 0 4.2 4.2"></path></g>');
  assert.equal(nextIcons['i-list'].body, '<g fill="none" stroke="currentColor" stroke-linecap="round" stroke-width="2"><path d="M9.5 6.5H20M9.5 12H20M9.5 17.5H20"></path><path d="M4.5 6.5h.5M4.5 12h.5M4.5 17.5h.5" stroke-width="2.6"></path></g>');
});

test('phone File info registers its exact server glyph without changing the global server', () => {
  // design/ui-v1 5cbcc03681b22a4593b3e7c5c6878ae74ace22d1 phone-detail.html:732.
  // Only the source symbol ID changes; desktop-detail.html:384 remains the global glyph.
  const id = 'i-server-file-info-phone';
  const source = '<symbol id="i-server" viewBox="0 0 24 24"><g fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="4" y="4.5" width="16" height="6.5" rx="1.8"/><rect x="4" y="13" width="16" height="6.5" rx="1.8"/><path d="M8 7.8h.1M8 16.3h.1"/></g></symbol>';
  const literal = source.replace('id="i-server"', `id="${id}"`);
  assert.ok(svg.includes(literal), 'exact phone source bytes with only the ID changed');
  assert.deepEqual(icons.find(icon => icon.attrs.id === id), parseSprite(document(literal))[0]);
  const body = '<g fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="4" y="4.5" width="16" height="6.5" rx="1.8"></rect><rect x="4" y="13" width="16" height="6.5" rx="1.8"></rect><path d="M8 7.8h.1M8 16.3h.1"></path></g>';
  assert.deepEqual(nextIcons[id], {viewBox: '0 0 24 24', body});
  assert.equal(iconNames.filter(name => name === id).length, 1);
  assert.ok(nextIconSprite.includes(`<symbol id="${id}" viewBox="0 0 24 24">${body}</symbol>`));
  assert.ok(renderNative(icons).includes('case "i-server-file-info-phone": return <G><G fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><Rect x="4" y="4.5" width="16" height="6.5" rx="1.8" /><Rect x="4" y="13" width="16" height="6.5" rx="1.8" /><Path d="M8 7.8h.1M8 16.3h.1" /></G></G>;'));
  assert.equal(nextIcons['i-server'].body, '<g fill="none" stroke="currentColor" stroke-linecap="round" stroke-width="2"><rect height="7" rx="2" width="16" x="4" y="4"></rect><rect height="7" rx="2" width="16" x="4" y="13"></rect><path d="M8 7.5h.01M8 16.5h.01M12 7.5h4M12 16.5h4"></path></g>');
  assert.notDeepEqual(nextIcons[id], nextIcons['i-server']);
});

test('Activity registers the exact chosen static Levels glyph without changing i-act', () => {
  // design/ui-v1 5cbcc03681b22a4593b3e7c5c6878ae74ace22d1 desktop-activity.html:198.
  // Chosen A / Levels:258-260; idle:278 and quiet:309 use this same static symbol.
  const id = 'i-activity';
  const source = '<symbol id="i-act" viewBox="0 0 24 24"><g fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="9"/><path d="M8.25 16v-3.5M12 16V8M15.75 16v-5.5"/></g></symbol>';
  const literal = source.replace('id="i-act"', `id="${id}"`);
  assert.ok(svg.includes(literal), 'exact chosen Levels source bytes with only the ID changed');
  const expected = parseSprite(document(literal))[0];
  assert.deepEqual(icons.find(icon => icon.attrs.id === id), expected);
  assert.deepEqual({...expected, attrs: {...expected.attrs, id: 'i-act'}}, icons.find(icon => icon.attrs.id === 'i-act'));
  const body = '<g fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="9"></circle><path d="M8.25 16v-3.5M12 16V8M15.75 16v-5.5"></path></g>';
  assert.deepEqual(nextIcons[id], {viewBox: '0 0 24 24', body});
  assert.equal(iconNames.filter(name => name === id).length, 1);
  assert.ok(nextIconSprite.includes(`<symbol id="${id}" viewBox="0 0 24 24">${body}</symbol>`));
  assert.ok(renderNative(icons).includes('case "i-activity": return <G><G fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><Circle cx="12" cy="12" r="9" /><Path d="M8.25 16v-3.5M12 16V8M15.75 16v-5.5" /></G></G>;'));
  assert.equal(nextIcons['i-act'].body, '<g fill="none" stroke="currentColor" stroke-linecap="round" stroke-width="2"><circle cx="12" cy="12" r="9"></circle><path d="M8.25 16v-3.5M12 16V8M15.75 16v-5.5"></path></g>');
});

test('unsupported SVG features, unsafe attributes and malformed geometry fail closed', () => {
  for (const invalid of [
    symbol + symbol,
    symbol.replace('<path ', '<script '),
    symbol.replace('fill="currentColor"', 'onclick="alert(1)"'),
    symbol.replace('fill="currentColor"', 'class="ic"'),
    symbol.replace('24 24', '0 24'),
    symbol.replace('24 24', 'NaN 24'),
    symbol.replace('fill="currentColor"', 'fill="currentColor" fill="none"'),
    symbol.replace('</symbol>', ''),
    symbol.replace('<path ', 'text<path '),
  ]) assert.throws(() => parseSprite(document(invalid)));
  assert.throws(() => parseSprite('<svg><defs /></svg><svg><defs /></svg>'));
  assert.throws(() => parseSprite(document('')));
});

test('generation is deterministic and check detects stale or missing native output', () => {
  assert.equal(renderShared(parseSprite(svg)), renderShared(parseSprite(svg)));
  assert.equal(renderNative(parseSprite(svg)), renderNative(parseSprite(svg)));
  const temp = mkdtempSync(path.join(tmpdir(), 'portico-icons-'));
  const target = path.join(temp, 'IconGlyphs.tsx');
  const run = extra => spawnSync(process.execPath, [path.join(here, 'next-icons.mjs'), '--check', '--native', target, ...extra], {encoding: 'utf8'});
  try {
    assert.notEqual(run([]).status, 0);
    assert.match(run([]).stderr, /Missing icon output/);
    writeFileSync(target, renderNative(icons));
    assert.equal(run([]).status, 0);
    writeFileSync(target, renderNative(icons) + '\n');
    assert.notEqual(run([]).status, 0);
    assert.match(run([]).stderr, /Stale icon output/);
    assert.equal(execFileSync(process.execPath, [path.join(here, 'next-icons.mjs'), '--check'], {encoding: 'utf8'}).trim(), 'next-icons: 261 symbols; 1 output(s) verified');
  } finally { rmSync(temp, {recursive: true, force: true}); }
});
