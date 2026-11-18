import test from 'node:test';
import assert from 'node:assert/strict';
import {parseDVRPage,parseRecording,validRecordingOptions,defaultRecordingOptions,countUnwatched,seriesRuleConfig} from '../src/dvr.ts';
const hex=(n=64,c='a')=>c.repeat(n);
const time='2026-09-06T12:00:00Z',end='2026-09-06T13:00:00Z';
/** Padding on the wire is milliseconds; defaultRecordingOptions is the client's own (seconds) form. */
const wireOptions={beforeMs:0,afterMs:0,priority:0,retentionDays:0,episodeLimit:0};
const recording=()=>({id:hex(),revision:1,occurrence:{sourceId:hex(48),channelId:hex(64,'b'),generation:hex(48,'c'),programmeId:hex(64,'d')},programme:{id:hex(64,'d'),channelId:hex(64,'b'),title:'Owned programme',start:time,end,lineage:'provider-id',seriesId:'series',episodeId:'episode',newEvidence:'unknown'},options:{...wireOptions},start:time,end,state:'scheduled',reason:'',keep:false,itemId:'',libraryId:'',coverageStart:'',coverageEnd:'',bytes:0,ruleId:'',conflicts:[]});
const page=()=>({recordings:[recording()],rules:[],nextCursor:'',revision:1,captureAvailable:true,captureUnavailableReason:'',deletionAvailable:true,usage:{bytes:0,pendingDeleteBytes:0,recordings:1}});
const envelope=(result:unknown)=>({protocolVersion:'1.0',serverId:'server',result});
test('DVR channel marks retain only actual canonical text and seed',()=>{
 const raw={...recording(),channel:{id:hex(64,'b'),name:'Channel One',number:'1',mark:{text:'C1',seed:`${hex(48)}:${hex(64,'b')}`,privateURL:'https://private.invalid'}}};
 const parsed=parseRecording(raw);
 assert.deepEqual(parsed.channel?.mark,{text:'C1',seed:raw.channel.mark.seed});
 assert.ok(!JSON.stringify(parsed).includes('private'));
 const absent={...raw,channel:{id:raw.channel.id,name:raw.channel.name,number:raw.channel.number}};
 assert.equal('mark' in parseRecording(absent).channel!,false,'unknown stays absent without a seed fallback');
 for(const mark of [null,{text:'C1'},{text:'',seed:raw.channel.mark.seed},{text:'C1',seed:'other:channel'}]){
  assert.throws(()=>parseRecording({...raw,channel:{...raw.channel,mark}}));
 }
});
test('DVR public DTOs project known fields and reject foreign identity or invalid intervals',()=>{
 const raw=recording();Object.assign(raw,{path:'/private/secret',providerURL:'secret'});Object.assign(raw.options,{secret:'secret'});Object.assign(raw.programme,{secret:'secret'});
 assert.ok(!JSON.stringify(parseRecording(raw)).includes('secret'));
 raw.occurrence.programmeId=hex(64,'f');assert.throws(()=>parseRecording(raw));
 raw.occurrence.programmeId=raw.programme.id;raw.end=raw.start;assert.throws(()=>parseRecording(raw));
 raw.end=end;raw.programme.start='2026-02-30T12:00:00Z';assert.throws(()=>parseRecording(raw));
 // On a page, a repeated recording keeps its first row, and one that cannot be read or is in a state this app does not know is left out alone.
 const p:any=page();const first=p.recordings[0];p.recordings.push(first,{...first,id:hex(64,'9'),state:'archived-to-tape'},'nope');p.future=true;delete p.usage;p.captureAvailable='yes';
 const parsed=parseDVRPage(p);assert.deepEqual(parsed.recordings.map(r=>r.id),[first.id]);
 assert.equal(parsed.captureAvailable,false,'recording is offered only when the server says so outright');assert.deepEqual(parsed.usage,{bytes:0,pendingDeleteBytes:0,recordings:0});
 const over:any=page();over.recordings=Array(51).fill(first);assert.throws(()=>parseDVRPage(over));
 const noRevision:any=page();delete noRevision.revision;assert.throws(()=>parseDVRPage(noRevision));
});
test('DVR projections preserve observed incomplete coverage without inventing play authority',()=>{
 const r=recording();r.state='incomplete-playable';r.itemId=hex(64,'1');r.libraryId=hex(64,'2');r.coverageStart=time;r.coverageEnd='2026-09-06T12:30:00Z';r.bytes=188*100;
 const got=parseRecording(r);assert.equal(got.state,'incomplete-playable');assert.equal(got.coverageEnd,r.coverageEnd);assert.equal(got.libraryId,r.libraryId);assert.ok(!('streamURL' in got));
 r.libraryId='';assert.throws(()=>parseRecording(r));
});
test('series rule DTOs do not retain injected provider fields',()=>{
 const p:any=page();p.rules=[{id:hex(),revision:1,reconcileState:'ready',diagnostic:'',updatedAt:time,secret:'secret',config:{name:'Rule',sourceId:hex(48),seriesId:'series',enabled:true,episodes:'new',allowedChannels:[],blockedChannels:[],keywords:[],blockedKeywords:[],options:{...wireOptions,secret:'secret'},secret:'secret'}}];
 assert.ok(!JSON.stringify(parseDVRPage(p)).includes('secret'));assert.equal(parseDVRPage(p).rules[0].config.episodes,'new');
});

