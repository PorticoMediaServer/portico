import type {LibraryRouteProofOwner} from './library-route-proof';
import type {HttpLocalApi, LocalSession} from '@core/index.ts';
import {dependsOn, finalAnswer, isSharedReadSuperseded, type FreshnessHub, type FreshnessInterest, type InterestOutcome, type InterestReadContext} from '@core/freshness-hub.ts';
import type {LocalSessionRefresher} from '@core/session-refresh.ts';
import type {PreferencesReader} from '@core/preferences-reader.ts';
import {parseDirectSnapshot, type DirectSnapshot} from '@core/profile-management.ts';
import {parseServerSignIn, type ServerSignIn} from '@core/sign-in.ts';
import {parseLibraryList} from '@core/library-list.ts';
import {fetchGuideSourceSummaries, type GuideSourceSummaries} from '@core/guide/directory.ts';
import type {HomeService} from '@core/home-service.ts';
import type {EventsClient} from '@core/playback-v1/events.ts';
import {routePath} from '../../../packages/contracts/src/routes.ts';
import type {ShellFacts} from './bootstrap';
import {createShellFactsSource} from './fact-source';

export type ShellFactGroup = 'directory' | 'server' | 'members' | 'liveSources' | 'libraryChannels';
export type ShellFactWitnesses = Readonly<{system: ServerSignIn; direct: DirectSnapshot; guide?: Pick<GuideSourceSummaries, 'viewerFence' | 'revision'>}>;
type ShellAccount = Pick<ShellFacts['identity'], 'username' | 'profileName' | 'profileArt' | 'primaryProfile' | 'profiles' | 'canManageProfiles' | 'accountFacts' | 'capabilities'>;

export function shellAccount(snapshot: DirectSnapshot, profileId: string): ShellAccount {
  const profile = snapshot.profiles.find(value => value.id === profileId);
  if (!profile) throw new Error('The selected profile is no longer present.');
  if (!profile.primary && Object.values(snapshot.capabilities).some(Boolean)) throw new Error('Management is available only on the main profile.');
  return {capabilities: snapshot.capabilities, username: snapshot.account.username, profileName: profile.name, profileArt: profile.art,
    primaryProfile: profile.primary, canManageProfiles: snapshot.canManage,
    profiles: snapshot.profiles.map(value => ({id: value.id, name: value.name, initial: Array.from(value.name.trim())[0] ?? '', art: value.art,
      pinRequired: value.pinRequired, primary: value.primary, revision: value.revision, pinRevision: value.pinRevision, trustRevision: value.trustRevision})), accountFacts: []};
}

const systemAuthority = (value: ServerSignIn) => JSON.stringify([value.id, value.signIn, value.move, value.setupRequired, value.hostedConfigured, value.hostedAttached]);
function directAuthority(value: DirectSnapshot, profileId: string) {
  const profile = value.profiles.find(row => row.id === profileId);
  return JSON.stringify([value.serverId, value.authority, value.account.id, value.account.role, value.account.disabled,
    value.account.primaryProfileId, [...value.account.allowedLibraries].sort(), value.account.hostedAccountId, value.capabilities, value.canManage,
    value.custodyPending, value.twoStepSetupRequired, profile?.id, profile?.primary, Array.isArray(profile?.allowedLibraries) ? [...profile.allowedLibraries].sort() : profile?.allowedLibraries]);
}
export function shellProofChanged(initial: ShellFactWitnesses, next: Partial<ShellFactWitnesses>, profileId: string): boolean {
  return !!(next.system && systemAuthority(initial.system) !== systemAuthority(next.system)
    || next.direct && directAuthority(initial.direct, profileId) !== directAuthority(next.direct, profileId));
}
function failed(error: unknown): InterestOutcome {
  if (isSharedReadSuperseded(error)) throw error;
  const code = (error as {code?: unknown} | null)?.code;
  return {kind: 'failed', error, retryable: code === 'invalid_guide_directory' || code === 'invalid_response' || code === 'invalid_system' || code === 'invalid_home' || !finalAnswer(error)};
}
function guideFailure(error: unknown): InterestOutcome {
  const result = failed(error);
  return (error as {status?: unknown} | null)?.status === 404 ? {kind:'failed', error, retryable:true} : result;
}
const answered: InterestOutcome = {kind: 'answered'};

