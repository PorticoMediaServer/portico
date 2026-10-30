import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {formatMessage} from '../../i18n/src/messageformat.ts';
import {shared} from '../../i18n/src/catalog/en-US/next/shared.ts';
import {homeCollectionCaption, homeRowPresentation, homeSpotlightModels, homeTitleFactParts, type HomeCollectionCaptionSource, type HomeTitlePresentationEntry} from '../src/presentation/home-spotlight.ts';
import {playbackIdentity, type FactPart, type KitTranslate} from '../src/presentation/kit-model/index.ts';
import {validateHomeDocument, validateHomeRow} from '../src/home.ts';
import type {ContentEntry} from '../src/library-content.ts';

const t:KitTranslate=(id,...args)=>formatMessage(shared[id],args[0]);
const env={t,locale:'en-US'};
const parts=(values:readonly FactPart[])=>values.map(value=>[value.id,value.text,value.role]);
const entry=(id:string,kind:ContentEntry['kind'],extra:Partial<ContentEntry>={}):ContentEntry=>({id,kind,title:id,...extra});

test('spotlight canonical Home facts use the displayed show while playback retains its real episode',()=>{
  const show=entry('show-real','show',{year:2023,genres:['Drama'],seasonCount:1,contentRating:'TV-MA',duration:9999,playback:{itemId:'wrong-parent-target'}});
  const resume=entry('episode-real','episode',{title:'Actual episode title',show:{id:show.id,name:show.title},year:2024,genres:['Episode genre'],seasonNumber:1,episodeNumber:2,duration:3000,progressSeconds:600,playback:{itemId:'file-real',startSeconds:600}});
  const reason={kind:'recommended',text:{code:'reason',fallback:'Actual server reason'}};
  const [slide]=homeSpotlightModels({spotlight:[{entry:show,resumeTarget:resume,reason}]});
  assert.strictEqual(slide.entry,show);assert.strictEqual(slide.primary!.entry,resume);assert.strictEqual(slide.primary!.target,resume.playback);
  assert.deepEqual(slide.primary!.label,{key:'next.home.resumeEpisode',params:{season:1,episode:2}});
  assert.deepEqual(parts(homeTitleFactParts(slide.entry,env)),[
    ['year','2023','value'],['genres','Drama','supplement'],['season-count','1 season','supplement'],
  ]);
  assert.deepEqual(playbackIdentity({kind:'video',item:{id:resume.id,kind:resume.kind,title:resume.title,show:{id:show.id,title:show.title},episode:{mode:'seasonal',season:1,episode:2}}},'dock',env),{
    id:resume.id,kind:'video',title:show.title,subtitle:[{id:'episode-label',text:'S1 E2',role:'context'},{id:'episode-title',text:resume.title,role:'identity'}],
  },'metadata context never changes the canonical playback identity');
});

test('Home adapter consumes normalized real entry fields and keeps server slide order and unknowns',()=>{
  const parsed=validateHomeDocument({serverId:'server',viewerFence:'viewer',rows:[],layout:{revision:1,rowOrder:[],hiddenRowIds:[]},revision:{catalog:1,viewer:1},generatedAt:'2026-10-22T00:00:00Z',spotlight:[
    {entry:{id:'film',kind:'movie',title:'Film',year:2001,genres:['Animation','Family'],durationMs:7500000,contentRating:'PG'},reason:{kind:'recommended',text:{code:'reason',fallback:'Actual reason'}}},
    {entry:{id:'show',kind:'show',title:'Show',subtitle:'2023 · Drama · 4 seasons'},reason:{kind:'recommended',text:{code:'reason',fallback:'Actual reason'}}},
  ]});
  const slides=homeSpotlightModels(parsed);
  assert.deepEqual(slides.map(slide=>slide.entry.id),['film','show']);
  assert.deepEqual(parts(homeTitleFactParts(slides[0].entry,env)),[
    ['year','2001','value'],['genres','Animation, Family','supplement'],['duration','2 h 5 min','value'],
  ]);
  assert.deepEqual(homeTitleFactParts(slides[1].entry,env),[],'joined legacy subtitle is never a source of facts');
  assert.equal(slides[1].primary,undefined,'formatting metadata does not create a playable target');
});

test('TV focused rows keep actual entry, explicit resume, rank and facts separate without parent inference',()=>{
  const source=validateHomeRow({id:'community_watching',viewerFence:'viewer',kind:'ranked',artworkShape:'poster',title:'Actual row',entries:[
    {id:'show',kind:'show',title:'Show',year:2023,genres:['Drama'],seasonCount:1},
  ],resumes:[{entryId:'show',target:{id:'episode',kind:'episode',title:'Actual episode',show:{id:'show',name:'Show'},seasonNumber:1,episodeNumber:2,playback:{itemId:'episode'},durationMs:3000000,progressMs:600000}}],start:10,total:11,limit:1,hasMore:false,nextCursor:'',revision:{catalog:1,viewer:1},period:{days:30,start:'2026-09-22T00:00:00Z',end:'2026-10-22T00:00:00Z',metric:'actual',text:{code:'period',fallback:'Actual period'}}});
  const card=homeRowPresentation(source).cards[0];
  assert.strictEqual(card.entry,source.entries[0]);assert.strictEqual(card.resumeTarget,source.resumes![0].target);assert.equal(card.rank,11);
  assert.deepEqual(parts(homeTitleFactParts(card.entry,env)),[
    ['year','2023','value'],['genres','Drama','supplement'],['season-count','1 season','supplement'],
  ]);
  assert.deepEqual(homeTitleFactParts(card.resumeTarget!,env),[],'episode runtime cannot manufacture a show season count');
  const loneEpisode=entry('episode','episode',{show:{id:'show',name:'Show'},subtitle:'S1 E2 · supplied string',duration:3000});
  assert.deepEqual(homeTitleFactParts(loneEpisode,env),[],'a show name alone supplies no parent metadata');
});

