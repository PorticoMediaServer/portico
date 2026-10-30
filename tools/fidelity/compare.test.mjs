import test from 'node:test';
import assert from 'node:assert/strict';
import {compareScreens} from './compare.mjs';

const node = (frame, patch = {}) => ({id: frame, frame, tag: 'h1', ownText: 'Public heading', box: {x: 10, y: 20, width: 100, height: 40}, style: {fontFamily: 'Example', fontSize: '32px', fontWeight: '800', opacity: '.7', color: 'rgb(46, 63, 73)'}, ...patch});
const spec = elements => ({scope: 'screen', fullViewport: true, root: {width: 200, height: 100}, viewport: {deviceScaleFactor: 1}, anchorIssues: [], elements});

test('canonical canvas pairing retains an unknown native root and cannot consume the anchored backdrop', () => {
  const root = node(null, {id: '0', parent: null, tag: 'div', leaf: false, ownText: '', normalizedText: '',
    box: {x: 0, y: 0, width: 200, height: 100}, style: {backgroundColor: 'rgb(7, 16, 24)'}});
  const backdrop = node('backdrop', {id: '0.0', parent: '0', tag: 'div', ownText: '', normalizedText: '', box: {x: 0, y: 0, width: 200, height: 70}});
  const actualRoot = {...root, id: 'native', style: {}};
  const actual = {...spec([actualRoot, {...backdrop, id: 'native.0', parent: 'native'}]), unavailable: [{element: 'native', property: 'effectiveBackgroundColor', reason: 'native paint unknown'}]};
  const result = compareScreens(spec([root, backdrop]), actual);
  assert.deepEqual(result.matches.map(value => [value.reference, value.actual, value.method]), [['0', 'native', 'canonical-canvas-root'], ['0.0', 'native.0', 'frame-anchor']]);
  assert.equal(result.differences.some(value => ['missing-element', 'ambiguous-match'].includes(value.kind)), false);
  assert.ok(result.differences.some(value => value.kind === 'unavailable-measurement'));
  const unknownBox = {...actual, elements: [{...actualRoot, box: null}, actual.elements[1]], unavailable: [...actual.unavailable, {element: 'native', property: 'box', reason: 'physical bounds unknown'}]};
  assert.ok(compareScreens(spec([root, backdrop]), unknownBox).matches.some(value => value.reference === '0' && value.actual === 'native'));
  const shifted = {...actual, elements: [{...actualRoot, box: {...actualRoot.box, x: 4}}, actual.elements[1]]};
  assert.ok(compareScreens(spec([root, backdrop]), shifted).differences.some(value => value.property === 'box.x'));
  const duplicate = {...actualRoot, id: 'other'};
  const invalid = compareScreens(spec([root, backdrop]), {...actual, elements: [...actual.elements, duplicate]});
  assert.equal(invalid.differences.filter(value => value.kind === 'anchor-cardinality').length, 1);
  assert.equal(invalid.matches.some(value => value.reference === '0'), false);
});

test('unanchored fallbacks cannot steal explicit anchors that appear later in reference order', () => {
  const plain = node(null, {id: 'plain', ownText: '', normalizedText: '', tag: 'div', style: {backgroundColor: 'rgb(7, 16, 24)'}});
  const anchored = {...plain, id: 'anchored', frame: 'reserved'};
  const result = compareScreens(spec([plain, anchored]), spec([anchored, plain]));
  assert.equal(result.verdict, 'PASS');
  assert.deepEqual(result.matches.map(value => [value.reference, value.actual]), [['plain', 'plain'], ['anchored', 'anchored']]);
});

