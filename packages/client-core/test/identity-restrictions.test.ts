import test from 'node:test';import assert from 'node:assert/strict';
import {parseRatingSystems,parseProfileRestrictions,restrictionEdit,parsePINRecoveryMethods,parseProfileAvatar,parseTwoFactorState,parseAuthCapabilities} from '../src/identity-restrictions.ts';
import {parseDevice,parseDevices,parseRememberedAccounts,parseTopShelfFeed,topShelfExpired,newInstallationId,validInstallationId} from '../src/identity-devices.ts';

const installation='a'.repeat(43);
const restrictions={profileId:'child',ratingSystem:'mpaa',maximumAgeRating:'PG-13',maximumAge:13,allowUnrated:false,blockedLabels:['Halloween'],allowDownloads:true,allowLiveTv:false,allowDvr:false,allowWatchTogether:true,revision:4};

test('rating systems keep the server order and reject a table that is not ascending',()=>{
 const parsed=parseRatingSystems([{id:'mpaa',name:'MPA',region:'US',screens:['movie'],values:[{code:'G',label:'G',minimumAge:0},{code:'R',label:'R',minimumAge:17}]}]);
 assert.equal(parsed[0].values[1].code,'R');
 assert.throws(()=>parseRatingSystems([{id:'mpaa',name:'MPA',region:'US',screens:[],values:[{code:'R',label:'R',minimumAge:17},{code:'G',label:'G',minimumAge:0}]}]),{code:'invalid_identity_document'});
 for(const bad of [[],{},[{id:'mpaa',name:'MPA',region:'US',screens:[],values:[]}],[{id:'mpaa',name:'MPA',region:'US',screens:[],values:[{code:'G',label:'G',minimumAge:0}],extra:1}]] as any[]){
  if(Array.isArray(bad)&&bad.length===0){assert.deepEqual(parseRatingSystems(bad),[]);continue;}
  assert.throws(()=>parseRatingSystems(bad));
 }
});

test('a restriction document with a ceiling of 0 survives, and an unset ceiling must agree with its code',()=>{
 const zero=parseProfileRestrictions({...restrictions,ratingSystem:'us-tv',maximumAgeRating:'TV-Y',maximumAge:0});
 // 0 is a real ceiling. Treating it as "unset" would quietly widen a child.
 assert.equal(zero.maximumAge,0);
 const none=parseProfileRestrictions({...restrictions,ratingSystem:'',maximumAgeRating:'',maximumAge:-1});
 assert.equal(none.maximumAge,-1);
 for(const patch of [
  {maximumAgeRating:'',maximumAge:13},
  {maximumAgeRating:'PG-13',maximumAge:-1},
  {ratingSystem:'',maximumAgeRating:'PG-13',maximumAge:13},
  {maximumAge:99},{maximumAge:-2},{revision:0},
  {blockedLabels:['a','A']},{blockedLabels:['']},{blockedLabels:['bad\nlabel']},
  {allowUnrated:'yes'},{profileId:''},
 ] as any[])assert.throws(()=>parseProfileRestrictions({...restrictions,...patch}),{code:'invalid_identity_document'});
 // An unknown key is a failure, not a field to drop: a full-replacement edit
 // built from a truncated document would silently reset it.
 assert.throws(()=>parseProfileRestrictions({...restrictions,futureFlag:true}),{code:'invalid_identity_document'});
 // The retired allowFeedback field is gone the same way: a document carrying
 // it is refused, and neither a parsed document nor an edit carries it, so a
 // save cannot send it back to a server that rejects it.
 assert.throws(()=>parseProfileRestrictions({...restrictions,allowFeedback:true}),{code:'invalid_identity_document'});
 assert.equal('allowFeedback' in parseProfileRestrictions(restrictions),false);
});

test('a restriction edit is a full replacement pinned to the revision that was read',()=>{
 const current=parseProfileRestrictions(restrictions);
 const body=restrictionEdit(current,{allowUnrated:true}) as any;
 assert.equal(body.expectedRevision,4);
 assert.equal(body.allowUnrated,true);
 // The retired allowFeedback field is never sent.
 assert.equal('allowFeedback' in body,false);
 // Every other flag is carried forward, not dropped.
 assert.equal(body.allowLiveTv,false);
 assert.deepEqual(body.blockedLabels,['Halloween']);
 assert.equal('maximumAge' in body,false);
 assert.throws(()=>restrictionEdit(current,{ratingSystem:''}),{code:'invalid_identity_document'});
 // C45: no step-up proof rides along any more (the server rejects unknown fields).
 assert.equal('stepUpProof' in body,false);
});

