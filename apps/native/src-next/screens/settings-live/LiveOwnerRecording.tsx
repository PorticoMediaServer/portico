import React, {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {recordingAllowed, recordingGuideRoute, recordingOwnerKey, recordingOwnerLabel, type RecordingGrant, type RecordingOwner, type RecordingOwnerChoice, type RecordingSeries} from '../../../../../packages/client-core/src/server-admin/recording-policy';
import {tunerStatus, tunerUseLine} from '../../../../../packages/client-core/src/server-admin/live-words';
import type {RecordingStorage, StoragePolicy} from '../../../../../packages/client-core/src/dvr';
import {Button, Focusable, Select, Switch, TextField} from '../../kit/controls';
import {Dialog, Menu} from '../../kit/overlays';
import {StateView} from '../../kit/states';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeLiveOwnerController} from './live-owner-controller';
import type {NativeRecordingsController} from './recordings-controller';
import {appendRecordingPage, keepPolicyFor, needsRuleOwner, rulePaddingChoice, rulePaddingFromChoice} from './recording-owner-model';
import type {AdminRule, DVRDefaults, DVRDocument, TunerAllocation, TunerView} from './recording-owner-model';
import type {LiveSource} from './live-owner-model';
import type {KeepPolicy} from './recording-owner-model';
import {NativeAdvancedSection} from './server/AdvancedSection';

const paddingChoices = [0, 60, 120, 300, 600, 900];
const keepModes: readonly DVRDefaults['keepPolicy']['mode'][] = ['keep-all', 'keep-count', 'keep-days'];

