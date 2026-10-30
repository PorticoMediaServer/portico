import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeModules,device} from './react-native-stub.mjs';
import {OfflineDownloads} from '../platform/offline-downloads';

test('volume capacity is the native metric and is never synthesized from free space plus downloads', async () => {
  const native=NativeModules.PorticoDownloads, original=native.storage;
  device.reset();
  try {
    for (const total of [123456,undefined,NaN,-1,0,1.5,Number.MAX_SAFE_INTEGER+1]) {
      native.storage=async()=>({freeBytes:1000,usedBytes:50,storedBytes:400,allocatedBytes:512,...(total===undefined?{}:{totalBytes:total})});
      const store=new OfflineDownloads();
      try {
        assert.equal(store.getSnapshot().totalBytes,undefined);
        await store.open();await new Promise(done=>setImmediate(done));
        assert.equal(store.getSnapshot().freeBytes,1000);assert.equal(store.getSnapshot().usedBytes,50);
        assert.equal(store.getSnapshot().totalBytes,total===123456?123456:undefined);
        assert.equal(store.getSnapshot().storedBytes,400);assert.equal(store.getSnapshot().allocatedBytes,512);
      } finally {store.dispose();}
    }
  } finally {native.storage=original;device.reset();}
});
