import assert from 'node:assert/strict';
import test from 'node:test';
import {NativeModules} from './react-native-stub.mjs';
import {NativeQueueStore} from '../src-next/platform/queue-storage';
import {bindNativeDeviceSettings} from '../src-next/platform/device-settings';
import {bindNativeDevicePreferences, readNativeDevicePreference, writeNativeDevicePreference} from '../src-next/screens/settings/device-preference-bindings';

const key = 'preferences:v1';
const deferred = () => {let resolve!: () => void; const promise = new Promise<void>(done => {resolve = done;}); return {promise, resolve};};
const device = (extra: Record<string, unknown> = {}) => ({playerEngine: 'automatic', forceStereo: false, disableHdr: false, ...extra});
type State = {raw: string | null; saves: number; loads: number; beforeSave?: () => Promise<void>; beforeRead?: () => Promise<void>};

async function records(run: (state: State) => Promise<void>, raw = JSON.stringify(device())) {
  const runtime = NativeModules.PorticoRuntime as Record<string, unknown>, old = {read: runtime.loadQueueRecord, write: runtime.saveQueueRecord};
  const state: State = {raw, saves: 0, loads: 0};
  runtime.loadQueueRecord = async (recordKey: string) => {assert.equal(recordKey, key); state.loads++; await state.beforeRead?.(); return state.raw;};
  runtime.saveQueueRecord = async (recordKey: string, next: string) => {assert.equal(recordKey, key); await state.beforeSave?.(); state.raw = next; state.saves++;};
  try {await run(state);} finally {runtime.loadQueueRecord = old.read; runtime.saveQueueRecord = old.write;}
}

async function fixture() {
  const storage = new NativeQueueStore(), listeners = new Set<() => void>();
  let current = true;
  let observed = await storage.change(key, raw => ({record: raw, value: raw}));
  const owner = {current: () => current, subscribePreferences: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}};
  const ports = {storage, readRecord: () => observed, acceptRecord: (raw: unknown) => {observed = raw; for (const listener of listeners) listener();}};
  return {owner, source: bindNativeDeviceSettings(owner, ports), retire: () => {current = false;}, listeners};
}

const value = (current: unknown) => ({current, effective: current, default: undefined, source: 'device', configOnly: false});

test('canonical device rows decode through the one installed source', async () => {
  await records(async () => {
    const f = await fixture();
    assert.deepEqual(readNativeDevicePreference('device.player', f.source), value('mpv'));
    assert.deepEqual(readNativeDevicePreference('audioSubtitles.surround', f.source), value('stereo'));
    assert.deepEqual(readNativeDevicePreference('quality.disableHdr', f.source), value(true));
  }, JSON.stringify(device({playerEngine: 'mpv', forceStereo: true, disableHdr: true})));
});

test('automatic player, asis surround and HDR enabled decode without inventing a default', async () => {
  await records(async () => {
    const f = await fixture();
    assert.deepEqual(readNativeDevicePreference('device.player', f.source), value('auto'));
    assert.deepEqual(readNativeDevicePreference('audioSubtitles.surround', f.source), value('asis'));
    assert.deepEqual(readNativeDevicePreference('quality.disableHdr', f.source), value(false));
  });
});

test('non-device, multi-key and unsupported rows are unavailable without reading the source', async () => {
  await records(async state => {
    const f = await fixture(), original = f.source.read, reads: string[] = [], loads = state.loads;
    f.source.read = id => {reads.push(id); return original(id);};
    try {
      for (const rowId of ['device.playerLink', 'playback.skipping', 'general.name', 'unknown.row']) {
        assert.equal(readNativeDevicePreference(rowId, f.source), undefined, rowId);
      }
      assert.deepEqual(reads, []);
      assert.equal(state.loads, loads);
      assert.equal(state.saves, 0);
    } finally {f.source.read = original;}
  });
});

test('device writes use canonical codecs and return the committed reread', async () => {
  await records(async state => {
    const f = await fixture();
    for (const [rowId, asked, stored, raw] of [
      ['device.player', 'auto', 'playerEngine', 'automatic'],
      ['device.player', 'native', 'playerEngine', 'native'],
      ['device.player', 'mpv', 'playerEngine', 'mpv'],
      ['audioSubtitles.surround', 'stereo', 'forceStereo', true],
      ['audioSubtitles.surround', 'asis', 'forceStereo', false],
      ['quality.disableHdr', true, 'disableHdr', true],
      ['quality.disableHdr', false, 'disableHdr', false],
    ] as const) {
      assert.deepEqual(await writeNativeDevicePreference(rowId, asked, f.source), value(asked), rowId);
      assert.equal((JSON.parse(state.raw!) as Record<string, unknown>)[stored], raw, rowId);
    }
    assert.deepEqual(JSON.parse(state.raw!), device({playerEngine: 'mpv'}));
  });
});

