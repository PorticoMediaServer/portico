import test from 'node:test';
import assert from 'node:assert/strict';
import {validateHomeDocument,validateHomeEntry,validateHomeRow,type HomeActions,type HomeChannel,type HomeCollection} from '../src/home.ts';
import {homeEntrySelection,homeEntryCapabilities,homeChannelSelection,homeChannelCapabilities,homeCollectionSelection,homeCollectionCapabilities,homeRowSelection,type HomeActionPlatform} from '../src/home-selection.ts';

const web:HomeActionPlatform={platform:'web',playbackAvailable:true,queueAvailable:true};
const phone:HomeActionPlatform={platform:'phone-app',playbackAvailable:true,queueAvailable:true,nativeDownloadsAvailable:true};
function actions(more:Partial<HomeActions>={}):HomeActions {
  return {play:false,queue:false,playlist:false,collection:false,myList:false,favorite:false,watched:false,played:false,rating:false,editMetadata:false,refreshMetadata:false,delete:false,download:false,details:false,state:{watchlisted:false,favorite:false,watched:false,rating:null,personalRevision:0,containerRevision:0},selection:{play:more.play===true,queue:more.queue===true,playlist:more.playlist===true,collection:more.collection===true,myList:more.myList===true,favorite:more.favorite===true,watched:more.watched===true,download:more.download===true,edit:more.editMetadata===true,refresh:more.refreshMetadata===true,delete:more.delete===true},...more};
}
function entry(kind='movie',more:Record<string,unknown>={}) {
  return validateHomeEntry({kind,id:'title',libraryId:'library',title:'Actual title',navigation:{view:'item',entityId:'title'},...more});
}
function row(more:Record<string,unknown>={}) {
  return validateHomeRow({id:'row',viewerFence:'f',entries:[],total:0,start:0,limit:12,nextCursor:'',revision:{catalog:1,viewer:1},...more});
}

test('Home selection and More never infer grants from playback, personal state or role-shaped unknowns',()=>{
  const value=entry('movie',{available:true,playback:{itemId:'title'},watched:true,watchlisted:true,role:'owner',homeActions:{play:'yes',queue:1,editMetadata:'owner'}});
  const item=homeEntrySelection(value,phone)!;
  assert.ok(Object.values(item.capabilities).every(flag=>flag===false));
  assert.deepEqual(homeEntryCapabilities(value,phone),{kind:'movie'});
  const explicit=entry('movie',{homeActions:actions({favorite:true,state:{watchlisted:true,favorite:false,watched:true,rating:7,personalRevision:3,containerRevision:0}})});
  assert.deepEqual(homeEntryCapabilities(explicit,web),{kind:'movie',playable:false,queue:false,favorite:false,addToPlaylist:false,addToCollection:false,details:false,editMetadata:false,refreshMetadata:false,delete:false});
  assert.equal(homeEntrySelection(entry('movie',{libraryId:undefined}),web),undefined);
});

test('aggregate selection and More retain the displayed identity and title-level state',()=>{
  for(const kind of ['show','album','book']){
    const value=entry(kind,{id:kind,playback:{itemId:'a-child'},progressMs:25000,homeActions:actions({play:true,queue:true,playlist:true,myList:kind!=='album',favorite:true,editMetadata:true,state:{watchlisted:true,favorite:true,watched:false,rating:null,personalRevision:7,containerRevision:9}})});
    const picked=homeEntrySelection(value,web)!;
    assert.equal(picked.kind,kind);
    assert.equal('id' in picked&&picked.id,kind);
    assert.equal('libraryId' in picked&&picked.libraryId,'library');
    assert.equal(value.playback!.itemId,'a-child');
    assert.equal(homeEntryCapabilities(value,web).favorite,true);
    assert.equal(homeEntryCapabilities(value,web).watchlisted,kind==='album'?undefined:true);
    assert.ok(Object.isFrozen(value.homeActions)&&Object.isFrozen(value.homeActions!.state)&&Object.isFrozen(value.homeActions!.selection));
  }
});

