import {wireDurations} from './wire-units.ts';
import {unreadableServerResponse} from './server-messages.ts';
/** Server-decided segment markers. The server owns skip safety; a client never
 * infers it from confidence, provenance or approval, because it is not sent any. */
export type SegmentMarkerKind='intro'|'recap'|'credits'|'commercial'|'outro';
export type SegmentMarker=Readonly<{id:string;kind:SegmentMarkerKind;startSeconds:number;endSeconds:number;automaticSafe:boolean}>;
export type SegmentMarkerSet=Readonly<{sourceId:string;revision:string;markers:readonly SegmentMarker[]}>;
const kinds:readonly string[]=['intro','recap','credits','commercial','outro'];
const obj=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const id=(v:unknown):v is string=>typeof v==='string'&&v.length>0&&v.length<=256&&/^[A-Za-z0-9_-]+$/.test(v);
const seconds=(v:unknown):v is number=>typeof v==='number'&&Number.isFinite(v)&&v>=0&&v<=1e9;
const only=(v:Record<string,unknown>,keys:readonly string[]):void=>{if(Object.keys(v).some(k=>!keys.includes(k)))fail();};
function fail():never{throw new Error(unreadableServerResponse);}
/** Markers arrive in start order, within the item clock, at most one per id. A
 * marker outside the item's duration is a clock disagreement, not a late segment. */
export function parseSegmentMarkers(raw:unknown,duration?:number):readonly SegmentMarker[]{
 if(!Array.isArray(raw)||raw.length>512)fail();
 const seen=new Set<string>();let previous=-1;
 const markers=raw.map(v=>{
 v=wireDurations(v,{startSeconds:"startMs",endSeconds:"endMs"});
  if(!obj(v))fail();only(v,['id','kind','startSeconds','endSeconds','automaticSafe']);
  if(!id(v.id)||seen.has(v.id)||!kinds.includes(String(v.kind))||!seconds(v.startSeconds)||!seconds(v.endSeconds)||v.endSeconds<=v.startSeconds||typeof v.automaticSafe!=='boolean')fail();
  if(v.startSeconds<previous)fail();
  if(duration!==undefined&&Number.isFinite(duration)&&duration>0&&v.endSeconds>duration+0.001)fail();
  seen.add(v.id);previous=v.startSeconds as number;
  return Object.freeze({id:v.id,kind:v.kind as SegmentMarkerKind,startSeconds:v.startSeconds as number,endSeconds:v.endSeconds as number,automaticSafe:v.automaticSafe});
 });
 return Object.freeze(markers);
}
/** A marker set is fenced to the source it was measured on. A set for a source
 * the player is not playing is refused rather than silently applied. */
export function parseSegmentMarkerSet(raw:unknown,expectedSourceId?:string,duration?:number):SegmentMarkerSet{
 if(!obj(raw))fail();only(raw,['sourceId','revision','markers']);
 if(!id(raw.sourceId)||!id(raw.revision)||expectedSourceId!==undefined&&raw.sourceId!==expectedSourceId)fail();
 return Object.freeze({sourceId:raw.sourceId,revision:raw.revision,markers:parseSegmentMarkers(raw.markers,duration)});
}
/** The only marker question a client may ask itself: may this segment be skipped
 * without the viewer asking for it? */
export const canSkipAutomatically=(marker:SegmentMarker):boolean=>marker.automaticSafe;
