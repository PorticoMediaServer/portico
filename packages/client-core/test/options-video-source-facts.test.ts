import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../src/index.ts';
import {localV1Http} from '../src/playback-v1/local-http.ts';
import {PlaybackOptionsClient} from '../src/playback-v1/options.ts';
import {routePath} from '../../contracts/src/routes.ts';

async function read(facts: Record<string,unknown>) {
  const requests: string[] = [];
  const api = new HttpLocalApi('https://server.example','',async(input,init)=>{
    requests.push(new URL(String(input)).pathname);
    assert.equal(init?.method,'GET');
    return new Response(JSON.stringify({itemId:'movie',kind:'movie',versions:[{id:'version',video:[{id:'v0',codec:'hevc',...facts}]}]}),{status:200,headers:{'Content-Type':'application/json'}});
  });
  const options = await new PlaybackOptionsClient(localV1Http(api)).options('movie');
  assert.deepEqual(requests,[routePath('get_playback_options',{itemId:'movie'})]);
  return {options,video:options.versions[0]!.video[0]!};
}
const technical = {level:153,chromaSubsampling:'4:2:0',frameRateNumerator:24000,frameRateDenominator:1001,frameRateMode:'variable',
  dolbyVisionProfile:7,dolbyVisionCompatibility:6,dolbyVisionRpu:true,dolbyVisionBaseLayer:true,dolbyVisionEnhancementType:'FEL',dolbyVisionCmVersion:'4.0',masterDisplay:'G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50)',maxCll:'1000,400'};

test('OptionsClient preserves canonical actual DV, mastering, chroma and rational frame facts through JSON',async()=>{
  const {video,options} = await read(technical);
  for(const [key,value] of Object.entries(technical)) assert.equal(Reflect.get(video,key),value,key);
  assert.ok(Object.isFrozen(video));
  assert.equal(video.hdr,undefined);
  assert.equal(video.hdr10Plus,undefined);
  assert.equal(options.plan,undefined);
});

test('source profiles and explicit false metadata flags do not fabricate RPU, BL, HDR or a playback plan',async()=>{
  for(const profile of [4,5,7,8,9,10]) {
    const {video,options} = await read({dolbyVisionProfile:profile,dolbyVisionCompatibility:0,dolbyVisionRpu:false,dolbyVisionBaseLayer:false});
    assert.equal(video.dolbyVisionProfile,profile);
    assert.equal(video.dolbyVisionCompatibility,0);
    assert.equal(video.dolbyVisionRpu,false);
    assert.equal(video.dolbyVisionBaseLayer,false);
    assert.equal(video.hdr,undefined);
    assert.equal(video.dolbyVisionEnhancementType,undefined);
    assert.equal(options.plan,undefined);
  }
});

test('unknown or malformed source fields remain absent without profile/codec/name inference',async()=>{
  for(const value of [undefined,null,[],{}]) {
    const facts = Object.fromEntries(Object.keys(technical).map(key=>[key,value]));
    const {video} = await read(facts);
    for(const key of Object.keys(technical)) assert.equal(Object.hasOwn(video,key),false,key);
  }
  const {video} = await read({level:-1,frameRateNumerator:0,frameRateDenominator:0,dolbyVisionProfile:256,dolbyVisionCompatibility:16,title:'Dolby Vision FEL HDR10+'});
  for(const key of Object.keys(technical)) assert.equal(Object.hasOwn(video,key),false,key);
  assert.equal(video.hdr,undefined);
});
