import test from 'node:test';
import assert from 'node:assert/strict';
import {playbackInfoProjection,reconciledPlaybackLosses,type PlaybackInfoWords} from '../src/playback-info-projection.ts';
import {parsePlaybackInfo} from '../src/playback-v1/playback-info.ts';

const raw=()=>({route:'converted',routes:{picture:'converted',sound:'converted',subtitles:'original'},reason:{kind:'format'},
 source:{video:{container:'matroska',codec:'hevc',profile:'Main 10',width:3840,height:2160,bitDepth:10,frameRateNumerator:24000,frameRateDenominator:1001,dynamicRange:'dolby_vision',dolbyVisionProfile:7,dolbyVisionEnhancementType:'FEL'},audio:{codec:'truehd',variant:'truehd_atmos',objectAudio:'atmos',channels:8,layout:'7.1',sampleRate:96000,bitDepth:24},subtitles:{format:'ass',language:'ja',origin:'sidecar'}},
 delivered:{video:{container:'fmp4_hls',codec:'hevc',profile:'Main 10',width:3840,height:2160,bitDepth:10,frameRateNumerator:24000,frameRateDenominator:1001,dynamicRange:'dolby_vision',dolbyVisionProfile:8,dolbyVisionCompatibility:1,conversion:{kind:'profile7_to_81',sourceDolbyVisionProfile:7,sourceEnhancementType:'FEL',enhancementDiscarded:true,felResidualDiscarded:true}},audio:{codec:'flac',channels:8,layout:'7.1',sampleRate:96000,bitDepth:24},subtitles:{format:'ass',language:'ja',origin:'sidecar',rendering:'drawnByDevice'}},
 losses:[{feature:'atmos',replacement:{audioCodec:'flac',channels:8,layout:'7.1'}}],network:'home',estimatedBitrateKbps:3000,basis:'committed_delivery_plan'});
const words:PlaybackInfoWords={locale:'en-US',standardRange:'SDR',tracks:{fourK:'4K',stereo:'Stereo',mono:'Mono',channels:n=>`${n} channels`,withAtmos:s=>`${s} with Atmos`,withDtsx:s=>`${s} (DTS:X)`,besideFile:s=>`${s}, beside the file`,language:s=>s},
 hls:s=>`${s} (HLS)`,bitDepth:n=>`${n}-bit`,frameRate:n=>`${n} fps`,sampleRate:n=>`${n} kHz`,dolbyVision:(profile,el)=>['Dolby Vision',profile,el].filter(Boolean).join(' '),convertedFromDolbyVision:(name,profile,el)=>`${name} from ${profile} ${el??''}`,reason:r=>r.kind,loss:l=>`${l.feature} lost`,network:(n,kbps)=>`${n}${kbps?` ${kbps} kbps`:''}`,screen:s=>`${s.dynamicRange??''} ${s.refreshRateHz??''}`,output:a=>`${a.handling??''} ${a.codec??''} ${a.sampleRate??''}`,shown:s=>s};

test('one projection keeps committed bitstream facts and renderer output in separate rows',()=>{
 const info=parsePlaybackInfo(raw());assert.ok(info);
 const model=playbackInfoProjection(info,{audio:{handling:'decoded',codec:'pcm',channels:2,sampleRate:48000},screen:{dynamicRange:'hdr10',refreshRateHz:59.94},subtitles:{rendering:'drawnByDevice'},deliveredBitrateKbps:4100},words);
 const picture=model.sections.find(s=>s.kind==='picture'),sound=model.sections.find(s=>s.kind==='sound'),subtitle=model.sections.find(s=>s.kind==='subtitles');
 assert.equal(model.route,'converted');assert.equal(model.networkAndBitrate,'home 4100 kbps');
 assert.equal(picture?.rows.find(r=>r.field==='container')?.source,'MKV');
 assert.equal(picture?.rows.find(r=>r.field==='container')?.delivered,'MP4 (HLS)');
 assert.equal(picture?.rows.find(r=>r.field==='hdr')?.delivered,'Dolby Vision 8.1 from 7 FEL');
 assert.equal(picture?.rows.find(r=>r.field==='frameRate')?.source,'23.976 fps');
 assert.equal(picture?.rows.find(r=>r.field==='screen')?.delivered,'hdr10 59.94');
 assert.equal(sound?.rows.find(r=>r.field==='format')?.source,'Dolby TrueHD with Atmos');
 assert.equal(sound?.rows.find(r=>r.field==='format')?.delivered,'FLAC');
 assert.equal(sound?.rows.find(r=>r.field==='sampleRate')?.delivered,'96 kHz');
 assert.equal(sound?.rows.find(r=>r.field==='output')?.delivered,'decoded pcm 48000');
 assert.deepEqual(sound?.losses,['atmos lost']);
 assert.equal(subtitle?.rows.find(r=>r.field==='track')?.source,'ja, ASS, beside the file');
 assert.equal(subtitle?.rows.find(r=>r.field==='shown')?.delivered,'drawnByDevice');
 assert.ok(Object.isFrozen(model)&&Object.isFrozen(model.sections)&&Object.isFrozen(sound?.rows[0]));
});

