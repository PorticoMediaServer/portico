import {previewOwnerSource, createOwnerSource, type OwnerSourcePreview, type SourceSave as LiveSourceSave} from '@core/server-admin/live-sources.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import {channelRequestID} from '@core/live-source-draft.ts';
import {normalizeHdHomerunLocator} from '@core/server-admin/panel-logic.ts';
import {OwnerStore, errorFacts} from './lifetime';
export type SourceKind = 'm3u' | 'xtream' | 'hdhomerun' | 'guide';
export type RemoteSourceDraft = Readonly<{kind: SourceKind; name: string; locator: string; guideUrl: string; username: string; password: string; viewerAccess: 'server-members' | 'owner-only'; refreshMs: number; tunerCount: number}>;
export type RemoteSourceSnapshot = Readonly<{draft: RemoteSourceDraft; busy: boolean; pending?: boolean; preview?: OwnerSourcePreview; error?: unknown}>;
const blank = (): RemoteSourceDraft => ({kind:'m3u',name:'',locator:'',guideUrl:'',username:'',password:'',viewerAccess:'server-members',refreshMs:21_600_000,tunerCount:1});
export class RemoteSourceController extends OwnerStore<RemoteSourceSnapshot> {
 private checked?: LiveSourceSave;
 private intent?: {body: LiveSourceSave; id: string};
 constructor(private api: LibraryContentApi, _serverId: string, isCurrent: () => boolean, reportError: (error: unknown) => void, private makeId = channelRequestID, tuner?: string) {
  super({busy:false,draft:blank()},isCurrent,reportError);
  if (tuner) this.snapshot={busy:false,draft:{...blank(),kind:'hdhomerun',locator:tuner}};
 }
 edit(patch: Partial<RemoteSourceDraft>) {
  if (!this.isActive() || this.intent) return;
  this.invalidate();this.checked=undefined;this.publish({draft:{...this.snapshot.draft,...patch},busy:false});
 }
 private body(): LiveSourceSave {
  const d=this.snapshot.draft;
  const config={refreshHours:d.refreshMs/3_600_000,...(d.kind==='m3u'?{playlistUrl:d.locator}:d.kind==='xtream'?{serverUrl:d.locator}:d.kind==='hdhomerun'?{tunerUrl:normalizeHdHomerunLocator(d.locator)}:{guideUrl:d.locator}),...(d.kind!=='guide'&&d.guideUrl?{guideUrl:d.guideUrl}:{})};
  return {kind:d.kind==='m3u'?'playlist':d.kind,name:d.name.trim(),audience:d.viewerAccess==='owner-only'?'owner':'everyone',tunerCount:d.tunerCount,config,...(d.kind==='xtream'?{signIn:{username:d.username,password:d.password}}:{})};
 }
 async check(_confirm=false) {
  if (!this.isActive() || this.snapshot.busy || this.intent || !this.snapshot.draft.name.trim() || !this.snapshot.draft.locator.trim()) return;
  const attempt=this.begin(),body=this.body();this.publish({...this.snapshot,busy:true,preview:undefined,error:undefined});
  if (!this.valid(attempt)) return;
  try {const preview=await previewOwnerSource(this.api,body,attempt.abort.signal);if(this.valid(attempt)){this.checked=this.snapshot.draft.kind==='hdhomerun' && preview.tuners>0?{...body,tunerCount:preview.tuners}:body;this.publish({...this.snapshot,busy:false,preview});}}
  catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,busy:false,error:errorFacts(error)});}
 }
 async save() {
  if (!this.isActive() || this.snapshot.busy || !this.snapshot.preview || !this.checked) return false;
  this.intent??={body:this.checked,id:this.makeId()};const intent=this.intent,attempt=this.begin();this.publish({...this.snapshot,busy:true,pending:true,error:undefined});
  if(!this.valid(attempt))return false;
  try {await createOwnerSource(this.api,intent.body,intent.id,attempt.abort.signal);if(!this.valid(attempt))return false;this.intent=undefined;this.checked=undefined;this.publish({busy:false,draft:blank()});return true;}
  catch(error){if(this.valid(attempt)){if([400,401,403,409,412,422].includes(errorFacts(error).status??0))this.intent=undefined;this.fail(error,{...this.snapshot,busy:false,pending:!!this.intent,error:errorFacts(error)});}return false;}
 }
 override dispose(){this.intent=undefined;this.checked=undefined;super.dispose();}
}
