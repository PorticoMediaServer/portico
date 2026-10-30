import test from 'node:test';
import assert from 'node:assert/strict';
import {appleDeclaredProfile,appleProfile,capabilityDocumentProblem,probeWebProfile,type AppleNativeFacts,type CapabilityDocument} from '../src/client-profile.ts';

const audioIn=(document:CapabilityDocument,transport:'direct'|'hls_fmp4',container:string)=>document.engines.native.transports!.find(value=>value.transport===transport&&value.containers?.includes(container))?.audio??[];

test('Apple native video stays in the conservative profile and 4:2:0 families',()=>{
 for(const document of [appleDeclaredProfile({television:true}),appleProfile({television:false},{hardwareDecode:{hevc:true,av1:true}})]){
  assert.equal(capabilityDocumentProblem(document),null);
  const video=document.engines.native.video!;
  assert.deepEqual(video.find(value=>value.codec==='h264')!.profiles,['baseline','constrained_baseline','main','high']);
  assert.deepEqual(video.find(value=>value.codec==='hevc')!.profiles,['main','main_10']);
  if(video.some(value=>value.codec==='av1'))assert.deepEqual(video.find(value=>value.codec==='av1')!.profiles,['main']);
  for(const codec of video)assert.deepEqual(codec.chromaSubsampling,['4:2:0']);
 }
});

test('Apple HDR10+ keeps parser, hardware and current display evidence separate',()=>{
 const facts:AppleNativeFacts={hardwareDecode:{hevc:true},videoEligibility:{hdr10Plus:true},hdrModes:{hdr10:true,hdr10Plus:true},eligibleForHDRPlayback:true};
 const has=(value:AppleNativeFacts)=>{
  const document=appleProfile({television:true},value);
  assert.equal(capabilityDocumentProblem(document),null);
  return {decoded:document.engines.native.video!.find(value=>value.codec==='hevc')!.dynamicRanges!.includes('hdr10plus'),displayed:document.display!.dynamicRanges!.includes('hdr10plus')};
 };
 assert.deepEqual(has(facts),{decoded:true,displayed:true});
 assert.deepEqual(has({...facts,eligibleForHDRPlayback:false}),{decoded:true,displayed:false});
 assert.deepEqual(has({...facts,hdrModes:{hdr10:true}}),{decoded:true,displayed:false});
 assert.deepEqual(has({...facts,videoEligibility:{}}),{decoded:false,displayed:false});
 assert.deepEqual(has({...facts,hardwareDecode:{hevc:false}}),{decoded:false,displayed:false});
 assert.deepEqual(has({model:'AppleTV14,1',hdrModes:{hdr10:true}}),{decoded:false,displayed:false});
});

test('Ogg/WebM Opus and standalone FLAC cannot grant direct MP4 or fMP4',async()=>{
 const document=await probeWebProfile({
  canPlayType:type=>type==='audio/flac'||type.startsWith('audio/ogg;')||type==='audio/webm; codecs="opus"'?'probably':'',
  mediaSourceSupports:type=>type==='audio/webm; codecs="opus"',
 });
 for(const codec of ['opus','flac']){
  assert.ok(document.engines.native.audio!.some(value=>value.codec===codec));
  assert.equal(audioIn(document,'direct','mp4').includes(codec),false);
  assert.equal(audioIn(document,'hls_fmp4','fmp4').includes(codec),false);
 }
 assert.ok(audioIn(document,'direct','ogg').includes('opus'));
 assert.ok(audioIn(document,'direct','flac').includes('flac'));
});

test('MP4 element and media-source answers grant only their matching transport',async()=>{
 for(const mode of ['element','source','both'] as const){
  const mp4=(type:string)=>type==='audio/mp4; codecs="opus"'||type==='audio/mp4; codecs="fLaC"';
  const document=await probeWebProfile({canPlayType:type=>mode!=='source'&&mp4(type)?'probably':'',mediaSourceSupports:type=>mode!=='element'&&mp4(type)});
  for(const codec of ['opus','flac']){
   assert.ok(document.engines.native.audio!.some(value=>value.codec===codec));
   assert.equal(audioIn(document,'direct','mp4').includes(codec),mode!=='source');
   assert.equal(audioIn(document,'hls_fmp4','fmp4').includes(codec),mode!=='element');
   assert.equal(audioIn(document,'direct',codec==='flac'?'flac':'ogg').includes(codec),false,'MP4-only evidence does not grant standalone/Ogg playback');
  }
 }
});

test('native HLS uses explicit MP4 FLAC evidence, never the standalone file answer',async()=>{
 for(const supported of [false,true]){
  const document=await probeWebProfile({canPlayType:type=>type==='application/vnd.apple.mpegurl'||type==='audio/flac'||supported&&type==='audio/mp4; codecs="flac"'?'probably':'',mediaSourceSupports:null});
  assert.equal(audioIn(document,'hls_fmp4','fmp4').includes('flac'),supported);
 }
});