test('Part 2.2: channel and watched parse behind presence; unwatched counts only false',()=>{
 const withBoth={...recording(),channel:{id:hex(),name:'Channel One',number:'1.1'},watched:false};
 const parsed=parseRecording(withBoth);
 assert.equal(parsed.channel!.name,'Channel One');
 assert.equal(parsed.watched,false);
 assert.equal(parseRecording({...recording(),watched:null}).watched,null);
 assert.equal(parseRecording({...recording(),watched:true}).watched,true);
 // Absent on older servers stays absent.
 assert.equal('channel' in parseRecording(recording()),false);
 assert.equal('watched' in parseRecording(recording()),false);
 // Empty channel name (gone from guide and source) decodes.
 assert.equal(parseRecording({...recording(),channel:{id:hex(),name:'',number:''}}).channel!.name,'');
 assert.throws(()=>parseRecording({...recording(),channel:{id:'bad',name:'X',number:'1'}}));
 assert.throws(()=>parseRecording({...recording(),watched:'yes' as any}));
 // Null counts as neither watched nor unwatched.
 assert.equal(countUnwatched([{watched:false},{watched:false},{watched:true},{watched:null},{}]),2);
 assert.equal(countUnwatched([]),0);
});

test('FEAT-02: rule-editor state maps to RuleConfig (new/all, keep, padding, channel scope)',()=>{
 const draft=()=>({channel:{sourceId:hex(48),id:hex(64,'b'),generation:hex(48,'c')},programme:{id:hex(64,'d'),title:'Show',seriesId:'series'},series:false} as any);
 const {config,anchor}=seriesRuleConfig(draft(),{episodes:'new',keep:5,beforeSeconds:60,afterSeconds:300,anyChannel:false});
 assert.equal(config.episodes,'new');
 assert.equal(config.options.episodeLimit,5);
 assert.equal(config.options.beforeSeconds,60);
 assert.equal(config.options.afterSeconds,300);
 assert.deepEqual(config.allowedChannels,[hex(64,'b')]);
 assert.deepEqual(anchor,{sourceId:hex(48),channelId:hex(64,'b'),generation:hex(48,'c'),programmeId:hex(64,'d')});
 const any=seriesRuleConfig(draft(),{episodes:'all',keep:0,beforeSeconds:0,afterSeconds:0,anyChannel:true});
 assert.equal(any.config.episodes,'all');
 assert.deepEqual(any.config.allowedChannels,[]);
 assert.ok(validRecordingOptions(any.config.options));
 // Out-of-range editor state never reaches the server.
 assert.throws(()=>seriesRuleConfig(draft(),{episodes:'some' as any,keep:0,beforeSeconds:0,afterSeconds:0,anyChannel:true}));
 assert.throws(()=>seriesRuleConfig(draft(),{episodes:'new',keep:-1,beforeSeconds:0,afterSeconds:0,anyChannel:true}));
 assert.throws(()=>seriesRuleConfig(draft(),{episodes:'new',keep:0,beforeSeconds:21601,afterSeconds:0,anyChannel:true}));
 const bad=()=>({channel:{sourceId:'bad',id:hex(64,'b'),generation:hex(48,'c')},programme:{id:hex(64,'d'),title:'Show',seriesId:'series'},series:false} as any);
 assert.throws(()=>seriesRuleConfig(bad(),{episodes:'new',keep:0,beforeSeconds:0,afterSeconds:0,anyChannel:true}));
});

test('FEAT-08: a recording carries the server\'s seriesId and seriesTitle; an unreadable one is left out, never fatal',()=>{
 const named=parseRecording({...recording(),seriesId:'show-1',seriesTitle:'The Show'});
 assert.equal(named.seriesId,'show-1');assert.equal(named.seriesTitle,'The Show');
 const odd=parseRecording({...recording(),seriesId:42,seriesTitle:''});
 assert.equal(odd.seriesId,undefined);assert.equal(odd.seriesTitle,undefined);
});

test('recordings retain programme facts, image, position and duration without copying private fields',()=>{
 const raw:any=recording();Object.assign(raw.programme,{subtitle:'Episode name',episode:{season:2,number:5},categories:['News'],rating:{system:'VCHIP',value:'TV-G'},year:2026,starRating:'4/5',flags:{live:false,new:true,premiere:true,repeat:false},image:'/v1/dvr/recordings/'+raw.id+'/image',providerURL:'private'});raw.positionMs=15000;raw.durationMs=3600000;
 raw.programme.seriesId='s'.repeat(512);raw.programme.episodeId='e'.repeat(512);
 const r=parseRecording(raw);assert.equal(r.programme.seriesId,raw.programme.seriesId);assert.equal(r.programme.episodeId,raw.programme.episodeId);assert.equal(r.programme.subtitle,'Episode name');assert.deepEqual(r.programme.episode,{season:2,number:5});assert.equal(r.programme.flags?.live,false);assert.equal(r.programme.flags?.premiere,true);assert.equal(r.programme.image,raw.programme.image);assert.equal(r.positionMs,15000);assert.equal(r.durationMs,3600000);assert.ok(!JSON.stringify(r).includes('private'));
 raw.durationMs=-1;assert.throws(()=>parseRecording(raw));
});

