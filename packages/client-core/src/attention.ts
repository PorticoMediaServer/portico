/**
 * The server's own "needs attention" list. Ordering and severity are server
 * policy: a client renders the list as given and never re-ranks it, so every
 * platform shows the owner the same most-urgent item first.
 */
import {keepReadable,oneOfOr} from './tolerant.ts';
export type AttentionSeverity='critical'|'warning'|'info';
export type AttentionAction={kind:string;target:string};
export type AttentionItem={id:string;severity:AttentionSeverity;title:string;detail:string;action:AttentionAction};

const obj=(v:unknown):v is Record<string,any>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const text=(v:unknown,max=512):v is string=>typeof v==='string'&&v.length<=max;
const severities:readonly AttentionSeverity[]=['critical','warning','info'];
function invalid():never{throw new Error('Invalid selected-server attention response.');}

/** A severity this client does not know reads as `info`, the plainest one. */
export function parseAttentionItem(value:unknown):AttentionItem{
 if(!obj(value))invalid();
 if(!text(value.id,200)||!value.id||!text(value.severity,40)||
  !text(value.title,120)||!value.title||!text(value.detail,512)||!value.detail)invalid();
 if(!obj(value.action)||!text(value.action.kind,60)||!value.action.kind||!text(value.action.target,200))invalid();
 return {...value,severity:oneOfOr('attention','item.severity',value.severity,severities,'info')} as AttentionItem;
}

/** The server caps this list; a longer one is a response we do not trust. An item that
 * cannot be read is left out and the others keep the server's order. */
export function parseAttention(value:unknown):AttentionItem[]{
 if(!obj(value)||!Array.isArray(value.items)||value.items.length>50)invalid();
 return keepReadable('attention','items',value.items,parseAttentionItem);
}