test('unanchored parts cannot steal paint or text from a different anchored region', () => {
  const canvas = node(null, {id: '0', parent: null, tag: 'div', leaf: false, ownText: '', normalizedText: '',
    box: {x: 0, y: 0, width: 200, height: 100}, style: {backgroundColor: 'rgb(7, 16, 24)'}});
  const backdrop = node('backdrop', {id: '0.0', parent: '0', ownText: '', normalizedText: '', tag: 'div'});
  const absent = node(null, {id: '0.1', parent: '0', ownText: 'Unobserved external text'});
  const native = {...canvas, id: 'native'}, background = {...backdrop, id: 'native.0', parent: 'native'};
  const payload = {...absent, id: 'native.0.0', parent: 'native.0'};
  const result = compareScreens(spec([canvas, backdrop, absent]), spec([native, background, payload]));
  assert.ok(result.differences.some(value => value.kind === 'missing-element' && value.reference === '0.1'));
  assert.equal(result.matches.some(value => value.reference === '0.1'), false);
  assert.ok(result.differences.some(value => value.kind === 'extra-element' && value.actual === 'native.0.0'));
});

test('transform origin is inactive only when both observed transforms are identity', () => {
  const left = node('title'); left.style.transform = 'none'; left.style.transformOrigin = '0px 0px';
  const right = structuredClone(left); right.style.transformOrigin = '50px 20px';
  assert.equal(compareScreens(spec([left]), spec([right])).verdict, 'PASS');
  left.style.transform = right.style.transform = 'matrix(1, 0, 0, 1, 10, 0)';
  assert.ok(compareScreens(spec([left]), spec([right])).differences.some(value => value.property === 'transformOrigin'));
});

test('control text and its run rectangles use the same descendant scope', () => {
  const runs = [{x: 10, y: 20, width: 100, height: 20}];
  const control = node(null, {id: 'action', tag: 'button', ownText: 'Save', normalizedText: 'Save', textRects: runs, descendantTextRects: runs});
  const wrapped = {...control, ownText: '', textRects: []};
  assert.equal(compareScreens(spec([control]), spec([wrapped])).verdict, 'PASS');
  assert.ok(compareScreens(spec([control]), spec([{...wrapped, descendantTextRects: [{...runs[0], x: 13}]}])).differences.some(value => value.property === 'textRun.0.x'));
});

test('only an explicit content recipe delegates aggregate text to measured child parts', () => {
  const root = node('screen', {ownText: '', normalizedText: 'One Two', anchoredDescendants: true});
  const children = [node('first', {parent: 'screen', ownText: 'One'}), node('second', {parent: 'screen', ownText: 'Two'})];
  const adapter = {textHandledBySlots: ['screen']};
  assert.equal(compareScreens(spec([root, ...children]), spec([{...root, normalizedText: 'Two One'}, ...children]), adapter).verdict, 'PASS');
  assert.ok(compareScreens(spec([root, ...children]), spec([{...root, normalizedText: 'Two One'}, ...children])).differences.some(value => value.property === 'text'));
  const changed = children.map(value => value.frame === 'second' ? {...value, ownText: 'Wrong'} : value);
  assert.ok(compareScreens(spec([root, ...children]), spec([root, ...changed]), adapter).differences.some(value => value.property === 'text' && value.label === 'second'));
});

test('unanchored parts stay within their matched semantic ancestor across wrapper depths', () => {
  const heading = (id, parent) => node(null, {id, parent, tag: 'button', ownText: 'Edit'});
  const reference = spec([node('first'), heading('first-label', 'first'), node('second'), heading('second-label', 'second')]);
  const actual = spec([node('first', {id: 'a'}), {id: 'wrapper', parent: 'a', tag: 'div', leaf: false, style: {}},
    heading('a-label', 'wrapper'), node('second', {id: 'b'}), heading('b-label', 'b')]);
  const result = compareScreens(reference, actual);
  assert.equal(result.verdict, 'PASS');
  assert.equal(result.matches.find(value => value.reference === 'first-label').actual, 'a-label');
  assert.equal(result.matches.find(value => value.reference === 'second-label').actual, 'b-label');
  const ambiguous = compareScreens(reference, {...actual, elements: [...actual.elements, heading('a-duplicate', 'a')]});
  assert.ok(ambiguous.differences.some(value => value.kind === 'ambiguous-match' && value.reference === 'first-label'));
  const outside = compareScreens(reference, {...actual, elements: actual.elements.filter(value => value.id !== 'a-label')});
  assert.ok(outside.differences.some(value => value.kind === 'missing-element' && value.reference === 'first-label'));
});

