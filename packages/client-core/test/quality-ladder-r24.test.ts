import test from 'node:test';
import assert from 'node:assert/strict';
import {canonicalQualityLadder} from '../../contracts/src/types.ts';
import {createI18n} from '../../i18n/src/index.ts';
import {qualityPresentation,qualityLimitKbps} from '../src/playback-v1/quality-presentation.ts';
import {savedQualities,qualityLadder,savedQualityRequest} from '../src/playback-v1/quality.ts';
import {qualityRequest,qualityChoiceOrder,offeredQualities,fixedQualityOf} from '../src/playback-v1/choices.ts';

test('one nine-choice ladder carries exact budgets and data while about hints round only for display',()=>{
 assert.deepEqual(savedQualities,['original','automatic','2160p','1080p-high','1080p-low','720p-high','720p-low','480p','240p']);
 assert.deepEqual(qualityChoiceOrder,savedQualities);
 assert.deepEqual(qualityLadder.map(q=>q.videoKbps),[40000,20000,8000,4000,2000,1500,500]);
 assert.deepEqual(qualityLadder.map(q=>q.dataGBPerHour),[18,9,3.6,1.8,.9,.675,.225]);
 const t=createI18n({locale:'en-US'});
 assert.equal(qualityPresentation('480p',false,t).hint,'about 0.7 GB an hour');
 assert.equal(qualityPresentation('240p',false,t).hint,'about 0.2 GB an hour');
 assert.equal(qualityPresentation('2160p',true,t).hint,'Same as the original');
 for(const q of canonicalQualityLadder){assert.equal(qualityLimitKbps(q.id),q.videoKbps);assert.equal(qualityPresentation(q.id).label,q.label)}
 assert.deepEqual(qualityRequest('1080p-high'),{mode:'fixed',id:'1080p-high'});
 assert.deepEqual(savedQualityRequest('1080p-low'),{mode:'fixed',id:'1080p-low'});
 assert.deepEqual(offeredQualities({height:1080,bitrateKbps:1000}).map(q=>q.id),['240p']);
 assert.deepEqual(offeredQualities({height:1080}),[]);
 assert.equal(fixedQualityOf({mode:'limit',maxHeight:1080,maxVideoBitrateKbps:3000}),undefined);
 assert.equal(fixedQualityOf({mode:'limit',maxVideoBitrateKbps:2000})?.id,'720p-low');
 assert.equal(fixedQualityOf({mode:'limit',maxHeight:1080}),undefined);
 assert.equal(fixedQualityOf({mode:'limit',maxHeight:1080,maxVideoBitrateKbps:2000}),undefined);
 assert.equal(fixedQualityOf({mode:'limit',maxHeight:1080,maxVideoBitrateKbps:8000})?.id,'1080p-low');
});
