import test from 'node:test';
import assert from 'node:assert/strict';
import {appendHomeRowPage,validateHomeDocument,validateHomeEntry,validateHomeRow,validateHomeSeeAll,fetchHomeRow,type HomeApi} from '../src/home.ts';
import {validateContentEntry} from '../src/library-content.ts';
import {HomeService} from '../src/home-service.ts';
import {homeRowPresentation,homeSpotlightModels} from '../src/presentation/home-spotlight.ts';
import {homeContractsMessages} from '../../i18n/src/catalog/en-US/next/home-contracts.ts';
import {formatMessage,parseMessage} from '../../i18n/src/messageformat.ts';

const entry=(id:string,kind='movie',more:Record<string,unknown>={})=>({id,kind,libraryId:'videos',title:id,navigation:{view:'item',entityId:id},playback:{itemId:id},available:true,...more});
const row=(id:string,more:Record<string,unknown>={})=>({id,viewerFence:'f',title:id,kind:'recent',artworkShape:'poster',entries:[],total:0,start:0,limit:2,hasMore:false,nextCursor:'',revision:{catalog:1,viewer:1},...more});
const doc=(rows:unknown[],more:Record<string,unknown>={})=>({serverId:'s',viewerFence:'f',spotlight:[],rows,layout:{revision:1,rowOrder:[],hiddenRowIds:[]},revision:{catalog:1,viewer:1},generatedAt:'2026-10-22T00:00:00Z',...more});
const source={id:'source',name:'Actual source',generation:'g',publishedAt:'2026-10-21T00:00:00Z',availableStart:'2026-10-21T00:00:00Z',availableEnd:'2026-10-23T00:00:00Z',provenance:'live-source',refreshState:'ready'};
const channel=(id:string,mark='SS')=>({source,channel:{id,sourceId:source.id,provenance:'live-source',name:'Summit Sports',number:'201',group:'Sports',generation:'g',favorite:true,hidden:false,preferenceRevision:1,tuneAvailable:true,recordAvailable:false,recordUnavailableReason:'not-recordable',mark:{text:mark,seed:source.id+':'+id},programmes:[{id:'programme-'+id,channelId:id,title:'Actual programme',start:'2026-10-22T00:00:00Z',end:'2026-10-22T01:00:00Z',lineage:'provider-id',seriesId:'',episodeId:'',newEvidence:'unknown',description:'',flags:{live:false,repeat:true}}]}});
const live=(channels:unknown[],more:Record<string,unknown>={})=>({revision:'directory-guide-preferences',observedAt:'2026-10-22T00:30:00Z',expiresAt:'2026-10-22T00:31:00Z',sort:'favorites',group:'Sports',groups:[{name:'Sports',count:2000}],channels,...more});
const api=(read:(path:string)=>unknown):HomeApi=>({request:async<T>(path:string)=>await read(path) as T});
const settle=()=>new Promise(resolve=>setTimeout(resolve,0));

test('Home episodes retain authorized parent facts without substituting child metadata or identity',()=>{
  const parent={kind:'show',year:2020,genres:['Drama','Mystery'],seasonCount:2};
  const episode=entry('episode','episode',{year:2026,genres:['Comedy'],seasonNumber:2,episodeNumber:3,durationMs:3000000,homeTitleContext:{...parent,privatePath:'/private/show'}});
  const parsed=validateHomeRow(row('continue',{entries:[episode],total:1})).entries[0];
  assert.deepEqual(parsed.homeTitleContext,parent);
  assert.ok(Object.isFrozen(parsed.homeTitleContext)&&Object.isFrozen(parsed.homeTitleContext!.genres));
  assert.equal(parsed.id,'episode');assert.equal(parsed.navigation!.entityId,'episode');assert.equal(parsed.playback!.itemId,'episode');
  assert.equal(parsed.year,2026);assert.deepEqual(parsed.genres,['Comedy']);assert.equal(parsed.duration,3000);
  assert.equal('homeTitleContext' in validateContentEntry(episode),false,'generic catalogue metadata stays unchanged');
  assert.equal(validateHomeEntry(entry('without','episode',{year:2026,genres:['Comedy']})).homeTitleContext,undefined);
  const sparse=validateHomeEntry({...episode,homeTitleContext:{kind:'show',genres:[],seasonCount:0}}).homeTitleContext!;
  assert.deepEqual(sparse,{kind:'show',genres:[],seasonCount:0});assert.equal(sparse.year,undefined);
  const resume={...episode,show:{id:'show',name:'Show'}};
  const document=validateHomeDocument(doc([],{spotlight:[{entry:entry('show','show'),reason:{kind:'recent',text:{code:'next.home.reason.recentTo',fallback:'Recently added'}},resumeTarget:resume}]}));
  assert.deepEqual(document.spotlight[0].resumeTarget!.homeTitleContext,parent);
  assert.equal(document.spotlight[0].entry.homeTitleContext,undefined);
  for(const broken of [{kind:'album',genres:[]},{kind:'show',genres:['a','b','c']},{kind:'show',genres:[1]},{kind:'show',genres:[],year:0},{kind:'show',genres:[],seasonCount:-1},{kind:'show',genres:[],seasonCount:1.5}]){
    const value=validateHomeEntry({...episode,homeTitleContext:broken});
    assert.equal(value.id,'episode');assert.equal(value.homeTitleContext,undefined);
  }
  assert.equal(validateHomeEntry(entry('movie','movie',{homeTitleContext:parent})).homeTitleContext,undefined);
});

