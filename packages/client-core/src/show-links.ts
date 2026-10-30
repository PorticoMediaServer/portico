import {validateShowWorkspaceRoute,type ShowWorkspaceRoute} from './show-workspace.ts';
export type ShowLink=Readonly<{serverId:string;target:ShowWorkspaceRoute}>;
const valid=(s:unknown):s is string=>typeof s==='string'&&s.length>0&&s.length<=256&&/^[A-Za-z0-9_-]+$/.test(s);
/** Parse location only: this grants no identity or permission. Callers must match the authenticated server and let the server validate ancestry. */
export function parseShowLink(url:URL):ShowLink|null{
 const p=url.pathname.split('/');
 if(url.username||url.password||url.hash||p.length!==7||p[0]!==''||p[1]!=='servers'||p[3]!=='libraries'||!['shows','seasons','episodes'].includes(p[5]))return null;
 if(!valid(p[2])||!valid(p[4])||!valid(p[6]))return null;
 for(const key of url.searchParams.keys())if(!['season','episode','group'].includes(key)||url.searchParams.getAll(key).length!==1)return null;
 const target:Record<string,string>={libraryId:p[4],[p[5]==='shows'?'showId':p[5]==='seasons'?'seasonId':'episodeId']:p[6]};
 for(const [query,key]of [['season','selectedSeasonId'],['episode','episodeId']] as const){const value=url.searchParams.get(query);if(value!==null){if(!valid(value)||target[key]&&target[key]!==value)return null;target[key]=value;}}
 if(target.seasonId&&target.selectedSeasonId&&target.seasonId!==target.selectedSeasonId)return null;
 if(url.searchParams.has('group')){if(url.searchParams.get('group')!=='unassigned_absolute'||target.selectedSeasonId||target.seasonId)return null;target.group='unassigned_absolute';}
 return Object.freeze({serverId:p[2],target:Object.freeze(target) as ShowWorkspaceRoute});
}
export function showLinkPath(serverId:string,input:ShowWorkspaceRoute):string{
 const target=validateShowWorkspaceRoute(input);
 for(const [key,value]of Object.entries(target))if(key!=='group'&&!valid(value))throw new Error('Invalid show link identifier.');
 if(!valid(serverId)||target.seasonId&&target.selectedSeasonId&&target.seasonId!==target.selectedSeasonId||target.group&&(target.seasonId||target.selectedSeasonId))throw new Error('Invalid show link context.');
 const kind=target.showId?'shows':target.seasonId?'seasons':'episodes';const entity=target.showId??target.seasonId??target.episodeId;
 const query=new URLSearchParams();const season=target.selectedSeasonId??(kind==='shows'?target.seasonId:undefined);
 if(season)query.set('season',season);if(target.group)query.set('group',target.group);if(target.episodeId&&kind!=='episodes')query.set('episode',target.episodeId);
 const path=`/servers/${serverId}/libraries/${target.libraryId}/${kind}/${entity}${query.size?'?'+query:''}`;
 if(!parseShowLink(new URL(path,'https://portico.invalid')))throw new Error('Invalid show link context.');return path;
}
