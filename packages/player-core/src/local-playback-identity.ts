import {invalidState, record, wireId} from './validation.ts';

/** Captured from the real selected session. This binds state; it grants no file access. */
export type LocalPlaybackScope = Readonly<{
  serverId:string; authority:'local'|'hosted'; accountId:string; profileId:string; sessionFamilyId:string;
}>;
export type LocalPlaybackIdentity = Readonly<{kind:'local';scope:LocalPlaybackScope;run:number}>;
/** The session id is returned by LocalPlayback; playId names the separate offline journal play.
 * Artifact facts come from the verified receipt, never transfer/display metadata. No URL or path travels here. */
export type LocalPlaybackFence = Readonly<{
  kind:'local';playback:LocalPlaybackIdentity;sessionId:string;generation:number;
  itemId:string;entryId:string;playId:string;artifact:Readonly<{sha256:string;bytes:number}>;
}>;
const scopeKeys = ['serverId','authority','accountId','profileId','sessionFamilyId'] as const;
const only = (value:Record<string,unknown>,keys:readonly string[]) => {
  if(Object.keys(value).some(key=>!keys.includes(key)))invalidState();
};
const positive = (value:unknown):value is number => typeof value==='number'&&Number.isSafeInteger(value)&&value>0;

export function localPlaybackScope(value:LocalPlaybackScope):LocalPlaybackScope {
  if(!record(value)||!scopeKeys.every(key=>key==='authority'?value[key]==='local'||value[key]==='hosted':wireId(value[key])))invalidState();
  only(value,scopeKeys);
  return Object.freeze({serverId:value.serverId,authority:value.authority,accountId:value.accountId,profileId:value.profileId,sessionFamilyId:value.sessionFamilyId});
}
export function localPlaybackIdentity(value:LocalPlaybackIdentity):LocalPlaybackIdentity {
  if(!record(value)||value.kind!=='local'||!positive(value.run))invalidState();
  only(value,['kind','scope','run']);
  return Object.freeze({kind:'local',scope:localPlaybackScope(value.scope),run:value.run});
}
export function localPlaybackFence(value:LocalPlaybackFence):LocalPlaybackFence {
  if(!record(value)||value.kind!=='local'||!wireId(value.entryId)||!wireId(value.itemId)||!wireId(value.playId)||!positive(value.generation)||!record(value.artifact))invalidState();
  only(value,['kind','playback','sessionId','generation','itemId','entryId','playId','artifact']);
  const suffix=typeof value.sessionId==='string'&&value.sessionId.startsWith(`local:${value.entryId}:`)?value.sessionId.slice(`local:${value.entryId}:`.length):'';
  if(!/^[1-9][0-9]{0,15}$/.test(suffix)||!Number.isSafeInteger(Number(suffix)))invalidState();
  only(value.artifact,['sha256','bytes']);
  if(typeof value.artifact.sha256!=='string'||!/^[a-f0-9]{64}$/.test(value.artifact.sha256)||!positive(value.artifact.bytes))invalidState();
  return Object.freeze({kind:'local',playback:localPlaybackIdentity(value.playback),sessionId:value.sessionId,generation:value.generation,
    itemId:value.itemId,entryId:value.entryId,playId:value.playId,artifact:Object.freeze({sha256:value.artifact.sha256,bytes:value.artifact.bytes})});
}
export const sameLocalScope = (a:LocalPlaybackScope,b:LocalPlaybackScope):boolean => scopeKeys.every(key=>a[key]===b[key]);
