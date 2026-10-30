import test from 'node:test';
import assert from 'node:assert/strict';
import {entrySubject} from '../src/platform/playback/subject.ts';

test('Reading leaves refuse the audiovisual playback-subject boundary',()=>{
 for(const kind of ['ebook','comic'] as const)assert.throws(()=>entrySubject({id:'reading',kind,title:'Actual title'}),/invalid_playback_item/);
 assert.equal(entrySubject({id:'movie',kind:'movie',title:'Actual movie'}).kind,'video');
 assert.equal(entrySubject({id:'song',kind:'song',title:'Actual song'}).kind,'music');
});
