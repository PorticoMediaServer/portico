import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import type {MusicSignalInput} from '../../../packages/client-core/src/music-signal';
import type {ListeningSnapshot} from '../../../packages/client-core/src/listening';
import {nativeSignalPath} from '../src-next/screens/destinations/music/signal-path-model';
import {nativeListeningNotice, type NativeNoticeIdentity} from './fixtures/superseded/listening-notice-model';

const t = defaultI18n.t;
const signal: MusicSignalInput = {
  source: {codec: 'flac', lossless: true, bitDepth: 24, sampleRate: 96000, channels: 2},
  server: {route: 'original'}, output: {platform: 'apple', device: 'appleTv', sampleRate: 96000, sampleRateMatched: true},
  eqOn: false, eqApplied: false, normalization: 'off', levelingApplied: false,
};

test('native short signal retains shared precedence and actual output truth', () => {
  const normal = nativeSignalPath(signal, t);
  assert.equal(normal.rule, 5);
  assert.equal(normal.pill, t('player.signal.pill.rateMatched'));
  const unavailable = nativeSignalPath({...signal, eqOn: true}, t);
  assert.equal(unavailable.rule, 2);
  assert.equal(unavailable.state, t('player.signal.line.eqUnavailable'));
  assert.equal(unavailable.pill, normal.pill);
  assert.equal(unavailable.steps.find(step => step.id === 'eq')?.value, t('player.signal.notApplied'));
  const applied = nativeSignalPath({...signal, eqOn: true, eqApplied: true}, t);
  assert.equal(applied.state, t('player.signal.line.eqOn'));
  assert.equal(applied.pill, t('player.signal.pill.notBitPerfect'));
  const converted = nativeSignalPath({...signal, eqOn: true, server: {route: 'converted', delivered: {codec: 'flac'}, atmosLost: true}}, t);
  assert.equal(converted.rule, 1);
  assert.equal(converted.pill, t('player.signal.pill.atmosLost'));
});

test('native signal exposes short source, accessible long text and real step facts', () => {
  const view = nativeSignalPath({...signal, output: {platform: 'android', device: 'phone', sampleRate: 48000, resampledBy: 'phone'}}, t);
  assert.equal(view.rule, 4);
  assert.ok(view.source?.includes('96'));
  assert.ok(view.accessibleName?.includes('96'));
  assert.deepEqual(view.steps.map(step => step.id), ['file', 'server', 'leveling', 'eq', 'output']);
  assert.ok(view.steps.find(step => step.id === 'output')?.value?.includes('48'));
  assert.equal(view.healthy, false);
  assert.ok(Object.isFrozen(view.steps));
});

const identity: NativeNoticeIdentity = {sessionId: 'session-a', generation: 1, itemId: 'book-part', intentId: 3};
function listening(extra: Partial<ListeningSnapshot> = {}): ListeningSnapshot {
  return {
    eqApplied: false, levelingApplied: false, phase: 'ready', preferences: null,
    context: {itemId: identity.itemId, libraryId: 'books', kind: 'audiobook'}, rate: 1,
    timer: null, timerLoading: false, stopped: false, message: null, error: null,
    rewind: {seconds: 10, fromSeconds: 100, toSeconds: 90, messageKey: 'listening.rewound'}, ...extra,
  };
}

test('rewind projection uses existing snapshot and copies only a current public identity', () => {
  const notice = nativeListeningNotice(listening(), identity, identity);
  assert.deepEqual(notice, {identity, key: 'listening.rewound', values: {seconds: 10}});
  assert.notEqual(notice?.identity, identity);
  assert.ok(Object.isFrozen(notice?.identity));
});

test('context alone cannot retain rewind across session, generation or intent changes', () => {
  for (const changed of [{sessionId: 'session-b'}, {generation: 2}, {itemId: 'another-part'}, {intentId: 4}]) {
    assert.equal(nativeListeningNotice(listening(), identity, {...identity, ...changed}), undefined);
  }
  assert.equal(nativeListeningNotice(listening(), null, identity), undefined);
  assert.equal(nativeListeningNotice(listening(), identity, null), undefined);
  assert.equal(nativeListeningNotice(listening({context: null}), identity, identity), undefined);
});

