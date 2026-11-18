import test from 'node:test';
import assert from 'node:assert/strict';
import {parseBookProgress,listeningSleepChoices} from '../src/listening.ts';
import {validateContentEntry} from '../src/library-content.ts';

const progress={bookId:'book',itemId:'part',partPositionMs:15000,positionMs:75000,durationMs:150000,remainingMs:75000,chapter:{index:0,number:3,title:'Actual chapter',startMs:10000,endMs:60000}};
const timeline={bookId:'book',libraryId:'books',revision:{catalog:2,viewer:3},parts:[{itemId:'part',title:'Part 2',offsetMs:60000,durationMs:90000}],totalCount:2,nextCursor:'next',durationMs:150000,progress,seek:{itemId:'part',startMs:15000}};
test('nested book progress stays in integer milliseconds while outer legacy display fields convert once',()=>{
 const entry=validateContentEntry({id:'book',kind:'book',title:'Title',durationMs:150000,progressMs:75000,bookProgress:progress});
 assert.equal(entry.duration,150);assert.equal(entry.progressSeconds,75);assert.deepEqual(entry.bookProgress,progress);assert.ok(Object.isFrozen(entry.bookProgress?.chapter));
 const part=validateContentEntry({id:'part',kind:'audiobook_file',title:'Part',bookProgress:progress});assert.deepEqual(part.bookProgress,progress);
 assert.equal(validateContentEntry({id:'another',kind:'book',title:'Other',bookProgress:progress}).bookProgress,undefined);
 for(const patch of [{partPositionMs:1.5},{positionMs:-1},{remainingMs:1},{chapter:{...progress.chapter,index:-1}},{chapter:{...progress.chapter,number:0}},{chapter:{...progress.chapter,number:undefined}},{chapter:{...progress.chapter,startMs:15001}},{chapter:{...progress.chapter,endMs:15000}}])assert.throws(()=>parseBookProgress({...progress,...patch}));
 assert.deepEqual(parseBookProgress({bookId:'book',itemId:'part',partPositionMs:15}),{bookId:'book',itemId:'part',partPositionMs:15},'missing totals and chapter marks stay absent');
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
