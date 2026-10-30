import {sameFence, type PlaybackFence} from '../../../../../packages/player-core/src/index.ts';
import type {PlayerChapter, PlayerChaptersSnapshot} from '@core/player-chapters.ts';
import type {LyricRevision, LyricsSnapshot} from '@core/lyrics.ts';
import type {QueuePlaylistSave} from '@core/playback-v1/queue.ts';
import type {V1QueuePolicyIntent} from '@core/playback-v1/v1-queue-player.ts';
import type {WebPlaybackRuntimeSnapshot, WebShellPlaybackRuntime, WebListeningPort, WebListeningLifetime, WebListeningTarget, WebQueueListSnapshot,
  WebQueueEditOpening, WebQueueEditSnapshot, WebQueueEditPage} from '../../../platform/playback';
import {dispatchWatch, type WatchIntent} from '../model';
import {captureLibrarySettings, type ListeningSettingsInput, type ListeningLibrarySettings} from './library-settings';

export type ListeningPanel = 'queue' | 'lyrics' | 'chapters' | 'speed' | 'sleep' | 'options';
type Opening = {token: object; controller: AbortController};
export type ListeningState = Readonly<{
  current: boolean; dockOpening: object | null; opening: object | null; panelOpening: object | null; panel: ListeningPanel; panelVisible:boolean; focus:Readonly<{current:HTMLElement|null}>|null;
  chapters: PlayerChaptersSnapshot | null; lyrics: LyricsSnapshot | null; queue: WebQueueListSnapshot | null;
  window: WebQueueEditSnapshot | null; anchor?: number; retainedWindow: WebQueueEditPage | null; queueError: boolean;
  settingsAvailable: boolean; policyBusy: boolean; save: Readonly<{token: object; list: WebQueueListSnapshot; name: string; busy: boolean; error: boolean; progress: QueuePlaylistSave | null}> | null;
}>;
export type ListeningOwnerInput = Readonly<{
  runtime: WebShellPlaybackRuntime; snapshot: WebPlaybackRuntimeSnapshot;
  isCurrent(): boolean; lyricsEnabled: boolean; settings?: ListeningSettingsInput;
}>;
function stale(): never {throw new DOMException('Listening opening retired', 'AbortError');}
export const listeningSleepChoices = Object.freeze([0, 15, 30, 45, 60, 90]);

/** Presentation lifetime only. The existing runtime owns every media/session/queue
 * command. Each committed expansion, panel and mutation has its own original
 * receipt, so closing and reopening cannot revive a saved callback. */
export class ListeningOwner {
  private readonly input: ListeningOwnerInput;
  private readonly fence: PlaybackFence;
  private readonly read: WebShellPlaybackRuntime['getSnapshot'];
  private readonly authority: WebPlaybackRuntimeSnapshot['authority'];
  private readonly run: number;
  readonly kind: 'music' | 'audiobook';
  private readonly life = new AbortController();
  private readonly listeners = new Set<() => void>();
  private readonly offs: (() => void)[] = [];
  private dead = false;
  private port?: WebListeningPort;
  private settings?: ListeningLibrarySettings;
  private readonly settingsAdmitted: boolean;
  private settingsTarget?: WebListeningTarget;
  private settingsFlight?:object;
  private dockToken?: object;
  private saveOpening?: Opening & {list: WebQueueListSnapshot};
  private saveFlight?: object;
  private view?: Opening;
  private panel?: Opening;
  private editor?: WebQueueEditOpening;
  private editorOff?: () => void;
  private operation?: object;
  private transportPolicy?: object;
  private transition=0;
  private saveSerial=0;
  private editRequest?:object;
  private initialChapters = false;
  private rates: readonly number[] = Object.freeze([]);
  private minutes = 0;
  private pendingPanel?: ListeningPanel;
  private returnFocus?: HTMLElement;
  private dockPanel = false;
  private state: ListeningState;
  private readonly commands: Pick<WebShellPlaybackRuntime, 'getSnapshot' | 'onAction'>;
  private readonly expand: WebShellPlaybackRuntime['open'];
  private readonly collapse: WebShellPlaybackRuntime['collapse'];
  private readonly offeredSpeeds: WebShellPlaybackRuntime['speedChoices'];
  private readonly writeSpeed: WebShellPlaybackRuntime['setSpeed'];
  private readonly readSleep: WebShellPlaybackRuntime['sleepTimer'];
  private readonly writeSleep: WebShellPlaybackRuntime['setSleepTimer'];

