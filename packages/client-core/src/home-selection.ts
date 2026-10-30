import type {ContentEntryKind} from './library-content.ts';
import type {HomeActions,HomeSelectionActions,HomeChannel,HomeCollection,HomeEntry,HomeRow} from './home.ts';
import {selectionCapabilities,type SelectionItem} from './selection-model/index.ts';
import type {EntryCapabilities} from './presentation/entry-actions.ts';

/** Actual platform runtimes narrow server offers; they never create a grant. */
export type HomeActionPlatform=Readonly<{
  platform:'web'|'phone-app'|'tv';
  /** Presentation context only; never supplies a server grant. */
  recommendationRow?:boolean;
  playbackAvailable?:boolean;
  queueAvailable?:boolean;
  nativeDownloadsAvailable?:boolean;
}>;
export type HomeCatalogueSelection=Extract<SelectionItem,{kind:ContentEntryKind}>;
export type HomeChannelSelection=Extract<SelectionItem,{kind:'channel'}>;

function offered(actions:HomeSelectionActions|undefined,platform:HomeActionPlatform) {
  return {
    play:actions?.play===true&&platform.playbackAvailable===true,
    queue:actions?.queue===true&&platform.queueAvailable===true,
    playlist:actions?.playlist===true,
    myList:actions?.myList===true,
    watched:actions?.watched===true,
    collection:actions?.collection===true,
    favorite:actions?.favorite===true,
    download:actions?.download===true&&platform.platform==='phone-app'&&platform.nativeDownloadsAvailable===true,
    // Catalogue curation is available on TV under DESIGN/Q45. These exact
    // server offers grant nothing in Server settings or File info management.
    edit:actions?.edit===true,
    refresh:actions?.refresh===true,
    delete:actions?.delete===true,
  };
}

/** Displayed catalogue identity, never entry.playback.itemId or a resume child. */
export function homeEntrySelection(entry:HomeEntry,platform:HomeActionPlatform):HomeCatalogueSelection|undefined {
  if(!entry.libraryId)return undefined;
  return Object.freeze({kind:entry.kind,id:entry.id,libraryId:entry.libraryId,capabilities:selectionCapabilities(entry.kind,offered(entry.homeActions?.selection,platform))});
}

/** A collection banner selects its canonical collection, not its artwork members. */
export function homeCollectionSelection(collection:HomeCollection,platform:HomeActionPlatform):HomeCatalogueSelection {
  return Object.freeze({kind:'collection',id:collection.collection.id,libraryId:collection.collection.libraryId,capabilities:selectionCapabilities('collection',offered(collection.actions?.selection,platform))});
}

/** Source is part of channel identity; no catalogue id or library is synthesized. */
export function homeChannelSelection(value:HomeChannel,platform:HomeActionPlatform):HomeChannelSelection {
  return Object.freeze({kind:'channel',sourceId:value.channel.sourceId,channelId:value.channel.id,capabilities:selectionCapabilities('channel',{
    play:value.actions.play&&value.channel.tuneAvailable&&platform.playbackAvailable===true,
    favorite:value.actions.favorite,
  })});
}

/** One bounded rendered row. A banner's mosaic is decorative; member cards, if
 * rendered separately, use homeEntrySelection individually. Family children are
 * separate rows and register their own windows. Resumes are playback, not picks. */
export function homeRowSelection(row:HomeRow,platform:HomeActionPlatform):readonly SelectionItem[] {
  if(row.live)return Object.freeze(row.live.channels.map(value=>homeChannelSelection(value,platform)));
  if(row.collection)return Object.freeze([homeCollectionSelection(row.collection,platform)]);
  return Object.freeze(row.entries.flatMap(entry=>{const item=homeEntrySelection(entry,platform);return item?[item]:[];}));
}

function menuCapabilities(kind:ContentEntryKind,actions:HomeActions|undefined,platform:HomeActionPlatform,progressSeconds?:number):EntryCapabilities {
  if(!actions)return Object.freeze({kind});
  // Single More uses title commands. Never borrow a descendant-selection grant
  // to advertise a title-level My List toggle or manufacture its saved state.
  const grants=offered({...actions,edit:actions.editMetadata,refresh:actions.refreshMetadata},platform);
  const container=['show','season','album','artist','book','collection'].includes(kind);
  return Object.freeze({
    kind,playable:grants.play,queue:grants.queue,
    ...(grants.play&&progressSeconds!==undefined?{progressSeconds}:{}),
    ...(actions.myList?{watchlisted:actions.state.watchlisted}:{}),
    ...(actions.favorite?{favorite:actions.state.favorite}:{}),
    ...(actions.watched||actions.played?{watched:actions.state.watched}:{}),
    ...(actions.rating?{rating:actions.state.rating}:{}),
    addToPlaylist:grants.playlist,addToCollection:grants.collection,
    ...(grants.download?{download:container?'container' as const:'media' as const}:{}),
    ...(actions.notInterested===true&&platform.recommendationRow===true?{notInterested:true}:{}),
    details:actions.details,editMetadata:grants.edit,refreshMetadata:grants.refresh,delete:grants.delete,
  });
}

/** Canonical input to entryActions for Home More. No role-string authority. */
export function homeEntryCapabilities(entry:HomeEntry,platform:HomeActionPlatform):EntryCapabilities {
  return menuCapabilities(entry.kind,entry.homeActions,platform,entry.progressSeconds);
}
export function homeCollectionCapabilities(collection:HomeCollection,platform:HomeActionPlatform):EntryCapabilities {
  return menuCapabilities('collection',collection.actions,platform);
}
export function homeChannelCapabilities(value:HomeChannel,platform:HomeActionPlatform):EntryCapabilities {
  const selected=homeChannelSelection(value,platform);
  return Object.freeze({kind:'channel',playable:selected.capabilities.play,...(selected.capabilities.favorite?{favorite:value.channel.favorite}:{})});
}
