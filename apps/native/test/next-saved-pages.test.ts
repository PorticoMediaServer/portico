import assert from 'node:assert/strict';
import test from 'node:test';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

test('Saved keeps the selected tablet tab above its content', async () => {
  const h = hooks();
  const {SavedPage} = await screenModule(new URL('../src-next/pages/touch/saved/SavedPage.tsx', import.meta.url), {
    react: h.react, '../../../kit/frames': {BrowseFrame: 'BrowseFrame'},
  });
  const tabs = {items: [{value: 'downloads', label: 'Downloads'}], value: 'downloads', onValueChange() {}};
  const frame = h.render(() => SavedPage({title: 'Saved', tabs, body: 'Device downloads'}));
  assert.equal(frame.props.header.title, 'Saved');
  assert.equal(frame.props.header.tabs, tabs);
  assert.equal(frame.props.grid, 'Device downloads');
});

test('Downloads keeps the device inventory and returns to Saved', async () => {
  const h = hooks();
  const {DownloadsPage} = await screenModule(new URL('../src-next/pages/touch/saved/DownloadsPage.tsx', import.meta.url), {
    react: h.react, '../../../kit/frames': {BrowseFrame: 'BrowseFrame'},
  });
  let returned = false;
  const frame = h.render(() => DownloadsPage({title: 'Downloads', back: {label: 'Saved', onPress: () => {returned = true;}}, body: 'Inventory'}));
  assert.equal(frame.props.header.title, 'Downloads');
  assert.equal(frame.props.header.back.label, 'Saved');
  assert.equal(frame.props.grid, 'Inventory');
  frame.props.header.back.onPress();
  assert.equal(returned, true);
});
