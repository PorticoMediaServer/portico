import type {LibraryContentApi} from '@core/library-content.ts';
import {readOwnerDvrSettings,saveOwnerDvrSettings,type OwnerDvrDocument} from '@core/server-admin/owner-dvr-settings.ts';
import {callServer} from '@core/query/server-api.ts';
import type {DvrFolderStatus} from '../../../../../../packages/contracts/src/next/api.ts';
import {OwnerStore,errorFacts} from '../../../live/owner/lifetime';
type State=Readonly<{loading:boolean;saving:boolean;document?:OwnerDvrDocument;folder?:DvrFolderStatus;draft?:string;error?:unknown;uncertain?:boolean}>;
/** A folder edit is one field of the rebuilt DVR document, using its captured ETag. */
export class RecordingRootController extends OwnerStore<State>{
 constructor(private api:LibraryContentApi,current:()=>boolean,reportError:(error:unknown)=>void){super({loading:false,saving:false},current,reportError);}
 dirty=()=>{const state=this.getSnapshot();return !!state.document&&state.draft!==undefined&&state.draft!==state.document.value.folder;};
 async load(preserveError?:unknown){if(!this.isActive()||this.snapshot.loading||this.snapshot.saving)return;const attempt=this.begin();this.publish({...this.snapshot,loading:true,error:preserveError});if(!this.valid(attempt))return;
  try{const document=await readOwnerDvrSettings(this.api,attempt.abort.signal);if(!this.valid(attempt))return;const folder=await callServer(this.api,'GET /v1/admin/dvr/folder',{signal:attempt.abort.signal});if(this.valid(attempt))this.publish({loading:false,saving:false,document,folder,draft:document.value.folder,error:preserveError});}
  catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,loading:false,error:errorFacts(error)});}
 }
 choose(path:string){if(!this.isActive()||this.snapshot.loading||this.snapshot.saving||this.snapshot.uncertain||!this.snapshot.document||!path||path.length>4096)return;this.publish({...this.snapshot,draft:path,error:undefined});}
 discard(){if(this.isActive()&&!this.snapshot.saving&&this.snapshot.document)this.publish({...this.snapshot,draft:this.snapshot.document.value.folder,error:undefined});}
 async save(){
  if(!this.isActive()||this.snapshot.loading||this.snapshot.saving||!this.snapshot.document||!this.snapshot.draft||!this.dirty())return;
  if(this.snapshot.uncertain){await this.load();return;}
  const attempt=this.begin(),document=this.snapshot.document;this.publish({...this.snapshot,saving:true,error:undefined});if(!this.valid(attempt))return;
  try{const saved=await saveOwnerDvrSettings(this.api,document,{folder:this.snapshot.draft},attempt.abort.signal);if(!this.valid(attempt))return;const folder=await callServer(this.api,'GET /v1/admin/dvr/folder',{signal:attempt.abort.signal});if(this.valid(attempt))this.publish({loading:false,saving:false,document:saved,folder,draft:saved.value.folder});}
  catch(error){if(!this.valid(attempt))return;const facts=errorFacts(error),uncertain=![400,401,403,409,412,422].includes(facts.status??0);this.fail(error,{...this.snapshot,saving:false,error:facts,uncertain});if(this.valid(attempt)&&(uncertain||facts.status===412)){await this.load(facts);}}
 }
}