test('R-10: a Home without a spotlight array is read as no spotlight',()=>{
  const {spotlight:_omitted,...rest}=doc([]);
  assert.deepEqual(validateHomeDocument(rest).spotlight,[]);
});
test('spotlight preserves server reasons and order, real show resume identity and normalized runtime',()=>{
  const spotlight=[
    {entry:entry('show','show',{year:2022,genres:['Drama'],seasonCount:3,overview:'Actual short overview',backdropUrl:'/art'}),reason:{kind:'because_you_watched',source:{id:'source-title',name:'Actual viewed title'},text:{code:'next.home.becauseYouWatched',params:{title:'Actual viewed title'},fallback:'Because you watched Actual viewed title'}},resumeTarget:entry('episode','episode',{show:{id:'show',name:'show'},seasonNumber:2,episodeNumber:3,durationMs:3000000,progressMs:600000,playback:{itemId:'episode',startMs:600000}})},
    {entry:entry('movie','movie',{durationMs:7200000}),reason:{kind:'new',libraryId:'videos',text:{code:'next.home.reason.newIn',params:{library:'Movies'},fallback:'New in Movies'}}},
  ];
  const parsed=validateHomeDocument(doc([],{spotlight,hero:{rowId:'legacy',entryId:'legacy'}}));
  assert.deepEqual(parsed.spotlight.map(v=>v.entry.id),['show','movie']);
  assert.equal(parsed.spotlight[0].reason.source!.name,'Actual viewed title');
  assert.equal(parsed.spotlight[0].resumeTarget!.show!.id,'show');
  const models=homeSpotlightModels(parsed);
  assert.deepEqual(models[0].primary!.label,{key:'next.home.resumeEpisode',params:{season:2,episode:3}});
  assert.equal(models[0].primary!.target.startSeconds,600);
  assert.equal(models[0].primary!.entry.duration,3000);
  assert.equal(models[1].facts.runtimeSeconds,7200);
  assert.equal(models[0].facts.seasonCount,3);
  assert.deepEqual(models[0].actions,[{key:'next.home.myList'}]);
  assert.equal('hero' in parsed,false);
  assert.equal(JSON.stringify(parsed).includes('"hero"'),false);
  const unicode=validateHomeDocument(doc([],{spotlight:[{...spotlight[1],entry:entry('unicode','movie',{overview:'🎬'.repeat(300)})}]}));
  assert.equal([...unicode.spotlight[0].entry.overview!].length,300,'synopsis limit counts Unicode characters as the server does');
  assert.throws(()=>validateHomeDocument(doc([],{spotlight:Array(6).fill(spotlight[0])})),'more spotlights than the bound is refused');
  // R-10 (O-159 two-tier parsing): a spotlight this client cannot read is left out alone; Home still shows.
  for(const broken of [{...spotlight[0],reason:{kind:'because_you_watched',text:spotlight[0].reason.text}},{...spotlight[0],resumeTarget:entry('e','episode',{show:{id:'other',name:'other'}})},{...spotlight[1],entry:entry('x','movie',{overview:'x'.repeat(301)})}]){
    const kept=validateHomeDocument(doc([],{spotlight:[broken,spotlight[1]]}));
    assert.deepEqual(kept.spotlight.map(v=>v.entry.id),['movie'],'an unreadable spotlight is dropped, the rest kept');
  }
});

