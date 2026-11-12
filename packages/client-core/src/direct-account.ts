/** Same creation/change policy as Hosted; old password hashes remain valid. */
export function validDirectPassword(value:string):boolean {
 return Array.from(value).length>=8&&new TextEncoder().encode(value).length<=72&&/\p{Lu}/u.test(value)&&/\p{Ll}/u.test(value)&&/[^\p{L}]/u.test(value);
}

import {routeBody} from '../../contracts/src/routes.ts';
import {routePath, type RouteRequest, type RouteResponse} from '../../contracts/src/route-paths.ts';
import type {ProfileAPI} from './profile-management.ts';
import {CodedError} from './server-messages.ts';
import {requireTag} from './needs-newer-app.ts';
import {identityCode} from './route-identity.ts';
import {callServer} from './query/server-api.ts';
import type {Invitation as NextInvitation} from '../../contracts/src/next/api.ts';
import {formatShortInvitationCode,parseShortInvitationCode} from './short-invitation-code.ts';

export type ServerInvitation=RouteResponse<'get_admin_access_invitation'>;
export type ServerInvitationLink=Readonly<{code:string;expiresAt:string;serverId?:string}>;
export type ServerShortInvitation=Readonly<{shortCode?:string;expiresAt:string;serverId:string}>;
export type PeopleMember=RouteResponse<'get_admin_access_member'>;
export type PublicInvitation=Readonly<{serverName:string;expiresAt:string;role:'admin'|'member';libraries:readonly {name:string;kind?:string}[];allLibraries:boolean;signInMethod:'local'|'portico';inviterUsername?:string}>;
export type PeopleInvitationPage=Readonly<{items:readonly ServerInvitation[];limit:number;nextCursor:string;totalCount:number;matchedCount:number}>;
export type PeopleInvitationQuery=Readonly<{q?:string;state?:'pending'|'accepted'|'revoked'|'expired';sort?:'sent'|'status';direction?:'asc'|'desc';limit?:number;cursor?:string}>;
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const bounded=(v:unknown,max:number):v is string=>typeof v==='string'&&v.length>0&&Array.from(v).length<=max&&!/[\p{Cc}\p{Cf}]/u.test(v);
const timestamp=(v:unknown):v is string=>typeof v==='string'&&/^\d{4}-\d\d-\d\dT/.test(v)&&Number.isFinite(Date.parse(v));
const rev=(v:unknown):v is number=>Number.isSafeInteger(v)&&Number(v)>0;
function invalidPeople():never{throw new CodedError('invalid_response','The server returned an unreadable people response.');}
/** Optional administrator text is never a recipient identity or part of a share link. */
export function privatePersonNotes(raw:string|null|undefined):string|null{
 if(raw==null)return null;
 const value=raw.trim();
 if(Array.from(value).length>500||/[\p{Cc}\p{Cf}]/u.test(value))throw new CodedError('invalid_notes','Use up to 500 characters for notes.');
 return value||null;
}
export function parsePublicInvitation(raw:unknown):PublicInvitation{
 if(!object(raw)||!bounded(raw.serverName,120)||!timestamp(raw.expiresAt)||!['member','admin'].includes(String(raw.role))||!['local','portico'].includes(String(raw.signInMethod))||typeof raw.allLibraries!=='boolean'||!Array.isArray(raw.libraries)||raw.libraries.length>256)invalidPeople();
 for(const field of ['notes','email','porticoUsername'])if(raw[field]!==undefined)invalidPeople();
 const libraries=raw.libraries.map(v=>{if(!object(v)||!bounded(v.name,256)||v.kind!==undefined&&!bounded(v.kind,64))invalidPeople();return Object.freeze({name:v.name,...(typeof v.kind==='string'?{kind:v.kind}:{})});});
 if(raw.inviterUsername!==undefined&&!bounded(raw.inviterUsername,64))invalidPeople();
 return Object.freeze({serverName:raw.serverName,expiresAt:raw.expiresAt,role:raw.role as 'member'|'admin',libraries:Object.freeze(libraries),allLibraries:raw.allLibraries,signInMethod:raw.signInMethod as 'local'|'portico',...(raw.inviterUsername?{inviterUsername:raw.inviterUsername as string}:{})});
}
export function parseServerInvitation(raw:unknown):ServerInvitation{
 if(!object(raw)||!bounded(raw.id,128)||!rev(raw.revision)||!['member','admin'].includes(String(raw.role))||!['pending','accepted','revoked','expired'].includes(String(raw.state))||!timestamp(raw.createdAt)||!timestamp(raw.expiresAt)||!Array.isArray(raw.allowedLibraries)||raw.allowedLibraries.length>1000||raw.allowedLibraries.some(v=>!bounded(v,128)))invalidPeople();
 if(raw.email!==undefined)invalidPeople();if(raw.notes!==undefined&&raw.notes!==null&&(typeof raw.notes!=='string'||privatePersonNotes(raw.notes)!==raw.notes))invalidPeople();
 if(raw.acceptedAt!==undefined&&!timestamp(raw.acceptedAt)||raw.accountId!==undefined&&!bounded(raw.accountId,128)||raw.porticoUsername!==undefined&&!bounded(raw.porticoUsername,64)||raw.code!==undefined&&(typeof raw.code!=='string'||!/^[-_A-Za-z0-9]{43}$/.test(raw.code)))invalidPeople();
 if(raw.serverId!==undefined){if(typeof raw.serverId!=='string')invalidPeople();try{identityCode(raw.serverId);}catch{invalidPeople();}}
 // What the person gets on joining. The stream count is exact: zero never stands for "no limit".
 const access=raw.access;if(!object(access)||!['inherit','unlimited','limit'].includes(String(access.maxStreamsMode))||!Number.isSafeInteger(access.maxStreams)||(access.maxStreamsMode==='limit'?(access.maxStreams as number)<1||(access.maxStreams as number)>1000:access.maxStreams!==0)||typeof access.allowRemote!=='boolean'||typeof access.accessTokens!=='boolean'||!(access.maxContentRating===''||bounded(access.maxContentRating,32)))invalidPeople();
 const {notes,...rest}=raw;
 return Object.freeze({...rest,...(typeof notes==='string'?{notes}:{}),allowedLibraries:Object.freeze([...raw.allowedLibraries]),access:Object.freeze({maxStreamsMode:access.maxStreamsMode,maxStreams:access.maxStreams,allowRemote:access.allowRemote,accessTokens:access.accessTokens,maxContentRating:access.maxContentRating})}) as ServerInvitation;
}
export function parsePeopleInvitationPage(raw:unknown):PeopleInvitationPage{
 if(!object(raw)||!Array.isArray(raw.items)||!Number.isSafeInteger(raw.limit)||Number(raw.limit)<1||Number(raw.limit)>100||raw.items.length>Number(raw.limit)||!Number.isSafeInteger(raw.totalCount)||Number(raw.totalCount)<0||!Number.isSafeInteger(raw.matchedCount)||Number(raw.matchedCount)<raw.items.length||Number(raw.matchedCount)>Number(raw.totalCount)||raw.nextCursor!==undefined&&(typeof raw.nextCursor!=='string'||raw.nextCursor.length>2048||/[\p{Cc}\p{Cf}]/u.test(raw.nextCursor)))invalidPeople();
 const seen=new Set<string>(),items=raw.items.map(value=>{const row=parseServerInvitation(value);if(row.code!==undefined||seen.has(row.id))invalidPeople();seen.add(row.id);return row;});
 return Object.freeze({items:Object.freeze(items),limit:Number(raw.limit),nextCursor:typeof raw.nextCursor==='string'?raw.nextCursor:'',totalCount:Number(raw.totalCount),matchedCount:Number(raw.matchedCount)});
}
/** Server-admin and recipient operations use distinct, generated contracts. */
const defaultAccess=Object.freeze({maxStreamsMode:'inherit',maxStreams:0,allowRemote:true,accessTokens:true,maxContentRating:''});
const invitationStates:Readonly<Record<string,ServerInvitation['state']>>={open:'pending',accepted:'accepted',revoked:'revoked',expired:'expired'};
const notOnServerYet=():never=>{throw new CodedError('not_available','This is not available on this server yet.');};
/** An invitation on the rebuilt server, in the shape the People screens read. `revision` is always 1: the later save
 * wins. What the person gets on joining is the default every account has today, since the server keeps no access
 * limits on an invitation yet. The code is kept only when `withCode` (a list never carries it, as before). */
