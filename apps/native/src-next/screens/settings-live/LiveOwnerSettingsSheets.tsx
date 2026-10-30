import React, {useEffect, useLayoutEffect, useRef, useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {guideOnlyPatch} from '../../../../../packages/client-core/src/server-admin/panel-logic';
import {liveSavedSettingsId} from '../../../../../packages/client-core/src/server-admin/live-words';
import type {MessageId} from '../../../../../packages/i18n/src';
import {Button, Focusable, Segmented, Select, Switch, TextField} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {ChannelMark} from '../../kit/artwork';
import {Icon} from '../../kit/Icon';
import {LoadingBoundary, SkeletonBlock, StateView} from '../../kit/states';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeLiveOwnerController} from './live-owner-controller';
import type {ChannelMapEdit, ChannelMapPage, FilterRule, LiveSource, LiveSourceDocument, LiveSourceSettings} from './live-owner-model';
import {listOf, megabytes, millisOf, secondsOf} from './live-owner-model';

import {lanConfirmationRoots} from '../../../../../packages/client-core/src/linear-api';

type Draft = {
 enabled?: boolean; viewerAccess?: 'owner-only' | 'server-members'; remote?: LiveSourceSettings['remote'];
  kind: string; enabledGroups: string[]; enabledChannels: string[]; streamBufferMs: number | null; retryWindowMs: number | null; userAgent: string;
  filters: {categories: FilterRule; countries: FilterRule; keywords: FilterRule}; logoImport: {enabled: boolean; overwriteExisting: boolean};
  channelNumbering: {mode: 'source' | 'sequential'; startAt: number; step: number}; guideSourceId: string;
  importLimits: {bytes: number; channels: number; programmes: number} | null;
};
const draftOf = (settings: LiveSourceSettings): Draft => ({
 ...settings,
  kind: settings.kind, enabledGroups: [...settings.enabledGroups], enabledChannels: [...settings.enabledChannels],
  streamBufferMs: settings.streamBufferMs, retryWindowMs: settings.retryWindowMs, userAgent: settings.userAgent,
  filters: {categories: {...settings.filters.categories, values: [...settings.filters.categories.values]}, countries: {...settings.filters.countries, values: [...settings.filters.countries.values]}, keywords: {...settings.filters.keywords, values: [...settings.filters.keywords.values]}},
  logoImport: {...settings.logoImport}, channelNumbering: {...settings.channelNumbering}, guideSourceId: settings.guideSourceId,
  importLimits: settings.importLimits ? {...settings.importLimits} : null,
});
const settingsOf = (draft: Draft): LiveSourceSettings => ({
 ...draft,
  kind: draft.kind, enabledGroups: draft.enabledGroups, enabledChannels: draft.enabledChannels, streamBufferMs: draft.streamBufferMs,
  retryWindowMs: draft.retryWindowMs, userAgent: draft.userAgent,
  filters: {categories: {mode: draft.filters.categories.mode, values: draft.filters.categories.values}, countries: {mode: draft.filters.countries.mode, values: draft.filters.countries.values}, keywords: {mode: draft.filters.keywords.mode, values: draft.filters.keywords.values}},
  logoImport: draft.logoImport, channelNumbering: draft.channelNumbering, guideSourceId: draft.guideSourceId, importLimits: draft.importLimits,
});
const kindKey: Record<string, string> = {playlist: 'server.live.sourceKind.playlist', 'xmltv-guide': 'server.live.sourceKind.guide', hdhomerun: 'server.live.sourceKind.tuner'};
const filterKeys = ['categories', 'countries', 'keywords'] as const;

function Section({title, description, children}: {title: string; description?: string; children: React.ReactNode}) {
  const theme = useTheme();
  return <View style={{gap: 10}}>
    <Text style={[shellType(13, 700), {color: theme.colors.textTertiary}]}>{title}</Text>
    {description && <Text style={[shellType(13), {color: theme.colors.textSecondary}]}>{description}</Text>}
    {children}
  </View>;
}
function Row({label, description, children}: {label: string; description?: string; children?: React.ReactNode}) {
  const theme = useTheme();
  return <View style={{gap: 3}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, minHeight: 30}}>
      <Text style={[shellType(15, 600), {color: theme.colors.text, flex: 1}]}>{label}</Text>
      {children}
    </View>
    {description && <Text style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{description}</Text>}
  </View>;
}