test('new badges, causal source, collection facts, ordered ranks and audio contributions survive parsing',()=>{
  const collection={mediaKind:'movie',collection:{id:'collection',libraryId:'videos',name:'A collection',itemCount:4,revision:3},members:[entry('one'),entry('two'),entry('three'),entry('four')],backdropUrl:'/member/backdrop',firstYear:1984,lastTitle:'four'};
  const rows=[
    row('new_on_server',{entries:[entry('new','movie',{new:true}),entry('watched','movie',{watched:true})],total:2,seeAll:{type:'row',rowId:'new_on_server'}}),
    row('because_you_watched',{entries:[entry('related')],reason:{kind:'because_you_watched',source:{id:'actual',name:'Actual source'},text:{code:'next.home.becauseYouWatched',fallback:'Because you watched Actual source',params:{title:'Actual source'}}}}),
    row('featured_collection',{kind:'collection_banner',collection,total:1,seeAll:{type:'collection',resourceId:'collection',libraryId:'videos'}}),
    row('community_watching',{entries:[entry('ranked')],start:12,period:{days:30,start:'2026-09-22T00:00:00Z',end:'2026-10-22T00:00:00Z',metric:'distinct_eligible_viewers',text:{code:'next.home.last30Days',fallback:'Last 30 days'}}}),
    row('listen_next',{artworkShape:'square',entries:[entry('album','album'),entry('book','book',{durationMs:16200000,progressMs:5520000})],resumes:[{entryId:'book',target:entry('part','audiobook_file',{playback:{itemId:'part',startMs:120000},durationMs:5400000,progressMs:120000})}],total:2}),
    row('continue_books',{artworkShape:'square',entries:[entry('book','book')],platforms:['web','phone']}),
  ];
  const parsed=validateHomeDocument(doc(rows));
  assert.deepEqual(parsed.rows.map(v=>v.id),rows.map(v=>v.id));
  assert.equal(parsed.rows[0].entries[0].new,true);
  assert.equal(parsed.rows[1].reason!.source!.id,'actual');
  assert.equal(parsed.rows[2].collection!.collection.itemCount,4);
  assert.equal(parsed.rows[2].collection!.members.length,4);
  assert.equal(homeRowPresentation(parsed.rows[3]).cards[0].rank,13);
  assert.equal(parsed.rows[4].entries[1].progressSeconds,5520);
  assert.equal(homeRowPresentation(parsed.rows[4]).cards[1].resumeTarget!.playback!.startSeconds,120);
  assert.deepEqual(parsed.rows[5].platforms,['web','phone']);
  assert.equal(validateHomeDocument(doc([row('continue_books')])).rows.length,0);
  assert.throws(()=>validateHomeRow(row('featured_collection',{collection:{...collection,members:Array(5).fill(entry('one'))}})));
  assert.throws(()=>validateHomeRow(row('featured_collection',{collection:{...collection,collection:{...collection.collection,itemCount:3}}})));
  assert.throws(()=>validateHomeRow(row('listen_next',{resumes:[{entryId:'not-in-row',target:entry('file','audiobook_file')}]})));
  assert.throws(()=>validateHomeRow(row('community_watching',{period:{...rows[3].period as object,days:-1}})));
});

test('On now requires authoritative marks, real one-programme windows and keeps source identities and flags',async()=>{
  const parsed=validateHomeRow(row('on_now',{live:live([channel('one')]),total:2000,hasMore:true,nextCursor:'signed',seeAll:{type:'guide',group:'Sports',sourceId:'source'}}));
  assert.equal(parsed.live!.channels[0].channel.mark.text,'SS');
  assert.equal(parsed.live!.channels[0].channel.mark.seed,'source:one');
  assert.equal(parsed.live!.channels[0].channel.programmes[0].flags!.live,false);
  assert.equal(parsed.live!.channels[0].channel.programmes[0].flags!.repeat,true);
  assert.equal(parsed.live!.channels[0].source.name,'Actual source');
  assert.deepEqual(parsed.live!.channels[0].actions,{play:false,favorite:false});
  const offered=validateHomeRow(row('on_now',{live:live([{...channel('one'),actions:{play:true,favorite:true,editMetadata:true,queue:true}}])})).live!.channels[0];
  assert.deepEqual(offered.actions,{play:true,favorite:true});
  const unavailable=validateHomeRow(row('on_now',{live:live([{...channel('one'),channel:{...channel('one').channel,tuneAvailable:false},actions:{play:true,favorite:true}}])})).live!.channels[0];
  assert.deepEqual(unavailable.actions,{play:false,favorite:true});
  assert.equal(validateHomeDocument(doc([row('on_now',{live:live([channel('one')]),total:2000})])).rows.length,1);
  for(const bad of [live([{...channel('one'),channel:{...channel('one').channel,mark:undefined}}]),live([channel('one')],{sort:'name'}),live(Array(51).fill(channel('one'))),live([{...channel('one'),channel:{...channel('one').channel,programmes:[...channel('one').channel.programmes,...channel('one').channel.programmes]}}]),live([channel('one')],{observedAt:'2026-10-22T01:00:00Z',expiresAt:'2026-10-22T01:01:00Z'})])assert.throws(()=>validateHomeRow(row('on_now',{live:bad})));
  assert.deepEqual(validateHomeSeeAll({type:'guide',group:'Sports',sourceId:'source'}),{type:'guide',group:'Sports',sourceId:'source'});
  const paths:string[]=[];
  await fetchHomeRow(api(path=>{paths.push(path);return row('on_now',{live:live([channel('one')])});}),'on_now',{cursor:'opaque',limit:2,group:'Sports',sourceId:'source'});
  assert.equal(paths[0],'/v1/home/rows/on_now?limit=2&cursor=opaque&group=Sports&sourceId=source');
});

