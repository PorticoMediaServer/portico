import type {LibraryRoute,LibraryEntityView} from './library-navigation.ts';
import {isSettingsDestination} from './settings-destinations.ts';
import {parseShowLink,showLinkPath} from './show-links.ts';
import type {ShowWorkspaceRoute} from './show-workspace.ts';
export type WorkspaceLink=Readonly<{serverId:string;route:LibraryRoute;showTarget?:ShowWorkspaceRoute}>;
const valid=(v:unknown):v is string=>typeof v==='string'&&v.length>0&&v.length<=128&&/^[A-Za-z0-9_.:-]+$/.test(v);
const entities:readonly LibraryEntityView[]=['artist','album','book','collection','disc','author','book_series'];
const simple=['home','search','channels','library-channels','downloads'] as const;
/** A link conveys a destination, never credentials. Match serverId to the authenticated session before navigating. */
export function parseWorkspaceLink(url:URL):WorkspaceLink|null {
 if(url.username||url.password||url.hash||url.pathname.length>2048)return null;
 const show=parseShowLink(url);
 if(show){const t=show.target;return {serverId:show.serverId,route:t.showId?{kind:'entity',libraryId:t.libraryId,view:'show',entityId:t.showId}:t.seasonId?{kind:'entity',libraryId:t.libraryId,view:'season',entityId:t.seasonId}:{kind:'detail',libraryId:t.libraryId,itemId:t.episodeId!},showTarget:t};}
 let p:string[];try{p=url.pathname.split('/').map(decodeURIComponent);}catch{return null;}
 if(p[0]!==''||p[1]!=='servers'||!valid(p[2]))return null;
 const serverId=p[2],query=url.searchParams;
 const only=(keys:string[])=>[...query.keys()].every(k=>keys.includes(k)&&query.getAll(k).length===1);
 if(p.length===4){
  const kind=p[3];
  if((simple as readonly string[]).includes(kind)&&!query.size)return {serverId,route:{kind:kind as typeof simple[number]}};
  if((kind==='settings'||kind==='account')&&only(['section'])){const section=query.get('section');if(section!==null&&!isSettingsDestination(section))return null;return {serverId,route:{kind,...(section?{section}: {})} as LibraryRoute};}
  if(kind==='saved'&&only(['view','resource'])){const view=query.get('view')??'watchlist',resourceId=query.get('resource');if(!['watchlist','favorites','playlists','collections','views','history','resource'].includes(view)||view==='resource'&&!valid(resourceId)||view!=='resource'&&resourceId!==null)return null;return {serverId,route:{kind:'saved',view,...(resourceId?{resourceId}:{})} as LibraryRoute};}
  if(kind==='dvr'&&only(['view','recording'])){const view=query.get('view')??'upcoming',recordingId=query.get('recording');if(!['upcoming','recorded','rules','history','storage'].includes(view)||recordingId!==null&&!/^[a-f0-9]{64}$/.test(recordingId))return null;return {serverId,route:{kind:'dvr',view,...(recordingId?{recordingId}:{})} as LibraryRoute};}
 }
 if(p.length===5&&p[3]==='playlists'&&valid(p[4])&&!query.size)return {serverId,route:{kind:'playlist',playlistId:p[4]}};
 if(p[3]!=='libraries'||!valid(p[4]))return null;
 if(p.length===5&&only(['tab'])){const tab=query.get('tab')??'discover';if(!valid(tab))return null;return {serverId,route:{kind:'library',libraryId:p[4],tab}};}
 if(p.length!==7||!valid(p[6])||query.size)return null;
 if(p[5]==='items')return {serverId,route:{kind:'detail',libraryId:p[4],itemId:p[6]}};
 if(entities.includes(p[5] as LibraryEntityView))return {serverId,route:{kind:'entity',libraryId:p[4],view:p[5] as LibraryEntityView,entityId:p[6]}};
 return null;
}
export function workspaceLinkPath(serverId:string,route:LibraryRoute,showTarget?:ShowWorkspaceRoute):string {
 if(!valid(serverId))throw new Error('Invalid server link.');
 if(showTarget)return showLinkPath(serverId,showTarget);
 const root='/servers/'+encodeURIComponent(serverId),q=new URLSearchParams();let path:string;
 if(route.kind==='entity'&&(route.view==='show'||route.view==='season'))return showLinkPath(serverId,{libraryId:route.libraryId,...(route.view==='show'?{showId:route.entityId}:{seasonId:route.entityId})});
 if(route.kind==='library'){path=root+'/libraries/'+encodeURIComponent(route.libraryId);q.set('tab',route.tab);}
 else if(route.kind==='entity')path=root+'/libraries/'+encodeURIComponent(route.libraryId)+'/'+route.view+'/'+encodeURIComponent(route.entityId);
 else if(route.kind==='detail'||route.kind==='player')path=root+'/libraries/'+encodeURIComponent(route.libraryId)+'/items/'+encodeURIComponent(route.itemId);
 else if(route.kind==='playlist')path=root+'/playlists/'+encodeURIComponent(route.playlistId);
 else {path=root+'/'+route.kind;
  if((route.kind==='settings'||route.kind==='account')&&route.section)q.set('section',route.section);
  if(route.kind==='saved'){q.set('view',route.view);if(route.resourceId)q.set('resource',route.resourceId);}
  if(route.kind==='dvr'){if(route.view)q.set('view',route.view);if(route.recordingId)q.set('recording',route.recordingId);}
 }
 const result=path+(q.size?'?'+q:'');if(!parseWorkspaceLink(new URL(result,'https://portico.invalid')))throw new Error('Invalid workspace destination.');return result;
}