/** One source's own settings: what it is, which channels are wanted, how they are numbered,
 * whether logos are brought in, its buffer and retry times, and its own import limits. */
export function SourceSettingsSheet({controller, runtime, source, sources, open, onOpenChange, onSaved}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; source?: LiveSource; sources: readonly LiveSource[]; open: boolean; onOpenChange: (open: boolean) => void; onSaved: () => void}) {
  const theme = useTheme(), t = runtime.text.t;
  const [document, setDocument] = useState<LiveSourceDocument>(), [draft, setDraft] = useState<Draft>(), [error, setError] = useState<unknown>(), [busy, setBusy] = useState(false), [logo, setLogo] = useState(''), [saved, setSaved] = useState<MessageId>();
  const [guideAddress, setGuideAddress] = useState(false);
  const [choosing, setChoosing] = useState<'groups' | 'channels'>();
  const [loading, setLoading] = useState(false);
  const cancel = useRef<View>(null), openingRef = useRef<Readonly<{sourceId: string}> | undefined>(undefined), documentRef = useRef<LiveSourceDocument | undefined>(undefined);
  // The opening ticket is published in layout state: every render's Save closes over the exact
  // ticket it rendered under, and a press may never borrow the ticket that is open now.
  const [opening, setOpening] = useState<Readonly<{sourceId: string}>>();
  documentRef.current = document;
  useLayoutEffect(() => {
    if (!open || !source) return;
    const ticket = Object.freeze({sourceId: source.id});
    openingRef.current = ticket;
    setOpening(ticket);
    setGuideAddress(false); setDocument(undefined); setDraft(undefined); setError(undefined); setBusy(false); setLogo(''); setSaved(undefined); setLoading(true);
    let active = true;
    const live = () => active && openingRef.current === ticket;
    void controller.document(source.id).then(value => {
      if (!live()) return;
      setDocument(value); setDraft(draftOf(value.settings));
    }, failure => {if (live()) setError(failure);}).finally(() => {if (live()) setLoading(false);});
    return () => {active = false; if (openingRef.current === ticket) openingRef.current = undefined; setOpening(current => current === ticket ? undefined : current);};
  }, [open, source, controller]);
  const dirty = !!document && !!draft && JSON.stringify(draft) !== JSON.stringify(draftOf(document.settings));
  const patch = (value: Partial<Draft>) => {setSaved(undefined); setDraft(current => current ? {...current, ...value} : current);};
  const patchFilter = (key: typeof filterKeys[number], value: Partial<FilterRule>) => {setSaved(undefined); setDraft(current => current ? {...current, filters: {...current.filters, [key]: {...current.filters[key], ...value}}} : current);};
  const save = async () => {
    // The ticket is the one this render rendered under, never acquired from the live opening.
    if (!document || !draft || !source || !opening) return;
    const ticket = opening, sourceId = document.sourceId, revision = document.revision, acquisition = document.acquisition;
    if (openingRef.current !== ticket || ticket.sourceId !== sourceId) return;
    setBusy(true); setError(undefined); setSaved(undefined);
    const result = await controller.saveDocument({sourceId, revision, settings: document.settings}, settingsOf(draft));
    // The accepted command still belongs to the opening and document it was sent for.
    const live = documentRef.current;
    if (openingRef.current !== ticket || live?.sourceId !== sourceId || live.revision !== revision) return;
    if (!result) {setBusy(false);setError(controller.getSnapshot().error);}
    if (result) {const fresh=await controller.document(sourceId).catch(failure=>{if(openingRef.current===ticket)setError(failure);return undefined;});if(openingRef.current!==ticket)return;if(fresh){setDocument(fresh);setDraft(draftOf(fresh.settings));}setBusy(false);setSaved(liveSavedSettingsId(acquisition)); onSaved();}
  };
  const importLogos = async () => {
    if (!source || !document) return;
    setBusy(true); setError(undefined);
    const result = await controller.importLogos(source.id);
    setBusy(false);
    if (result) setLogo(t('web.liveSource.logoResult', {imported: result.imported, skipped: result.skipped, requested: result.requested}));
  };
  const guideOnly = draft?.kind === 'xmltv-guide';
  const guides = sources.filter(value => value.id !== source?.id);
  const footer = <View style={{flexDirection: 'row', gap: 10}}>
    <Button ref={cancel} variant="glass" disabled={busy} onPress={() => onOpenChange(false)}>{t('action.close')}</Button>
    <Button variant="primary" loading={busy} disabled={!dirty} onPress={() => void save()}>{t('action.save')}</Button>
  </View>;
  return <>
    <Dialog open={open} onOpenChange={value => onOpenChange(value)} title={source?.name ?? t('web.liveTv.sourceSettings')} initialFocusRef={cancel} footer={footer} testID="live-owner-source-settings">
      <View style={{gap: 22}}>
        {error !== undefined && <StateView testID="live-owner-source-settings-error" {...runtime.error(error, () => onOpenChange(false), 'live')}/>}
        <LoadingBoundary ready={!loading || !!(draft && document)} immediate={!loading || !!(draft && document)} skeleton={<View style={{gap: 10}}><SkeletonBlock height={48} radius={10}/><SkeletonBlock height={48} radius={10}/><SkeletonBlock height={48} radius={10}/></View>}>
        {draft && document && <>
          {draft.enabled !== undefined && <Row label={t('next.settingsSpec.server.live.groups.2.rows.0.label')}><Switch testID="live-owner-source-enabled" checked={draft.enabled} disabled={busy} onCheckedChange={enabled=>patch({enabled})}/></Row>}
          {draft.viewerAccess && <Select testID="live-owner-source-access" label={t('next.settingsSpec.server.live.groups.2.rows.1.label')} value={draft.viewerAccess} disabled={busy} options={['server-members','owner-only'].map((value,index)=>({value,label:t(`next.settingsSpec.server.live.groups.2.rows.1.control.options.${index}.label` as MessageId)}))} onValueChange={viewerAccess=>patch({viewerAccess:viewerAccess as 'owner-only'|'server-members'})}/>}

          <Section title={t('web.liveSource.provides')}>
            <Select testID="live-owner-provides" label={t('web.liveSource.provides')} value={draft.kind} options={document.availableKinds.map(kind => ({value: kind, label: t((kindKey[kind] ?? kind) as Parameters<typeof t>[0])}))}
              onValueChange={kind => patch({...guideOnlyPatch(kind)})}/>
            {guideOnly && <Text style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{t('web.liveSource.guideOnlyNote')}</Text>}
          </Section>
          {!guideOnly && <>
            <Section title={t('next.settingsSpec.server.live.groups.2.rows.2.label')} description={t('next.settingsSpec.server.live.groups.2.rows.2.help')}>
              <Select testID="live-owner-guide-from" label={t('next.settingsSpec.server.live.groups.2.rows.2.label')} value={draft.guideSourceId || (guideAddress || draft.remote?.guideUrl || draft.remote?.guideUrlRedacted ? 'url' : 'own')} options={[{value: 'own', label: t('next.settingsSpec.server.live.groups.2.rows.2.control.options.0.label')}, ...guides.map(value => ({value: value.id, label: value.name})),...(draft.remote?[{value:'url',label:t('next.settingsSpec.server.live.groups.2.rows.2.control.options.2.label')}]:[])]}
                onValueChange={value => {setGuideAddress(value==='url');patch({guideSourceId:value==='own'||value==='url'?'':value,...value==='own'&&draft.remote?{remote:{...draft.remote,guideUrl:'',guideUrlRedacted:false}}:{}});}}/>
              {draft.remote&&(guideAddress||draft.remote.guideUrl||draft.remote.guideUrlRedacted)&&<TextField testID="live-owner-source-guide-url" label={t('next.settingsSpec.server.live.groups.2.rows.2.children.0.label')} value={draft.remote.guideUrl} editable={!busy} onChangeText={guideUrl=>patch({remote:{...draft.remote!,guideUrl,guideUrlRedacted:false}})}/>}
            </Section>
            {draft.remote && <Select testID="live-owner-source-refresh" label={t('next.settingsSpec.server.live.groups.2.rows.3.label')} value={String(draft.remote.refreshMs)} disabled={busy} options={[3600000,21600000,43200000,86400000].map((value,index)=>({value:String(value),label:t(`next.settingsSpec.server.live.groups.2.rows.3.control.options.${index}.label` as MessageId)})).concat([3600000,21600000,43200000,86400000].includes(draft.remote.refreshMs)?[]:[{value:String(draft.remote.refreshMs),label:t('next.settingsSpec.server.live.groups.1.rows.0.control.options.3.label')}])} onValueChange={value=>patch({remote:{...draft.remote!,refreshMs:Number(value)}})}/>}
            <Section title={t('server.live.onlyGroups')} description={t('server.live.pickGroupsHelp')}>
              <Row label={t('server.live.onlyGroups')} description={draft.enabledGroups.length ? t('server.live.chosenCount', {count: draft.enabledGroups.length}) : t('server.live.chosenAll')}>
                <Button variant="glass" size="small" onPress={() => setChoosing('groups')}>{t('server.live.choose')}</Button>
                {draft.enabledGroups.length > 0 && <Button variant="text" size="small" onPress={() => patch({enabledGroups: []})}>{t('server.live.chooseAll')}</Button>}
              </Row>
            </Section>
            <Section title={t('server.live.onlyChannels')} description={t('server.live.pickChannelsHelp')}>
              <Row label={t('server.live.onlyChannels')} description={draft.enabledChannels.length ? t('server.live.chosenCount', {count: draft.enabledChannels.length}) : t('server.live.chosenAll')}>
                <Button variant="glass" size="small" onPress={() => setChoosing('channels')}>{t('server.live.choose')}</Button>
                {draft.enabledChannels.length > 0 && <Button variant="text" size="small" onPress={() => patch({enabledChannels: []})}>{t('server.live.chooseAll')}</Button>}
              </Row>
            </Section>
            <Section title={t('web.liveSource.keepChannels')}>
              {filterKeys.map(key => <View key={key} style={{gap: 6}}>
                <TextField testID={`live-owner-filter-${key}`} label={t(`server.live.filter.${key}`)} value={draft.filters[key].values.join(', ')} onChangeText={value => patchFilter(key, {values: listOf(value)})}/>
                <Segmented testID={`live-owner-filter-mode-${key}`} accessibilityLabel={t('web.liveSource.filterMode', {filter: t(`server.live.filter.${key}`)})} value={draft.filters[key].mode}
                  items={[{value: 'include', label: t('web.liveSource.onlyThese')}, {value: 'exclude', label: t('web.liveSource.allButThese')}]} onValueChange={mode => patchFilter(key, {mode: mode as 'include' | 'exclude'})}/>
              </View>)}
            </Section>
            <Section title={t('web.liveSource.channelNumbers')}>
              <Segmented testID="live-owner-numbering" accessibilityLabel={t('web.liveSource.numbering')} value={draft.channelNumbering.mode}
                items={[{value: 'source', label: t('web.liveSource.asSent')}, {value: 'sequential', label: t('web.liveSource.inOrder')}]} onValueChange={mode => patch({channelNumbering: {...draft.channelNumbering, mode: mode as 'source' | 'sequential'}})}/>
              {draft.channelNumbering.mode === 'sequential' && <View style={{flexDirection: 'row', gap: 10}}>
                <TextField testID="live-owner-number-start" containerStyle={{flex: 1}} label={t('web.liveSource.startAt')} keyboardType="number-pad" value={String(draft.channelNumbering.startAt)} onChangeText={value => patch({channelNumbering: {...draft.channelNumbering, startAt: Math.max(1, Number(value) || 1)}})}/>
                <TextField testID="live-owner-number-step" containerStyle={{flex: 1}} label={t('web.liveSource.countBy')} keyboardType="number-pad" value={String(draft.channelNumbering.step)} onChangeText={value => patch({channelNumbering: {...draft.channelNumbering, step: Math.max(1, Number(value) || 1)}})}/>
              </View>}
            </Section>
            <Section title={t('web.liveSource.channelLogos')}>
              <Row label={t('web.liveSource.bringLogos')}><Switch testID="live-owner-logos" accessibilityLabel={t('web.liveSource.bringLogos')} checked={draft.logoImport.enabled} onCheckedChange={enabled => patch({logoImport: {...draft.logoImport, enabled}})}/></Row>
              <Row label={t('web.liveSource.replaceLogos')}><Switch testID="live-owner-logos-replace" accessibilityLabel={t('web.liveSource.replaceLogos')} checked={draft.logoImport.overwriteExisting} disabled={!draft.logoImport.enabled} onCheckedChange={overwriteExisting => patch({logoImport: {...draft.logoImport, overwriteExisting}})}/></Row>
              <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, flexWrap: 'wrap'}}>
                <Button variant="glass" size="small" icon="i-dl" loading={busy} disabled={dirty || !document.settings.logoImport.enabled} onPress={() => void importLogos()}>{t('web.liveSource.importLogos')}</Button>
                {logo ? <Text style={[shellType(12.5), {color: theme.colors.textSecondary}]}>{logo}</Text> : null}
              </View>
            </Section>
          {draft.remote && <>
            {lanConfirmationRoots(error)&&<Button disabled={busy} onPress={()=>{patch({remote:{...draft.remote!,confirmedLanRoots:lanConfirmationRoots(error)}});setError(undefined);}}>{t('web.liveSource.confirmGuideNetwork')}</Button>}
            {draft.remote.kind==='xtream' && (['username','password'] as const).map(key=><View key={key}><TextField testID={`live-owner-source-${key}`} label={t(key==='username'?'web.liveSource.replaceUsername':'web.liveSource.replacePassword')} secureTextEntry autoComplete="off" value={draft.remote?.[key]??''} editable={!busy} onChangeText={value=>patch({remote:{...draft.remote!,[key]:value || undefined}})}/><Button disabled={busy} variant="text" onPress={()=>patch({remote:{...draft.remote!,[key]:''}})}>{t(key==='username'?'web.liveSource.clearUsername':'web.liveSource.clearPassword')}</Button></View>)}
          </>}
            {draft.remote && <Select testID="live-owner-source-stream-limit" label={t('next.settingsSpec.server.live.advanced.groups.2.rows.0.label')} value={String(draft.remote.ownerLimit || 'auto')} disabled={busy} options={['auto',1,2,3,4,6,8,...(![0,1,2,3,4,6,8].includes(draft.remote.ownerLimit)?[draft.remote.ownerLimit]:[])].map((value,index)=>({value:String(value),label:index===0?t('next.settingsSpec.server.live.advanced.groups.2.rows.0.control.options.0.label'):String(value)}))} onValueChange={value=>patch({remote:{...draft.remote!,ownerLimit:value==='auto'?0:Number(value),useDiscoveredCapacity:value==='auto'&&draft.remote!.kind==='hdhomerun'}})}/>}
            <Section title={t('web.liveSource.streamBuffer')}>
              <View style={{flexDirection: 'row', gap: 10}}>
                <TextField testID="live-owner-buffer" containerStyle={{flex: 1}} label={t('web.liveSource.streamBuffer')} keyboardType="number-pad" placeholder={t('web.liveSource.serverDefault', {value: document.effective.streamBufferMs / 1000})} value={secondsOf(draft.streamBufferMs)} onChangeText={value => patch({streamBufferMs: millisOf(value)})}/>
                <TextField testID="live-owner-retry" containerStyle={{flex: 1}} label={t('web.liveSource.retryFor')} keyboardType="number-pad" placeholder={t('web.liveSource.serverDefault', {value: document.effective.retryWindowMs / 1000})} value={secondsOf(draft.retryWindowMs)} onChangeText={value => patch({retryWindowMs: millisOf(value)})}/>
              </View>
            </Section>
            <Section title={t('server.liveSource.limits')} description={t('server.liveSource.limitsHelp')}>
              <View style={{gap: 10}}>
                {(['channels', 'programmes', 'bytes'] as const).map(field => {
                  const own = draft.importLimits?.[field] ?? 0, scale = field === 'bytes' ? megabytes : 1, server = document.effective.importLimits[field];
                  const label = field === 'channels' ? t('server.live.limitChannels') : field === 'programmes' ? t('server.live.limitProgrammes') : `${t('server.live.limitSize')} (${t('settings.unit.mb')})`;
                  return <TextField key={field} testID={`live-owner-limit-${field}`} label={label} keyboardType="number-pad" placeholder={server ? t('server.liveSource.limitDefault', {value: Math.round(server / scale)}) : undefined}
                    value={own ? String(Math.round(own / scale)) : ''} onChangeText={raw => {
                      const next = {...(draft.importLimits ?? {bytes: 0, channels: 0, programmes: 0}), [field]: raw === '' ? 0 : Math.max(0, Math.round((Number(raw) || 0) * scale))};
                      patch({importLimits: next.bytes || next.channels || next.programmes ? next : null});
                    }}/>;
                })}
              </View>
            </Section>
          </>}
        </>}
        </LoadingBoundary>
        {saved && <Text testID="live-owner-source-saved" accessibilityLiveRegion="polite" style={[shellType(12.5), {color: theme.colors.textSecondary}]}>{t(saved)}</Text>}
      </View>
    </Dialog>
    <SourceChoiceSheet controller={controller} runtime={runtime} source={source} kind={choosing} open={choosing !== undefined} onOpenChange={value => {if (!value) setChoosing(undefined);}}
      selected={choosing === 'groups' ? draft?.enabledGroups ?? [] : draft?.enabledChannels ?? []} onChange={value => {if (choosing === 'groups') patch({enabledGroups: value}); else patch({enabledChannels: value});}}/>
  </>;
}

