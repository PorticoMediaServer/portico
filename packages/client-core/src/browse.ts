import {validSelectionUnavailableActions} from './selection-model/unavailable-actions.ts';
import {validSelectionBasis} from './selection-basis.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {routePattern} from '../../contracts/src/routes.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
const readingFacetRoute=routePattern('get_library_facets');
const readingPeriodicalRoute=routePattern('get_reading_periodical_groups');
/** Browse engine contract. The server owns the vocabulary, the query grammar and
 * every count; this module only parses what it publishes and refuses to send a
 * query the published capabilities cannot execute. No client-side filtering,
 * sorting or facet inference lives here. */
import {validateContentEntry,type ContentEntry} from './library-content.ts';
import {firstOfEach,keepReadable,noteSkipped,oneOfOr} from './tolerant.ts';

export type BrowseValueType='string'|'enum'|'number'|'date'|'boolean'|'identity-set';
export type BrowseControlHint='toggle'|'select'|'number-range'|'date-range'|'facet-multi-select'|'text';
export type BrowseComplexity='quick'|'standard'|'advanced';
export type BrowseCost='indexed'|'indexed-join';
export type BrowseDirection='asc'|'desc';
export type BrowseOperator='equals'|'not-equals'|'contains'|'starts-with'|'in'|'not-in'|'less-than'|'at-most'|'greater-than'|'at-least'|'within-last-days'|'between'|'is-present'|'is-missing'|'contains-any'|'contains-all';
export type BrowseScalar=string|number|boolean;
export type BrowseNode=
 |Readonly<{variant:'group';all:readonly BrowseNode[]}>
 |Readonly<{variant:'group';any:readonly BrowseNode[]}>
 |Readonly<{variant:'group';not:BrowseNode}>
 |Readonly<{variant:'predicate'|'relative';field:string;operator:BrowseOperator;value:BrowseScalar|readonly BrowseScalar[]|null}>;
export type BrowseSortSelection=Readonly<{field:string;direction:BrowseDirection;seed?:number}>;
/** Every field a query's conditions name, however deep. */
export function browseFields(nodes:readonly BrowseNode[]):string[]{
 const out:string[]=[];
 const walk=(n:BrowseNode)=>{if('all' in n)n.all.forEach(walk);else if('any' in n)n.any.forEach(walk);else if('not' in n)walk(n.not);else if('field' in n)out.push(n.field);};
 nodes.forEach(walk);
 return out;
}
export type BrowseFacetSource=Readonly<{endpoint:string;field:string}>;
export type BrowseFieldCapability=Readonly<{id:string;labelKey:string;type:BrowseValueType;operators:readonly BrowseOperator[];controlHint:BrowseControlHint;complexity:BrowseComplexity;cost:BrowseCost;applicableKinds:readonly string[];allowedValues?:readonly string[];facetSource?:BrowseFacetSource}>;
/** `requiresGroup`: the group's own order (a playlist's, a season's, an album's), offered on a group's page and never on a library tab. */
export type BrowseSortCapability=Readonly<{id:string;labelKey:string;directions:readonly BrowseDirection[];defaultDirection:BrowseDirection;expensive:boolean;seeded?:boolean;requiresGroup?:boolean;ownerOnly?:boolean;applicableKinds:readonly string[]}>;
/** `members`: a pivot read within one group (a book's parts), never a library tab. Paging is by key in a library and by position across libraries. */
export type BrowsePivotCapability=Readonly<{facetSource?:Readonly<{endpoint:string;field:string}>;id:string;labelKey:string;entityKinds:readonly string[];defaultSort:readonly BrowseSortSelection[];supportedViews:readonly string[];browsable:boolean;aggregate?:string;members?:boolean;sorts?:readonly string[];filters?:readonly string[];paging?:BrowsePaging}>;
export type BrowsePaging='keyset'|'offset';
export type BrowseQuickFilter=Readonly<{id:string;labelKey:string;query:BrowseNode}>;
export type BrowseQueryLimits=Readonly<{maximumDepth:number;maximumClauses:number;maximumBytes:number;maximumSorts:number;defaultLimit:number;maximumLimit:number;cursorTtlMs:number}>;
export type BrowseLibraryScope=Readonly<{id:string;name:string;kind:string;defaultView:string;pinned:boolean}>;
export type BrowseSurfaceCapability=Readonly<{id:string;endpoint:string;membershipField?:string;sorts:readonly string[];filters:readonly string[];paging:BrowsePaging}>;
export type BrowseCapabilities=Readonly<{surfaces?:readonly BrowseSurfaceCapability[];library:BrowseLibraryScope;pivots:readonly BrowsePivotCapability[];resolvedPivot:BrowsePivotCapability|null;fields:readonly BrowseFieldCapability[];sorts:readonly BrowseSortCapability[];quickFilters:readonly BrowseQuickFilter[];queryLimits:BrowseQueryLimits}>;
export type BrowsePositionAnchor=Readonly<{key:string;index:number}>;
/** `totalIsLowerBound`: a filter matched more titles than the server counts (10,000); `total` then
 * grows as pages arrive and the count reads "10,000+". */
export type BrowsePageInfo=Readonly<{start:number;total:number;totalIsLowerBound:boolean;revision:string;hasMore:boolean;nextCursor:string;durationMs?:number;durationIsLowerBound?:boolean}>;
export type BrowseRange=Readonly<{start:number;revision?:string;anchorId?:string}>;
export type BrowseSeek=Readonly<{prefix:string}>;
export type BrowseApplied=Readonly<{query:BrowseNode|null;sort:readonly BrowseSortSelection[];seek:BrowseSeek|null}>;
export type BrowseSelectionCapabilities=Readonly<{play:number;queue:number;playlist:number;myList:number;watched:number;collection:number;favorite:number;download:number;edit:number;refresh:number;delete:number}>;
export type BrowseSelectionType=Readonly<{kind:string;count:number;capabilities:BrowseSelectionCapabilities;libraries:readonly Readonly<{libraryId:string;count:number}>[]}>;
/** Exact facts about the whole authorized view, present only on a view the server can replay
 * unchanged as one job. Counts are displayed titles; nothing here is inferred from a loaded page.
 * selectionBasis is a server-issued full witness independent of the page revision;
 * its absence is supported only for legacy ordinary-200 descriptors. */
