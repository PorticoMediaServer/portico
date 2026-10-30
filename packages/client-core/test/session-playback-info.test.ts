import test from 'node:test';
import assert from 'node:assert/strict';
import {SessionsClient} from '../src/playback-v1/sessions.ts';
import {parsePlaybackInfo} from '../src/playback-v1/playback-info.ts';
import type {V1Http} from '../src/playback-v1/http.ts';
import {routePath} from '../../contracts/src/routes.ts';

const info=()=>({route:'converted',routes:{picture:'converted',sound:'converted'},reason:{kind:'format',audioCodec:'truehd'},
 source:{video:{codec:'hevc',width:3840,height:2160,interlaced:false,dynamicRange:'dolby_vision',dolbyVisionProfile:7,dolbyVisionEnhancementType:'FEL',dolbyVisionRpu:true,dolbyVisionBaseLayer:true,hdr10Plus:true},audio:{codec:'truehd',variant:'truehd_atmos',objectAudio:'atmos',channels:8,layout:'7.1',sampleRate:96000,bitDepth:24}},
 delivered:{video:{codec:'hevc',dynamicRange:'dolby_vision',dolbyVisionProfile:8,dolbyVisionCompatibility:1,conversion:{kind:'profile7_to_81',sourceDolbyVisionProfile:7,sourceEnhancementType:'FEL',enhancementDiscarded:true,felResidualDiscarded:true}},audio:{codec:'flac',channels:8,layout:'7.1',sampleRate:96000,bitDepth:24}},
 losses:[{feature:'atmos',replacement:{audioCodec:'flac',channels:8,layout:'7.1'}}],network:'home',basis:'committed_delivery_plan'});
const resource=(playbackInfo?:unknown)=>({id:'session',revision:'1',kind:'vod',role:'local',state:'playing',presentation:{generation:1,mode:'direct',url:'/v1/media/current',startPositionMs:0,subtitles:[],decision:{}},...(playbackInfo===undefined?{}:{playbackInfo})});
function wire(raw:unknown){const paths:string[]=[];const http:V1Http={send:async request=>{paths.push(request.path);return {status:200,headers:{},body:JSON.parse(JSON.stringify(raw))};}};return {client:new SessionsClient(http),paths};}

test('SessionsClient reads exact sparse source/delivery comparison independently of output observations',async()=>{
 const raw={...resource({...info(),privateUrl:'https://private.invalid?token=secret'}),observedPlayback:{audio:{handling:'decoded',channels:2,sampleRate:48000}}};
 const {client,paths}=wire(raw);const session=await client.get('session');
 assert.deepEqual(paths,[routePath('get_playback_session',{id:'session'})]);
 assert.deepEqual(session.playbackInfo,info());
 assert.equal(session.playbackInfo?.source.video?.interlaced,false);
 assert.equal(session.playbackInfo?.source.video?.dolbyVisionRpu,true);
 assert.equal(session.playbackInfo?.source.video?.dolbyVisionBaseLayer,true);
 assert.equal(session.playbackInfo?.delivered.video?.hdr10Plus,undefined);
 assert.equal(session.playbackInfo?.delivered.audio?.sampleRate,96000);
 assert.equal(session.observedPlayback?.audio?.sampleRate,48000);
 assert.equal(session.observedPlayback?.screen,undefined);
 assert.equal(session.playbackInfo?.estimatedBitrateKbps,undefined);
 assert.ok(Object.isFrozen(session.playbackInfo)&&Object.isFrozen(session.playbackInfo?.source.video)&&Object.isFrozen(session.playbackInfo?.losses[0]?.replacement));
 assert.ok(!JSON.stringify(session.playbackInfo).includes('secret'));
});

test('missing, ended, non-VOD and malformed comparisons never poison valid session authority',async()=>{
 for(const playbackInfo of [undefined,null,[],{}, {...info(),route:'original'}, {...info(),losses:Array(9).fill({})}, {...info(),source:{video:{codec:'hevc',interlaced:null}}}, {...info(),delivered:{audio:{codec:'flac',sampleRate:-1}}}]) {
  const session=await wire(resource(playbackInfo)).client.get('session');assert.equal(session.id,'session');assert.equal(session.revision,'1');assert.equal(session.playbackInfo,undefined);
 }
 assert.equal((await wire({...resource(info()),state:'ended'}).client.get('session')).playbackInfo,undefined);
 assert.equal((await wire({...resource(info()),kind:'audio'}).client.get('session')).playbackInfo,undefined);
 const noReport=await wire({...resource(),presentation:{...resource().presentation,generation:2}}).client.get('session');
 assert.equal(noReport.playbackInfo,undefined);
 assert.throws(()=>parsePlaybackInfo({...info(),basis:'capability_guess'}));
});

test('explicit negative RPU/base observations remain negative and missing ones remain unknown',()=>{
 const raw=info();
 const negative=parsePlaybackInfo({...raw,source:{video:{codec:'hevc',dolbyVisionProfile:7,dolbyVisionRpu:false,dolbyVisionBaseLayer:false}}});
 assert.equal(negative?.source.video?.dolbyVisionRpu,false);
 assert.equal(negative?.source.video?.dolbyVisionBaseLayer,false);
 assert.equal(parsePlaybackInfo({...raw,source:{video:{codec:'hevc',dolbyVisionProfile:7}}})?.source.video?.dolbyVisionRpu,undefined);
});