/** Pick some of the source's real groups or channels; nothing picked means all of them. */
function SourceChoiceSheet({controller, runtime, source, kind, open, onOpenChange, selected, onChange}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; source?: LiveSource; kind?: 'groups' | 'channels'; open: boolean; onOpenChange: (open: boolean) => void; selected: readonly string[]; onChange: (value: string[]) => void}) {
  const theme = useTheme(), t = runtime.text.t;
  const [items, setItems] = useState<readonly {id: string; name: string; channels: number; group: string}[]>([]), [total, setTotal] = useState(0), [cursor, setCursor] = useState(''), [query, setQuery] = useState(''), [loading, setLoading] = useState(false), [error, setError] = useState<unknown>();
  useEffect(() => {
    if (!open || !source || !kind) return;
    let active = true;
    setLoading(true); setError(undefined);
    const timer = setTimeout(() => {
      void controller.choices(source.id, kind, '', query).then(page => {
        if (!active) return;
        setItems(page.items); setTotal(page.total); setCursor(page.nextCursor);
      }, failure => {if (active) setError(failure);}).finally(() => {if (active) setLoading(false);});
    }, query ? 300 : 0);
    return () => {active = false; clearTimeout(timer);};
  }, [open, source, kind, query, controller]);
  const more = async () => {
    if (!source || !kind) return;
    setLoading(true);
    try {const page = await controller.choices(source.id, kind, cursor, query); setItems(current => [...current, ...page.items.filter(value => !current.some(existing => existing.id === value.id))]); setCursor(page.nextCursor);}
    catch (failure) {setError(failure);}
    finally {setLoading(false);}
  };
  const toggle = (id: string, on: boolean) => onChange(on ? [...selected, id] : selected.filter(value => value !== id));
  const gone = selected.filter(id => !items.some(value => value.id === id));
  const close = useRef<View>(null);
  return <Dialog open={open} onOpenChange={value => onOpenChange(value)} title={t(kind === 'groups' ? 'server.live.onlyGroups' : 'server.live.onlyChannels')} initialFocusRef={close} footer={<Button ref={close} variant="glass" onPress={() => onOpenChange(false)}>{t('action.close')}</Button>} testID="live-owner-source-choices">
    <View style={{gap: 12}}>
      <TextField testID="live-owner-choice-search" variant="search" leadingIcon="i-search" accessibilityLabel={t(kind === 'groups' ? 'server.live.searchGroups' : 'server.live.searchChannels')} placeholder={t(kind === 'groups' ? 'server.live.searchGroups' : 'server.live.searchChannels')} value={query} onChangeText={setQuery}/>
      {error !== undefined && <StateView testID="live-owner-choice-error" {...runtime.error(error, () => setQuery(value => value), 'live')}/>}
      <View style={{gap: 4}}>
        <LoadingBoundary ready={!loading || items.length > 0 || gone.length > 0} immediate={!loading || items.length > 0 || gone.length > 0} skeleton={<SkeletonBlock height={44} radius={10}/>}>
        <>
        {items.map(value => <Focusable key={value.id} testID={`live-owner-choice-${value.id}`} accessible accessibilityRole="switch" accessibilityLabel={value.name || value.id} accessibilityState={{checked: selected.includes(value.id)}} focusKind="row" onPress={() => toggle(value.id, !selected.includes(value.id))} style={{flexDirection: 'row', alignItems: 'center', gap: 12, minHeight: 44, borderRadius: 10, paddingHorizontal: 8}}>
          <Text style={[shellType(15), {color: theme.colors.text, flex: 1}]} numberOfLines={1}>{value.name || value.id}</Text>
          <Text style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{kind === 'groups' ? t('server.live.channelCount', {count: value.channels}) : value.group}</Text>
          <View pointerEvents="none" accessibilityElementsHidden importantForAccessibility="no-hide-descendants"><Switch testID={`live-owner-choice-switch-${value.id}`} accessible={false} focusable={false} checked={selected.includes(value.id)}/></View>
        </Focusable>)}
        {gone.map(id => <Focusable key={`gone:${id}`} accessible accessibilityRole="switch" accessibilityLabel={id} accessibilityState={{checked: true}} focusKind="row" onPress={() => toggle(id, false)} style={{flexDirection: 'row', alignItems: 'center', gap: 12, minHeight: 44, borderRadius: 10, paddingHorizontal: 8}}>
          <Text style={[shellType(15), {color: theme.colors.text, flex: 1}]} numberOfLines={1}>{id}</Text>
          <View pointerEvents="none" accessibilityElementsHidden importantForAccessibility="no-hide-descendants"><Switch accessible={false} focusable={false} checked/></View>
        </Focusable>)}
        {!loading && !items.length && !gone.length && error === undefined && <Text style={[shellType(13), {color: theme.colors.textTertiary}]}>{t('server.live.noMatches')}</Text>}
        </>
        </LoadingBoundary>
      </View>
      {cursor ? <Button variant="glass" size="small" loading={loading} onPress={() => void more()}>{t('action.showMore')}</Button> : null}
      <Text style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{total ? `${t('server.live.choiceTotal', {count: total})} ` : ''}{t(kind === 'groups' ? 'server.live.pickGroupsHelp' : 'server.live.pickChannelsHelp')}</Text>
    </View>
  </Dialog>;
}

