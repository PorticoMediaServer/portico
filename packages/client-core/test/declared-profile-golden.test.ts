import test from 'node:test';import assert from 'node:assert/strict';import {existsSync,readFileSync,writeFileSync} from 'node:fs';
import {declaredProfiles,appleTVRuntimeFacts} from './declared-profile-fixtures.ts';
import {appleProfile,capabilityDocumentProblem,withMpvEngine} from '../src/client-profile.ts';
// The server keeps one recorded copy of every table here, with the routes it expects for each
// (server-next/internal/streaming/profiles). This holds the copy equal to the table, so what an
// app sends and what the server's cases were written against cannot drift apart.
// WRITE_PROFILES=1 rewrites the copies from the tables, and adds the file for a new table.
test('the server\'s recorded profiles are exactly the declared client tables',()=>{
 const folder=new URL('../../../server-next/internal/streaming/profiles/',import.meta.url);
 for(const [name,profile] of Object.entries(declaredProfiles)){
  assert.equal(capabilityDocumentProblem(profile),null,name);
  const file=new URL(name+'.json',folder),document=JSON.parse(JSON.stringify(profile));
  if(process.env.WRITE_PROFILES){
   const source=existsSync(file)?JSON.parse(readFileSync(file,'utf8')).source:'app table: written from documentation, not measured on a device';
   writeFileSync(file,JSON.stringify({source,document},null,1)+'\n');
  }
  assert.deepEqual(JSON.parse(readFileSync(file,'utf8')).document,document,name);
 }
});
test('synthetic Apple positives require their exact MIME and JOC route responses independently',()=>{
 const noMime=appleProfile({television:true},{...appleTVRuntimeFacts,videoEligibility:undefined});
 const noJoc=appleProfile({television:true},{...appleTVRuntimeFacts,audio:{...appleTVRuntimeFacts.audio,eac3JocEligible:false}});
 const hevc=(profile:typeof noMime)=>profile.engines.native.video!.find(video=>video.codec==='hevc')!;
 const eac3=(profile:typeof noMime)=>profile.engines.native.audio!.find(audio=>audio.codec==='eac3')!;
 assert.deepEqual(hevc(declaredProfiles.appletv_dv).dolbyVisionProfiles,[5,8]);
 assert.deepEqual(hevc(declaredProfiles.appletv_dv).dolbyVisionProfile8Compatibility,[1]);
 assert.deepEqual(hevc(noMime).dolbyVisionProfiles,[]);
 assert.equal(hevc(noMime).dynamicRanges?.includes('dolby_vision'),false);
 assert.deepEqual(eac3(noMime).objectAudio,['atmos'],'removing DV eligibility must not invent an audio denial');
 assert.deepEqual(hevc(noJoc).dolbyVisionProfiles,[5,8],'removing JOC eligibility must not invent a video denial');
 assert.equal(eac3(noJoc).objectAudio,undefined);
 assert.deepEqual(withMpvEngine(noMime).engines.mpv,declaredProfiles.appletv_mpv.engines.mpv,'native MIME facts never widen the separate mpv table');
 for(const profile of [declaredProfiles.ios,declaredProfiles.tvos,declaredProfiles.safari_hdr])assert.equal(eac3(profile).objectAudio,undefined,'plain EAC3 is not Atmos');
 assert.equal(declaredProfiles.chromium.display?.maxFrameRate,undefined,'unknown display refresh is not the decoder limit');
 assert.equal(declaredProfiles.safari_hdr.display?.maxFrameRate,undefined);
});
