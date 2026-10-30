import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {lifecycleHooks, nodes, content} from './helpers/owner-server-settings.mjs';

const t = (key: string) => key;
const theme = {colors: {}, type: {caption: {}, body: {}, bodyStrong: {}}};
const tick = async () => {await Promise.resolve(); await Promise.resolve();};

test('remote list keeps offscreen selections through paging, Up and hidden changes and stops at 500', async () => {
  const h = lifecycleHooks(), calls: any[] = [], chosen: any[] = [];
  const original = {name: 'Retained', relative: 'Elsewhere/Retained', path: '/source/Elsewhere/Retained'};
  const controller = {sourceFolders: async (_source: unknown, options: any) => {
    calls.push(options); const offset = Number(options.cursor ?? 0), relative = options.path ?? '';
    return {sourceId: 'archive', generation: '1', relative, path: '/source' + (relative ? `/${relative}` : ''), nextCursor: String(offset + 100), truncated: false,
      entries: Array.from({length: 100}, (_, i) => ({name: `Folder ${offset + i}`, path: `/source/${relative ? relative + '/' : ''}${offset + i}`, relative: `${relative ? relative + '/' : ''}${offset + i}`}))};
  }};
  const module = await screenModule(new URL('../src-next/screens/settings/remote-sources/SourceFolderBrowser.tsx', import.meta.url), {
    react: h.react, 'react-native': {Text: 'Text', View: 'View', FlatList: 'FlatList'}, '../../../../../../packages/i18n/src': {defaultI18n: {t}},
    '../../../kit/controls/Button': {Button: 'Button'}, '../../../kit/controls/Switch': {Switch: 'Switch'}, '../../../kit/Icon': {Icon: 'Icon'},
    '../../../kit/controls/Focusable': {Focusable: 'Focusable'}, '../../../kit/theme': {useTheme: () => theme},
    './RemoteSourceForms': {Page: 'Page', Problem: 'Problem', SourceWizardContext: {value: undefined}, useAttempt: (current: any) => current, remoteSourceError: () => 'error'},
  });
  const render = () => h.render(() => module.SourceFolderBrowser({controller, source: {id: 'archive', name: 'Archive', generation: 1}, t, isCurrent: () => true, onCancel() {}, onChoose() {}, initial: [original], onSelectionChange: (rows: any[]) => chosen.push(rows)}));
  const getList = () => nodes(render()).find((node: any) => node.type === 'FlatList')!;
  const button = (label: string) => nodes(render()).find((node: any) => node.type === 'Button' && content(node) === label)!;
  try {
    render(); h.flush(); await tick();
    assert.equal(nodes(render()).find((node: any) => node.type === 'Page').props.scrollable, false);
    for (let page = 1; page < 5; page++) {button('next.remoteSources.showMoreFolders').props.onPress(); await tick();}
    const list = getList(); assert.equal(list.props.data.length, 500); assert.equal(list.props.initialNumToRender, 16);
    assert.equal(button('next.remoteSources.showMoreFolders'), undefined);
    assert.ok(nodes(render()).some((node: any) => node.type === 'Text' && content(node) === 'next.remoteSources.folderTruncated'));
    assert.equal(calls.length, 5);
    const row = list.props.renderItem({item: list.props.data[499]});
    nodes(row).find((node: any) => node.type === 'Focusable').props.onPress();
    assert.deepEqual(chosen.at(-1).map((folder: any) => folder.relative), [original.relative, '499']);
    nodes(row).find((node: any) => node.type === 'Button').props.onPress(); await tick();
    assert.equal(getList().props.data.length, 100);
    nodes(render()).find((node: any) => node.type === 'Switch').props.onCheckedChange(true); await tick();
    assert.equal(calls.at(-1).showHidden, true);
    button('next.remoteSources.upOneFolder').props.onPress(); await tick();
    assert.equal(calls.at(-1).showHidden, true); assert.equal(calls.at(-1).path, undefined);
    assert.ok(button('next.remoteSources.removeFolder')); assert.equal(chosen.at(-1).length, 2);
  } finally {h.dispose();}
});
