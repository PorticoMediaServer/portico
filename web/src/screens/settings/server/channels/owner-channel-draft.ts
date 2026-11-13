import type {LibraryChannelBlock,LibraryChannelBlockPreset,LibraryChannelBuilder,LibraryChannelCondition,LibraryChannelSelection} from '../../../../../../packages/contracts/src/next/api.ts';
export const week=['sun','mon','tue','wed','thu','fri','sat'];
export const clockMinute=(value:string)=>/^([01]\d|2[0-3]):[0-5]\d$/.test(value)?Number(value.slice(0,2))*60+Number(value.slice(3)):undefined;
export const clockText=(value:number)=>`${String(Math.floor(value/60)%24).padStart(2,'0')}:${String(value%60).padStart(2,'0')}`;
export function copyBlockPreset(preset:LibraryChannelBlockPreset,kind:string,id:string):LibraryChannelBlock{
 const recipe=preset.recipe,from=recipe.startsAt||'00:00';
 return {id,name:preset.name,days:recipe.days?.length?[...recipe.days]:[...week],from,to:clockText((clockMinute(from)??0)+recipe.minutes),selection:{libraries:[preset.library],kinds:kind==='movie'?['movie']:kind==='anime'?['movie','show']:['show'],conditions:structuredClone(recipe.clauses),order:recipe.order==='recent'?'inOrder':recipe.order,sort:recipe.order==='recent'?'recent':'title'}};
}
export function blockOverlap(blocks:readonly LibraryChannelBlock[]):[LibraryChannelBlock,LibraryChannelBlock]|undefined{
 const spans:{from:number;to:number;block:LibraryChannelBlock}[]=[];
 for(const block of blocks){const start=clockMinute(block.from),end=clockMinute(block.to);if(start===undefined||end===undefined)continue;
  for(const day of block.days){const d=week.indexOf(day);if(d<0)continue;const from=d*1440+start,to=d*1440+(end>start?end:end+1440);spans.push({from,to,block},{from:from-10080,to:to-10080,block});}}
 spans.sort((a,b)=>a.from-b.from);for(let i=0;i<spans.length;i++)for(let j=i+1;j<spans.length&&spans[j]!.from<spans[i]!.to;j++)if(spans[i]!.block!==spans[j]!.block)return [spans[i]!.block,spans[j]!.block];return undefined;
}
export function validConditions(rows:readonly LibraryChannelCondition[]):boolean{return rows.every(row=>row.conditions?row.conditions.length>0&&validConditions(row.conditions):!!row.field&&!!row.values?.length&&row.values.every(value=>!!value.trim()&&value!=='..'));}
export function validSelection(selection:LibraryChannelSelection){return !!selection.libraries.length&&!!selection.kinds.length&&validConditions(selection.conditions??[])&&(!selection.onlyAlways||!!selection.always?.length);}
export function validBuilder(builder:LibraryChannelBuilder){return validSelection(builder)&&!blockOverlap(builder.blocks??[])&&(builder.blocks??[]).every(block=>block.days.length>0&&clockMinute(block.from)!==undefined&&clockMinute(block.to)!==undefined&&(!block.selection||validSelection(block.selection)));}