test('selection My List is a descendant command and never invents a single-title saved state',()=>{
  for(const kind of ['show','season','album','artist','book','collection']){
    const single=kind==='show'||kind==='book';
    const value=entry(kind,{id:kind,playback:{itemId:'separate-child'},homeActions:actions({myList:single,selection:{...actions().selection,myList:true},state:{watchlisted:false,favorite:false,watched:false,rating:null,personalRevision:0,containerRevision:0}})});
    const selected=homeEntrySelection(value,web)!;
    assert.equal(selected.id,kind);assert.equal(selected.capabilities.myList,true);
    assert.equal(homeEntryCapabilities(value,web).watchlisted,single?false:undefined);
    assert.equal(value.homeActions!.state.watchlisted,false);
  }
  const titleOnly=entry('show',{homeActions:{...actions({play:true,myList:true}),selection:undefined}});
  assert.ok(Object.values(homeEntrySelection(titleOnly,web)!.capabilities).every(value=>value===false));
  assert.equal(homeEntryCapabilities(titleOnly,web).watchlisted,false);
  const malformed=entry('album',{homeActions:{...actions(),selection:{myList:'true',play:1,delete:'owner',privatePath:'/private/file'}}});
  assert.ok(Object.values(homeEntrySelection(malformed,phone)!.capabilities).every(value=>value===false));
  assert.equal('privatePath' in malformed.homeActions!.selection,false);
  const collection:HomeCollection={mediaKind:'movie',collection:{id:'collection',libraryId:'movies',name:'Series',itemCount:1000,revision:1},members:[],actions:actions({selection:{...actions().selection,myList:true}})};
  assert.equal(homeCollectionSelection(collection,web).capabilities.myList,true);
  assert.equal(homeCollectionCapabilities(collection,web).watchlisted,undefined);
});

test('aggregate Delete retains the displayed target and never counts or substitutes descendants',()=>{
  for(const kind of ['show','season','album','artist','book']){
    const value=entry(kind,{id:kind,itemCount:900,playback:{itemId:'first-child'},homeActions:actions({delete:true})});
    const selected=homeEntrySelection(value,web)!;
    assert.equal(selected.id,kind);assert.equal(selected.capabilities.delete,true);
    assert.equal(homeEntryCapabilities(value,web).delete,true);
    assert.equal(homeEntrySelection(value,{platform:'tv'})!.capabilities.delete,true);
    assert.equal(homeEntryCapabilities(value,{platform:'tv'}).delete,true);
  }
});

test('device runtimes narrow playback and Download while TV retains authorized catalogue commands',()=>{
  const value=entry('movie',{homeActions:actions({play:true,queue:true,download:true,editMetadata:true,refreshMetadata:true,delete:true})});
  const blocked=homeEntrySelection(value,{platform:'phone-app'})!.capabilities;
  assert.equal(blocked.play,false);assert.equal(blocked.queue,false);assert.equal(blocked.download,false);
  assert.equal(homeEntrySelection(value,phone)!.capabilities.download,true);
  assert.equal(homeEntrySelection(value,web)!.capabilities.download,false);
  const tv=homeEntrySelection(value,{platform:'tv',playbackAvailable:true,queueAvailable:true})!.capabilities;
  assert.equal(tv.edit,true);assert.equal(tv.refresh,true);assert.equal(tv.delete,true);assert.equal(tv.play,true);
  assert.equal(homeEntryCapabilities(value,{platform:'tv'}).editMetadata,true);
  assert.equal(homeEntryCapabilities(value,{...phone,nativeDownloadsAvailable:false}).download,undefined);
  assert.equal(homeEntrySelection(entry('movie',{homeActions:actions()}),phone)!.capabilities.play,false);
});