  constructor(input: ListeningOwnerInput) {
    const {snapshot, runtime} = input, kind = snapshot.presentation?.kind;
    if (!snapshot.fence || kind !== 'music' && kind !== 'audiobook') stale();
    this.input = input;this.fence = snapshot.fence;this.kind = kind;this.authority = snapshot.authority;this.run = snapshot.playback.playbackRun;
    this.read = runtime.getSnapshot.bind(runtime);this.expand = runtime.open.bind(runtime);this.collapse = runtime.collapse.bind(runtime);
    this.offeredSpeeds = runtime.speedChoices.bind(runtime);this.writeSpeed = runtime.setSpeed.bind(runtime);
    this.readSleep = runtime.sleepTimer.bind(runtime);this.writeSleep = runtime.setSleepTimer.bind(runtime);
    this.commands = Object.freeze({getSnapshot: this.read, onAction: runtime.onAction.bind(runtime)});
    this.state = Object.freeze({current: true, dockOpening: null, opening: null, panelOpening: null, panel: kind === 'music' ? 'queue' : 'chapters',panelVisible:false,focus:null,
      chapters: null, lyrics: null, queue: null, window: null, retainedWindow: null, queueError: false, settingsAvailable:false, policyBusy: false, save: null});
    const identity = input.settings?.context.facts?.identity;
    this.settingsAdmitted = identity?.viewer.role === 'owner' && identity.primaryProfile === true && identity.capabilities?.manageServer === true;
    const capture = runtime.captureListening.bind(runtime);
    const port = capture(this.fence, {signal: this.life.signal, isCurrent: this.current});
    if (this.dead) {port?.dispose();return;}this.port = port;
    this.settingsTarget=port?Object.freeze({...port.target}):undefined;
    this.settings=this.kind==='music'&&this.settingsTarget?captureLibrarySettings(input.settings,this.authority,this.settingsTarget.libraryId,{signal:this.life.signal,isCurrent:this.current}):undefined;
    this.keep(input.settings?.context.factsSource?.subscribe(this.factsChanged) ?? (() => {}));
    this.keep(runtime.subscribe(this.observe));
    if (port) {
      this.keep(port.chapters.subscribe(this.observe));this.keep(port.lyrics.subscribe(this.observe));
      if (port.queue) this.keep(port.queue.list.subscribe(this.observe));
    }
    this.observe();
  }
  private keep(off: () => void) {if (this.dead) off();else this.offs.push(off);}
  private matches(value: WebPlaybackRuntimeSnapshot): boolean {
    return !value.disposed && value.authority === this.authority && !!value.fence && sameFence(value.fence, this.fence)
      && value.playback.playbackRun === this.run && value.presentation?.kind === this.kind && sameFence(value.presentation.fence, this.fence)
      && !value.dock.remote && value.dock.local?.kind === this.kind && sameFence(value.dock.local.fence, this.fence);
  }
  /** Does not call port.isCurrent: the facade uses this same original lifetime. */
  current = (): boolean => {
    if (this.dead || this.life.signal.aborted) return false;
    let accepted = false;
    try {accepted = this.input.isCurrent() && this.matches(this.read()) && this.input.isCurrent() && this.matches(this.read());} catch { /* Refused original owner. */ }
    if (!accepted || this.dead || this.life.signal.aborted) {this.dispose();return false;}
    return true;
  };
  getSnapshot = (): ListeningState => this.state;
  subscribe = (listener: () => void) => {if (this.dead) return () => {};this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<ListeningState>) {
    if (this.dead) return;
    this.state = Object.freeze({...this.state, ...patch});for (const listener of [...this.listeners]) listener();
  }
  private factsChanged = () => {
    this.settings = undefined;this.settingsFlight = undefined;
    if (!this.current()) return;
    const target = this.settingsTarget;
    this.settings = this.settingsAdmitted && this.kind === 'music' && target ? captureLibrarySettings(this.input.settings, this.authority, target.libraryId, {signal: this.life.signal, isCurrent: this.current}) : undefined;
    this.observe(true);
  };
  private observe = (force = false) => {
    if (!this.current()) return;
    if (this.port && !this.port.isCurrent()) {this.dispose();return;}
    if (!this.current()) return;
    if (!this.read().dock.local?.expanded) this.retireView();
    else if(this.dockToken){this.dockToken=undefined;this.publish({dockOpening:null});}
    let rates:readonly number[]=Object.freeze([]),minutes=0;
    try{rates=this.offeredSpeeds(this.fence);minutes=this.readSleep(this.fence)?.minutes??0;}catch{/* An unavailable source offers no options. */}
    if(!this.current())return;
    const settingsAvailable=this.settings?.isCurrent()===true;
    if(!this.current())return;
    const optionsChanged=rates.join(',')!==this.rates.join(',')||minutes!==this.minutes;this.rates=rates;this.minutes=minutes;
    const chapters = this.port?.chapters.getSnapshot() ?? null, lyrics = this.port?.lyrics.getSnapshot() ?? null;
    const queue = this.port?.queue?.list.getSnapshot() ?? null;
    if (!this.current()) return;
    if (force || settingsAvailable!==this.state.settingsAvailable || optionsChanged || chapters !== this.state.chapters || lyrics !== this.state.lyrics || queue !== this.state.queue) this.publish({chapters, lyrics, queue,settingsAvailable});
  };
  /** Detach every mutable slot before invoking an original external cleanup. */
  private detachEditor() {
    const editor=this.editor,off=this.editorOff;this.editor=undefined;this.editorOff=undefined;
    return ()=>{try{off?.();}finally{editor?.dispose();}};
  }
  private detachSave() {
    ++this.saveSerial;const opening=this.saveOpening,flight=this.saveFlight;
    this.saveOpening=undefined;this.saveFlight=undefined;if(this.operation===flight)this.operation=undefined;
    return ()=>opening?.controller.abort();
  }
  private detachPanel() {
    const panel=this.panel;this.panel=undefined;if(this.operation!==this.transportPolicy)this.operation=undefined;this.editRequest=undefined;this.settingsFlight=undefined;
    const save=this.detachSave(),editor=this.detachEditor();
    return ()=>{try{save();}finally{try{panel?.controller.abort();}finally{editor();}}};
  }
  private retireView() {
    if(!this.view&&!this.panel)return this.transition;
    const serial=++this.transition,view=this.view;this.view=undefined;this.transportPolicy=undefined;
    const cleanup=this.detachPanel();
    try{view?.controller.abort();}finally{cleanup();}
    if(!this.dead&&this.transition===serial&&!this.view)this.publish({opening:null,panelOpening:null,window:null,retainedWindow:null,queueError:false,policyBusy:false,save:null});
    return serial;
  }
  /** Called by the original host's layout commit, never by a runtime publication. */
  commitExpanded(expanded: boolean) {
    if (!this.current() || this.read().dock.local?.expanded !== expanded) return;
    if (!expanded) {
      const serial=this.retireView();
      const own=()=>!this.dead&&this.transition===serial&&!this.view;
      if(!own()||!this.current()||!own()||this.read().dock.local?.expanded||!own())return;
      const anchor=this.returnFocus;this.returnFocus=undefined;this.dockPanel=false;
      const fresh=!this.dockToken;if(fresh)this.dockToken={};const token=this.dockToken;
      if(fresh)this.publish({dockOpening:token!});
      if(anchor?.isConnected&&own()&&this.current()&&own()&&this.dockToken===token&&!anchor.closest('[inert],[aria-hidden="true"]')&&own()&&this.dockToken===token)anchor.focus();
      return;
    }
    this.dockToken=undefined;
    if (this.view) return;
    const view=this.view = {token: {}, controller: new AbortController()};
    const pending=this.pendingPanel,panel=pending??(this.kind==='music'?'queue':'chapters');this.pendingPanel=undefined;
    this.setPanel(panel,!!pending,this.returnFocus);
    if(this.view!==view||!this.current()||this.view!==view)return;
    if (this.kind === 'audiobook' && this.port && !this.initialChapters) {
      this.initialChapters = true;const shown = this.state;
      if (this.admit(shown)) void this.port.chapters.load().catch(() => {});
    }
  }
  private admit(shown: ListeningState, panel = false): boolean {
    const view = this.view, opening = this.panel;
    const local = () => !this.dead && !!view && this.view === view && shown.opening === view.token && !view.controller.signal.aborted
      && (!panel || !!opening && this.panel === opening && shown.panelOpening === opening.token && !opening.controller.signal.aborted);
    if(!local()||!this.current()||!local())return false;
    if(this.port&&!this.port.isCurrent()){this.dispose();return false;}
    return local()&&this.current()&&local()&&this.read().dock.local?.expanded===true&&local();
  }
  private lifetime(shown: ListeningState, panel = false): WebListeningLifetime {
    if (!this.admit(shown, panel)) stale();
    const view = this.view!, opening = this.panel;
    return {signal: panel ? opening!.controller.signal : view.controller.signal, isCurrent: () => this.admit(shown, panel)};
  }
  private setPanel(panel: ListeningPanel,visible=true,anchor?:HTMLElement) {
    const view=this.view,serial=++this.transition,cleanup=this.detachPanel();cleanup();
    const own=()=>!this.dead&&!!view&&this.view===view&&!view.controller.signal.aborted&&this.transition===serial;
    if(!own()||!this.current()||!own())return;
    this.panel={token:{},controller:new AbortController()};
    this.publish({dockOpening:null,opening:view!.token,panelOpening:this.panel.token,panel,panelVisible:visible,focus:anchor?Object.freeze({current:anchor}):null,window:null,anchor:undefined,retainedWindow:null,queueError:false,policyBusy:!!this.transportPolicy,save:null});
    if(!own())return;const shown=this.state;if(!this.admit(shown,true)||!own())return;
    if(panel==='queue')void this.reloadQueue(shown).catch(()=>{});
    else if(panel==='lyrics'&&this.lyricsOffered())void this.port!.lyrics.refresh().catch(()=>{});
  }
  show(shown: ListeningState, panel: ListeningPanel,anchor?:HTMLElement) {
    if (!this.admit(shown) || panel === 'lyrics' && !this.lyricsOffered()) return;
    this.setPanel(panel,true,anchor);
  }
  closePanel(shown:ListeningState){if(!this.admit(shown,true))return;if(this.dockPanel){this.close(shown);return;}this.setPanel(this.kind==='music'?'queue':'chapters',false);}
  cover(shown:ListeningState) {
    if(!this.admit(shown)||!this.panel)return;
    const view=this.view,serial=++this.transition,cleanup=this.detachPanel();cleanup();
    if(this.transition!==serial||this.view!==view||!this.current()||this.transition!==serial||this.view!==view)return;
    this.publish({panelOpening:null,save:null,window:null});
  }
  canReport(shown:ListeningState,panel=false){return this.admit(shown,panel);}
  canReportDock(shown:ListeningState){const token=this.dockToken;return !!token&&shown.dockOpening===token&&!this.view&&this.current()&&this.dockToken===token&&!this.view&&!this.read().dock.local?.expanded&&this.dockToken===token;}
  lyricsOffered() {return this.state.current && this.input.lyricsEnabled && this.kind === 'music' && !!this.port;}
  chaptersOffered(){return this.state.current&&this.kind==='audiobook'&&!!this.port;}
  external(shown:ListeningState,snapshot:WebPlaybackRuntimeSnapshot,action:()=>void){if(!this.admit(shown)||!this.matches(snapshot)||!this.admit(shown))return;action();}
  queueOffered() {return this.state.current && !!this.port?.queue;}
  /** A dock gesture is consumed before expand; only its subsequent layout commit opens. */
  requestDock(shown: ListeningState, panel: ListeningPanel, anchor?: HTMLElement): boolean {
    const token=this.dockToken,serial=this.transition,view=this.view;
    if (!token||shown.dockOpening!==token||this.read().dock.local?.expanded||!this.current()||this.dockToken!==token || this.pendingPanel || panel === 'lyrics' && !this.lyricsOffered() || panel === 'queue' && !this.queueOffered()||panel==='chapters'&&!this.chaptersOffered()||panel==='speed'&&!this.speeds().length||panel==='sleep'&&!this.sleepOffered()) return false;
    this.dockToken=undefined;this.pendingPanel=panel;this.dockPanel=true;this.returnFocus=anchor;
    const own=()=>!this.dead&&this.transition===serial&&this.view===view&&!this.dockToken&&this.pendingPanel===panel;
    this.publish({dockOpening:null});
    if(!own()||!this.current()||!own())return false;
    // A subscriber may have expanded the same source. Only its subsequent layout
    // commit consumes the pending intent; this old tail never replaces that view.
    if(this.read().dock.local?.expanded)return own();
    if(!own())return false;
    try{this.expand(this.fence);}catch(error){
      if(own()&&this.current()&&own()&&!this.read().dock.local?.expanded&&own()){
        this.pendingPanel=undefined;this.returnFocus=undefined;this.dockPanel=false;this.dockToken=token;this.publish({dockOpening:token});
      }
      throw error;
    }
    return true;
  }
  close(shown: ListeningState) {
    if (!this.admit(shown)) return;
    this.pendingPanel=undefined;const serial=this.retireView();
    if(this.transition===serial&&!this.view&&this.current()&&this.transition===serial&&!this.view)this.collapse(this.fence);
  }
  async dispatch(shown: ListeningState, snapshot: WebPlaybackRuntimeSnapshot, intent: WatchIntent) {
    if (!this.admit(shown) || !this.matches(snapshot)) stale();
    await dispatchWatch(this.commands, snapshot, intent);
  }
  speeds() {return this.state.current ? this.rates : [];}
  setSpeed(shown: ListeningState, snapshot: WebPlaybackRuntimeSnapshot, rate: number, panel=false) {
    if (!this.admit(shown,panel) || !this.matches(snapshot) || !this.speeds().includes(rate) || !this.admit(shown,panel)) stale();this.writeSpeed(this.fence, rate);
  }
  sleepMinutes() {return this.minutes;}
  sleepOffered() {return this.state.current && this.fence.kind === 'queue' && !!this.port;}
  setSleep(shown: ListeningState, snapshot: WebPlaybackRuntimeSnapshot, minutes: number, panel=false) {
    if (!this.admit(shown,panel) || !this.matches(snapshot) || !this.sleepOffered() || !listeningSleepChoices.includes(minutes) || !this.admit(shown,panel)) stale();this.writeSleep(this.fence, minutes);
  }
  async chapter(shown: ListeningState, snapshot: PlayerChaptersSnapshot, row: PlayerChapter, panel=false) {
    const lifetime = this.lifetime(shown,panel);if (!this.port) stale();await this.port.chapters.seek(snapshot, row, lifetime);
  }
  async chapterPage(shown: ListeningState, snapshot: PlayerChaptersSnapshot, direction: 'next' | 'previous' | 'retry', panel=false) {
    if (!this.admit(shown,panel) || !this.port || snapshot !== this.port.chapters.getSnapshot() || !this.admit(shown,panel)) stale();
    if (direction === 'retry') {if(snapshot.phase==='refresh-required')await this.port.chapters.refresh();else await this.port.chapters.retry();}else if (direction === 'next') await this.port.chapters.next();else await this.port.chapters.previous();
  }
  async lyricsRetry(shown: ListeningState) {if (!this.admit(shown, true) || !this.lyricsOffered() || !this.admit(shown, true)) stale();await this.port!.lyrics.refresh();}
  private emptyLyrics(lyrics:LyricsSnapshot):boolean {
    const target=this.settingsTarget,data=lyrics.data;
    return this.lyricsOffered()&&!!target&&lyrics.generation>0&&!lyrics.busy&&!lyrics.error&&!data?.selection.resource?.document
      && (!data || data.scope.serverId===this.authority.serverId&&data.scope.libraryId===target.libraryId&&data.scope.itemId===target.itemId
        &&data.scope.sessionId===target.sessionId&&data.scope.sessionGeneration===target.sessionGeneration&&(!data.source||data.source.id===target.sourceId));
  }
  librarySettingsOffered(shown:ListeningState,lyrics:LyricsSnapshot|null){
    return this.state.current&&this.state.settingsAvailable&&shown===this.state&&shown.panel==='lyrics'&&!!this.view&&shown.opening===this.view.token
      &&!!this.panel&&shown.panelOpening===this.panel.token&&!!lyrics&&this.emptyLyrics(lyrics);
  }
  async openLibrarySettings(shown:ListeningState, lyrics:LyricsSnapshot, reportError:(error:unknown)=>void){
    if (shown !== this.state) return;
    const lifetime=this.lifetime(shown,true),view=this.view,panel=this.panel,settings=this.settings,port=this.port,target=this.settingsTarget;
    const active=()=>!this.dead&&this.state.lyrics===lyrics&&this.emptyLyrics(lyrics)&&this.port===port&&this.settings===settings&&this.settingsTarget===target&&this.view===view&&this.panel===panel&&!!view&&!!panel&&!view.controller.signal.aborted&&!panel.controller.signal.aborted;
    const empty=()=>shown.panel==='lyrics'&&this.port===port&&!!port&&this.settings===settings&&!!settings&&settings.libraryId===this.settingsTarget?.libraryId
      && lyrics===this.state.lyrics&&lyrics===port.lyrics.getSnapshot()&&this.emptyLyrics(lyrics);
    const current=()=>active()&&this.admit(shown,true)&&active()&&empty()&&active()&&this.admit(shown,true)&&active()&&empty()&&active();
    if(!settings||!current()||this.settingsFlight)return;
    const token={};this.settingsFlight=token;
    try{await settings.open({signal:lifetime.signal,isCurrent:current,active});}
    catch(error){
      if(this.settingsFlight===token&&current()&&settings.isCurrent()&&active()&&current()&&this.settingsFlight===token)reportError(error);
    }finally{if(this.settingsFlight===token)this.settingsFlight=undefined;}
  }
  async chooseLyric(shown: ListeningState, snapshot: LyricsSnapshot, resource: LyricRevision | null) {const lifetime=this.lifetime(shown,true);if(!this.lyricsOffered())stale();await this.port!.lyrics.choose(snapshot,resource,lifetime);}
  async lyric(shown: ListeningState, snapshot: LyricsSnapshot, index: number) {const lifetime = this.lifetime(shown, true);if (!this.lyricsOffered()) stale();await this.port!.lyrics.seek(snapshot, index, lifetime);}
  private editorChanged = (editor: WebQueueEditOpening) => {
    if (!this.current() || this.editor !== editor) return;
    const window = editor.getSnapshot();if (!this.current() || this.editor !== editor) return;
    const anchor=this.state.anchor??window.page?.rows.find(row=>row.current&&!row.hidden)?.position;
    this.publish({window,anchor,...(window.page ? {retainedWindow: window.page} : {})});
  };
  async reloadQueue(shown: ListeningState) {
    const lifetime = this.lifetime(shown, true), queue = this.port?.queue;if (!queue) return;
    const token={};this.editRequest=token;const cleanup=this.detachEditor();cleanup();
    const owns=()=>this.editRequest===token&&this.admit(shown,true)&&this.editRequest===token;
    let editor:WebQueueEditOpening|undefined;
    try{
      if(!owns())return;const list=queue.list.getSnapshot();
      if(!list.current||!owns())return;
      editor=queue.edit(list,lifetime);const captured=editor;
      if(!owns()){captured.dispose();return;}
      this.editor=captured;const off=captured.subscribe(()=>this.editorChanged(captured));
      if(!owns()||this.editor!==captured){try{off();}finally{captured.dispose();}return;}
      this.editorOff=off;this.publish({window:captured.getSnapshot(),anchor:undefined,queueError:false});
      if(!owns()||this.editor!==captured){captured.dispose();return;}
      await captured.open();
    }catch(error){if(owns()&&(!editor||this.editor===editor))this.publish({queueError:true});throw error;}
  }

