import {invoke} from '@tauri-apps/api/core';
import {capabilityDocumentProblem,type CapabilityDocument} from '../../../packages/client-core/src/client-profile.ts';

/** Only the actual installed libmpv catalog enters this profile. No WebView probe,
 * hardware ceilings, output rate, HDR, DTS-HD or Atmos grant comes from its name. */
export function desktopCapabilities(raw:unknown,os:'linux'|'macos'|'windows',appVersion:string):CapabilityDocument {
  if(!raw||typeof raw!=='object'||Array.isArray(raw))throw new Error('engine_unavailable');
  const value=raw as Record<string,unknown>;
  if(!Array.isArray(value.decoders)||!value.decoders.length||value.decoders.length>4096||value.decoders.some(codec=>typeof codec!=='string'||!/^[a-z0-9_.+-]{1,64}$/.test(codec)))throw new Error('engine_unavailable');
  if(typeof value.version!=='string'||value.version.length>64||!/^[A-Za-z0-9_.+ -]+$/.test(value.version))throw new Error('engine_unavailable');
  const codecs=new Set(value.decoders);
  const video=['h264','hevc','av1','vp9','vp8','mpeg2video','mpeg4','vc1','wmv3','theora','ffv1'].filter(codec=>codecs.has(codec)).map(codec=>({codec,dynamicRanges:['sdr' as const],evidence:'probed' as const}));
  // Canonical pcm covers the ordinary WAV/AIFF integer and float encodings;
  // one decoder or a demuxer/container name alone cannot admit that family.
  const pcm=['pcm_u8','pcm_s16le','pcm_s16be','pcm_s24le','pcm_s24be','pcm_s32le','pcm_s32be','pcm_f32le','pcm_f32be','pcm_f64le','pcm_f64be'].every(codec=>codecs.has(codec));
  const audio=[...['aac','mp3','mp2','flac','alac','opus','vorbis','wmav2','wmapro','wavpack'].filter(codec=>codecs.has(codec)),...(pcm?['pcm']:[])].map(codec=>({codec,evidence:'probed' as const}));
  const profile:CapabilityDocument={version:2,client:{family:'desktop',form:'desktop',platform:os,os,app:'Portico',appVersion},engineSetting:'native',
    engines:{native:{name:'mpv',version:value.version,evidence:'probed',video,audio,audioPassthrough:[],
      transports:[{transport:'direct',containers:['mkv','mp4','m4v','mov','webm','avi','ts','m2ts','mpg','mpeg','wmv','asf','flv','mp3','m4a','m4b','aac','flac','ogg','opus','wav','aiff','wma','mka',...(codecs.has('wavpack')?['wv']:[])],video:video.map(v=>v.codec),audio:audio.map(a=>a.codec)}],
      // Extracted subtitle broker and streaming manifests are not installed.
      subtitleRendering:[],toneMapsHdr:false,embeddedAudioSwitching:false,liveChannels:false}},audioDecode:[]};
  if(capabilityDocumentProblem(profile))throw new Error('engine_unavailable');
  return profile;
}
export const nativeCapabilities=(os:'linux'|'macos'|'windows',appVersion:string)=>invoke<unknown>('desktop_capabilities').then(raw=>desktopCapabilities(raw,os,appVersion));