test('matching icon slots never imply measured glyph identity', () => {
  const reference = spec([node('close', {image: {kind: 'icon'}, ownText: ''})]);
  const result = compareScreens(reference, structuredClone(reference));
  assert.equal(result.verdict, 'PASS');
  assert.ok(result.diagnostics.some(value => value.property === 'icon.identity/contents' && value.scored === false));
});

test('identical semantic screen passes, diagnostic DOM classes never change measured typography', () => {
  const reference = spec([node('title')]), actual = spec([node('title', {classes: ['different-implementation-wrapper']})]);
  const result = compareScreens(reference, actual);
  assert.equal(result.verdict, 'PASS'); assert.equal(result.differences.length, 0);
  assert.ok(result.diagnostics.some(value => value.property === 'classes' && value.scored === false));
});
test('measured negative fixture reports type, opacity, geometry, missing and extra drawn content', () => {
  const reference = spec([node('title'), node('all-genres', {ownText: 'Public genres'})]);
  const actual = spec([node('title', {box: {x: 14, y: 20, width: 100, height: 40}, style: {fontFamily: 'Example', fontSize: '34px', fontWeight: '800', opacity: '.6', color: 'rgb(255, 0, 0)'}}), node('added', {ownText: 'Added content'})]);
  const result = compareScreens(reference, actual);
  assert.equal(result.verdict, 'FAIL');
  for (const property of ['fontSize', 'effectiveOpacity', 'box.x', 'color']) assert.ok(result.differences.some(value => value.property === property));
  assert.ok(result.differences.some(value => value.kind === 'missing-element' && value.label === 'all-genres'));
  assert.ok(result.differences.some(value => value.kind === 'extra-element' && value.label === 'added'));
});
test('two pixel box boundary passes; larger geometry, viewport mismatch and ambiguous matching cannot pass', () => {
  assert.equal(compareScreens(spec([node('title')]), spec([node('title', {box: {x: 12, y: 20, width: 100, height: 40}})])).verdict, 'PASS');
  assert.equal(compareScreens(spec([node('title')]), spec([node('title', {box: {x: 12.01, y: 20, width: 100, height: 40}})])).verdict, 'FAIL');
  assert.equal(compareScreens(spec([node('title')]), {...spec([node('title')]), fullViewport: false}).verdict, 'FAIL');
  const plain = node(null, {id: 'expected', tag: 'button'});
  const ambiguous = compareScreens(spec([plain]), spec([{...plain, id: 'one'}, {...plain, id: 'two'}]));
  assert.equal(ambiguous.verdict, 'FAIL'); assert.ok(ambiguous.differences.some(value => value.kind === 'ambiguous-match'));
});
test('image slot geometry and fit are checked without artwork contents', () => {
  const image = node('art', {tag: 'img', ownText: '', image: {kind: 'image', loaded: true}, style: {objectFit: 'contain'}});
  assert.equal(compareScreens(spec([image]), spec([{...image}])).verdict, 'PASS');
  assert.ok(compareScreens(spec([image]), spec([{...image, style: {objectFit: 'cover'}}])).differences.some(value => value.property === 'objectFit'));
});

