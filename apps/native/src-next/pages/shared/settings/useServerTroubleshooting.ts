import {useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {AppState, type ScrollView} from 'react-native';
import {serverNow} from '../../../../../../packages/client-core/src/server-clock';
import {repeat} from '../../../../../../packages/client-core/src/timer';
import {KEEP_MESSAGE_DAYS, MESSAGE_KINDS, SettingsMessagesSource, type KeepMessageDays, type MessageKind} from '../../../../../../packages/client-core/src/server-admin/messages-source';
import {LOG_DETAIL_MINUTES, logLevels, type LogDetailMinutes} from '../../../../../../packages/client-core/src/server-administration';
import {readSkippedFiles, type SkippedFile} from '../../../../../../packages/client-core/src/admin/skipped-files';
import {SettingsFeedbackSource, feedbackSentFrom, feedbackTriageCategory, type FeedbackReport, type FeedbackSnapshot} from '../../../../../../packages/client-core/src/server-admin/feedback-source';
import type {FeedbackStatus} from '../../../../../../packages/client-core/src/console';
import {feedbackCategoryName, feedbackStatusName} from '../../../../../../packages/client-core/src/server-admin/console-catalogue';
import {sentence, when} from '../../../../../../packages/client-core/src/server-admin/console-words';
import {OwnerAppLogsSource, type AppReport} from '../../../../../../packages/client-core/src/server-admin/owner-app-logs';
import {SUPPORT_COMPONENTS, type SupportComponent, fixOwnerSupportPermissions, OWNER_SUPPORT_DEFAULT_COMPONENTS, OWNER_SUPPORT_WINDOWS, prepareOwnerSupportExport, readOwnerSupport,
  SUPPORT_CAPABILITY_STATES, supportCapabilityName, type OwnerSupportFacts, type OwnerSupportOptions} from '../../../../../../packages/client-core/src/server-admin/support-next';
import {skippedParts, resetSkippedParts, skippedVersion, subscribeSkipped} from '../../../../../../packages/client-core/src/tolerant';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import {linkNativeAbortSignals} from '../../../platform/abort-signals';
import {diagnosticsSavePort, supportZipSavePort} from '../../../platform/diagnostics-save';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {NativeScreenLifetime, screenScopeEnded} from '../../../screens/settings-live/lifetime';
import {nativeStorageOwner} from '../../../screens/settings-live/server/storage/space-source';

type Translate = typeof defaultI18n.t;
export type TroubleshootingTab = 'messages' | 'feedback' | 'reports' | 'support' | 'skipped';
type ViewerRole = 'owner' | 'administrator' | 'member';

/** Settings › Server › Troubleshooting (owner-server.html frame 33). An administrator reads Messages and the files Portico
 * could not prepare (console rule 1); Feedback, Reports and Support are the owner's. */
export function useTroubleshooting(ports: NativeSettingsLivePorts, runtime: NativeShellRuntime, initialTab?: TroubleshootingTab) {
  const t = runtime.text.t as Translate, context = ports.context;
  const viewerRole: ViewerRole = context.account.account.role === 'owner' ? 'owner' : context.account.canManage ? 'administrator' : 'member';
  const [tab, setTab] = useState<TroubleshootingTab>(initialTab ?? 'messages'), [scrollY, setScrollY] = useState(0);
  const scrollRef = useRef<ScrollView>(null);
  const feedback = useFeedbackGroup(ports, runtime), reports = useReportsGroup(ports, runtime), support = useSupportGroup(ports);
  const openReports = feedback.state.reports.filter(report => report.status === 'open' || report.status === 'in-progress').length;
  const owner = viewerRole === 'owner';
  return {t, owner, tab, scrollY, setScrollY, scrollRef, scrollTo: (y: number) => scrollRef.current?.scrollTo({y, animated: true}), feedback, reports, support,
    choose: (value: string) => {if (value === 'messages' || value === 'feedback' || value === 'reports' || value === 'support' || value === 'skipped') setTab(value);},
    tabs: [{value: 'messages', label: t('web.logs.messages')}, {value: 'skipped', label: t('next.settings.troubleshootingStuckFiles')},
      ...(owner ? [{value: 'feedback', label: openReports ? `${t('server.feedbackTab')} ${openReports}` : t('server.feedbackTab')},
        {value: 'reports', label: t('server.reportsTab')}, {value: 'support', label: t('server.support')}] : [])]};
}

/** The owner and administrators read Messages (console rule 1, frame 33); only the owner keeps it and records more detail. */
/** Troubleshooting's server calls on the phone: on this screen's lifetime, and only while `current` holds. */
export function troubleshootingApi(context: NativeShellBootstrapContext, lifetime: NativeScreenLifetime, current: () => boolean) {
  return {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => current()
    ? lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller) : Promise.reject(screenScopeEnded())};
}
export function messagesReader(context: NativeShellBootstrapContext): boolean {
  const authority = context.authority(), account = context.account;
  return nativeStorageOwner(context) || context.current() && account.serverId === authority.serverId && account.account.id === authority.accountId && !account.account.disabled && account.canManage;
}
function createMessages(context: NativeShellBootstrapContext, reportError: NativeShellRuntime['reportError']) {
  const lifetime = new NativeScreenLifetime(context), listeners = new Set<() => void>(), owner = nativeStorageOwner(context);
  const current = () => lifetime.current() && messagesReader(context);
  const api = troubleshootingApi(context, lifetime, current);
  let failed = false, desired = false, foreground = AppState.currentState === 'active', token: object | undefined, timer: ReturnType<typeof setTimeout> | undefined;
  const notify = () => {for (const listener of listeners) listener();};
  const handleFailure = (error: unknown) => {
    if (source.getSnapshot().phase === 'idle') {const owned = current(); dispose(); if (owned) reportError(error);}
    else {failed = true; notify();}
  };
  // How long messages are kept is the owner's alone: an administrator's source never reads it.
  const source = new SettingsMessagesSource(api, undefined, current, handleFailure, {keeping: owner});
  const stop = () => {token = undefined; clearTimeout(timer); timer = undefined;};
  const active = (captured: object) => current() && desired && foreground && token === captured;
  const poll = async (captured: object) => {
    if (!active(captured)) return;
    const state = source.getSnapshot();
    if (state.phase !== 'loading' && !state.changing) {if (!active(captured)) return; await source.load();}
    if (!active(captured)) return;
    if (source.getSnapshot().phase === 'error') {desired = false; stop(); source.setLive(false); return;}
    timer = setTimeout(() => {if (active(captured)) void poll(captured);}, 3000);
  };
  const start = (immediate: boolean) => {stop(); if (!current() || !desired || !foreground) return; const captured = {}; token = captured; source.setLive(true); if (immediate) void poll(captured); else timer = setTimeout(() => {if (active(captured)) void poll(captured);}, 3000);};
  const setLive = (live: boolean) => {if (!current()) return; desired = live; stop(); source.setLive(live && foreground); if (live) start(true);};
  const subscription = AppState.addEventListener('change', value => {foreground = value === 'active'; stop(); if (!current()) return; if (!foreground) source.pause(); else if (desired) start(true);});
  const dispose = () => {desired = false; stop(); subscription.remove(); source.dispose(); lifetime.dispose(); listeners.clear();};
  let selection: object | undefined;
  return {context, source, current, owner, dispose, setLive, failure: handleFailure,
    getFailure: () => failed, subscribeFailure: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}, clearFailure: () => {failed = false; notify();},
    select: async (level: Parameters<SettingsMessagesSource['select']>[0], kind: Parameters<SettingsMessagesSource['select']>[1]) => {if (!current()) return; const captured = {}; selection = captured; stop(); await source.select(level, kind); if (selection === captured && current() && desired) start(false);},
  };
}
export type MessagesGroup = ReturnType<typeof createMessages>;
export function useMessagesGroup(ports: NativeSettingsLivePorts, runtime: NativeShellRuntime) {
  const context = ports.context, reportError = runtime.reportError, [group, setGroup] = useState<MessagesGroup>();
  useLayoutEffect(() => {const next = createMessages(context, reportError); setGroup(next); return next.dispose;}, [context, reportError]);
  return group?.context === context ? group : undefined;
}

