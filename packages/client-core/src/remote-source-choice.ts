/**
 * Root57 read-only choice projection. These are the canonical server-published
 * definitions and the one exact recorded baseline, parsed and compared without
 * reading or writing any source mutation. Consent is never part of a definition
 * or baseline reading: choosing or comparing a choice cannot grant it.
 */
import type {SourceReadValues,SourceAdvanced,SourceReadSettings,SourceAdvancedSettings} from './remote-sources.ts';

export const sourceChoiceNames=['list','once','everything','ask'] as const;
export type SourceChoiceName=typeof sourceChoiceNames[number];

/** The closed authored comparison order. The server publishes which of these
 * rows the actual adapter applies; this list is never a provider guess. */
export const sourceChoiceFields=[
 'scanDepth','connections','requestsPerSecond','bytesPerSecond',
 'scanIntervalSeconds','watchForChanges','scanOnStart','fullRecheckDays','doubleCheckFolders',
 'seekPreviews','chapterImages','waveforms','loudness','checksums','introsMode',
 'cacheBytes','cacheFloorBytes','dirCacheSeconds','readBufferBytes','connectTimeoutSeconds','idleTimeoutSeconds','retries','lowLevelRetries',
] as const;
export type SourceChoiceField=typeof sourceChoiceFields[number];

export type SourceChoiceDefinition=Readonly<{name:SourceChoiceName;definitionRevision:number;reading:SourceReadValues;advanced:SourceAdvanced}>;
export type SourceChoiceBaseline=Readonly<{
 sourceId:string;generation:string;configurationGeneration:string;name:SourceChoiceName;definitionRevision:number;
 readRevision:number;advancedRevision:number;reading:SourceReadValues;advanced:SourceAdvanced;currentAdvanced:SourceAdvanced;
 applicableFields:readonly SourceChoiceField[];changedFields:readonly SourceChoiceField[];
}>;
export type SourceChoiceComparison=Readonly<{name:SourceChoiceName;definitionRevision:number;applicableFields:readonly SourceChoiceField[];changedFields:readonly SourceChoiceField[]}>;

const record=(value:unknown):value is Record<string,unknown>=>typeof value==='object'&&value!==null&&!Array.isArray(value);
const integer=(value:unknown,low=0):value is number=>typeof value==='number'&&Number.isSafeInteger(value)&&value>=low;
const generation=(value:unknown):value is string=>typeof value==='string'&&/^[1-9][0-9]{0,18}$/.test(value)&&value.length<=19;
function bad(): never { throw new Error('Invalid remote source response.'); }
const definitionReadingKeys=['scanDepth','wholeFileReads','connections','requestsPerSecond','bytesPerSecond'] as const;
const advancedKeys=['scanIntervalSeconds','watchForChanges','scanOnStart','fullRecheckDays','cacheBytes','cacheFloorBytes','seekPreviews','chapterImages','waveforms','loudness','checksums','introsMode','doubleCheckFolders','dirCacheSeconds','readBufferBytes','connectTimeoutSeconds','idleTimeoutSeconds','retries','lowLevelRetries'] as const;
const closed=new Set<string>(sourceChoiceFields);
const nameSet=new Set<string>(sourceChoiceNames);

/** A definition reading is exact and consent-free: the retired whole-file flag
 * is inert false and no analysis consent appears anywhere. */