test('stopped, errors and invalid rewinds stay quiet; message strings create no pause notice', () => {
  for (const extra of [{stopped: true}, {error: 'arbitrary engine text'}, {phase: 'disposed' as const}, {phase: 'error' as const},
    {rewind: undefined, message: 'Playback paused. Press play to continue.'}]) {
    assert.equal(nativeListeningNotice(listening(extra), identity, identity), undefined);
  }
  for (const seconds of [0, -1, Infinity, NaN, 11]) {
    assert.equal(nativeListeningNotice(listening({rewind: {seconds, fromSeconds: 100, toSeconds: 90, messageKey: 'listening.rewound'}}), identity, identity), undefined);
  }
});

const dsd = (multiple: number, deliveredRate: number): MusicSignalInput => ({
  source: {codec: 'dsd', dsdMultiple: multiple, channels: 2},
  server: {route: 'converted', dsdConverted: true, delivered: {codec: 'flac', sampleRate: deliveredRate}},
  output: {platform: 'apple', device: 'appleTv'},
  eqOn: false, eqApplied: false, normalization: 'off', levelingApplied: false,
});

test('DSD converted to PCM folds the source once at the delivered rate', () => {
  for (const [multiple, deliveredRate, khz] of [[64, 88200, 88.2], [128, 176400, 176.4]] as const) {
    const view = nativeSignalPath(dsd(multiple, deliveredRate), t);
    const rate = t('player.signal.rate', {khz});
    assert.equal(view.rule, 1);
    assert.equal(view.state, t('player.signal.line.dsdToPcm', {source: t('player.signal.source.dsd', {multiple}), rate}));
    // Folded, not repeated: the line carries the source and the visual line shows it once.
    assert.equal(view.source, undefined);
    assert.equal(view.accessibleName?.split(`DSD${multiple}`).length, 2);
    assert.ok(view.accessibleName?.includes(khz.toFixed(1)));
    assert.equal(view.pill, t('player.signal.pill.notBitPerfect'));
    assert.equal(view.healthy, false);
  }
});

test('an absent delivered rate stays unknown and claims no PCM rate or capability', () => {
  const view = nativeSignalPath({...dsd(64, 88200), server: {route: 'converted', dsdConverted: true, delivered: {codec: 'flac'}}}, t);
  assert.equal(view.rule, 1);
  assert.equal(view.state, t('player.signal.server.converted', {format: 'FLAC'}));
  assert.equal(view.source, 'DSD64');
  assert.equal(view.accessibleName?.split('DSD64').length, 2);
  for (const text of [view.state ?? '', view.accessibleName ?? '', JSON.stringify(view.steps)]) {
    assert.ok(!/kHz|DoP|passthrough/i.test(text), `no invented rate or passthrough in: ${text}`);
  }
  // The pill may say "not bit-perfect"; it may never say the output was bit-perfect.
  assert.equal(view.pill, t('player.signal.pill.notBitPerfect'));
  assert.equal(view.steps.find(step => step.id === 'output')?.value, undefined);
});

const layoutInput = (source: MusicSignalInput['source']): MusicSignalInput => ({
  source, server: {route: 'original'}, output: {platform: 'apple', device: 'appleTv'},
  eqOn: false, eqApplied: false, normalization: 'off', levelingApplied: false,
});
const fileValue = (input: MusicSignalInput) => nativeSignalPath(input, t).steps.find(step => step.id === 'file')?.value ?? '';