/** `pageScrollY` is how far the page is scrolled: away from the log's top, new lines wait (rule 66, newest first). */
export function useMessages(group: MessagesGroup, runtime: NativeShellRuntime, pageScrollY = 0) {
  const [rootY, setRootY] = useState(0), [logY, setLogY] = useState(0);
  useEffect(() => {group.source.setAtTop(pageScrollY - (rootY + logY) < 48);}, [group, pageScrollY, rootY, logY]);
  const {source} = group, state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot), failure = useSyncExternalStore(group.subscribeFailure, group.getFailure, group.getFailure), t = runtime.text.t as Translate;
  const [now, setNow] = useState(serverNow), [filter, setFilter] = useState<Readonly<{kind: 'level' | 'category'}>>();
  const filterRef = useRef(filter);
  const openFilter = (kind: 'level' | 'category') => {if (!group.current() || filterRef.current) return; const next = {kind}; filterRef.current = next; setFilter(next);};
  const closeFilter = () => {if (!filter || filterRef.current !== filter) return; filterRef.current = undefined; setFilter(undefined);};
  useEffect(() => {
    void source.load();
    const clock = repeat(async () => {if (group.current()) setNow(serverNow());}, {everyMs: 30000, backoff: {baseMs: 30000, maxMs: 30000, jitter: 'none'}, immediate: false});
    return () => clock.stop();
  }, [source, group]);
  const until = state.detailUntil, recording = !!until && Date.parse(until) > now;
  const detail = () => {if (!group.current() || source.getSnapshot().changing) return; group.clearFailure(); void source.detail(!recording).catch(error => {if (group.current()) group.failure(error);});};
  const keep = (days: KeepMessageDays) => {if (!group.current()) return; group.clearFailure(); void source.keep(days).catch(error => {if (group.current()) group.failure(error);});};
  const category = filter?.kind === 'category';
  return {state, failure, recording, rootY, logY, setRootY, setLogY,
    window: {title: recording ? t('next.settingsSpec.server.troubleshooting.groups.0.rows.0.label') : t('settings.server.detailWindow.forMinutes', {minutes: state.detailMinutes}),
      body: recording && until ? t('settings.server.detailWindow.until', {time: defaultI18n.time(Date.parse(until))}) : t('settings.server.detailWindow.help'),
      action: group.owner ? {label: t(recording ? 'settings.server.detailWindow.stop' : 'settings.server.detailWindow.start'), busy: state.changing, onPress: detail} : undefined},
    levelLabel: state.level ? t(`web.logs.level.${state.level}`) : t('web.logs.allLevels'), kindLabel: state.kind ? t(`web.logs.category.${state.kind}`) : t('web.logs.everything'),
    openFilter, closeFilter, filterOpen: !!filter && filterRef.current === filter && group.current(), filterTitle: t(category ? 'feedback.category' : 'web.logs.level'),
    filterItems: (category ? [{value: '', label: t('web.logs.everything')}, ...MESSAGE_KINDS.map(value => ({value, label: t(`web.logs.category.${value}`)}))]
      : [{value: '', label: t('web.logs.allLevels')}, ...logLevels.map(value => ({value, label: t(`web.logs.level.${value}`)}))]).map(option => ({id: option.value || 'all', label: option.label, selected: option.value === (category ? state.kind : state.level), onSelect: () => {
        if (!filter || filterRef.current !== filter || !group.current()) return;
        closeFilter();
        if (filter.kind === 'category') {if (option.value === '' || MESSAGE_KINDS.includes(option.value as MessageKind)) void group.select(source.getSnapshot().level, option.value as MessageKind | '');}
        else if (option.value === '' || logLevels.includes(option.value as typeof logLevels[number])) void group.select(option.value as typeof state.level, source.getSnapshot().kind);
      }})),
    setLive: (value: boolean) => {if (group.current()) group.setLive(value);},
    refresh: () => {if (group.current()) {group.clearFailure(); void source.load();}},
    showWaiting: () => source.showWaiting(),
    lines: state.lines.map(row => ({key: row.sequence, time: defaultI18n.time(Date.parse(row.at)), level: t(`web.logs.level.${row.level}`), tone: row.level, kind: t(`server.logCategory.${row.kind}`), message: row.message, detail: row.detail || undefined})),
    detailLength: group.owner && !recording ? {label: t('next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.1.label'), value: String(state.detailMinutes),
      options: LOG_DETAIL_MINUTES.map(value => ({value: String(value), label: t('web.dvr.minutes', {count: value})})),
      onValueChange: (value: string) => {const chosen = Number(value); if (group.current() && LOG_DETAIL_MINUTES.includes(chosen as LogDetailMinutes)) source.chooseLength(chosen as LogDetailMinutes);}} : undefined,
    keeping: state.keepDays ? {label: t('settings.server.keepMessages'), value: String(state.keepDays),
      options: KEEP_MESSAGE_DAYS.map(days => ({value: String(days), label: t('settings.server.keepMessages.days', {days})})),
      onValueChange: (value: string) => {const days = Number(value); if (KEEP_MESSAGE_DAYS.includes(days as KeepMessageDays)) keep(days as KeepMessageDays);}} : undefined};
}