/** Fixed read/classification/projection adapters. The hub owns every ticket, timer and shared flight. */
export function createShellFactOwner(o: Readonly<{
  api: HttpLocalApi; session(): LocalSession; auth: LocalSessionRefresher; preferences: PreferencesReader;
  home: HomeService; libraryRouteProof?: LibraryRouteProofOwner; hub: FreshnessHub; initial: ShellFacts; witnesses: ShellFactWitnesses;
  isCurrent(): boolean; retire(): void; readonlyAccess?: boolean;
}>) {
  const source = createShellFactsSource(o.initial), admitted = o.session(), initial = o.witnesses;
  let closed = false, retained = false, confirmedGuide = initial.guide;
  const current = () => !closed && o.isCurrent();
  // A mounted owner can return the same strictly parsed proof without another
  // network read. Known eligibility changes still retire before its display work.
  const acceptDirect = (snapshot: DirectSnapshot): boolean => {
    if (!current()) return false;
    if (shellProofChanged(initial, {direct: snapshot}, admitted.viewer.profileId)) {o.retire();return false;}
    return true;
  };
  const interests: FreshnessInterest[] = [];
  const add = (name: string, dependencies: readonly string[], read: (context: InterestReadContext) => Promise<InterestOutcome>, background = false) => {
    const interest = o.hub.interest({name: 'shell-' + name, dependencies, read,
      ...(background ? {background: 'whenRetained' as const} : {})});
    interests.push(interest); return interest;
  };
  const sameRequest = (captured: LocalSession, signal: AbortSignal) => {
    const now = o.session();
    return current() && !signal.aborted && captured.sessionFamilyId === now.sessionFamilyId
      && (['serverId','authority','accountId','profileId','role'] as const).every(key => captured.viewer[key] === now.viewer[key]);
  };
  const publish = async (context: InterestReadContext, captured: LocalSession, project: () => void): Promise<InterestOutcome> => {
    if (!sameRequest(captured, context.signal)) return {kind: 'superseded'};
    await o.hub.settled(context.signal);
    if (!sameRequest(captured, context.signal)) return {kind: 'superseded'};
    project(); return answered;
  };
  const updateIdentity = (patch: Partial<ShellFacts['identity']>) => source.update({identity: {...source.getSnapshot().identity, ...patch}});
  const adoptGuide = (value: GuideSourceSummaries | undefined) => {
    const next = value && {viewerFence:value.viewerFence, revision:value.revision};
    const available = !!value?.sources.length;
    if (confirmedGuide?.viewerFence === next?.viewerFence && confirmedGuide?.revision === next?.revision
      && source.getSnapshot().identity.liveAvailable === available) return;
    // This proof stays in the guide domain; it is never a Home/preferences fence.
    confirmedGuide = next; updateIdentity({liveAvailable:available});
  };
  const guide = (context: InterestReadContext) => context.shared('guide', signal =>
    fetchGuideSourceSummaries(o.api, admitted.viewer.serverId, Intl.DateTimeFormat().resolvedOptions().timeZone, signal).catch(error => {
      if ((error as {status?: unknown} | null)?.status === 403) return undefined;
      throw error;
    }));
  const preference = (context: InterestReadContext) => context.shared('preferences:web', signal => o.preferences.observePreferences('web', signal));
  const adoptPreference = async (context: InterestReadContext, captured: LocalSession): Promise<InterestOutcome> => {
    const result = await preference(context);
    if (!sameRequest(captured, context.signal)) return {kind: 'superseded'};
    if (result.kind === 'scope-changed') {o.retire(); return {kind: 'superseded'};}
    if (result.kind === 'unreadable') return failed(result.error);
    return publish(context, captured, () => source.update({preferences: result.value}));
  };
  const preferences = add('preferences', [dependsOn.settings('preferences'), dependsOn.settings('server')], async context => {
    try {return await adoptPreference(context, o.session());} catch (error) {return failed(error);}
  });
  const directory = add('directory', [dependsOn.libraries], async context => {
    const captured = o.session(), proofTicket = o.libraryRouteProof?.beginDirectory();
    try {
      const raw = await o.api.request(routePath('get_libraries'), 'GET', undefined, context.signal);
      const value = parseLibraryList(raw);
      if (!sameRequest(captured, context.signal)) return {kind: 'superseded'};
      // Same successful body, separate strict membership witness. Tolerant loss
      // may update labels but cannot ever prove a route absent.
      if (proofTicket) o.libraryRouteProof?.directory(proofTicket, raw);
      return await publish(context, captured, () => updateIdentity({libraries: value.items, librariesUnavailable: undefined}));
    } catch (error) {return failed(error);}
  });
  const serverSystem = add('server-system', [dependsOn.settings('server')], async context => {
    const captured = o.session();
    try {
      const system = parseServerSignIn(await o.api.request(routePath('get_system'), 'GET', undefined, context.signal));
      if (!sameRequest(captured, context.signal)) return {kind:'superseded'};
      if (shellProofChanged(initial, {system}, admitted.viewer.profileId)) {o.retire(); return {kind:'superseded'};}
      return await publish(context, captured, () => source.update({serverName:system.name,
        identity:{...source.getSnapshot().identity, serverName:system.name, serverVersion:system.version, serverApiVersion:system.apiVersion}}));
    } catch (error) {return failed(error);}
  });
  const serverGuide = add('server-guide', [dependsOn.settings('server')], async context => {
    const captured = o.session();
    try {const value = await guide(context); return await publish(context, captured, () => adoptGuide(value));}
    catch (error) {return guideFailure(error);}
  });
  // A display group can have independent input obligations. Never collapse their
  // errors/floors into a single selected result that drops another input's debt.
  const server = {invalidate() {serverSystem.invalidate(); serverGuide.invalidate(); preferences.invalidate();}};
  const guideGroup = (name: 'liveSources' | 'libraryChannels') => add(name, [dependsOn.list(name)], async context => {
    const captured = o.session();
    try {const value = await guide(context); return await publish(context, captured, () => adoptGuide(value));}
    catch (error) {return guideFailure(error);}
  });
  const liveSources = guideGroup('liveSources'), libraryChannels = guideGroup('libraryChannels');
  // One pending accepted projection; the authority read is released before input settles.
  let directProjection: Readonly<{snapshot: DirectSnapshot; session: LocalSession}> | undefined;
  const members = add('members', [], async context => {
    const value = directProjection;
    if (!value) return {kind: 'deferred'};
    return publish(context, value.session, () => {
      if (directProjection !== value) return;
      const {accountFacts: _unchanged, ...profile} = shellAccount(value.snapshot, admitted.viewer.profileId);
      updateIdentity(profile);
      directProjection = undefined;
    });
  });
  const direct = add('direct-authority', [dependsOn.list('members')], async context => {
    const captured = o.session();
    try {
      const raw = await o.api.request(routePath('get_direct_account'), 'GET', undefined, context.signal);
      // The strict parse of this same body is required before absence/eligibility inference.
      const snapshot = parseDirectSnapshot(raw);
      if (!sameRequest(captured, context.signal)) return {kind: 'superseded'};
      if (!acceptDirect(snapshot)) return {kind: 'superseded'};
      directProjection = {snapshot, session: captured}; members.invalidate(); return answered;
    } catch (error) {return failed(error);}
  }, true);
  const permission = o.readonlyAccess ? undefined : add('permission-authority', [dependsOn.list('members')], async context => {
    const result = await o.auth.reconcilePermissions({signal: context.signal});
    switch (result.kind) {
      case 'confirmed-unchanged': return answered;
      case 'adopted': case 'ended': return current() ? answered : {kind: 'superseded'};
      case 'deferred': return {kind: 'deferred', nextAllowedAt: result.nextAllowedAt};
      case 'superseded': return {kind: 'superseded'};
      case 'failed': return {kind: 'failed', error: result.error, retryable: result.retryable};
    }
  }, true);
  const homeAuthority = add('home-authority', [dependsOn.libraries, dependsOn.list('members'), dependsOn.settings('server'), dependsOn.settings('preferences')], async context => {
    const proofTicket = o.libraryRouteProof?.beginHome();
    try {
      const kind = await o.home.authority({signal: context.signal, isCurrent: current});
      if (kind === 'answered' && current() && proofTicket) o.libraryRouteProof?.homeAnswered(proofTicket);
      return {kind};
    } catch (error) {if (!current()) return {kind: 'superseded'}; return failed(error);}
  }, true);
  const groups = {directory, server, members, liveSources, libraryChannels};
  const request = (group: ShellFactGroup) => {
    if (!current()) return;
    if (group === 'directory') o.libraryRouteProof?.invalidate();
    if (group === 'members' || group === 'server') o.libraryRouteProof?.invalidateHome();
    if (group === 'members') {direct.invalidate(); permission?.invalidate();} else groups[group].invalidate();
    if (group === 'members' || group === 'directory' || group === 'server') homeAuthority.invalidate();
  };
  return {
    source, request,
    observeDirect: (snapshot: DirectSnapshot) => {acceptDirect(snapshot);},
    preferences: () => o.hub.note(dependsOn.settings('preferences')),
    authority: () => {o.libraryRouteProof?.invalidateHome(); homeAuthority.invalidate();},
    all() {if (!current()) return; for (const group of ['directory', 'server', 'members', 'liveSources', 'libraryChannels'] as const) request(group); preferences.invalidate(); direct.invalidate(); permission?.invalidate(); homeAuthority.invalidate();},
    setRetained(value: boolean) {if (closed || retained === value) return; retained = value; for (const interest of [homeAuthority, direct, permission]) interest?.setRetained(value);},
    dispose() {if (closed) return; closed = true; directProjection = undefined; for (const interest of interests) interest.dispose(); source.dispose();},
  };
}

