import test from 'node:test';
import assert from 'node:assert/strict';
import {MediaCarrier, type CarrierElement} from '../src/platform/playback/audio/carrier.ts';

/** B-17.19: the media element owns controls; the context owns the heard sample clock. */
function rig(o: {stream?: boolean; refuse?: boolean} = {}) {
  const links: string[] = [], listeners = new Map<string, () => void>();
  const destination = {name: 'speakers'} as unknown as AudioDestinationNode;
  const stream = {name: 'stream', stream: {id: 'ms'} as unknown as MediaStream, disconnect: () => links.push('stream-off')} as unknown as MediaStreamAudioDestinationNode;
  const output = {connect: (to: {name: string}) => { links.push('to ' + to.name); }, disconnect: (to?: AudioNode) => links.push(to ? 'stream-input-off' : 'output-off')} as unknown as AudioNode;
  const silent = {name: 'control-silence', gain: {value: 1}, connect: (to: any) => links.push('silence to ' + to.name), disconnect: () => links.push('silence-off')} as unknown as GainNode;
  const context = {destination, ...(o.stream === false ? {} : {createMediaStreamDestination: () => stream, createGain: () => silent})};
  const element: CarrierElement & {plays: number; pauses: number} = {
    srcObject: null, paused: true, plays: 0, pauses: 0,
    play() { this.plays++; if (o.refuse) return Promise.reject(new Error('NotAllowedError')); (this as {paused: boolean}).paused = false; return Promise.resolve(); },
    pause() { this.pauses++; (this as {paused: boolean}).paused = true; listeners.get('pause')?.(); },
    setAttribute() {},
    addEventListener(type, fn) { listeners.set(type, fn); },
    removeEventListener(type) { listeners.delete(type); },
  };
  let reported = 0;
  const carrier = new MediaCarrier(context, output, () => { reported++; }, () => element);
  return {carrier, element, links, listeners, silent, reported: () => reported};
}
const settle = () => new Promise(r => setTimeout(r, 0));

test('music goes straight to the speakers while the silent element carries controls and reports only outside pauses', () => {
  const r = rig();
  assert.equal(r.carrier.carrying, true);
  assert.deepEqual(r.links, ['silence to stream', 'to speakers', 'to control-silence']);
  assert.equal(r.silent.gain.value, 0, 'the element cannot add delayed music to the direct speaker output');
  assert.equal((r.element.srcObject as unknown as {id: string}).id, 'ms');
  r.carrier.play(); r.carrier.play();
  assert.equal(r.element.plays, 1, 'already playing: not asked twice');
  r.carrier.pause();
  assert.equal(r.reported(), 0, 'the page’s own pause is not news');
  r.carrier.play();
  // The system takes the audio (a call, the lock screen's pause without a handler): the player hears of it.
  (r.element as {paused: boolean}).paused = true; r.listeners.get('pause')!();
  assert.equal(r.reported(), 1);
  r.carrier.dispose();
  assert.equal(r.element.srcObject, null);
  assert.equal(r.listeners.size, 0);
});

test('without a stream destination, or when the element refuses to play, the audio goes straight to the speakers', async () => {
  const none = rig({stream: false});
  assert.equal(none.carrier.carrying, false);
  assert.deepEqual(none.links, ['to speakers']);
  const refused = rig({refuse: true});
  refused.carrier.play();
  await settle();
  assert.equal(refused.carrier.carrying, false);
  assert.deepEqual(refused.links, ['silence to stream', 'to speakers', 'to control-silence', 'stream-input-off', 'stream-off', 'silence-off']);
  assert.equal(refused.reported(), 0, 'falling back is not a pause');
});

test('only the current carrier refusal is reported; pause and disposal fence late play rejections', async () => {
  for (const action of ['current', 'pause', 'dispose'] as const) {
    const r = rig(); let reject!: (error: unknown) => void, refused: unknown;
    r.element.play = () => new Promise<void>((_, no) => { reject = no; });
    r.carrier.play(error => { refused = error; });
    if (action === 'pause') r.carrier.pause();
    if (action === 'dispose') r.carrier.dispose();
    const error = new DOMException('controlled policy refusal', 'NotAllowedError'); reject(error); await settle();
    assert.equal(refused, action === 'current' ? error : undefined);
    if (action === 'pause') assert.equal(r.carrier.carrying, true, 'a stale refusal cannot replace the current output path');
    r.carrier.dispose();
  }
});

// Real HTMLMediaElement.pause queues its event; it does not dispatch on the pause() stack.
test('a delayed buffering pause stays ours, including a pause delivered after resume', () => {
  const r = rig();
  r.element.pause = () => { (r.element as {paused: boolean}).paused = true; };
  r.carrier.play(); r.carrier.pause();
  r.listeners.get('pause')!();
  assert.equal(r.reported(), 0, 'buffering must not change the listener intent to paused');
  r.carrier.play(); r.carrier.pause(); r.carrier.play();
  r.listeners.get('pause')!();
  assert.equal(r.reported(), 0, 'a late event must not pause the resumed track');
  (r.element as {paused: boolean}).paused = true; r.listeners.get('pause')!();
  assert.equal(r.reported(), 1, 'a real outside pause is still reported');
  r.carrier.dispose();
});
