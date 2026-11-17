import type {GroupCreate, GroupEdit, GroupMe, GroupQueueEntry} from '../../contracts/src/next/api.ts';
import type {HttpLocalApi} from './http.ts';
import {ApiError} from './http.ts';
import {serverPath} from './query/server-api.ts';
import {linkAbortSignals, throwIfAborted} from './abort-lifetime.ts';
import {randomId} from './random-id.ts';
import {repeat, retryAfterFrom, type Repeating} from './timer.ts';
import type {EventsClient} from './playback-v1/events.ts';
import {presentError} from './presentation/errors.ts';
import type {I18n, MessageId} from '../../i18n/src/index.ts';
import {serviceI18n} from './presentation/service-text.ts';
import {parseRoom, parseGroupSummaryPage, roomEntry, roomPositionMs, type Room, type GroupSummaryPage, type GroupAction, type GroupReportState} from './room-protocol.ts';
export type {Room, GroupAction, GroupReportState} from './room-protocol.ts';

export type RoomEnterLifetime = Readonly<{signal: AbortSignal; isCurrent(): boolean}>;
export type RoomApi = Pick<HttpLocalApi, 'requestRaw'>;
export type RoomPhase = 'idle' | 'joining' | 'live' | 'reconnecting' | 'ended' | 'left';
export type RoomSessionSnapshot = Readonly<{
  directory: GroupSummaryPage['items']; directoryPhase: 'idle' | 'loading' | 'ready' | 'error'; directoryError?: string; directoryErrorId?: MessageId;
  phase: RoomPhase; group?: Room; busy: boolean; error?: string; errorId?: MessageId;
  joinFailure?: Readonly<{code: 'code_invalid'; inviteCode: string}>; clockOffsetMs: number;
}>;
type Options = Readonly<{api: RoomApi; events?: Pick<EventsClient, 'on' | 'onResync' | 'subscribeStatus' | 'getStatus'>; key?: () => string; now?: () => number; i18n?: I18n}>;
type ActiveRoom = {id: string; life: AbortController; timer?: ReturnType<typeof setTimeout>; reading?: Promise<void>; dirty: boolean; report?: Promise<void>; flight?: Promise<unknown>; retryAt: number};
const status = (e: unknown) => (e as {status?: number})?.status;
const code = (e: unknown) => (e as {code?: string})?.code;
const groupPath = (id: string) => serverPath('GET /v1/groups/{id}', {id});

/** One viewer's current Room. The app owns the authenticated change feed; this
 * service re-reads on group changes/reconnect and reports presence on the server's
 * cadence. No private stream, revision fences, or replayed playback commands. */
