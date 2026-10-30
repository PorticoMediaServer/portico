import assert from 'node:assert/strict';
import test from 'node:test';
import {parseMembershipEndingNotice,membershipEndingWasShown,recordMembershipEndingShown} from '../src/membership-ending.ts';
const now=Date.parse('2026-10-25T12:00:00Z');
const notice={serverId:'srv_'+'A'.repeat(43),membershipId:'a'.repeat(32),sequence:12,cause:'owner_removed',serverName:'The cottage',expiresAt:'2026-11-24T12:00:00Z'};
test('only a closed original ending projection is admitted',()=>{
 assert.equal(parseMembershipEndingNotice(notice).sequence,12);
 for(const patch of [{cause:'invitation_expired'},{sequence:0},{sequence:2**53},{membershipId:'other'},{serverName:'name\nprivate'},{expiresAt:'not a date'},{route:'https://private.test'}])assert.throws(()=>parseMembershipEndingNotice({...notice,...patch}));
});
test('shown markers bind account, server, incarnation and sequence without dropping originals',()=>{
 const values=new Map<string,string>(),store={getItem:(key:string)=>values.get(key)??null,setItem:(key:string,value:string)=>{values.set(key,value);}};
 const event=parseMembershipEndingNotice(notice);
 assert.equal(membershipEndingWasShown(store,'account_A',event,now),false);
 assert.equal(recordMembershipEndingShown(store,'account_A',event,now),true);
 assert.equal(recordMembershipEndingShown(store,'account_A',event,now),false);
 assert.equal(membershipEndingWasShown(store,'account_B',event,now),false);
 for(const patch of [{serverId:'srv_'+'B'.repeat(43)},{membershipId:'b'.repeat(32)},{sequence:13}])assert.equal(membershipEndingWasShown(store,'account_A',parseMembershipEndingNotice({...notice,...patch}),now),false);
 const original=[...values.values()][0];
 assert.equal(recordMembershipEndingShown(store,'account_A',event,Date.parse(event.expiresAt)),false);
 assert.equal([...values.values()][0],original);
 values.set([...values.keys()][0],'broken');
 assert.throws(()=>recordMembershipEndingShown(store,'account_A',event,now));
 assert.equal([...values.values()][0],'broken');
});
