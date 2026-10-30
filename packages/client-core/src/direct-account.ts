/** Same creation/change policy as Hosted; old password hashes remain valid. */
export function validDirectPassword(value:string):boolean {
 return Array.from(value).length>=8&&new TextEncoder().encode(value).length<=72&&/\p{Lu}/u.test(value)&&/\p{Ll}/u.test(value)&&/[^\p{L}]/u.test(value);
}

import {routeBody} from '../../contracts/src/routes.ts';
import {routePath, type RouteRequest, type RouteResponse} from '../../contracts/src/route-paths.ts';
import type {ProfileAPI} from './profile-management.ts';
import {CodedError} from './server-messages.ts';
import {identityCode} from './route-identity.ts';
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
export class PeopleInvitationsClient{
 readonly api:ProfileAPI;
 constructor(api:ProfileAPI){this.api=api;}
 async list(query:PeopleInvitationQuery={},signal?:AbortSignal):Promise<PeopleInvitationPage>{
  if(query.q!==undefined&&(typeof query.q!=='string'||new TextEncoder().encode(query.q).length>160||/[\p{Cc}\p{Cf}]/u.test(query.q))||query.state!==undefined&&!['pending','accepted','revoked','expired'].includes(query.state)||query.sort!==undefined&&!['sent','status'].includes(query.sort)||query.direction!==undefined&&!['asc','desc'].includes(query.direction)||query.limit!==undefined&&(!Number.isSafeInteger(query.limit)||query.limit<1||query.limit>100)||query.cursor!==undefined&&(typeof query.cursor!=='string'||query.cursor.length>2048))invalidPeople();
  const params=new URLSearchParams();for(const key of ['q','state','sort','direction','limit','cursor'] as const)if(query[key]!==undefined)params.set(key,String(query[key]));
  const path=routePath('get_admin_access_invitations')+(params.size?'?'+params.toString():'');
  return parsePeopleInvitationPage(await this.api.request(path,'GET',undefined,signal));
 }
 async create(input:RouteRequest<'post_admin_access_invitations'>,signal?:AbortSignal):Promise<ServerInvitation>{
  const {notes,...rest}=input;return parseServerInvitation(await this.api.request(routePath('post_admin_access_invitations'),'POST',routeBody('post_admin_access_invitations',{...rest,notes:privatePersonNotes(notes)??undefined}),signal));
 }
 async read(id:string,signal?:AbortSignal):Promise<ServerInvitation>{return parseServerInvitation(await this.api.request(routePath('get_admin_access_invitation',{id}),'GET',undefined,signal));}
 async edit(id:string,input:RouteRequest<'put_admin_access_invitation'>,signal?:AbortSignal):Promise<ServerInvitation>{
  if(!rev(input.expectedRevision))invalidPeople();const {notes,...rest}=input;
  return parseServerInvitation(await this.api.request(routePath('put_admin_access_invitation',{id}),'PUT',routeBody('put_admin_access_invitation',{...rest,notes:privatePersonNotes(notes)??undefined}),signal));
 }
 async link(invitation:Pick<ServerInvitation,'id'|'revision'|'state'>,signal?:AbortSignal):Promise<ServerInvitationLink>{
  if(invitation.state!=='pending'||!rev(invitation.revision))throw new CodedError('invitation_expired','This invitation is no longer usable.');
  const raw=await this.api.request<unknown>(routePath('post_admin_access_invitation_link',{id:invitation.id}),'POST',routeBody('post_admin_access_invitation_link',{expectedRevision:invitation.revision}),signal);
  if(!object(raw)||!bounded(raw.code,128)||raw.code.length<32||!timestamp(raw.expiresAt))invalidPeople();
  if(raw.serverId!==undefined){if(typeof raw.serverId!=='string')invalidPeople();try{identityCode(raw.serverId);}catch{invalidPeople();}}
  return Object.freeze({code:raw.code,expiresAt:raw.expiresAt,...(raw.serverId===undefined?{}:{serverId:raw.serverId as string})});
 }
 async revoke(invitation:Pick<ServerInvitation,'id'|'revision'>,operationId:string,signal?:AbortSignal):Promise<ServerInvitation>{return parseServerInvitation(await this.api.request(routePath('post_admin_access_invitations_id_revoke',{id:invitation.id}),'POST',routeBody('post_admin_access_invitations_id_revoke',{expectedRevision:invitation.revision,operationId}),signal));}
 /** Explicit New code. A receipt replay intentionally has no plaintext and
  * must not cause a second operation or silently rotate the current code. */
 async shortCode(invitation:Pick<ServerInvitation,'id'|'revision'|'state'>,operationId:string,signal?:AbortSignal):Promise<ServerShortInvitation>{
  if(invitation.state!=='pending'||!rev(invitation.revision))throw new CodedError('invitation_expired','This invitation is no longer usable.');
  const raw=await this.api.request<unknown>(routePath('post_admin_access_invitation_short_code',{id:invitation.id}),'POST',routeBody('post_admin_access_invitation_short_code',{expectedRevision:invitation.revision,operationId}),signal);
  if(!object(raw)||typeof raw.serverId!=='string'||!timestamp(raw.expiresAt)||Object.hasOwn(raw,'code'))invalidPeople();
  try{identityCode(raw.serverId);}catch{invalidPeople();}
  if(Object.hasOwn(raw,'shortCode')&&!parseShortInvitationCode(raw.shortCode))invalidPeople();
  return Object.freeze({serverId:raw.serverId,expiresAt:raw.expiresAt,...(raw.shortCode===undefined?{}:{shortCode:formatShortInvitationCode(raw.shortCode)!})});
 }
 async preview(code:string,signal?:AbortSignal):Promise<PublicInvitation>{return parsePublicInvitation(await this.api.request(routePath('preview_invitation'),'POST',routeBody('preview_invitation',{code}),signal));}
 async previewShort(raw:string,signal?:AbortSignal):Promise<PublicInvitation>{
  const shortCode=parseShortInvitationCode(raw);if(!shortCode)throw new CodedError('invitation_incomplete','This invitation code is not complete.');
  return parsePublicInvitation(await this.api.request(routePath('preview_invitation'),'POST',routeBody('preview_invitation',{shortCode}),signal));
 }
 async acceptLocal(input:Extract<RouteRequest<'post_access_invitations_accept'>,{username:string}>,signal?:AbortSignal):Promise<Readonly<{accountId:string;username:string;profileId:string;role:string;allowedLibraries:string[]}>>{
  const raw=await this.api.request<unknown>(routePath('post_access_invitations_accept'),'POST',routeBody('post_access_invitations_accept',input),signal);
  if(!object(raw)||!bounded(raw.accountId,128)||!bounded(raw.username,64)||!bounded(raw.profileId,128)||!['admin','member'].includes(String(raw.role))||!Array.isArray(raw.allowedLibraries)||raw.allowedLibraries.some(v=>!bounded(v,128))||Object.hasOwn(raw,'notes'))invalidPeople();
  return Object.freeze({accountId:raw.accountId,username:raw.username,profileId:raw.profileId,role:raw.role as string,allowedLibraries:[...raw.allowedLibraries]});
 }
 async notes(member:Pick<PeopleMember,'accountId'|'revision'>,notes:string|null,operationId:string,signal?:AbortSignal):Promise<PeopleMember>{
  return this.api.request(routePath('put_admin_access_member_notes',{id:member.accountId}),'PUT',routeBody('put_admin_access_member_notes',{expectedRevision:member.revision,operationId,notes:privatePersonNotes(notes)??undefined}),signal);
 }
}
/** A profile is secondary to the genuine account username. Private viewers stay private. */
export function personIdentity(username:string,profileName:string,privateViewer=false):Readonly<{username?:string;profileName?:string;privateViewerKey?:'together.privateViewer'}>{
 if(privateViewer)return Object.freeze({privateViewerKey:'together.privateViewer'});
 if(!bounded(username,64))invalidPeople();
 return Object.freeze({username,...(profileName&&profileName!==username?{profileName}:{})});
}