export class RoomSessionService {
  private state: RoomSessionSnapshot = Object.freeze({directory: [], directoryPhase: 'idle', phase: 'idle', busy: false, clockOffsetMs: 0});
  private listeners = new Set<() => void>(); private disposed = false; private entering?: AbortController; private room?: ActiveRoom;
  private lifetime = new AbortController(); private directoryRead?: AbortController; private releases: (() => void)[] = [];
  private createIntent?: {body: string; key: string};
  private clockSamples: {at: number; offset: number; rtt: number}[] = [];
  private reportValue: GroupMe = {state: 'ready', positionMs: 0}; private reportAt = 0;
  private ownI18n?: I18n; private now: () => number; private key: () => string;
  private options: Options;
  constructor(options: Options) {
    this.options = options;
    this.now = options.now ?? Date.now; this.key = options.key ?? randomId; this.ownI18n = options.i18n;
    const events = options.events;
    if (events) {
      this.releases.push(events.on('group.changed', event => {void this.refreshDirectory(); if (!event.resource?.id || event.resource.id === this.room?.id) void this.refresh();}));
      this.releases.push(events.onResync(() => {void this.refreshDirectory(); void this.refresh();}));
      this.releases.push(events.subscribeStatus(() => {if (events.getStatus().connected) {void this.refreshDirectory(); void this.refresh();}}));
    }
  }
  private get i18n() {return this.ownI18n ?? serviceI18n();}
  setI18n(i18n: I18n) {this.ownI18n = i18n;}
  getSnapshot = () => this.state;
  subscribe = (fn: () => void) => {this.listeners.add(fn); return () => {this.listeners.delete(fn);};};
  private publish(patch: Partial<RoomSessionSnapshot>) {if (this.disposed) return; this.state = Object.freeze({...this.state, ...patch}); for (const listener of this.listeners) listener();}
  private problem(error: unknown, fallback: MessageId) {
    const presented = presentError(error, 'together', {operation: 'action', i18n: this.i18n});
    return presented.category === 'unknown' ? {error: this.i18n.t(fallback), errorId: fallback} : {error: presented.body, errorId: presented.messageId as MessageId};
  }
  serverNow() {return this.now() + this.state.clockOffsetMs;}
  private async request(path: string, method: string, body?: unknown, signal = this.lifetime.signal, headers?: Record<string, string>) {
    const answer = await this.options.api.requestRaw(path, method, {body, signal, headers, singleAttempt: true});
    throwIfAborted(signal);
    if (answer.status < 200 || answer.status >= 300) {
      const error = (answer.body as {error?: {code?: string; message?: string; retryable?: boolean}})?.error;
      const retryMs = retryAfterFrom({get: name => answer.headers[name.toLowerCase()] ?? null}, this.now());
      const seconds = retryMs === undefined ? undefined : retryMs / 1000;
      throw new ApiError(answer.status, error?.code ?? 'unknown', error?.message ?? '', error?.retryable, seconds);
    }
    return answer.body;
  }
  async refreshDirectory() {
    if (this.disposed) return;
    this.directoryRead?.abort(); const read = this.directoryRead = new AbortController();
    const link = linkAbortSignals([read.signal, this.lifetime.signal]), signal = link.signal;
    if (this.state.directoryPhase === 'idle') this.publish({directoryPhase: 'loading'});
    try {
      const items: GroupSummaryPage['items'][number][] = []; let cursor: string | undefined; const seen = new Set<string>();
      do {
        const page = parseGroupSummaryPage(await this.request(serverPath('GET /v1/groups', {query: {cursor}}), 'GET', undefined, signal));
        items.push(...page.items); cursor = page.nextCursor;
        if (cursor && seen.has(cursor)) throw new Error('Repeated group cursor');
        if (cursor) seen.add(cursor);
      } while (cursor);
      if (this.directoryRead === read) this.publish({directory: Object.freeze(items), directoryPhase: 'ready', directoryError: undefined, directoryErrorId: undefined});
    } catch (error) {
      if (!signal.aborted && this.directoryRead === read) {const problem = this.problem(error, 'together.error.load'); this.publish({directoryPhase: this.state.directory.length ? 'ready' : 'error', directoryError: problem.error, directoryErrorId: problem.errorId});}
    } finally {link.dispose();}
  }
  async create(input: GroupCreate, lifetime?: RoomEnterLifetime) {
    const body = {name: input.name.trim(), displayName: input.displayName?.trim(), control: input.control};
    const fingerprint = JSON.stringify(body);
    if (this.createIntent?.body !== fingerprint) this.createIntent = {body: fingerprint, key: this.key()};
    const headers = {'Idempotency-Key': this.createIntent.key};
    const result = await this.enter(signal => this.request(serverPath('POST /v1/groups'), 'POST', body, signal, headers), 'together.error.create', lifetime);
    if (result) this.createIntent = undefined;
    return result;
  }
  async join(inviteCode: string, displayName: string, lifetime?: RoomEnterLifetime) {
    const tidy = inviteCode.replace(/[\s-]/g, '').toUpperCase();
    return this.enter(signal => this.request(serverPath('POST /v1/groups/join'), 'POST', {code: tidy, displayName: displayName.trim()}, signal), 'together.error.join', lifetime, tidy);
  }
  async open(id: string, lifetime?: RoomEnterLifetime) {return this.enter(signal => this.request(groupPath(id), 'GET', undefined, signal), 'together.error.open', lifetime);}
  private async enter(read: (signal: AbortSignal) => Promise<unknown>, fallback: MessageId, lifetime?: RoomEnterLifetime, inviteCode?: string) {
    if (this.disposed || lifetime?.signal.aborted || lifetime?.isCurrent() === false) return false;
    this.close('joining'); const turn = this.entering = new AbortController();
    const link = linkAbortSignals([turn.signal, this.lifetime.signal, lifetime?.signal]), signal = link.signal;
    const current = () => this.entering === turn && !signal.aborted && lifetime?.isCurrent() !== false;
    const cancel = () => {if (this.entering === turn) this.close('idle');};
    signal.addEventListener('abort', cancel, {once: true});
    this.publish({busy: true, error: undefined, errorId: undefined});
    try {
      const sent = this.now(), group = parseRoom(await read(signal)); if (!current()) return false;
      this.publish({...this.sample(group, sent), group, phase: group.ended ? 'ended' : 'live'});
      if (!group.ended) {
        const room: ActiveRoom = {id: group.id, life: new AbortController(), dirty: false, retryAt: 0}; this.room = room;
        this.reportValue = {state: group.clock.entryId ? 'buffering' : 'ready', positionMs: 0}; this.reportAt = 0;
        // Opening an existing membership deliberately moves it to this device.
        await this.report(this.reportValue.state as GroupReportState, 0, true);
        if (this.room === room) this.schedule(room);
      }
      return current();
    } catch (error) {
      if (current()) this.publish({phase: 'idle', ...this.problem(error, fallback), joinFailure: inviteCode !== undefined && code(error) === 'code_invalid' ? {code: 'code_invalid', inviteCode} : undefined});
      return false;
    } finally {signal.removeEventListener('abort', cancel); link.dispose(); if (this.entering === turn) {const valid = current(); this.entering = undefined; this.publish({busy: false, ...(!valid && !this.room ? {phase: 'idle' as const} : {})});}}
  }
  private sample(group: Room, sent: number) {
    const received = this.now();
    this.clockSamples = this.clockSamples.filter(x => received - x.at < 120000).slice(-7);
    this.clockSamples.push({at: received, offset: Date.parse(group.clock.at) - (sent + received) / 2, rtt: received - sent});
    const best = this.clockSamples.reduce((a, b) => a.rtt <= b.rtt ? a : b);
    return {clockOffsetMs: best.offset};
  }
  private adopt(group: Room, room: ActiveRoom, sent: number) {
    if (this.room !== room || room.life.signal.aborted) return;
    this.publish({...this.sample(group, sent), group, phase: group.ended ? 'ended' : 'live'});
    if (group.ended) this.close('ended');
  }
  private readRoom(room: ActiveRoom, method = 'GET', suffix = '', body?: unknown): Promise<Room> {
    // No server revision exists. Keep this viewer's requests in order so a slow
    // presence/read answer cannot put the clock back after their Pause or Seek.
    const flight = (room.flight ?? Promise.resolve()).catch(() => {}).then(async () => {
      throwIfAborted(room.life.signal); const sent = this.now();
      const group = parseRoom(await this.request(groupPath(room.id) + suffix, method, body, room.life.signal));
      this.adopt(group, room, sent); return group;
    });
    room.flight = flight; return flight;
  }
  private failed(error: unknown, room: ActiveRoom, visible = false) {
    if (this.room !== room || room.life.signal.aborted) return;
    const retry = (error as {retryAfterSeconds?: number})?.retryAfterSeconds;
    room.retryAt = this.now() + Math.max(1000, Number.isFinite(retry) ? retry! * 1000 : 0);
    if (status(error) === 401 || status(error) === 403 || code(error) === 'in_group_elsewhere') {this.close('idle'); this.publish({directory: [], ...this.problem(error, 'together.error.open')});}
    else if (status(error) === 404 || status(error) === 410) {this.close('ended'); this.publish(this.problem(error, 'together.error.open'));}
    else this.publish({phase: 'reconnecting', ...(visible ? this.problem(error, 'together.error.notReached') : {})});
  }
  refresh(): Promise<void> {
    const room = this.room; if (!room || this.now() < room.retryAt) return Promise.resolve();
    if (room.reading) {room.dirty = true; return room.reading;}
    room.reading = (async () => {
      do {room.dirty = false; try {await this.readRoom(room);} catch (error) {this.failed(error, room); break;}}
      while (room.dirty && this.room === room);
    })().finally(() => {room.reading = undefined;});
    return room.reading;
  }
  private schedule(room: ActiveRoom) {
    clearTimeout(room.timer);
    room.timer = setTimeout(() => {if (this.room !== room) return; void this.report(this.reportValue.state as GroupReportState, this.reportValue.positionMs); void this.refresh(); this.schedule(room);}, Math.max(250, this.state.group?.reportEveryMs ?? 10000));
  }
  /** The follower supplies seconds converted to milliseconds; the service owns cadence and presence. */
  async report(state: GroupReportState, positionMs: number, here = false): Promise<void> {
    const room = this.room, group = this.state.group; if (!room || !group || !Number.isFinite(positionMs)) return;
    this.reportValue = {state, positionMs: Math.max(0, Math.round(positionMs)), ...(group.clock.entryId ? {entryId: group.clock.entryId} : {}), ...(here ? {here: true} : {})};
    if (room.report || this.now() < room.retryAt || !here && this.now() - this.reportAt < group.reportEveryMs) return;
    const body = this.reportValue; this.reportAt = this.now();
    room.report = this.readRoom(room, 'PUT', '/me', body).then(() => {}, error => {this.failed(error, room);}).finally(() => {room.report = undefined;});
    await room.report;
  }
  private async act(method: string, suffix: string, body: unknown, fallback: MessageId): Promise<Room | undefined> {
    const room = this.room; if (!room || this.state.busy) return;
    this.publish({busy: true, error: undefined, errorId: undefined});
    try {const group = await this.readRoom(room, method, suffix, body); return this.room === room || group.ended ? group : undefined;}
    catch (error) {if (this.room === room && !room.life.signal.aborted) this.publish(this.problem(error, fallback)); return;}
    finally {if (this.room === room) this.publish({busy: false});}
  }
  async transport(action: GroupAction, input: {positionMs?: number; entryId?: string} = {}) {
    if (!this.state.group?.you.mayControl) return false;
    return !!await this.act('POST', '/playback', {action, ...input}, 'together.error.notReached');
  }
  async edit(input: GroupEdit) {return !!await this.act('PATCH', '', input, 'together.error.setting');}
  async invite(input: {joins?: number} = {}) {return this.edit({newCode: input});}
  async makeHost(memberId: string) {return this.edit(memberId === this.state.group?.you.memberId ? {becomeHost: true} : {host: memberId});}
  private async replaceQueue(entries: readonly GroupQueueEntry[]) {return this.act('PUT', '/queue', {entries}, 'together.error.queueAdd');}
  async queueAdd(itemIds: readonly string[], replace = false) {
    const group = this.state.group; if (!group?.you.mayControl) return false;
    return !!await this.replaceQueue([...(replace ? [] : group.queue.map(entry => ({entryId: entry.entryId}))), ...itemIds.map(itemId => ({itemId}))]);
  }
  async queueRemove(entryId: string) {
    const group = this.state.group; if (!group?.you.mayControl || !group.queue.some(e => e.entryId === entryId)) return false;
    return !!await this.replaceQueue(group.queue.filter(entry => entry.entryId !== entryId).map(entry => ({entryId: entry.entryId})));
  }
  async queueMove(entryId: string, destinationEntryId: string, placement: 'before' | 'after') {
    const group = this.state.group; if (!group?.you.mayControl || entryId === destinationEntryId) return false;
    const entries = group.queue.map(entry => ({entryId: entry.entryId})), index = entries.findIndex(entry => entry.entryId === entryId);
    if (index < 0 || !entries.some(entry => entry.entryId === destinationEntryId)) return false;
    const [entry] = entries.splice(index, 1); const destination = entries.findIndex(entry => entry.entryId === destinationEntryId);
    entries.splice(destination + (placement === 'after' ? 1 : 0), 0, entry!); return !!await this.replaceQueue(entries);
  }
  async watch(itemId: string) {
    const room = this.room, previous = new Set(this.state.group?.queue.map(entry => entry.entryId));
    if (!await this.queueAdd([itemId]) || this.room !== room) return false;
    const entry = this.state.group?.queue.find(entry => !previous.has(entry.entryId) && entry.itemId === itemId);
    return !!entry && this.transport('watch', {entryId: entry.entryId});
  }
  async leave() {
    const room = this.room; if (!room) return;
    this.publish({busy: true, error: undefined});
    try {await this.request(groupPath(room.id) + '/me', 'DELETE', undefined, room.life.signal); if (this.room === room) {this.close('left'); void this.refreshDirectory();}}
    catch (error) {if (this.room === room) this.publish({busy: false, ...this.problem(error, 'together.error.notReached')});}
  }
  async end() {
    const room = this.room; if (!room || !this.state.group?.you.host || this.state.busy) return false;
    this.publish({busy: true, error: undefined});
    try {
      await this.request(groupPath(room.id), 'DELETE', undefined, room.life.signal);
      if (this.room !== room) return false;
      const group = this.state.group!; this.publish({group: Object.freeze({...group, ended: {reason: 'ended_by_host', at: new Date(this.serverNow()).toISOString()}})});
      this.close('ended'); void this.refreshDirectory(); return true;
    } catch (error) {if (this.room === room) this.publish({busy: false, ...this.problem(error, 'together.error.end')}); return false;}
  }
  notice(message: string) {this.publish({error: message});}
  clearJoinFailure() {this.entering?.abort(); this.publish({joinFailure: undefined, error: undefined, errorId: undefined});}
  dismiss() {this.close('idle'); this.publish({error: undefined, errorId: undefined});}
  private close(phase: RoomPhase) {
    const entering = this.entering; this.entering = undefined; entering?.abort(); const room = this.room; this.room = undefined;
    if (room) {room.life.abort(); clearTimeout(room.timer);}
    this.publish({phase, busy: false, joinFailure: undefined, ...(['idle', 'joining', 'left'].includes(phase) ? {group: undefined} : {})});
  }
  dispose() {this.close('idle'); this.lifetime.abort(); this.directoryRead?.abort(); for (const release of this.releases) release(); this.disposed = true; this.listeners.clear();}
}