function definitionReading(raw:unknown):SourceReadValues{
 if(!record(raw))bad();
 const keys=Object.keys(raw);
 if(keys.length!==definitionReadingKeys.length||keys.some(key=>!(definitionReadingKeys as readonly string[]).includes(key)))bad();
 const {scanDepth,wholeFileReads,connections,requestsPerSecond,bytesPerSecond}=raw;
 if((scanDepth!=='file_list_only'&&scanDepth!=='basic'&&scanDepth!=='complete')||wholeFileReads!==false)bad();
 if(!integer(connections,1)||connections>32||!integer(requestsPerSecond,1)||requestsPerSecond>50)bad();
 if(!integer(bytesPerSecond)||!(bytesPerSecond===0||bytesPerSecond>=1_000_000&&bytesPerSecond<=1_000_000_000&&bytesPerSecond%1_000_000===0))bad();
 return Object.freeze({scanDepth,wholeFileReads:false,connections,requestsPerSecond,bytesPerSecond}) as SourceReadValues;
}
function definitionAdvanced(raw:unknown):SourceAdvanced{
 if(!record(raw))bad();
 const keys=Object.keys(raw);
 if(keys.length!==advancedKeys.length||keys.some(key=>!(advancedKeys as readonly string[]).includes(key)))bad();
 const {scanIntervalSeconds,watchForChanges,scanOnStart,fullRecheckDays,cacheBytes,cacheFloorBytes,seekPreviews,chapterImages,waveforms,loudness,checksums,introsMode,doubleCheckFolders,dirCacheSeconds,readBufferBytes,connectTimeoutSeconds,idleTimeoutSeconds,retries,lowLevelRetries}=raw;
 if(!integer(scanIntervalSeconds)||![0,900,3600,21600,86400,604800].includes(scanIntervalSeconds))bad();
 if(typeof watchForChanges!=='boolean'||typeof scanOnStart!=='boolean')bad();
 if(!integer(fullRecheckDays)||![0,7,14,30].includes(fullRecheckDays))bad();
 if(!integer(cacheBytes)||cacheBytes>64*1024*1024*1024||!integer(cacheFloorBytes)||cacheFloorBytes>1024*1024*1024*1024)bad();
 if(![seekPreviews,chapterImages,waveforms,loudness,checksums,doubleCheckFolders].every(value=>typeof value==='boolean'))bad();
 if(introsMode!=='off'&&introsMode!=='inherit'&&introsMode!=='sound'&&introsMode!=='picture')bad();
 for(const value of [dirCacheSeconds,readBufferBytes,connectTimeoutSeconds,idleTimeoutSeconds,retries,lowLevelRetries])if(!integer(value))bad();
 return Object.freeze({scanIntervalSeconds,watchForChanges,scanOnStart,fullRecheckDays,cacheBytes,cacheFloorBytes,seekPreviews,chapterImages,waveforms,loudness,checksums,introsMode,doubleCheckFolders,dirCacheSeconds,readBufferBytes,connectTimeoutSeconds,idleTimeoutSeconds,retries,lowLevelRetries}) as SourceAdvanced;
}
function fields(raw:unknown,allowEmpty=false):readonly SourceChoiceField[]{
 if(!Array.isArray(raw)||raw.length>sourceChoiceFields.length||(!allowEmpty&&raw.length===0))bad();
 const seen=new Set<string>();
 for(const value of raw){if(typeof value!=='string'||!closed.has(value)||seen.has(value))bad();seen.add(value);}
 return Object.freeze(raw as SourceChoiceField[]);
}
function definition(raw:unknown):SourceChoiceDefinition{
 if(!record(raw))bad();
 if(typeof raw.name!=='string'||!nameSet.has(raw.name))bad();
 if(!integer(raw.definitionRevision,1))bad();
 return Object.freeze({name:raw.name as SourceChoiceName,definitionRevision:raw.definitionRevision,reading:definitionReading(raw.reading),advanced:definitionAdvanced(raw.advanced)});
}
/** Exactly the four named definitions, each once. A missing or unknown
 * definition withholds the projection rather than being supplemented. */
