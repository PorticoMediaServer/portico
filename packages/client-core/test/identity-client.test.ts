import {test} from 'node:test';
import assert from 'node:assert/strict';
import {IdentityClient,parseTwoFactorEnrolment} from '../src/identity-client.ts';

test('an enrolment must carry a base32 secret, an otpauth address and sane recovery codes',()=>{
 const ok={secret:'JBSWY3DPEHPK3PXPJBSWY3DP',uri:'otpauth://totp/Portico:sam?secret=JBSWY3DPEHPK3PXPJBSWY3DP&issuer=Portico',recoveryCodes:['aaaa-bbbb','cccc-dddd']};
 assert.equal(parseTwoFactorEnrolment(ok).recoveryCodes.length,2);
 assert.throws(()=>parseTwoFactorEnrolment({...ok,uri:'https://evil.example/totp'}));
 assert.throws(()=>parseTwoFactorEnrolment({...ok,secret:'not base32!'}));
 assert.throws(()=>parseTwoFactorEnrolment({...ok,recoveryCodes:['x']}));
});

test('requests go to the documented routes with exactly the documented bodies',async()=>{
 const calls:{path:string;method:string;body:unknown}[]=[];
 const api={request:async<T,>(path:string,method='GET',body?:unknown):Promise<T>=>{calls.push({path,method,body});
  if(path.endsWith('/two-factor/verify'))return {enabled:true,pendingEnrolment:false,recoveryCodesRemaining:8} as T;
  return undefined as T;}};
 const client=new IdentityClient(api);
 // C45 shapes: current password (plus code with two-step on) only in the password and two-step forms.
 await client.disableTwoFactor('secret','123456');await client.signOutEverywhere();await client.signOutDevice('d/1');
 await client.verifyTwoFactor('secret','654321');await client.changePassword('old-Pass1','new-Pass2');await client.changePassword('old-Pass1','new-Pass2','123456');
 await client.resetPIN('p-1','1234');
 assert.deepEqual(calls[0],{path:'/v1/direct/two-factor',method:'DELETE',body:{password:'secret',code:'123456'}});
 assert.deepEqual(calls[1],{path:'/v1/direct/sessions/sign-out-everywhere',method:'POST',body:undefined});
 assert.equal(calls[2].path,'/v1/devices/d%2F1/sessions');
 assert.deepEqual(calls[3],{path:'/v1/direct/two-factor/verify',method:'POST',body:{password:'secret',code:'654321'}});
 assert.deepEqual(calls[4].body,{currentPassword:'old-Pass1',newPassword:'new-Pass2'});
 assert.deepEqual(calls[5].body,{currentPassword:'old-Pass1',newPassword:'new-Pass2',code:'123456'});
 assert.deepEqual(calls[6],{path:'/v1/direct/profiles/p-1/pin-reset',method:'POST',body:{pin:'1234'}});
});

test('the device list is parsed from the envelope the server sends',async()=>{
 const device={id:'d1',installationId:'i'.repeat(32),name:'Living room',platform:'tvos',app:'Portico',appVersion:'0.1.0',ip:'192.168.2.41',firstSeen:'2026-08-01T10:00:00Z',lastSeen:'2026-09-17T10:00:00Z',lastProfileId:'',rememberAccount:true,current:true,sessions:1};
 const client=new IdentityClient({request:async<T,>():Promise<T>=>({items:[device]}) as T});
 const list=await client.devices('i'.repeat(32));
 assert.equal(list.length,1);assert.equal(list[0].name,'Living room');
});

test('rating systems are read from the {items} envelope the server sends',async()=>{
 const system={id:'mpaa',name:'MPA',region:'US',values:[{code:'PG',label:'PG',minimumAge:8}],screens:['movie']};
 const client=new IdentityClient({request:async<T,>():Promise<T>=>({items:[system]}) as T});
 assert.equal((await client.ratingSystems())[0].values[0].code,'PG');
});

const deviceRecord=(over:Record<string,unknown>={})=>({id:'dev-1',installationId:'i'.repeat(36),name:'This browser',platform:'web',app:'Portico Web',appVersion:'1',ip:'10.0.0.2',firstSeen:'2026-09-01T00:00:00Z',lastSeen:'2026-09-17T00:00:00Z',rememberAccount:true,lastProfileId:'',current:true,sessions:1,...over});

test('announcing a device registers then binds, and never costs a sign-in',async()=>{
 const {announceDevice}=await import('../src/identity-client.ts');
 const calls:{path:string;body:any}[]=[];let refuse:Error|undefined;
 const api={request:async<T,>(path:string,_method='GET',body?:unknown):Promise<T>=>{calls.push({path,body});if(refuse)throw refuse;return (path==='/v1/devices'?deviceRecord():undefined) as T;}};
 const description={installationId:'i'.repeat(36),name:'Chrome on Mac',platform:'web',app:'Portico Web',appVersion:'1'};
 assert.equal(await announceDevice(new IdentityClient(api),description,'fam-1'),'bound');
 assert.deepEqual(calls.map(c=>c.path),['/v1/devices','/v1/devices/dev-1/sessions/bind']);
 assert.deepEqual(calls[1].body,{installationId:'i'.repeat(36),sessionFamilyId:'fam-1'});
 refuse=new TypeError('Failed to fetch');
 assert.equal(await announceDevice(new IdentityClient(api),description,'fam-1'),'skipped');
 calls.length=0;
 assert.equal(await announceDevice(new IdentityClient(api),{...description,installationId:'short'},'fam-1'),'skipped');
 assert.equal(calls.length,0,'an installation id the server would refuse is never sent');
});

test('a picture upload is bounded before it is sent and needs a transport',async()=>{
 const api={request:async<T,>():Promise<T>=>undefined as T};
 await assert.rejects(new IdentityClient(api).uploadAvatar('p-1',new Uint8Array(10),'image/png'),/can’t send pictures/);
 const sent:{path:string;type:string}[]=[];
 const client=new IdentityClient(api,async(path,_body,type)=>{sent.push({path,type});return {profileId:'p-1',version:2,updatedAt:'2026-09-17T00:00:00Z',sourceMime:'image/png',url:'/v1/profiles/p-1/avatar?v=2'};});
 await assert.rejects(client.uploadAvatar('p-1',new Uint8Array(4*1024*1024+1),'image/png'),/smaller than 4 MB/);
 assert.equal((await client.uploadAvatar('p-1',new Uint8Array(10),'image/heic')).version,2);
 assert.deepEqual(sent,[{path:'/v1/direct/profiles/p-1/avatar',type:'application/octet-stream'}]);
});
