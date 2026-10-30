import React, {useEffect, useMemo, useRef, useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import type {GuideChannel, GuideProgramme} from '../../../../../packages/client-core/src/channel-guide';
import type {DVRCommandLifetime, RecordingDraft, RulePreview, RuleConfig, Occurrence} from '../../../../../packages/client-core/src/dvr';
import {Button, Chip, Select} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {Dialog} from '../../kit/overlays';
import {StateView} from '../../kit/states';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeRecordingsController} from './recordings-controller';
import {keepChoiceList, seriesConfig, type SeriesForm} from './recording-rule-presentation';

const paddingChoices = [0, 60, 120, 300, 600, 900];

/** live-owner.html frame 29: Record series… from a guide programme, in the same side sheet.
 * The rule is built through `seriesRuleConfig` and the server's own preview supplies the count;
 * nothing is created until the primary action and the returned receipt. */
export function NativeSeriesRuleSheet({controller, draft, defaults, runtime, open, onOpenChange, onCreated, lifetime}: {
  controller: NativeRecordingsController;
  draft: RecordingDraft | undefined;
  defaults: SeriesForm | undefined;
  runtime: NativeShellRuntime;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: () => void;
  /** Watch opts into its original source/programme/route opening. Guide stays unchanged. */
  lifetime?: DVRCommandLifetime;
}) {
  const theme = useTheme(), t = runtime.text.t, tv = theme.isTV;
  const [form, setForm] = useState<SeriesForm>(defaults ?? {episodes: 'new', anyChannel: false, keep: 'count:0', beforeSeconds: 0, afterSeconds: 0});
  const [preview, setPreview] = useState<RulePreview>(), [error, setError] = useState<unknown>(), [busy, setBusy] = useState(false);
  const primary = useRef<View>(null), cancel = useRef<View>(null);
  const command = useRef<{id: Promise<string>; config: RuleConfig; anchor: Occurrence} | undefined>(undefined);
  const busyCommand = useRef(false);
  const current = () => !lifetime || !lifetime.signal.aborted && lifetime.isCurrent();
  const changeForm: typeof setForm = value => {if (!lifetime || !command.current) setForm(value);};
  useEffect(() => {if (open && (!lifetime || !command.current)) {setForm(defaults ?? {episodes: 'new', anyChannel: false, keep: 'count:0', beforeSeconds: 0, afterSeconds: 0}); setPreview(undefined); setError(undefined); setBusy(false);}}, [open, defaults, lifetime]);
  const built = useMemo(() => draft ? seriesConfig(draft, form) : undefined, [draft, form]);
  // The match count changes with the episode and channel choices only; the limits cannot.
  useEffect(() => {
    if (!open || !built || !current() || lifetime && command.current) return;
    let live = true;
    setPreview(undefined);
    void controller.previewRule(built.config, lifetime).then(value => {if (live && current()) setPreview(value);}, failure => {if (live && current()) setError(failure);});
    return () => {live = false;};
  }, [open, form.episodes, form.anyChannel, controller]); // eslint-disable-line react-hooks/exhaustive-deps
  const titleMatch = built?.config.matchKind === 'title';
  useEffect(() => {if (titleMatch && (!lifetime || !command.current)) setForm(value => value.anyChannel ? {...value, anyChannel: false} : value);}, [titleMatch, lifetime]);
  const save = async () => {
    if (!built || busy || busyCommand.current || !current()) return;
    busyCommand.current = true;
    setBusy(true); setError(undefined);
    try {
      if (lifetime && !command.current) {
        const config: RuleConfig = {...built.config, allowedChannels: [...built.config.allowedChannels], blockedChannels: [...built.config.blockedChannels],
          keywords: [...built.config.keywords], blockedKeywords: [...built.config.blockedKeywords], options: {...built.config.options}};
        for (const values of [config.allowedChannels, config.blockedChannels, config.keywords, config.blockedKeywords]) Object.freeze(values);
        Object.freeze(config.options);Object.freeze(config);
        const anchor = Object.freeze({...built.anchor});
        const id = controller.newRuleID(lifetime);void id.catch(() => {});
        command.current = Object.freeze({id, config, anchor});
      }
      const original = command.current;
      let ruleId: string | undefined;
      if (original) {
        try {ruleId = await original.id;}
        catch (failure) {if (command.current === original) command.current = undefined;throw failure;}
        if (!current()) return;
      }
      const result = await controller.createRule(original?.config ?? built.config, original?.anchor ?? built.anchor, lifetime, original?.id);
      if (lifetime && current() && !result) throw new Error('recording_action_unavailable');
      if (original && result && result.value.id !== ruleId) throw new Error('recording_rule_unavailable');
      if (result && current()) {onCreated();if (current()) onOpenChange(false);}
    } catch (failure) {if (current()) setError(failure);}
    finally {busyCommand.current = false;if (current()) setBusy(false);}
  };
  const rowStyle = {height: tv ? 76 : 52, borderRadius: tv ? 16 : 12, paddingHorizontal: tv ? 26 : 16, backgroundColor: `${theme.colors.text}0F`};
  const rowText = {fontSize: (tv ? 25 : 15.5) * theme.textScale, fontWeight: '600' as const};
  const minutes = (value: number) => t(value ? 'recordings.minutes' : 'recordings.none', {count: value / 60});
  return <Dialog open={open} onOpenChange={onOpenChange} title={draft?.programme.title ?? ''} restoreFocus={lifetime ? false : undefined}
    headerContext={<View style={{flexDirection: 'row', alignItems: 'center', gap: 16, marginBottom: tv ? 18 : 12}}><Icon name="i-repeatrule" size={tv ? 26 : 20} color={theme.colors.accentStrong}/><Text style={[shellType(tv ? 22 : 14, 700), {color: theme.colors.accentStrong}]}>{t('guide.recordSeriesTitle')}</Text></View>}
    placement={tv || theme.width >= 700 ? 'side' : undefined} width={tv ? 760 : undefined} initialFocusRef={primary} returnFocusRef={cancel} testID="live-series-rule">
    <ScrollView contentContainerStyle={{gap: 12}}>
      {error !== undefined && <StateView testID="live-series-rule-error" {...runtime.error(error, () => setError(undefined))}
        {...(lifetime ? {primaryAction: undefined, secondaryAction: undefined, additionalActions: undefined} : {})}/>}
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 22, marginBottom: tv ? 18 : 6}}>
        {preview && <Text style={[shellType(tv ? 22 : 13, 700), {color: theme.colors.text}]}>{t('recordings.matches', {count: preview.matches})}</Text>}
        {titleMatch && <Text style={[shellType(tv ? 22 : 13, 400), {color: theme.colors.textTertiary}]}>{t('guide.matchesByTitle')}</Text>}
      </View>
      <View style={{flexDirection: 'row', gap: 12}}>{(['new', 'all'] as const).map(episodes => <Chip key={episodes} testID={`live-series-episodes-${episodes}`} disabled={!!lifetime && !!command.current} selected={form.episodes === episodes} style={{flex: 1, justifyContent: 'center'}} onPress={() => changeForm(value => ({...value, episodes}))}>{t(episodes === 'new' ? 'guide.newOnly' : 'guide.allEpisodes')}</Chip>)}</View>
      <View style={{flexDirection: 'row', gap: 12}}>{([false, true] as const).map(anyChannel => <Chip key={String(anyChannel)} testID={`live-series-channel-${anyChannel ? 'any' : 'this'}`} selected={form.anyChannel === anyChannel} disabled={anyChannel && titleMatch || !!lifetime && !!command.current} style={{flex: 1, justifyContent: 'center'}} onPress={() => changeForm(value => ({...value, anyChannel}))}>{t(anyChannel ? 'guide.anyChannel' : 'guide.thisChannel')}</Chip>)}</View>
      <Select testID="live-series-keep" rowLabel={t('recordings.keepLabel')} accessibilityLabel={t('recordings.keepLabel')} value={form.keep} disabled={!!lifetime && !!command.current}
        options={keepChoiceList(runtime.text, {episodeLimit: keepCount(form.keep), retentionDays: keepDays(form.keep)})} onValueChange={keep => changeForm(value => ({...value, keep}))} style={rowStyle} textStyle={rowText}
        pickerContext={<View style={{flexDirection: 'row', alignItems: 'center', gap: 16}}><Icon name="i-repeatrule" size={26} color={theme.colors.accentStrong}/><Text style={[shellType(22, 700), {color: theme.colors.accentStrong}]}>{t('guide.recordSeriesTitle')}</Text></View>}/>
      <Select testID="live-series-before" rowLabel={t('recordings.startEarly')} accessibilityLabel={t('recordings.startEarly')} value={String(form.beforeSeconds)} disabled={!!lifetime && !!command.current}
        options={paddingChoices.map(value => ({value: String(value), label: minutes(value)}))} onValueChange={value => changeForm(current => ({...current, beforeSeconds: Number(value)}))} style={rowStyle} textStyle={rowText}/>
      <Select testID="live-series-after" rowLabel={t('recordings.endLate')} accessibilityLabel={t('recordings.endLate')} value={String(form.afterSeconds)} disabled={!!lifetime && !!command.current}
        options={paddingChoices.map(value => ({value: String(value), label: minutes(value)}))} onValueChange={value => changeForm(current => ({...current, afterSeconds: Number(value)}))} style={rowStyle} textStyle={rowText}/>
      <View style={{gap: 12, marginTop: tv ? 16 : 8}}>
        <Button testID="live-series-create" ref={primary} disabled={busy || !preview} loading={busy} onPress={() => void save()} icon="i-repeatrule">{t('guide.recordSeriesTitle')}</Button>
        <Button testID="live-series-back" ref={cancel} variant="glass" disabled={busy} onPress={() => onOpenChange(false)}>{t('action.back')}</Button>
      </View>
    </ScrollView>
  </Dialog>;
}

const keepCount = (keep: string) => keep.startsWith('count:') ? Number(keep.slice(6)) || 0 : 0;
const keepDays = (keep: string) => keep.startsWith('days:') ? Number(keep.slice(5)) || 0 : 0;
