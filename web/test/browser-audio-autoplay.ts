import {DecodeAudioRender} from '../src/platform/playback/DecodeAudioRender';
import {ByteCache} from '../src/platform/playback/audio/bytes';
import flacUrl from '../../fixtures/audio-gapless/album-flac-01.flac?url';
import flacPlan from '../../fixtures/audio-gapless/album-flac-01.json';

if (!import.meta.env.DEV) throw new Error('Development audio activation fixture only.');
Object.assign(globalThis, {audioActivationProof: (async () => {
  const bytes = new Uint8Array(await (await fetch(flacUrl)).arrayBuffer()), cache = new ByteCache();
  cache.put('activation:0', bytes);
  const notices: string[] = [], failures: string[] = [], reports: unknown[] = [];
  const element = document.createElement('video'); element.muted = true;
  const snapshot: any = {intentId: 1, intent: 'playing', phase: 'ready', positionSeconds: 0,
    session: {id: 'activation-session', generation: 1, audio: {...flacPlan, version: 2, mode: 'direct', id: 'activation', url: flacUrl, bytes: bytes.length}},
    audioEffects: {rendering: true, rate: 1, settings: {gapless: false, crossfadeSeconds: 0, normalization: 'off'}}};
  let render: DecodeAudioRender;
  const service: any = {getSnapshot: () => snapshot, ready() {}, engineProgress() {}, fact() {}, audioRenderStatus() {}, setDeviceAudioProcessing() {},
    audioRenderFailed: (code: string) => failures.push(code), audioProcessingApplied: (...args: unknown[]) => reports.push(args),
    pause: () => { snapshot.intent = 'paused'; render.apply(snapshot); }, listeningNotice: (code: string) => notices.push(code)};
  render = new DecodeAudioRender(service, path => path, element, () => false, cache);
  try {
    render.apply(snapshot);
    const until = performance.now() + 3000;
    while (snapshot.intent !== 'paused' && !failures.length && performance.now() < until) await new Promise(resolve => setTimeout(resolve, 20));
    const context = (render as unknown as {context: AudioContext}).context;
    const result = {secureContext: isSecureContext, contextState: context.state, intent: snapshot.intent, notices, failures, reports,
      activation: {active: navigator.userActivation?.isActive, everActive: navigator.userActivation?.hasBeenActive}};
    document.querySelector('#result')!.textContent = JSON.stringify(result);
    // Passive network observation avoids a debugger evaluation granting user activation.
    await fetch('/__audio_activation_proof', {method: 'POST', body: JSON.stringify(result), headers: {'Content-Type': 'application/json'}});
    return result;
  } finally { render.dispose(); cache.clear(); }
})()});