test('TV Delete and Refresh never borrow a selection grant for More or a title grant for selection',()=>{
  const tv:HomeActionPlatform={platform:'tv'};
  for(const kind of ['movie','show','season','episode','album','artist','book','song','audiobook_file']){
    const selectionOnly=entry(kind,{homeActions:actions({selection:{...actions().selection,refresh:true,delete:true}})});
    const selected=homeEntrySelection(selectionOnly,tv)!;
    assert.equal(selected.id,'title');assert.equal(selected.capabilities.refresh,true);assert.equal(selected.capabilities.delete,true);
    assert.equal(homeEntryCapabilities(selectionOnly,tv).refreshMetadata,false);assert.equal(homeEntryCapabilities(selectionOnly,tv).delete,false);
    const moreOnly=entry(kind,{homeActions:actions({refreshMetadata:true,delete:true,selection:{...actions().selection}})});
    assert.equal(homeEntrySelection(moreOnly,tv)!.capabilities.refresh,false);assert.equal(homeEntrySelection(moreOnly,tv)!.capabilities.delete,false);
    assert.equal(homeEntryCapabilities(moreOnly,tv).refreshMetadata,true);assert.equal(homeEntryCapabilities(moreOnly,tv).delete,true);
  }
  const malformed=entry('movie',{role:'owner',homeActions:{...actions(),refreshMetadata:'true',delete:1,selection:{refresh:'owner',delete:'true'}}});
  assert.equal(homeEntrySelection(malformed,tv)!.capabilities.refresh,false);assert.equal(homeEntrySelection(malformed,tv)!.capabilities.delete,false);
  assert.equal(homeEntryCapabilities(malformed,tv).refreshMetadata,false);assert.equal(homeEntryCapabilities(malformed,tv).delete,false);
  const collection:HomeCollection={mediaKind:'movie',collection:{id:'collection',libraryId:'movies',name:'Actual collection',itemCount:900,revision:2},members:[],actions:actions({selection:{...actions().selection,delete:true,refresh:true}})};
  assert.equal(homeCollectionSelection(collection,tv).capabilities.delete,true);assert.equal(homeCollectionSelection(collection,tv).capabilities.refresh,true);
  assert.equal(homeCollectionCapabilities(collection,tv).delete,false);assert.equal(homeCollectionCapabilities(collection,tv).refreshMetadata,false);
});

test('TV Edit requires its independent selection or More grant on every supported title kind',()=>{
  const tv:HomeActionPlatform={platform:'tv'};
  for(const kind of ['movie','episode','song','audiobook_file','show','season','album','artist','book']){
    const denied=entry(kind,{homeActions:actions()});
    assert.equal(homeEntrySelection(denied,tv)!.capabilities.edit,false);
    assert.equal(homeEntryCapabilities(denied,tv).editMetadata,false);
    const selectionOnly=entry(kind,{homeActions:actions({selection:{...actions().selection,edit:true}})});
    assert.equal(homeEntrySelection(selectionOnly,tv)!.capabilities.edit,true);
    assert.equal(homeEntryCapabilities(selectionOnly,tv).editMetadata,false);
    const moreOnly=entry(kind,{homeActions:actions({editMetadata:true,selection:{...actions().selection}})});
    assert.equal(homeEntrySelection(moreOnly,tv)!.capabilities.edit,false);
    assert.equal(homeEntryCapabilities(moreOnly,tv).editMetadata,true);
  }
  const malformed=entry('movie',{role:'owner',homeActions:{...actions(),editMetadata:'owner',selection:{edit:'true'}}});
  assert.equal(homeEntrySelection(malformed,tv)!.capabilities.edit,false);
  assert.equal(homeEntryCapabilities(malformed,tv).editMetadata,false);
});

function channel(sourceId:string,play=true,favorite=true):HomeChannel {
  return {source:{id:sourceId,name:'Source',generation:'g',publishedAt:'2026-10-22T00:00:00Z',availableStart:'2026-10-22T00:00:00Z',availableEnd:'2026-10-23T00:00:00Z',provenance:'live-source',refreshState:'ready'},channel:{id:'same-channel',sourceId,provenance:'live-source',name:'Channel',number:'1',group:'',generation:'g',programmes:[],tuneAvailable:play,tuneUnavailableReason:'',recordAvailable:false,recordUnavailableReason:'',favorite:false,hidden:false,preferenceRevision:1,mark:{text:'C',seed:sourceId}},actions:{play,favorite}};
}
test('channel source collisions remain distinct without a library and only carry real channel offers',()=>{
  const first=homeChannelSelection(channel('one'),web),second=homeChannelSelection(channel('two'),web);
  assert.equal(first.kind,'channel');assert.equal(second.kind,'channel');
  assert.equal('sourceId' in first&&first.sourceId,'one');assert.equal('sourceId' in second&&second.sourceId,'two');
  assert.equal('channelId' in first&&first.channelId,'same-channel');
  assert.equal('libraryId' in first,false);assert.equal('id' in first,false);
  assert.deepEqual(Object.entries(first.capabilities).filter(([,v])=>v).map(([k])=>k).sort(),['favorite','play']);
  assert.ok(Object.values(homeChannelSelection(channel('one',false,false),web).capabilities).every(v=>!v));
  assert.deepEqual(homeChannelCapabilities(channel('one'),web),{kind:'channel',playable:true,favorite:false});
});