/** What a follower needs from whichever player the app has. Positions are seconds. */
export type RoomPlayer=Readonly<{
 /** Optional actual room-occurrence adoption. When supplied, a same-title source from a
  * different room/entry must be admitted afresh instead of inheriting its controls. */
 occurrence?(): Readonly<{groupId:string;entryId:string}> | null;
 /** The item loaded now, its position, and whether it is actually moving and able to.
  * `seeking` is true while a seek the player was asked for has not landed yet, and false once it
  * has landed or failed. Only the player knows this: one that leaves it out is given a fixed wait
  * after each seek, and its seeks are never aimed ahead.
  * `durationSeconds` is the length of the title, when known. Without it nothing is aimed ahead
  * either, because where the end is cannot be known. */
 state():{itemId:string;positionSeconds:number;playing:boolean;buffering:boolean;ready:boolean;seeking?:boolean;durationSeconds?:number};
 load(itemId:string,positionSeconds:number,playing:boolean):void;
 play():void;pause():void;seek(positionSeconds:number):void;
 /** 1 restores normal speed. A player that cannot change speed may ignore it; drift then resolves by seeking. */
 setRate(rate:number):void;
}>;

/** How long the follower gives a seek of its own before judging the player again: until the
 * player says the seek is over, or for this whole time when it does not say. Longer than the
 * player's own seek timeout, so by then the seek has landed or failed. It is also the slowest seek
 * the follower compensates for; a slower one is asked again and the member reports lagging. */
