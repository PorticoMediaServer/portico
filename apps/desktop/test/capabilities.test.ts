import {test} from 'node:test';
import assert from 'node:assert/strict';
import {desktopCapabilities} from '../src/capabilities.ts';
test('canonical PCM requires the complete native integer/float decoder family, without output grants',()=>{
 const required=['pcm_u8','pcm_s16le','pcm_s16be','pcm_s24le','pcm_s24be','pcm_s32le','pcm_s32be','pcm_f32le','pcm_f32be','pcm_f64le','pcm_f64be'];
 for(const missing of [undefined,...required]){
  const profile=desktopCapabilities({version:'mpv 0.41.0',decoders:['flac',...required.filter(codec=>codec!==missing)]},'linux','0.1.0');
  assert.equal(profile.engines.native.audio?.some(value=>value.codec==='pcm'),missing===undefined);
  assert.equal(profile.engines.native.transports?.[0]?.audio?.includes('pcm'),missing===undefined);
  assert.equal(profile.audioOutput,undefined);
  assert.doesNotMatch(JSON.stringify(profile.engines.native.audio?.find(value=>value.codec==='pcm')??{}),/lossless|bitPerfect|sampleRate|bitDepth|objectAudio/);
 }
});
test('native decoder inventory never grants browser, hardware, HDR, object audio or passthrough facts',()=>{
 const document=desktopCapabilities({version:'mpv 0.41.0',decoders:['h264','hevc','aac','flac','dca','truehd'],path:'/private'},'linux','0.1.0');
 const engine=document.engines.native;
 assert.deepEqual(engine.video?.map(value=>value.codec),['h264','hevc']);assert.deepEqual(engine.audio?.map(value=>value.codec),['aac','flac','dts','truehd'],'N6-1: base DTS and TrueHD from their decoders');
 assert.deepEqual(engine.video?.map(value=>value.dynamicRanges),[['sdr'],['sdr']]);assert.deepEqual(engine.audioPassthrough,[]);assert.equal(document.audioOutput,undefined);assert.equal(document.display,undefined);
 assert.deepEqual(engine.transports?.map(value=>value.transport),['direct']);assert.deepEqual(engine.subtitleRendering,[]);assert.equal(engine.toneMapsHdr,false);
 assert.doesNotMatch(JSON.stringify(document),/path|private|maxWidth|maxHeight|sampleRate|atmos|dtsx|dts_hd|dolby_vision|hdr10/);
});
test('native missing, malformed or unreported inventory cannot become a declared fallback',()=>{
 for(const value of [null,{},[],{version:'mpv 0.41.0',decoders:[]},{version:'mpv /private',decoders:['aac']},{version:'mpv 0.41.0',decoders:['../aac'] }])assert.throws(()=>desktopCapabilities(value,'macos','0.1.0'),/engine_unavailable/);
});
test('original WavPack requires the actual decoder; it supplies no losslessness or output/hardware facts',()=>{
 for(const present of [false,true]){
  const profile=desktopCapabilities({version:'mpv 0.41.0',decoders:['flac',...(present?['wavpack']:[])]},'linux','0.1.0');
  const native=profile.engines.native;
  assert.equal(native.audio?.some(a=>a.codec==='wavpack'),present);
  assert.equal(native.transports?.[0]?.containers?.includes('wv'),present);
  assert.equal(native.transports?.[0]?.audio?.includes('wavpack'),present);
  assert.equal(profile.audioOutput,undefined);
  assert.doesNotMatch(JSON.stringify(native.audio?.find(a=>a.codec==='wavpack')??{}),/lossless|bitPerfect|sampleRate|output|passthrough|objectAudio/);
 }
});
test('N6-1: AC-3, E-AC-3, DTS and TrueHD are declared only from their actual decoders, with no variant list',()=>{
 const profile=desktopCapabilities({version:'mpv 0.41.0',decoders:['h264','aac','ac3','eac3','dca','truehd']},'windows','0.1.0'),native=profile.engines.native;
 // No variants: the server's default rule refuses DTS-HD/DTS:X and admits TrueHD Atmos and E-AC-3 JOC
 // as decoded beds (acceptsAudioVariant); an explicit list would be an exact allow-list.
 assert.deepEqual(native.audio?.filter(value=>['ac3','eac3','dts','truehd'].includes(value.codec)),[
  {codec:'ac3',evidence:'probed'},{codec:'eac3',evidence:'probed'},{codec:'dts',evidence:'probed'},{codec:'truehd',evidence:'probed'}]);
 assert.deepEqual(native.transports?.[0]?.audio?.filter(codec=>['ac3','eac3','dts','truehd'].includes(codec)),['ac3','eac3','dts','truehd'],'the direct transport carries them');
 assert.deepEqual(native.audioPassthrough,[],'nothing is passed through, so object audio is only ever the decoded bed');
 assert.doesNotMatch(JSON.stringify(profile),/variants|atmos|dtsx|objectAudio/,'no variant list and no object-audio claim');
 assert.equal(desktopCapabilities({version:'mpv 0.41.0',decoders:['aac']},'linux','0.1.0').engines.native.audio?.some(value=>value.codec==='ac3'),false,'no decoder, no claim');
});
