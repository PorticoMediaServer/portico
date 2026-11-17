import {test} from 'node:test';
import assert from 'node:assert/strict';
import {parseReceiver,parseReceiverList,parseReceiverGrant,parseHandoff,parseCastReceiverSession,SOCIAL_PROTOCOL} from '../src/social-playback.ts';

const receiver={id:'rcv_1',kind:'portico',deviceId:'tv-1',displayName:'Living room',platform:'androidtv',
  keyFingerprint:'A'.repeat(43),supportedCommands:['load','play','pause','seek','stop'],grantPolicy:'per-device',
  authorizationRevision:'1',state:'active',presence:'online',lastSeenAt:'x',createdAt:'x'};

test('a receiver and the directory parse', () => {
  assert.equal(parseReceiver(receiver).kind,'portico');
  const list=parseReceiverList({protocolVersion:SOCIAL_PROTOCOL,serverTime:'x',receivers:[receiver,{...receiver,id:'rcv_2',kind:'cast'}]});
  assert.deepEqual(list.map(r=>r.kind),['portico','cast']);
  assert.throws(()=>parseReceiver({...receiver,kind:'dlna'}));
});

test('an accepted grant that cannot load is refused', () => {
  const grant={id:'grt_1',receiverId:'rcv_1',controllerDeviceId:'phone-1',controllerDisplayName:'Phone',
    receiverKeyFingerprint:'A'.repeat(43),allowedCommands:['load','play','pause'],authorizationRevision:'1',
    state:'accepted',expiresAt:'x',createdAt:'x',decidedAt:'x'};
  assert.equal(parseReceiverGrant({protocolVersion:SOCIAL_PROTOCOL,serverTime:'x',grant}).state,'accepted');
  assert.throws(()=>parseReceiverGrant({grant:{...grant,allowedCommands:['play','pause']}}));
  // A pending grant has no decision timestamp yet.
  assert.equal(parseReceiverGrant({grant:{...grant,state:'pending',decidedAt:null}}).decidedAt,null);
});

const handoff={id:'hdf_1',receiverId:'rcv_1',grantId:'grt_1',requestId:'handoff-1',state:'prepared',outcome:'waiting',
  reason:'',revision:'1',itemId:'item',sourcePlaybackId:'pb_1',receiverPlaybackId:'',
  requestedPositionUs:'1000000',readyPositionUs:'0',committedPositionUs:'0',sourceRetired:false,
  createdAt:'x',expiresAt:'y',settledAt:null};

test('handoff phases parse and the invariants hold', () => {
  assert.equal(parseHandoff({protocolVersion:SOCIAL_PROTOCOL,serverTime:'x',handoff}).outcome,'waiting');
  const ready=parseHandoff({handoff:{...handoff,outcome:'pending',revision:'2',receiverPlaybackId:'pb_2',readyPositionUs:'1500000'}});
  assert.equal(ready.state,'prepared');
  assert.equal(ready.sourceRetired,false);
  const committed=parseHandoff({handoff:{...handoff,state:'committed',outcome:'accepted',revision:'3',
    receiverPlaybackId:'pb_2',readyPositionUs:'1500000',committedPositionUs:'1500000',sourceRetired:true,settledAt:'z'}});
  assert.equal(committed.committedPositionUs,'1500000');
  assert.equal(committed.sourceRetired,true);
});

test('a handoff that claims the source is retired before commit is refused', () => {
  assert.throws(()=>parseHandoff({handoff:{...handoff,sourceRetired:true}}));
  assert.throws(()=>parseHandoff({handoff:{...handoff,outcome:'accepted'}}));
  // Readiness without a receiver occurrence is not readiness.
  assert.throws(()=>parseHandoff({handoff:{...handoff,outcome:'pending'}}));
  // A commit must land at the position the receiver actually proved.
  assert.throws(()=>parseHandoff({handoff:{...handoff,state:'committed',outcome:'accepted',sourceRetired:true,
    receiverPlaybackId:'pb_2',readyPositionUs:'1500000',committedPositionUs:'9000000'}}));
});

test('a rolled-back handoff leaves the source alone', () => {
  const rolled=parseHandoff({handoff:{...handoff,state:'rolled_back',outcome:'rejected',reason:'receiver-load-failed',settledAt:'z'}});
  assert.equal(rolled.sourceRetired,false);
  assert.equal(rolled.reason,'receiver-load-failed');
});

test('a cast receiver session carries two distinct credentials', () => {
  const body={protocolVersion:SOCIAL_PROTOCOL,serverTime:'x',grantSemantics:'initial',deviceToken:'d'.repeat(50),
    device:{id:'cdv_1',deviceId:'cast-1',displayName:'TV',receiverId:'rcv_1',state:'active',generation:'1',expiresAt:'y'},
    scope:{accountId:'account',profileId:'profile',authority:'local',capabilities:['load','progress']},
    session:{accessToken:'s'.repeat(50),expiresAt:'y'},applicationId:'ABCD1234'};
  const out=parseCastReceiverSession(body);
  assert.equal(out.grantSemantics,'initial');
  assert.notEqual(out.deviceToken,out.session.accessToken);
  assert.equal(parseCastReceiverSession({...body,grantSemantics:'rotation'}).grantSemantics,'rotation');
  // A server that returns one value for both credentials has collapsed the
  // device-scoped token into a bearer.
  assert.throws(()=>parseCastReceiverSession({...body,deviceToken:body.session.accessToken}));
  assert.throws(()=>parseCastReceiverSession({...body,grantSemantics:'renewal'}));
});
