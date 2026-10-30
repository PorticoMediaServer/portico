/** Semantic Home presentation for the new UI. Selection, order and reasons remain server-owned. */
import type {HomeDocument, HomeRow, HomeSpotlight} from '../home.ts';
import type {ContentEntry} from '../library-content.ts';
import type {HomeContractsMessageId} from '../../../i18n/src/catalog/en-US/next/home-contracts.ts';
import {collectionCaptionParts, titleFactParts, type FactPart, type TextEnvironment, type TitleFactContext} from './kit-model/index.ts';

/** Optional genuine parent facts are projected by Home, never fetched or inferred here. */
export type HomeTitlePresentationEntry = ContentEntry & Readonly<{
  homeTitleContext?:Readonly<{kind:'show';year?:number;genres:readonly string[];seasonCount?:number}>;
}>;
/** Home metadata belongs to the supplied display context, independently of the playable/resume target. */
export function homeTitleFactParts(entry:HomeTitlePresentationEntry,env:TextEnvironment):readonly FactPart[] {
  if(entry.kind==='episode'&&entry.homeTitleContext){
    const context=entry.homeTitleContext;
    return titleFactParts({year:context.year,genres:context.genres,seasonCount:context.seasonCount},env,'home-show');
  }
  let context:TitleFactContext;
  switch(entry.kind){
    case 'movie':case 'extra':context='home-movie';break;
    case 'show':case 'season':case 'episode':context='home-show';break;
    case 'artist':case 'album':case 'song':case 'disc':context='music';break;
    case 'book':case 'audiobook_file':case 'chapter':context='audiobook';break;
    default:return Object.freeze([]);
  }
  return titleFactParts({year:entry.year,genres:entry.genres,durationSeconds:entry.duration,seasonCount:entry.seasonCount,
    artist:entry.artist?.name,album:entry.album?.name,author:entry.author},env,context);
}

/** Semantic input only: the Home contract/parser owner supplies authoritative mediaKind. */
export type HomeCollectionCaptionSource = Readonly<{
  collection:Readonly<{itemCount:number}>;mediaKind?:string;firstYear?:number;lastTitle?:string;
}>;
export function homeCollectionCaption(source:HomeCollectionCaptionSource,env:TextEnvironment):readonly FactPart[] {
  return collectionCaptionParts({mediaKind:source.mediaKind,itemCount:source.collection.itemCount,firstYear:source.firstYear,lastTitle:source.lastTitle},env);
}

export type HomeMessage = Readonly<{key:HomeContractsMessageId;params?:Readonly<Record<string,string|number>>}>;
export type HomeSpotlightModel = Readonly<{
  entry:ContentEntry; reason:HomeSpotlight['reason']; overview?:string;
  facts:Readonly<{year?:number;genres:readonly string[];runtimeSeconds?:number;seasonCount?:number}>;
  artwork:Readonly<{backdropUrl?:string;posterUrl?:string}>;
  primary?:Readonly<{target:NonNullable<ContentEntry['playback']>;entry:ContentEntry;label:HomeMessage}>;
  actions:readonly HomeMessage[];
}>;
/** The renderer owns carousel choice, focus and timing; this function never picks a slide. */
export function homeSpotlightModels(document:Pick<HomeDocument,'spotlight'>):readonly HomeSpotlightModel[] {
  return Object.freeze(document.spotlight.map(slide=>{
    const entry=slide.entry,target=slide.resumeTarget??entry;
    const resume=(target.progressSeconds??target.playback?.startSeconds??0)>0&&!target.watched;
    const episode=target.kind==='episode'&&target.seasonNumber!==undefined&&target.episodeNumber!==undefined;
    const label:HomeMessage=episode
      ? {key:resume?'next.home.resumeEpisode':'next.home.playEpisode',params:{season:target.seasonNumber!,episode:target.episodeNumber!}}
      : {key:resume?'next.home.resume':'next.home.play'};
    return Object.freeze({entry,reason:slide.reason,...(entry.overview?{overview:entry.overview}:{}),
      facts:Object.freeze({...(entry.year===undefined?{}:{year:entry.year}),genres:entry.genres??Object.freeze([]),...(entry.duration===undefined?{}:{runtimeSeconds:entry.duration}),...(entry.seasonCount===undefined?{}:{seasonCount:entry.seasonCount})}),
      artwork:Object.freeze({...(entry.backdropUrl?{backdropUrl:entry.backdropUrl}:{}),...(entry.posterUrl?{posterUrl:entry.posterUrl}:{})}),
      ...(target.playback&&target.available!==false&&!target.missing?{primary:Object.freeze({target:target.playback,entry:target,label:Object.freeze(label)})}:{}),
      actions:Object.freeze([{key:'next.home.myList'}] as const)});
  }));
}
/** Keep every row in its published position and geometry. Renderers format these facts with the generic kit. */
export function homeRowPresentation(row:HomeRow) {
  return Object.freeze({id:row.id,kind:row.kind,artworkShape:row.artworkShape,title:row.titleText??{code:row.id,fallback:row.title},
    subtitle:row.subtitleText,reason:row.reason,seeAll:row.seeAll,platforms:row.platforms,
    cards:Object.freeze(row.entries.map((entry,index)=>Object.freeze({entry,
      resumeTarget:row.resumes?.find(v=>v.entryId===entry.id)?.target,
      ...(row.id==='community_watching'&&row.period?{rank:row.start+index+1}:{})}))),
    collection:row.collection,live:row.live,period:row.period});
}
