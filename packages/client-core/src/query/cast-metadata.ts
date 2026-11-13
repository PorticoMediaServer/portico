import {entryFromItem,type Item} from '../items.ts';
import type {ContentEntry} from '../library-content.ts';
import type {CastApi,CastMetadata} from '../cast/index.ts';
import {serverPath} from './server-api.ts';

const artwork=(value:unknown):string|undefined=>typeof value==='string'&&value.length<=1024&&/^\/v1\/[^\\\s#]+$/.test(value)&&!value.includes('..')?value:undefined;

/** Read display facts under the original viewer. An optional album cover never
 * delays the identity read that decides whether a resumed title may be shown. */
export async function readCastMetadata(api:CastApi,itemId:string,assertCurrent:()=>void,purpose:'identity'|'cover'):Promise<Readonly<{item:ContentEntry;metadata:CastMetadata}>>{
 assertCurrent();
 const raw=await api.request<Item>(serverPath('GET /v1/items/{id}',{id:itemId}));assertCurrent();
 if(raw?.id!==itemId)throw new Error('cast_item_mismatch');
 const item=entryFromItem(raw);
 if(!item||typeof item.title!=='string'||!item.title)throw new Error('cast_item_mismatch');
 let artworkPath=artwork(item.posterUrl)??artwork(item.backdropUrl);
 const parent=raw.kind==='track'&&raw.parent?.kind==='album'?raw.parent:undefined;
 if(purpose==='cover'&&!artworkPath&&parent){
  try{assertCurrent();const album=await api.request<Item>(serverPath('GET /v1/items/{id}',{id:parent.id}));assertCurrent();
   if(album?.id===parent.id&&album.kind==='album'&&album.available===true)artworkPath=artwork(album.images?.poster??album.images?.cover);
  }catch{assertCurrent();}
 }
 assertCurrent();
 return Object.freeze({item,metadata:Object.freeze({title:item.title,kind:raw.kind==='audiobook'?'audiobook':item.kind,artworkPath,
  ...(raw.kind==='episode'?{seriesTitle:raw.root?.title??raw.rootTitle,season:raw.parentIndex,episode:raw.index}:{}),
  ...(raw.kind==='track'?{artist:raw.root?.title,albumTitle:raw.parent?.title}:{}),
 })});
}
