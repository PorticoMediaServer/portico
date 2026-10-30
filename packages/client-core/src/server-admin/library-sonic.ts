import {throwIfAborted} from '../abort-lifetime.ts';
import {decodeGetAdminLibrariesIdSonicAnalysisResponse} from '../../../contracts/src/decoders.ts';
import {encodedRoutePath} from '../../../contracts/src/route-paths.ts';
import type {GetAdminLibrariesIdSonicAnalysisResponse} from '../../../contracts/src/types.ts';
import type {LibraryContentApi} from '../library-content.ts';

export type LibrarySonicState='disabled'|'unavailable'|'complete'|'paused'|'analyzing';
export type LibrarySonicDocument=Readonly<Omit<GetAdminLibrariesIdSonicAnalysisResponse,'state'>&{state:LibrarySonicState}>;

function libraryID(id:string):string{
 if(!id||id.length>256||/[\x00-\x1f\x7f]/.test(id))throw new Error('Invalid library identity.');
 return encodeURIComponent(id);
}
function parse(raw:unknown,id:string,signal?:AbortSignal):LibrarySonicDocument{
 throwIfAborted(signal);
 const doc=decodeGetAdminLibrariesIdSonicAnalysisResponse(raw);
 if(doc.libraryId!==id||!Number.isSafeInteger(doc.revision)||doc.revision<1||
  !['disabled','unavailable','complete','paused','analyzing'].includes(doc.state)||
  [doc.done,doc.total,doc.policyRevision,doc.observedAtMs].some(v=>!Number.isSafeInteger(v)||v<0)||
  [doc.pausedUntilMs,doc.estimatedRemainingSeconds].some(v=>v!==undefined&&(!Number.isSafeInteger(v)||v<0)))throw new Error('Invalid library Sonic status.');
 return Object.freeze({...doc,state:doc.state as LibrarySonicState});
}

/** Runtime readiness is a captured producer fact, independent of the saved switch. */
export function librarySonicAvailable(doc:LibrarySonicDocument):boolean{return doc.available;}

export async function readLibrarySonic(api:LibraryContentApi,libraryId:string,signal?:AbortSignal):Promise<LibrarySonicDocument>{
 const path=encodedRoutePath('get_admin_libraries_id_sonic_analysis',{id:libraryID(libraryId)});
 throwIfAborted(signal);
 return parse(await api.request<unknown>(path,'GET',undefined,signal),libraryId,signal);
}

/** Save exactly one library choice at its captured revision; never replay automatically. */
export async function saveLibrarySonic(api:LibraryContentApi,current:LibrarySonicDocument,enabled:boolean,signal?:AbortSignal):Promise<LibrarySonicDocument>{
 if(typeof enabled!=='boolean'||!Number.isSafeInteger(current.revision)||current.revision<1)throw new Error('Invalid library Sonic change.');
 const path=encodedRoutePath('put_admin_libraries_id_sonic_analysis',{id:libraryID(current.libraryId)});
 throwIfAborted(signal);
 const doc=parse(await api.request<unknown>(path,'PUT',{enabled,expectedRevision:current.revision},signal),current.libraryId,signal);
 if(doc.revision<=current.revision||doc.enabled!==enabled)throw new Error('Library Sonic change could not be reconciled.');
 return doc;
}

/** Retry only the server's current runtime/model probe, with no client model or work input. */
export async function retryLibrarySonic(api:LibraryContentApi,libraryId:string,signal?:AbortSignal):Promise<LibrarySonicDocument>{
 const path=encodedRoutePath('post_admin_libraries_id_sonic_analysis_retry',{id:libraryID(libraryId)});
 throwIfAborted(signal);
 return parse(await api.request<unknown>(path,'POST',{},signal),libraryId,signal);
}