export type BrowseWholeSelection=Readonly<{libraryId:string;pivot:string;filter:BrowseNode|null;sort:readonly BrowseSortSelection[];catalogRevision:string;selectionBasis?:string;total:number;types:readonly BrowseSelectionType[];unavailableActions?:import('./selection-model/types.ts').SelectionUnavailableActions}>;
export type BrowseWholeSelectionUnavailable=Readonly<{reason:'search_not_bounded'}>;
export type BrowseResult=Readonly<{pivot:string;applied:BrowseApplied;entries:readonly ContentEntry[];pageInfo:BrowsePageInfo;positionIndex:readonly BrowsePositionAnchor[];displayTargets?:readonly BrowseDisplayTarget[];wholeSelection?:BrowseWholeSelection;wholeSelectionUnavailable?:BrowseWholeSelectionUnavailable}>;
export type BrowseDisplayTarget=Readonly<{membershipId:string;entryId:string}>;
export type BrowseFacetValue=Readonly<{value:string;label:string;count:number;countIsLowerBound?:boolean;seriesId?:string}>;
export type BrowseFacetPage=Readonly<{field:string;total:number;values:readonly BrowseFacetValue[];totalIsLowerBound?:boolean;revision?:string}>;
export type BrowseRequestBody=Readonly<{pivot?:string;query?:BrowseNode;sort?:readonly BrowseSortSelection[];limit?:number;cursor?:string;selectionBasis?:string;range?:BrowseRange;seek?:BrowseSeek;homeRowDisplay?:string}>;

const hints:readonly string[]=['toggle','select','number-range','date-range','facet-multi-select','text'];
const operators:readonly string[]=['equals','not-equals','contains','starts-with','in','not-in','less-than','at-most','greater-than','at-least','within-last-days','between','is-present','is-missing','contains-any','contains-all'];
const types:readonly string[]=['string','enum','number','date','boolean','identity-set'];
const listOperators:readonly string[]=['in','not-in','between','contains-any','contains-all'];
const presenceOperators:readonly string[]=['is-present','is-missing'];

const obj=(v:unknown):v is Record<string,unknown>=>v!==null&&typeof v==='object'&&!Array.isArray(v);
const str=(v:unknown,max=4096):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>str(v,256)&&v.length>0&&!/[\r\n]/.test(v);
const count=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const finite=(v:unknown):v is number=>typeof v==='number'&&Number.isFinite(v);
/**
 * The library a browse reads, or `acrossLibraries` for a group that spans the viewer's libraries
 * (a person's titles, a playlist, a personal collection, a Home row, My List, Favorites, the
 * history): those are read from `/v1/browse`, with the same vocabulary.
 */
export const acrossLibraries='*';
/** Where a browse page is read. */
export const browsePath=(libraryId:string):string=>libraryId===acrossLibraries?routePath('browse_across'):routePath('browse_library',{id:libraryId});
/** Where a browse vocabulary is read, narrowed to one pivot. */
export const browseCapabilitiesPath=(libraryId:string,pivot:string):string=>`${libraryId===acrossLibraries?routePath('get_browse_capabilities'):routePath('get_libraries_id_browse_capabilities',{id:libraryId})}?pivot=${encodeURIComponent(pivot)}`;
/** Where a filter's counted values are read (`?field=…`). */
export const browseFacetsPath=(libraryId:string):string=>libraryId===acrossLibraries?routePath('get_browse_facets'):routePath('get_library_facets',{id:libraryId});
/** The group a list across libraries is (a person, playlist, collection, Home row, My List, Favorites, the history), from the conditions it always applies: its filter choices are counted over that list's own titles (`?group=…&value=…` on the facets path). */
export function facetGroupOf(base:readonly BrowseNode[]):Readonly<{group:string;value:string}>|undefined{
  for(const node of base){
    if(!('field' in node))continue;
    if(['person','cast','crew','list','savedCollection','homeRow'].includes(node.field)&&typeof node.value==='string')return {group:node.field,value:node.value};
    if(['watchlisted','favorite','lastPlayedAt'].includes(node.field))return {group:node.field,value:''};
  }
  return undefined;
}
/** The sorts a library tab offers: every published sort but a group's own order. */
export const libraryTabSorts=<S extends Readonly<{requiresGroup?:boolean}>>(sorts:readonly S[]):S[]=>sorts.filter(sort=>!sort.requiresGroup);

function bad(message=unreadableServerResponse):never{throw Object.assign(new Error(message),{code:'invalid_browse'});}
function rows(v:unknown,max:number):unknown[]{if(!Array.isArray(v)||v.length>max)bad();return v;}
/** The names in a published list that this app can read (and knows, when `allowed` is given), each once. The list's bound is exact; a list the server leaves out is empty. */
function known(part:string,v:unknown,max:number,allowed?:readonly string[]):string[]{return [...new Set(keepReadable('browse',part,rows(v??[],max),entry=>id(entry)&&(!allowed||allowed.includes(entry))?entry:undefined))];}
function freeze<T>(v:T):T{if(v&&typeof v==='object'){for(const child of Object.values(v))freeze(child);Object.freeze(v);}return v;}

/** Thrown when a query names something the capabilities do not publish. The path
 * matches the server's own `error.field`, so one message renders in both cases. */
export class BrowseQueryError extends Error{
 readonly field:string;readonly code='invalid_query';
 constructor(field:string,message:string){super(message);this.field=field;this.name='BrowseQueryError';}
}

function sortSelection(v:unknown):BrowseSortSelection{
 if(!obj(v)||!id(v.field)||(v.direction!=='asc'&&v.direction!=='desc'))bad();
 if(v.seed!==undefined&&(!count(v.seed)||v.seed>4294967295||v.field!=='random'))bad();
 return {field:v.field,direction:v.direction,...(v.seed===undefined?{}:{seed:v.seed as number})};
}

/** The wire form of a node built from the loose `{field, operator, value}` and `{all|any|not}` shapes the
 * toolbar and tabs work in: each node gains the `variant` its keys name. Nothing else changes, and
 * `parseBrowseNode` still decides whether the result is valid. */
