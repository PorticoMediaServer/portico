import {routeOrigin} from './route-identity.ts';
import {formatShortInvitationCode} from './short-invitation-code.ts';

/** Invitation secrets stay in memory; id is a public expectation, not authority to join. The code is the old long
 * secret or, on the rebuilt server, the invitation's own code (K7QP-2MXD-9HRT-4WNC), kept in that spelling. */
export type JoinLink=Readonly<{code:string;server:string;id?:string}>;
export function parseJoinLink(url:string):JoinLink|undefined {
 if(typeof url!=='string'||url.length>2048)return;
 const match=/^(?:portico:\/\/join\/?|https:\/\/web\.getportico\.tv\/join\/?)#(.+)$/.exec(url);
 if(!match)return;
 const pairs=new Map<string,string>();
 for(const part of match[1].split('&')){
  const at=part.indexOf('=');if(at<1)return;
  const key=part.slice(0,at);if(!['code','server','id'].includes(key)||pairs.has(key))return;
  try{pairs.set(key,decodeURIComponent(part.slice(at+1)));}catch{return;}
 }
 const raw=pairs.get('code'),server=pairs.get('server'),id=pairs.get('id');
 const code=raw&&(/^[A-Za-z0-9_-]{32,128}$/.test(raw)?raw:formatShortInvitationCode(raw));
 if(!code||!server||id!==undefined&&!/^srv_[A-Za-z0-9_-]{43}$/.test(id))return;
 try{return {code,server:routeOrigin(server),...(id===undefined?{}:{id})};}catch{return;}
}
