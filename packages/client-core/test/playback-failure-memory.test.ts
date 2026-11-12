import test from 'node:test';
import {shouldRetireAccountSession} from '../src/profile-management.ts';
import assert from 'node:assert/strict';
import {tmdbAbsoluteOrder} from '../src/screen-metadata.ts';

test('anime order offsets stay explicit and bounded',()=>{
 assert.equal(tmdbAbsoluteOrder('official',-12),'tmdb-absolute:official:-12');
 assert.equal(tmdbAbsoluteOrder('tmdb-group:dvd',5),'tmdb-absolute:tmdb-group:dvd:5');
 assert.equal(tmdbAbsoluteOrder('official',0),'official');
 assert.throws(()=>tmdbAbsoluteOrder('official',1000001));
 assert.throws(()=>tmdbAbsoluteOrder('https://outside',1));
});
test('account cleanup cannot sign out the accepted viewer family',()=>{
 const account={accessToken:'account',sessionFamilyId:'family'};
 assert.equal(shouldRetireAccountSession(account,account),false);
 assert.equal(shouldRetireAccountSession(account,{accessToken:'renewed',sessionFamilyId:'family'}),false);
 assert.equal(shouldRetireAccountSession(undefined,account),false);
 assert.equal(shouldRetireAccountSession(account,{accessToken:'viewer',sessionFamilyId:'viewer-family'}),true);
});