type SkippedRead = Readonly<{items: readonly SkippedFile[]; next?: string; loading: boolean; failed: boolean}>;
/** Files Portico could not prepare (console rules 1 and 72), for the owner and administrators: an administrator sees
 *  those of the libraries they are allowed. Each file with its library and the server's reason. */
export function useSkippedFiles(ports: NativeSettingsLivePorts) {
  const context = ports.context;
  const [lifetime, setLifetime] = useState<NativeScreenLifetime>();
  const [read, setRead] = useState<SkippedRead>({items: [], loading: true, failed: false});
  useLayoutEffect(() => {const next = new NativeScreenLifetime(context); setLifetime(next); return () => next.dispose();}, [context]);
  const load = (life: NativeScreenLifetime, cursor?: string) => {
    const current = () => life.current() && messagesReader(context);
    if (!current()) return;
    setRead(before => ({...before, loading: true, failed: false}));
    const api = troubleshootingApi(context, life, current);
    void readSkippedFiles(api, cursor).then(page => {if (current()) setRead(before => ({items: cursor ? [...before.items, ...page.items] : page.items, next: page.nextCursor, loading: false, failed: false}));},
      () => {if (current()) setRead(before => ({...before, loading: false, failed: true}));});
  };
  // The first read goes through the latest `load`, published at commit.
  const ports_ = useRef({load});
  useEffect(() => {ports_.current = {load};});
  useEffect(() => {if (lifetime) ports_.current.load(lifetime);}, [lifetime]);
  return {read, more: () => {if (lifetime) load(lifetime, read.next);}};
}

