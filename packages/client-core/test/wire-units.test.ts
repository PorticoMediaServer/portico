import test from 'node:test';
import assert from 'node:assert/strict';
import {wireInstant,wireInstants,wireDurations,wireDisplayDurations,durationIntent} from '../src/wire-units.ts';
import {searchQueryProblem} from '../src/search.ts';

test('wire instants use RFC 3339 and keep internal UI milliseconds',()=>{
 assert.equal(wireInstant('2026-10-13T02:00:00.125Z'),Date.parse('2026-10-13T02:00:00.125Z'));
 assert.equal(wireInstant(''),0);
 assert.equal(wireInstant(null),null);
 assert.throws(()=>wireInstant(123));
 assert.throws(()=>wireInstant('yesterday'));
 assert.deepEqual(wireInstants({at:'2026-10-13T02:00:00Z',revision:2},['at']),{at:Date.parse('2026-10-13T02:00:00Z'),revision:2});
});
test('wire media times use bounded integer milliseconds in both directions',()=>{
 assert.deepEqual(wireDurations({durationMs:1250,progressMs:0},{duration:'durationMs',progressSeconds:'progressMs'}),{duration:1.25,progressSeconds:0});
 assert.deepEqual(durationIntent({progressSeconds:1.25,operationId:'op'},{progressSeconds:'progressMs'}),{progressMs:1250,operationId:'op'});
 for(const value of [-1,1.5,Number.MAX_SAFE_INTEGER+1])assert.throws(()=>wireDurations({durationMs:value},{duration:'durationMs'}));
 assert.throws(()=>wireDurations({duration:1},{duration:'durationMs'}));
});
test('search admits short titles and punctuated abbreviations',()=>{
 for(const q of ['X','神','M*A*S*H','WALL·E','spiderman'])assert.equal(searchQueryProblem(q),undefined);
 assert.equal(searchQueryProblem('***'),'short');
 assert.equal(searchQueryProblem('a b c d e f g h i'),'words');
});

test('display milliseconds retain readable fields and omit only malformed durations',()=>{
 const skipped:string[]=[];
 const row=wireDisplayDurations({id:'one',durationMs:1250,progressMs:-1},{duration:'durationMs',progressSeconds:'progressMs'},part=>skipped.push(part));
 assert.deepEqual(row,{id:'one',duration:1.25});
 assert.deepEqual(skipped,['progressMs']);
});