export function parseSourceChoiceDefinitions(raw:unknown):readonly SourceChoiceDefinition[]|undefined{
 if(raw===undefined)return undefined;
 if(!Array.isArray(raw)||raw.length!==sourceChoiceNames.length)bad();
 const out=raw.map(definition),names=new Set(out.map(value=>value.name));
 if(names.size!==sourceChoiceNames.length)bad();
 return Object.freeze(out);
}
export function parseSourceChoiceApplicableFields(raw:unknown):readonly SourceChoiceField[]|undefined{
 return raw===undefined?undefined:fields(raw);
}
export function parseSourceChoiceBaseline(raw:unknown):SourceChoiceBaseline|undefined{
 if(raw===undefined)return undefined;
 if(!record(raw))bad();
 const allowed=['sourceId','generation','configurationGeneration','name','definitionRevision','readRevision','advancedRevision','reading','advanced','currentAdvanced','applicableFields','changedFields'];
 const keys=Object.keys(raw);
 if(keys.length!==allowed.length||keys.some(key=>!allowed.includes(key)))bad();
 const {sourceId,generation:rawGeneration,configurationGeneration,name,definitionRevision,readRevision,advancedRevision,reading,advanced,currentAdvanced}=raw;
 if(typeof sourceId!=='string'||sourceId.length<1||sourceId.length>160||!generation(rawGeneration)||!generation(configurationGeneration))bad();
 if(typeof name!=='string'||!nameSet.has(name)||!integer(definitionRevision,1)||!integer(readRevision,1)||!integer(advancedRevision,1))bad();
 const applicable=fields(raw.applicableFields),changed=fields(raw.changedFields,true);
 if(changed.length>applicable.length||changed.some(field=>!applicable.includes(field)))bad();
 return Object.freeze({sourceId,generation:rawGeneration,configurationGeneration,name:name as SourceChoiceName,definitionRevision,readRevision,advancedRevision,
  reading:definitionReading(reading),advanced:definitionAdvanced(advanced),currentAdvanced:definitionAdvanced(currentAdvanced),applicableFields:applicable,changedFields:changed});
}
const sameReading=(a:SourceReadValues,b:SourceReadValues):boolean=>a.scanDepth===b.scanDepth&&a.wholeFileReads===b.wholeFileReads&&a.connections===b.connections&&a.requestsPerSecond===b.requestsPerSecond&&a.bytesPerSecond===b.bytesPerSecond;
const sameAdvanced=(a:SourceAdvanced,b:SourceAdvanced):boolean=>advancedKeys.every(key=>a[key]===b[key]);
function differs(field:SourceChoiceField,definition:SourceChoiceDefinition,reading:SourceReadValues,advanced:SourceAdvanced):boolean{
 switch(field){
  case 'scanDepth':return reading.scanDepth!==definition.reading.scanDepth;
  case 'connections':return reading.connections!==definition.reading.connections;
  case 'requestsPerSecond':return reading.requestsPerSecond!==definition.reading.requestsPerSecond;
  case 'bytesPerSecond':return reading.bytesPerSecond!==definition.reading.bytesPerSecond;
  default:return advanced[field]!==definition.advanced[field];
 }
}
/** Compare one projection only when every original witness matches the same
 * snapshot and the server's changed set is exactly the applicable difference.
 * Anything absent, older, unknown or incoherent withholds the comparison; the
 * current read facts stay published either way. */
export function sourceChoiceComparison(settings:Readonly<{sourceId:string;generation:string;revision:number;configurationGeneration?:string;values:SourceReadValues;choiceDefinitions?:readonly SourceChoiceDefinition[];choiceApplicableFields?:readonly SourceChoiceField[];choiceBaseline?:SourceChoiceBaseline}>):SourceChoiceComparison|undefined{
 const baseline=settings.choiceBaseline,definitions=settings.choiceDefinitions,applicableFields=settings.choiceApplicableFields;
 if(!baseline||!definitions||!applicableFields||!settings.configurationGeneration)return undefined;
 if(baseline.sourceId!==settings.sourceId||baseline.generation!==settings.generation||baseline.configurationGeneration!==settings.configurationGeneration||baseline.readRevision!==settings.revision)return undefined;
 if(baseline.applicableFields.length!==applicableFields.length||baseline.applicableFields.some((field,index)=>field!==applicableFields[index]))return undefined;
 const match=definitions.filter(value=>value.name===baseline.name&&value.definitionRevision===baseline.definitionRevision);
 if(match.length!==1)return undefined;
 const definition=match[0]!;
 if(!sameReading(definition.reading,baseline.reading)||!sameAdvanced(definition.advanced,baseline.advanced))return undefined;
 const changed=baseline.applicableFields.filter(field=>differs(field,definition,settings.values,baseline.currentAdvanced));
 if(changed.length!==baseline.changedFields.length||changed.some((field,index)=>field!==baseline.changedFields[index]))return undefined;
 return Object.freeze({name:baseline.name,definitionRevision:baseline.definitionRevision,applicableFields:baseline.applicableFields,changedFields:baseline.changedFields});
}


/** The exact explicit choice a later write will name. It is intent, not a grant. */
export type SourceChoiceStart=Readonly<{name:SourceChoiceName;definitionRevision:number;expectedAdvancedRevision:number;expectedConfigurationGeneration:string}>;
/** One frozen original-pair capture for a first or repeat explicit choice. The
 * wire body later selects only the start-choice and read controls; metadata here
 * never leaves the process. */
export type SourceChoiceCommand=Readonly<{sourceId:string;expectedGeneration:string;expectedRevision:number;values:SourceReadValues;
 startChoice:SourceChoiceStart;definition:SourceChoiceDefinition;applicableFields:readonly SourceChoiceField[];originalAdvanced:SourceAdvanced;originalConsent?:boolean}>;

/** Actual saved reading, including the independent consent fact (validated, not
 * forwarded in startChoice or definition). */
