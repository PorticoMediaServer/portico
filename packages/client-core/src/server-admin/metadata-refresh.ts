import {callServer,type ServerRequester} from '../query/server-api.ts';
import {throwIfAborted} from '../abort-lifetime.ts';
import {unreadableServerResponse} from '../server-messages.ts';

/** The Metadata settings action: queue a provider refresh for every owner library. */
export async function refreshAllMetadata(api:ServerRequester,signal?:AbortSignal):Promise<void>{
 const libraries=new Set<string>(),cursors=new Set<string>();let cursor:string|undefined;
 do{
  throwIfAborted(signal);
  const page=await callServer(api,'GET /v1/admin/libraries',{query:{cursor},signal});
  if(!Array.isArray(page.items)||page.items.some(row=>typeof row.id!=='string'||!row.id)||page.nextCursor!==undefined&&typeof page.nextCursor!=='string')throw new Error(unreadableServerResponse);
  for(const row of page.items)libraries.add(row.id);
  cursor=page.nextCursor||undefined;
  if(cursor){if(cursors.has(cursor))throw new Error(unreadableServerResponse);cursors.add(cursor);}
 }while(cursor);
 for(const library of libraries){
  throwIfAborted(signal);
  await callServer(api,'POST /v1/jobs',{body:{kind:'refresh_library',target:{library}},signal});
 }
 throwIfAborted(signal);
}