export function completeBrowseNode(v:unknown):unknown{
 if(!obj(v)||v.variant!==undefined)return v;
 if('field' in v)return {variant:v.operator==='within-last-days'?'relative':'predicate',...v};
 const key=['all','any','not'].find(name=>name in v);
 if(!key)return v;
 const child=v[key];
 return {variant:'group',...v,[key]:key==='not'?completeBrowseNode(child):Array.isArray(child)?child.map(completeBrowseNode):child};
}
/** Nodes are re-validated on the way in: a malformed tree is never handed to a
 * caller as if the server had blessed it. A node is a query this app may send back,
 * so it stays exact: one with an operator or a key this app does not know is refused,
 * and whatever carries it (a quick filter, a See all destination) is left out. */
export function parseBrowseNode(v:unknown,depth=0,path='query'):BrowseNode{
 if(!obj(v)||depth>16)bad();
 const {variant,...body}=v;

 if(variant==='predicate'||variant==='relative'){
  if(!('field' in body)||Object.keys(body).length!==3||!id(body.field)||!str(body.operator,64)||!operators.includes(body.operator))bad();
  const operator=body.operator as BrowseOperator;
  // The relative-date predicate has a variant of its own; no other operator may use it.
  if((operator==='within-last-days')!==(variant==='relative'))bad();
  const field=body.field;
  const value=body.value;
  if(operator==='within-last-days'){
   if(!Number.isInteger(value)||typeof value!=='number'||value<1||value>36500)bad();
   return {variant:'relative',field,operator,value};
  }
  if(presenceOperators.includes(operator)){if(value!==null&&value!==undefined)bad();return {variant:'predicate',field,operator,value:null};}
  if(Array.isArray(value)){
   if(!listOperators.includes(operator)||value.length<1||value.length>100||(operator==='between'&&value.length!==2))bad();
   return {variant:'predicate',field,operator,value:value.map(entry=>{if(!str(entry,500)&&!finite(entry)&&typeof entry!=='boolean')bad();return entry as BrowseScalar;})};
  }
  if(listOperators.includes(operator))bad();
  if(!str(value,500)&&!finite(value)&&typeof value!=='boolean')bad();
  return {variant:'predicate',field,operator,value:value as BrowseScalar};
 }
 if(variant==='group'){
  const keys=Object.keys(body);
  if(keys.length!==1||keys[0]==='field')bad();
  if(keys[0]==='not')return {variant:'group',not:parseBrowseNode(body.not,depth+1,path+'.not')};
  if(keys[0]==='all'||keys[0]==='any'){
   const children=rows(body[keys[0]],100).map((child,index)=>parseBrowseNode(child,depth+1,`${path}.${keys[0]}[${index}]`));
   if(children.length===0)bad();
   return keys[0]==='all'?{variant:'group',all:children}:{variant:'group',any:children};
  }
  return bad();
 }
 // A node this app has no name for comes from a newer server. It is not guessed at:
 // a carrier read item by item leaves it out, and a page that depends on it says the app needs updating.
 if(typeof variant==='string'&&/^[a-z][a-z0-9-]{0,63}$/.test(variant))throw Object.assign(new Error(unreadableServerResponse),{code:'unsupported_version'});
 return bad();
}

/**
 * What a library can be browsed by. The library, the query limits and the resolved pivot are
 * exact. A pivot, filter, sort or quick filter this app cannot read is not offered and the rest
 * are kept: a filter whose value type or control it does not know cannot be drawn, an operator or
 * direction it does not know is left out of its list, and a repeated id keeps its first appearance.
 */
