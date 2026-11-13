import type {LibraryContentApi} from '@core/library-content.ts';
import {readOwnerDvrSettings, saveOwnerDvrSettings, readRecordingStorage, type OwnerDvrDocument, type OwnerDvrSettings} from '@core/server-admin/owner-dvr-settings.ts';
import type {DvrOverview} from '../../../../../../packages/contracts/src/next/api.ts';
import {OwnerStore, errorFacts} from '../../../live/owner/lifetime';
type Policy = Pick<OwnerDvrSettings, 'floorBytes' | 'capBytes' | 'keepDays' | 'keepNewest'>;
type State = Readonly<{loading:boolean;saving:boolean;storage?:DvrOverview;document?:OwnerDvrDocument;draft?:Policy;error?:unknown;uncertain?:boolean}>;
const policy = (value:OwnerDvrSettings):Policy => ({floorBytes:value.floorBytes,capBytes:value.capBytes,keepDays:value.keepDays,keepNewest:value.keepNewest});
/** Autosave changes only storage fields under the ETag the owner read. */
export class RecordingStorageController extends OwnerStore<State> {
 private saveTimer?:ReturnType<typeof setTimeout>;
 constructor(private api:LibraryContentApi,_serverId:string,current:()=>boolean,reportError:(error:unknown)=>void){super({loading:false,saving:false},current,reportError);}
 async load(preserveError?:unknown){
  if(!this.isActive()||this.snapshot.loading||this.snapshot.saving)return;
  clearTimeout(this.saveTimer);const attempt=this.begin();this.publish({...this.snapshot,loading:true,error:preserveError});if(!this.valid(attempt))return;
  try{const document=await readOwnerDvrSettings(this.api,attempt.abort.signal);if(!this.valid(attempt))return;const storage=await readRecordingStorage(this.api,attempt.abort.signal);if(this.valid(attempt))this.publish({loading:false,saving:false,document,storage,draft:policy(document.value),error:preserveError});}
  catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,loading:false,error:errorFacts(error)});}
 }
 edit(patch:Partial<Policy>){
  if(!this.isActive()||this.snapshot.loading||this.snapshot.saving||this.snapshot.uncertain||!this.snapshot.draft)return;
  const draft={...this.snapshot.draft,...patch};
  if(patch.keepDays&&patch.keepDays>0)draft.keepNewest=0;if(patch.keepNewest&&patch.keepNewest>0)draft.keepDays=0;
  if(Object.values(draft).some(value=>!Number.isSafeInteger(value)||value<0)||JSON.stringify(draft)===JSON.stringify(this.snapshot.draft))return;
  this.publish({...this.snapshot,draft,error:undefined});clearTimeout(this.saveTimer);this.saveTimer=setTimeout(()=>{this.saveTimer=undefined;void this.save();},500);
 }
 discard(){clearTimeout(this.saveTimer);if(this.isActive()&&!this.snapshot.saving&&this.snapshot.document)this.publish({...this.snapshot,draft:policy(this.snapshot.document.value),error:undefined});}
 async save(){
  clearTimeout(this.saveTimer);this.saveTimer=undefined;
  const {document,draft}=this.snapshot;if(!this.isActive()||this.snapshot.saving||this.snapshot.loading||!document||!draft)return;
  if(this.snapshot.uncertain){await this.load();return;}
  const change=Object.fromEntries(Object.entries(draft).filter(([key,value])=>value!==document.value[key as keyof Policy]));if(!Object.keys(change).length)return;
  const attempt=this.begin();this.publish({...this.snapshot,saving:true,error:undefined});if(!this.valid(attempt))return;
  try{const saved=await saveOwnerDvrSettings(this.api,document,change,attempt.abort.signal);if(!this.valid(attempt))return;this.publish({...this.snapshot,saving:false,document:saved,draft:policy(saved.value)});await this.load();}
  catch(error){if(!this.valid(attempt))return;const facts=errorFacts(error),uncertain=![400,401,403,409,412,422].includes(facts.status??0);this.fail(error,{...this.snapshot,saving:false,error:facts,uncertain});if(this.valid(attempt)&&(uncertain||facts.status===412))await this.load(facts);}
 }
 override dispose(){clearTimeout(this.saveTimer);super.dispose();}
}
