import type {GetReadingContinueResponse} from '../../contracts/src/index.ts';
/** Source-owned strict provenance over the officially decoded publication.
 * Parsed from actual response facts; never invents conversion support or grants.
 */
export type ReadingConversion = Readonly<{
  originalRevision:string; originalSourceId:string; originalFormat:'mobi'|'azw3'; originalSize:number;
  inputSHA256:string; converter:'libmobi:85dcfe803fc2a21020ddcf15c3eb66b93d388add'; workerSHA256:string;
  options:'portico-epub-v1;unencrypted-only;no-replica;default-kf8'; outputSHA256:string; outputSize:number; revision:string;
}>;
const keys=['originalRevision','originalSourceId','originalFormat','originalSize','inputSHA256','converter','workerSHA256','options','outputSHA256','outputSize','revision'] as const;
const record=(value:unknown):value is Record<string,unknown>=>!!value&&typeof value==='object'&&!Array.isArray(value);
const hash=(value:unknown):value is string=>typeof value==='string'&&/^[a-f0-9]{64}$/.test(value);
const size=(value:unknown,max:number):value is number=>typeof value==='number'&&Number.isSafeInteger(value)&&value>0&&value<=max;
type ConversionBinding={format:string;sourceId:string;revision:string;size:number;sequential?:boolean};
function parseConversion(supplied:unknown,base:ConversionBinding):ReadingConversion|undefined {
  if(supplied===undefined)return undefined;
  if(!record(supplied)||Object.keys(supplied).length!==keys.length||keys.some(key=>!Object.hasOwn(supplied,key))||Object.keys(supplied).some(key=>!keys.some(allowed=>allowed===key))
    ||base.format!=='epub'||base.sequential===true||supplied.originalSourceId!==base.sourceId
    ||!hash(supplied.originalRevision)||typeof supplied.originalSourceId!=='string'||!supplied.originalSourceId.length||supplied.originalSourceId.length>256
    ||(supplied.originalFormat!=='mobi'&&supplied.originalFormat!=='azw3')||!size(supplied.originalSize,64<<20)
    ||!hash(supplied.inputSHA256)||supplied.converter!=='libmobi:85dcfe803fc2a21020ddcf15c3eb66b93d388add'||!hash(supplied.workerSHA256)
    ||supplied.options!=='portico-epub-v1;unencrypted-only;no-replica;default-kf8'||!hash(supplied.outputSHA256)
    ||!size(supplied.outputSize,128<<20)||supplied.outputSize!==base.size||!hash(supplied.revision)||supplied.revision!==base.revision)throw new Error('invalid_reading_conversion');
  return Object.freeze({originalRevision:supplied.originalRevision,originalSourceId:supplied.originalSourceId,originalFormat:supplied.originalFormat,originalSize:supplied.originalSize,inputSHA256:supplied.inputSHA256,converter:supplied.converter,workerSHA256:supplied.workerSHA256,options:supplied.options,outputSHA256:supplied.outputSHA256,outputSize:supplied.outputSize,revision:supplied.revision});
}
/** The base still passes the canonical decoder, including its normal facts hook. */
export function withReadingConversion<T extends ConversionBinding>(raw:unknown,decode:(value:unknown)=>T):T&Readonly<{conversion?:ReadingConversion}> {
  const plain=record(raw)?{...raw}:undefined,supplied=plain?.conversion;
  if(plain)delete plain.conversion;
  const base=decode(plain??raw),conversion=parseConversion(supplied,base);
  return conversion?Object.freeze({...base,conversion}):base;
}
type ConvertedContinueItem=Omit<GetReadingContinueResponse['items'][number],'conversion'>&Readonly<{conversion?:ReadingConversion}>;
type ConvertedContinue=Omit<GetReadingContinueResponse,'items'>&{items:ConvertedContinueItem[]};
const sameSourceFormat=(original:string,source:string)=>original===source;
/** A nonstale converted observation carries an actual retained-output witness.
 * This never converts or changes the saved CFI/page/revision to another output. */
export function withReadingContinueConversions(raw:unknown,decode:(value:unknown)=>GetReadingContinueResponse):ConvertedContinue {
  const supplied=record(raw)&&Array.isArray(raw.items)?raw.items:undefined;
  const plain=supplied?.map(row=>{if(!record(row))return row;const copy={...row};delete copy.conversion;return copy;});
  const base=decode(record(raw)&&plain?{...raw,items:plain}:raw);
  const items=base.items.map((entry,index)=>{
    const original=supplied?.[index],value=record(original)?original.conversion:undefined;
    const conversion=parseConversion(value,{format:entry.progress.location.format,sourceId:entry.item.sourceId,revision:entry.progress.sourceRevision,size:record(value)&&typeof value.outputSize==='number'?value.outputSize:NaN});
    if(conversion&&(entry.progress.stale||!sameSourceFormat(conversion.originalFormat,entry.item.format)))throw new Error('invalid_reading_conversion');
    // The canonical type now includes broad provenance strings. Only this
    // strictly source/revision-bound parser may publish the pinned witness.
    const {conversion: _decodedConversion, ...originalEntry}=entry;
    return conversion?Object.freeze({...originalEntry,conversion}):originalEntry;
  });
  return {...base,items};
}
