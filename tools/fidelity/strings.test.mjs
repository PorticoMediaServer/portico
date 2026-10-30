import test from 'node:test';
import assert from 'node:assert/strict';
import {catalogueMatches, compareStrings, compareCatalogueStrings, mapScreenStrings, stringInventory} from './strings.mjs';

const screen = strings => ({scope: 'screen', strings: strings.map((value, index) => ({id: String(index), kind: 'text', ...value}))});

test('strings inventory retains exact visible values, duplicates and separate accessibility channels', () => {
  const measured = screen([{text: 'Account'}, {text: 'Account'}, {kind: 'value', text: 'public-user'}, {kind: 'accessible-label', text: 'Account'}]);
  assert.deepEqual(stringInventory(measured), [
    {channel: 'visible', text: 'Account', occurrences: ['0', '1']},
    {channel: 'visible', text: 'public-user', occurrences: ['2']},
    {channel: 'accessible-label', text: 'Account', occurrences: ['3']},
  ]);
});

test('comparison rejects missing duplicate, extra copy and semantic substitution', () => {
  const reference = screen([{text: 'Save'}, {text: 'Save'}, {text: 'Exact copy'}]);
  const actual = screen([{text: 'Save'}, {kind: 'accessible-label', text: 'Save'}, {text: 'Exact Copy'}, {text: 'Added'}]);
  const result = compareStrings(reference, actual);
  assert.equal(result.required, 3); assert.equal(result.matched, 1);
  assert.deepEqual(result.differences, [
    {kind: 'missing-string', channel: 'visible', text: 'Save', count: 1},
    {kind: 'missing-string', channel: 'visible', text: 'Exact copy', count: 1},
    {kind: 'extra-string', channel: 'accessible-label', text: 'Save', count: 1},
    {kind: 'extra-string', channel: 'visible', text: 'Exact Copy', count: 1},
    {kind: 'extra-string', channel: 'visible', text: 'Added', count: 1},
  ]);
});

test('catalogue lookup reports exact existing keys without normalizing or making new copy', () => {
  const values = screen([{text: "Can't connect"}, {text: 'Can’t connect'}, {text: 'é'}, {text: 'e\u0301'}]);
  assert.deepEqual(catalogueMatches(values, {first: "Can't connect", other: "Can't connect", unicode: 'é'}).map(value => value.catalogueKeys), [['first', 'other'], [], ['unicode'], []]);
  assert.throws(() => stringInventory({scope: 'crop', strings: []}), /screen measurement/);
  assert.throws(() => catalogueMatches(values, {wrong: {nested: true}}), /flat string/);
});

test('explicit keys resolve catalogue ambiguity; public data slots are exact and cannot bless arbitrary copy', () => {
  const frame = screen([{text: 'Save', frame: 'save'}, {text: 'Public title', frame: 'title'}]);
  const catalogue = {one: 'Save', two: 'Save'};
  assert.equal(mapScreenStrings(frame, catalogue)[0].status, 'ambiguous');
  const adapter = {keys: {save: 'two'}, data: {title: {name: 'title', value: 'Public title'}}};
  assert.deepEqual(mapScreenStrings(frame, catalogue, adapter).map(value => value.status), ['mapped', 'data']);
  const built = screen([{text: 'Save', frame: 'save'}, {text: 'Wrong title', frame: 'title'}, {text: 'Undrawn extra', frame: 'extra'}]);
  const result = compareCatalogueStrings(frame, built, catalogue, adapter);
  assert.ok(result.differences.some(value => value.kind === 'built-string-data-mismatch'));
  assert.ok(result.differences.some(value => value.kind === 'built-string-unmapped' && value.text === 'Undrawn extra'));
});

test('list cardinality root excludes only that list from derivative copy cascades', () => {
  const reference = screen([{text: 'First', owner: '0.1.0'}, {text: 'Second', owner: '0.1.1'}]);
  const actual = screen([{text: 'First', owner: '0.1.0'}]);
  const roots = {reference: ['0.1'], actual: ['0.1']};
  assert.deepEqual(compareCatalogueStrings(reference, actual, {one: 'First', two: 'Second'}, {}, roots).differences, []);
  const outside = screen([{text: 'First', owner: '0.1.0'}, {text: 'Unrelated extra', owner: '0.2'}]);
  assert.ok(compareCatalogueStrings(reference, outside, {one: 'First', two: 'Second'}, {}, roots).differences.some(value => value.kind === 'built-string-unmapped'));
});

test('declared formatted catalogue slots use the real caller formatter and retain occurrence checks', () => {
  const catalogue = {count: '{count} public rows'}, adapter = {messageSlots: [{name: 'rows.count', key: 'count', values: {count: 3}}]};
  let calls = 0;
  const format = (key, values) => {calls++; assert.equal(key, 'count'); assert.deepEqual(values, {count: 3}); return '3 public rows';};
  const reference = screen([{text: '3 public rows'}]), actual = screen([{text: '3 public rows'}]);
  assert.equal(compareCatalogueStrings(reference, actual, catalogue, adapter, undefined, format).differences.length, 0); assert.ok(calls > 0);
  actual.strings.push({id: 'extra', kind: 'text', text: '3 public rows'});
  assert.ok(compareCatalogueStrings(reference, actual, catalogue, adapter, undefined, format).differences.some(value => value.kind === 'extra-string'));
  assert.ok(compareCatalogueStrings(reference, actual, catalogue, adapter).differences.some(value => value.kind === 'message-slot-invalid'));
});
test('explicit public fixture literals and preferred existing keys never approve arbitrary copy', () => {
  const catalogue = {one: 'Public action', duplicate: 'Public action'};
  const values = screen([{text: 'Public action'}, {text: 'Q'}, {text: 'Undeclared value'}]);
  const mapped = mapScreenStrings(values, catalogue, {preferredKeys: ['one'], dataValues: [{name: 'fixture.monogram', value: 'Q'}]});
  assert.equal(mapped[0].status, 'mapped'); assert.deepEqual(mapped[0].catalogueKeys, ['one']);
  assert.equal(mapped[1].dataSlot, 'fixture.monogram'); assert.equal(mapped[2].status, 'unmapped');
});