test('unknown output is omitted, estimates never impersonate measured delivery, and channel counts never become layouts',()=>{
 const value=raw();const info=parsePlaybackInfo({...value,source:{video:{codec:'hevc',height:1080},audio:{codec:'eac3',channels:8}},delivered:{video:{codec:'hevc',height:1080},audio:{codec:'flac',channels:8}},losses:[]});assert.ok(info);
 const model=playbackInfoProjection(info,undefined,words);
 assert.equal(model.networkAndBitrate,'home');
 const picture=model.sections.find(s=>s.kind==='picture'),sound=model.sections.find(s=>s.kind==='sound');
 assert.equal(picture?.rows.find(r=>r.field==='resolution'),undefined);
 assert.equal(picture?.rows.find(r=>r.field==='screen'),undefined);
 assert.equal(sound?.rows.find(r=>r.field==='output'),undefined);
 assert.equal(sound?.rows.find(r=>r.field==='sampleRate'),undefined);
 assert.equal(sound?.rows.find(r=>r.field==='channels')?.source,'8 channels');
 assert.equal(sound?.rows.find(r=>r.field==='format')?.source,'Dolby Digital Plus');
 assert.deepEqual(sound?.losses,[]);
 assert.equal(model.sections.find(s=>s.kind==='subtitles')?.rows.find(r=>r.field==='shown'),undefined);
 assert.ok(!JSON.stringify(model).includes('3000'));
});


test('observed copied passthrough clears false object-audio loss without hiding conversion losses',()=>{
 for(const feature of ['atmos','dtsx'] as const){
  const codec=feature==='atmos'?'eac3':'dts';
  const info=parsePlaybackInfo({route:'repackaged',routes:{sound:'repackaged'},reason:{kind:'container'},source:{audio:{codec,objectAudio:feature}},delivered:{audio:{codec,delivery:'decoded'}},losses:[{feature,replacement:{audioCodec:codec}}],network:'home',basis:'committed_delivery_plan'});assert.ok(info);
  const losses=(observed:Parameters<typeof playbackInfoProjection>[1],current=info)=>playbackInfoProjection(current,observed,words).sections[0]!.losses;
  assert.deepEqual(losses({audio:{handling:'passthrough',codec}}),feature==='atmos'?[]:[`${feature} lost`]);
  if(feature==='dtsx')assert.deepEqual(losses({audio:{handling:'passthrough',codec:'dts_hd'}}),[`${feature} lost`]);
  assert.deepEqual(losses({audio:{handling:'passthrough'}}),[`${feature} lost`]);
  assert.deepEqual(losses({audio:{handling:'passthrough',codec,objectAudio:feature}}),[]);
  assert.deepEqual(losses({audio:{handling:'decoded',objectAudio:feature}}),[]);
  assert.deepEqual(losses(undefined),[`${feature} lost`]);
  assert.deepEqual(losses({audio:{handling:'decoded',codec}}),[`${feature} lost`]);
  assert.deepEqual(losses({audio:{handling:'pcm',codec:'pcm'}}),[`${feature} lost`]);
  assert.deepEqual(losses({audio:{handling:'passthrough',codec:'aac'}}),[`${feature} lost`]);
  const converted={...info,route:'converted' as const,routes:{sound:'converted' as const}};
  assert.deepEqual(losses({audio:{handling:'passthrough',codec}},converted),[`${feature} lost`]);
  assert.deepEqual(info.losses.map(loss=>loss.feature),[feature],'the committed server document is not mutated');
 }
});

test('a copied codec with a different delivered variant or profile retains loss without object observation',()=>{
 for(const field of ['profile','variant'] as const){
  const info=parsePlaybackInfo({route:'repackaged',routes:{sound:'repackaged'},reason:{kind:'container'},source:{audio:{codec:'eac3',objectAudio:'atmos',[field]:'eac3_joc'}},delivered:{audio:{codec:'eac3',delivery:'decoded',[field]:'eac3'}},losses:[{feature:'atmos',replacement:{audioCodec:'eac3'}}],network:'home',basis:'committed_delivery_plan'});assert.ok(info);
  assert.deepEqual(playbackInfoProjection(info,{audio:{handling:'passthrough',codec:'eac3'}},words).sections[0]!.losses,['atmos lost']);
  assert.deepEqual(playbackInfoProjection(info,{audio:{handling:'passthrough',codec:'eac3',objectAudio:'atmos'}},words).sections[0]!.losses,[]);
 }
});

test('the exported reconciliation is what the comparison shows, and keeps every other loss',()=>{
 const info=parsePlaybackInfo({route:'repackaged',routes:{sound:'repackaged'},reason:{kind:'container'},source:{audio:{codec:'eac3',objectAudio:'atmos'}},delivered:{audio:{codec:'eac3',delivery:'decoded'}},losses:[{feature:'atmos',replacement:{audioCodec:'eac3'}},{feature:'hdr',replacement:{dynamicRange:'sdr'}}],network:'home',basis:'committed_delivery_plan'});assert.ok(info);
 assert.deepEqual(reconciledPlaybackLosses(info,{audio:{handling:'passthrough',codec:'eac3'}}).map(loss=>loss.feature),['hdr']);
 assert.deepEqual(reconciledPlaybackLosses(info,undefined).map(loss=>loss.feature),['atmos','hdr']);
 assert.deepEqual(reconciledPlaybackLosses({...info,routes:{}},{audio:{handling:'passthrough',codec:'eac3'}}).map(loss=>loss.feature),['atmos','hdr'],'no sound route: nothing is reconciled');
});
