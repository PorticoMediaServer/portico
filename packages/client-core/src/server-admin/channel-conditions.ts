import type {BrowseFieldCapability,BrowseNode} from '../browse.ts';
import type {LibraryChannelConfig,LibraryChannelQuery} from '../library-channels.ts';
export type ChannelPredicate=Extract<BrowseNode,{field:string}>;
export type ChannelCondition=Readonly<{key:string;field:string;predicates:readonly ChannelPredicate[];not:boolean}>;
export type ChannelConditionGroup=Readonly<{key:string;mode:'all'|'any';conditions:readonly ChannelCondition[];not:boolean}>;
export type ChannelConditionItem=ChannelCondition|ChannelConditionGroup;
export type ChannelConditions=Readonly<{mode:'all'|'any';items:readonly ChannelConditionItem[];opaque?:BrowseNode}>;
const personal=new Set(['playState','favorite','watchlisted','personalRating','lastPlayedAt','entityKind']);
/** Shared channels never use a person's watched, saved or rating state. The
 * server remains authoritative; this is the same public field vocabulary. */
export const channelConditionFields=(fields:readonly BrowseFieldCapability[])=>fields.filter(field=>!personal.has(field.id));
let sequence=0;
export const channelConditionKey=()=>`channel-condition-${++sequence}`;
export const isChannelConditionGroup=(item:ChannelConditionItem):item is ChannelConditionGroup=>'conditions'in item;
function conditionNode(condition:ChannelCondition):BrowseNode|undefined{
  if(!condition.predicates.length)return;
  const node:BrowseNode=condition.predicates.length===1?condition.predicates[0]!:{variant:'group' as const,all:condition.predicates};
  return condition.not?{variant:'group' as const,not:node}:node;
}
export function channelConditionNode(conditions:ChannelConditions):BrowseNode|undefined{
  if(conditions.opaque)return conditions.opaque;
  const nodes=conditions.items.map(item=>{
    if(!isChannelConditionGroup(item))return conditionNode(item);
    const inner=item.conditions.map(conditionNode).filter((node):node is BrowseNode=>!!node);if(!inner.length)return;
    const node:BrowseNode=item.mode==='all'?{variant:'group' as const,all:inner}:{variant:'group' as const,any:inner};return item.not?{variant:'group' as const,not:node}:node;
  }).filter((node):node is BrowseNode=>!!node);
  if(!nodes.length)return;
  if(nodes.length===1&&conditions.mode==='all')return nodes[0];
  return conditions.mode==='all'?{variant:'group' as const,all:nodes}:{variant:'group' as const,any:nodes};
}
function conditionOf(input:BrowseNode):ChannelCondition|undefined{
  let node=input,not=false;if('not'in node){not=true;node=node.not;}
  if('field'in node&&!personal.has(node.field))return{key:channelConditionKey(),field:node.field,predicates:[node],not};
  if('all'in node&&node.all.length&&node.all.every((part):part is ChannelPredicate=>'field'in part&&!personal.has(part.field))&&new Set(node.all.map(part=>(part as ChannelPredicate).field)).size===1)return{key:channelConditionKey(),field:(node.all[0] as ChannelPredicate).field,predicates:node.all as readonly ChannelPredicate[],not};
}
/** A tree the drawn two-level editor cannot represent is retained whole. It is
 * never silently flattened or deleted when another field is changed. */
export function readChannelConditions(node?:BrowseNode):ChannelConditions{
  if(!node)return{mode:'all',items:[]};
  const single=conditionOf(node);if(single)return{mode:'all',items:[single]};
  const top='all'in node?{mode:'all'as const,nodes:node.all}:'any'in node?{mode:'any'as const,nodes:node.any}:undefined;
  if(!top)return{mode:'all',items:[],opaque:node};
  const items:ChannelConditionItem[]=[];
  for(const child of top.nodes){
    const condition=conditionOf(child);if(condition){items.push(condition);continue;}
    let inner=child,not=false;if('not'in inner){not=true;inner=inner.not;}
    const list='all'in inner?inner.all:'any'in inner?inner.any:undefined,conditions=list?.map(conditionOf);
    if(!list||!conditions||conditions.some(value=>!value))return{mode:'all',items:[],opaque:node};
    items.push({key:channelConditionKey(),mode:'all'in inner?'all':'any',conditions:conditions as ChannelCondition[],not});
  }
  return{mode:top.mode,items};
}
/** First-version template year/genre fields remain conditions, so reviewing a
 * template cannot accidentally broaden it to every title in its libraries. */
export function channelConditionsWithLegacy(query:Pick<LibraryChannelQuery,'filter'|'genres'|'yearFrom'|'yearThrough'>):ChannelConditions{
  const extra:BrowseNode[]=[];
  if(query.genres?.length)extra.push({variant:'predicate' as const,field:'genre',operator:'contains-any',value:[...query.genres]});
  const from=query.yearFrom||0,through=query.yearThrough||0;
  if(from&&through)extra.push({variant:'predicate' as const,field:'year',operator:'between',value:[from,through]});
  else if(from)extra.push({variant:'predicate' as const,field:'year',operator:'at-least',value:from});else if(through)extra.push({variant:'predicate' as const,field:'year',operator:'at-most',value:through});
  return readChannelConditions(extra.length?{variant:'group' as const,all:[...(query.filter?[query.filter]:[]),...extra]}:query.filter);
}
export function applyChannelConditions(config:LibraryChannelConfig,conditions:ChannelConditions):LibraryChannelConfig{
  const rule=config.rules.find(value=>value.id===config.defaultRuleId);if(!rule)throw new Error('missing_channel_default_rule');
  const filter=channelConditionNode(conditions);
  return{...config,rules:config.rules.map(value=>value===rule?{...value,query:{...value.query,filter,genres:[],yearFrom:0,yearThrough:0}}:value)};
}