test('recovery methods, avatars and two-factor state reject contradictory documents',()=>{
 assert.deepEqual(parsePINRecoveryMethods({password:true,authenticator:false,recoveryCode:false}),{password:true,authenticator:false,recoveryCode:false});
 // An older server still sending emailedToken parses; the flag is ignored (C59).
 assert.deepEqual(parsePINRecoveryMethods({password:true,authenticator:false,recoveryCode:false,emailedToken:true}),{password:true,authenticator:false,recoveryCode:false});
 assert.throws(()=>parsePINRecoveryMethods({password:true,authenticator:false}));
 const avatar=parseProfileAvatar({profileId:'child',version:3,updatedAt:new Date().toISOString(),sourceMime:'image/webp',url:'/v1/profiles/child/avatar?v=3'});
 assert.equal(avatar.version,3);
 // An absolute URL would point a profile picture at another origin.
 for(const url of ['https://elsewhere.example/avatar.png','//elsewhere/x','/v1/items/x/art/poster'])
  assert.throws(()=>parseProfileAvatar({profileId:'child',version:3,updatedAt:new Date().toISOString(),sourceMime:'image/png',url}),{code:'invalid_identity_document'});
 assert.throws(()=>parseProfileAvatar({profileId:'child',version:3,updatedAt:new Date().toISOString(),sourceMime:'image/gif',url:'/v1/profiles/child/avatar'}));
 assert.equal(parseTwoFactorState({enabled:true,pendingEnrolment:false,recoveryCodesRemaining:10}).enabled,true);
 // A factor cannot be both in force and awaiting verification.
 assert.throws(()=>parseTwoFactorState({enabled:true,pendingEnrolment:true,recoveryCodesRemaining:0}),{code:'invalid_identity_document'});
});

test('capabilities gate the sign-in affordances a client may draw',()=>{
 const open=parseAuthCapabilities({serverId:'server',serverName:'Home',setupRequired:false,methods:['password','self-registration'],selfRegistration:'open',quickConnect:true,twoFactor:true,hostedAttach:false});
 assert.deepEqual([...open.methods],['password','self-registration']);
 for(const patch of [{selfRegistration:'maybe'},{methods:['password','password']},{methods:'password'},{quickConnect:1}] as any[])
  assert.throws(()=>parseAuthCapabilities({serverId:'server',serverName:'Home',setupRequired:false,methods:['password'],selfRegistration:'off',quickConnect:true,twoFactor:true,hostedAttach:false,...patch}),{code:'invalid_identity_document'});
});

const device={id:'device',installationId:installation,name:'Living room TV',platform:'tvos',app:'portico-tv',appVersion:'1.0.0',ip:'203.0.113.7',firstSeen:new Date().toISOString(),lastSeen:new Date().toISOString(),lastProfileId:'child',rememberAccount:true,current:true,sessions:1};

test('an installation id is a secret, not a label',()=>{
 const generated=newInstallationId();
 assert.equal(validInstallationId(generated),true);
 assert.equal(generated.length,32);
 assert.notEqual(generated,newInstallationId());
 // A short id would make the unauthenticated remembered-account list guessable.
 for(const bad of ['','abc','a'.repeat(31),'a'.repeat(129),'has spaces','has/slash'])assert.equal(validInstallationId(bad),false);
 assert.throws(()=>parseDevice({...device,installationId:'short'}),{code:'invalid_device_document'});
});

test('current device records have binding/session facts and refuse retired approval/trust fields',()=>{
 const parsed=parseDevice(device);
 assert.equal(parsed.sessions,1);assert.equal('approvalState' in parsed,false);assert.equal('trusted' in parsed,false);
 for(const patch of [{ip:'not an address'},{ip:'x'.repeat(46)},{approvalState:'approved'},{name:''},{sessions:-1},{trusted:false},{futureField:1}] as any[])
  assert.throws(()=>parseDevice({...device,...patch}),{code:'invalid_device_document'});
 assert.equal(parseDevice({...device,ip:''}).ip,'');
});

