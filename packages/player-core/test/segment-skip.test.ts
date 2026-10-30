import test from 'node:test';import assert from 'node:assert/strict';
import {activeSegment,skipsAutomatically,skipTarget,followedByScene,SKIP_LEAD_SECONDS} from '../src/segment-skip.ts';
import {upNextCountdown,upNextOffer} from '../src/up-next.ts';
const m=(over:Record<string,unknown>={})=>({id:'intro',kind:'intro',startSeconds:30,endSeconds:80,automaticSafe:true,...over}) as any;
test('the prompt offers the playing segment unless dismissed, turned off or about to end',()=>{
 const markers=[m(),m({id:'credits',kind:'credits',startSeconds:1300,endSeconds:1400})];
 assert.equal(activeSegment(markers,40,{},new Set())?.id,'intro');
 assert.equal(activeSegment(markers,79.6,{},new Set()),undefined);
 assert.equal(activeSegment(markers,40,{intro:'off'},new Set()),undefined);
 assert.equal(activeSegment(markers,40,{},new Set(['intro'])),undefined);
 assert.equal(activeSegment(markers,1350,{},new Set())?.id,'credits');
});
test('unattended skips need the viewer to ask for them and the server to call the segment safe',()=>{
 assert.equal(skipsAutomatically(m(),{intro:'auto'}),true);
 assert.equal(skipsAutomatically(m(),{intro:'ask'}),false);
 assert.equal(skipsAutomatically(m(),{}),false);
 assert.equal(skipsAutomatically(m({automaticSafe:false}),{intro:'auto'}),false);
});
test('an intro skip lands a moment before its end; credits land on the scene after them or the end',()=>{
 assert.equal(skipTarget(m()),80-SKIP_LEAD_SECONDS);
 assert.equal(skipTarget(m({startSeconds:79.5,endSeconds:80})),79.5);
 assert.equal(skipTarget(m({id:'c',kind:'credits',startSeconds:1300,endSeconds:1360})),1360);
 assert.equal(followedByScene({endSeconds:1360},1400),true);
 assert.equal(followedByScene({endSeconds:1398},1400),false);
});
test('credits followed by a scene never start the next episode early',()=>{
 const credits=m({id:'credits',kind:'credits',startSeconds:1300,endSeconds:1360});
 const offer=upNextOffer({markers:[credits],positionSeconds:1310,durationSeconds:1400,kind:'episode',mode:'ask',nextAvailable:true,passoutDue:false});
 assert.ok(offer);
 const countdown=upNextCountdown({offer:offer!,positionSeconds:1310,durationSeconds:1400,rate:1,autoplay:true,countdownSeconds:10,elapsedSeconds:12,startDuringCredits:true});
 assert.deepEqual(countdown,{automatic:false,seconds:100});
 const toEnd=m({id:'credits',kind:'credits',startSeconds:1300,endSeconds:1400});
 const at=upNextOffer({markers:[toEnd],positionSeconds:1310,durationSeconds:1400,kind:'episode',mode:'ask',nextAvailable:true,passoutDue:false});
 assert.deepEqual(upNextCountdown({offer:at!,positionSeconds:1310,durationSeconds:1400,rate:1,autoplay:true,countdownSeconds:10,elapsedSeconds:4,startDuringCredits:true}),{automatic:true,seconds:6});
});
