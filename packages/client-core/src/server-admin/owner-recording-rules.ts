import type {LibraryContentApi} from '../library-content.ts';
import type {DvrRule,DvrRuleSave,LiveProgramme} from '../../../contracts/src/next/api.ts';
import {callServer} from '../query/server-api.ts';
import {unreadableServerResponse} from '../server-messages.ts';
export type {DvrRule,DvrRuleSave};
export type RecordingRuleProgramme=LiveProgramme&{channelName:string};
export async function readOwnerRecordingRules(api:LibraryContentApi,signal?:AbortSignal){
 const page=await callServer(api,'GET /v1/dvr/rules',{query:{scope:'everyone'},signal});
 if(!Array.isArray(page.items)||page.items.some(row=>!row.id||typeof row.profileId!=='string'||typeof row.title!=='string'))throw new Error(unreadableServerResponse);
 return page.items;
}
export function saveOwnerRecordingRule(api:LibraryContentApi,body:DvrRuleSave,id?:string,preview=false,signal?:AbortSignal){
 return id?callServer(api,'PATCH /v1/dvr/rules/{id}',{id,query:{preview:preview||undefined},body,signal}):callServer(api,'POST /v1/dvr/rules',{query:{preview:preview||undefined},body,signal});
}
export function removeOwnerRecordingRule(api:LibraryContentApi,id:string,cancelBookings:boolean,signal?:AbortSignal){return callServer(api,'DELETE /v1/dvr/rules/{id}',{id,query:{bookings:cancelBookings?'cancel':undefined},signal});}
export async function readRecordingRuleProgrammes(api:LibraryContentApi,day:string,source:string,cursor?:string,signal?:AbortSignal){
 const from=new Date(day+'T00:00:00Z');if(!/^\d{4}-\d{2}-\d{2}$/.test(day)||!Number.isFinite(from.getTime())||from.toISOString().slice(0,10)!==day)throw {code:'invalid_request'};
 const page=await callServer(api,'GET /v1/live/guide',{query:{from:from.toISOString(),to:new Date(from.getTime()+86400000).toISOString(),source,cursor,limit:30},signal});
 return {items:page.items.filter(row=>row.channel.kind==='source').flatMap(row=>row.programmes.filter(item=>!!item.title&&!item.notAvailable&&!item.slate).map(item=>({...item,channelName:row.channel.name}))),nextCursor:page.nextCursor};
}
export async function readRecordingRuleChannels(api:LibraryContentApi,source:string,cursor?:string,signal?:AbortSignal){
 const page=await callServer(api,'GET /v1/admin/live/sources/{id}/channels',{id:source,query:{cursor},signal});return {items:page.items.filter(row=>row.broughtIn&&!row.hidden&&!row.missing),nextCursor:page.nextCursor};
}