function actualReading(raw:unknown):SourceReadValues{
 if(!record(raw))bad();
 const {scanDepth,wholeFileReads,connections,requestsPerSecond,bytesPerSecond,allowAdvancedMediaAnalysis}=raw;
 if((scanDepth!=='file_list_only'&&scanDepth!=='basic'&&scanDepth!=='complete')||typeof wholeFileReads!=='boolean')bad();
 if(!integer(connections,1)||connections>32||!integer(requestsPerSecond,1)||requestsPerSecond>50)bad();
 if(!integer(bytesPerSecond)||!(bytesPerSecond===0||bytesPerSecond>=1_000_000&&bytesPerSecond<=1_000_000_000&&bytesPerSecond%1_000_000===0))bad();
 if(allowAdvancedMediaAnalysis!==undefined&&typeof allowAdvancedMediaAnalysis!=='boolean')bad();
 return Object.freeze({scanDepth,wholeFileReads,connections,requestsPerSecond,bytesPerSecond,...(allowAdvancedMediaAnalysis===undefined?{}:{allowAdvancedMediaAnalysis})}) as SourceReadValues;
}

/** Capture the original read and Advanced pair for an offered definition. The
 * offered object must be the exact one the read published, so a substituted
 * clone cannot be turned into intent. No baseline is required: a first explicit
 * choice works from the original revisions and effective generation. Every
 * retained value is a frozen copy; invalid or mismatched input returns undefined. */
export function sourceChoiceCommand(read:SourceReadSettings,advanced:SourceAdvancedSettings,offered:SourceChoiceDefinition):SourceChoiceCommand|undefined{
 try{
  const capturedDefinition=definition(offered);
  const originalDefinitions=read.choiceDefinitions;
  if(!originalDefinitions||!originalDefinitions.includes(offered))return undefined;
  // The whole published set must still be the complete four, even though the
  // identity check is against the original array the caller actually read.
  parseSourceChoiceDefinitions(originalDefinitions);
  const sourceId=read.sourceId,rawGeneration=read.generation,configurationGeneration=read.configurationGeneration,originalRevision=read.revision;
  const advancedSourceId=advanced.sourceId,advancedRawGeneration=advanced.generation,advancedRevision=advanced.revision;
  const readSaved=read.saved,advancedSaved=advanced.saved;
  const values=actualReading(read.values),originalAdvanced=definitionAdvanced(advanced.values),applicable=fields([...(read.choiceApplicableFields??[])]);
  if(typeof sourceId!=='string'||sourceId.length<1||sourceId.length>160||sourceId!==advancedSourceId)return undefined;
  if(!generation(rawGeneration)||rawGeneration!==advancedRawGeneration||!generation(configurationGeneration))return undefined;
  if(typeof readSaved!=='boolean'||typeof advancedSaved!=='boolean'||!integer(originalRevision)||!integer(advancedRevision))return undefined;
  if(originalRevision===0&&readSaved||advancedRevision===0&&advancedSaved)return undefined;
  const startChoice=Object.freeze({name:capturedDefinition.name,definitionRevision:capturedDefinition.definitionRevision,
   expectedAdvancedRevision:advancedRevision,expectedConfigurationGeneration:configurationGeneration});
  const wireValues=Object.freeze({scanDepth:capturedDefinition.reading.scanDepth,wholeFileReads:values.wholeFileReads,
   connections:capturedDefinition.reading.connections,requestsPerSecond:capturedDefinition.reading.requestsPerSecond,bytesPerSecond:capturedDefinition.reading.bytesPerSecond}) as SourceReadValues;
  return Object.freeze({sourceId,expectedGeneration:rawGeneration,expectedRevision:originalRevision,values:wireValues,
   startChoice,definition:capturedDefinition,applicableFields:applicable,originalAdvanced,
   ...(values.allowAdvancedMediaAnalysis===undefined?{}:{originalConsent:values.allowAdvancedMediaAnalysis})});
 }catch{return undefined;}
}


/** The exact one-PUT body for an explicit choice. Only the selected four read
 * controls plus the original compatibility flag travel; consent never does, and
 * the Advanced object is not sent separately. */
