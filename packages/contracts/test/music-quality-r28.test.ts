import test from 'node:test';
import assert from 'node:assert/strict';
import {canonicalMusicQualityLadder, canonicalQualityLadder} from '../src/types.ts';
import {canonicalMusicQualityLadder as playerMusic} from '../../player-core/src/quality-ladder.ts';
import {savedMusicQualityRequest, requestInForce, qualityOptionRequest, qualityOptionSelected} from '../../player-core/src/quality.ts';

test('generated music vocabulary separates fidelity ceilings from bitrates and retains video',()=>{
 assert.deepEqual(canonicalMusicQualityLadder.map(q=>q.id),['original','cd-quality','320','96']);
 assert.deepEqual(canonicalMusicQualityLadder.map(q=>q.audioKbps),[0,0,320,96]);
 assert.deepEqual(canonicalMusicQualityLadder.map(q=>q.dataMBPerHour),[0,0,144,43.2]);
 assert.deepEqual(playerMusic,canonicalMusicQualityLadder);
 assert.equal(canonicalQualityLadder.length,9);
 for (const q of playerMusic) assert.deepEqual(savedMusicQualityRequest(q.id),q.id==='cd-quality'?{mode:'cd-quality'}:q.audioKbps?{mode:'limit',maxAudioBitrateKbps:q.audioKbps}:{mode:'original'});
 for (const retired of ['256','192','128','automatic','320000']) assert.deepEqual(savedMusicQualityRequest(retired),{mode:'original'});
});

test('CD quality remains a distinct request through selection and replay',()=>{
 const cd={mode:'cd-quality'} as const;
 assert.deepEqual(qualityOptionRequest(cd),cd);
 assert.deepEqual(requestInForce(cd),cd);
 assert.equal(qualityOptionSelected(cd,cd),true);
 assert.equal(qualityOptionSelected(cd,{mode:'original'}),false);
 assert.equal(qualityOptionSelected(cd,{mode:'limit',maxAudioBitrateKbps:320}),false);
});
