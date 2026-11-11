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

test('rating systems are read from the {items} envelope the server sends',async()=>{
 const system={id:'mpaa',name:'MPA',region:'US',values:[{code:'PG',label:'PG',minimumAge:8}],screens:['movie']};
 const client=new IdentityClient({request:async<T,>():Promise<T>=>({items:[system]}) as T});
 assert.equal((await client.ratingSystems())[0].values[0].code,'PG');
});

const deviceRecord=(over:Record<string,unknown>={})=>({id:'dev-1',installationId:'i'.repeat(36),name:'This browser',platform:'web',app:'Portico Web',appVersion:'1',ip:'10.0.0.2',firstSeen:'2026-09-01T00:00:00Z',lastSeen:'2026-09-17T00:00:00Z',rememberAccount:true,lastProfileId:'',current:true,sessions:1,...over});

test('a picture upload is bounded before it is sent and needs a transport',async()=>{
 const api={request:async<T,>():Promise<T>=>undefined as T};
 await assert.rejects(new IdentityClient(api).uploadAvatar('p-1',new Uint8Array(10),'image/png'),/can’t send pictures/);
 const sent:{path:string;type:string}[]=[];
 const client=new IdentityClient(api,async(path,_body,type)=>{sent.push({path,type});return {profileId:'p-1',version:2,updatedAt:'2026-09-17T00:00:00Z',sourceMime:'image/png',url:'/v1/profiles/p-1/avatar?v=2'};});
 await assert.rejects(client.uploadAvatar('p-1',new Uint8Array(4*1024*1024+1),'image/png'),/smaller than 4 MB/);
 assert.equal((await client.uploadAvatar('p-1',new Uint8Array(10),'image/heic')).version,2);
 assert.deepEqual(sent,[{path:'/v1/direct/profiles/p-1/avatar',type:'application/octet-stream'}]);
});