/** First successful connection needs no cursor: a quiet relay follower may never get one. */
export function attachShellFactFeed(events: EventsClient, hub: FreshnessHub, current: () => boolean, invalidate: () => void): () => void {
  let queued = false, timer: unknown;
  const status = () => {
    if (queued || !current() || !events.getStatus().connected) return;
    queued = true;
    timer = hub.timers.setTimer(() => {timer = undefined; if (current()) invalidate();}, 0);
  };
  const offStatus = events.subscribeStatus(status), offResync = events.onResync(() => {if (current()) invalidate();});
  status();
  return () => {offStatus(); offResync(); if (timer !== undefined) hub.timers.clearTimer(timer);};
}

/** Platform signals invalidate canonical obligations; hidden display stays paused. */
export function bindShellFactWake(page: Pick<Document, 'hidden' | 'addEventListener' | 'removeEventListener'>,
  browser: Pick<Window, 'addEventListener' | 'removeEventListener'>,
  o: Readonly<{isCurrent(): boolean; retained(): boolean; wakeAuth(): void; invalidate(): void}>) {
  const foreground = () => {if (o.isCurrent() && !page.hidden) {o.wakeAuth(); o.invalidate();}};
  const online = () => {
    if (!o.isCurrent()) return;
    if (!page.hidden || o.retained()) o.wakeAuth();
    o.invalidate();
  };
  page.addEventListener('visibilitychange', foreground); browser.addEventListener('online', online);
  return () => {page.removeEventListener('visibilitychange', foreground); browser.removeEventListener('online', online);};
}