  async queueEdit(shown: ListeningState, page: WebQueueEditPage, entryId: string, action: 'remove' | -1 | 1 | 'next' | 'previous') {
    const editor = this.editor;
    if (!this.admit(shown, true) || !editor || editor.getSnapshot().page !== page || !this.admit(shown, true) || this.editor !== editor) stale();
    try {
      if (action === 'next') await editor.next();else if (action === 'previous') await editor.previous();
      else if (action === 'remove') await editor.remove(page, entryId);else await editor.move(page, entryId, action);
    } catch (error) {if (this.admit(shown, true) && this.editor === editor) this.publish({queueError: true});throw error;}
  }
  async policy(shown: ListeningState, list: WebQueueListSnapshot, intent: V1QueuePolicyIntent, panel = true) {
    const lifetime = this.lifetime(shown, panel), queue = this.port?.queue;if (!queue || this.operation) stale();
    const token = {};this.operation = token;if(!panel)this.transportPolicy=token;this.publish({policyBusy: true});
    try {
      if (!lifetime.isCurrent() || this.operation !== token) stale();await queue.policy(list, intent, lifetime);
      if (!lifetime.isCurrent() || this.operation !== token) stale();
      if(this.state.panel==='queue'&&this.admit(this.state,true))await this.reloadQueue(this.state);
    } catch(error){
      if(this.operation===token&&lifetime.isCurrent()&&this.operation===token)this.publish({queueError:true});throw error;
    } finally {
      // Keep the slot reserved through external predicates; a retired predecessor
      // cannot publish idle over a successor started from that predicate.
      if(this.operation===token&&lifetime.isCurrent()&&this.operation===token){this.operation=undefined;if(this.transportPolicy===token)this.transportPolicy=undefined;this.publish({policyBusy:false});}
    }
  }
  openSave(shown: ListeningState, list: WebQueueListSnapshot) {
    if (!this.admit(shown, true) || !this.port?.queue || list !== this.port.queue.list.getSnapshot() || !this.admit(shown, true)) return;
    const cleanup=this.detachSave(),serial=this.saveSerial;cleanup();
    if(this.saveSerial!==serial||!this.admit(shown,true)||this.saveSerial!==serial)return;
    const opening={token:{},controller:new AbortController(),list};this.saveOpening=opening;
    this.publish({save:Object.freeze({token:opening.token,list,name:'',busy:false,error:false,progress:null})});
  }
  saveName(shown: ListeningState, name: string) {if (this.admit(shown, true) && shown.save && this.state.save === shown.save && !shown.save.progress&&!shown.save.busy) this.publish({save: Object.freeze({...shown.save, name})});}
  closeSave(shown: ListeningState) {if (this.admit(shown, true) && shown.save?.token === this.state.save?.token) {const cleanup=this.detachSave(),serial=this.saveSerial;cleanup();if(this.saveSerial===serial&&this.admit(shown,true)&&this.saveSerial===serial)this.publish({save:null});}}
  async save(shown: ListeningState, list: WebQueueListSnapshot, name: string) {
    const panelLife = this.lifetime(shown, true), save = shown.save, queue = this.port?.queue, opening=this.saveOpening;
    if (!queue || !save || !opening || opening.token!==save.token || opening.list!==list || this.state.save !== save || save.progress || save.busy || save.error || this.operation || name.trim()!==save.name.trim()) stale();
    const current=()=>this.saveOpening===opening&&!opening.controller.signal.aborted&&panelLife.isCurrent()&&this.saveOpening===opening&&!opening.controller.signal.aborted;
    const lifetime={signal:AbortSignal.any([panelLife.signal,opening.controller.signal]),isCurrent:current};
    const token = {};this.operation = token;this.saveFlight=token;this.publish({save:Object.freeze({...save,busy:true,error:false})});
    try {
      if(!current()||this.operation!==token)stale();
      const receipt = await queue.save(list, name.trim(), lifetime, progress => {
        if (current() && this.operation === token && this.state.save?.token === save.token) this.publish({save: Object.freeze({...save,busy:true,error:false,progress})});
      });
      if (current() && this.operation === token && this.state.save?.token === save.token) this.publish({save: Object.freeze({...save,busy:false,error:false,progress:receipt})});
    } catch(error){
      if(current()&&this.operation===token&&this.state.save?.token===save.token)this.publish({save:Object.freeze({...save,busy:false,error:true})});throw error;
    } finally {if (this.operation === token) this.operation = undefined;if(this.saveFlight===token)this.saveFlight=undefined;}
  }

  dispose = () => {
    if (this.dead) return;this.dead = true;this.dockToken=undefined;this.pendingPanel = undefined;this.returnFocus = undefined;this.dockPanel=false;
    const view=this.view;this.view=undefined;const cleanup=this.detachPanel();try{view?.controller.abort();}finally{cleanup();this.life.abort();}
    for (const off of this.offs.splice(0)) {try {off();} catch { /* Retire all original subscriptions. */ }}
    this.port?.dispose();this.state = Object.freeze({...this.state, current: false, dockOpening:null, opening: null, panelOpening: null});
    for (const listener of [...this.listeners]) listener();this.listeners.clear();
  };
}