test('collection banner has independent movie semantics, identity and grants',()=>{
  const member=entry('movie',{homeActions:actions({play:true,editMetadata:true,delete:true})});
  const collection:HomeCollection={mediaKind:'movie',collection:{id:'collection',libraryId:'movies',name:'Actual collection',itemCount:400,revision:4},members:[{...member,libraryId:'movies'}],actions:actions({play:true,queue:true,playlist:true,details:true})};
  const selected=homeCollectionSelection(collection,web);
  assert.equal('id' in selected&&selected.id,'collection');
  assert.equal(selected.capabilities.edit,false);assert.equal(selected.capabilities.delete,false);
  assert.equal(homeCollectionCapabilities(collection,web).playable,true);
  const parsed=row({collection});
  assert.equal(parsed.collection!.mediaKind,'movie');assert.equal(parsed.collection!.collection.itemCount,400);
  assert.deepEqual(homeRowSelection(parsed,web),[selected]);
  assert.equal(homeCollectionSelection({...collection,actions:undefined},web).capabilities.play,false);
  assert.throws(()=>row({collection:{...collection,mediaKind:'song'}}));
  assert.throws(()=>row({collection:{...collection,members:[entry('episode',{libraryId:'movies'})]}}));
});

test('bounded Home row picks omit resume targets and preserve offers across append inputs',()=>{
  const shown=entry('show',{id:'show',homeActions:actions({play:true})});
  const resume=entry('episode',{id:'resume',playback:{itemId:'resume'},homeActions:actions({play:true})});
  const parsed=row({entries:[shown],resumes:[{entryId:'show',target:resume}]});
  const picks=homeRowSelection(parsed,web);
  assert.equal(picks.length,1);assert.equal('id' in picks[0]&&picks[0].id,'show');
  assert.equal(parsed.resumes![0].target.homeActions!.play,true);
  const frozen=Object.isFrozen(picks)&&Object.isFrozen(picks[0]);assert.equal(frozen,true);
});

test('direct spotlight offers parse and adapt without manufacturing a row or selecting its resume child',()=>{
  const shown=entry('show',{id:'show',homeActions:actions({play:true,myList:true,editMetadata:true,state:{watchlisted:true,favorite:false,watched:false,rating:null,personalRevision:5,containerRevision:0}})});
  const resume=entry('episode',{id:'episode',show:{id:'show',name:'Actual show'},playback:{itemId:'episode'},homeActions:actions({play:true})});
  const document=validateHomeDocument({serverId:'server',viewerFence:'fence',revision:{catalog:4,viewer:2},rows:[],spotlight:[{entry:shown,reason:{kind:'new',text:{code:'next.home.reason.newIn',fallback:'New in Movies'}},resumeTarget:resume}],layout:{revision:1,rowOrder:[],hiddenRowIds:[]},generatedAt:'2026-10-22T00:00:00Z'});
  const candidate=document.spotlight[0];
  const selected=homeEntrySelection(candidate.entry,web)!;
  assert.equal(selected.id,'show');assert.equal(selected.libraryId,'library');assert.equal(candidate.resumeTarget!.playback!.itemId,'episode');
  assert.equal(homeEntryCapabilities(candidate.entry,web).watchlisted,true);
  assert.equal(homeEntryCapabilities(candidate.resumeTarget!,web).watchlisted,undefined);
  assert.equal(candidate.entry.homeActions!.state.personalRevision,5);
});

test('Not interested requires a strict explicit offer and never becomes a selection offer', () => {
  for (const offered of [undefined, false, 'true', 1, null, true]) {
    const value = entry('movie', {homeActions: {...actions(), notInterested: offered}});
    assert.equal(homeEntryCapabilities(value, {...phone, recommendationRow:true}).notInterested === true, offered === true);
    assert.equal('notInterested' in homeEntrySelection(value, phone)!.capabilities, false);
    assert.equal(value.homeActions?.notInterested === true, offered === true);
    assert.equal(homeEntryCapabilities(value, phone).notInterested, undefined);
  }
});
