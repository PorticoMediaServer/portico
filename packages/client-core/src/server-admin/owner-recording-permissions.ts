import type {LibraryContentApi} from '../library-content.ts';
import type {DvrPermission, DvrPermissionChange} from '../../../contracts/src/next/api.ts';
import {callServer, serverPath} from '../query/server-api.ts';
import {unreadableServerResponse} from '../server-messages.ts';
export type {DvrPermission, DvrPermissionChange};
function rows(raw:unknown):readonly DvrPermission[]{
 const items=(raw as {items?:DvrPermission[]})?.items;
 if(!Array.isArray(items)||items.some(row=>!row.accountId||!row.profileId||typeof row.accountName!=='string'||typeof row.profileName!=='string'||!['local','hosted'].includes(row.authority)||!Number.isSafeInteger(row.revision)||!Number.isSafeInteger(row.accountRevision)||['accountAllowed','profileAllowed','liveAllowed','canRecord','ownerAccount'].some(key=>typeof row[key as keyof DvrPermission]!=='boolean')))throw new Error(unreadableServerResponse);
 return items;
}
export async function readRecordingPermissions(api:LibraryContentApi,signal?:AbortSignal){return rows(await callServer(api,'GET /v1/admin/dvr/permissions',{signal}));}
export async function saveRecordingPermission(api:LibraryContentApi,body:DvrPermissionChange,signal?:AbortSignal){
 if(!api.requestRaw)throw new Error(unreadableServerResponse);
 const answer=await api.requestRaw(serverPath('PATCH /v1/admin/dvr/permissions'),'PATCH',{body,signal,singleAttempt:true});
 if(answer.status>=400){const error=(answer.body as {error?:{code?:string}})?.error;throw {status:answer.status,code:error?.code};}
 return rows(answer.body);
}