const STATUSES: readonly FeedbackStatus[] = ['open', 'in-progress', 'resolved', 'closed'];
const EMPTY: FeedbackSnapshot = Object.freeze({phase: 'idle', reports: []});
const noopSubscribe = () => () => {};
const emptySnapshot = () => EMPTY;
function createFeedback(context: NativeShellBootstrapContext, reportError: NativeShellRuntime['reportError']) {
  const lifetime = new NativeScreenLifetime(context), current = () => lifetime.current() && nativeStorageOwner(context);
  const api = {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => current() ? lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller) : Promise.reject(screenScopeEnded())};
  const source = new SettingsFeedbackSource(api, lifetime.authority.serverId, lifetime.authority.profileId, current, error => {const owned = current(); source.dispose(); lifetime.dispose(); if (owned) reportError(error);});
  return {context, source, current, dispose: () => {source.dispose(); lifetime.dispose();}};
}
export type FeedbackGroup = ReturnType<typeof createFeedback>;
export function useFeedbackGroup(ports: NativeSettingsLivePorts, runtime: NativeShellRuntime) {
  const context = ports.context, reportError = runtime.reportError, [group, setGroup] = useState<FeedbackGroup>();
  useLayoutEffect(() => {const next = createFeedback(context, reportError); setGroup(next); return next.dispose;}, [context, reportError]);
  useEffect(() => {if (group?.context === context) void group.source.load();}, [group, context]);
  const actual = group?.context === context ? group : undefined;
  const state = useSyncExternalStore(actual?.source.subscribe ?? noopSubscribe, actual?.source.getSnapshot ?? emptySnapshot, actual?.source.getSnapshot ?? emptySnapshot);
  return {group: actual, state};
}
type FeedbackDialog = Readonly<{abort: AbortController}>;
type Draft = Readonly<{report: FeedbackReport; status: FeedbackStatus; reply: string}>;
type FeedbackScope = {active: boolean; dialog?: FeedbackDialog; draft?: Draft; saving?: object};
/** The owner's feedback inbox: each report, and one at a time opened to set its status and reply. */
export function useFeedback(group: FeedbackGroup) {
  const {source} = group, state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot), t = defaultI18n.t;
  const [messageOpen, setMessageOpen] = useState(false);
  const [scope, setScope] = useState<FeedbackScope>(), [dialog, setDialog] = useState<FeedbackDialog>(), [draft, setDraft] = useState<Draft>(), [busy, setBusy] = useState(false), [loading, setLoading] = useState(false), [error, setError] = useState(false);
  useLayoutEffect(() => {const next: FeedbackScope = {active: true}; setScope(next); return () => {next.active = false; next.dialog?.abort.abort(); next.dialog = undefined; next.draft = undefined;};}, [source]);
  const current = () => !!scope?.active && group.current();
  const held = dialog && scope?.dialog === dialog && !dialog.abort.signal.aborted && current() ? dialog : undefined;
  const close = () => {if (!scope || scope.dialog !== held) return; scope.dialog = undefined; scope.draft = undefined; held?.abort.abort(); setDialog(undefined); setDraft(undefined); setLoading(false); setError(false);};
  const open = async (report: FeedbackReport) => {
    if (!scope || !current() || scope.dialog || scope.saving || !source.getSnapshot().reports.includes(report)) return;
    const captured: FeedbackDialog = {abort: new AbortController()}; scope.dialog = captured; scope.draft = undefined; setDialog(captured); setDraft(undefined); setLoading(true); setError(false); setBusy(false);
    const owns = () => current() && scope.dialog === captured && !captured.abort.signal.aborted;
    try {if (!owns()) return; const value = await source.read(report.id, captured.abort.signal); if (!owns() || !source.getSnapshot().reports.includes(value)) return; const next: Draft = {report: value, status: value.status === 'open' ? 'in-progress' : value.status, reply: ''}; scope.draft = next; setDraft(next);} catch {if (owns()) setError(true);} finally {if (owns()) setLoading(false);}
  };
  const editable = () => !!held && !!draft && scope?.draft === draft && source.getSnapshot().reports.includes(draft.report) && !scope.saving;
  const change = (next: Draft) => {if (!scope || !editable()) return; scope.draft = next; setDraft(next); setError(false);};
  const save = async () => {
    const captured = held, original = draft; if (!scope || !captured || !original || !editable()) return;
    const flight = {}; scope.saving = flight; setBusy(true); setError(false);
    const owns = () => current() && scope.dialog === captured && scope.draft === original && scope.saving === flight && !captured.abort.signal.aborted;
    try {if (!owns()) return; await source.triage(original.report, original.status, original.reply, captured.abort.signal); if (owns()) close();} catch {if (owns()) setError(true);} finally {if (scope.saving === flight) {scope.saving = undefined; if (current() && scope.dialog === captured) setBusy(false);}}
  };
  const shown = draft && scope?.draft === draft ? draft : undefined;
  const category = (report: FeedbackReport) => feedbackCategoryName(t, defaultI18n.has, feedbackTriageCategory(report));
  return {state, messageOpen, setMessageOpen, openMessage: () => {if (current()) setMessageOpen(true);}, held: !!held, shown, busy, loading, error, close, save: () => {void save();},
    retry: () => {if (current() && !scope?.dialog && !scope?.saving) void source.load();},
    more: () => {if (current() && !scope?.dialog && !scope?.saving && source.getSnapshot() === state) void source.more();},
    rows: state.reports.map(report => ({key: report.id, title: report.message.split('\n')[0], open: () => {void open(report);},
      detail: report.deviceName ? t('web.feedback.rowSubtitleFrom', {category: category(report), when: when(report.createdAt), device: report.deviceName, about: report.itemId ? 'yes' : 'no'})
        : t('web.feedback.rowSubtitle', {category: category(report), when: when(report.createdAt), about: report.itemId ? 'yes' : 'no'}),
      status: feedbackStatusName(t, defaultI18n.has, report.status)})),
    sheet: shown ? {title: category(shown.report), description: t('web.feedback.reportedMeta', {when: when(shown.report.createdAt), status: feedbackStatusName(t, defaultI18n.has, shown.report.status)}),
      sentFromDevice: shown.report.deviceName ? t('web.feedback.sentFromDevice', {device: shown.report.deviceName}) : undefined,
      sentFrom: shown.report.client ? t('server.feedback.sentFrom', {...feedbackSentFrom(shown.report)!, platform: sentence(shown.report.client.platform)}) : undefined,
      statuses: STATUSES.map(status => ({value: status, label: feedbackStatusName(t, defaultI18n.has, status)})),
      setStatus: (value: string) => {const status = STATUSES.find(status => status === value); if (status) change({...shown, status});},
      setReply: (reply: string) => change({...shown, reply})} : undefined};
}

