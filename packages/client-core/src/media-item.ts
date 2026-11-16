import {parseMusicTechnicalFacts} from './music-facts.ts';
import type {MediaItem} from './index.ts';
import {wireDurations} from './wire-units.ts';
import {unreadableServerResponse} from './server-messages.ts';
import type {Item} from '../../contracts/src/next/api.ts';

/** The rebuilt item body of `GET /v1/items/{id}`, adapted to the app's media facts. */
export type ItemResponse = Item;
const object=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
export function parseMediaItem(raw:unknown):MediaItem {
 // P2-29: an item whose length the server cannot represent omits durationMs and says so; that
 // length stays unknown, never zero. Any other item still carries its exact length.
 const unrepresentable=object(raw)&&raw.durationState==='unrepresentable'&&raw.durationMs===undefined;
 if(object(raw)&&raw.durationState!==undefined&&!unrepresentable)throw new Error(unreadableServerResponse);
 if(!object(raw))throw new Error(unreadableServerResponse);
 const state=object(raw.state)?raw.state:undefined;
 const item=wireDurations({...raw,progressMs:state?.positionMs??raw.progressMs??0},{duration:'durationMs',progressSeconds:'progressMs'});
 if(object(item)){delete item.durationState;if(unrepresentable)item.durationUnrepresentable=true;}
 if(!object(item)||!['id','kind','title','libraryId'].every(k=>typeof item[k]==='string'&&item[k])||(item.available!==undefined&&typeof item.available!=='boolean')||(item.duration!==undefined&&typeof item.duration!=='number')||typeof item.progressSeconds!=='number')throw new Error(unreadableServerResponse);
 item.available=item.available!==false;
 item.kind=({track:'song',audiobook:'audiobook_file',book:'ebook',issue:'comic',series:'book_series'} as Record<string,string>)[item.kind as string]??item.kind;
 const images=object(raw.images)?raw.images:undefined;
 item.posterUrl=images?.poster??images?.cover??images?.thumb??item.posterUrl;
 item.backdropUrl=images?.backdrop??item.backdropUrl;
 item.overview=raw.summary??item.overview;
 const audioFacts=item.kind==='song'?parseMusicTechnicalFacts(item.audioFacts):undefined;
 if(audioFacts)item.audioFacts=audioFacts;else delete item.audioFacts;
 // P2-29: an item no longer carries its sources; they are a File info lane of their own.
 delete item.sources;
 return item as unknown as MediaItem;
}
