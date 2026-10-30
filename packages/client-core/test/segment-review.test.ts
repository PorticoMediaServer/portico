import test from 'node:test';import assert from 'node:assert/strict';
import {parseSegmentStatus,parseSegmentTitle,parseSegmentTitles,approveSegments,editSegmentMarker} from '../src/segment-review.ts';
const envelope=(result:unknown)=>({protocolVersion:'1.0',serverId:'server',result});
const files={pending:1,complete:20,failed:0,unsupported:2};
test('status keeps every readable library and the server mode',()=>{
 const s=parseSegmentStatus(envelope({mode:'standard',modes:['off','standard','thorough'],choices:['server','off','standard','thorough'],libraries:[{libraryId:'tv',name:'TV',itemKind:'episode',choice:'server',mode:'standard',files},{libraryId:'odd',name:'Odd',itemKind:'photo',choice:'server',mode:'standard',files}],files,showsWaiting:2,catchUpComplete:false,lastFullPassMS:0,failures:[]}));
 assert.equal(s.mode,'standard');
 assert.equal(s.libraries.length,1);
 assert.equal(s.libraries[0].files.complete,20);
 assert.throws(()=>parseSegmentStatus({result:{}}));
});
test('a title review keeps its targets and drops markers it cannot read',()=>{
 const marker={id:'m1',kind:'intro',startUS:'0',endUS:'51000000',confidence:0.95,provenance:'season_audio_match:intro:v1',basis:'season',approved:false,edited:false,automaticSafe:true,viewerVisible:true};
 const r=parseSegmentTitle(envelope({id:'show',title:'Show',kind:'show',libraryId:'tv',mode:'standard',seasons:[{number:1,title:'Season 1',entries:[{itemId:'e1',title:'Pilot',number:1,durationUS:'1500000000',state:'complete',errorCode:'',sourceId:'asset',sourceRevision:'r1',mappingRevision:'map',markerRevision:3,markers:[marker,{...marker,id:'m2',kind:'chapter'},{...marker,id:'m3',endUS:'9999999999'}]}]}]}));
 const entry=r.seasons[0].entries[0];
 assert.equal(entry.markers.length,1);
 assert.equal(entry.markers[0].automaticSafe,true);
 assert.equal(entry.markerRevision,3);
 const page=parseSegmentTitles(envelope({libraryId:'tv',nextCursor:'',titles:[{id:'show',title:'Show',kind:'show',files,markers:{automatic:4,suggested:1,approved:0,dismissed:0}}]}));
 assert.equal(page.titles[0].markers.automatic,4);
});
test('approving and editing send exactly what the server routes take',async()=>{
 const calls:{path:string;method?:string;body?:any}[]=[];
 const api={request:async(path:string,method?:string,body?:unknown)=>{calls.push({path,method,body});return envelope({changed:6});}} as any;
 assert.equal(await approveSegments(api,'show',['intro','credits'],1),6);
 assert.deepEqual(calls[0],{path:'/v1/admin/segments/titles/show/approve',method:'POST',body:{kinds:['intro','credits'],season:1}});
 const entry={itemId:'e1',title:'',number:1,durationUS:'1',state:'complete',errorCode:'',sourceId:'asset',sourceRevision:'r1',mappingRevision:'map',markerRevision:3,markers:[]};
 await editSegmentMarker(api,entry,{action:'dismiss',markerId:'m1'});
 assert.equal(calls[1].path,'/v1/items/e1/analysis/markers');
 assert.equal(calls[1].body.expectedRevision,3);
 assert.equal(calls[1].body.action,'dismiss');
 assert.ok(calls[1].body.requestId.length>=16);
});
import {parseSegmentClock,segmentClock} from '../src/segment-review.ts';
test('positions read and print as minutes and seconds',()=>{
 assert.equal(segmentClock('83400000'),'1:23.4');
 assert.equal(segmentClock('3723000000'),'1:02:03.0');
 assert.equal(parseSegmentClock('1:23.4'),'83400000');
 assert.equal(parseSegmentClock('83.4'),'83400000');
 assert.equal(parseSegmentClock('0:01:23'),'83000000');
 assert.equal(parseSegmentClock('1:75'),null);
 assert.equal(parseSegmentClock('abc'),null);
});