const LANES = [{value: 'runtime', label: 'web.logs.laneServer'}, {value: 'audit', label: 'web.logs.laneAudit'}] as const;
function createReports(context: NativeShellBootstrapContext, reportError: NativeShellRuntime['reportError']) {
  const lifetime = new NativeScreenLifetime(context), current = () => lifetime.current() && nativeStorageOwner(context);
  const api = {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => current() ? lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller) : Promise.reject(screenScopeEnded())};
  // App problem reports and the server's records (rebuilt /v1/admin/reports, logs, audit). Files Portico could not
  // prepare have their own tab, so this source does not read them again.
  const source = new OwnerAppLogsSource({...api, readReportLog: context.api.readReportLog.bind(context.api)}, lifetime.authority.serverId, lifetime.authority.profileId, current, error => {const owned = current(); source.dispose(); lifetime.dispose(); if (owned) reportError(error);}, {reports: true, stuckFiles: false});
  return {context, source, current, signal: lifetime.signal, dispose: () => {source.dispose(); lifetime.dispose();}};
}
export type ReportsGroup = ReturnType<typeof createReports>;
export function useReportsGroup(ports: NativeSettingsLivePorts, runtime: NativeShellRuntime) {
  const context = ports.context, reportError = runtime.reportError, [group, setGroup] = useState<ReportsGroup>();
  useLayoutEffect(() => {const next = createReports(context, reportError); setGroup(next); return next.dispose;}, [context, reportError]);
  // Reports are the owner's alone; an administrator's page never asks for them.
  const owner = context.account.account.role === 'owner';
  useEffect(() => {if (owner && group?.context === context) void group.source.load();}, [group, context, owner]);
  return group?.context === context ? group : undefined;
}
type ReportDialog = Readonly<{report: AppReport; abort: AbortController}>;
type Confirmation = Readonly<{dialog: ReportDialog; report: AppReport}>;
type ReportScope = {active: boolean; dialog?: ReportDialog; confirmation?: Confirmation; flight?: object};
/** What a report says, with its attached app log when it has one. */
const reportText = (report: AppReport & {log?: string}): string => [report.message, report.log].filter(Boolean).join('\n\n');
/** Problem reports from the apps, each opened to read, save or delete; then the server's records by lane. */
export function useReports(group: ReportsGroup) {
  const {source} = group, state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot), t = defaultI18n.t;
  const [savePort] = useState(() => diagnosticsSavePort());
  const [scope, setScope] = useState<ReportScope>(), [dialog, setDialog] = useState<ReportDialog>(), [confirmation, setConfirmation] = useState<Confirmation>(), [body, setBody] = useState<string>(), [busy, setBusy] = useState(false), [error, setError] = useState(false);
  useLayoutEffect(() => {const next: ReportScope = {active: true}; setScope(next); return () => {next.active = false; next.dialog?.abort.abort(); next.confirmation?.dialog.abort.abort(); next.dialog = undefined; next.confirmation = undefined;};}, [source]);
  useLayoutEffect(() => {const selected = scope?.dialog; if (scope && selected && !source.listed(selected.report)) {scope.dialog = undefined; scope.confirmation = undefined; selected.abort.abort(); setDialog(undefined); setConfirmation(undefined); setBody(undefined);}}, [scope, state.reports, source]);
  const current = () => !!scope?.active && group.current();
  const held = dialog && scope?.dialog === dialog && current() && source.listed(dialog.report) ? dialog : undefined;
  const heldConfirmation = confirmation && scope?.confirmation === confirmation && current() && source.listed(confirmation.report) ? confirmation : undefined;
  const close = () => {if (!scope || scope.dialog !== held) return; scope.dialog = undefined; scope.confirmation = undefined; held?.abort.abort(); setDialog(undefined); setConfirmation(undefined); setBody(undefined); setError(false);};
  const closeConfirmation = () => {if (!scope || scope.confirmation !== heldConfirmation) return; if (scope.flight) {close(); return;} scope.confirmation = undefined; setConfirmation(undefined);};
  const open = async (report: AppReport) => {
    if (!scope || !current() || scope.dialog || scope.flight || state.phase !== 'ready' || source.getSnapshot() !== state || !source.listed(report)) return;
    const captured: ReportDialog = {report, abort: new AbortController()}; scope.dialog = captured; setDialog(captured); setBody(undefined); setError(false); setBusy(true);
    const owns = () => current() && scope.dialog === captured && !captured.abort.signal.aborted && source.listed(report);
    try {if (!owns()) return; const value = await source.read(report.id, captured.abort.signal, report); if (owns()) setBody(reportText(value));} catch {if (owns()) setError(true);} finally {if (owns()) setBusy(false);}
  };
  const ask = () => {if (!scope || !held || scope.dialog !== held || scope.flight || source.getSnapshot().phase !== 'ready') return; const next = {dialog: held, report: held.report}; scope.confirmation = next; setConfirmation(next);};
  const remove = async () => {
    const captured = heldConfirmation; if (!scope || !captured || scope.confirmation !== captured || scope.flight || !current() || !source.listed(captured.report) || source.getSnapshot().phase !== 'ready') return;
    const flight = {}; scope.flight = flight; setBusy(true); setError(false);
    const owns = () => current() && scope.flight === flight && scope.confirmation === captured && scope.dialog === captured.dialog && !captured.dialog.abort.signal.aborted;
    try {if (!owns()) return; const done = await source.remove(captured.report.id, captured.report, {signal: captured.dialog.abort.signal, isCurrent: () => owns() && source.listed(captured.report)}); if (owns()) {if (done) {scope.dialog = undefined; scope.confirmation = undefined; captured.dialog.abort.abort(); setDialog(undefined); setConfirmation(undefined); setBody(undefined);} else {scope.confirmation = undefined; setConfirmation(undefined); setError(true);}}} catch {if (owns()) setError(true);} finally {if (scope.flight === flight) {scope.flight = undefined; if (current()) setBusy(false);}}
  };
  const save = async () => {
    const captured = held; if (!scope || !captured || !savePort || !current() || scope.dialog !== captured || scope.flight || !source.listed(captured.report)) return;
    const flight = {}; scope.flight = flight; setBusy(true); setError(false);
    const read = new AbortController(), linked = linkNativeAbortSignals([captured.abort.signal, group.signal, read.signal]), signal = linked.signal;
    const owns = () => current() && scope.dialog === captured && scope.flight === flight && !signal.aborted && source.listed(captured.report);
    const off = source.subscribe(() => {if (!owns()) read.abort();});
    try {if (!owns()) return; const report = await source.read(captured.report.id, signal, captured.report); if (!owns() || report.id !== captured.report.id) return; const result = await savePort('report', {...report, body: reportText(report)}, signal, owns); if (owns() && result === 'saved') close();} catch {if (owns()) setError(true);} finally {linked.dispose(); off(); read.abort(); if (scope.flight === flight) {scope.flight = undefined; if (current()) setBusy(false);}}
  };
  const records = state.records;
  return {state, busy, error, body, canSave: !!savePort, held, heldConfirmation: !!heldConfirmation, close, closeConfirmation, ask, remove: () => {void remove();}, save: () => {void save();},
    title: held ? held.report.deviceName || t('server.reports.unknownSender') : undefined,
    refresh: () => {if (current() && !scope?.dialog && !scope?.flight) void source.load();},
    more: () => {if (current() && !scope?.dialog && !scope?.flight && source.getSnapshot() === state) void source.more();},
    rows: state.reports.map(report => ({key: report.id, title: [report.deviceName || t('server.reports.unknownSender'), report.message?.split('\n')[0]].filter((part): part is string => !!part),
      detail: when(Date.parse(report.createdAt)), open: () => {void open(report);}})),
    records: records ? {lane: records.lane, lanes: LANES.map(lane => ({value: lane.value, label: t(lane.label)})), hasOlder: !!records.next,
      chooseLane: (value: string) => {const lane = LANES.find(lane => lane.value === value); if (current() && lane && source.getSnapshot().records === records) void source.selectLane(lane.value);},
      latest: () => {if (current() && source.getSnapshot().records === records) void source.latestRecords();},
      older: () => {if (current() && source.getSnapshot().records === records) void source.moreRecords();},
      items: records.items.map((record, index) => ({key: `${record.at}:${index}`, title: `${when(Date.parse(record.at))} · ${record.message}`,
        detail: record.actor || record.target || record.detail ? [record.actor, record.target, record.detail].filter(Boolean).join(' · ') : undefined}))} : undefined};
}