/** Recording › tuners, who may record, standing rules, defaults and space. */
export function NativeRecordingOwner({controller, recordings, runtime, sources}: {controller: NativeLiveOwnerController; recordings: NativeRecordingsController; runtime: NativeShellRuntime; sources: readonly LiveSource[]}) {
  const theme = useTheme(), t = runtime.text.t;
  const routeRevision = controller.current() ? runtime.navigation?.getSnapshot().revision : undefined;
  const visible = controller.current() && runtime.getSnapshot().contentVisible === true && controller.current();
  const navigation = runtime.navigation;
  const facts = useMemo(() => ({controller, recordings, runtime, navigation, routeRevision, visible}), [controller, recordings, runtime, navigation, routeRevision, visible]);
  type Admission = {facts: typeof facts; live: boolean};
  const admissionRef = useRef<Admission | undefined>(undefined), [admission, setAdmission] = useState<Admission>();
  useLayoutEffect(() => {
    const opening: Admission = {facts, live: true}; admissionRef.current = opening; setAdmission(opening);
    return () => {opening.live = false; if (admissionRef.current === opening) admissionRef.current = undefined;};
  }, [facts]);
  const active = () => {
    const opening = admission;
    const exact = () => !!opening && opening.live && admissionRef.current === opening && opening.facts === facts;
    if (!exact() || !facts.visible || facts.routeRevision === undefined) return false;
    const owned = () => exact() && !controller.lifetime.signal.aborted && controller.current() && !controller.lifetime.signal.aborted && exact();
    if (!owned()) return false;
    if (facts.navigation?.getSnapshot().revision !== facts.routeRevision || !owned()) return false;
    if (runtime.getSnapshot().contentVisible !== true || !owned()) return false;
    return true;
  };
  const [dataAdmission, setDataAdmission] = useState<Admission>();
  const [tuners, setTuners] = useState<TunerView>(), [policy, setPolicy] = useState<Readonly<{owners: readonly RecordingOwnerChoice[]; grants: readonly RecordingGrant[]}>>();
  const [rules, setRules] = useState<readonly AdminRule[]>(), [settings, setSettings] = useState<DVRDocument>(), [storage, setStorage] = useState<RecordingStorage>();
  const [ownerCursor, setOwnerCursor] = useState(''), [ruleCursor, setRuleCursor] = useState(''), [pageBusy, setPageBusy] = useState(false), [pageError, setPageError] = useState<'owners' | 'rules'>();
  type ReadChain = {admission: Admission; retired: boolean};
  const [chain, setChain] = useState<ReadChain>();
  const chainRef = useRef<ReadChain | undefined>(undefined), headFlight = useRef<object | undefined>(undefined);
  const operationRef = useRef<AbortController | undefined>(undefined);
  const pageFlight = useRef<object | undefined>(undefined), ownerSeen = useRef(new Set<string>()), ruleSeen = useRef(new Set<string>()), pageEpoch = useRef(0);
  const [error, setError] = useState<unknown>(), [busy, setBusy] = useState(''), [ruleMenu, setRuleMenu] = useState<AdminRule>(), [newRule, setNewRule] = useState(false);
  const newRuleRef = useRef<object | undefined>(undefined), [newRuleOpening, setNewRuleOpening] = useState<object>();
  const currentSheet = () => !!newRuleOpening && newRuleRef.current === newRuleOpening && active() && newRuleRef.current === newRuleOpening;
  // One rule-removal confirmation, exactly the rule it was drawn for; the flight keeps that same
  // opening so a delayed close or a second press cannot reach a successor.
  type RuleRemoveOpening = Readonly<{token: number; rule: AdminRule; controller: NativeLiveOwnerController; revision: number; admission: Admission}>;
  const [ruleRemoveOpening, setRuleRemoveOpening] = useState<RuleRemoveOpening>();
  const ruleRemoveRef = useRef<RuleRemoveOpening | undefined>(undefined), ruleRemoveFlight = useRef<RuleRemoveOpening | undefined>(undefined), ruleRemoveToken = useRef(0), mounted = useRef(true);
  const ruleOwner = useRef(controller), ruleScopeRef = useRef<object | undefined>(undefined);
  useLayoutEffect(() => {mounted.current = true; return () => {mounted.current = false; ruleRemoveRef.current = undefined; ruleRemoveFlight.current = undefined;};}, []);
  const openRuleRemove = (rule: AdminRule) => {
    if (!active() || ruleRemoveFlight.current || !publishedRules.current?.includes(rule)) return;
    const revision = runtime.navigation?.getSnapshot().revision;
    if (revision === undefined || !active() || !admission || ruleRemoveFlight.current || !publishedRules.current?.includes(rule)) return;
    const next: RuleRemoveOpening = {token: ++ruleRemoveToken.current, rule, controller, revision, admission};
    // A fresh confirmation starts with no stale busy display from an orphaned write.
    if (!ruleRemoveFlight.current) setBusy('');
    ruleRemoveRef.current = next; setRuleRemoveOpening(next);
  };
  const closeRuleRemove = (target: RuleRemoveOpening) => {if (ruleRemoveRef.current !== target) return; ruleRemoveRef.current = undefined; setRuleRemoveOpening(current => current === target ? undefined : current);};
  useLayoutEffect(() => {const token = {}; if(chainRef.current) chainRef.current.retired=true; chainRef.current=undefined; headFlight.current=undefined; pageFlight.current=undefined; ruleScopeRef.current = token; ruleOwner.current = facts.controller; ruleRemoveRef.current = undefined; ruleRemoveFlight.current = undefined; return () => {if (ruleScopeRef.current === token) {ruleScopeRef.current = undefined; if(chainRef.current)chainRef.current.retired=true; chainRef.current=undefined;headFlight.current=undefined;pageFlight.current=undefined;ruleRemoveRef.current = undefined; ruleRemoveFlight.current = undefined; operationRef.current?.abort();operationRef.current = undefined; newRuleRef.current = undefined;}};}, [facts]);
  const ruleRevision = routeRevision;

  const shownRuleRemove = ruleRemoveOpening && ruleRemoveRef.current === ruleRemoveOpening && ruleRemoveOpening.controller === controller && ruleRemoveOpening.revision === ruleRevision ? ruleRemoveOpening : undefined;
  const [permissionDraft, setPermissionDraft] = useState<Readonly<Record<string, Readonly<{enabled: boolean}>>>>({});
  const [defaultsDraft, setDefaultsDraft] = useState<DVRDefaults>(), [storageDraft, setStorageDraft] = useState<StoragePolicy>();
  const publishedRules = useRef<readonly AdminRule[] | undefined>(undefined);
  useLayoutEffect(() => {publishedRules.current = rules; return () => {if (publishedRules.current === rules) publishedRules.current = undefined;};}, [rules, facts]);
  const ruleOpeners = useRef(new Map<string, View>()), ruleMenuOpener = useRef<View | null>(null), confirmCancel = useRef<View>(null);
  const reload = async (initial = false) => {
    if (!active() || headFlight.current || !initial && chainRef.current !== chain) return;
    const head = {}, epoch = ++pageEpoch.current;
    if(chainRef.current)chainRef.current.retired=true;
    chainRef.current=undefined;headFlight.current=head;pageFlight.current=undefined;
    newRuleRef.current=undefined;ruleRemoveRef.current=undefined;setNewRule(false);setNewRuleOpening(undefined);setRuleMenu(undefined);
    setChain(undefined);setPageBusy(true);setPageError(undefined);setError(undefined);
    const local=()=>headFlight.current===head&&pageEpoch.current===epoch;
    const current=()=>local()&&active()&&local();
    try {
      if (!current()) return;
      const [nextTuners,nextPolicy,nextRules,nextSettings,nextStorage]=await Promise.all([controller.tuners(),controller.policy(),controller.rules(),controller.dvrSettings(),recordings.storage()]);
      if (!current() || !admission) return;
      const issued:ReadChain={admission,retired:false};
      ownerSeen.current=new Set();ruleSeen.current=new Set();chainRef.current=issued;setChain(issued);
      setDataAdmission(admission);setTuners(nextTuners);setPolicy(nextPolicy);setRules(nextRules.items);
      setOwnerCursor(nextPolicy.nextCursor);setRuleCursor(nextRules.nextCursor);setSettings(nextSettings);setStorage(nextStorage);
      setDefaultsDraft(value=>value??nextSettings.settings);setStorageDraft(value=>value??nextStorage.policy);
    } catch(failure){if(current()){setDataAdmission(admission);setError(failure);}}
    finally{if(local()&&active()&&local()){headFlight.current=undefined;setPageBusy(false);}}
  };
  useEffect(()=>{if(!active())return;setBusy('');setError(undefined);setPolicy(undefined);setRules(undefined);setTuners(undefined);setSettings(undefined);setStorage(undefined);setPermissionDraft({});setDefaultsDraft(undefined);setStorageDraft(undefined);setRuleMenu(undefined);setNewRule(false);setNewRuleOpening(undefined);setOwnerCursor('');setRuleCursor('');void reload(true);return()=>{pageEpoch.current++;if(chainRef.current)chainRef.current.retired=true;chainRef.current=undefined;headFlight.current=undefined;pageFlight.current=undefined;};},[admission]); // eslint-disable-line react-hooks/exhaustive-deps
  const pendingEdits=!!(Object.keys(permissionDraft).length||(settings&&defaultsDraft&&JSON.stringify(defaultsDraft)!==JSON.stringify(settings.settings))||(storage&&storageDraft&&JSON.stringify(storageDraft)!==JSON.stringify(storage.policy)));
  const publication=useMemo(()=>chain?{chain,policy,rules,ownerCursor,ruleCursor,ownersSeen:ownerSeen.current,rulesSeen:ruleSeen.current}:undefined,[chain,policy,rules,ownerCursor,ruleCursor]);
  const publicationRef=useRef<typeof publication>(undefined);
  useLayoutEffect(()=>{publicationRef.current=publication;return()=>{if(publicationRef.current===publication)publicationRef.current=undefined;};},[publication]);
  const publicationCurrent=()=>{
    const local=()=>!!publication&&publicationRef.current===publication&&chainRef.current===publication.chain&&!publication.chain.retired&&!headFlight.current;
    return local()&&active()&&local();
  };
  const blocking=useMemo(()=>({pendingEdits,busy,ruleMenu,shownRuleRemove,newRule}),[pendingEdits,busy,ruleMenu,shownRuleRemove,newRule]);
  const blockingRef=useRef(blocking);useLayoutEffect(()=>{blockingRef.current=blocking;},[blocking]);
  const loadMore=async(kind:'owners'|'rules')=>{
    const offered=publication,cursor=kind==='owners'?offered?.ownerCursor:offered?.ruleCursor;
    const blocked=blockingRef.current;
    if(!publicationCurrent()||!offered||!cursor||pageFlight.current||operationRef.current||blocked.busy||blocked.pendingEdits||blocked.ruleMenu||blocked.shownRuleRemove||kind==='rules'&&blocked.newRule)return;
    const seen=kind==='owners'?offered.ownersSeen:offered.rulesSeen;if(seen.has(cursor))return;
    const flight={offered},epoch=pageEpoch.current;
    const local=()=>pageFlight.current===flight&&pageEpoch.current===epoch&&publicationRef.current===offered&&chainRef.current===offered.chain&&!offered.chain.retired&&!headFlight.current;
    const current=()=>local()&&active()&&local();
    if(!publicationCurrent())return;pageFlight.current=flight;setPageBusy(true);setPageError(undefined);
    try{
      if(!current())return;
      if(kind==='owners'){
        const page=await controller.policy(cursor);if(!current())return;
        if(!offered.policy||JSON.stringify(offered.policy.grants)!==JSON.stringify(page.grants))throw new Error('invalid_response');
        const owners=appendRecordingPage(offered.policy.owners,page.owners,cursor,page.nextCursor,seen,row=>recordingOwnerKey(row.owner));
        if(!current())return;seen.add(cursor);setPolicy({...offered.policy,owners});setOwnerCursor(page.nextCursor);
      }else{
        const page=await controller.rules(cursor);if(!current())return;
        if(!offered.rules)throw new Error('invalid_response');
        const rows=appendRecordingPage(offered.rules,page.items,cursor,page.nextCursor,seen,row=>row.id);
        if(!current())return;seen.add(cursor);setRules(rows);setRuleCursor(page.nextCursor);
      }
    }catch{if(current())setPageError(kind);}
    finally{if(local()&&active()&&local()){pageFlight.current=undefined;setPageBusy(false);}}
  };
  const write = async (key: string, work: (isCurrent: () => boolean, signal: AbortSignal) => Promise<unknown>, current: () => boolean = () => true) => {
    if (!publicationCurrent() || busy || pageFlight.current || operationRef.current) return;
    const flight = new AbortController(); operationRef.current = flight;
    const live = () => !flight.signal.aborted && operationRef.current === flight && active() && current() && active() && operationRef.current === flight && !flight.signal.aborted;
    if (!live()) {if (operationRef.current === flight) operationRef.current = undefined; return;}
    setBusy(key); setError(undefined);
    try {if (!live()) return; await work(live,flight.signal); if (live()) await reload();}
    catch (failure) {if (live()) setError(failure);}
    finally {if (operationRef.current === flight) {const stillHere = active() && operationRef.current === flight; if (operationRef.current === flight) operationRef.current = undefined; if (stillHere) setBusy('');}}
  };
  const saveAll = async () => {
    if (!publicationCurrent() || busy || pageFlight.current || operationRef.current || !policy) return;
    const flight = new AbortController(); operationRef.current = flight;
    const current = () => operationRef.current === flight && active() && operationRef.current === flight;
    if (!current()) {if (operationRef.current === flight) operationRef.current = undefined; return;}
    setBusy('save'); setError(undefined);
    try {
      for (const [key, change] of Object.entries(permissionDraft)) {
        const choice = policy.owners.find(value => recordingOwnerKey(value.owner) === key);
        if (!current()) return;
        if (choice) await controller.setPermission(choice.owner, change.enabled, choice.revision);
        if (!current()) return;
      }
      if (!current()) return;
      if (settings && defaultsDraft && JSON.stringify(defaultsDraft) !== JSON.stringify(settings.settings)) await controller.saveDvrSettings(settings.revision, defaultsDraft);
      if (!current()) return;
      if (storage && storageDraft && JSON.stringify(storageDraft) !== JSON.stringify(storage.policy)) await recordings.saveStorage(storageDraft);
      if (!current()) return;
      setPermissionDraft({}); setDefaultsDraft(undefined); setStorageDraft(undefined);
      await reload();
    } catch (failure) {if (current()) setError(failure);}
    finally {if (current()) {operationRef.current = undefined; setBusy('');} else if (operationRef.current === flight) operationRef.current = undefined;}
  };
  if (!admission || !admission.live || admission.facts !== facts || dataAdmission !== admission) return null;
  if (error !== undefined && !tuners && !policy) return <View style={{paddingHorizontal: 20, paddingTop: 20}}><StateView testID="live-owner-recording-error" {...runtime.error(error, () => void reload(), 'live')}/></View>;
  return <View style={{paddingHorizontal: 20, paddingTop: 18, gap: 26}}>
    {error !== undefined && <StateView testID="live-owner-recording-error" {...runtime.error(error, () => void reload(), 'live')}/>}
    <Section title={t('web.dvrSettings.tuners')} description={tuners ? t('web.dvrSettings.tunersLede', {hours: tuners.upcomingWindowHours}) : undefined}>
      {tuners?.sources.map(source => <TunerCard key={source.sourceId} source={source} runtime={runtime}/>)}
      {tuners && !tuners.sources.length && <Text style={[shellType(13), {color: theme.colors.textSecondary}]}>{t('web.dvrSettings.noTuners')}</Text>}
      {!tuners && error === undefined && <Text style={[shellType(13), {color: theme.colors.textTertiary}]}>{t('web.librarySettings.loading')}</Text>}
    </Section>
    {policy && <Permissions policy={policy} draft={permissionDraft} runtime={runtime} onChange={(owner, change) => publicationCurrent() && !pageFlight.current && setPermissionDraft(current => ({...current, [recordingOwnerKey(owner)]: change}))}/>}
    {pageError && <Button testID="live-owner-page-retry" focusId="live-owner-page-retry" disabled={pageBusy} onPress={() => void loadMore(pageError)}>{t('next.states.tryAgain')}</Button>}
    {!!ownerCursor && <Button testID="live-owner-owners-more" focusId="live-owner-owners-more" disabled={pageBusy || !!busy || pendingEdits} onPress={() => void loadMore('owners')}>{t('next.settings.loadMore')}</Button>}
    <Section title={t('web.recordingRules.title')} description={t('web.recordingRules.lede')} action={policy && <Button testID="live-owner-new-rule" icon="i-plus" size="small" disabled={pageBusy} onPress={() => {if (active()) {const opening = {}; newRuleRef.current = opening; setNewRuleOpening(opening); setNewRule(true);}}}>{t('web.recordingRules.newRule')}</Button>}>
      {rules?.map(rule => <Focusable key={rule.id} ref={node => {if (node) ruleOpeners.current.set(rule.id, node); else ruleOpeners.current.delete(rule.id);}} testID={`live-owner-rule-${rule.id}`} focusKind="row"
        onPress={() => {if (!publicationCurrent() || pageFlight.current) return; ruleMenuOpener.current = ruleOpeners.current.get(rule.id) ?? null; setRuleMenu(rule);}} style={{flexDirection: 'row', alignItems: 'center', gap: 12, backgroundColor: theme.colors.raised, borderRadius: 14, padding: 14}}>
        <View style={{flex: 1, gap: 4}}>
          <Text style={[shellType(15, 700), {color: theme.colors.text}]} numberOfLines={1}>{rule.name || rule.match}</Text>
          <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 3}}>{[needsRuleOwner(rule) ? t('web.recordingRules.chooseOwner') : t(rule.kind === 'title' ? 'web.recordingRules.titleMatch' : 'web.recordingRules.seriesKind'), rule.options.newEpisodesOnly ? t('web.recordingRules.newOnly') : '', rule.owner.authority ? ownerName(policy?.owners, rule.owner as RecordingOwner) ?? '' : ''].filter(Boolean).map((part, index) => <Text key={index} style={[shellType(12.5), {color: theme.colors.textSecondary}]} numberOfLines={2}>{part}</Text>)}</View>
        </View>
        <Text style={[shellType(12.5, 700), {color: rule.enabled ? theme.colors.healthy : theme.colors.textTertiary}]}>{rule.enabled ? t('recordings.on') : t('web.recordingRules.pausedRule')}</Text>
      </Focusable>)}
      {!!ruleCursor && <Button testID="live-owner-rules-more" focusId="live-owner-rules-more" disabled={pageBusy || !!busy || pendingEdits || newRule} onPress={() => void loadMore('rules')}>{t('next.settings.loadMore')}</Button>}
      {rules && !rules.length && <Text style={[shellType(13), {color: theme.colors.textSecondary}]}>{t('web.recordingRules.noRules')} {t('web.recordingRules.noRulesHelp')}</Text>}
    </Section>
    {settings && defaultsDraft && <Defaults document={settings} draft={defaultsDraft} runtime={runtime} onChange={value => {if (publicationCurrent() && !pageFlight.current) setDefaultsDraft(value);}}/>}
    {storage && storageDraft && <Storage storage={storage} draft={storageDraft} runtime={runtime} onChange={value => {if (publicationCurrent() && !pageFlight.current) setStorageDraft(value);}}/>}
    {settings && storage && <PendingBar count={Object.keys(permissionDraft).length + (JSON.stringify(defaultsDraft) !== JSON.stringify(settings.settings) ? 1 : 0) + (JSON.stringify(storageDraft) !== JSON.stringify(storage.policy) ? 1 : 0)}
      busy={busy === 'save' || pageBusy} onDiscard={() => {if(!publicationCurrent())return;setPermissionDraft({}); setDefaultsDraft(settings.settings); setStorageDraft(storage.policy);}}
      onSave={() => void saveAll()} runtime={runtime}/>}
    <Menu open={ruleMenu !== undefined} onOpenChange={value => {if (!value && active()) setRuleMenu(current => current === ruleMenu ? undefined : current);}} title={ruleMenu?.name} anchorRef={ruleMenuOpener} presentation="sheet" testID="live-owner-rule-menu" items={ruleMenu ? [
      ...(policy && !needsRuleOwner(ruleMenu) ? [{id: 'toggle', icon: ruleMenu.enabled ? 'i-pause' as const : 'i-play' as const, label: t(ruleMenu.enabled ? 'web.recordingRules.pauseRule' : 'web.recordingRules.resumeRule'), onSelect: () => void write(`rule:${ruleMenu.id}`, async (isCurrent,signal) => {await controller.saveRuleEnabled(ruleMenu, policy.grants, !ruleMenu.enabled,{signal,isCurrent}); if (!isCurrent()) return;}, () => publishedRules.current === rules && !!rules?.includes(ruleMenu))}] : []),
      {id: 'separator', separator: true as const},
      {id: 'remove', icon: 'i-trash' as const, label: t('web.recordingRules.remove'), destructive: true, onSelect: () => openRuleRemove(ruleMenu)},
    ] : []}/>
    {policy && <NewRuleSheet controller={controller} runtime={runtime} policy={policy} sources={sources} ownerCursor={ownerCursor} ownerLoading={pageBusy} ownerError={pageError === 'owners'} onMoreOwners={() => void loadMore('owners')} open={newRule} onOpenChange={value => {if (currentSheet() && !value) {newRuleRef.current = undefined; setNewRule(false); setNewRuleOpening(undefined);}}}
      onSaved={() => {if (currentSheet()) {newRuleRef.current = undefined; setNewRule(false); setNewRuleOpening(undefined); void reload();}}}/>}
    <Dialog open={shownRuleRemove !== undefined} onOpenChange={value => {if (!value && !ruleRemoveFlight.current && shownRuleRemove) closeRuleRemove(shownRuleRemove);}} title={t('web.recordingRules.removeTitle', {name: shownRuleRemove?.rule.name ?? ''})} description={t('web.recordingRules.removeBody')} testID="live-owner-rule-remove" initialFocusRef={confirmCancel}
      footer={<View style={{flexDirection: 'row', gap: 10}}><Button ref={confirmCancel} variant="glass" disabled={!!busy} onPress={() => {if (shownRuleRemove) closeRuleRemove(shownRuleRemove);}}>{t('action.cancel')}</Button><Button variant="glass" textStyle={{color: theme.colors.record}} style={{backgroundColor: `${theme.colors.record}29`}} disabled={!!busy} loading={busy.startsWith('rule')} onPress={() => {
        const opening = shownRuleRemove;
        if (!opening || ruleRemoveFlight.current) return;
        const current = () => {
          const exact = () => mounted.current && ruleRemoveRef.current === opening && opening.controller === controller
            && opening.admission === admission && opening.admission.live && admissionRef.current === opening.admission;
          const owned = () => exact() && !opening.controller.lifetime.signal.aborted && opening.controller.current()
            && !opening.controller.lifetime.signal.aborted && exact();
          if (!owned()) return false;
          if (runtime.navigation?.getSnapshot().revision !== opening.revision || !owned()) return false;
          if (runtime.getSnapshot().contentVisible !== true || !owned()) return false;
          return true;
        };
        if (!current()) return;
        ruleRemoveFlight.current = opening;
        void write(`rule:${opening.rule.id}`, async isCurrent => {await opening.controller.deleteRule(opening.rule); if (!isCurrent()) return;},
          () => ruleRemoveFlight.current === opening && current() && ruleRemoveFlight.current === opening).then(
          () => {if (mounted.current && opening.controller.current() && ruleRemoveRef.current === opening) closeRuleRemove(opening);},
          () => {}
        ).finally(() => {if (ruleRemoveFlight.current === opening) ruleRemoveFlight.current = undefined;});
      }}>{t('web.recordingRules.remove')}</Button></View>}>
      <View/>
    </Dialog>
  </View>;
}

