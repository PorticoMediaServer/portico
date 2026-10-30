/** Component-level regression: original directory, selection and request custody across hidden toggles. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, tree} from './helpers/interaction-harness.mjs';

test('hidden filter preserves the nested selection and only the latest listing publishes', async () => {
  const h = createRenderer(); h.react.useId = () => 'folder-path';
  const calls: any[] = [];
  const service = {listFolders: (id: string, query: any) => new Promise((resolve, reject) => calls.push({id, query, resolve, reject}))};
  const app = await componentModule(new URL('../src/screens/settings/remote-sources/RemoteFolderPicker.tsx', import.meta.url), {
    react: h.react, '@i18n': {defaultI18n: {t: (key: string) => key}},
    '../../../kit/overlays/Dialog': {Dialog: 'Dialog'}, '../../../kit/controls': {Button: 'Button', Switch: 'Switch', TextField: 'TextField'},
    '../../../kit/Icon': {Icon: 'Icon'}, '../../../kit/overlays/shared': {usePhone: () => true},
    './remote-sources.module.css': {default: {}},
  });
  let open = true, sourceId = 'one';
  const render = () => tree(h.render(() => app.RemoteFolderPicker({service, sourceId, sourceName: 'Storage', open, onOpenChange() {}, onChoose() {}})));
  const settle = async () => {await Promise.resolve(); await Promise.resolve(); return render();};
  const page = (relative: string, entries: any[] = [], nextCursor?: string) => ({relative, path: `/root/${relative}`, entries, nextCursor});
  const nested = {name: 'Nested', path: '/root/nested', relative: 'nested', hidden: false};
  let view = render(); assert.equal(calls.length, 1); assert.equal(calls[0].query.showHidden, false);
  calls[0].resolve(page('', [nested])); view = await settle();
  all(view, 'button').find((node: any) => node.props.role === 'checkbox')!.props.onClick();
  all(view, 'button').find((node: any) => node.props['aria-label'] === 'next.remoteSources.openFolder')!.props.onClick();
  assert.equal(calls[1].query.path, 'nested'); calls[1].resolve(page('nested', [], 'next')); view = await settle();
  const toggle = all(view, 'Switch')[0]; toggle.props.onCheckedChange(true);
  assert.equal(calls[2].query.path, 'nested'); assert.equal(calls[2].query.showHidden, true);
  // A second original toggle supersedes the first request, whose late result must be ignored.
  toggle.props.onCheckedChange(false); assert.equal(calls[3].query.path, 'nested'); assert.equal(calls[3].query.showHidden, false);
  calls[3].resolve(page('nested', [nested], 'next')); view = await settle();
  calls[2].resolve(page('stale')); view = await settle();
  assert.equal(all(view, 'Switch')[0].props.checked, false);
  assert.equal(all(view, 'TextField')[0].props.value, 'nested');
  assert.equal(all(view, 'button').filter((node: any) => node.props['aria-label'] === 'next.remoteSources.removeFolder').length, 1);
  all(view, 'Switch')[0].props.onCheckedChange(true); assert.equal(calls[4].query.path, 'nested');
  calls[4].resolve(page('nested', [nested], 'next')); view = await settle();
  assert.equal(all(view, 'Switch')[0].props.checked, true);
  assert.equal(all(view, 'TextField')[0].props.value, 'nested');
  assert.equal(all(view, 'button').filter((node: any) => node.props['aria-label'] === 'next.remoteSources.removeFolder').length, 1);
  all(view, 'Button').find((node: any) => node.props.children?.includes?.('next.remoteSources.showMoreFolders'))!.props.onClick();
  assert.equal(calls[5].query.path, 'nested'); assert.equal(calls[5].query.showHidden, true); assert.equal(calls[5].query.cursor, 'next');
  calls[5].reject(new Error('retry')); view = await settle();
  all(view, 'Button').find((node: any) => node.props.children?.includes?.('next.remoteSources.retry'))!.props.onClick();
  assert.equal(calls[6].query.path, 'nested'); assert.equal(calls[6].query.showHidden, true); assert.equal(calls[6].query.cursor, 'next');
  calls[6].resolve(page('nested')); view = await settle();
  open = false; view = render(); open = true; view = render();
  assert.equal(calls[7].query.showHidden, false); assert.equal(calls[7].query.path, undefined);
  calls[7].resolve(page('')); view = await settle();
  assert.equal(all(view, 'Switch')[0].props.checked, false);
  assert.equal(all(view, 'button').filter((node: any) => node.props['aria-label'] === 'next.remoteSources.removeFolder').length, 0);
  sourceId = 'two'; view = render(); assert.equal(calls[8].id, 'two'); assert.equal(calls[8].query.showHidden, false); assert.equal(calls[8].query.path, undefined);
});