export function parseBrowseCapabilities(raw:unknown):BrowseCapabilities{
 if(!obj(raw)||!obj(raw.library)||!obj(raw.queryLimits))bad();
 const library=raw.library;
 // A vocabulary across libraries names no library.
 const across=(library.id===''||library.id===undefined)&&(library.kind===''||library.kind===undefined);
 if(!across&&(!id(library.id)||!id(library.kind))||!str(library.name??'',512)||!str(library.defaultView??'',64))bad();
 const limits=raw.queryLimits;
 for(const key of ['maximumDepth','maximumClauses','maximumBytes','maximumSorts','defaultLimit','maximumLimit','cursorTtlMs'])if(!count(limits[key]))bad();
 if((limits.defaultLimit as number)<1||(limits.maximumLimit as number)<(limits.defaultLimit as number))bad();
 const pivot=(v:unknown):BrowsePivotCapability=>{
  if(!obj(v)||!id(v.id)||!id(v.labelKey))bad();
  // A pivot that does not say it can be queried is not queried.
  const facetSource=v.facetSource==null?undefined:obj(v.facetSource)&&((v.facetSource.endpoint===readingFacetRoute&&(v.facetSource.field==='author'||v.facetSource.field==='series'))||(v.facetSource.endpoint===readingPeriodicalRoute&&v.facetSource.field==='publicationType'&&(v.id==='magazines'||v.id==='newspapers')&&v.browsable===false))?{endpoint:v.facetSource.endpoint,field:v.facetSource.field}:bad();
  return {...(facetSource?{facetSource}:{}),id:v.id,labelKey:v.labelKey,entityKinds:known('pivot.entityKinds',v.entityKinds,32),defaultSort:keepReadable('browse','pivot.defaultSort',rows(v.defaultSort??[],3),sortSelection),supportedViews:known('pivot.supportedViews',v.supportedViews,8),sorts:known('pivot.sorts',v.sorts,64),filters:known('pivot.filters',v.filters,128),...(v.paging==='keyset'||v.paging==='offset'?{paging:v.paging}:{}),browsable:v.browsable===true,...(v.members===true?{members:true}:{}),...(v.aggregate===undefined?{}:{aggregate:id(v.aggregate)?v.aggregate:bad()})};
 };
 const pivots=firstOfEach('browse','pivots',keepReadable('browse','pivots',rows(raw.pivots,32),pivot),p=>p.id);
 const fields=firstOfEach('browse','fields',keepReadable('browse','fields',rows(raw.fields,128),v=>{
  if(!obj(v)||!id(v.id)||!id(v.labelKey)||!str(v.type,32)||!types.includes(v.type)||!str(v.controlHint,32)||!hints.includes(v.controlHint))bad();
  const facet=v.facetSource===undefined?undefined:(obj(v.facetSource)&&str(v.facetSource.endpoint,256)&&id(v.facetSource.field)?{endpoint:v.facetSource.endpoint,field:v.facetSource.field}:bad());
  const offered=known('field.operators',v.operators,32,operators) as BrowseOperator[];
  // A filter none of whose operators this app knows cannot be used.
  if(!offered.length&&Array.isArray(v.operators)&&v.operators.length)bad();
  // How prominent and how costly a filter is are hints: unknown ones read as the least prominent and the costliest.
  return {id:v.id,labelKey:v.labelKey,type:v.type as BrowseValueType,operators:offered,controlHint:v.controlHint as BrowseControlHint,complexity:oneOfOr('browse','field.complexity',v.complexity,['quick','standard','advanced'] as const,'advanced'),cost:oneOfOr('browse','field.cost',v.cost,['indexed','indexed-join'] as const,'indexed-join'),applicableKinds:known('field.applicableKinds',v.applicableKinds,64),...(v.allowedValues===undefined?{}:{allowedValues:known('field.allowedValues',v.allowedValues,256)}),...(facet?{facetSource:facet}:{})};
 }),f=>f.id);
 const sorts=firstOfEach('browse','sorts',keepReadable('browse','sorts',rows(raw.sorts,64),v=>{
  if(!obj(v)||!id(v.id)||!id(v.labelKey))bad();
  const directions=known('sort.directions',v.directions,2,['asc','desc']) as BrowseDirection[];
  // A sort with no direction this app knows is not offered; a default it does not know is the first direction offered.
  if(!directions.length)bad();
  const defaultDirection=directions.includes(v.defaultDirection as BrowseDirection)?v.defaultDirection as BrowseDirection:directions[0];
  return {id:v.id,labelKey:v.labelKey,directions,defaultDirection,expensive:v.expensive===true,seeded:v.seeded===true,...(v.requiresGroup===true?{requiresGroup:true}:{}),...(v.ownerOnly===true?{ownerOnly:true}:{}),applicableKinds:known('sort.applicableKinds',v.applicableKinds,64)};
 }),v=>v.id);
 const quickFilters=keepReadable('browse','quickFilters',rows(raw.quickFilters??[],32),v=>{
  if(!obj(v)||!id(v.id)||!id(v.labelKey))bad();
  return {id:v.id,labelKey:v.labelKey,query:parseBrowseNode(v.query)};
 });
 // The fields and sorts describe the resolved pivot, so it must be one this app could read.
 const resolved=raw.resolvedPivot===undefined||raw.resolvedPivot===null?null:pivot(raw.resolvedPivot);
 if(resolved&&!pivots.some(p=>p.id===resolved.id))bad();
 return freeze({
  library:{id:across?acrossLibraries:library.id as string,name:(library.name??'') as string,kind:across?'':library.kind as string,defaultView:(library.defaultView??'') as string,pinned:library.pinned===true},
  surfaces:keepReadable('browse','surfaces',rows(raw.surfaces??[],32),v=>{
   if(!obj(v)||!id(v.id)||!str(v.endpoint,256)||(v.paging!=='keyset'&&v.paging!=='offset'))bad();
   return {id:v.id,endpoint:v.endpoint,paging:v.paging as BrowsePaging,sorts:known('surface.sorts',v.sorts,64),filters:known('surface.filters',v.filters,128),...(v.membershipField===undefined?{}:{membershipField:id(v.membershipField)?v.membershipField:bad()})};
  }),pivots,resolvedPivot:resolved,fields,sorts,quickFilters,
  queryLimits:{maximumDepth:limits.maximumDepth as number,maximumClauses:limits.maximumClauses as number,maximumBytes:limits.maximumBytes as number,maximumSorts:limits.maximumSorts as number,defaultLimit:limits.defaultLimit as number,maximumLimit:limits.maximumLimit as number,cursorTtlMs:limits.cursorTtlMs as number},
 });
}

/**
 * One page of a browse. The pivot, the revision, the cursor, what was applied and the bound of
 * 200 entries are exact. An entry that cannot be read is left out alone (a page none of whose
 * entries can be read is refused), a repeated entry keeps its first appearance, and the total and
 * `hasMore` are reckoned from where the page starts and how many rows the server sent.
 */
export function parseBrowseResult(raw:unknown):BrowseResult{
 if(!obj(raw)||!id(raw.pivot)||!obj(raw.applied)||!obj(raw.pageInfo))bad();
 const info=raw.pageInfo;
 if(!count(info.start)||!count(info.total)||!id(info.revision)||!str(info.nextCursor??'',4096))bad();
 const sent=rows(raw.entries,200);
 const displayTargets=raw.displayTargets===undefined?undefined:rows(raw.displayTargets,60).map((v,index)=>{
  if(!obj(v)||!id(v.membershipId)||!id(v.entryId)||!obj(sent[index])||sent[index].id!==v.entryId)bad();
  return {membershipId:v.membershipId,entryId:v.entryId};
 });
 if(displayTargets&&(displayTargets.length!==sent.length||new Set(displayTargets.map(t=>t.membershipId)).size!==sent.length))bad();
 // A Home projection is an exact canonical page: do not compact a stale or
 // unreadable target and accidentally change membership positions or prefix.
 const entries=displayTargets?sent.map(validateContentEntry):firstOfEach('browse','entries',keepReadable('browse','entries',sent,validateContentEntry),e=>e.id);
 if(sent.length&&!entries.length)bad();
 // Past a lower bound the server's own hasMore says whether the list goes on; the total then
 // reaches one page past what has arrived, so a virtualized list keeps growing as it scrolls.
 const lowerBound=info.totalIsLowerBound===true;
 const end=sent.length?info.start+sent.length:0;
 const more=lowerBound?info.hasMore===true||!!info.nextCursor:info.start+sent.length<info.total;
 let total=lowerBound?Math.max(info.total,more?end+Math.max(sent.length,1):end):Math.max(info.total,end);
 const positionIndex=parsePositionIndex(raw.positionIndex,total,lowerBound);
 // An anchor is proof that at least index+1 canonical members exist, even
 // past a capped count. Preserve every Unicode key and grow the virtual bound.
 if(lowerBound&&positionIndex.length)total=Math.max(total,...positionIndex.map(anchor=>anchor.index+1));
 const reachedEnd=sent.length>0&&!more;
 const duration=info.durationMs===undefined?{}:count(info.durationMs)&&typeof info.durationIsLowerBound==='boolean'?{durationMs:info.durationMs,durationIsLowerBound:info.durationIsLowerBound}:bad();
 const applied=raw.applied;
 const appliedQuery=applied.query===undefined||applied.query===null?null:parseBrowseNode(applied.query);
 const appliedSort=rows(applied.sort,3).map(sortSelection);
 const selectionStates=['wholeSelection','wholeSelectionPreparing','wholeSelectionUnavailable'].filter(key=>raw[key]!==undefined&&raw[key]!==null);
 if(selectionStates.length>1)bad();
 const unavailable=raw.wholeSelectionUnavailable;
 if(unavailable!==undefined&&(!obj(unavailable)||Object.keys(unavailable).length!==1||unavailable.reason!=='search_not_bounded'))bad();
 const wholeSelectionUnavailable=unavailable===undefined?undefined:freeze({reason:'search_not_bounded' as const});
 const wholeSelection=parseWholeSelection(raw.wholeSelection,{revision:info.revision,pivot:raw.pivot,query:appliedQuery,sort:appliedSort});
 return freeze({
  pivot:raw.pivot,
  applied:{
   query:appliedQuery,
   sort:appliedSort,
   seek:applied.seek===undefined||applied.seek===null?null:(obj(applied.seek)&&str(applied.seek.prefix,8)&&applied.seek.prefix?{prefix:applied.seek.prefix}:bad()),
  },
  entries,
  pageInfo:{start:info.start,total,totalIsLowerBound:lowerBound&&!reachedEnd,revision:info.revision,hasMore:lowerBound?more:info.start+sent.length<total,nextCursor:(info.nextCursor??'') as string,...duration},
  positionIndex,...(displayTargets?{displayTargets}:{}),
  ...(wholeSelection?{wholeSelection}:{}),...(wholeSelectionUnavailable?{wholeSelectionUnavailable}:{}),
 });
}