function Section({title, description, children, action}: {title: string; description?: string; children: React.ReactNode; action?: React.ReactNode}) {
  const theme = useTheme();
  return <View style={{gap: 10}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}><Text style={[shellType(13, 700), {color: theme.colors.textTertiary, flex: 1}]}>{title}</Text>{action}</View>
    {description && <Text style={[shellType(13), {color: theme.colors.textSecondary, lineHeight: 20}]}>{description}</Text>}
    {children}
  </View>;
}
function ownerName(owners: readonly RecordingOwnerChoice[] | undefined, owner: RecordingOwner): string | undefined {
  const choice = owners?.find(value => recordingOwnerKey(value.owner) === recordingOwnerKey(owner));
  return choice ? recordingOwnerLabel(choice) : undefined;
}
function TunerCard({source, runtime}: {source: TunerAllocation; runtime: NativeShellRuntime}) {
  const theme = useTheme(), t = runtime.text.t, status = tunerStatus(t, source), uses = [...source.active, ...source.upcoming.slice(0, 6)];
  const when = (iso: string) => defaultI18n.formatDate(iso, {weekday: 'short', hour: 'numeric', minute: '2-digit'});
  return <View testID={`live-owner-tuner-${source.sourceId}`} style={{backgroundColor: theme.colors.raised, borderRadius: 14, padding: 14, gap: 8}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}>
      <Text style={[shellType(15, 700), {color: theme.colors.text, flex: 1}]} numberOfLines={1}>{source.sourceName}</Text>
      <Text style={[shellType(12.5, 700), {color: status.tone === 'danger' ? theme.colors.record : status.tone === 'warning' ? theme.colors.amber : theme.colors.healthy}]}>{status.text}</Text>
    </View>
    {uses.length ? uses.map((use, index) => <Text key={use.recordingId || index} style={[shellType(12.5), {color: theme.colors.textSecondary}]} numberOfLines={1}>{tunerUseLine(t, use, when)}</Text>)
      : <Text style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{t('web.dvrSettings.nothingScheduled')}</Text>}
  </View>;
}