test('Home adapter keeps music/audiobook contexts and does not reinterpret other display kinds',()=>{
  assert.deepEqual(parts(homeTitleFactParts(entry('album','album',{artist:{id:'artist',name:'Artist'},year:2020,genres:['Soul'],duration:900}),env)),[
    ['artist','Artist','identity'],['year','2020','value'],['genre-0','Soul','supplement'],['duration','15 min','value'],
  ]);
  assert.deepEqual(parts(homeTitleFactParts(entry('book','book',{author:'Author',year:1997,genres:['Fantasy'],duration:7200}),env)),[
    ['author','Author','identity'],['duration','2 h','value'],
  ]);
  for(const kind of ['author','book_series','collection','category'] as const)assert.deepEqual(homeTitleFactParts(entry('entry',kind,{year:2024,duration:7200}),env),[]);
});

test('focused episode uses the authoritative parent Home context without substituting identity or playback',()=>{
  const episode=entry('episode-real','episode',{title:'Actual episode',show:{id:'show-real',name:'Actual show'},year:2026,genres:['Episode genre'],duration:3000,progressSeconds:600,playback:{itemId:'file-real',startSeconds:600}});
  const focused:HomeTitlePresentationEntry=Object.freeze({...episode,homeTitleContext:Object.freeze({kind:'show',year:2023,genres:Object.freeze(['Drama','Mystery']),seasonCount:1})});
  assert.deepEqual(parts(homeTitleFactParts(focused,env)),[
    ['year','2023','value'],['genres','Drama, Mystery','supplement'],['season-count','1 season','supplement'],
  ]);
  assert.equal(focused.id,episode.id);assert.equal(focused.title,episode.title);assert.strictEqual(focused.show,episode.show);assert.strictEqual(focused.playback,episode.playback);
  assert.equal(focused.progressSeconds,600);assert.equal(focused.duration,3000);
  assert.deepEqual(parts(homeTitleFactParts(episode,env)),[
    ['year','2026','value'],['genres','Episode genre','supplement'],
  ],'absent context retains only the actual entry facts; it is not parent-show proof');
});

test('partial authoritative parent context omits unknown parent fields rather than borrowing episode facts',()=>{
  const episode=entry('episode','episode',{year:2026,genres:['Episode genre'],duration:3000,seasonCount:99});
  const sparse:HomeTitlePresentationEntry={...episode,homeTitleContext:{kind:'show',genres:[]}};
  assert.deepEqual(homeTitleFactParts(sparse,env),[],'a supplied context is authoritative even when sparse');
  assert.deepEqual(parts(homeTitleFactParts({...sparse,homeTitleContext:{kind:'show',genres:['Drama'],seasonCount:2}},env)),[
    ['genres','Drama','supplement'],['season-count','2 seasons','supplement'],
  ]);
  assert.deepEqual(parts(homeTitleFactParts({...sparse,homeTitleContext:{kind:'show',year:2023,genres:[]}},env)),[
    ['year','2023','value'],
  ]);
});

test('collection caption uses the authoritative summary and never reads bounded preview members',()=>{
  let memberReads=0;
  const source={collection:{id:'collection',libraryId:'movies',name:'Actual collection',itemCount:12,revision:1},mediaKind:'movie',firstYear:1901,lastTitle:'Unpreviewed last title',
    get members():readonly ContentEntry[]{memberReads++;throw new Error('A caption must not inspect preview members');}};
  assert.deepEqual(parts(homeCollectionCaption(source,env)),[
    ['collection-summary','12 films on your server, from 1901 to Unpreviewed last title.','context'],
  ]);
  assert.equal(memberReads,0);
  const unknownKind={collection:{id:'collection',libraryId:'movies',name:'Actual collection',itemCount:4,revision:1},members:[entry('preview','movie',{year:1995})],firstYear:1995,lastTitle:'Toy Story 5'} satisfies HomeCollectionCaptionSource & Readonly<{members:readonly ContentEntry[];collection:Readonly<{id:string;libraryId:string;name:string;itemCount:number;revision:number}>}>;
  assert.deepEqual(homeCollectionCaption(unknownKind,env),[],'the accepted base has no mediaKind; visible movies are not authoritative kind');
  const authoritative={...unknownKind,mediaKind:'movie'};
  assert.equal(homeCollectionCaption(authoritative,env)[0]?.text,'Four films on your server, from 1995 to Toy Story 5.');
  assert.equal(homeCollectionCaption({...authoritative,firstYear:undefined},env)[0]?.text,'Four films on your server.');
});

test('the narrow Home consumer delegates to canonical parts without old formatters, UI or endpoint access',async()=>{
  const source=await readFile(new URL('../src/presentation/home-spotlight.ts',import.meta.url),'utf8');
  assert.match(source,/return titleFactParts\(/);assert.match(source,/return collectionCaptionParts\(/);
  assert.doesNotMatch(source,/(?:from\s*|import\s*\()\s*['"][^'"]*(?:\.tsx|\.css|react|web\/|native\/)/);
  assert.doesNotMatch(source,/\b(?:fetch|homeHero|cardCaption|continueCaption)\s*\(|\.(?:split|join|sort)\s*\(/);
});