export function serverInvitationOf(raw:NextInvitation,withCode=false):ServerInvitation{
 const state=invitationStates[raw?.state];
 if(!object(raw)||!bounded(raw.id,128)||!state||!['member','admin'].includes(raw.role)||!timestamp(raw.createdAt)||!timestamp(raw.expiresAt)||typeof raw.allLibraries!=='boolean'||!Array.isArray(raw.libraries))invalidPeople();
 if(raw.acceptedAt!==undefined&&!timestamp(raw.acceptedAt))invalidPeople();
 return Object.freeze({id:raw.id,revision:1,role:raw.role,state,createdAt:raw.createdAt,expiresAt:raw.expiresAt,allowedLibraries:Object.freeze(raw.allLibraries?[]:[...raw.libraries]),
  ...(raw.note?{notes:raw.note}:{}),...(raw.acceptedAt?{acceptedAt:raw.acceptedAt}:{}),...(withCode&&raw.code?{code:raw.code}:{}),access:defaultAccess}) as ServerInvitation;
}
const stateOrder:Readonly<Record<string,number>>={pending:0,accepted:1,expired:2,revoked:3};
/** Invitations on the rebuilt server. It answers with every invitation (at most a few hundred are kept waiting), so
 * search, the status filter and sorting happen here and the table gets one page. */