/** Every profile the server lists, with its effective grant; an inherited profile reads as such.
 * A switch edits the page's draft; nothing is written until the bar's Save. */
/** Who can record: one Can record switch per profile, the same switch as Recordings in that
 * profile's settings. Nothing is inherited from the account. */
function Permissions({policy, draft, runtime, onChange}: {policy: Readonly<{owners: readonly RecordingOwnerChoice[]; grants: readonly RecordingGrant[]}>; draft: Readonly<Record<string, Readonly<{enabled: boolean}>>>; runtime: NativeShellRuntime; onChange: (owner: RecordingOwner, change: Readonly<{enabled: boolean}>) => void}) {
  const theme = useTheme(), t = runtime.text.t;
  const accounts = new Map<string, RecordingOwnerChoice[]>();
  for (const choice of policy.owners) {const key = JSON.stringify([choice.owner.authority, choice.owner.accountId]); accounts.set(key, [...(accounts.get(key) ?? []), choice]);}
  return <Section title={t('web.dvrSettings.whoCanRecord')} description={t('web.dvrSettings.whoCanRecordLede')}>
    {[...accounts.entries()].map(([accountId, profiles]) => <View key={accountId} style={{gap: 4, backgroundColor: theme.colors.raised, borderRadius: 14, padding: 10}}>
      <Text style={[shellType(12.5, 700), {color: theme.colors.textTertiary, paddingHorizontal: 6}]}>{profiles[0]!.accountName}</Text>
      {profiles.map(choice => {
        const key = recordingOwnerKey(choice.owner), enabled = draft[key]?.enabled ?? choice.canRecord;
        return <View key={key} style={{flexDirection: 'row', alignItems: 'center', gap: 12, paddingHorizontal: 6, minHeight: 44}}>
          <View style={{flex: 1}}>
            <Text style={[shellType(15, 600), {color: theme.colors.text}]} numberOfLines={1}>{choice.profileName}</Text>
            <Text style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{t(enabled ? 'next.liveOwner.canRecord' : 'next.liveOwner.cannotRecord')}</Text>
          </View>
          <Switch testID={`live-owner-permission-${choice.owner.profileId}`} accessibilityLabel={choice.profileName} checked={enabled}
            onCheckedChange={next => onChange(choice.owner, {enabled: next})}/>
        </View>;
      })}
    </View>)}
  </Section>;
}

