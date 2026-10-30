import test from 'node:test';
import assert from 'node:assert/strict';
import {createNativeOfflineDownloadsAdapter, offlineDownloads, type OfflineEntry, type OfflineSnapshot} from '../src-next/platform/offline-downloads';
// The shared device module behind every shell's downloads.
import {offlineDownloads as deviceOwner} from '../platform/offline-downloads';

test('both shells share the same authoritative device owner', () => {
  assert.equal(offlineDownloads,deviceOwner);
});

test('the screen port observes the same device store and routes play through its runtime callback', async () => {
  const entry: OfflineEntry = {id:'copy',itemId:'movie',owner:'viewer',title:'Movie',subtitle:'',quality:'original',artworkUrl:'file:///art',state:'ready',error:'',waiting:'',held:false,bytes:50,bytesDone:50,addedAt:1,onServer:true,changedOnServer:false};
  let state: OfflineSnapshot = {supported:true,ready:true,entries:[entry],settings:{wifiOnly:true,deleteWatched:false,autoNext:false,limitBytes:100},freeBytes:1000,usedBytes:50,otherBytes:30,pendingProgress:0};
  const calls: unknown[] = [], listener = () => {};
  const device = {
    getSnapshot: () => state,
    subscribe: (value: () => void) => {calls.push(value); return () => calls.push('off');},
    open: async () => {calls.push('open');},
    setSettings: async (value: Partial<OfflineSnapshot['settings']>) => {calls.push(value);},
    pause: async (value: OfflineEntry) => {calls.push(['pause',value]);},
    resume: async (value: OfflineEntry) => {calls.push(['resume',value]);},
    remove: async (value: OfflineEntry) => {calls.push(['remove',value]);},
    removeOthersConfirmed: async (expected: {revision: number; bytes: number}) => {calls.push(['others', expected]);},
    playable: () => '', playabilityReason: () => null, displayFacts: async () => ({}), note: () => '',
    localMedia: async (value: OfflineEntry) => ({entryId:value.id,itemId:value.itemId,playId:'play',fileUrl:'file:///media',durationSeconds:10,startSeconds:0}),
  };
  const adapter = createNativeOfflineDownloadsAdapter(async value => {calls.push(['play',value]);},device);
  const storageOnly = createNativeOfflineDownloadsAdapter(undefined, device);
  assert.equal(storageOnly.openOffline, undefined, 'no placeholder playback command is installed');
  assert.equal(storageOnly.getSnapshot(), state, 'the genuine local store remains available without playback');
  assert.equal(adapter.getSnapshot(),state);
  const off = adapter.subscribe(listener); await adapter.open(); await adapter.setSettings({limitBytes:75});
  await adapter.openOffline(entry); await adapter.pause(entry); await adapter.resume(entry); await adapter.remove(entry); await adapter.removeOthers({revision: 1, bytes: 30});
  assert.equal((await adapter.localMedia(entry)).fileUrl,'file:///media'); off();
  assert.deepEqual(calls,[listener,'open',{limitBytes:75},['play',entry],['pause',entry],['resume',entry],['remove',entry],['others',{revision:1,bytes:30}],'off']);
  state = {...state, entries:[]};
  await assert.rejects(adapter.openOffline(entry));
  await assert.rejects(async () => adapter.pause(entry));
  await assert.rejects(async () => adapter.localMedia(entry));
  assert.equal(calls.length,9);
});