/** Renumber, hide, and point individual channels at a guide listing. Only touched rows are sent. */
export function ChannelMapSheet({controller, runtime, source, open, onOpenChange}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime; source?: LiveSource; open: boolean; onOpenChange: (open: boolean) => void}) {
  const theme = useTheme(), t = runtime.text.t;
  const [page, setPage] = useState<ChannelMapPage>(), [edits, setEdits] = useState<Record<string, ChannelMapEdit>>({}), [filter, setFilter] = useState(''), [error, setError] = useState<unknown>(), [busy, setBusy] = useState(false), [loading, setLoading] = useState(false), [revision, setRevision] = useState(0);
  const close = useRef<View>(null);
  const load = async (id: string, cursor = '') => {
    setLoading(true); setError(undefined);
    try {
      const next = await controller.channelMap(id, cursor);
      setPage(current => cursor && current ? {...next, items: [...current.items, ...next.items]} : next);
    } catch (failure) {setError(failure);}
    finally {setLoading(false);}
  };
  useEffect(() => {
    if (!open || !source) return;
    setPage(undefined); setEdits({}); setFilter(''); setError(undefined); setRevision(0);
    void controller.document(source.id).then(value => setRevision(value.revision), failure => setError(failure));
    void load(source.id);
  }, [open, source]); // eslint-disable-line react-hooks/exhaustive-deps
  const value = (channelId: string, entry: ChannelMapPage['items'][number]): ChannelMapEdit => edits[channelId] ?? {number: entry.overridden ? entry.number : '', guideChannelId: entry.guideChannelId, hidden: entry.hidden};
  const edit = (entry: ChannelMapPage['items'][number], patch: Partial<ChannelMapEdit>) => setEdits(current => ({...current, [entry.channelId]: {...value(entry.channelId, entry), ...patch}}));
  const changed = Object.keys(edits).length;
  const shown = page ? (filter.trim() ? page.items.filter(entry => `${entry.name} ${entry.number} ${entry.group}`.toLowerCase().includes(filter.trim().toLowerCase())) : page.items) : [];
  const save = async () => {
    if (!source || !changed) return;
    setBusy(true); setError(undefined);
    try {
      await controller.saveChannelMap(source.id, revision, Object.entries(edits).map(([channelId, override]) => ({channelId, override})));
      setEdits({}); setBusy(false);
      const document = await controller.document(source.id); setRevision(document.revision); await load(source.id);
    } catch (failure) {
      const partial = failure as {saved?: number; total?: number};
      setBusy(false); setError(partial.saved !== undefined ? Object.assign(new Error('partial'), {saved: partial.saved, total: partial.total}) : failure);
      if (source) void load(source.id);
    }
  };
  const footer = <View style={{flexDirection: 'row', gap: 10}}>
    <Button ref={close} variant="glass" disabled={busy} onPress={() => onOpenChange(false)}>{t('action.close')}</Button>
    <Button variant="primary" loading={busy} disabled={!changed} onPress={() => void save()}>{changed ? t('web.liveSource.saveMapChanges', {changed}) : t('action.save')}</Button>
  </View>;
  const partial = error as {saved?: number; total?: number} | undefined;
  return <Dialog open={open} onOpenChange={value => onOpenChange(value)} title={t('web.liveSource.channelMapTitle', {name: source?.name ?? ''})} description={t('web.liveSource.channelMapLede')} initialFocusRef={close} footer={footer} testID="live-owner-channel-map">
    <View style={{gap: 12}}>
      {error !== undefined && (partial?.saved !== undefined
        ? <Text testID="live-owner-channel-map-partial" style={[shellType(13), {color: theme.colors.amber}]}>{t('web.liveSource.channelMapPartial', {saved: partial.saved ?? 0, total: partial.total ?? changed})}</Text>
        : <StateView testID="live-owner-channel-map-error" {...runtime.error(error, () => {if (source) void load(source.id);}, 'live')}/>)}
      <TextField testID="live-owner-channel-search" variant="search" leadingIcon="i-search" accessibilityLabel={t('web.live.findChannel')} placeholder={t('web.live.findChannel')} value={filter} onChangeText={setFilter}/>
      <ScrollView style={{maxHeight: 420}} contentContainerStyle={{gap: 10}} keyboardShouldPersistTaps="handled">
        <LoadingBoundary ready={!loading || !!page} immediate={!loading || !!page} skeleton={<View style={{gap: 8}}><SkeletonBlock height={84} radius={12}/><SkeletonBlock height={84} radius={12}/></View>}>
        <>
        {shown.map(entry => {
          const current = value(entry.channelId, entry), touched = edits[entry.channelId] !== undefined;
          return <View key={entry.channelId} testID={`live-owner-channel-${entry.channelId}`} style={{gap: 8, padding: 12, borderRadius: 12, backgroundColor: theme.colors.raisedHigh, borderWidth: touched ? 1.5 : 0, borderColor: theme.colors.accent, opacity: current.hidden ? .6 : 1}}>
            <View style={{flexDirection: 'row', alignItems: 'center', gap: 10}}>
              <View style={{width: 28, height: 28, borderRadius: 6, overflow: 'hidden', backgroundColor: theme.colors.raisedHigh, alignItems: 'center', justifyContent: 'center'}}>
                {entry.mark ? <ChannelMark mark={{sourceId: page?.sourceId ?? '', id: entry.channelId, ...entry.mark}} size={28}/> : <Icon name="i-tv" size={18} color={theme.colors.textSecondary}/>}
              </View>
              <View style={{flex: 1}}>
                <Text style={[shellType(15, 700), {color: theme.colors.text}]} numberOfLines={1}>{entry.name}</Text>
                <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 3}}>{[`${t('server.live.map.fromSource')} ${entry.sourceNumber || '—'}`, entry.group].filter(Boolean).map((part, index) => <Text key={index} style={[shellType(12.5), {color: theme.colors.textTertiary}]} numberOfLines={1}>{part}</Text>)}</View>
              </View>
              <Switch testID={`live-owner-channel-hidden-${entry.channelId}`} accessibilityLabel={t('web.liveSource.hideChannel', {name: entry.name})} checked={current.hidden} onCheckedChange={hidden => edit(entry, {hidden})}/>
            </View>
            <View style={{flexDirection: 'row', gap: 8}}>
              <TextField testID={`live-owner-channel-number-${entry.channelId}`} containerStyle={{flex: 1}} label={t('server.live.map.number')} value={current.number} maxLength={12} onChangeText={number => edit(entry, {number})}/>
              <TextField testID={`live-owner-channel-guide-${entry.channelId}`} containerStyle={{flex: 2}} label={t('server.live.map.guideId')} placeholder={t('player.quality.automatic')} value={current.guideChannelId} maxLength={200} autoCapitalize="none" autoCorrect={false} onChangeText={guideChannelId => edit(entry, {guideChannelId})}/>
            </View>
          </View>;
        })}
        {!loading && page && !shown.length && <Text style={[shellType(13), {color: theme.colors.textTertiary}]}>{t('web.liveSource.noChannels')}</Text>}
        </>
        </LoadingBoundary>
      </ScrollView>
      {page?.nextCursor ? <Button variant="glass" size="small" loading={loading} onPress={() => {if (source && page) void load(source.id, page.nextCursor);}}>{t('action.showMoreChannels')}</Button> : null}
    </View>
  </Dialog>;
}
