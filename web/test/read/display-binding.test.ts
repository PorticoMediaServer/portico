import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {test} from 'node:test';
import {parsePreferenceSnapshot, type ReadingPreferences} from '@portico/client-core';
import {createReadingController, type ReaderAdapter} from '@portico/reader-core';
import {defaultI18n} from '@i18n';
import {readingDisplayControls, type ReadingDisplayCopy} from '../../src/screens/read/display.ts';
import {readingDisplayCopy} from '../../src/screens/read/display-copy.ts';

const recorded = JSON.parse(readFileSync(new URL('../../../server/testdata/contracts/get-bootstrap.json', import.meta.url), 'utf8')).response.preferences;
const copy: ReadingDisplayCopy = {
  themes: {label: 'Page', options: {dark: 'Dark', black: 'Black', sepia: 'Sepia', light: 'Light'}},
  fonts: {label: 'Font', options: {publisher: {label: 'Publisher’s', compactLabel: 'Publisher’s'}, literata: {label: 'Literata', compactLabel: 'Literata'}, manrope: {label: 'Manrope', compactLabel: 'Manrope'}}},
  size: {label: 'Size', sliderLabel: 'Text size', smallerLabel: 'Smaller', largerLabel: 'Larger', valueText: value => `${value} of 7`},
  lineSpacing: {label: 'Line spacing', options: {tight: 'Tight', normal: 'Normal', loose: 'Loose'}},
  margins: {label: 'Margins', options: {narrow: 'Narrow', normal: 'Normal', wide: 'Wide'}},
  alignment: {label: 'Alignment', options: {left: 'Left', justified: 'Justified'}},
  layout: {label: 'Layout', options: {pages: 'Pages', scroll: 'Scrolling'}},
  note: {sample: 'Aa', sizeSample: 'A', sessionNote: 'Changes here last until you close this book.', defaultsNote: 'Your defaults are in Settings, Reading.'},
  twoPages: {label: 'Two pages side by side in landscape', description: 'Held upright, a book always shows one page.'},
};
const scale = {min: 1, max: 7, step: 1, ticks: [1, 2, 3, 4, 5, 6, 7]};
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
const adapter: ReaderAdapter<ReadingPreferences> = {format: 'epub', async open({source}) {
  return {id: source.id, revision: source.revision, format: 'epub', metadata: {}, toc: [], dispose() {}, async *search() {},
    async mount() {return {async navigate() {}, async resize() {}, async applyPreferences() {}, getLocation: () => null, dispose() {}};}};
}};
const source = {id: 'display-binding-unit', revision: 'unit-1', size: 1};

test('Display uses the actual core snapshot and changes only its current book overrides', async t => {
  const confirmed = parsePreferenceSnapshot(recorded), before = JSON.stringify(confirmed);
  const controller = createReadingController({adapters: [adapter], preferenceSnapshot: confirmed});
  t.after(() => controller.close());
  const authority = {isCurrent: () => true, subscribe: () => () => {}};
  const errors: string[] = [];
  await controller.open({source, format: 'epub', container: {} as HTMLElement, viewport: {width: 390, height: 844}});
  assert.equal(controller.getSnapshot().status, 'ready');
  const model = () => readingDisplayControls({controller, authority, copy, scale, phone: true, reportError: code => errors.push(code)});
  const desktop = () => readingDisplayControls({controller, authority, copy: readingDisplayCopy(defaultI18n, 'desktop', scale.max), scale, reportError: code => errors.push(code)});
  assert.equal(model().size.value, controller.getSnapshot().preferences.textSize);
  model().themes.onChange('sepia'); model().fonts.onChange('literata'); model().size.onChange(5);
  model().rows.find(row => row.id === 'layout')!.onChange('scroll');
  model().twoPages!.onChange(false); await tick();
  assert.equal(model().themes.value, 'sepia'); assert.equal(model().fonts.value, 'literata');
  assert.equal(model().size.valueText, '5 of 7'); assert.equal(model().size.summary, '5 of 7');
  assert.deepEqual(model().size.ticks, scale.ticks); assert.equal(desktop().size.ticks, undefined);
  assert.equal(model().rows.find(row => row.id === 'layout')!.value, 'scroll');
  assert.equal(controller.getSnapshot().preferences.layout, 'scroll');
  assert.equal(model().twoPages!.checked, false);
  assert.equal(desktop().rows.find(row => row.id === 'columns')!.value, 'one');
  desktop().rows.find(row => row.id === 'columns')!.onChange('auto'); await tick();
  assert.equal(model().twoPages!.checked, true);
  assert.deepEqual(new Set(controller.getSnapshot().overriddenKeys), new Set(['theme', 'font', 'textSize', 'layout', 'twoPages']));
  assert.equal(JSON.stringify(confirmed), before); assert.deepEqual(errors, []);
  await controller.close();
  await controller.open({source, format: 'epub', container: {} as HTMLElement, viewport: {width: 390, height: 844}});
  assert.deepEqual(controller.getSnapshot().overriddenKeys, []);
  assert.equal(model().themes.value, confirmed.effective['reading.theme']);
  await controller.close();
});

