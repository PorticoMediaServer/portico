import test from 'node:test';
import {shouldRetireAccountSession} from '../src/profile-management.ts';
import assert from 'node:assert/strict';

test('account cleanup cannot sign out the accepted viewer family',()=>{
 const account={accessToken:'account',sessionFamilyId:'family'};
 assert.equal(shouldRetireAccountSession(account,account),false);
 assert.equal(shouldRetireAccountSession(account,{accessToken:'renewed',sessionFamilyId:'family'}),false);
 assert.equal(shouldRetireAccountSession(undefined,account),false);
 assert.equal(shouldRetireAccountSession(account,{accessToken:'viewer',sessionFamilyId:'viewer-family'}),true);
});