const PARTS: Readonly<Record<SupportComponent, MessageId>> = {runtime: 'web.logs.includeServer', client: 'web.logs.includeClient', audit: 'web.logs.includeAudit', snapshot: 'web.logs.includeSnapshot'};
/** The export's time windows (1 hour to 7 days) in the spec's own words, in the same order. */
const WINDOWS = OWNER_SUPPORT_WINDOWS.map((minutes, index) => ({minutes, label: `next.settingsSpec.server.troubleshooting.advanced.groups.1.rows.0.control.options.${index}.label` as MessageId}));
type Facts = Readonly<{phase: 'loading' | 'ready' | 'error'; facts?: OwnerSupportFacts}>;
/** The owner's Support reads and the export, through the page's own lifetime (rebuilt routes in client-core support-next.ts). */
function createSupport(context: NativeShellBootstrapContext) {
  const lifetime = new NativeScreenLifetime(context), current = () => lifetime.current() && nativeStorageOwner(context);
  const api = {request: <T,>(path: string, method?: string, body?: unknown, caller?: AbortSignal): Promise<T> => current() ? lifetime.request(signal => context.api.request<T>(path, method, body, signal), caller) : Promise.reject(screenScopeEnded())};
  const exportZip = async (options: OwnerSupportOptions) => {
    if (!current()) throw screenScopeEnded();
    const ready = await prepareOwnerSupportExport(context.api, options, lifetime.signal);
    if (!current()) throw screenScopeEnded();
    return ready.blob;
  };
  return {context, api, current, exportZip, signal: lifetime.signal, dispose: () => lifetime.dispose()};
}
export type SupportGroup = ReturnType<typeof createSupport>;
export function useSupportGroup(ports: NativeSettingsLivePorts) {
  const context = ports.context, [group, setGroup] = useState<SupportGroup>();
  useLayoutEffect(() => {const next = createSupport(context); setGroup(next); return next.dispose;}, [context]);
  return group?.context === context ? group : undefined;
}
/** Support: the data folder's permissions, what this server can do (each check with the server's own sentence and path),
 * the parts the app skipped, and the support export, a ZIP saved through the system's save sheet (definition 12 rules 74, 76). */