test('wrapped control label is a separate measured text part without descendant text hoisting', () => {
  const button = node('submit', {tag: 'button', ownText: 'Public action'});
  const shell = {...button, ownText: ''}, label = {...button, id: 'submit-label', frame: 'submit-label', tag: 'span'};
  const adapter = {textHandledBySlots: ['submit'], actualContentOnly: ['submit-label'], contentSlots: [{reference: 'submit', actual: 'submit-label'}]};
  const result = compareScreens(spec([button]), spec([shell, label]), adapter);
  assert.equal(result.verdict, 'PASS');
  const changed = {...label, style: {...label.style, fontSize: '34px'}};
  assert.ok(compareScreens(spec([button]), spec([shell, changed]), adapter).differences.some(value => value.property === 'fontSize'));
  assert.ok(compareScreens(spec([button]), spec([shell, {...label, ownText: 'Undrawn'}]), adapter).differences.some(value => value.property === 'text'));
  const wrapped = compareScreens(spec([button]), spec([shell, label]), {anchors: {public: 'submit'}, comparison: adapter});
  assert.equal(wrapped.verdict, 'FAIL');
  assert.equal(wrapped.differences[0].kind, 'invalid-adapter');
});

test('an unknown native image decode fact is never inferred as loaded', () => {
  const image = node('brand', {tag: 'img', ownText: '', normalizedText: '', image: {kind: 'image', loaded: true}});
  const actual = {...image, image: {kind: 'image'}};
  assert.ok(compareScreens(spec([image]), spec([actual])).differences.some(value => value.property === 'image-slot.loaded' && value.actual === undefined));
  const declared = {...spec([actual]), unavailable: [{element: actual.id, property: 'image.loaded', reason: 'decode state unobserved'}]};
  const result = compareScreens(spec([image]), declared);
  assert.ok(result.differences.some(value => value.kind === 'unavailable-measurement'));
  assert.equal(result.differences.some(value => value.property === 'image-slot.loaded'), false);
  assert.ok(compareScreens(spec([image]), spec([{...actual, image: {kind: 'image', loaded: false}}])).differences.some(value => value.property === 'image-slot.loaded' && value.actual === false));
});

test('raw alignment/normal line height and raw background do not replace rendered/composited facts', () => {
  const a = node('title', {normalizedText: 'Public heading', descendantTextRects: [{x: 10, y: 20, width: 100, height: 20}], effectiveOpacity: .5, effectiveBackgroundColor: 'rgba(7, 16, 24, 1)'});
  a.style = {...a.style, lineHeight: 'normal', textAlign: 'start', backgroundColor: 'rgba(0, 0, 0, 0)'};
  const b = {...a, style: {...a.style, lineHeight: '21px', textAlign: 'center', backgroundColor: 'rgb(7, 16, 24)'}};
  assert.equal(compareScreens(spec([a]), spec([b])).verdict, 'PASS');
  assert.ok(compareScreens(spec([a]), spec([{...b, descendantTextRects: [{x: 13, y: 20, width: 100, height: 20}]}])).differences.some(value => value.property === 'textRun.0.x'));
});

test('one repeated-list cardinality failure suppresses its missing/extra and ancestor text cascades', () => {
  const parent = node('screen', {id: '0', tag: 'div', ownText: '', normalizedText: 'One Two', anchoredDescendants: 3});
  const list = node('items', {id: '0.1', tag: 'div', ownText: '', normalizedText: 'One Two', anchoredDescendants: 2});
  const item = (index, text) => node(`items-${index}`, {id: `0.1.${index}`, tag: 'span', ownText: text, normalizedText: text, repeat: {list: 'items', item: `0.1.${index}`, index, role: 'button'}});
  const reference = {...spec([parent, list, item(0, 'One'), item(1, 'Two')]), repeatedLists: [{name: 'items', container: '0.1', count: 2}]};
  const actual = {...spec([{...parent, normalizedText: 'One'}, {...list, normalizedText: 'One'}, item(0, 'One')]), repeatedLists: [{name: 'items', container: '0.1', count: 1}]};
  const result = compareScreens(reference, actual);
  assert.deepEqual(result.differences, [{kind: 'list-cardinality', label: 'items', expected: 2, actual: 1}]);
  assert.deepEqual(result.cascadeRoots, {reference: ['0.1'], actual: ['0.1']});
});