/** The page a descriptor must describe exactly to be retained: its revision, its pivot and its
 * applied predicate and ordering. A descriptor for another view is stale, not useful. */
export type WholeSelectionPage=Readonly<{revision:string;pivot:string;query:BrowseNode|null;sort:readonly BrowseSortSelection[]}>;

const wholeSelectionKinds:readonly string[]=['movie','show','season','episode','artist','album','song','book','audiobook_file','collection','extra','disc','author'];
const capabilityKeys:readonly string[]=['play','queue','playlist','myList','watched','collection','favorite','download','edit','refresh','delete'];
const sameJson=(a:unknown,b:unknown)=>JSON.stringify(a)===JSON.stringify(b);

/** The exact whole-view aggregate. A malformed or inconsistent descriptor is dropped alone —
 * the page still parses — so a stale or wrong server can never make a client over-count, and a
 * descriptor is retained only when it describes this exact page. */
export function parseWholeSelection(raw:unknown,page?:WholeSelectionPage):BrowseWholeSelection|undefined{
 if(raw===undefined||raw===null)return;
 try{
  if(!obj(raw)||!obj(raw.selector))return;
  // Query-only: the existing job selector's container/items forms cannot describe a library view.
  if(Object.keys(raw.selector).some(key=>key!=='query')||!obj(raw.selector.query))return;
  if(raw.unavailableActions!==undefined&&!validSelectionUnavailableActions(raw.unavailableActions))return;
  const query=raw.selector.query;
  if(Object.keys(query).some(key=>!['libraryId','pivot','filter','sort','selectionBasis'].includes(key)))return;
  if(query.selectionBasis!==undefined&&!validSelectionBasis(query.selectionBasis))return;
  if(!id(query.libraryId)||!id(query.pivot)||!(str(raw.catalogRevision,128)&&(raw.catalogRevision as string).length>0)||!count(raw.total)||!Array.isArray(raw.types)||raw.types.length>16)return;
  const filter=query.filter===undefined||query.filter===null?null:parseBrowseNode(query.filter);
  const sort=query.sort===undefined?[]:rows(query.sort,3).map(sortSelection);
  if(page){
   if(raw.catalogRevision!==page.revision||query.pivot!==page.pivot)return;
   if(!sameJson(filter,page.query)||!sameJson(sort,page.sort))return;
  }
  const seen=new Set<string>();
  let summed=0;
  const types:BrowseSelectionType[]=[];
  for(const value of raw.types as unknown[]){
   if(!obj(value)||!id(value.kind)||!wholeSelectionKinds.includes(value.kind)||seen.has(value.kind)||!count(value.count)||!obj(value.capabilityCounts)||!Array.isArray(value.libraries))return;
   seen.add(value.kind);
   const counts=value.capabilityCounts as Record<string,unknown>;
   if(Object.keys(counts).length!==capabilityKeys.length||Object.keys(counts).some(key=>!capabilityKeys.includes(key)))return;
   const capabilities={} as Record<string,number>;
   for(const key of capabilityKeys){
    const amount=counts[key];
    if(!count(amount)||amount>value.count)return;
    capabilities[key]=amount;
   }
   const libraries:Readonly<{libraryId:string;count:number}>[]=[];
   const seenLibraries=new Set<string>();
   let libraryTotal=0;
   for(const row of value.libraries as unknown[]){
    if(!obj(row)||!id(row.libraryId)||row.libraryId!==query.libraryId||seenLibraries.has(row.libraryId)||!count(row.count))return;
    seenLibraries.add(row.libraryId);
    libraryTotal+=row.count;
    libraries.push({libraryId:row.libraryId,count:row.count});
   }
   if(!libraries.length||libraryTotal!==value.count)return;
   summed+=value.count;
   types.push({kind:value.kind,count:value.count,capabilities:capabilities as BrowseSelectionCapabilities,libraries:libraries});
  }
  if(summed!==raw.total)return;
  return freeze({libraryId:query.libraryId,pivot:query.pivot,filter,sort,catalogRevision:raw.catalogRevision,...(query.selectionBasis===undefined?{}:{selectionBasis:query.selectionBasis as string}),total:raw.total,types,...(raw.unavailableActions===undefined?{}:{unavailableActions:raw.unavailableActions})});
 }catch{
  // A malformed optional descriptor never fails the page it rides on.
  return undefined;
 }
}

