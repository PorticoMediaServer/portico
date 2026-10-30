import test from 'node:test';
import assert from 'node:assert/strict';
import {ownerResetFailure} from '../src/account-reset.ts';

test('only exact owner-reset endpoint outcomes are drawn as their specific state',()=>{
 assert.equal(ownerResetFailure({code:'owner_reset_file_unavailable'},'request'),'write');
 assert.equal(ownerResetFailure({code:'owner_reset_expired'},'verify'),'expired');
 assert.equal(ownerResetFailure({code:'rate_limited'},'verify'),'limited');
 assert.equal(ownerResetFailure({code:'unauthorized'},'verify'),'invalid');
 assert.equal(ownerResetFailure({code:'unauthorized'},'request'),undefined);
 assert.equal(ownerResetFailure({code:'owner_reset_expired'},'request'),undefined);
 assert.equal(ownerResetFailure({code:'owner_reset_file_unavailable'},'verify'),undefined);
});
test('no raw message, status, absent source or other sign-in outcome discloses reset expiry',()=>{
 for(const error of [undefined,null,new Error('owner_reset_expired'),{message:'owner_reset_expired'},{status:410},{code:'two_factor_required',status:401},{code:'owner_reset_expired\n'}])assert.equal(ownerResetFailure(error,'verify'),undefined);
 assert.equal(ownerResetFailure({code:'owner_reset_expired'},undefined),undefined);
});
