import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeTitleController} from '../src-next/screens/watch/title-controller.ts';

test('a retired Play season load cannot play the newly selected season or a retained failed page', async () => {
 for (const phase of ['ready', 'error'] as const) {
  const authority = {serverId: 's', accountId: 'a', profileId: 'p', epoch: '1', viewerFence: 'viewer'};
  const controller = new NativeTitleController({entry: {id: 'show', kind: 'show', title: 'Show', libraryId: 'l'} as never,
   api: {request: async () => {throw new Error('no network expected');}}, scope: {serverId: 's', viewerId: 'a:p'} as never,
   authority, current: () => authority, subscribeCurrent: () => () => {}, requestId: async () => 'op', playback: {start: async () => {}}});
  const played: string[] = [];let release!: () => void;
  controller.season = () => new Promise(resolve => {release = resolve;});
  controller.play = async entry => {played.push(entry!.id);};
  controller.workspace.getSnapshot = () => ({phase, data: {selected: {seasonId: phase === 'ready' ? 's3' : 's2'}, nextUp: {id: 'episode-s3'}}}) as never;
  try {
   const pending = controller.playSeason('s2');release();await pending;
   assert.deepEqual(played, []);
   controller.workspace.getSnapshot = () => ({phase: 'ready', data: {selected: {seasonId: 's2'}, nextUp: {id: 'episode-s2'}}}) as never;
   const valid = controller.playSeason('s2');release();await valid;assert.deepEqual(played, ['episode-s2']);
  } finally {controller.dispose();}
 }
});
