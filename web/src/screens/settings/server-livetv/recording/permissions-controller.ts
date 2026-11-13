import {readRecordingPermissions,saveRecordingPermission,type DvrPermission} from '@core/server-admin/owner-recording-permissions.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {RecordingOwnerChoice,RecordingGrant} from '@core/server-admin/recording-policy.ts';
import {OwnerStore,errorFacts} from '../../../live/owner/lifetime';
export type RecordingPermissionRow=RecordingOwnerChoice&DvrPermission;
type State=Readonly<{loading:boolean;saving:boolean;owners:readonly RecordingPermissionRow[];grants:readonly RecordingGrant[];cursor:string;edits:Readonly<Record<string,never>>;error?:unknown;uncertain?:boolean}>;
const empty:State={loading:false,saving:false,owners:[],grants:[],cursor:'',edits:{}};
/** Both switches are stored server permissions; profile choices only narrow their account. */
export class RecordingPermissionsController extends OwnerStore<State>{
 constructor(private api:LibraryContentApi,_serverId:string,current:()=>boolean,reportError:(error:unknown)=>void){super(empty,current,reportError);}
 private loaded(rows:readonly DvrPermission[],error?:unknown):State{
  const owners=rows.map(row=>({...row,owner:{authority:row.authority as 'local'|'hosted',accountId:row.accountId,profileId:row.profileId}}));
  return {loading:false,saving:false,owners,grants:owners.map(row=>({owner:row.owner,enabled:row.canRecord,revision:row.revision})),cursor:'',edits:{},error};
 }
 async load(_more=false,preserveError?:unknown){if(!this.isActive()||this.snapshot.loading||this.snapshot.saving)return;
  const attempt=this.begin();this.publish({...this.snapshot,loading:true,error:preserveError});if(!this.valid(attempt))return;
  try{const rows=await readRecordingPermissions(this.api,attempt.abort.signal);if(this.valid(attempt))this.publish(this.loaded(rows,preserveError));}
  catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,loading:false,error:errorFacts(error)});}
 }
 async change(row:RecordingPermissionRow,enabled:boolean,account=false){
  if(!this.isActive()||this.snapshot.loading||this.snapshot.saving||this.snapshot.uncertain||!this.snapshot.owners.includes(row)||account&&row.ownerAccount)return;
  const attempt=this.begin(),body={accountId:row.accountId,...(account?{}:{profileId:row.profileId}),enabled,revision:account?row.accountRevision:row.revision};
  this.publish({...this.snapshot,saving:true,error:undefined});if(!this.valid(attempt))return;
  try{const rows=await saveRecordingPermission(this.api,body,attempt.abort.signal);if(this.valid(attempt))this.publish(this.loaded(rows));}
  catch(error){if(!this.valid(attempt))return;const facts=errorFacts(error);this.fail(error,{...this.snapshot,saving:false,error:facts,uncertain:true});if(this.valid(attempt))await this.load(false,facts);}
 }
}
