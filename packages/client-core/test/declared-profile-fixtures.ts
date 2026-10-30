import {withMpvEngine,webDeclaredProfile,appleDeclaredProfile,androidDeclaredProfile,fireTvDeclaredProfile,vegaDeclaredProfile,webosDeclaredProfile,tizenDeclaredProfile,castDeclaredProfile,appleProfile,androidProfile,probeWebProfile} from '../src/client-profile.ts';
import type {AppleNativeFacts} from '../src/client-profile.ts';

// Synthetic probe responses exercise the production merge functions. These are
// acceptance fixtures, not a claim that physical devices were verified here.
const chromiumTypes = /avc1\.640028|av01|vp09|mp4a|audio\/mpeg|flac|fLaC|opus|vorbis/;
const safariTypes = /avc1\.640028|hvc1|mp4a|audio\/mpeg|ac-3|ec-3|flac|fLaC|alac|mpegurl/;
const chromium = await probeWebProfile({
 canPlayType: type => chromiumTypes.test(type) ? 'probably' : '',
 mediaSourceSupports: type => chromiumTypes.test(type),
 decodingInfo: async q => ({supported:!q.transferFunction,smooth:!q.transferFunction,powerEfficient:true}),
 audioOutputChannels: 2, identity: {family:'web',engine:'chromium'},
});
const safariHDR = await probeWebProfile({
 canPlayType: type => safariTypes.test(type) ? 'probably' : '',
 mediaSourceSupports: null,
 decodingInfo: async () => ({supported:true,smooth:true,powerEfficient:true}),
 matchMedia: () => true, audioOutputChannels:6, embeddedAudioTracks:true,
 identity: {family:'web',engine:'safari'},
});
// These positive parser/route responses are explicit synthetic observations.
// Hardware decoding or a Dolby Vision display alone supplies neither grant.
export const appleTVRuntimeFacts: AppleNativeFacts = {
 hardwareDecode:{hevc:true,dolbyVision:true,av1:false},
 videoEligibility:{dolbyVision5:true,dolbyVision81:true},
 hdrModes:{hdr10:true,hlg:true,dolbyVision:true},eligibleForHDRPlayback:true,
 screen:{width:3840,height:2160,maxFrameRate:60},
 audio:{maxOutputChannels:8,route:'hdmi',spatial:true,eac3JocEligible:true},
};
export const declaredProfiles={
 web:webDeclaredProfile(),ios:appleDeclaredProfile({television:false}),tvos:appleDeclaredProfile({television:true}),
 android:androidDeclaredProfile(),androidtv:androidDeclaredProfile({television:true}),firetv:fireTvDeclaredProfile(),
 vega:vegaDeclaredProfile(),webos:webosDeclaredProfile(),tizen:tizenDeclaredProfile(),
 chromecast:castDeclaredProfile('chromecast'),ultra:castDeclaredProfile('chromecast_ultra'),google_tv:castDeclaredProfile('google_tv_4k'),
 chromium, safari_hdr:safariHDR,
 appletv_dv:appleProfile({television:true},appleTVRuntimeFacts),
 shield_avr:androidProfile({television:true},{
  decoders:[
   {codec:'h264',maxWidth:3840,maxHeight:2160,maxFrameRate:60,maxLevel:52},
   {codec:'hevc',maxWidth:3840,maxHeight:2160,maxFrameRate:60,tenBit:true,hdr:['hdr10','dolby_vision'],dolbyVisionProfiles:[5,8]},
   {codec:'vp9',maxWidth:3840,maxHeight:2160,maxFrameRate:60,tenBit:true},
   {codec:'mpeg2video',maxWidth:1920,maxHeight:1080,maxFrameRate:60},
   {codec:'vc1',maxWidth:1920,maxHeight:1080,maxFrameRate:60},
  ], displayHdr:['hdr10','dolby_vision'],
  passthrough:[{codec:'ac3',maxChannels:6},{codec:'eac3',maxChannels:8,objectAudio:['atmos']},{codec:'dts',maxChannels:8,objectAudio:['dtsx'],variants:['dts_core','dts_hd_hra','dts_hd_ma','dtsx']},{codec:'truehd',maxChannels:8,objectAudio:['atmos']}],
  audio:{maxOutputChannels:8,route:'hdmi'},
 }),
 // The same Apple TV and Shield with the mpv engine beside the native one.
 appletv_mpv:withMpvEngine(appleProfile({television:true},appleTVRuntimeFacts)),
 android_mpv:withMpvEngine(androidProfile({},{decoders:[{codec:'h264',maxWidth:1920,maxHeight:1080,maxFrameRate:60},{codec:'hevc',maxWidth:3840,maxHeight:2160,maxFrameRate:60,tenBit:true,hdr:['hdr10']}],audioDecoders:[{codec:'aac',maxChannels:8},{codec:'mp3'},{codec:'opus'},{codec:'flac'}],displayHdr:[],audio:{maxOutputChannels:2,route:'speaker'}})),
};