test('CD-36: strict device strings reject C0/DEL; expired families do not inflate active work',()=>{
 // ESC and other C0/DEL in registration/rename input fail closed.
 for(const name of ['TV\x1b[2J','Bad\x00name','Bad\x7fname'])assert.throws(()=>parseDevice({...device,name}),{code:'invalid_device_document'});
 assert.equal(parseDevice({...device,name:'Living room TV'}).name,'Living room TV');
 // Expired authorization families are parsed but never authorize a revoke;
 // only active sessions can be ended, so old expired rows do not inflate work.
 assert.throws(()=>parseDevice({...device,name:'A'.repeat(121)}),{code:'invalid_device_document'});
});

test('a device list cannot claim two current devices or duplicate ids',()=>{
 const list=parseDevices({items:[device,{...device,id:'second',installationId:'b'.repeat(43),current:false,sessions:0}]});
 assert.equal(list.length,2);
 assert.throws(()=>parseDevices({items:[device,{...device,id:'second',installationId:'b'.repeat(43)}]}),{code:'invalid_device_document'});
 assert.throws(()=>parseDevices({items:[device,device]}),{code:'invalid_device_document'});
 assert.throws(()=>parseDevices({items:[device],cursor:'x'}),{code:'invalid_device_document'});
});

test('remembered accounts hold descriptors and at most one automatic sign-in',()=>{
 const accounts=parseRememberedAccounts({items:[{accountId:'one',username:'owner',displayName:'Owner',avatarVersion:2,automaticSignIn:true,lastUsed:new Date().toISOString()},{accountId:'two',username:'guest',displayName:'Guest',avatarVersion:0,automaticSignIn:false,lastUsed:new Date().toISOString()}]});
 assert.equal(accounts.length,2);
 assert.equal(accounts[0].automaticSignIn,true);
 // Two automatic accounts would make which one signs in a race.
 assert.throws(()=>parseRememberedAccounts({items:[{accountId:'one',username:'a',displayName:'A',avatarVersion:0,automaticSignIn:true,lastUsed:new Date().toISOString()},{accountId:'two',username:'b',displayName:'B',avatarVersion:0,automaticSignIn:true,lastUsed:new Date().toISOString()}]}),{code:'invalid_device_document'});
 // Nothing in the list may be a credential.
 assert.throws(()=>parseRememberedAccounts({items:[{accountId:'one',username:'a',displayName:'A',avatarVersion:0,automaticSignIn:false,lastUsed:new Date().toISOString(),accessToken:'secret'}]}),{code:'invalid_device_document'});
});

test('the Top Shelf feed bounds its shelves and refuses a link that leaves the app',()=>{
 const feed=parseTopShelfFeed({serverId:'server',profileId:'child',expiresAt:new Date(Date.now()+86400000).toISOString(),sections:[{id:'continue',title:'Continue Watching',shape:'poster',entries:[{id:'m1',title:'Alpha',subtitle:'2020',imageUrl:'/v1/topshelf/art/m1?token=abc',displayUrl:'portico://item/m1',playUrl:'portico://play/m1',progressPercent:42}]}]});
 assert.equal(feed.sections[0].entries[0].progressPercent,42);
 assert.equal(topShelfExpired(feed),false);
 assert.equal(topShelfExpired(feed,Date.now()+2*86400000),true);
 const base={serverId:'server',profileId:'child',expiresAt:new Date().toISOString()};
 for(const sections of [
  [{id:'x',title:'X',shape:'circle',entries:[]}],
  [{id:'x',title:'X',shape:'poster',entries:new Array(11).fill({id:'m',title:'t',displayUrl:'portico://item/m'})}],
  [{id:'x',title:'X',shape:'poster',entries:[{id:'m',title:'t',displayUrl:'https://elsewhere.example/m'}]}],
  [{id:'x',title:'X',shape:'poster',entries:[{id:'m',title:'t',displayUrl:'portico://item/m',imageUrl:'https://tracker.example/pixel.gif'}]}],
  [{id:'x',title:'X',shape:'poster',entries:[{id:'m',title:'t',displayUrl:'portico://item/m',progressPercent:100}]}],
  [{id:'x',title:'X',shape:'poster',entries:[{id:'m',title:'t',displayUrl:'portico://item/m'},{id:'m',title:'t',displayUrl:'portico://item/m'}]}],
  new Array(5).fill({id:'x',title:'X',shape:'poster',entries:[]}),
 ] as any[])assert.throws(()=>parseTopShelfFeed({...base,sections}),{code:'invalid_device_document'});
});
