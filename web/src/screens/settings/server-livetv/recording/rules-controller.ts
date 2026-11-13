import {readOwnerRecordingRules,saveOwnerRecordingRule,removeOwnerRecordingRule,readRecordingRuleProgrammes,readRecordingRuleChannels,type DvrRule,type DvrRuleSave,type RecordingRuleProgramme} from '@core/server-admin/owner-recording-rules.ts';
import {readOwnerDvrSettings,type OwnerDvrSettings} from '@core/server-admin/owner-dvr-settings.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {RecordingPermissionsController} from './permissions-controller';
import {OwnerStore,errorFacts} from '../../../live/owner/lifetime';
export type OwnerRule=DvrRule;
export type RuleDraft={base?:DvrRule;profileId:string;programme?:string;title:string;byTitle:boolean|undefined;anyChannel:boolean;channel:string;newOnly:boolean;keepNewest:number;keepDays:number;priority:number;padBeforeMs:number;padAfterMs:number;paused:boolean};
type State=Readonly<{loading:boolean;saving:boolean;rules:readonly DvrRule[];draft?:RuleDraft;error?:unknown;uncertain?:boolean;day:string;sourceId:string;programmes:readonly RecordingRuleProgramme[];guideLoading:boolean;guideCursor?:string;channels:readonly {id:string;name:string}[];channelCursor?:string;preview?:DvrRule['preview'];previewLoading:boolean;newOnlyAvailable:boolean;defaults?:OwnerDvrSettings}>;
const empty:State={loading:false,saving:false,rules:[],day:'',sourceId:'',programmes:[],guideLoading:false,channels:[],previewLoading:false,newOnlyAvailable:false};
export class RecordingRulesController extends OwnerStore<State>{
 constructor(private api:LibraryContentApi,_serverId:string,private permissions:RecordingPermissionsController,current:()=>boolean,reportError:(error:unknown)=>void){super(empty,current,reportError);}
 async load(preserveError?:unknown){if(!this.isActive()||this.snapshot.loading||this.snapshot.saving||this.snapshot.draft&&!this.snapshot.uncertain)return;
  const attempt=this.begin();this.publish({...this.snapshot,loading:true,error:preserveError});if(!this.valid(attempt))return;
  try{const rules=await readOwnerRecordingRules(this.api,attempt.abort.signal);if(!this.valid(attempt))return;const defaults=await readOwnerDvrSettings(this.api,attempt.abort.signal);if(this.valid(attempt))this.publish({...empty,rules,defaults:defaults.value,error:preserveError});}
  catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,loading:false,error:errorFacts(error)});}
 }
 open(rule?:DvrRule){if(!this.isActive()||this.snapshot.saving||this.snapshot.draft||this.snapshot.uncertain||!this.snapshot.defaults||rule&&!this.snapshot.rules.includes(rule))return;
  this.invalidate();const defaults=this.snapshot.defaults;
  const draft:RuleDraft={base:rule,profileId:rule?.profileId??'',title:rule?.title??'',byTitle:rule?.byTitle,anyChannel:!!rule&&!rule.channel,channel:rule?.channel??'',newOnly:rule?.newOnly??false,keepNewest:rule?.keepNewest??0,keepDays:rule?.keepDays??0,priority:rule?.priority??0,padBeforeMs:rule?.padBeforeMs??defaults.beforeMs,padAfterMs:rule?.padAfterMs??defaults.afterMs,paused:rule?.paused??false};
  this.publish({...this.snapshot,draft,error:undefined,day:new Date().toISOString().slice(0,10),sourceId:rule?.source??'',programmes:[],channels:[],preview:undefined,newOnlyAvailable:!!rule?.newOnly});if(rule)void this.preview(draft);
 }
 close(draft:RuleDraft){if(this.isActive()&&!this.snapshot.saving&&this.snapshot.draft===draft){this.invalidate();this.publish({...this.snapshot,draft:undefined,programmes:[],channels:[],preview:undefined,previewLoading:false,guideLoading:false});}}
 edit(draft:RuleDraft,patch:Partial<RuleDraft>){if(!this.isActive()||this.snapshot.draft!==draft||this.snapshot.saving||this.snapshot.uncertain)return;this.invalidate();this.publish({...this.snapshot,draft:{...draft,...patch,...(patch.profileId!==undefined&&patch.profileId!==draft.profileId?{programme:undefined,title:'',byTitle:undefined}:{})},preview:undefined,previewLoading:false,guideLoading:false,error:undefined});}
 async guide(expected:RuleDraft,day:string,sourceId:string,cursor?:string){if(this.snapshot.draft!==expected||!this.isActive()||this.snapshot.saving||this.snapshot.uncertain||!this.snapshot.draft||this.snapshot.draft.base)return;
  const attempt=this.begin();this.publish({...this.snapshot,day,sourceId,guideLoading:true,error:undefined,preview:undefined,draft:{...this.snapshot.draft,programme:undefined,title:''}});if(!this.valid(attempt))return;
  try{const page=await readRecordingRuleProgrammes(this.api,day,sourceId,cursor,attempt.abort.signal);if(this.valid(attempt))this.publish({...this.snapshot,guideLoading:false,programmes:page.items,guideCursor:page.nextCursor});}
  catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,guideLoading:false,error:errorFacts(error)});}
 }
 choose(draft:RuleDraft,programme:RecordingRuleProgramme){if(this.snapshot.draft!==draft||!this.snapshot.programmes.includes(programme)||this.snapshot.saving||!draft.profileId)return;this.edit(draft,{programme:programme.id,title:programme.title??'',byTitle:undefined,anyChannel:false,newOnly:false});const current=this.snapshot.draft;if(current)void this.preview(current);}
 private body(draft:RuleDraft):DvrRuleSave{
  const body:DvrRuleSave=draft.base?{}:{programme:draft.programme,profileId:draft.profileId};
  const defaults=this.snapshot.defaults!;
  const original=draft.base;
  for(const key of ['newOnly','keepNewest','keepDays','priority','padBeforeMs','padAfterMs','paused'] as const){const value=draft[key],before=original?.[key]??(key==='padBeforeMs'?defaults.beforeMs:key==='padAfterMs'?defaults.afterMs:key==='keepNewest'||key==='keepDays'?0:undefined);if(!original||value!==before)Object.assign(body,{[key]:value});}
  if(!original||draft.anyChannel!==!original.channel||!draft.anyChannel&&draft.channel!==original.channel){if(draft.anyChannel)body.anyChannel=true;else if(original)body.channel=draft.channel;}
  return body;
 }
 async preview(draft:RuleDraft){if(!this.isActive()||this.snapshot.draft!==draft||this.snapshot.saving||this.snapshot.uncertain||!draft.base&&!draft.programme||!draft.profileId)return;
  const attempt=this.begin();this.publish({...this.snapshot,previewLoading:true,error:undefined});if(!this.valid(attempt))return;
  try{const rule=await saveOwnerRecordingRule(this.api,this.body(draft),draft.base?.id,true,attempt.abort.signal);if(this.valid(attempt))this.publish({...this.snapshot,previewLoading:false,preview:rule.preview,newOnlyAvailable:rule.preview?.newOnlyAvailable??false,draft:{...draft,byTitle:rule.byTitle,channel:rule.channel??draft.channel}});}
  catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,previewLoading:false,error:errorFacts(error)});}
 }
 async channels(expected:RuleDraft,cursor?:string){if(this.snapshot.draft!==expected||!this.isActive()||this.snapshot.saving||!this.snapshot.draft?.base)return;const attempt=this.begin(),source=this.snapshot.draft.base.source;
  try{const page=await readRecordingRuleChannels(this.api,source,cursor,attempt.abort.signal);if(this.valid(attempt))this.publish({...this.snapshot,channels:page.items,channelCursor:page.nextCursor});}catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,error:errorFacts(error)});}
 }
 async save(draft:RuleDraft){
  if(!this.isActive()||this.snapshot.draft!==draft||this.snapshot.saving||this.snapshot.previewLoading||this.snapshot.uncertain||!draft.base&&(!draft.programme||draft.byTitle===undefined||!this.permissions.getSnapshot().owners.some(row=>row.profileId===draft.profileId&&row.canRecord)))return false;
  const attempt=this.begin();this.publish({...this.snapshot,saving:true,error:undefined});if(!this.valid(attempt))return false;
  try{const saved=await saveOwnerRecordingRule(this.api,this.body(draft),draft.base?.id,false,attempt.abort.signal);if(!this.valid(attempt))return false;this.publish({...this.snapshot,saving:false,draft:undefined,preview:undefined,rules:[saved,...this.snapshot.rules.filter(row=>row.id!==saved.id)]});return true;}
  catch(error){if(!this.valid(attempt))return false;const facts=errorFacts(error),uncertain=![400,401,403,404,409,412,422].includes(facts.status??0);this.fail(error,{...this.snapshot,saving:false,error:facts,uncertain});if(this.valid(attempt)&&uncertain)await this.load(facts);return false;}
 }
 async toggle(rule:DvrRule){this.open(rule);const draft=this.snapshot.draft;if(!draft)return;this.edit(draft,{paused:!rule.paused});const changed=this.snapshot.draft;if(changed)return this.save(changed);}
 async remove(rule:DvrRule,cancelBookings=false){if(!this.isActive()||this.snapshot.saving||this.snapshot.draft||this.snapshot.uncertain||!this.snapshot.rules.includes(rule))return false;const attempt=this.begin();this.publish({...this.snapshot,saving:true,error:undefined});if(!this.valid(attempt))return false;
  try{await removeOwnerRecordingRule(this.api,rule.id,cancelBookings,attempt.abort.signal);if(!this.valid(attempt))return false;this.publish({...this.snapshot,saving:false,rules:this.snapshot.rules.filter(row=>row.id!==rule.id)});return true;}
  catch(error){if(!this.valid(attempt))return false;const facts=errorFacts(error);this.fail(error,{...this.snapshot,saving:false,error:facts,uncertain:true});if(this.valid(attempt))await this.load(facts);return false;}
 }
}
