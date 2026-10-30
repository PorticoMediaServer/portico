import {test} from 'node:test';
import assert from 'node:assert/strict';
import {
  parseGroupSnapshot,parseGroupList,parseGroupQueue,parseGroupTransportReceipt,parseGroupInvite,
  parseReceiver,parseReceiverList,parseReceiverGrant,parseHandoff,parseCastReceiverSession,
  groupTargetPositionUs,groupCorrection,GROUP_EVENT_KINDS,SOCIAL_PROTOCOL,
} from '../src/social-playback.ts';

const sync={noCorrectionUnderMs:750,rateCorrectionMinimum:'0.90',rateCorrectionMaximum:'1.10',rateCorrectionMaxMs:4000,seekAtOrOverMs:3000};
const timeline={itemId:'item',currentEntryId:'ent_1',state:'playing',anchorPositionUs:'1000000',anchorAt:'2026-09-16T20:00:00Z',rate:{numerator:'1',denominator:'1'},queuePosition:0,waitUntil:null};
const host={id:'mem_1',displayName:'Host',private:false,role:'host',state:'joined',readiness:'ready',positionUs:'1000000',reportedAt:'2026-09-16T20:00:00Z',presence:'connected',joinedAt:'2026-09-16T19:59:00Z'};
const guest={...host,id:'mem_2',displayName:'Guest',role:'member'};

function group(overrides:Record<string,unknown>={}){
  return {
    id:'grp_1',name:'Movie night',state:'playing',hostAuthority:'host-only',hostMemberId:'mem_1',
    revision:'7',playbackRevision:'3',settingsRevision:'1',governanceRevision:'1',queueRevision:'2',reconnectGeneration:'1',
    lastCommand:'play',lastCommandId:'play-1',endedReason:'',eventOrdinal:'9',
    createdAt:'2026-09-16T19:59:00Z',updatedAt:'2026-09-16T20:00:00Z',
    permissions:{isHost:true,canControl:true,canManageQueue:true},
    authority:{deviceId:'dev_1',playbackId:'pb1',state:'bound'},
    host:{presence:'connected',lastSeenAt:'2026-09-16T20:00:00Z',pauseAt:null,endAt:null},
    timeline,settings:{shuffleEnabled:false,repeatMode:'none'},sync,
    readiness:{aggregate:'ready',ready:2,buffering:0,lagging:0,stale:0,memberCount:2},
    members:[host,guest],queue:null,viewerMemberId:'mem_1',...overrides,
  };
}
const snapshot=(overrides:Record<string,unknown>={})=>({protocolVersion:SOCIAL_PROTOCOL,serverTime:'2026-09-16T20:00:00Z',group:group(overrides)});

test('a group snapshot parses and freezes', () => {
  const out=parseGroupSnapshot(snapshot());
  assert.equal(out.group.id,'grp_1');
  assert.equal(out.group.members.length,2);
  assert.equal(out.group.timeline.anchorPositionUs,'1000000');
  assert.ok(Object.isFrozen(out.group));
  assert.ok(Object.isFrozen(out.group.members));
});

test('a wrong protocol version is refused rather than coerced', () => {
  assert.throws(()=>parseGroupSnapshot({...snapshot(),protocolVersion:'2.0'}));
});

test('group state and timeline state must agree', () => {
  // A `playing` group whose timeline says paused leaves a client unable to decide
  // whether to extrapolate the anchor.
  assert.throws(()=>parseGroupSnapshot(snapshot({timeline:{...timeline,state:'paused'}})));
  const paused=parseGroupSnapshot(snapshot({state:'paused',timeline:{...timeline,state:'paused'}}));
  assert.equal(paused.group.timeline.state,'paused');
  const reconnecting=parseGroupSnapshot(snapshot({state:'host-reconnecting-playing'}));
  assert.equal(reconnecting.group.timeline.state,'playing');
  const lobby=parseGroupSnapshot(snapshot({state:'lobby',timeline:{...timeline,state:'idle'}}));
  assert.equal(lobby.group.state,'lobby');
});