export class PeopleInvitationsClient{
 readonly api:ProfileAPI;
 constructor(api:ProfileAPI){this.api=api;}
 private async all(signal?:AbortSignal):Promise<readonly NextInvitation[]>{
  const page=await callServer(this.api,'GET /v1/admin/invitations',{signal});
  if(!Array.isArray(page?.items))invalidPeople();
  return page.items;
 }
 private async find(id:string,signal?:AbortSignal):Promise<NextInvitation>{
  const found=(await this.all(signal)).find(invitation=>invitation?.id===id);
  if(!found)throw new CodedError('invitation_expired','This invitation is no longer usable.');
  return found;
 }
 async list(query:PeopleInvitationQuery={},signal?:AbortSignal):Promise<PeopleInvitationPage>{
  if(query.q!==undefined&&(typeof query.q!=='string'||new TextEncoder().encode(query.q).length>160||/[\p{Cc}\p{Cf}]/u.test(query.q))||query.state!==undefined&&!['pending','accepted','revoked','expired'].includes(query.state)||query.sort!==undefined&&!['sent','status'].includes(query.sort)||query.direction!==undefined&&!['asc','desc'].includes(query.direction)||query.limit!==undefined&&(!Number.isSafeInteger(query.limit)||query.limit<1||query.limit>100)||query.cursor!==undefined&&(typeof query.cursor!=='string'||query.cursor.length>2048))invalidPeople();
  const seen=new Set<string>(),all=(await this.all(signal)).map(raw=>{const row=serverInvitationOf(raw);if(seen.has(row.id))invalidPeople();seen.add(row.id);return row;});
  if(query.cursor)return Object.freeze({items:Object.freeze([]),limit:1,nextCursor:'',totalCount:all.length,matchedCount:0});
  const search=query.q?.trim().toLocaleLowerCase()??'',direction=query.direction==='asc'?1:-1;
  const items=all.filter(row=>(!query.state||row.state===query.state)&&(!search||(row.notes??'').toLocaleLowerCase().includes(search)))
   .sort((a,b)=>direction*(query.sort==='status'?stateOrder[a.state]-stateOrder[b.state]||a.createdAt.localeCompare(b.createdAt):a.createdAt.localeCompare(b.createdAt)));
  return Object.freeze({items:Object.freeze(items),limit:Math.max(1,items.length),nextCursor:'',totalCount:all.length,matchedCount:items.length});
 }
 /** Access limits and a named Portico Account are not kept on an invitation yet: asking for them is refused, never dropped. */
 async create(input:RouteRequest<'post_admin_access_invitations'>,signal?:AbortSignal):Promise<ServerInvitation>{
  if(input.porticoUsername||input.access&&JSON.stringify({...defaultAccess,...input.access})!==JSON.stringify(defaultAccess))notOnServerYet();
  const days=input.expiresInHours/24,note=privatePersonNotes(input.notes)??undefined;
  const raw=await callServer(this.api,'POST /v1/admin/invitations',{body:{role:input.role,allLibraries:input.allowedLibraries.length===0,libraries:[...input.allowedLibraries],...(note?{note}:{}),expiresInDays:days},signal});
  return serverInvitationOf(raw,true);
 }
 async read(id:string,signal?:AbortSignal):Promise<ServerInvitation>{return serverInvitationOf(await this.find(id,signal));}
 /** Changes who it is for and what it shares. The expiry stays as it is. */
 async edit(id:string,input:RouteRequest<'put_admin_access_invitation'>,signal?:AbortSignal):Promise<ServerInvitation>{
  if(input.access&&JSON.stringify({...defaultAccess,...input.access})!==JSON.stringify(defaultAccess))notOnServerYet();
  const note=privatePersonNotes(input.notes);
  return serverInvitationOf(await callServer(this.api,'PATCH /v1/admin/invitations/{id}',{id,body:{role:input.role,allLibraries:input.allowedLibraries.length===0,libraries:[...input.allowedLibraries],note:note??''},signal}));
 }
 /** The code a link carries: the invitation's own code, while it is waiting. */
 async link(invitation:Pick<ServerInvitation,'id'|'revision'|'state'>,signal?:AbortSignal):Promise<ServerInvitationLink>{
  if(invitation.state!=='pending')throw new CodedError('invitation_expired','This invitation is no longer usable.');
  const found=await this.find(invitation.id,signal);
  if(found.state!=='open'||!bounded(found.code,64)||!timestamp(found.expiresAt))throw new CodedError('invitation_expired','This invitation is no longer usable.');
  return Object.freeze({code:found.code,expiresAt:found.expiresAt});
 }
 async revoke(invitation:Pick<ServerInvitation,'id'|'revision'>,_operationId:string,signal?:AbortSignal):Promise<ServerInvitation>{
  await callServer(this.api,'DELETE /v1/admin/invitations/{id}',{id:invitation.id,signal});
  return serverInvitationOf(await this.find(invitation.id,signal));
 }
 /** The code a person types. On the rebuilt server it is the invitation's own code, the same one a link carries;
  * asking again shows the same code. `serverId` is empty: the server does not name itself here. */
 async shortCode(invitation:Pick<ServerInvitation,'id'|'revision'|'state'>,_operationId:string,signal?:AbortSignal):Promise<ServerShortInvitation>{
  const link=await this.link(invitation,signal),shortCode=formatShortInvitationCode(link.code);
  if(!shortCode)invalidPeople();
  return Object.freeze({serverId:'',expiresAt:link.expiresAt,shortCode:shortCode!});
 }
 async preview(code:string,signal?:AbortSignal):Promise<PublicInvitation>{return parsePublicInvitation(await this.api.request(routePath('preview_invitation'),'POST',routeBody('preview_invitation',{code}),signal));}
 async previewShort(raw:string,signal?:AbortSignal):Promise<PublicInvitation>{
  const shortCode=parseShortInvitationCode(raw);if(!shortCode)throw new CodedError('invitation_incomplete','This invitation code is not complete.');
  return parsePublicInvitation(await this.api.request(routePath('preview_invitation'),'POST',routeBody('preview_invitation',{shortCode}),signal));
 }
 async acceptLocal(input:Extract<RouteRequest<'post_access_invitations_accept'>,{username:string}>,signal?:AbortSignal):Promise<Readonly<{accountId:string;username:string;profileId:string;role:string;allowedLibraries:string[]}>>{
  const raw=await this.api.request<unknown>(routePath('post_access_invitations_accept'),'POST',routeBody('post_access_invitations_accept',input),signal);
  if(object(raw)&&requireTag(raw,'variant',['accepted','signed-in','challenge'],invalidPeople)!=='accepted')invalidPeople();
  if(!object(raw)||!bounded(raw.accountId,128)||!bounded(raw.username,64)||!bounded(raw.profileId,128)||!['admin','member'].includes(String(raw.role))||!Array.isArray(raw.allowedLibraries)||raw.allowedLibraries.some(v=>!bounded(v,128))||Object.hasOwn(raw,'notes'))invalidPeople();
  return Object.freeze({accountId:raw.accountId,username:raw.username,profileId:raw.profileId,role:raw.role as string,allowedLibraries:[...raw.allowedLibraries]});
 }
}
/** A profile is secondary to the genuine account username. Private viewers stay private. */
export function personIdentity(username:string,profileName:string,privateViewer=false):Readonly<{username?:string;profileName?:string;privateViewerKey?:'together.privateViewer'}>{
 if(privateViewer)return Object.freeze({privateViewerKey:'together.privateViewer'});
 if(!bounded(username,64))invalidPeople();
 return Object.freeze({username,...(profileName&&profileName!==username?{profileName}:{})});
}
