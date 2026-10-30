import test from 'node:test';
import assert from 'node:assert/strict';
import {observeRenderedFonts} from './resolved-fonts.mjs';

const fixture = () => ({measurement: {renderer: 'dom', coordinateUnit: 'css-px'}, elements: [
  {id: '0.1', ownText: 'Public text', style: {fontFamily: 'Requested, serif'}},
  {id: '0.2', ownText: '', input: {redacted: true}, style: {fontFamily: 'Requested, serif'}},
]});
const observer = fonts => async (method, params) => {
  if (method === 'DOM.getDocument') return {root: {nodeId: 1}};
  if (method === 'DOM.querySelector') return {nodeId: params.selector === '#screen' ? 2 : 3};
  if (method === 'CSS.getPlatformFontsForNode') return {fonts};
  assert.ok(['DOM.enable', 'CSS.enable'].includes(method)); return {};
};
test('active font identity comes only from observed glyph families, preserving requested/raw facts', async () => {
  const input = fixture(), raw = JSON.stringify(input);
  const output = await observeRenderedFonts(observer([{familyName: 'Observed face', glyphCount: 3, isCustomFont: false, postScriptName: '/private/font-file'}]), input, '#screen');
  assert.equal(output.elements[0].style.resolvedFontFamily, 'Observed face');
  assert.equal(output.elements[0].style.fontFamily, 'Requested, serif');
  assert.equal(JSON.stringify(input), raw); assert.ok(!JSON.stringify(output).includes('/private/'));
  assert.equal(output.elements[1].style.resolvedFontFamily, undefined);
  assert.equal(output.unavailable.length, 1); assert.equal(output.unavailable[0].element, '0.2');
});

test('custom legacy family names require actual resource metadata and never style-word stripping', async () => {
  const active = observer([{familyName: 'Example ExtraLight', glyphCount: 4, isCustomFont: true}]);
  const known = await observeRenderedFonts(active, fixture(), '#screen', async () => ({complete: true, families: [{family: 'Example', nameId: 16, platformFamilies: ['Example ExtraLight']}]}));
  assert.equal(known.elements[0].style.resolvedFontFamily, 'Example');
  assert.equal(known.elements[0].style.platformFontFamily, 'Example ExtraLight');
  const unknown = await observeRenderedFonts(active, fixture(), '#screen');
  assert.equal(unknown.elements[0].style.resolvedFontFamily, undefined);
  const ambiguous = await observeRenderedFonts(active, fixture(), '#screen', async () => ({complete: true, families: [
    {family: 'One', nameId: 16, platformFamilies: ['Example ExtraLight']}, {family: 'Two', nameId: 16, platformFamilies: ['Example ExtraLight']},
  ]}));
  assert.equal(ambiguous.elements[0].style.resolvedFontFamily, undefined);
});

test('legacy-only metadata needs a unique observed active font registration, not a requested chain', async () => {
  const active = observer([{familyName: 'Example ExtraLight', glyphCount: 4, isCustomFont: true}]);
  const metadata = {complete: true, families: [{family: 'Example ExtraLight', nameId: 1, platformFamilies: ['Example ExtraLight']}],
    registrations: [{platformFamily: 'Example ExtraLight', family: 'Registered face'}]};
  const output = await observeRenderedFonts(active, fixture(), '#screen', async () => metadata);
  assert.equal(output.elements[0].style.resolvedFontFamily, 'Registered face');
  const missing = await observeRenderedFonts(active, fixture(), '#screen', async () => ({...metadata, registrations: []}));
  assert.equal(missing.elements[0].style.resolvedFontFamily, undefined);
  const duplicate = await observeRenderedFonts(active, fixture(), '#screen', async () => ({...metadata,
    registrations: [...metadata.registrations, {platformFamily: 'Example ExtraLight', family: 'Other alias'}]}));
  assert.equal(duplicate.elements[0].style.resolvedFontFamily, undefined);
  const quoted = await observeRenderedFonts(active, fixture(), '#screen', async () => ({...metadata,
    registrations: [{platformFamily: 'Example ExtraLight', family: '"Registered face"'}]}));
  assert.equal(quoted.elements[0].style.resolvedFontFamily, 'Registered face');
  const unsafe = await observeRenderedFonts(active, fixture(), '#screen', async () => ({...metadata,
    registrations: [...metadata.registrations, {platformFamily: 'Example ExtraLight', family: null}]}));
  assert.equal(unsafe.elements[0].style.resolvedFontFamily, undefined);
});
test('mixed, empty, unsafe or failed observations never become the requested family', async () => {
  for (const fonts of [[], [{familyName: 'A', glyphCount: 1}, {familyName: 'B', glyphCount: 1}], [{familyName: '/private/font', glyphCount: 1}], [{familyName: 'A', glyphCount: 0}]]) {
    const output = await observeRenderedFonts(observer(fonts), fixture(), '#screen');
    assert.equal(output.elements[0].style.resolvedFontFamily, undefined);
    assert.ok(output.unavailable.some(fact => fact.element === '0.1' && fact.property === 'resolvedFontFamily'));
  }
  const stale = fixture(); stale.elements[0].style.resolvedFontFamily = 'Old face';
  const failed = await observeRenderedFonts(async () => {throw new Error('Unavailable');}, stale, '#screen');
  assert.equal(failed.elements[0].style.resolvedFontFamily, undefined);
});

test('an input-containing label is never recursively probed for glyph fonts', async () => {
  const input = fixture(); input.elements[1].parent = input.elements[0].id;
  let probes = 0;
  const output = await observeRenderedFonts(async (method, params) => {
    if (method === 'CSS.getPlatformFontsForNode') probes++;
    return observer([{familyName: 'Face', glyphCount: 1}])(method, params);
  }, input, '#screen');
  assert.equal(probes, 0); assert.equal(output.elements[0].style.resolvedFontFamily, undefined);
  assert.equal(output.unavailable.length, 2);
});
