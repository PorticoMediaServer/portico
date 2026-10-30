import {parseMusicTechnicalFacts} from './music-facts.ts';
import type {MediaItem} from './index.ts';
import {wireDurations} from './wire-units.ts';
import {unreadableServerResponse} from './server-messages.ts';
import type {GetItemsIdResponse} from '../../contracts/src/types.ts';

/** The item body of `GET /v1/items/{id}`. The generated response also names the FileInfo opening
 * preparation (202), which only a FileInfo opening read can receive; a plain item read never does,
 * and `parseMediaItem` refuses anything that is not an item. */
export type ItemResponse = Extract<GetItemsIdResponse, {variant: 'item'}>;
const object=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
export function parseMediaItem(raw:unknown):MediaItem {
 // P2-29: an item whose length the server cannot represent omits durationMs and says so; that
 // length stays unknown, never zero. Any other item still carries its exact length.
 const unrepresentable=object(raw)&&raw.durationState==='unrepresentable'&&raw.durationMs===undefined;
 if(object(raw)&&raw.durationState!==undefined&&!unrepresentable)throw new Error(unreadableServerResponse);
 const item=wireDurations(raw,{duration:'durationMs',progressSeconds:'progressMs'});
 if(object(item)){delete item.durationState;if(unrepresentable)item.durationUnrepresentable=true;}
 if(!object(item)||!['id','kind','title','libraryId'].every(k=>typeof item[k]==='string')||typeof item.available!=='boolean'||(!unrepresentable&&typeof item.duration!=='number')||typeof item.progressSeconds!=='number')throw new Error(unreadableServerResponse);
 const audioFacts=item.kind==='song'?parseMusicTechnicalFacts(item.audioFacts):undefined;
 if(audioFacts)item.audioFacts=audioFacts;else delete item.audioFacts;
 // P2-29: an item no longer carries its sources; they are a File info lane of their own.
 delete item.sources;
 return item as unknown as MediaItem;
}
