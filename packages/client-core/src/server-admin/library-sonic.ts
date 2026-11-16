import {throwIfAborted} from '../abort-lifetime.ts';
import {callServer, serverPath} from '../query/server-api.ts';
import type {LibraryContentApi} from '../library-content.ts';
import {unreadableServerResponse} from '../server-messages.ts';
import {ownerLibrary} from './library-document.ts';
import {patchOwnerLibrary} from './libraries.ts';

export type LibrarySonicState='disabled'|'unavailable'|'complete'|'paused'|'analyzing';
/** A music library's Sonic analysis: its switch (the library's `soundAnalysis` option, on by default) and the
 * server's status line (`GET /v1/admin/libraries/{id}/sound-analysis`). `revision` is the library's. */
export type LibrarySonicDocument=Readonly<{libraryId:string;revision:number;enabled:boolean;defaultEnabled:boolean;available:boolean;state:LibrarySonicState;
 done:number;total:number;observedAtMs:number;reasonCode?:string;pausedUntilMs?:number;estimatedRemainingSeconds?:number}>;

const states:Readonly<Record<string,LibrarySonicState>>={analyzing:'analyzing',paused:'paused',done:'complete',off:'disabled',cant_run:'unavailable'};
const count=(v:unknown):v is number=>Number.isSafeInteger(v)&&(v as number)>=0;
const invalid=():never=>{throw new Error(unreadableServerResponse);};

/** Runtime readiness is a captured producer fact, independent of the saved switch. */
export function librarySonicAvailable(doc:LibrarySonicDocument):boolean{return doc.available;}

export async function readLibrarySonic(api:LibraryContentApi,libraryId:string,signal?:AbortSignal):Promise<LibrarySonicDocument>{
 const [raw,status]=await Promise.all([api.request<unknown>(serverPath('GET /v1/admin/libraries/{id}',{id:libraryId}),'GET',undefined,signal),
  callServer(api,'GET /v1/admin/libraries/{id}/sound-analysis',{id:libraryId,signal})]);
 throwIfAborted(signal);
 const library=ownerLibrary(raw,libraryId),state=states[status?.state];
 if(!state||typeof status.on!=='boolean'||typeof status.available!=='boolean'||!count(status.songs)||!count(status.analyzed)||
  status.secondsLeft!==undefined&&!count(status.secondsLeft)||status.pausedUntil!==undefined&&!Number.isFinite(Date.parse(status.pausedUntil)))return invalid();
 return Object.freeze({libraryId,revision:Number(library.revision),enabled:status.on,defaultEnabled:true,available:status.available,state,
  done:status.analyzed,total:status.songs,observedAtMs:Date.now(),
  ...(status.pausedUntil?{pausedUntilMs:Date.parse(status.pausedUntil)}:{}),...(status.secondsLeft!==undefined?{estimatedRemainingSeconds:status.secondsLeft}:{})});
}

/** Turns the library's switch on or off at the revision the page read; the library keeps its other choices. */
export async function saveLibrarySonic(api:LibraryContentApi,current:LibrarySonicDocument,enabled:boolean,signal?:AbortSignal):Promise<LibrarySonicDocument>{
 if(typeof enabled!=='boolean'||!Number.isSafeInteger(current.revision)||current.revision<1)throw new Error('Invalid library Sonic change.');
 const library=ownerLibrary(await api.request<unknown>(serverPath('GET /v1/admin/libraries/{id}',{id:current.libraryId}),'GET',undefined,signal),current.libraryId);
 await patchOwnerLibrary(api,current.libraryId,current.revision,{options:{...library.options,soundAnalysis:enabled}},signal);
 const doc=await readLibrarySonic(api,current.libraryId,signal);
 if(doc.enabled!==enabled)throw new Error('Library Sonic change could not be reconciled.');
 return doc;
}

/** The server checks its sound model by itself; trying again reads its answer once more. */
export const retryLibrarySonic=readLibrarySonic;
