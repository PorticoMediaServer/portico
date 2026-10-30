import test from 'node:test';
import assert from 'node:assert/strict';
import {parseBookProgress,parseBookTimeline,readBookTimeline,listeningSleepChoices} from '../src/listening.ts';
import {validateContentEntry} from '../src/library-content.ts';
import {validateHomeDocument,validateHomeRow} from '../src/home.ts';

const progress={bookId:'book',itemId:'part',partPositionMs:15000,positionMs:75000,durationMs:150000,remainingMs:75000,chapter:{index:0,title:'Actual chapter',startMs:10000,endMs:60000}};
const timeline={bookId:'book',libraryId:'books',revision:{catalog:2,viewer:3},parts:[{itemId:'part',title:'Part 2',offsetMs:60000,durationMs:90000}],totalCount:2,nextCursor:'next',durationMs:150000,progress,seek:{itemId:'part',startMs:15000}};
test('nested book progress stays in integer milliseconds while outer legacy display fields convert once',()=>{
 const entry=validateContentEntry({id:'book',kind:'book',title:'Title',durationMs:150000,progressMs:75000,bookProgress:progress});
 assert.equal(entry.duration,150);assert.equal(entry.progressSeconds,75);assert.deepEqual(entry.bookProgress,progress);assert.ok(Object.isFrozen(entry.bookProgress?.chapter));
 const part=validateContentEntry({id:'part',kind:'audiobook_file',title:'Part',bookProgress:progress});assert.deepEqual(part.bookProgress,progress);
 assert.equal(validateContentEntry({id:'another',kind:'book',title:'Other',bookProgress:progress}).bookProgress,undefined);
 for(const patch of [{partPositionMs:1.5},{positionMs:-1},{remainingMs:1},{chapter:{...progress.chapter,index:-1}},{chapter:{...progress.chapter,startMs:15001}},{chapter:{...progress.chapter,endMs:15000}}])assert.throws(()=>parseBookProgress({...progress,...patch}));
 assert.deepEqual(parseBookProgress({bookId:'book',itemId:'part',partPositionMs:15}),{bookId:'book',itemId:'part',partPositionMs:15},'missing totals and chapter marks stay absent');
});
test('book timelines preserve exact targets, bounded pages and scope while dropping private extras',async()=>{
 const out=parseBookTimeline({...timeline,privatePath:'/private',parts:[{...timeline.parts[0],path:'/private/book'}]},'book');
 assert.equal(out.seek?.itemId,'part');assert.equal(out.parts[0].offsetMs,60000);assert.equal((out.parts[0] as any).path,undefined);assert.ok(Object.isFrozen(out.parts));
 for(const raw of [{...timeline,bookId:'other'},{...timeline,parts:Array(101).fill(timeline.parts[0])},{...timeline,parts:[timeline.parts[0],timeline.parts[0]]},{...timeline,parts:[{...timeline.parts[0],offsetMs:NaN}]}])assert.throws(()=>parseBookTimeline(raw,'book'));
 const scope={serverId:'server',authority:'local' as const,accountId:'a',profileId:'p',controllerId:'c',controllerEpoch:'e',commandLaneId:'l'};
 const calls:string[]=[];await readBookTimeline(async path=>{calls.push(path);return{scope,data:timeline};},scope,'book',{cursor:'next',positionMs:75000,limit:1});
 assert.match(calls[0],/^\/v1\/books\/book\/timeline\?/);assert.match(calls[0],/positionMs=75000/);
 await assert.rejects(readBookTimeline(async()=>({scope:{...scope,profileId:'different'},data:timeline}),scope,'book'));
});
test('book and music metadata survive shared content parsing without fabricated facts',()=>{
 const book=validateContentEntry({id:'book',kind:'book',title:'Title (Abridged)',displayTitle:'Title',edition:'Abridged',narrators:['Reader'],series:[{name:'Saga',position:'2.5'}],publisher:'Press',language:'en',publishedYear:2020});
 assert.equal(book.title,'Title (Abridged)');assert.equal(book.displayTitle,'Title');assert.equal(book.series?.[0].position,'2.5');assert.equal(book.narrators?.[0],'Reader');
 const artist=validateContentEntry({id:'artist',kind:'artist',title:'Artist',artistType:'Group',albumCount:37});assert.equal(artist.albumCount,37);assert.equal(artist.artistType,'Group');
 const song=validateContentEntry({id:'song',kind:'song',title:'Song',favorite:true});assert.equal(song.favorite,true);
 assert.equal(validateContentEntry({id:'artist',kind:'artist',title:'Artist'}).artistType,undefined);
 const list=validateContentEntry({id:'list',kind:'playlist',title:'List',navigation:{view:'playlist',entityId:'list'}});assert.equal(list.navigation?.view,'playlist');
});
test('books offer chapter and book boundaries; songs retain their item boundary',()=>{
 assert.deepEqual(listeningSleepChoices('audiobook_file'),['off',15,30,45,60,90,'chapter','book']);
 assert.deepEqual(listeningSleepChoices('song'),['off',15,30,45,60,90,'item']);
});

test('Home document and paginated row keep embedded book progress and the actual part target',()=>{
 const book={id:'book',kind:'book',title:'Book',libraryId:'books',durationMs:150000,progressMs:75000,bookProgress:progress,playback:{itemId:'part',startMs:15000}};
 const row={id:'continue_books',viewerFence:'f',title:'Continue your books',kind:'continue',artworkShape:'square',entries:[book],total:1,start:0,limit:2,hasMore:false,nextCursor:'',revision:{catalog:1,viewer:1}};
 const parsed=validateHomeRow(row);
 const doc=validateHomeDocument({serverId:'s',viewerFence:'f',spotlight:[],rows:[row],layout:{revision:1,rowOrder:[],hiddenRowIds:[]},revision:{catalog:1,viewer:1},generatedAt:'2026-10-22T00:00:00Z'});
 for(const entry of [parsed.entries[0],doc.rows[0].entries[0]]){assert.deepEqual(entry.bookProgress,progress);assert.equal(entry.id,'book');assert.equal(entry.playback?.itemId,'part');assert.equal(entry.playback?.startSeconds,15);assert.equal(entry.duration,150);assert.equal(entry.progressSeconds,75);}
});
