import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';

function nodes(value: any): any[] {
  if (Array.isArray(value)) return value.flatMap(nodes);
  return value?.props ? [value, ...nodes(value.props.children)] : [];
}

async function sheet(view: Record<string, unknown>) {
  const {ReadingDownloadSheet} = await screenModule(new URL('../src-next/pages/touch/downloads/ReadingDownloadSheet.tsx', import.meta.url), {
    react: {...React, default: React},
    '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string, values?: Record<string, unknown>) => values ? `${key}:${JSON.stringify(values)}` : key}},
    '../../../kit/frames': {SectionCard: 'SectionCard', SheetFrame: 'SheetFrame'},
    '../../../kit/overlays/SurfacePaintHost': {RetainedOverlay: 'RetainedOverlay'},
    '../../../kit/saved/DownloadChoiceRows': {DownloadChoiceRow: 'DownloadChoiceRow'}, '../../../kit/states': {StateView: 'StateView'},
    '../../../kit/theme': {useTheme: () => ({platform: 'phone'})},
    '../../shared/downloads/useReadingDownloadChoice': {useReadingDownloadChoice: () => view},
  });
  return ReadingDownloadSheet({context: {}, request: {title: 'Dune'}});
}

const epub = {sourceId: 'epub', format: 'epub', size: 1_900_000}, pdf = {sourceId: 'pdf', format: 'pdf', size: 9, unreadable: 'The file is damaged.'};
const base = {versions: [epub, pdf], selectedVersion: epub, ready: (version: any) => version === epub, size: (version: any) => `${version.size} B`,
  busy: false, loading: false, unavailable: false, failed: false, refusal: undefined, free: '12 GB', permit: undefined, select() {}, dismiss() {}, download() {}, retryLoad() {}};

test('a book\'s Download is the kit task sheet: its versions as one card, the chosen one ticked, Download with its size as the action', async () => {
  const tree = await sheet(base);
  const frame = nodes(tree).find(node => node.type === 'SheetFrame');
  assert.equal(frame.props.task, 'task'); assert.equal(frame.props.subtitle, 'Dune');
  assert.equal(frame.props.action.label, 'action.download · 1900000 B'); assert.equal(frame.props.action.disabled, false);
  const card = nodes(tree).find(node => node.type === 'SectionCard');
  assert.equal(card.props.help, 'next.saved.phone.free:{"size":"12 GB"}');
  const [first, second] = nodes(card).filter(node => node.type === 'DownloadChoiceRow');
  assert.deepEqual([first.props.label, first.props.detail, first.props.selected, first.props.disabled], ['EPUB', '1900000 B', true, false]);
  // A version the server can't open says why and offers nothing.
  assert.deepEqual([second.props.description, second.props.detail, second.props.disabled], ['The file is damaged.', undefined, true]);
});

test('while it adds, and when nothing could load, the sheet says so in place', async () => {
  assert.equal(nodes(await sheet({...base, busy: true})).find(node => node.type === 'SheetFrame').props.action.label, 'downloads.adding');
  const failed = nodes(await sheet({...base, versions: [], selectedVersion: undefined, failed: true}));
  assert.equal(failed.find(node => node.type === 'SheetFrame').props.action, undefined);
  assert.equal(failed.find(node => node.type === 'StateView').props.primaryAction.id, 'retry');
});