function Defaults({draft, runtime, onChange}: {document: DVRDocument; draft: DVRDefaults; runtime: NativeShellRuntime; onChange: (value: DVRDefaults) => void}) {
  const theme = useTheme(), t = runtime.text.t, [advanced, setAdvanced] = useState(false);
  const patch = (value: Partial<DVRDefaults>) => onChange({...draft, ...value});
  const minutes = (ms: number | null) => String(Math.round((ms ?? 0) / 60000));
  return <Section title={t('web.dvrSettings.defaults')}>
    <View style={{gap: 12, backgroundColor: theme.colors.raised, borderRadius: 14, padding: 14}}>
      <Select testID="live-owner-padding-before" label={t('web.dvrSettings.startEarly')} description={t('web.dvrSettings.startEarlyHelp')} value={minutes(draft.prePaddingMs)} options={paddingChoices.map(value => ({value: minutes(value), label: value ? t('recordings.minutes', {count: value / 60}) : t('recordings.none')}))} onValueChange={value => patch({prePaddingMs: Number(value) * 60000})}/>
      <Select testID="live-owner-padding-after" label={t('web.dvrSettings.finishLate')} description={t('web.dvrSettings.finishLateHelp')} value={minutes(draft.postPaddingMs)} options={paddingChoices.map(value => ({value: minutes(value), label: value ? t('recordings.minutes', {count: value / 60}) : t('recordings.none')}))} onValueChange={value => patch({postPaddingMs: Number(value) * 60000})}/>
      <Select testID="live-owner-tuner-conflict" label={t('server.live.tunerConflict')} description={t('server.live.tunerConflictHelp')} value={draft.liveTunerConflict} options={[{value: 'keep-watching', label: t('server.live.tunerConflict.keep')}, {value: 'stop-watching', label: t('server.live.tunerConflict.stop')}]} onValueChange={liveTunerConflict => patch({liveTunerConflict})}/>
      <Select testID="live-owner-keep-mode" label={t('recordings.keepLabel')} value={draft.keepPolicy.mode} options={keepModes.map(mode => ({value: mode, label: t(mode === 'keep-all' ? 'web.logs.everything' : mode === 'keep-count' ? 'web.dvrSettings.keepNewest' : 'web.dvrSettings.keepDays')}))}
        onValueChange={mode => patch({keepPolicy: keepPolicyFor(draft.keepPolicy, mode as DVRDefaults['keepPolicy']['mode'])})}/>
      {draft.keepPolicy.mode === 'keep-count' && <Select testID="live-owner-keep-count" label={t('web.dvrSettings.howMany')} value={String(draft.keepPolicy.keepCount)} options={[1, 3, 5, 10, 20].map(value => ({value: String(value), label: String(value)}))} onValueChange={value => patch({keepPolicy: {...draft.keepPolicy, keepCount: Number(value)}})}/>}
      {draft.keepPolicy.mode === 'keep-days' && <Select testID="live-owner-keep-days" label={t('web.dvrSettings.howManyDays')} value={String(draft.keepPolicy.keepDays)} options={[7, 14, 30, 60, 90].map(value => ({value: String(value), label: String(value)}))} onValueChange={value => patch({keepPolicy: {...draft.keepPolicy, keepDays: Number(value)}})}/>}
    </View>
    <NativeAdvancedSection testID="live-owner-recording-advanced" open={advanced} onOpenChange={setAdvanced}>
      <View style={{gap: 12, backgroundColor: theme.colors.raised, borderRadius: 14, padding: 14}}>
        <TextField testID="live-owner-folder-shows" label={t('web.dvrSettings.folderShows')} value={draft.folderTemplate} maxLength={400} autoCapitalize="none" autoCorrect={false} onChangeText={folderTemplate => patch({folderTemplate})}/>
        <TextField testID="live-owner-folder-movies" label={t('web.dvrSettings.folderMovies')} value={draft.movieFolderTemplate} maxLength={400} autoCapitalize="none" autoCorrect={false} onChangeText={movieFolderTemplate => patch({movieFolderTemplate})}/>
      </View>
    </NativeAdvancedSection>
  </Section>;
}

function Storage({storage, draft, runtime, onChange}: {storage: RecordingStorage; draft: StoragePolicy; runtime: NativeShellRuntime; onChange: (value: StoragePolicy) => void}) {
  const theme = useTheme(), t = runtime.text.t;
  const gb = (bytes: number) => Math.round(bytes / 1e9);
  const total = storage.measurement.usedBytes + storage.measurement.freeBytes;
  const facts: readonly [string, string][] = [
    [t('server.live.usedByRecordings'), defaultI18n.bytes(storage.measurement.usedBytes)],
    [t('server.live.freeOnVolume'), defaultI18n.bytes(storage.measurement.freeBytes)],
    [t('web.liveTv.space.reserved'), defaultI18n.bytes(storage.measurement.reservedBytes)],
    [t('web.liveTv.space.waitingDelete'), defaultI18n.bytes(storage.pendingDeleteBytes)],
    [t('web.liveTv.space.forecast'), storage.forecastDescription || t('web.liveTv.space.forecastOver', {size: defaultI18n.bytes(storage.forecastBytes), hours: storage.forecastHours})],
  ];
  return <Section title={t('channels.recordings')} description={t('web.liveTv.recordingsLede')}>
    <View style={{backgroundColor: theme.colors.raised, borderRadius: 14, padding: 14, gap: 8}} testID="live-owner-storage">
      {facts.map(([label, value]) => <View key={label} style={{flexDirection: 'row', gap: 12}}><Text style={[shellType(13), {color: theme.colors.textTertiary, flex: 1}]}>{label}</Text><Text style={[shellType(13, 700), {color: theme.colors.text}]}>{value}</Text></View>)}
      <View style={{height: 6, borderRadius: 4, backgroundColor: theme.colors.line, overflow: 'hidden', marginTop: 6}}><View style={{height: '100%', width: `${total > 0 ? Math.min(100, storage.measurement.usedBytes / total * 100) : 0}%`, backgroundColor: theme.colors.accentStrong}}/></View>
      <Text style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{t('recordings.hoursLeft', {count: storage.forecastHours})}</Text>
      {!storage.measurement.writeHealthy && <Text style={[shellType(12.5), {color: theme.colors.amber}]}>{t('web.liveTv.notWritable')}</Text>}
    </View>
    <View style={{gap: 12, backgroundColor: theme.colors.raised, borderRadius: 14, padding: 14}}>
      <Select testID="live-owner-storage-retention" label={t('web.liveTv.keepRecordings')} description={t('web.liveTv.keepRecordingsHelp')} value={String(draft.retentionDays)} options={[0, 7, 14, 30, 60, 90, 365].map(value => ({value: String(value), label: String(value)}))} onValueChange={value => onChange({...draft, retentionDays: Number(value)})}/>
      <Select testID="live-owner-storage-episodes" label={t('web.liveTv.episodesPerSeries')} description={t('web.liveTv.episodesPerSeriesHelp')} value={String(draft.episodeLimit)} options={[0, 1, 3, 5, 10, 20, 50].map(value => ({value: String(value), label: String(value)}))} onValueChange={value => onChange({...draft, episodeLimit: Number(value)})}/>
      <Select testID="live-owner-storage-floor" label={t('web.liveTv.leaveFree')} description={t('web.liveTv.leaveFreeHelp')} value={String(gb(draft.floorBytes))} options={[0, 10, 20, 50, 100, 200].map(value => ({value: String(value), label: `${value} ${t('web.liveTv.gbUnit')}`}))} onValueChange={value => onChange({...draft, floorBytes: Number(value) * 1e9})}/>
      <Select testID="live-owner-storage-cap" label={t('web.liveTv.capUpTo')} description={t('web.liveTv.capUpToHelp')} value={String(gb(draft.capBytes))} options={[0, 100, 250, 500, 1000, 2000].map(value => ({value: String(value), label: `${value} ${t('web.liveTv.gbUnit')}`}))} onValueChange={value => onChange({...draft, capBytes: Number(value) * 1e9})}/>
    </View>
  </Section>;
}