test('measured 5.1 and 7.1 layouts survive, and a channel count invents neither', () => {
  const lossless = {codec: 'flac', lossless: true, bitDepth: 24, sampleRate: 96000, channels: 6} as const;
  assert.ok(fileValue(layoutInput({...lossless, layout: '5.1'})).includes('5.1'));
  assert.ok(fileValue(layoutInput({...lossless, layout: '7.1', channels: 8})).includes('7.1'));
  const countOnly = fileValue(layoutInput(lossless));
  assert.ok(!/5\.1|7\.1|6\.0|8\.0/.test(countOnly), `a 6-channel count names no layout: ${countOnly}`);
});

test('source Atmos alone claims no passthrough and no output fact', () => {
  const view = nativeSignalPath(layoutInput({codec: 'truehd', atmos: true, lossless: true, bitDepth: 24, sampleRate: 48000, channels: 8, layout: '7.1'}), t);
  assert.equal(view.rule, 6);
  assert.equal(view.state, undefined);
  assert.equal(view.healthy, false);
  assert.ok(fileValue(layoutInput({codec: 'truehd', atmos: true, lossless: true, bitDepth: 24, sampleRate: 48000, channels: 8, layout: '7.1'})).includes('Atmos'));
  for (const text of [view.state ?? '', view.pill, view.accessibleName ?? '', JSON.stringify(view.steps)]) {
    assert.ok(!/passthrough|bit-perfect parity/i.test(text), `no passthrough claim in: ${text}`);
  }
  assert.equal(view.steps.find(step => step.id === 'output')?.value, undefined);
});

test('Atmos lost is drawn from measured server or output facts only', () => {
  const converted = nativeSignalPath({...layoutInput({codec: 'truehd', atmos: true, channels: 8, layout: '7.1'}),
    server: {route: 'converted', atmosLost: true, delivered: {codec: 'eac3', channels: 6, layout: '5.1'}}}, t);
  assert.equal(converted.rule, 1);
  assert.equal(converted.state, t('player.signal.line.atmosLost', {layout: '5.1'}));
  assert.equal(converted.pill, t('player.signal.pill.atmosLost'));
  const decoded = nativeSignalPath({...layoutInput({codec: 'flac', lossless: true, bitDepth: 24, sampleRate: 48000, channels: 8, layout: '7.1'}),
    output: {platform: 'apple', device: 'appleTv', decodedLayout: '5.1', atmosLost: true}}, t);
  assert.equal(decoded.rule, 4);
  assert.equal(decoded.state, t('player.signal.output.decoded', {layout: '5.1'}));
  assert.equal(decoded.stateLoss, t('player.signal.pill.atmosLost'));
});

test('baked loudness gain stays visible with normalization off, and explicit zero is not a gain', () => {
  const baked = nativeSignalPath({...layoutInput({codec: 'flac', lossless: true, bitDepth: 24, sampleRate: 96000, channels: 2}),
    server: {route: 'original', loudnessAppliedDb: -3.5}}, t);
  assert.equal(baked.rule, 3);
  assert.equal(baked.state, t('player.signal.line.loudness', {gain: '−3.5'}));
  assert.equal(baked.steps.find(step => step.id === 'leveling')?.value, t('player.signal.line.loudness', {gain: '−3.5'}));
  const zero = nativeSignalPath({...layoutInput({codec: 'flac', lossless: true, bitDepth: 24, sampleRate: 96000, channels: 2}),
    server: {route: 'original', loudnessAppliedDb: 0}}, t);
  assert.notEqual(zero.rule, 3);
  assert.equal(zero.steps.find(step => step.id === 'leveling')?.value, t('player.signal.off'));
});

test('a requested EQ claims no applied gain, and an original format is sent as it is', () => {
  const base = layoutInput({codec: 'flac', lossless: true, bitDepth: 24, sampleRate: 96000, channels: 2});
  const requested = nativeSignalPath({...base, eqOn: true}, t);
  assert.equal(requested.rule, 2);
  assert.equal(requested.state, t('player.signal.line.eqUnavailable'));
  assert.equal(requested.steps.find(step => step.id === 'eq')?.value, t('player.signal.notApplied'));
  assert.equal(requested.steps.find(step => step.id === 'eq')?.changed, false);
  const original = nativeSignalPath(base, t);
  assert.equal(original.steps.find(step => step.id === 'server')?.value, t('player.signal.server.asIs'));
  // A renderer gain that was not applied is not claimed as applied gain.
  const unused = nativeSignalPath({...base, levelingGainDb: -6}, t);
  assert.equal(unused.steps.find(step => step.id === 'leveling')?.value, t('player.signal.off'));
});

