import {BrowserEqualizer, eqFrequencies, equalizerResponse, flatEqualizer} from '../src/platform/playback/audio/equalizer';
import {EqualizerStore, deviceEqualizerDatabase, loadDeviceEqualizer, saveDeviceEqualizer, resetDeviceEqualizer, subscribeDeviceEqualizer} from '../src/platform/playback/audio/equalizer-store';
import {Deck, linkDecks} from '../src/platform/playback/audio/engine';
import {qualifyBrowserDecoders} from '../src/platform/playback/audio/qualification';
import {WEBCODECS_SKIP, browserEngine} from '../src/platform/playback/audio/track';
import {DecodeAudioRender} from '../src/platform/playback/DecodeAudioRender';
import {ByteCache} from '../src/platform/playback/audio/bytes';
import flacUrl from '../../fixtures/audio-gapless/album-flac-01.flac?url';
import flacPlan from '../../fixtures/audio-gapless/album-flac-01.json';

const check = (ok: unknown, message: string) => { if (!ok) throw new Error(message); };
const rms = (data: Float32Array, from = 24000) => Math.sqrt(data.subarray(from).reduce((sum, value) => sum + value * value, 0) / (data.length - from));

export async function runBrowserAudioChecks() {
  const report: Record<string, unknown> = {userAgent: navigator.userAgent, secureContext: isSecureContext, audioDecoderAvailable: !!(globalThis as unknown as {AudioDecoder?: unknown}).AudioDecoder};
  // Render actual Web Audio nodes. Compare the audio, not a second implementation of a biquad.
  const response: number[] = [];
  let responseErrorDb = 0;
  for (let band = 0; band < eqFrequencies.length; band++) {
    const context = new OfflineAudioContext(1, 48000, 48000), eq = new BrowserEqualizer(context);
    const bandsDb = eqFrequencies.map((_, i) => i === band ? 6 : 0);
    eq.apply({enabled:true,preampDb:-3,bandsDb}); eq.output.connect(context.destination);
    const oscillator = context.createOscillator(); oscillator.frequency.value = eqFrequencies[band]!;
    const attenuation = context.createGain(); attenuation.gain.value = 0.05;
    oscillator.connect(attenuation); attenuation.connect(eq.input); oscillator.start();
    const data = (await context.startRendering()).getChannelData(0);
    const db = 20 * Math.log10(rms(data) / (0.05 / Math.SQRT2));
    check(Math.abs(db - 3) < 0.16, `band ${band}: ${db} dB instead of 3`);
    response.push(db); eq.dispose();
  }
  report.eqNetDbAtCentres = response;

  const curveContext = new OfflineAudioContext(1,48000,48000), curveEq = new BrowserEqualizer(curveContext);
  const curveSettings = {enabled:true,preampDb:-3,bandsDb:[5.5,3,-.5,-1.5,.5,-1,2.5,-2,1.5,-3.5]};
  // Keep the graph active so the real scheduled parameter ramps advance during rendering.
  const silence = curveContext.createConstantSource(); silence.offset.value = 0;
  silence.connect(curveEq.input); curveEq.output.connect(curveContext.destination); silence.start();
  curveEq.apply(curveSettings); await curveContext.startRendering();
  const frequencies = Array.from({length:241},(_,i)=>31*(16000/31)**(i/240));
  const predicted = equalizerResponse(curveSettings,48000,frequencies), actual = curveEq.frequencyResponse(frequencies);
  predicted.forEach((value,i)=>{responseErrorDb=Math.max(responseErrorDb,Math.abs(value-actual[i]!));});
  check(responseErrorDb<.001,'saved response does not match actual Web Audio filters: '+responseErrorDb);
  report.responseCurveMaxErrorDb=responseErrorDb;curveEq.dispose();

  const context = new OfflineAudioContext(6, 4096, 48000), eq = new BrowserEqualizer(context);
  eq.apply(flatEqualizer); eq.output.connect(context.destination);
  const source = context.createBufferSource(), input = context.createBuffer(6, 4096, 48000);
  for (let ch = 0; ch < 6; ch++) { const data = input.getChannelData(ch); for (let i = 0; i < data.length; i++) data[i] = (ch + 1) / 16 * Math.sin(2 * Math.PI * (ch + 1) * 100 * i / 48000); }
  source.buffer = input; source.connect(eq.input); source.start();
  const bypass = await context.startRendering();
  for (let ch = 0; ch < 6; ch++) check(bypass.getChannelData(ch).every((value, i) => value === input.getChannelData(ch)[i]), `off EQ changed channel ${ch}`);
  report.sixChannelOffEqExact = true; eq.dispose();

  // Actual scheduler -> AudioBufferSourceNodes -> EQ bypass -> OfflineAudioContext, adjacent frames.
  const joined = new OfflineAudioContext(2, 24000, 48000), graph = new BrowserEqualizer(joined);
  graph.output.connect(joined.destination);
  const pcm = (frames: number, value: number) => {
    let at = 0;
    return {sampleRate:48000,channels:2,close(){},async read(max: number) {
      const count = Math.min(max, frames - at); if (!count) return;
      at += count; return {frames:count, channels:[new Float32Array(count).fill(value),new Float32Array(count).fill(-value)]};
    }};
  };
  const a = new Deck(joined, graph.input, pcm(10001,0.125), {frames:10001,gain:1});
  const b = new Deck(joined, graph.input, pcm(13999,0.25), {frames:13999,gain:1});
  await a.pump(1); await b.pump(1); a.start(0);
  const edge = linkDecks(joined,a,b,{crossfadeSeconds:0,sameAlbum:true,earliest:0});
  check(edge.start === 10001, 'nonexact scheduled edge');
  const audio = await joined.startRendering();
  for (let ch = 0; ch < 2; ch++) check(audio.getChannelData(ch).every((value,i) => value === (i < 10001 ? 0.125 : 0.25) * (ch ? -1 : 1)), 'gap or overlap at actual render boundary');
  report.gaplessJoinFrame = edge.start; a.close(); b.close(); graph.dispose();

  // One app/device value, with no output identity: real normalized IndexedDB transactions.
  const database = 'portico-audio-conformance-' + Date.now() + '-' + Math.random().toString(36).slice(2);
  let store = new EqualizerStore(indexedDB,database);
  const saved = {enabled:true,preampDb:-4,bandsDb:[1,2,3,4,0,-1,-2,-3,-4,-5]};
  try {
    check(JSON.stringify(await store.load()) === JSON.stringify(flatEqualizer), 'new app EQ is not off and flat');
    await store.save(saved); await store.close(); store = new EqualizerStore(indexedDB,database);
    check(JSON.stringify(await store.load()) === JSON.stringify(saved), 'app setting did not survive re-open');
    const second = new EqualizerStore(indexedDB,database);
    try {
      check(JSON.stringify(await second.load()) === JSON.stringify(saved), 'app callers did not share one setting');
      const reset = await second.reset();
      check(reset.enabled && reset.preampDb === 0 && reset.bandsDb.every(gain => gain === 0), 'reset did not flatten controls while retaining the switch');
      check(JSON.stringify(await store.load()) === JSON.stringify(reset), 'reset did not persist to the same app setting');
    } finally { await second.close(); }
    let rejected = false; try { await store.save({...saved,bandsDb:[NaN]}); } catch { rejected = true; }
    check(rejected, 'invalid equalizer settings accepted');
    const db = await new Promise<IDBDatabase>((resolve,reject) => { const r=indexedDB.open(database);r.onsuccess=()=>resolve(r.result);r.onerror=()=>reject(r.error); });
    try {
      check([...db.objectStoreNames].sort().join(',') === 'bands,settings', 'unexpected EQ stores');
      const tx=db.transaction(['settings','bands'],'readonly');
      const requests=[tx.objectStore('settings').getAll(),tx.objectStore('bands').getAll()];
      const rows=await Promise.all(requests.map(r=>new Promise<any[]>((resolve,reject)=>{r.onsuccess=()=>resolve(r.result);r.onerror=()=>reject(r.error);})));
      check(rows[0]!.length===1 && Object.keys(rows[0]![0]).sort().join(',')==='enabled,id,preampDb', 'setting is not a single scalar row');
      check(rows[1]!.length===10 && rows[1]!.every(row=>Object.keys(row).sort().join(',')==='band,gainDb'), 'band rows contain an identity or are missing');
      report.deviceEqRows={settings:rows[0]!.length,bands:rows[1]!.length};
    } finally { db.close(); }
  } finally { await store.close(); await new Promise<void>((resolve,reject)=>{const r=indexedDB.deleteDatabase(database);r.onsuccess=()=>resolve();r.onerror=()=>reject(r.error);}); }
  await qualifyBrowserDecoders(); report.qualifiedDecoderSkips = {...WEBCODECS_SKIP[browserEngine()]};
  // The actual renderer consumes a real generated FLAC fixture and reports only confirmed facts.
  // Silence the output; this is decoder/graph/clock evidence, never physical speaker/DAC proof.
  const bytes = new Uint8Array(await (await fetch(flacUrl)).arrayBuffer()), cache = new ByteCache();
  cache.put('browser-proof:0',bytes);
  const element = document.createElement('video'); element.muted = true;
  const snapshot:any = {session:{id:'browser-proof-session',generation:1,audio:{...flacPlan,version:2,mode:'direct',id:'browser-proof',url:flacUrl,bytes:bytes.length}},
    phase:'ready',intent:'paused',intentId:1,positionSeconds:0,audioEffects:{rendering:true,rate:1,settings:{gapless:false,crossfadeSeconds:0,normalization:'off'},notice:''}};
  let ready = false, failure = '';
  const service:any = {getSnapshot:()=>snapshot,ready:()=>{ready=true;},audioRenderFailed:(message:string)=>{failure=message;},
    audioEffectsNotice(){},audioRenderStatus(){},setDeviceAudioProcessing(){},engineProgress(){},fact(){},pause(){}};
  const render = new DecodeAudioRender(service,path=>path,element,()=>false,cache);
  // The facade used by both future entry points sends only committed local values to the engine.
  const unsubscribe = subscribeDeviceEqualizer(settings => render.applyEqualizer(settings));
  try {
    check(render.audioFacts() === undefined,'facts published before decoded renderer exists');
    render.apply(snapshot);
    const until=performance.now()+5000;
    while(!ready&&!failure&&performance.now()<until) await new Promise(resolve=>setTimeout(resolve,20));
    check(ready&&!failure,'actual FLAC renderer did not start: '+failure);
    const off=render.audioFacts()!;check(off.bitPerfect===false&&!off.eqApplied&&!off.levelingApplied&&off.sourceRate===44100&&off.deviceRate===null,'renderer invented output facts');
    await saveDeviceEqualizer({...flatEqualizer,enabled:true,preampDb:-3,bandsDb:[0,0,0,0,0,3,0,0,0,0]});
    check(render.audioFacts()!.eqApplied,'persisted choice never reached actual renderer');
    const current = render.getEqualizer();
    check(JSON.stringify(await loadDeviceEqualizer())===JSON.stringify(current),'player/Settings facade differs from the actual graph');
    const actualContext = (render as unknown as {context:AudioContext}).context;
    // An enabled preference cannot supply an applied fact. Read the actual current-song
    // normalization node after its gain ramp, including the unchanged/unknown cases.
    snapshot.audioEffects.settings.normalization = 'track'; render.apply(snapshot);
    check(!render.audioFacts()!.levelingApplied, 'unknown leveling gain was inferred from its preference');
    snapshot.session.audio.gain = {trackDb:0,trackPeak:0.5}; render.apply(snapshot);
    check(!render.audioFacts()!.levelingApplied, 'zero dB leveling was called a signal change');
    const advanceGainRamp = async () => {
      await actualContext.resume();
      const until = actualContext.currentTime + 0.04, deadline = performance.now() + 2000;
      while (actualContext.currentTime < until && performance.now() < deadline) await new Promise(resolve => setTimeout(resolve,10));
      check(actualContext.currentTime >= until, 'normalization sample clock did not advance');
    };
    snapshot.session.audio.gain = {trackDb:-6,trackPeak:0.5}; render.apply(snapshot);
    await advanceGainRamp();
    check(render.audioFacts()!.levelingApplied, 'actual attenuating gain node was not reported');
    snapshot.audioEffects.settings.normalization = 'off'; render.apply(snapshot);
    // Paused contexts do not advance scheduled automation. Observe off after the next actual
    // sample-clock advance, rather than treating a wall-clock sleep as a completed ramp.
    await advanceGainRamp();
    check(!render.audioFacts()!.levelingApplied, 'disabled leveling retained an applied fact after the ramp');
    await actualContext.suspend();
    report.currentSongLeveling = {unknown:false,zeroDb:false,attenuation:true,off:false};
    actualContext.dispatchEvent(new Event('sinkchange'));
    navigator.mediaDevices?.dispatchEvent(new Event('devicechange'));
    await new Promise(resolve=>setTimeout(resolve,0));
    check(render.getEqualizer()===current,'output notification reset or reloaded the EQ');
    await resetDeviceEqualizer();
    check(render.getEqualizer().enabled && render.getEqualizer().preampDb===0 && render.getEqualizer().bandsDb.every(gain=>gain===0),'shared reset did not reach the graph');
    check(render.audioFacts()!.eqApplied && render.audioFacts()!.bitPerfect===false,'flat but enabled EQ invented bit-perfect output');
    await saveDeviceEqualizer(flatEqualizer);check(!render.audioFacts()!.eqApplied,'EQ off did not reach graph');
    // Force the real graph's filter installation to fail. The same decoded song stays on
    // the dry path; saved intent stays on, while observed processing remains false.
    const createFilter = actualContext.createBiquadFilter.bind(actualContext);
    const nativeGraph = (render as unknown as {equalizer:BrowserEqualizer}).equalizer;
    nativeGraph.bypass(); actualContext.createBiquadFilter = () => { throw new Error('controlled filter failure'); };
    try {
      await saveDeviceEqualizer({...flatEqualizer,enabled:true});
      check(!render.audioFacts()!.eqApplied && !failure,'failed filter claimed EQ or interrupted playback');
      check((await loadDeviceEqualizer()).enabled,'an engine failure rewrote saved intent');
      report.failedFilterDryPlayback = true;
    } finally { actualContext.createBiquadFilter = createFilter; }
    await saveDeviceEqualizer(flatEqualizer);
    report.actualRendererFacts=render.audioFacts();
  } finally {
    unsubscribe(); render.dispose(); cache.clear();
    await new Promise<void>((resolve,reject)=>{const request=indexedDB.deleteDatabase(deviceEqualizerDatabase);request.onsuccess=()=>resolve();request.onerror=()=>reject(request.error);});
  }
  return report;
}