test('live paging retains server order and next cursor without a source-length cap',()=>{
  let current=validateHomeRow(row('on_now',{live:live([channel('0')]),total:2000,nextCursor:'next',hasMore:true}));
  for(let page=0;page<3;page++)current=appendHomeRowPage(current,validateHomeRow(row('on_now',{live:live(Array.from({length:50},(_,i)=>channel(String(page*50+i)))),total:2000,nextCursor:'next',hasMore:true})));
  assert.equal(current.live!.channels.length,150);
  assert.equal(current.nextCursor,'next');
  assert.equal(current.total,2000);
  assert.deepEqual(current.live!.channels.slice(0,3).map(v=>v.channel.id),['0','1','2']);
  assert.ok(current.live!.channels.every(v=>v.channel.mark.text==='SS'));
  assert.throws(()=>appendHomeRowPage(current,validateHomeRow(row('on_now',{live:live([channel('new')],{revision:'new'})}))));
});

test('merged live pages keep the earliest programme expiry in either page order',()=>{
  const early='2026-10-22T00:30:20Z',late='2026-10-22T00:30:50Z';
  for(const [firstExpiry,nextExpiry] of [[late,early],[early,late]]){
    const first=validateHomeRow(row('on_now',{live:live([channel('one')],{expiresAt:firstExpiry}),total:3,nextCursor:'second',hasMore:true}));
    const page=validateHomeRow(row('on_now',{live:live([channel('two')],{expiresAt:nextExpiry}),start:1,total:3,nextCursor:'third',hasMore:true}));
    const merged=appendHomeRowPage(first,page);
    assert.equal(merged.live!.expiresAt,early);
    assert.equal(merged.live!.observedAt,first.live!.observedAt);
    assert.equal(merged.live!.revision,first.live!.revision);
    assert.deepEqual(merged.live!.channels.map(v=>v.channel.id),['one','two']);
    assert.deepEqual(merged.live!.channels[1].channel.programmes[0].flags,{live:false,premiere:false,new:false,repeat:true});
    const last=validateHomeRow(row('on_now',{live:live([channel('three')]),start:2,total:3}));
    assert.equal(appendHomeRowPage(merged,last).live!.expiresAt,early,'later pages cannot extend the retained expiry');
    assert.equal(appendHomeRowPage(first,page).live!.channels.length,2,'both authorized channels remain loaded');
  }
});

test('deferred collection/live hydration survives empty entries and denied live refresh removes retained names',async()=>{
  let denied=false;
  const service=new HomeService(api(path=>{
    if(path==='/v1/home')return doc([row('featured_collection',{deferred:true}),row('on_now',{deferred:true})]);
    if(path.includes('featured_collection'))return row('featured_collection',{kind:'collection_banner',collection:{mediaKind:'movie',collection:{id:'collection',libraryId:'videos',name:'Collection',itemCount:1,revision:1},members:[entry('member')]},total:1});
    if(denied)throw {status:404,code:'not_found'};
    return row('on_now',{live:live([channel('one')]),total:2000,nextCursor:'next',hasMore:true});
  }));
  await service.load();await settle();await settle();
  assert.equal(service.getSnapshot().document!.rows[0].collection!.members.length,1);
  assert.equal(service.getSnapshot().document!.rows[1].live!.channels.length,1);
  denied=true;await service.revalidate();await settle();await settle();
  assert.deepEqual(service.getSnapshot().document!.rows.map(v=>v.id),['featured_collection']);
  assert.equal(JSON.stringify(service.getSnapshot().document).includes('Summit'),false);
  service.dispose();
});

test('dedicated Home copy has readable ICU and uses the exact frame headings',()=>{
  for(const message of Object.values(homeContractsMessages))assert.doesNotThrow(()=>parseMessage(message));
  assert.equal(homeContractsMessages['next.home.videoKinds'],'Movies, shows and anime');
  assert.equal(homeContractsMessages['next.home.audioKinds'],'Audiobooks and music');
  assert.equal(formatMessage(homeContractsMessages['next.home.reason.recentTo'],{library:'Movies'}),'Recently added to Movies');
  assert.equal(formatMessage(homeContractsMessages['next.home.becauseYouWatched'],{title:'Actual title'}),'Because you watched Actual title');
  assert.equal(formatMessage(homeContractsMessages['next.home.resumeEpisode'],{season:2,episode:3}),'Resume S2 E3');
});