test('a bit-perfect flag alone claims no bit-perfect output', () => {
  const view = nativeSignalPath({...layoutInput({codec: 'flac', lossless: true, bitDepth: 24, sampleRate: 96000, channels: 2}),
    output: {platform: 'apple', device: 'appleTv', sampleRate: 96000, bitDepth: 24, bitPerfect: true}}, t);
  assert.equal(view.healthy, false);
  assert.notEqual(view.rule, 5);
  assert.equal(view.pill, t('player.signal.pill.notBitPerfect'));
  assert.equal(view.steps.find(step => step.id === 'output')?.value, undefined);
  for (const text of [view.state ?? '', view.accessibleName ?? '']) {
    assert.ok(!/bit-perfect|passthrough/i.test(text), `no output claim in: ${text}`);
  }
});

test('conversion outranks baked loudness, and applied leveling reports its own mode and gain', () => {
  const converted = nativeSignalPath({...layoutInput({codec: 'flac', lossless: true, bitDepth: 24, sampleRate: 96000, channels: 2}),
    server: {route: 'converted', loudnessAppliedDb: -3.5, delivered: {codec: 'flac'}}}, t);
  assert.equal(converted.rule, 1);
  assert.equal(converted.state, t('player.signal.server.converted', {format: 'FLAC'}));
  const levelled = nativeSignalPath({...layoutInput({codec: 'flac', lossless: true, bitDepth: 24, sampleRate: 96000, channels: 2}),
    normalization: 'track', levelingApplied: true, levelingGainDb: -2}, t);
  assert.equal(levelled.rule, 3);
  assert.equal(levelled.state, t('player.signal.line.loudness', {gain: '−2.0'}));
  assert.equal(levelled.steps.find(step => step.id === 'leveling')?.value, t('player.signal.leveling.applied', {mode: 'track', gain: '−2.0'}));
});

const stereo96 = {codec: 'flac', lossless: true, bitDepth: 24, sampleRate: 96000, channels: 2} as const;

test('observed passthrough shows while nothing altered the signal, and each contrary fact removes it', () => {
  const base: MusicSignalInput = {...layoutInput(stereo96), output: {platform: 'apple', device: 'receiver', passthrough: true}};
  const passed = nativeSignalPath(base, t);
  assert.equal(passed.rule, 5);
  assert.equal(passed.healthy, true);
  assert.equal(passed.state, t('player.signal.line.passthrough'));
  assert.equal(passed.steps.find(step => step.id === 'output')?.value, t('player.signal.line.passthrough'));
  assert.equal(passed.steps.find(step => step.id === 'leveling')?.value, t('player.signal.notApplied'));
  assert.equal(passed.steps.find(step => step.id === 'eq')?.value, t('player.signal.notApplied'));
  const contrary: MusicSignalInput[] = [
    {...base, server: {route: 'converted', delivered: {codec: 'flac'}}},
    {...base, eqOn: true, eqApplied: true},
    {...base, normalization: 'track', levelingApplied: true, levelingGainDb: -3},
    {...base, server: {route: 'original', atmosLost: true}},
    {...base, output: {platform: 'apple', device: 'receiver', passthrough: true, atmosLost: true}},
  ];
  for (const input of contrary) {
    const view = nativeSignalPath(input, t);
    assert.equal(view.healthy, false, JSON.stringify(input.server ?? input.output));
    assert.notEqual(view.rule, 5);
    assert.notEqual(view.state, t('player.signal.line.passthrough'));
    // The shared signal model still labels the output step "passed through" when the route
    // was converted or EQ was applied; that wording lives in client-core and is Media's to
    // correct, so it is recorded here rather than asserted as intended behaviour.
  }
});