export function sourceChoicePutBody(command:SourceChoiceCommand,operationId:string):Readonly<{operationId:string;expectedGeneration:string;expectedRevision:number;values:SourceReadValues;startChoice:SourceChoiceStart}>{
 if(typeof operationId!=='string'||!/^[0-9]{13}-[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/.test(operationId))bad();
 return Object.freeze({operationId,expectedGeneration:command.expectedGeneration,expectedRevision:command.expectedRevision,values:command.values,startChoice:command.startChoice});
}

/** Actual immutable parent acceptance, not a cache-applied or current claim. */
export type SourceChoiceAcceptance=Readonly<{settings:SourceReadSettings;readRevision:number;advancedRevision:number}>;
const sameFields=(a:readonly SourceChoiceField[],b:readonly SourceChoiceField[])=>a.length===b.length&&a.every((value,index)=>value===b[index]);

/** Check definition, current applicable values and preserved independent facts
 * against the ORIGINAL command. The enclosing generations belong to this very
 * snapshot; a causal child need not retain the parent's generations. */
function choiceAssociation(settings:SourceReadSettings,command:SourceChoiceCommand):SourceChoiceComparison|undefined{
 const baseline=settings.choiceBaseline,definitions=settings.choiceDefinitions,applicable=settings.choiceApplicableFields;
 if(!baseline||!definitions||!applicable||settings.saved!==true||settings.sourceId!==command.sourceId)return undefined;
 if(!generation(settings.generation)||!generation(settings.configurationGeneration))return undefined;
 if(!sameFields(applicable,command.applicableFields)||!sameFields(baseline.applicableFields,command.applicableFields))return undefined;
 if(baseline.name!==command.startChoice.name||baseline.definitionRevision!==command.startChoice.definitionRevision)return undefined;
 const offered=definitions.filter(value=>value.name===command.definition.name&&value.definitionRevision===command.definition.definitionRevision);
 if(offered.length!==1||!sameReading(offered[0]!.reading,command.definition.reading)||!sameAdvanced(offered[0]!.advanced,command.definition.advanced))return undefined;
 if(!sameReading(baseline.reading,command.definition.reading)||!sameAdvanced(baseline.advanced,command.definition.advanced))return undefined;
 if(!sameReading(settings.values,command.values)||settings.values.allowAdvancedMediaAnalysis!==command.originalConsent)return undefined;
 for(const field of advancedKeys){
  const expected=command.applicableFields.includes(field)?command.definition.advanced[field]:command.originalAdvanced[field];
  if(baseline.currentAdvanced[field]!==expected)return undefined;
 }
 const comparison=sourceChoiceComparison(settings);
 return comparison&&comparison.changedFields.length===0?comparison:undefined;
}

/** Validate the immutable parent BEFORE recording acceptance. A no-op read may
 * keep its revision; Advanced must advance. Retain the actual returned pair. */
export function sourceChoiceAccepted(settings:SourceReadSettings,command:SourceChoiceCommand):SourceChoiceAcceptance|undefined{
 if(settings.generation!==command.expectedGeneration||settings.configurationGeneration!==command.startChoice.expectedConfigurationGeneration)return undefined;
 const baseline=settings.choiceBaseline;
 if(!baseline||!integer(settings.revision,1)||!integer(baseline.advancedRevision,1)||settings.revision<command.expectedRevision||baseline.advancedRevision<=command.startChoice.expectedAdvancedRevision)return undefined;
 if(!choiceAssociation(settings,command))return undefined;
 return Object.freeze({settings,readRevision:settings.revision,advancedRevision:baseline.advancedRevision});
}

/** Reconcile only the accepted revision pair and its exact recorded association.
 * The fresh parent or causal child must cohere with its OWN generations; equal
 * values at another operation's later revisions do not qualify. */
export function sourceChoiceAdopt(settings:SourceReadSettings,command:SourceChoiceCommand,accepted:SourceChoiceAcceptance):SourceChoiceComparison|undefined{
 const parent=sourceChoiceAccepted(accepted.settings,command);
 if(!parent||parent.readRevision!==accepted.readRevision||parent.advancedRevision!==accepted.advancedRevision)return undefined;
 if(settings.revision!==parent.readRevision||settings.choiceBaseline?.advancedRevision!==parent.advancedRevision)return undefined;
 return choiceAssociation(settings,command);
}

/** Reset uses only the recorded name/version and its exact published definition,
 * never the latest definition of the same name. A first apply needs no reset. */
export function sourceChoiceResetDefinition(settings:SourceReadSettings):SourceChoiceDefinition|undefined{
 const comparison=sourceChoiceComparison(settings);
 return comparison?settings.choiceDefinitions?.find(value=>value.name===comparison.name&&value.definitionRevision===comparison.definitionRevision):undefined;
}
