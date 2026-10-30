import test from 'node:test';import assert from 'node:assert/strict';import {readFileSync} from 'node:fs';
import {declaredProfiles,appleTVRuntimeFacts} from './declared-profile-fixtures.ts';
import {appleProfile,capabilityDocumentProblem,withMpvEngine} from '../src/client-profile.ts';
test('Go planner capability fixtures exactly match the declared client tables',()=>{
 for(const [name,profile] of Object.entries(declaredProfiles)) assert.equal(capabilityDocumentProblem(profile),null,name);
 assert.deepEqual(JSON.parse(JSON.stringify(declaredProfiles)),JSON.parse(readFileSync(new URL('../../../server/internal/playback/testdata/client-profiles.json',import.meta.url),'utf8')));
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