test('Display copy retains the drawn platform differences without a preference default', () => {
  const desktop = readingDisplayCopy(defaultI18n, 'desktop', scale.max), phone = readingDisplayCopy(defaultI18n, 'phone', scale.max), tablet = readingDisplayCopy(defaultI18n, 'tablet', scale.max);
  assert.equal(desktop.size.valueText(5), '5'); assert.equal(phone.size.valueText(5), '5 of 7');
  assert.equal(desktop.fonts.options.publisher.label, 'Publisher’s font'); assert.equal(phone.fonts.options.publisher.compactLabel, 'Publisher’s');
  assert.equal(desktop.columns?.label, 'Columns'); assert.equal(phone.columns, undefined);
  assert.equal(tablet.twoPages?.description, 'Held upright, a book always shows one page.'); assert.equal(phone.twoPages, undefined);
  assert.equal(phone.note.openSettings, undefined); assert.equal(tablet.note.openSettings, 'Open Settings');
});

test('stale Display callbacks and invalid control values cannot alter a retired or unopened book', async t => {
  const controller = createReadingController({adapters: [adapter], preferenceSnapshot: parsePreferenceSnapshot(recorded)});
  t.after(() => controller.close());
  let current = true;
  const errors: string[] = [];
  const authority = {isCurrent: () => current, subscribe: () => () => {}};
  const model = () => readingDisplayControls({controller, authority, copy, scale, reportError: code => errors.push(code)});
  const beforeOpen = model();
  beforeOpen.themes.onChange('sepia'); await tick(); assert.deepEqual(controller.getSnapshot().overriddenKeys, []);
  await controller.open({source, format: 'epub', container: {} as HTMLElement, viewport: {width: 390, height: 844}});
  assert.equal(controller.getSnapshot().status, 'ready');
  beforeOpen.themes.onChange('sepia'); await tick(); assert.deepEqual(controller.getSnapshot().overriddenKeys, []);
  const controls = model();
  controls.themes.onChange('unknown'); controls.fonts.onChange('unknown');
  controls.size.onChange(NaN); controls.size.onChange(8); controls.size.onChange(2.5);
  controls.rows.find(row => row.id === 'layout')!.onChange('scrolling');
  await tick(); assert.deepEqual(controller.getSnapshot().overriddenKeys, []);
  current = false; controls.themes.onChange('sepia'); controls.twoPages!.onChange(false);
  await tick(); assert.deepEqual(controller.getSnapshot().overriddenKeys, []); assert.deepEqual(errors, []);
  current = true;
  await controller.open({source: {...source, revision: 'unit-2'}, format: 'epub', container: {} as HTMLElement, viewport: {width: 390, height: 844}});
  controls.themes.onChange('sepia'); await tick(); assert.deepEqual(controller.getSnapshot().overriddenKeys, []);
  await controller.close();
});