/** Frame 16c: one bar counts what is waiting and applies it to the whole server. */
function PendingBar({count, busy, runtime, onDiscard, onSave}: {count: number; busy: boolean; runtime: NativeShellRuntime; onDiscard: () => void; onSave: () => void}) {
  const theme = useTheme(), t = runtime.text.t;
  if (!count) return null;
  return <View testID="live-owner-pending" style={{borderTopWidth: 1, borderTopColor: theme.colors.line, paddingTop: 12, gap: 10}}>
    <Text style={[shellType(13.5), {color: theme.colors.textSecondary}]}>{t('web.liveTv.changesWaiting', {count})}</Text>
    <View style={{flexDirection: 'row', gap: 10}}>
      <Button testID="live-owner-pending-discard" variant="glass" disabled={busy} onPress={onDiscard}>{t('action.discard')}</Button>
      <Button testID="live-owner-pending-save" loading={busy} onPress={onSave}>{t('action.save')}</Button>
    </View>
  </View>;
}

/** What the new-rule sheet's own data is read for: the drawn owner keys, the sheet guard and the
 * own state of one render, frozen once a layout pass commits it so neither a successor render nor
 * a render React throws away can change what a committed read follows. Only the four keys decide
 * when this owner is replaced. */
type OwnRead = Readonly<{controller: NativeLiveOwnerController; runtime: NativeShellRuntime; navigation: NativeShellRuntime['navigation']; revision: number | undefined; current: () => boolean; policy: Readonly<{owners: readonly RecordingOwnerChoice[]; grants: readonly RecordingGrant[]; nextCursor: string}> | undefined; sources: readonly LiveSource[] | undefined}>;
type OwnKeys = Readonly<Pick<OwnRead, 'controller' | 'runtime' | 'navigation' | 'revision'>>;
function ownRead(controller: NativeLiveOwnerController, runtime: NativeShellRuntime, navigation: NativeShellRuntime['navigation'], revision: number | undefined, current: () => boolean, policy: OwnRead['policy'], sources: readonly LiveSource[] | undefined): OwnRead {
  return Object.freeze({controller, runtime, navigation, revision, current, policy, sources});
}
function sameOwnOwner(left: OwnKeys | undefined, right: OwnKeys): boolean {
  return !!left && left.controller === right.controller && left.runtime === right.runtime && left.navigation === right.navigation && left.revision === right.revision;
}
/** The guide read's drawn witnesses: the sheet guard, the query key, and the day and source the
 * guide is listed for, frozen by the layout pass that commits the query. */
type GuideRead = Readonly<{controller: NativeLiveOwnerController; day: string; sourceId: string; query: object; current: () => boolean}>;
function guideRead(controller: NativeLiveOwnerController, day: string, sourceId: string, query: object, current: () => boolean): GuideRead {
  return Object.freeze({controller, day, sourceId, query, current});
}

