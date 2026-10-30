import test from 'node:test';import assert from 'node:assert/strict';
import {WatchTogetherSync,GroupClock,groupCorrection,groupTargetPositionUs,type FollowerLocal,type FollowerEffect} from '../src/index.ts';import {room,scope,fence,channelFence,group,T0} from './fixtures.ts';
const local=(over:Partial<FollowerLocal>={}):FollowerLocal=>({fence,positionSeconds:30,playing:true,buffering:false,ready:true,seeking:false,durationSeconds:7200,...over});
const follower=(over:Parameters<typeof group>[0]={})=>{const s=new WatchTogetherSync();s.join(room);s.bind(fence);s.adopt(room,group(over));return s;};
test('copied group timeline and correction bands preserve quiet, rate and seek boundaries',()=>{
 const {timeline,sync}=group();assert.equal(groupTargetPositionUs(timeline,1000,2000),11000000n);assert.equal(groupTargetPositionUs({...timeline,state:'paused'},1000,60000),10000000n);assert.equal(groupTargetPositionUs({...timeline,rate:{numerator:'2',denominator:'1'}},1000,2000),12000000n);assert.equal(groupTargetPositionUs(timeline,5000,1000),10000000n);
 assert.equal(groupCorrection(sync,500000n,0n).action,'none');assert.equal(groupCorrection(sync,750000n,0n).action,'rate');assert.equal(groupCorrection(sync,1500000n,7000000n).rate,1.1);assert.equal(groupCorrection(sync,-1500000n,7000000n).rate,0.9);assert.equal(groupCorrection(sync,3000000n,7000000n).seekToUs,'7000000');
});
test('follower loads each occurrence once, nudges moderate drift and seeks large drift',()=>{
 const s=follower();const other={...fence,itemId:'other'};s.bind(other);const a=s.tick(local({fence:other}),T0+20000);assert.equal(a[0].kind,'load');if(a[0].kind==='load')assert.equal(a[0].positionSeconds,30);assert.ok(!s.tick(local({fence:other}),T0+21000).some(e=>e.kind==='load'));
 const near=follower();assert.ok(!near.tick(local({positionSeconds:29.8}),T0+20000).some(e=>['seek','rate'].includes(e.kind)));
 const behind=follower();assert.ok(behind.tick(local({positionSeconds:28.5}),T0+20000).some(e=>e.kind==='rate'&&e.rate===1.1));
 const lost=follower();assert.ok(lost.tick(local({positionSeconds:5}),T0+20000).some(e=>e.kind==='seek'&&e.positionSeconds===30));
 const duplicate=follower();duplicate.adopt(room,group({revision:'8',eventOrdinal:'10',timeline:{...group().timeline,currentEntryId:'second'}}));assert.ok(duplicate.tick(local(),T0+20000).some(e=>e.kind==='load'));
});
/** Adapted slow-player fixture from client-core/test/group-session.test.ts. */
function simulate(options:{latency:number|number[];says:'seeking'|'target'|'position';startAt?:number;duration?:number;start?:number;fails?:number;playing?:boolean;seconds?:number;paused?:boolean;jump?:boolean}) {
 let now=T0+(options.startAt??20)*1000,base=options.start??5,since=now,pending:{until:number;target:number;fails:boolean}|undefined,playing=options.playing??true;
 const s=follower(options.paused?{timeline:{...group().timeline,state:'paused'}}:{}),log:number[]=[],drifts:number[]=[];let restarted=0;
 const where=()=>Math.min(options.duration??Infinity,playing?base+(now-since)/1000:base);
 const settle=()=>{if(pending&&now>=pending.until){if(!pending.fails){base=pending.target;since=pending.until;}pending=undefined;}};
 for(let i=0;i<(options.seconds??60);i++){
  settle();if(options.jump&&i===4)s.adopt(room,group({revision:'8',playbackRevision:'4',eventOrdinal:'10',timeline:{...group().timeline,anchorPositionUs:'600000000',anchorAt:new Date(now).toISOString()}}));
  const at=options.says!=='position'&&pending?pending.target:where();
  const effects=s.tick(local({positionSeconds:at,playing,...(options.duration?{durationSeconds:options.duration}:{durationSeconds:undefined}),...(options.says==='seeking'?{seeking:!!pending}:{seeking:undefined})}),now);
  for(const e of effects){if(e.kind==='seek'){if(pending)restarted++;const latency=Array.isArray(options.latency)?options.latency[Math.min(log.length,options.latency.length-1)]:options.latency;log.push(e.positionSeconds);pending={until:now+(options.fails??latency),target:Math.min(options.duration??Infinity,e.positionSeconds),fails:options.fails!==undefined};settle();}else if(e.kind==='pause'){base=where();since=now;playing=false;}else if(e.kind==='play'){base=where();since=now;playing=true;}}
  const t=s.getSnapshot()!.timeline,target=Number(groupTargetPositionUs(t,Date.parse(t.anchorAt),now))/1e6;drifts.push(Math.abs(Math.min(options.duration??Infinity,target)-where()));now+=1000;
 }
 return {log,drifts,restarted};
}
for(const latency of [1200,2000,5000,8000,12000,17000,19000])test(`slow seek converges at ${latency} ms without restarting an in-flight seek`,()=>{
 const run=simulate({latency,says:'seeking',duration:7200,seconds:100});assert.equal(run.restarted,0);assert.ok(run.drifts.slice(60).every(d=>d<0.75),JSON.stringify(run));
 if(latency===8000)assert.deepEqual(run.log,[30,46]);if(latency===17000)assert.deepEqual(run.log,[30,64]);
});
test('without explicit seek completion the follower waits twenty seconds and never aims ahead',()=>{
 for(const says of ['target','position'] as const){const run=simulate({latency:8000,says,duration:7200,seconds:61});assert.equal(run.restarted,0);assert.deepEqual(run.log,[30,50,70,90]);}
});
test('near the ending or without duration, seek compensation cannot cut the title short',()=>{
 const near=simulate({latency:[8000,0],says:'seeking',startAt:80,duration:100,seconds:9});assert.deepEqual(near.log,[90,98]);assert.ok(near.log.every(p=>p<100));
 assert.deepEqual(simulate({latency:8000,says:'seeking',startAt:70,duration:120,seconds:20}).log,[80,96]);
 assert.deepEqual(simulate({latency:8000,says:'seeking',startAt:70,seconds:12}).log,[80,88]);
});
test('paused groups, failed seeks and host jumps retain the existing correction semantics',()=>{
 for(const says of ['seeking','position'] as const)assert.deepEqual(simulate({latency:8000,says,start:40,playing:false,paused:true,seconds:12}).log,[10]);
 assert.deepEqual(simulate({latency:0,says:'seeking',fails:15000,duration:7200,seconds:31}).log,[30,45,60]);
 assert.deepEqual(simulate({latency:8000,says:'seeking',duration:7200,seconds:40,jump:true}).log,[30,600,616]);
});
test('buffering waits, hidden group media pauses, and hosts report end once per playback revision',()=>{
 const s=follower();assert.ok(!s.tick(local({buffering:true,positionSeconds:0}),T0+20000).some(e=>e.kind==='seek'||e.kind==='rate'));
 const hidden=follower({timeline:{...group().timeline,itemId:''}});assert.ok(hidden.tick(local(),T0+20000).some(e=>e.kind==='pause'));
 const host=follower({isHost:true,canControl:true});const end=local({durationSeconds:30,positionSeconds:30,playing:false});assert.equal(host.tick(end,T0+30000).filter(e=>e.kind==='readiness'&&e.ended).length,1);assert.equal(host.tick(end,T0+31000).filter(e=>e.kind==='readiness'&&e.ended).length,0);
});
test('scope, room epoch, session and all three monotonic counters fence group updates and commands',()=>{
 const s=follower();assert.equal(s.adopt({...room,epoch:2},group({revision:'8',eventOrdinal:'10'})),false);assert.equal(s.adopt({...room,scope:{...scope,profileId:'other'}},group()),false);
 for(const delta of [{revision:'6',eventOrdinal:'10'},{playbackRevision:'2',eventOrdinal:'10'},{eventOrdinal:'8'}])assert.equal(s.adopt(room,group(delta)),false);
 assert.deepEqual(s.tick(local({fence:{...fence,sessionId:'old'}}),T0),[]);s.tick(local(),T0+20000);assert.deepEqual(s.tick(local(),T0+19999),[]);
 assert.deepEqual(s.control(room,'play'),{kind:'host-only',expand:true});const host=follower({isHost:true,canControl:true});assert.deepEqual(host.control(room,'seek',5),{kind:'group-transport',command:'seek',positionUs:'5000000',expectedRevision:'3'});
 s.adopt(room,group({phase:'ended',revision:'8',eventOrdinal:'10'}));assert.equal(s.adopt(room,group({revision:'9',eventOrdinal:'11'})),false);assert.deepEqual(s.tick(local(),T0+30000),[]);s.leave();assert.equal(s.status(),null);
});
test('equal-counter service projections follow Live to Reconnecting to Live and terminal closure',()=>{
 const s=follower();assert.equal(s.status(),'in-step');
 for(const phase of ['reconnecting','live','ended'] as const){
  assert.equal(s.adopt(room,group({phase})),true);assert.equal(s.status(),phase==='live'?'in-step':phase);assert.deepEqual(s.getSnapshot(),group({phase}));
 }
 assert.deepEqual(s.tick(local(),T0+20000),[]);assert.equal(s.control(room,'play'),null);
 const ended=s.getSnapshot();assert.equal(s.adopt(room,group()),false);assert.equal(s.adopt(room,group({revision:'8',playbackRevision:'4',eventOrdinal:'10'})),false);assert.equal(s.getSnapshot(),ended);
});
test('equal-counter lifecycle changes retain lower-counter, retired-room and stale-source fences',()=>{
 const s=follower();assert.equal(s.adopt(room,group({phase:'reconnecting'})),true);const reconnecting=s.getSnapshot();
 for(const delta of [{revision:'6'},{playbackRevision:'2'},{eventOrdinal:'8'}])for(const phase of ['live','ended'] as const){assert.equal(s.adopt(room,group({...delta,phase})),false);assert.equal(s.getSnapshot(),reconnecting);}
 assert.equal(s.adopt({...room,roomId:'retired'},group({id:'retired',phase:'live'})),false);assert.equal(s.adopt({...room,scope:{...scope,profileId:'other'}},group()),false);
 const freshRoom={...room,epoch:2};s.join(freshRoom);s.bind(fence);assert.equal(s.adopt(freshRoom,group({phase:'reconnecting'})),true);assert.equal(s.adopt(room,group({phase:'live'})),false);assert.equal(s.status(),'reconnecting');assert.equal(s.adopt(freshRoom,group()),true);
 const freshSource={...fence,sourceId:'fresh',generation:2};s.bind(freshSource);assert.deepEqual(s.tick(local(),T0+20000),[]);assert.ok(s.tick(local({fence:freshSource,positionSeconds:5}),T0+20000).some(e=>e.kind==='seek'&&e.fence.sourceId==='fresh'));
 s.leave();assert.equal(s.adopt(freshRoom,group({phase:'live'})),false);assert.equal(s.status(),null);
});
test('malicious clocks, counters, rates, identifiers and sync bands cannot reach engines',()=>{
 for(const delta of [{revision:'9223372036854775808'},{timeline:{...group().timeline,anchorAt:'bad'}},{timeline:{...group().timeline,rate:{numerator:'1',denominator:'0'}}},{timeline:{...group().timeline,itemId:'../../private'}},{sync:{...group().sync,rateCorrectionMaximum:'Infinity'}},{sync:{...group().sync,noCorrectionUnderMs:5000}}]){const s=new WatchTogetherSync();s.join(room);assert.throws(()=>s.adopt(room,group(delta)));}
});
test('all pill states come from group facts and reconnects keep the last known room',()=>{
 assert.equal(follower().status(),'in-step');assert.equal(follower({waiting:true}).status(),'waiting');assert.equal(follower({hostPresence:'absent'}).status(),'host-away');const s=follower({phase:'reconnecting'});assert.equal(s.status(),'reconnecting');assert.equal(s.getSnapshot()?.timeline.itemId,'item');
});
test('bounded NTP samples ignore slow outliers, expire after two minutes and reset per room',()=>{
 const c=new GroupClock();assert.equal(c.sample(5000,1000),4000);assert.equal(c.sample(5150,1300,{sent:1000,received:1300}),4000);assert.equal(c.sample(6000,4000,{sent:2000,received:4000}),4000);assert.equal(c.sample(9000,5000),4000);assert.equal(c.sample(150000,130001),19999);c.reset();assert.equal(c.offsetMs,0);
 for(let i=0;i<100;i++)c.sample(1000+i,1000+i,{sent:1000+i,received:1000+i});assert.equal(c.offsetMs,0);
});

test('the queue-only group contract cannot mistake a live channel for a matching catalogue occurrence',()=>{
 const s=follower(),live={...channelFence,sessionId:fence.sessionId,channelId:fence.itemId};
 assert.throws(()=>s.bind(live as never));
 assert.deepEqual(s.tick(local({fence:live as never,positionSeconds:5}),T0+20000),[]);
 assert.ok(s.tick(local({positionSeconds:5}),T0+20000).some(e=>e.kind==='seek'));
});