export function useSupport(group: SupportGroup) {
  const t = defaultI18n.t;
  useSyncExternalStore(subscribeSkipped, skippedVersion, skippedVersion);
  const [savePort] = useState(() => supportZipSavePort());
  const [state, setState] = useState<Facts>({phase: 'loading'}), [fixing, setFixing] = useState(false), [fixed, setFixed] = useState(false);
  const [window, setWindow] = useState<number>(1440), [parts, setParts] = useState<readonly SupportComponent[]>(OWNER_SUPPORT_DEFAULT_COMPONENTS), [paths, setPaths] = useState(false);
  const [exporting, setExporting] = useState(false), [failed, setFailed] = useState(false);
  const load = useCallback(() => {
    setState(before => ({...before, phase: 'loading'}));
    void readOwnerSupport(group.api, group.signal).then(facts => {if (group.current()) setState({phase: 'ready', facts});}, () => {if (group.current()) setState(before => ({...before, phase: 'error'}));});
  }, [group]);
  useEffect(load, [load]);
  const fix = async () => {
    if (fixing || !group.current()) return; setFixing(true);
    try {await fixOwnerSupportPermissions(group.api, group.signal); if (group.current()) {setFixed(true); load();}}
    catch {if (group.current()) setState(before => ({...before, phase: 'error'}));}
    finally {if (group.current()) setFixing(false);}
  };
  // One action: the server makes the ZIP at once and nothing is kept there (rule 76); the save sheet takes it.
  const exportNow = async () => {
    if (exporting || !savePort || !parts.length || !group.current()) return;
    setExporting(true); setFailed(false);
    try {
      const zip = await group.exportZip({windowMinutes: window as OwnerSupportOptions['windowMinutes'], components: [...parts], includePaths: paths && parts.includes('snapshot')});
      await savePort(zip, group.signal, group.current);
    } catch {if (group.current()) setFailed(true);}
    finally {if (group.current()) {setExporting(false); setPaths(false);}}
  };
  const permissions = state.facts?.permissions, skipped = skippedParts();
  return {state, load, fixing, fixed, exporting, failed, canExport: !!savePort, paths, setPaths, parts,
    fix: () => {void fix();}, exportNow: () => {void exportNow();},
    permissions: permissions?.readableByOthers ? {body: permissions.fixable ? t('web.maintenance.permissionsBody') : permissions.message ?? t('web.maintenance.permissionsBody'), fixable: permissions.fixable} : undefined,
    permissionsFixed: fixed && !permissions?.readableByOthers,
    capabilities: state.facts?.capabilities.map(capability => {const word = SUPPORT_CAPABILITY_STATES[capability.state]; return {key: `${capability.area}/${capability.name}/${capability.path ?? ''}`,
      name: supportCapabilityName(capability, t), state: word ? {label: t(word.label), tone: word.tone} : undefined,
      lines: [capability.reason, capability.detail].filter((line): line is string => !!line), path: capability.path || undefined};}),
    skipped: skipped.map(part => ({key: part.document + ':' + part.part, title: part.document, detail: `${part.part} · ${t('settings.server.skippedPartsCount', {count: part.count})}`})),
    clearSkipped: () => {if (group.current()) resetSkippedParts();},
    window: {value: String(window), options: WINDOWS.map(entry => ({value: String(entry.minutes), label: t(entry.label)})),
      onValueChange: (value: string) => {const found = WINDOWS.find(entry => String(entry.minutes) === value); if (found) setWindow(found.minutes);}},
    components: SUPPORT_COMPONENTS.map(component => ({key: component, label: t(PARTS[component]), checked: parts.includes(component),
      onCheckedChange: (checked: boolean) => {setParts(current => checked ? [...new Set([...current, component])] : current.filter(part => part !== component)); if (component === 'snapshot' && !checked) setPaths(false);}}))};
}
