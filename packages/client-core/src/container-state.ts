/**
 * A show's, season's, album's, book's or artist's state for one person: Watched for everything in
 * it, and the title's own My List and Favorite flags, so a show's action row matches a film's.
 *
 * It is the same one route as any title (`PUT /v1/items/{id}/state`). Marking a holder sets every
 * title in it now; ones added later arrive unwatched. The answer carries an Undo that works for
 * about 30 seconds and puts back every title not changed since.
 */
import {readItem, setItemState, undoItemState, type Item, type ItemStateAnswer} from './items.ts';

export type ContainerKind='show'|'season'|'album'|'book'|'artist';
/** The saved flags a title carries besides watched. */
export type ContainerFlag='watchlisted'|'favorite';
export type ContainerState=Readonly<{
 /** Offered after marking everything in the title: `undoContainerState` takes it back until `undoUntil`. */
 undoToken?:string;undoUntil?:string;
 watched:boolean;watchlisted:boolean;favorite:boolean;
 /** Present on a read: the titles in it this person may see. */
 watchedCount?:number;unwatchedCount?:number;
 /** Present on a read: the library the title lives in, so a link with only its id can open its page. */
 libraryId?:string;
}>;
export interface ContainerStateApi{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>}

export const containerKinds:readonly ContainerKind[]=Object.freeze(['show','season','album','book','artist'] as const);
/** Which kinds carry a flag: My List (`watchlisted`) is for shows and books; Favorite for shows, albums, books and artists. */
export function containerCarries(kind:string|undefined,flag:ContainerFlag|'watched'):boolean{
 if(flag==='watched')return kind==='show'||kind==='season'||kind==='album'||kind==='book';
 if(flag==='watchlisted')return kind==='show'||kind==='book';
 return kind==='show'||kind==='album'||kind==='book'||kind==='artist';
}
const id=(v:unknown):v is string=>typeof v==='string'&&v.length>0&&v.length<=256&&!/[\x00-\x1f\x7f]/.test(v);
const count=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;

function checkKind(kind:string):ContainerKind{
 if(!containerKinds.includes(kind as ContainerKind))throw new Error('Container state covers shows, seasons, albums, books and artists.');
 return kind as ContainerKind;
}
const fromItem=(item:Item):ContainerState=>{
 const counts=item.counts,watched=counts?counts.leaves-counts.unwatchedLeaves:0;
 return Object.freeze({watched:counts?counts.leaves>0&&counts.unwatchedLeaves===0:item.state.watched,watchlisted:item.state.inList,favorite:item.state.favorite,
  watchedCount:watched,unwatchedCount:counts?.unwatchedLeaves??0,...(item.libraryId?{libraryId:item.libraryId}:{})});
};
const fromAnswer=(answer:ItemStateAnswer,watched:boolean):ContainerState=>Object.freeze({watched,watchlisted:answer.state.inList,favorite:answer.state.favorite,
 ...(answer.undo?{undoToken:answer.undo.token,undoUntil:answer.undo.expiresAt}:{})});

/** The title's flags and how many of its titles this person has watched. */
export async function getContainerState(api:ContainerStateApi,kind:ContainerKind,containerId:string,signal?:AbortSignal):Promise<ContainerState>{
 checkKind(kind);
 if(!id(containerId))throw new Error('A container id is required.');
 return fromItem(await readItem(api,containerId,signal));
}

/** Marks everything in the title watched or unwatched. The answer offers Undo. */
export async function setContainerState(api:ContainerStateApi,kind:ContainerKind,containerId:string,input:Readonly<{watched:boolean}>,signal?:AbortSignal):Promise<ContainerState>{
 checkKind(kind);
 if(!id(containerId))throw new Error('A container id is required.');
 if(typeof input.watched!=='boolean')throw new Error('Container watched needs the watched value.');
 return fromAnswer(await setItemState(api,containerId,{watched:input.watched},signal),input.watched);
}

/** Sets the title's My List or Favorite flag. The last tap wins. */
export async function setContainerFlag(api:ContainerStateApi,kind:ContainerKind,containerId:string,flag:ContainerFlag,value:boolean,signal?:AbortSignal):Promise<ContainerState>{
 const checked=checkKind(kind);
 if(!id(containerId))throw new Error('A container id is required.');
 if(!containerCarries(checked,flag)||typeof value!=='boolean')throw new Error('This title does not carry that flag.');
 const item=await readItem(api,containerId,signal);
 const answer=await setItemState(api,containerId,flag==='favorite'?{favorite:value}:{inList:value},signal);
 return Object.freeze({...fromItem(item),watchlisted:answer.state.inList,favorite:answer.state.favorite});
}

/** Detail copy for the visible watched/unwatched counts (no list/grid badges). */
export function containerCountLabel(watchedCount:number,unwatchedCount:number):{watched:number;unwatched:number}{
 if(!count(watchedCount)||!count(unwatchedCount))throw new Error('Invalid container counts.');
 return {watched:watchedCount,unwatched:unwatchedCount};
}

/** Takes back "mark all" within about 30 seconds: every title not changed since goes back to how it was. Later, or after a server restart, the server answers `undo_expired`. */
export async function undoContainerState(api:ContainerStateApi,kind:ContainerKind,containerId:string,input:Readonly<{undoToken:string}>,signal?:AbortSignal):Promise<ContainerState>{
 checkKind(kind);
 if(!id(containerId)||!id(input.undoToken))throw new Error('A recent container mark is required.');
 await undoItemState(api,input.undoToken,signal);
 return fromItem(await readItem(api,containerId,signal));
}
