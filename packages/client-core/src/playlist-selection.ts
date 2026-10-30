import type {SavedService, SavedError} from './saved.ts';
function savedFailure(problem:SavedError|null,fallback:string){return Object.assign(new Error(problem?.message??fallback),problem??{});}
/** Adds a bounded selection in order. Confirmed entries are never replayed;
 * ambiguous responses use SavedService's original idempotency key. */
export async function appendPlaylistSelection(service:SavedService,itemIds:readonly string[],completed:number,onProgress:(count:number)=>void,isActive:()=>boolean=()=>true):Promise<number>{
 if(!itemIds.length||itemIds.length>100||!Number.isInteger(completed)||completed<0||completed>itemIds.length)throw new Error('Choose up to 100 individual items.');
 const generation=service.getSnapshot().generation;
 while(completed<itemIds.length&&isActive()&&service.getSnapshot().generation===generation){
  const before=service.getSnapshot();
  if(before.pending.length){const pending=before.pending[0];if(pending.intent.action!=='add'||pending.intent.itemId!==itemIds[completed])throw new Error('Finish the pending playlist change first.');await service.retryMutation();}
  else {if(before.phase!=='ready'){await service.retry();if(!isActive()||service.getSnapshot().generation!==generation)return completed;if(service.getSnapshot().phase!=='ready')throw savedFailure(service.getSnapshot().error,'The playlist could not be refreshed.');}await service.mutate({action:'add',itemId:itemIds[completed]});}
  if(!isActive()||service.getSnapshot().generation!==generation)return completed;
  const result=service.getSnapshot();
  if(result.pending.length||result.mutationError)throw savedFailure(result.mutationError,'The last addition could not be confirmed. Retry to check it.');
  completed++;onProgress(completed);
  if(completed<itemIds.length){await service.refresh();if(!isActive()||service.getSnapshot().generation!==generation)return completed;if(service.getSnapshot().phase!=='ready')throw savedFailure(service.getSnapshot().error,'Refresh the playlist before adding the remaining items.');}
 }
 return completed;
}

/** Reads the complete revision-bound playlist, never just the visible page. */
export async function readPlaylistSelection(service:SavedService,playlistId:string,signal?:AbortSignal){
 await service.select({view:'playlist',playlistId});
 const first=service.getSnapshot(),resource=first.playlist;
 if(signal?.aborted)throw new Error('Playlist selection cancelled.');
 if(first.phase!=='ready'||!resource)throw savedFailure(first.error,'The playlist could not be loaded.');
 if(resource.entryCount>1000)throw new Error('This playlist exceeds the device queue’s 1,000-item limit.');
 const entries:import('./queues.ts').QueueItemInput[]=[],seen=new Set<string>(),cursors=new Set<string>();let unavailable=0;
 for(;;){
  const state=service.getSnapshot();
  if(signal?.aborted)throw new Error('Playlist selection cancelled.');
  if(state.phase!=='ready'||state.playlist?.revision!==resource.revision)throw savedFailure(state.error,'The playlist changed. Try again with its latest order.');
  for(const section of state.projection?.sections??[])for(const entry of section.entries){
   if(entry.kind!=='playlist_entry'||seen.has(entry.id))throw new Error('The playlist order could not be verified.');
   seen.add(entry.id);if(seen.size>1000)throw new Error('The playlist exceeds the device queue limit.');
   if(entry.hidden||entry.media.available===false||!entry.media.playback){unavailable++;continue;}
   entries.push({itemId:entry.media.playback.itemId,editionId:null,partId:null,sourceContext:{kind:'playlist',id:playlistId,revision:String(resource.revision),entryId:entry.id}});
  }
  const next=state.pagination.next[0];if(!next)break;
  if(cursors.has(next.cursor))throw new Error('The playlist returned a repeated page.');cursors.add(next.cursor);await service.next(next.sectionId);
 }
 if(seen.size!==resource.entryCount)throw new Error('The complete playlist order could not be verified. Refresh and try again.');
 if(!entries.length)throw new Error('No items in this playlist are available to play.');
 return {entries,unavailable};
}