/** Anchors for the position rail, on the first page of every sort (M20): letters for
 * title (either direction), years (decades past 240 anchors) for year, months (years
 * past 240) for added and lastPlayed, whole points for the ratings, half hours in
 * minutes for duration, and an empty key for rows without a value. The field is
 * optional and best-effort: a missing index, an entry whose shape is unknown, or an
 * anchor that is out of order or past the total is dropped rather than failing the page. */
function parsePositionIndex(v:unknown,total:number,lowerBound=false):BrowsePositionAnchor[]{
 if(v===undefined||v===null)return [];
 if(!Array.isArray(v))return [];
 const out:BrowsePositionAnchor[]=[];
 let previous=-1;
 for(const entry of v){
  if(!obj(entry)||!str(entry.key,8)||!count(entry.index))continue;
  const index=entry.index as number;
  if(index<=previous||!lowerBound&&index>=Math.max(total,1)){noteSkipped('browse','positionIndex');continue;}
  previous=index;
  out.push({key:entry.key as string,index});
 }
 return out;
}

/** US English short months for position-rail labels ("Sep 2026"). A static table, not
 * Intl: the rail labels the server's YYYY-MM bucket keys deterministically on every
 * runtime (Hermes included), and US English is the product source locale. */
const shortMonths:readonly string[]=['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];

/** Rail label for one anchor key under the given engine sort (M20). Keys are server
 * buckets, so most display as-is (letters, years like "2019", decades like "1990s");
 * months ("2026-09") read "Sep 2026", whole points ("8") read "8★", half hours in
 * minutes ("90") read "1h 30m", and an empty key reads `noValue` (the caller's
 * catalogue string, e.g. `library.anchorNoValue`). Unknown sorts and unparseable keys
 * pass through untouched, never fatal. */
export function formatBrowseAnchor(key:string,sortField:string,noValue=''):string{
 if(!key)return noValue;
 if(sortField==='personalRating'&&/^\d+$/.test(key))return `${Number(key)/2}★`;
 if(sortField==='communityRating')return `${key}★`;
 if(sortField==='duration'){
  const minutes=Number(key);
  if(!Number.isFinite(minutes)||minutes<0)return key;
  const h=Math.floor(minutes/60),m=Math.floor(minutes%60);
  if(h>0)return m?`${h}h ${m}m`:`${h}h`;
  return `${m}m`;
 }
 if(sortField==='added'||sortField==='lastPlayed'){
  const month=/^(\d{4})-(\d{2})$/.exec(key);
  if(month){
   const mi=Number(month[2]);
   if(mi>=1&&mi<=12)return `${shortMonths[mi-1]} ${month[1]}`;
  }
 }
 return key;
}

/** The values of one facet. The field asked for and the bound of 200 are exact; a value that cannot be read is left out alone, a repeated one keeps its first appearance, a value without a label is labelled by itself, and a missing count is 0. */
export function parseBrowseFacets(raw:unknown,expected?:string,variant?:'page'):BrowseFacetPage{
 if(!obj(raw)||(variant!==undefined&&raw.variant!==variant)||!count(raw.total)||!id(raw.field)||(expected!==undefined&&raw.field!==expected))bad();
 const values=firstOfEach('browse','facets',keepReadable('browse','facets',rows(raw.values,200),v=>{
  const attribution=raw.field==='author'||raw.field==='series';
  if(!obj(v)||!(attribution?str(v.value,4096)&&v.value.length>0&&!/[\r\n]/.test(v.value):id(v.value))||!str(v.label??'',attribution?4096:512))bad();
  const seriesId=raw.field==='series'&&typeof v.seriesId==='string'&&/^[A-Za-z0-9_-]{43}$/.test(v.seriesId)?v.seriesId:undefined;
  return {value:v.value as string,label:(v.label||v.value) as string,count:count(v.count)?v.count:0,...(v.countIsLowerBound===true?{countIsLowerBound:true}:{}),...(seriesId?{seriesId}:{})};
 }),v=>v.value);
 if(raw.total<values.length)bad();
 return freeze({field:raw.field,total:raw.total,values,...(raw.totalIsLowerBound===true?{totalIsLowerBound:true}:{}),...(id(raw.revision)?{revision:raw.revision}:{})});
}

/** Validates one node against the published vocabulary, naming the first failure
 * with the same path the server would return. */
export function validateBrowseQuery(capabilities:BrowseCapabilities,node:BrowseNode,path='query',depth=0):void{
 const limits=capabilities.queryLimits;
 if(depth>limits.maximumDepth)throw new BrowseQueryError(path,`Nest at most ${limits.maximumDepth} levels.`);
 if('field' in node){
  const field=capabilities.fields.find(f=>f.id===node.field);
  if(!field)throw new BrowseQueryError(path+'.field',`This library cannot filter on ${node.field}.`);
  if(!field.operators.includes(node.operator))throw new BrowseQueryError(path+'.operator',`${node.operator} is not available for ${node.field}.`);
  const value=node.value;
  if(node.operator==='within-last-days'){
   if(field.type!=='date'||typeof value!=='number'||!Number.isInteger(value)||value<1||value>36500)throw new BrowseQueryError(path+'.value','Choose 1 to 36500 whole days.');
   return;
  }
  if(presenceOperators.includes(node.operator)){
   if(value!==null)throw new BrowseQueryError(path+'.value','Presence filters take no value.');
   return;
  }
  if(field.type==='boolean'){
   if(typeof value!=='boolean')throw new BrowseQueryError(path+'.value','Choose on or off.');
   return;
  }
  const list=listOperators.includes(node.operator);
  if(list!==Array.isArray(value))throw new BrowseQueryError(path+'.value',list?'This filter takes a list of values.':'This filter takes one value.');
  const candidates=Array.isArray(value)?value:[value];
  if(list&&(candidates.length<1||candidates.length>100||(node.operator==='between'&&candidates.length!==2)))throw new BrowseQueryError(path+'.value','Choose a valid number of values.');
  for(const candidate of candidates){
   if(field.type==='number'){if(!finite(candidate))throw new BrowseQueryError(path+'.value','Enter a number.');continue;}
   if(typeof candidate!=='string'||!candidate.trim()||candidate.length>500)throw new BrowseQueryError(path+'.value','Enter a value.');
   if(field.type==='date'&&!/^\d{4}-\d{2}-\d{2}(T.*)?$/.test(candidate))throw new BrowseQueryError(path+'.value','Use a calendar date.');
   if(field.allowedValues&&!field.allowedValues.includes(candidate))throw new BrowseQueryError(path+'.value','Choose a published value.');
  }
  return;
 }
 if('not' in node){validateBrowseQuery(capabilities,node.not,path+'.not',depth+1);return;}
 const group='all' in node?'all':'any';
 const children=('all' in node?node.all:node.any);
 if(children.length<1||children.length>limits.maximumClauses)throw new BrowseQueryError(`${path}.${group}`,'Add at least one condition.');
 children.forEach((child,index)=>validateBrowseQuery(capabilities,child,`${path}.${group}[${index}]`,depth+1));
}

function countClauses(node:BrowseNode):number{
 if('field' in node)return 1;
 if('not' in node)return countClauses(node.not);
 const children='all' in node?node.all:node.any;
 return children.reduce((total,child)=>total+countClauses(child),0);
}

/** Builds one POST /v1/libraries/{id}/browse body and refuses to produce a
 * request the published capabilities cannot execute. */
export class BrowseQueryBuilder{
 private readonly capabilities:BrowseCapabilities;
 private readonly pivot:BrowsePivotCapability;
 private predicates:BrowseNode[]=[];
 private sorts:BrowseSortSelection[]=[];
 private pageLimit?:number;
 private pageCursor?:string;
 private pageRange?:BrowseRange;
 private pageSeek?:BrowseSeek;
 private displayRow?:string;
 constructor(capabilities:BrowseCapabilities,pivotId?:string){
  this.capabilities=capabilities;
  const wanted=pivotId??capabilities.resolvedPivot?.id??capabilities.pivots.find(p=>p.browsable)?.id;
  const pivot=capabilities.pivots.find(p=>p.id===wanted);
  if(!pivot)throw new BrowseQueryError('pivot','That view is not available in this library.');
  if(!pivot.browsable)throw new BrowseQueryError('pivot','That view is not queryable.');
  this.pivot=pivot;
  this.capabilities={...capabilities,fields:pivot.filters?.length?capabilities.fields.filter(field=>pivot.filters?.includes(field.id)):capabilities.fields};
 }
 /** Adds one condition to the implicit conjunction. */
 where(field:string,operator:BrowseOperator,value:BrowseScalar|readonly BrowseScalar[]|null=null):this{
  const node:BrowseNode={variant:operator==='within-last-days'?'relative':'predicate',field,operator,value};
  validateBrowseQuery(this.capabilities,node,`query.all[${this.predicates.length}]`);
  this.predicates.push(node);
  return this;
 }
 /** Adds a pre-built node, such as a published quick filter. */
 add(node:BrowseNode):this{
  validateBrowseQuery(this.capabilities,node,`query.all[${this.predicates.length}]`);
  this.predicates.push(node);
  return this;
 }
 quickFilter(quickFilterId:string):this{
  const filter=this.capabilities.quickFilters.find(f=>f.id===quickFilterId);
  if(!filter)throw new BrowseQueryError('query','That quick filter is not available.');
  return this.add(filter.query);
 }
 sort(field:string,direction?:BrowseDirection,seed?:number):this{
  const capability=this.capabilities.sorts.find(s=>s.id===field);
  if(!capability||(this.pivot.sorts?.length&&!this.pivot.sorts.includes(field)))throw new BrowseQueryError(`sort[${this.sorts.length}].field`,'That sort is not available here.');
  const chosen=direction??capability.defaultDirection;
  if(!capability.directions.includes(chosen))throw new BrowseQueryError(`sort[${this.sorts.length}].direction`,'That direction is not available.');
  if(this.sorts.some(entry=>entry.field===field))throw new BrowseQueryError(`sort[${this.sorts.length}].field`,'That sort is already applied.');
  if(this.sorts.length>=this.capabilities.queryLimits.maximumSorts)throw new BrowseQueryError('sort',`Use at most ${this.capabilities.queryLimits.maximumSorts} sorts.`);
  if(seed!==undefined&&(!capability.seeded||!count(seed)||seed>4294967295))throw new BrowseQueryError(`sort[${this.sorts.length}].seed`,'That random seed is not usable.');
  this.sorts.push({field,direction:chosen,...(seed===undefined?{}:{seed})});
  return this;
 }
 limit(value:number):this{
  const limits=this.capabilities.queryLimits;
  if(!Number.isSafeInteger(value)||value<1||value>limits.maximumLimit)throw new BrowseQueryError('limit',`Ask for between 1 and ${limits.maximumLimit} entries.`);
  this.pageLimit=value;
  return this;
 }
 cursor(value:string):this{
  if(!str(value,4096)||!value)throw new BrowseQueryError('cursor','That continuation is not usable.');
  if(this.pageRange)throw new BrowseQueryError('cursor','A range and a cursor cannot be combined.');
  this.pageCursor=value;
  return this;
 }
 range(value:BrowseRange):this{
  if(!Number.isSafeInteger(value.start)||value.start<0)throw new BrowseQueryError('range.start','Start at zero or later.');
  if(this.pageCursor)throw new BrowseQueryError('range','A range and a cursor cannot be combined.');
  this.pageRange={start:value.start,...(value.revision?{revision:value.revision}:{}),...(value.anchorId?{anchorId:value.anchorId}:{})};
  return this;
 }
 /** Seeking is only meaningful under the letter index, which the server
  * publishes only for an ascending title sort. */
 seek(prefix:string):this{
  if(!prefix||prefix.length>8)throw new BrowseQueryError('seek.prefix','Choose a letter.');
  const first=this.sorts[0]??this.pivot.defaultSort[0];
  if(!first||first.field!=='title'||first.direction!=='asc')throw new BrowseQueryError('seek','Sort by title A to Z to jump to a letter.');
  this.pageSeek={prefix};
  return this;
 }
 /** Home-origin See all alone opts into actual episode/season cards. */
 displayHomeRow(rowId:string):this{
  if(!id(rowId))throw new BrowseQueryError('homeRowDisplay','Choose a Home row.');
  this.displayRow=rowId;return this;
 }
 build():BrowseRequestBody{
  const limits=this.capabilities.queryLimits;
  const query=this.predicates.length===0?undefined:this.predicates.length===1?this.predicates[0]:{variant:'group' as const,all:[...this.predicates]};
  if(query&&countClauses(query)>limits.maximumClauses)throw new BrowseQueryError('query',`Use at most ${limits.maximumClauses} conditions.`);
  if(this.displayRow){
   if(!positiveHomeRow(query,this.displayRow))throw new BrowseQueryError('homeRowDisplay','Use the same positive Home row condition.');
   if((this.pageLimit??this.capabilities.queryLimits.defaultLimit)>60)throw new BrowseQueryError('limit','Ask for at most 60 Home display entries.');
  }
  const body:BrowseRequestBody={pivot:this.pivot.id,...(this.displayRow?{homeRowDisplay:this.displayRow}:{}),...(query?{query}:{}),...(this.sorts.length?{sort:[...this.sorts]}:{}),...(this.pageLimit?{limit:this.pageLimit}:{}),...(this.pageCursor?{cursor:this.pageCursor}:{}),...(this.pageRange?{range:this.pageRange}:{}),...(this.pageSeek?{seek:this.pageSeek}:{})};
  if(JSON.stringify(body).length>limits.maximumBytes)throw new BrowseQueryError('query','That query is too large.');
  return freeze(body);
 }
}

/** Open a person's titles, a collection, a container, a list or search results
 * through the same server browse surface as the library. */
export function browseWithin(capabilities:BrowseCapabilities,surfaceId:string,value:string,pivotId?:string):BrowseQueryBuilder{
 const surface=capabilities.surfaces?.find(s=>s.id===surfaceId);
 if(!surface?.membershipField)throw new BrowseQueryError('surface','That title group is not available.');
 return new BrowseQueryBuilder(capabilities,pivotId).where(surface.membershipField,surfaceId==='search-results'?'contains':'equals',value);
}

function positiveHomeRow(node:BrowseNode|undefined,rowId:string):boolean{
 if(!node)return false;
 if('field' in node)return node.field==='homeRow'&&node.operator==='equals'&&node.value===rowId;
 return 'all' in node&&node.all.filter(child=>positiveHomeRow(child,rowId)).length===1;
}
/** Stable work identity for virtual positions/anchors; Entry.id remains the
 * actual navigation, selection and playback target. Never infer one from another. */
export function browseMembershipId(page:BrowseResult,index:number):string|undefined{
 return page.displayTargets?.[index]?.membershipId??page.entries[index]?.id;
}
/** Canonical anchor for a currently displayed entry, including Home's genuine
 * displayTargets mapping. A missing entry is not guessed from a parent id. */
export function browseMembershipAnchor(page:Pick<BrowseResult,'entries'|'displayTargets'>,entryId:string):string|undefined{
 const mapped=page.displayTargets?.find(target=>target.entryId===entryId);
 return mapped?.membershipId??(page.entries.some(entry=>entry.id===entryId)?entryId:undefined);
}
export type BrowseCount=Readonly<{id:string;total:number;totalIsLowerBound:boolean;revision:string}>;
export type BrowseCountQuery=Readonly<{id:string;query:BrowseNode|null}>;
export const browseCountsPath=(libraryId:string):string=>libraryId===acrossLibraries?routePath('browse_across_counts'):routePath('browse_library_counts',{id:libraryId});
/** One request for the empty state's candidates. UI callers debounce/cancel it
 * with the panel lifecycle; it does not run another full browse for counts. */
export function browseCountsRequest(pivot:string,queries:readonly BrowseCountQuery[]):Readonly<{pivot:string;queries:readonly BrowseCountQuery[]}>{
 if(!id(pivot)||queries.length<1||queries.length>8||new Set(queries.map(q=>q.id)).size!==queries.length)bad();
 return freeze({pivot,queries:queries.map(q=>{if(!id(q.id)||q.id.length>128)bad();return {id:q.id,query:q.query===null?null:parseBrowseNode(q.query)};})});
}
export function parseBrowseCounts(raw:unknown,expected:readonly string[]):readonly BrowseCount[]{
 if(!obj(raw))bad();const sent=rows(raw.counts,8);if(sent.length!==expected.length)bad();
 return freeze(sent.map((v,index)=>{if(!obj(v)||v.id!==expected[index]||!count(v.total)||typeof v.totalIsLowerBound!=='boolean'||!id(v.revision))bad();return {id:v.id as string,total:v.total,totalIsLowerBound:v.totalIsLowerBound,revision:v.revision};}));
}
/** Contextual facet URL; query includes current filters and immutable group.
 * The server leaves the counted field out and rechecks current authority. */
export function browseContextFacetsPath(libraryId:string,field:string,pivot:string,query?:BrowseNode,q='',limit=200,offset=0):string{
 if(!id(field)||!id(pivot)||!count(limit)||limit<1||limit>200||q.length>100||!count(offset)||(offset>0&&libraryId===acrossLibraries))bad();
 const params=new URLSearchParams({field,pivot,limit:String(limit)});
 if(offset>0)params.set('offset',String(offset));
 if(query)params.set('query',JSON.stringify(parseBrowseNode(query)));
 if(q)params.set('q',q);
 return browseFacetsPath(libraryId)+'?'+params.toString();
}
export type BrowseRememberedTotal=Readonly<{total:number;totalIsLowerBound:boolean}>;
/** Last authoritative unfiltered total, scoped to the caller's viewer/library
 * identity. Applying filters never launches a second count or replaces it. */
export class BrowseTotalsMemory{
 private totals=new Map<string,BrowseRememberedTotal>();
 observe(scope:string,page:BrowseResult,unfiltered:boolean):BrowseRememberedTotal|undefined{
  if(unfiltered){if(!this.totals.has(scope)&&this.totals.size>=64)this.totals.delete(this.totals.keys().next().value!);this.totals.set(scope,Object.freeze({total:page.pageInfo.total,totalIsLowerBound:page.pageInfo.totalIsLowerBound}));}
  return this.totals.get(scope);
 }
 clear():void{this.totals.clear();}
}