test('the readiness aggregate must be the worst state present', () => {
  assert.throws(()=>parseGroupSnapshot(snapshot({readiness:{aggregate:'ready',ready:1,buffering:1,lagging:0,stale:0,memberCount:2}})));
  assert.throws(()=>parseGroupSnapshot(snapshot({readiness:{aggregate:'buffering',ready:1,buffering:0,lagging:1,stale:0,memberCount:2}})));
  const lagging=parseGroupSnapshot(snapshot({readiness:{aggregate:'lagging',ready:1,buffering:0,lagging:0,stale:1,memberCount:2}}));
  assert.equal(lagging.group.readiness.aggregate,'lagging');
  // The counts must add up to the joined members.
  assert.throws(()=>parseGroupSnapshot(snapshot({readiness:{aggregate:'ready',ready:1,buffering:0,lagging:0,stale:0,memberCount:2}})));
});

test('a host without control is an inconsistent snapshot', () => {
  assert.throws(()=>parseGroupSnapshot(snapshot({permissions:{isHost:true,canControl:false,canManageQueue:false}})));
});

test('two hosts in one group is refused', () => {
  assert.throws(()=>parseGroupSnapshot(snapshot({members:[host,{...guest,role:'host'}]})));
});

test('an unavailable queue entry is an opaque placeholder', () => {
  const queue=parseGroupQueue({revision:'4',position:0,entries:[
    {entryId:'ent_1',position:0,itemId:'item',unavailable:false,addedBy:'mem_1'},
    {entryId:'ent_2',position:1,itemId:null,unavailable:true,addedBy:''},
  ],eligibility:null});
  assert.equal(queue.entries[1].itemId,null);
  assert.equal(queue.entries[1].unavailable,true);
  // A placeholder that still carries an item id, or provenance, is a leak.
  assert.throws(()=>parseGroupQueue({revision:'4',position:0,entries:[{entryId:'ent_2',position:0,itemId:'secret',unavailable:true,addedBy:''}],eligibility:null}));
  assert.throws(()=>parseGroupQueue({revision:'4',position:0,entries:[{entryId:'ent_2',position:0,itemId:null,unavailable:true,addedBy:'mem_1'}],eligibility:null}));
  // A visible entry must name its item.
  assert.throws(()=>parseGroupQueue({revision:'4',position:0,entries:[{entryId:'ent_1',position:0,itemId:null,unavailable:false,addedBy:'mem_1'}],eligibility:null}));
  // Placeholders must preserve ordering: positions are dense and in order.
  assert.throws(()=>parseGroupQueue({revision:'4',position:0,entries:[{entryId:'ent_1',position:3,itemId:'item',unavailable:false,addedBy:''}],eligibility:null}));
});

test('the host eligibility summary parses', () => {
  const queue=parseGroupQueue({revision:'4',position:0,entries:[{entryId:'ent_1',position:0,itemId:null,unavailable:true,addedBy:''}],
    eligibility:{blockedEntries:1,currentEntryBlocked:true,members:[{memberId:'mem_2',displayName:'Guest',blockedEntryIds:['ent_1']}]}});
  assert.equal(queue.eligibility?.blockedEntries,1);
  assert.deepEqual([...queue.eligibility!.members[0].blockedEntryIds],['ent_1']);
});

test('a transport receipt distinguishes an acceptance from a replay', () => {
  const body={protocolVersion:SOCIAL_PROTOCOL,serverTime:'2026-09-16T20:00:01Z',groupId:'grp_1',idempotencyKey:'play-1',
    disposition:'accepted',groupState:'paused',command:'play',revision:'8',playbackRevision:'4',queueRevision:'2',recordedAt:'2026-09-16T20:00:01Z',
    timeline,settings:{shuffleEnabled:false,repeatMode:'none'},override:null};
  assert.equal(parseGroupTransportReceipt(body).disposition,'accepted');
  assert.equal(parseGroupTransportReceipt({...body,disposition:'duplicate'}).disposition,'duplicate');
  assert.throws(()=>parseGroupTransportReceipt({...body,disposition:'applied'}));
  const overridden=parseGroupTransportReceipt({...body,override:{reason:'host-override',blockedMemberIds:['mem_2']}});
  assert.equal(overridden.override?.reason,'host-override');
});

