import test from 'node:test';
import assert from 'node:assert/strict';
import {canonicalMusicQualityLadder, canonicalQualityLadder} from '../../contracts/src/types.ts';
import {savedMusicQualities, savedMusicQualityRequest, qualityOptionRequest, qualityOptionSelected, requestInForce, savedQualityRequest} from '../src/playback-v1/quality.ts';
import {musicQualityPresentation} from '../src/playback-v1/quality-presentation.ts';
import {preferenceLabel,preferenceValueLabel} from '../src/presentation/preference-copy.ts';
import {parseOptions} from '../src/playback-v1/types.ts';

test('music uses a separate generated saved domain and numeric audio requests', () => {
 assert.deepEqual(savedMusicQualities,['original','cd-quality','320','96']);
 assert.equal(canonicalQualityLadder.length,9);
 for(const q of canonicalMusicQualityLadder){
  const request=savedMusicQualityRequest(q.id);
  assert.deepEqual(request,q.id==='cd-quality'?{mode:'cd-quality'}:q.audioKbps?{mode:'limit',maxAudioBitrateKbps:q.audioKbps}:{mode:'original'});
  assert.deepEqual(requestInForce(request),request);
  assert.deepEqual(savedQualityRequest(q.id),{mode:'original'});
 }
 assert.deepEqual(savedMusicQualityRequest('720p-high'),{mode:'original'});
 assert.deepEqual(savedMusicQualityRequest('320000'),{mode:'original'});
});

test('real music options keep numeric audio ceilings through parser, selection and request replay',()=>{
 const qualities=canonicalMusicQualityLadder.map(q=>q.id==='cd-quality'?{mode:'cd-quality'}:q.audioKbps?{mode:'limit',maxAudioBitrateKbps:q.audioKbps,sameAsOriginal:q.audioKbps>=192,applicable:true}:{mode:'original',applicable:true});
 const options=parseOptions({itemId:'song',kind:'track',qualities,versions:[],preferred:{versionId:'v1',reason:'default',quality:{mode:'limit',maxAudioBitrateKbps:320}}});
 assert.equal(options.qualities.length,4);
 for(const [i,q] of options.qualities.entries()) {
  const request=qualityOptionRequest(q);
  assert.deepEqual(request,savedMusicQualityRequest(canonicalMusicQualityLadder[i].id));
  assert.equal(qualityOptionSelected(q,request),true);
 }
 assert.equal(qualityOptionSelected(options.qualities[1],{mode:'limit',maxAudioBitrateKbps:256}),false);
 assert.equal(qualityOptionSelected(options.qualities[1],{mode:'limit',maxAudioBitrateKbps:320,maxHeight:720}),false);
 assert.deepEqual(options.preferred?.quality,{mode:'limit',maxAudioBitrateKbps:320});
});

test('music preference consumers use the source keys and localized bitrate units',()=>{
 assert.equal(preferenceLabel('music.qualityAway','phone').label,'Away from home');
 assert.equal(preferenceLabel('music.qualityMobile','phone').label,'On mobile data');
 for(const q of canonicalMusicQualityLadder){
  assert.equal(preferenceValueLabel('music.qualityMobile',q.id),q.id==='cd-quality'?'CD quality':q.audioKbps?`${q.audioKbps} kbps`:'Original');
  assert.equal(musicQualityPresentation(q.id).audioKbps,q.audioKbps);
 }
 assert.equal(musicQualityPresentation('320',true).hint,'Same as the original');
});