const SEEK_SETTLE_MS=20000;
/** A seek aimed ahead must leave at least this much of the title after it, and at least as much as
 * it was aimed ahead by: landing early must never carry the player to the end before the group. */
const SEEK_END_ROOM_SECONDS=5;

/** Keeps one local player on the group's clock, and tells the group how this member is doing.
 * It corrects gently (a small speed change) before it corrects visibly (a seek), in the bands
 * the server publishes, and it never commands the group.
 *
 * A seek takes time, and the group keeps moving while it does. The follower lets each seek of its
 * own finish before judging where the player is, and it learns how far behind a moving group this
 * player's seeks land: the next one is aimed that far ahead, so it lands with the group instead of
 * behind it again. It aims ahead only for a player that says when its seeks land, and only where
 * that leaves room before the end of the title. */
export class RoomFollower{
 private timer?:Repeating;private running=false;private correctingUntil=0;private loadedFor='';private unsubscribe?:()=>void;
 /** The follower's own seek that is not known to be over: when it was asked, the group clock it
  * was aimed at, and the position asked for. */
 private own?:{at:number;clock:string;aim:number};
 /** Seconds this player's seeks have been landing behind a moving group. */
 private lead=0;
 private epoch='';private endReported='';private anchor = 0;
 private service:RoomSessionService;private player:RoomPlayer;private now:()=>number;
 constructor(service:RoomSessionService,player:RoomPlayer,now:()=>number=()=>Date.now()){this.service=service;this.player=player;this.now=now;}
 start(){if(this.timer)return;this.running=true;this.timer=repeat(async()=>{this.tick();},{everyMs:1000,backoff:{baseMs:1000,maxMs:1000},immediate:false});this.unsubscribe=this.service.subscribe(()=>this.tick());this.tick();}
 stop(){this.running=false;this.timer?.stop();this.timer=undefined;this.unsubscribe?.();this.unsubscribe=undefined;this.player.setRate(1);}
 /** Where the group is now, in seconds, or undefined when nothing is loaded. */
 target(): number | undefined {
  const group = this.service.getSnapshot().group;
  if (!group || !roomEntry(group)?.itemId) return undefined;
  const target = roomPositionMs(group, this.service.serverNow()) / 1000;
  const local = this.player.state(), length = local.itemId === roomEntry(group)?.itemId ? local.durationSeconds : undefined;
  return length !== undefined && Number.isFinite(length) && length > 0 ? Math.min(target, length) : target;
 }
 private tick() {
  if (!this.running) return;
  const snapshot = this.service.getSnapshot(), group = snapshot.group;
  if (!group || (snapshot.phase !== 'live' && snapshot.phase !== 'reconnecting')) return;
  const entry = roomEntry(group), target = this.target(), timeline = group.clock;
  if (!entry) return;
  if (!entry.available || !entry.itemId) {this.player.pause(); this.report('buffering', 0); return;}
  if (target === undefined) return;
  const epoch = [group.id, timeline.entryId, timeline.playing, timeline.waitUntil].join('/');
  const anchor = timeline.positionMs - (timeline.playing && !timeline.waitUntil ? Date.parse(timeline.at) : 0);
  if (this.epoch !== epoch || Math.abs(anchor - this.anchor) > 50) {this.epoch = epoch; this.anchor = anchor; this.correctingUntil = 0; this.player.setRate(1); this.own = undefined;}
  const local = this.player.state(), wantPlaying = timeline.playing && !timeline.waitUntil;
  const occurrence = this.player.occurrence?.(), key = group.id + '/' + entry.entryId;
  if (local.itemId !== entry.itemId || this.player.occurrence && (!occurrence || occurrence.groupId !== group.id || occurrence.entryId !== entry.entryId)) {
   if (this.loadedFor !== key) {this.loadedFor = key; this.own = undefined; this.lead = 0; this.player.load(entry.itemId, target, wantPlaying);}
   this.report('buffering', 0); return;
  }
  this.loadedFor = key;
  const length = local.durationSeconds;
  if (length !== undefined && Number.isFinite(length) && length > 0 && local.positionSeconds >= length - .25 && !local.playing && target >= length - .25) {
   this.player.setRate(1); this.report('paused', length);
   // Only the host advances the shared queue, once for this actual occurrence.
   if (group.you.host && timeline.playing && this.endReported !== key) {
    this.endReported = key;
    const next = group.queue[group.queue.findIndex(e => e.entryId === entry.entryId) + 1];
    if (next) void this.service.transport('watch', {entryId: next.entryId});
    else void this.service.transport('pause');
   }
   return;
  }
  if (!local.ready) {this.report('buffering', local.positionSeconds); return;}
  if (wantPlaying && !local.playing && !local.buffering) this.player.play();
  if (!wantPlaying && local.playing) this.player.pause();
  const drift = target - local.positionSeconds, magnitude = Math.abs(drift * 1000), at = this.now();
  const clock = epoch + '/' + this.anchor, band = group.jumpMs / 1000;
  if (this.settling(local, !!wantPlaying, target, clock, band, at) || local.buffering) {this.report('buffering', local.positionSeconds); return;}
  if (magnitude >= group.jumpMs || !wantPlaying && magnitude >= group.inStepMs) this.seek(local, target, !!wantPlaying, clock, at);
  else if (magnitude >= group.inStepMs && wantPlaying) {
   if (!this.correctingUntil) {this.correctingUntil = at + 4000; this.player.setRate(drift > 0 ? 1.05 : .95);}
   else if (at > this.correctingUntil) this.seek(local, target, true, clock, at);
  } else if (this.correctingUntil) {this.player.setRate(1); this.correctingUntil = 0;}
  this.report(local.buffering || local.seeking ? 'buffering' : timeline.waitUntil ? 'ready' : local.playing ? 'playing' : 'paused', local.positionSeconds);
 }
 /** A corrective seek at normal speed. While the group is moving it is aimed ahead by what this
  * player's seeks have cost, but never near the end of the title: there, or when the length of
  * the title is not known, it goes to where the group is and no further. */
 private seek(local:ReturnType<RoomPlayer['state']>,target:number,moving:boolean,clock:string,at:number){
  const length=local.durationSeconds,room=length!==undefined&&Number.isFinite(length)?length-(target+this.lead):0;
  const aim=moving&&this.lead>0&&room>=Math.max(this.lead,SEEK_END_ROOM_SECONDS)?target+this.lead:target;
  this.player.setRate(1);this.correctingUntil=0;this.own={at,clock,aim};
  this.player.seek(aim);
 }
 /** Whether the follower's last seek (or one the player reports) is still to be waited for. */
 private settling(local:ReturnType<RoomPlayer['state']>,moving:boolean,target:number,clock:string,band:number,at:number):boolean{
  const own=this.own;
  if(!own)return local.seeking===true;
  // The group was moved, paused or resumed since: that seek no longer answers where the group is,
  // so the player is judged afresh, and may be sent somewhere else at once.
  if(own.clock!==clock){this.own=undefined;return false;}
  const waited=at-own.at;
  // Only the player can say its seek is over. A player that does not say is given the whole wait:
  // its position moving proves nothing, since some players go on reporting where they were.
  if(local.seeking!==false&&waited<SEEK_SETTLE_MS)return true;
  this.own=undefined;
  // It said so, in time, and is where it was sent (a seek that failed leaves it somewhere else):
  // how far behind or ahead of the moving group that is adjusts the lead for the next seek.
  if(local.seeking===false&&waited<SEEK_SETTLE_MS&&moving&&Math.abs(local.positionSeconds-own.aim)<band)this.lead=Math.min(SEEK_SETTLE_MS/1000,Math.max(0,this.lead+target-local.positionSeconds));
  return false;
 }
 private report(state: GroupReportState, positionSeconds: number) {
  void this.service.report(state, positionSeconds * 1000);
 }
}
