/** An explicit own-account directory projection. It is never derived from an
 * empty list, HTTP refusal, expired invitation or account-initiated Departure. */
export type MembershipEndingNotice = Readonly<{serverId:string;membershipId:string;sequence:number;cause:'owner_removed';serverName?:string;expiresAt:string}>;
const object=(raw:unknown):raw is Record<string,unknown>=>!!raw&&typeof raw==='object'&&!Array.isArray(raw);
export function parseMembershipEndingNotice(raw:unknown):MembershipEndingNotice {
 if(!object(raw)||Object.keys(raw).some(key=>!['serverId','membershipId','sequence','cause','serverName','expiresAt'].includes(key))||typeof raw.serverId!=='string'||!/^srv_[A-Za-z0-9_-]{43}$/.test(raw.serverId)||typeof raw.membershipId!=='string'||!/^[0-9a-f]{32}$/.test(raw.membershipId)||!Number.isSafeInteger(raw.sequence)||Number(raw.sequence)<1||raw.cause!=='owner_removed'||typeof raw.expiresAt!=='string'||raw.expiresAt.length>64||!/^\d{4}-\d\d-\d\dT.*Z$/.test(raw.expiresAt)||!Number.isFinite(Date.parse(raw.expiresAt))||raw.serverName!==undefined&&(typeof raw.serverName!=='string'||!raw.serverName.length||raw.serverName.length>2048||/[\x00-\x1f\x7f]/.test(raw.serverName)))throw new Error('Invalid membership ending.');
 return Object.freeze({serverId:raw.serverId,membershipId:raw.membershipId,sequence:Number(raw.sequence),cause:'owner_removed',expiresAt:raw.expiresAt,...(typeof raw.serverName==='string'?{serverName:raw.serverName}:{})});
}
export const membershipEndingIdentity=(notice:MembershipEndingNotice)=>JSON.stringify([notice.serverId,notice.membershipId,notice.sequence]);
type MarkerStore={getItem(key:string):string|null;setItem(key:string,value:string):void};
const markerKey=(accountId:string)=>'portico.services.ending-shown.v1:'+accountId;
type Marker={id:string;expires:number};
function markers(store:MarkerStore,accountId:string):Marker[] {
 if(!/^[A-Za-z0-9_-]{1,160}$/.test(accountId))throw new Error('Invalid account scope.');
 const value=store.getItem(markerKey(accountId));if(value===null)return [];
 if(value.length>1<<20)throw new Error('Invalid ending marker.');
 const raw:unknown=JSON.parse(value);
 if(!object(raw)||raw.version!==1||Object.keys(raw).some(key=>key!=='version'&&key!=='events')||!Array.isArray(raw.events)||raw.events.length>2048)throw new Error('Invalid ending marker.');
 return raw.events.map(value=>{if(!object(value)||Object.keys(value).some(key=>key!=='id'&&key!=='expires')||typeof value.id!=='string'||value.id.length>256||typeof value.expires!=='number'||!Number.isSafeInteger(value.expires)||value.expires<1)throw new Error('Invalid ending marker.');return{id:value.id,expires:value.expires};});
}
/** Call only while holding the account/browser Web Lock. A failed or malformed
 * store suppresses the notice. Shown identities are never evicted for new ones;
 * the bounded full store suppresses further notices instead of repeating one. */
export function membershipEndingWasShown(store:MarkerStore,accountId:string,notice:MembershipEndingNotice,_now:number):boolean {
 return markers(store,accountId).some(value=>value.id===membershipEndingIdentity(notice));
}
/** Record only from the actual notice's committed layout, under that same lock. */
export function recordMembershipEndingShown(store:MarkerStore,accountId:string,notice:MembershipEndingNotice,now:number):boolean {
 const expires=Date.parse(notice.expiresAt),events=markers(store,accountId),id=membershipEndingIdentity(notice);
 if(expires<=now||events.some(value=>value.id===id)||events.length>=2048)return false;
 store.setItem(markerKey(accountId),JSON.stringify({version:1,events:[...events,{id,expires}]}));return true;
}