test('a device write resolves only after the same source saves and rereads, and refusal keeps the current row', async () => {
  await records(async state => {
    const f = await fixture(), begun = deferred(), durable = deferred();
    state.beforeSave = async () => {begun.resolve(); await durable.promise;};
    let settled = false;
    const writing = writeNativeDevicePreference('device.player', 'mpv', f.source).then(result => {settled = true; return result;});
    await begun.promise;
    assert.equal(settled, false);
    assert.equal(readNativeDevicePreference('device.player', f.source)!.current, 'auto');
    assert.equal(JSON.parse(state.raw!).playerEngine, 'automatic');
    durable.resolve();
    assert.deepEqual(await writing, value('mpv'));
    assert.equal(readNativeDevicePreference('device.player', f.source)!.current, 'mpv');
    assert.deepEqual(JSON.parse(state.raw!), device({playerEngine: 'mpv', cardScale: 1.25, serverCache: {kept: true}}));
    state.beforeSave = async () => {throw new Error('save refused');};
    await assert.rejects(writeNativeDevicePreference('audioSubtitles.surround', 'stereo', f.source), /save refused/);
    assert.equal(readNativeDevicePreference('audioSubtitles.surround', f.source)!.current, 'asis');
    assert.equal(JSON.parse(state.raw!).forceStereo, false);
  }, JSON.stringify(device({cardScale: 1.25, serverCache: {kept: true}})));
});

test('the TV output row composes both device keys and writes only its own override', async () => {
  await records(async state => {
    const f = await fixture();
    assert.deepEqual(readNativeDevicePreference('quality.tvSupports', f.source), value('none'), 'an unset override reads as nothing chosen');
    assert.deepEqual(await writeNativeDevicePreference('quality.tvSupports', 'hdr', f.source), value('hdr'));
    assert.equal(JSON.parse(state.raw!).tvHdr, true);
    assert.equal(JSON.parse(state.raw!).tvSurround, false);
    assert.deepEqual(await writeNativeDevicePreference('quality.tvSupports', 'hdr-surround', f.source), value('hdr-surround'));
    assert.equal(JSON.parse(state.raw!).tvHdr, true);
    assert.equal(JSON.parse(state.raw!).tvSurround, true);
    assert.deepEqual(await writeNativeDevicePreference('quality.tvSupports', 'none', f.source), value('none'));
    assert.equal(JSON.parse(state.raw!).tvHdr, false);
    assert.equal(JSON.parse(state.raw!).tvSurround, false);
  });
});

test('invalid values and unavailable rows never reach the device writer', async () => {
  await records(async state => {
    const f = await fixture(), saves = state.saves, writes: Array<[unknown, unknown]> = [], write = f.source.write;
    f.source.write = async (id, next) => {writes.push([id, next]); return write(id, next);};
    try {
      for (const [rowId, asked] of [
        ['device.player', 'speedy'],
        ['audioSubtitles.surround', 'mono'],
        ['quality.disableHdr', 'yes'],
        ['quality.tvSupports', 'dolby'],
        ['playback.skipping', 'off'],
        ['general.name', 'x'],
        ['unknown.row', 'x'],
      ] as const) {
        await assert.rejects(writeNativeDevicePreference(rowId, asked, f.source), /Invalid setting value|native_device_setting_unsupported/, rowId);
      }
      assert.deepEqual(writes, []);
      assert.equal(state.saves, saves);
    } finally {f.source.write = write;}
  });
});

test('two borrowers share the one bound source and unsubscribing never retires it', async () => {
  await records(async () => {
    const f = await fixture(), a = bindNativeDevicePreferences(f.owner), b = bindNativeDevicePreferences(f.owner), seen: string[] = [];
    assert.deepEqual(a.read('device.player'), value('auto'));
    assert.deepEqual(b.read('device.player'), value('auto'));
    const offA = a.subscribe(() => seen.push('a')), offB = b.subscribe(() => seen.push('b'));
    await a.write('device.player', 'native');
    assert.deepEqual(seen, ['a', 'b']);
    seen.length = 0; offA();
    await b.write('audioSubtitles.surround', 'stereo');
    assert.deepEqual(seen, ['b']);
    seen.length = 0; offB();
    await a.write('quality.disableHdr', true);
    assert.deepEqual(seen, []);
    assert.equal(a.read('device.player')!.current, 'native');
    assert.equal(b.read('quality.disableHdr')!.current, true);
    const c = bindNativeDevicePreferences(f.owner);
    assert.deepEqual(c.read('audioSubtitles.surround'), value('stereo'));
  });
});

test('source retirement blocks late writes, reads and notifications', async () => {
  await records(async state => {
    const f = await fixture(), bound = bindNativeDevicePreferences(f.owner), seen: string[] = [];
    bound.subscribe(() => seen.push('late'));
    const begun = deferred(), release = deferred();
    state.beforeRead = async () => {begun.resolve(); await release.promise;};
    const writing = bound.write('device.player', 'mpv');
    await begun.promise;
    f.retire();
    release.resolve();
    await assert.rejects(writing, {name: 'AbortError'});
    assert.deepEqual(seen, []);
    assert.equal(state.saves, 0);
    assert.equal(JSON.parse(state.raw!).playerEngine, 'automatic');
    assert.throws(() => bound.read('device.player'), {name: 'AbortError'});
    assert.throws(() => bound.subscribe(() => {}), {name: 'AbortError'});
    await assert.rejects(bound.write('quality.disableHdr', true), {name: 'AbortError'});
  });
});