test('Android USB bit-perfect needs the whole measured tuple, and Apple matched stays matched', () => {
  const output = {platform: 'android' as const, device: 'phone' as const, bitPerfect: true as const, bitPerfectMode: 'android14-usb' as const,
    usbDac: true, sampleRate: 96000, bitDepth: 24};
  const perfect = nativeSignalPath({...layoutInput(stereo96), output}, t);
  assert.equal(perfect.rule, 5);
  assert.equal(perfect.healthy, true);
  assert.equal(perfect.pill, t('player.signal.pill.bitPerfect'));
  assert.equal(perfect.state, t('player.signal.line.bitPerfect', {output: t('player.signal.output.usbDac')}));
  assert.equal(perfect.steps.find(step => step.id === 'output')?.value, t('player.signal.output.bitPerfect', {bits: 24, rate: t('player.signal.rate', {khz: 96})}));
  const single: MusicSignalInput['output'][] = [
    {...output, bitPerfectMode: undefined},
    {...output, usbDac: undefined},
    {...output, sampleRate: 48000},
    {...output, bitDepth: 16},
  ];
  for (const partial of single) {
    const view = nativeSignalPath({...layoutInput(stereo96), output: partial}, t);
    assert.equal(view.healthy, false);
    assert.notEqual(view.pill, t('player.signal.pill.bitPerfect'));
  }
  const matched = nativeSignalPath({...layoutInput(stereo96), output: {platform: 'apple', device: 'appleTv', sampleRate: 96000, bitDepth: 24, sampleRateMatched: true, bitPerfect: true}}, t);
  assert.equal(matched.rule, 5);
  assert.equal(matched.pill, t('player.signal.pill.rateMatched'));
  assert.notEqual(matched.pill, t('player.signal.pill.bitPerfect'));
});

test('absent, zero or nonfinite measurements and a bare channel count fabricate nothing', () => {
  const unknown = nativeSignalPath(layoutInput({codec: 'flac', lossless: true, bitDepth: 0, sampleRate: 0, channels: 2}), t);
  for (const text of [unknown.state ?? '', unknown.accessibleName ?? '', JSON.stringify(unknown.steps)]) {
    assert.ok(!/kHz|\d+-bit/i.test(text), `no invented rate or depth in: ${text}`);
  }
  assert.equal(unknown.accessibleName?.split('FLAC').length, 2);
  const nonfinite = nativeSignalPath(layoutInput({codec: 'flac', lossless: true, bitDepth: Number.POSITIVE_INFINITY, sampleRate: Number.NaN, channels: 2}), t);
  assert.ok(!/kHz/.test(JSON.stringify(nonfinite.steps)));
  const eightChannels = fileValue(layoutInput({codec: 'flac', lossless: true, bitDepth: 24, sampleRate: 96000, channels: 8}));
  assert.ok(!/5\.1|7\.1|6\.0|8\.0|receiver/i.test(eightChannels), `a bare 8-channel count names no layout: ${eightChannels}`);
  assert.ok(fileValue(layoutInput({codec: 'flac', lossless: true, bitDepth: 24, sampleRate: 96000, channels: 1})).includes('mono'));
  assert.ok(fileValue(layoutInput(stereo96)).includes('stereo'));
  const deliveredUnknown = nativeSignalPath({...layoutInput({codec: 'flac', lossless: true, bitDepth: 24, sampleRate: 96000, channels: 8}),
    server: {route: 'converted', delivered: {codec: 'eac3', channels: 8}}}, t);
  assert.ok(!/5\.1|7\.1|kHz/.test(deliveredUnknown.state ?? '') && !/5\.1|7\.1/.test(JSON.stringify(deliveredUnknown.steps)));
});