test('an invitation carries a readable code', () => {
  const invite=parseGroupInvite({protocolVersion:SOCIAL_PROTOCOL,serverTime:'2026-09-16T20:00:00Z',
    invite:{id:'inv_1',groupId:'grp_1',code:'KM7QX2TR',expiresAt:'2026-09-16T20:15:00Z',maxUses:8,uses:0,recipientProfileId:''}});
  assert.equal(invite.code,'KM7QX2TR');
  // Confusable glyphs are not in the alphabet, so they are not a valid code.
  assert.throws(()=>parseGroupInvite({protocolVersion:SOCIAL_PROTOCOL,serverTime:'x',
    invite:{id:'inv_1',groupId:'grp_1',code:'kM7qx2tr',expiresAt:'x',maxUses:8,uses:0,recipientProfileId:''}}));
});

test('a group list parses every entry', () => {
  const groups=parseGroupList({protocolVersion:SOCIAL_PROTOCOL,serverTime:'x',groups:[group(),group({id:'grp_2'})]});
  assert.deepEqual(groups.map(g=>g.id),['grp_1','grp_2']);
});

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

test('the group timeline extrapolates only while playing', () => {
  // One second of wall clock at rate 1 is one second of media.
  assert.equal(groupTargetPositionUs(timeline as never,1000,2000),1000000n+1000000n);
  assert.equal(groupTargetPositionUs({...timeline,state:'paused'} as never,1000,60000),1000000n);
  // Double rate advances twice as fast.
  assert.equal(groupTargetPositionUs({...timeline,rate:{numerator:'2',denominator:'1'}} as never,1000,2000),1000000n+2000000n);
  // A clock that has gone backwards never rewinds the anchor.
  assert.equal(groupTargetPositionUs(timeline as never,5000,1000),1000000n);
});

test('the correction bands pick one action each', () => {
  assert.equal(groupCorrection(sync,500_000n,0n).action,'none');
  const behind=groupCorrection(sync,1_500_000n,7_000_000n);
  assert.equal(behind.action,'rate');
  assert.equal(behind.rate,1.10);
  assert.equal(behind.holdMs,4000);
  const ahead=groupCorrection(sync,-1_500_000n,7_000_000n);
  assert.equal(ahead.rate,0.90);
  const far=groupCorrection(sync,4_000_000n,7_000_000n);
  assert.equal(far.action,'seek');
  assert.equal(far.seekToUs,'7000000');
  // The band boundary belongs to the cheaper correction on the low side and to
  // the seek on the high side, so no drift falls into both.
  assert.equal(groupCorrection(sync,750_000n,0n).action,'rate');
  assert.equal(groupCorrection(sync,3_000_000n,0n).action,'seek');
});

test('an unordered sync policy is refused', () => {
  assert.throws(()=>parseGroupSnapshot(snapshot({sync:{...sync,noCorrectionUnderMs:5000}})));
});

test('the event vocabulary is published for stream consumers', () => {
  assert.ok(GROUP_EVENT_KINDS.includes('group.transport'));
  assert.ok(GROUP_EVENT_KINDS.includes('resume-gap'));
  assert.ok(GROUP_EVENT_KINDS.includes('heartbeat'));
});

test('a public member carries its username; a private member never does, even if sent one', () => {
  const parsed = parseGroupSnapshot(snapshot({members: [{...host, username: 'host_user'}, {...guest, private: true, displayName: 'Viewer', username: 'leaked'}]}));
  assert.equal(parsed.group.members[0].username, 'host_user');
  assert.equal(parsed.group.members[1].username, undefined);
  assert.equal('username' in parsed.group.members[1], false);
  assert.equal(parseGroupSnapshot(snapshot()).group.members[0].username, undefined, 'absent is allowed');
  assert.throws(() => parseGroupSnapshot(snapshot({members: [{...host, username: 7}, guest]})));
});