/** Frame 17a: a new rule from a series the guide really lists, for a permitted profile. */
export function NewRuleSheet({controller, runtime, policy, sources, ownerCursor, ownerLoading, ownerError, onMoreOwners, open, onOpenChange, onSaved}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; policy?: Readonly<{owners: readonly RecordingOwnerChoice[]; grants: readonly RecordingGrant[]}>; sources?: readonly LiveSource[]; ownerCursor?: string; ownerLoading?: boolean; ownerError?: boolean; onMoreOwners?: () => void; open: boolean; onOpenChange: (open: boolean) => void; onSaved: () => void}) {
  const theme = useTheme(), t = runtime.text.t;
  const revision = controller.current() ? runtime.navigation?.getSnapshot().revision : undefined;
  const visible = controller.current() && runtime.getSnapshot().contentVisible === true && controller.current();
  const navigation = runtime.navigation;
  const sheetFacts = useMemo(() => ({controller, runtime, navigation, revision, visible, open}), [controller, runtime, navigation, revision, visible, open]);
  type SheetAdmission = {facts: typeof sheetFacts; live: boolean};
  const sheetFlight = useRef<AbortController | undefined>(undefined), ownFlight = useRef<object | undefined>(undefined);
  const sheetRef = useRef<SheetAdmission | undefined>(undefined), [sheet, setSheet] = useState<SheetAdmission>();
  useLayoutEffect(() => {
    const opening: SheetAdmission = {facts: sheetFacts, live: true}; sheetRef.current = opening; setSheet(opening);
    return () => {opening.live = false; if (sheetRef.current === opening) {sheetRef.current = undefined; sheetFlight.current?.abort();sheetFlight.current = undefined; ownFlight.current = undefined;}};
  }, [sheetFacts]);
  const sheetCurrent = useCallback(() => {
    const exact = () => !!sheet && sheet.live && sheetRef.current === sheet && sheet.facts === sheetFacts;
    if (!exact() || !sheetFacts.open || !sheetFacts.visible || sheetFacts.revision === undefined) return false;
    const owned = () => exact() && !controller.lifetime.signal.aborted && controller.current() && !controller.lifetime.signal.aborted && exact();
    if (!owned()) return false;
    if (sheetFacts.navigation?.getSnapshot().revision !== sheetFacts.revision || !owned()) return false;
    if (runtime.getSnapshot().contentVisible !== true || !owned()) return false;
    return true;
  }, [controller, runtime, sheet, sheetFacts]);
  const [ownPolicy, setOwnPolicy] = useState<Readonly<{owners: readonly RecordingOwnerChoice[]; grants: readonly RecordingGrant[]; nextCursor: string}>>(), [ownSources, setOwnSources] = useState<readonly LiveSource[]>();
  const [ownLoading, setOwnLoading] = useState(false), [ownError, setOwnError] = useState(false);
  const ownRevision = revision;
  const ownOwner = useRef<OwnKeys>({controller, runtime, navigation, revision});
  const ownEpoch = useRef(0), ownSeen = useRef(new Set<string>());
  // The committed own read witness. A layout pass runs only for a committed render, so the own
  // state of a render React throws away can never stand in front of this passive effect; the
  // effect copies the capture once and every settlement below follows that one copy.
  const ownCapture = useRef<OwnRead | undefined>(undefined);
  useLayoutEffect(() => {
    ownCapture.current = ownRead(controller, runtime, navigation, revision, sheetCurrent, ownPolicy, ownSources);
  }, [controller, runtime, navigation, revision, sheetCurrent, ownPolicy, ownSources]);
  useEffect(() => {
    const now = ownCapture.current!;
    const changedController = !sameOwnOwner(ownOwner.current, now);
    if (changedController) {ownOwner.current = now; ownEpoch.current++; ownSeen.current = new Set(); setOwnPolicy(undefined); setOwnSources(undefined);}
    if (now.current()) setOwnLoading(false);
    if (!now.current() || policy && sources || (!changedController && now.policy && now.sources)) return;
    let active = true; const epoch = ++ownEpoch.current; setOwnError(false);
    void Promise.all([controller.policy(), controller.load(true).then(() => controller.getSnapshot().list?.sources ?? [])]).then(([nextPolicy, nextSources]) => {if (active && ownEpoch.current === epoch && now.current()) {setOwnPolicy(nextPolicy); setOwnSources(nextSources);}}, () => {});
    return () => {active = false; if (ownEpoch.current === epoch) {ownEpoch.current += 1; ownFlight.current = undefined;}};
  }, [sheet, policy, sources, controller]);
  const effectivePolicy = policy ?? (ownOwner.current.controller === controller && ownOwner.current.runtime === runtime && ownOwner.current.navigation === runtime.navigation && ownOwner.current.revision === revision ? ownPolicy : undefined), effectiveSources = sources ?? (ownOwner.current.controller === controller && ownOwner.current.runtime === runtime && ownOwner.current.navigation === runtime.navigation && ownOwner.current.revision === revision ? ownSources : undefined) ?? [];
  const loadOwnOwners = async () => {
    const cursor = ownPolicy?.nextCursor;
    if (!sheetCurrent() || policy || !ownPolicy || !cursor || ownFlight.current || ownSeen.current.has(cursor) || ownRevision === undefined) return;
    const epoch = ownEpoch.current, revision = ownRevision;
    const current = () => ownFlight.current === flight && ownEpoch.current === epoch && sheetCurrent() && ownFlight.current === flight && runtime.navigation?.getSnapshot().revision === revision && sheetCurrent() && ownFlight.current === flight;
    const flight = {}; ownFlight.current = flight; setOwnLoading(true); setOwnError(false);
    try {
      if (!current()) return;
      const page = await controller.policy(cursor); if (!current()) return;
      if (JSON.stringify(ownPolicy.grants) !== JSON.stringify(page.grants)) throw new Error('invalid_response');
      const owners = appendRecordingPage(ownPolicy.owners, page.owners, cursor, page.nextCursor, ownSeen.current, row => recordingOwnerKey(row.owner));
      ownSeen.current.add(cursor); setOwnPolicy({...ownPolicy, owners, nextCursor: page.nextCursor});
    } catch {if (current()) setOwnError(true);}
    finally {if (ownFlight.current === flight) {ownFlight.current = undefined; if (ownEpoch.current === epoch) setOwnLoading(false);}}
  };
  const allowed = effectivePolicy ? effectivePolicy.owners.filter(value => recordingAllowed(value.owner, effectivePolicy.grants)) : [];
  const [name, setName] = useState(''), [owner, setOwner] = useState(''), [sourceId, setSourceId] = useState(''), [day, setDay] = useState(() => new Date().toISOString().slice(0, 10));
  const [seriesPage, setSeriesPage] = useState<Readonly<{query: object; items: readonly RecordingSeries[]}>>(), [anchor, setAnchor] = useState(''), [newOnly, setNewOnly] = useState(false);
  const [before, setBefore] = useState<number | null>(null), [after, setAfter] = useState<number | null>(null), [priority, setPriority] = useState('0'), [keepKey, setKeepKey] = useState('default');
  const [loading, setLoading] = useState(false), [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const cancel = useRef<View>(null);
  const guideQuery=useMemo(()=>({sheet,controller,day,sourceId,live:true}),[sheet,controller,day,sourceId]);
  const guideQueryRef=useRef<typeof guideQuery | undefined>(undefined);
  useLayoutEffect(()=>{guideQuery.live=true;guideQueryRef.current=guideQuery;return()=>{guideQuery.live=false;if(guideQueryRef.current===guideQuery)guideQueryRef.current=undefined;};},[guideQuery]);
  const guideCurrent=useCallback(()=>{const local=()=>guideQueryRef.current===guideQuery&&guideQuery.live;return local()&&sheetCurrent()&&local();},[guideQuery, sheetCurrent]);
  // The committed guide read witness: the same guard, day and source the drawn query lists, held
  // by the layout pass so only a committed render decides what the read follows.
  const guideCapture = useRef<GuideRead | undefined>(undefined);
  useLayoutEffect(() => {
    guideCapture.current = guideRead(controller, day, sourceId, guideQuery, guideCurrent);
  }, [controller, day, sourceId, guideQuery, guideCurrent]);
  const series=useMemo(()=>seriesPage?.query===guideQuery?seriesPage.items:[],[seriesPage,guideQuery]);
  useEffect(() => {
    if (!sheetCurrent()) return;
    setName(''); setOwner(allowed[0] ? recordingOwnerKey(allowed[0].owner) : ''); setSourceId(effectiveSources[0]?.id ?? ''); setDay(new Date().toISOString().slice(0, 10));
    setSeriesPage(undefined); setAnchor(''); setNewOnly(false); setBefore(null); setAfter(null); setPriority('0'); setKeepKey('default'); setError(undefined); setBusy(false);
  }, [sheet]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(()=>{
    const now = guideCapture.current!;
    if(!now.current()||!now.sourceId||!recordingGuideRoute(now.day,now.sourceId))return;
    let active=true;setLoading(true);setError(undefined);
    const current=()=>active&&now.current()&&active;
    if(current())void now.controller.guideSeries(now.day,now.sourceId).then(items=>{if(current()){setSeriesPage({query:now.query,items});setAnchor('');}},failure=>{if(current())setError(failure);}).finally(()=>{if(current())setLoading(false);});
    return()=>{active=false;};
  },[guideQuery]);
  const draftFacts = useMemo(() => ({sheet, effectivePolicy, name, owner, sourceId, day, series, anchor, newOnly, before, after, priority, keepKey}), [sheet, effectivePolicy, name, owner, sourceId, day, series, anchor, newOnly, before, after, priority, keepKey]);
  const draftRef = useRef<typeof draftFacts | undefined>(undefined);
  useLayoutEffect(() => {draftRef.current = draftFacts; return () => {if (draftRef.current === draftFacts) draftRef.current = undefined;};}, [draftFacts]);
  if (!sheet || !sheet.live || sheet.facts !== sheetFacts || !effectivePolicy) return null;
  const chosen = series.find(value => value.key === anchor), chosenOwner = allowed.find(value => recordingOwnerKey(value.owner) === owner);
  const kept: KeepPolicy | null = keepKey === 'default' ? null : keepKey === 'keep-all' ? {mode: 'keep-all', keepCount: 0, keepDays: 0, keepUntilWatched: false} : keepKey === 'keep-count' ? {mode: 'keep-count', keepCount: 10, keepDays: 0, keepUntilWatched: false} : {mode: 'keep-days', keepCount: 0, keepDays: 30, keepUntilWatched: false};
  const ready = !!chosenOwner && name.trim().length > 0 && !!chosen && recordingGuideRoute(day, sourceId) !== null;
  const save = async () => {
    if (!sheetCurrent() || draftRef.current !== draftFacts || !ready || !chosen || !chosenOwner || ownerLoading || ownLoading || sheetFlight.current) return;
    const flight = new AbortController(); sheetFlight.current = flight;
    const current = () => !flight.signal.aborted && sheetFlight.current === flight && draftRef.current === draftFacts && guideCurrent() && draftRef.current === draftFacts && sheetFlight.current === flight && !flight.signal.aborted;
    if (!current()) {if (sheetFlight.current === flight) sheetFlight.current = undefined; return;}
    setBusy(true); setError(undefined);
    try {
      if (!current()) return;
      await controller.createRule({owner: chosenOwner.owner, anchor: chosen.anchor, kind: chosen.seriesId ? 'series' : 'title', name: name.trim(), match: chosen.seriesId || chosen.title, sourceId: chosen.anchor.sourceId, enabled: chosen.recordAvailable,
        options: {prePaddingMs: before, postPaddingMs: after, keepPolicy: kept, newEpisodesOnly: newOnly, priority: Math.round(Number(priority) || 0), folderTemplate: ''}}, effectivePolicy!.grants,{signal:flight.signal,isCurrent:current});
      if (current()) onSaved();
    } catch (failure) {if (current()) setError(failure);}
    finally {if (sheetFlight.current === flight) {sheetFlight.current = undefined; if (sheetCurrent()) setBusy(false);}}
  };
  const paddingOptions = [{value: 'default', label: t('web.recordingRules.default')}, ...[0, 60, 120, 300, 600, 900].map(value => ({value: String(value), label: value ? t('recordings.minutes', {count: value / 60}) : t('recordings.none')}))];
  return <Dialog open={open} onOpenChange={value => {if (sheetCurrent()) onOpenChange(value);}} title={t('web.recordingRules.newTitle')} initialFocusRef={cancel} testID="live-owner-new-rule-sheet"
    footer={<View style={{flexDirection: 'row', gap: 10}}><Button ref={cancel} variant="glass" disabled={busy} onPress={() => {if (sheetCurrent()) onOpenChange(false);}}>{t('action.cancel')}</Button><Button variant="primary" loading={busy} disabled={!ready || ownerLoading || ownLoading} onPress={() => void save()}>{t('action.save')}</Button></View>}>
    <View style={{gap: 12}}>
      {error !== undefined && <StateView testID="live-owner-new-rule-error" {...runtime.error(error, () => setError(undefined), 'live')}/>}
      <TextField testID="live-owner-new-rule-name" label={t('web.recordingRules.ruleName')} value={name} maxLength={160} onChangeText={setName}/>
      <Select testID="live-owner-new-rule-owner" label={t('web.recordingRules.recordFor')} pickerContext={(policy ? ownerCursor : ownPolicy?.nextCursor) ? <Button testID="live-owner-picker-more" focusId="live-owner-picker-more" disabled={ownerLoading || ownLoading || busy} onPress={policy ? onMoreOwners : () => void loadOwnOwners()}>{t((policy ? ownerError : ownError) ? 'next.states.tryAgain' : 'next.settings.loadMore')}</Button> : undefined} value={owner} options={[{value: '', label: t('web.recordingRules.chooseProfile')}, ...allowed.map(value => ({value: recordingOwnerKey(value.owner), label: recordingOwnerLabel(value)}))]} onValueChange={setOwner}/>
      {!allowed.length && <Text style={[shellType(13), {color: theme.colors.amber}]}>{t('web.recordingRules.allowFirst')}</Text>}
      <Select testID="live-owner-new-rule-source" label={t('live.guideSource')} value={sourceId} options={effectiveSources.map(value => ({value: value.id, label: value.name}))} onValueChange={value => {if(!guideCurrent())return;setSourceId(value); setAnchor('');}}/>
      <TextField testID="live-owner-new-rule-day" label={t('web.recordingRules.guideDate')} value={day} maxLength={10} autoCapitalize="none" onChangeText={value => {if(!guideCurrent())return;setDay(value); setAnchor('');}}/>
      {!recordingGuideRoute(day, sourceId) && <Text style={[shellType(13), {color: theme.colors.amber}]}>{t('web.recordingRules.validDate')}</Text>}
      {loading && <Text style={[shellType(13), {color: theme.colors.textTertiary}]}>{t('web.recordingRules.readingGuide')}</Text>}
      <Select testID="live-owner-new-rule-series" label={t('web.recordingRules.seriesFromGuide')} value={anchor} options={[{value: '', label: t('web.recordingRules.chooseProgram')}, ...series.map(value => ({value: value.key, label: t('web.recordingRules.seriesOption', {title: value.title, channel: value.channelName, time: defaultI18n.time(value.startsAt), available: value.recordAvailable ? 'yes' : 'no'})}))]}
        onValueChange={value => {if(!guideCurrent())return;const picked = series.find(item => item.key === value); setAnchor(value); if (picked) {setName(current => current || picked.title); setNewOnly(false);}}}/>
      {!loading && !series.length && recordingGuideRoute(day, sourceId) && error === undefined && <Text style={[shellType(13), {color: theme.colors.textSecondary}]}>{t('web.recordingRules.noSeries')}</Text>}
      {chosen && !chosen.recordAvailable && <Text style={[shellType(13), {color: theme.colors.amber}]}>{t('web.recordingRules.captureUnavailable')}</Text>}
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}><Text style={[shellType(15, 600), {color: theme.colors.text, flex: 1}]}>{t('guide.newOnly')}</Text><Switch testID="live-owner-new-rule-new-only" accessibilityLabel={t('guide.newOnly')} checked={newOnly} onCheckedChange={setNewOnly}/></View>
      <Select testID="live-owner-new-rule-before" label={t('web.recordingRules.startEarlySeconds')} value={rulePaddingChoice(before)} options={paddingOptions} onValueChange={value => setBefore(rulePaddingFromChoice(value))}/>
      <Select testID="live-owner-new-rule-after" label={t('web.recordingRules.endLateSeconds')} value={rulePaddingChoice(after)} options={paddingOptions} onValueChange={value => setAfter(rulePaddingFromChoice(value))}/>
      <TextField testID="live-owner-new-rule-priority" label={t('web.recordingRules.priority')} description={t('web.recordingRules.priorityHelp')} keyboardType="number-pad" value={priority} onChangeText={setPriority}/>
      <Select testID="live-owner-new-rule-keep" label={t('recordings.keepLabel')} value={keepKey} options={[{value: 'default', label: t('web.recordingRules.useDefaults')}, {value: 'keep-all', label: t('web.logs.everything')}, {value: 'keep-count', label: t('web.recordingRules.latestFew')}, {value: 'keep-days', label: t('web.dvrSettings.keepDays')}]} onValueChange={setKeepKey}/>
    </View>
  </Dialog>;
}
