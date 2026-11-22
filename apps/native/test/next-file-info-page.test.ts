import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

test('File info draws its versions and asks for the next window only when Show more is pressed', async () => {
  const host = hooks(), shown: string[] = [], calls: string[] = [];
  const {NativeFileInfoPanel} = await screenModule(new URL('../src-next/pages/shared/title/FileInfoPanel.tsx', import.meta.url), {
    react: host.react,
    '../../../../../../packages/client-core/src/file-info-session': {fileInfoTrackKey: (id: string, lane: string) => `${id}:${lane}`},
    '../../../screens/watch/FileInfoContent': {NativeFileInfoContent: 'Content'},
    '../../../screens/watch/file-info': {
      nativeFileInfoShowVersion: (_session: unknown, group: {id: string}) => {shown.push(group.id);}, nativeFileInfoShowFile() {},
      nativeFileInfoView: () => ({model: {id: 'model'}, locations: [], versionCount: 2}),
    },
  });
  const snapshot = {groups: {rows: {rows: [{id: 'v1'}, {id: 'v2'}], more: true}, status: 'ready'}, tracks: new Map(), headTracks: new Map(), members: new Map(), surface: 'title'};
  const session = {subscribe: () => () => {}, getSnapshot: () => snapshot, groups: (mode: string) => {calls.push(`groups:${mode}`);}};
  let tree: any;
  host.render(() => {tree = NativeFileInfoPanel({session, kind: 'movie', text: {}, isTV: false, onManage() {}});});
  assert.equal(tree.type, 'Content');
  assert.deepEqual(tree.props.model, {id: 'model'});
  assert.deepEqual(calls, [], 'nothing drains the cursor on its own');
  tree.props.more.onPress();
  assert.deepEqual(calls, ['groups:next']);
});
