import React from 'react';
import {ActivityIndicator, ScrollView, Text, View} from 'react-native';
import {sameFence} from '../../../../../packages/player-core/src';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {clockText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {queueVideoKind} from '../../../../../packages/client-core/src/playback-v1/v1-queue-player';
import {subtitleReason} from '../../../../../packages/client-core/src/subtitles';
import {Artwork} from '../artwork';
import {Button, Focusable} from '../controls';
import {FocusRegion} from '../focus';
import {Dialog} from '../overlays';
import {PanelBackground} from '../overlays/surface';
import {Icon, type IconName} from '../Icon';
import {textLines} from '../theme';
import {shellType} from '../shell/visual';
import {sameAuthority} from '../../shell/model';
import type {NativeWatchPanel} from '../../screens/watch/controls';
import {nativeWatchTracks} from '../../screens/watch/track-identity';
import {nativeWatchPanelRows, type NativeWatchChoiceRow} from '../../screens/watch/panel-model';
import {nativeWatchSubtitleTiming} from '../../screens/watch/subtitle-timing';
import {Quiet} from '../states';
import type {NativeUpNextOpening, NativeWatchPanelsProps, useWatchPanels} from '../../pages/shared/player/useWatchPanels';

export type WatchPanelsViewProps = Pick<NativeWatchPanelsProps, 'context' | 'options' | 'playback' | 'names' | 'copy' | 'renderError' | 'playbackInfo' | 'episodes' | 'subtitleControls' | 'sleep' | 'style'> & ReturnType<typeof useWatchPanels>;

/** The real Options/Audio/Subtitles/Quality/Speed/Chapters renderer. The host positions it inside its accepted pane/canvas.
 * It owns no Surface or engine; its state and commands are `useWatchPanels`'. */
export function WatchPanelsView({context, options, playback, names, copy, renderError, playbackInfo, episodes, subtitleControls, sleep, style, source, fence, state, theme, t, panelInteractive, tablet, choices, first, scroll, busy, reportCancel, pending, subtitleSearching, setSubtitleSearching, subtitleReasonText, setSubtitleReasonText, reportAsk, reportBusy, reportFailure, reportSent, reportCurrent, closeReport, openReport, sendReport, current, back, run}: WatchPanelsViewProps) {
  if (!state.panel || !sameFence(state.fence, fence)) return null;
  if (state.panel === 'episodes') return <FocusRegion ref={first} id="watch-panel" hold orientation="vertical" entryRegion="watch-episodes" enabled={panelInteractive} style={{flex: 1}}>{episodes?.(context)}</FocusRegion>;
  const ready = choices.phase === 'ready' && sameFence(choices.fence, fence) ? choices.options : undefined;
  const onlineSubtitles = fence.kind === 'queue' ? source.subtitles : undefined;
  const subtitlePlan = onlineSubtitles?.plan, subtitleProvider = onlineSubtitles?.provider, subtitleCandidates = onlineSubtitles?.candidates ?? [];
  const subtitleDiscovered = subtitlePlan ? subtitlePlan.discovered.filter(track => !subtitlePlan.resources.some(resource => resource.discoveryId === track.id && resource.enabled)) : [];
  const selected = nativeWatchTracks(source), localTracks = selected.local;
  const rows = ready || localTracks ? nativeWatchPanelRows(source, ready, names) : undefined;
  const subtitleOffsetMs = localTracks && localTracks.subtitleTimingSupported && selected.subtitle ? localTracks.subtitleOffsetMs
    : selected.resource && source.subtitles?.plan ? Number(source.subtitles.plan.offsetUs) / 1000 : undefined;
  // The accepted window's own wire facts only: an entry is offered where the projection says
  // it is available and carries a real position after the current one, with a display row the
  // queue actually published and a video kind the producer admits. No unseen slots, no
  // filtered-array index, no synthesized whole queue, and no legacy playEntry.
  const queueView = source.queue.view, selectQueued = playback.selectQueuedEntry;
  const queueSelection = !!selectQueued && source.capabilities.queueSelection === true;
  const offered = queueView?.window;
  const offeredCurrent = offered && queueView && offered.queueId === queueView.queue.id && offered.revision === queueView.queue.revision
    ? offered.entries.find(entry => entry.entryId === queueView.queue.currentEntryId) : undefined;
  const currentPosition = offeredCurrent?.available === true ? offeredCurrent.position : undefined;
  // The original page, read in the same render as the rows: its exact route snapshot object, the
  // authority, the fence and the accepted queue scope. A missing route is no proof and offers none.
  const routeNow = (() => {try {return context.runtime.navigation?.getSnapshot();} catch {return undefined;}})();
  const authorityNow = (() => {try {return context.runtime.getSnapshot().authority;} catch {return undefined;}})();
  const openingBase = routeNow && authorityNow && queueView
    ? {fence, route: routeNow, authority: authorityNow, queueId: queueView.queue.id, revision: queueView.queue.revision} : undefined;
  const following = queueSelection && queueView && offered && currentPosition !== undefined && openingBase
    ? (() => {
        const items = new Map(queueView.items.map(item => [item.entryId, item]));
        return offered.entries.flatMap(row => {
          const item = items.get(row.entryId);
          return item && row.entryId !== queueView.queue.currentEntryId && row.available === true && row.position !== undefined && row.position > currentPosition
            && queueVideoKind(item.kind) ? [{row, item, opening: {...openingBase, entryId: row.entryId}}] : [];
        });
      })()
    : [];
  const upNextCount = queueView?.remainingCount ?? queueView?.total;
  const rateLabel = (rate: number) => rate === 1 ? t('player.opt.normalSpeed') : `${new Intl.NumberFormat(context.runtime.text.locale, {maximumFractionDigits: 2}).format(rate)}×`;
  const labels: Record<Exclude<NativeWatchPanel, 'episodes'>, string> = {options: t('player.opt.menu'), audio: t('title.audio'), subtitles: t('player.opt.subtitles'),
    quality: t('player.opt.quality'), speed: t('player.opt.speed'), sleep: t('player.opt.sleepTimer'), style: t('player.opt.subtitleStyle'),
    chapters: t('player.opt.chapters'), 'up-next': t('player.post.upNext'), 'playback-info': t('player.opt.info')};
  const chooseRow = <T,>(row: NativeWatchChoiceRow<T>, action: (value: T) => Promise<void>, preferred: boolean) => <Focusable key={row.id} ref={preferred ? first : undefined}
    focusId={`watch-choice-${row.id}`} testID={`watch-choice-${row.id}`} focusKind="row" focusRadius={theme.isTV ? 12 : 0}
    accessibilityRole="button" accessibilityLabel={[row.primary, row.secondary, row.unavailable ? t('player.opt.unavailable') : undefined].filter(Boolean).join(', ')}
    accessibilityState={{selected: row.selected, busy: pending, disabled: pending || row.unavailable === true}} disabled={!current() || !theme.isTV && (pending || row.unavailable === true)}
    onPress={pending || row.unavailable ? undefined : () => {if (!row.selected) void run(() => action(row.value));}}
    style={({focused}) => ({minHeight: theme.isTV ? 54 : tablet ? 48 : 52, paddingVertical: 8, paddingHorizontal: theme.isTV ? 14 : 12,
      flexDirection: 'row', alignItems: 'center', gap: theme.isTV ? 14 : 12, borderRadius: theme.isTV ? 12 : 0,
      backgroundColor: theme.isTV && focused ? '#edf5fa29' : 'transparent', opacity: row.unavailable ? .5 : 1,
      borderBottomWidth: theme.isTV ? 0 : 1, borderBottomColor: '#3b566873'})}>
    {({focused}) => <>
      {theme.isTV && <View style={{width: 28}}>{row.selected && <Icon name="i-check" size={24} color={theme.colors.accentStrong}/>}</View>}
      <View style={{flex: 1}}><Text style={[shellType(theme.isTV ? 22 : 15, 600, 'normal', 0, theme.textScale), {color: row.selected || focused || !theme.isTV ? theme.colors.text : theme.colors.textSecondary}]}>{row.primary}</Text>
        {row.secondary && <Text style={[shellType(theme.isTV ? 19 : 12.5, 500, 'normal', 0, theme.textScale), {marginTop: theme.isTV ? 2 : 1, color: row.selected || focused ? theme.colors.textSecondary : theme.colors.textTertiary}]}>{row.secondary}</Text>}</View>
      {row.unavailable ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('player.opt.unavailable')}</Text> : !theme.isTV && row.selected && <Icon name="i-check" size={20} color={theme.colors.accentStrong}/>}
    </>}
  </Focusable>;
  const list = <T,>(values: readonly NativeWatchChoiceRow<T>[], action: (value: T) => Promise<void>) => <View style={theme.isTV ? {padding: 4, borderRadius: 14, backgroundColor: '#edf5fa0d'} : {marginHorizontal: 12, borderRadius: 14, backgroundColor: theme.colors.raisedHigh, overflow: 'hidden'}}>
    {values.map((row, index) => chooseRow(row, action, values.some(value => value.selected && (theme.isTV || !value.unavailable)) ? row.selected && (theme.isTV || !row.unavailable) : index === 0))}
  </View>;
  const actionRow = (id: string, primary: string, secondary: string | undefined, onPress: () => void, disabled = false, readable = false) => <Focusable key={id} focusId={`watch-action-${id}`} testID={`watch-action-${id}`}
    focusKind="row" focusRadius={theme.isTV ? 12 : 0} accessibilityRole="button" accessibilityLabel={[primary, secondary].filter(Boolean).join(', ')}
    accessibilityState={{disabled: pending || disabled, busy: pending}}
    disabled={!current() || !(theme.isTV && readable) && (pending || disabled)} onPress={pending || disabled ? undefined : onPress}
    style={({focused}) => ({minHeight: theme.isTV ? 54 : tablet ? 48 : 52, paddingVertical: 8, paddingHorizontal: theme.isTV ? 14 : 12, flexDirection: 'row', alignItems: 'center', gap: 12,
      borderRadius: theme.isTV ? 12 : 0, backgroundColor: theme.isTV && focused ? '#edf5fa29' : 'transparent', opacity: disabled ? .5 : 1,
      borderBottomWidth: theme.isTV ? 0 : 1, borderBottomColor: '#3b566873'})}>
    <View style={{flex: 1}}><Text style={[shellType(theme.isTV ? 22 : 15, 600, 'normal', 0, theme.textScale), {color: theme.colors.text}]}>{primary}</Text>
      {secondary && <Text style={[shellType(theme.isTV ? 19 : 12.5, 500, 'normal', 0, theme.textScale), {marginTop: theme.isTV ? 2 : 1, color: theme.colors.textTertiary}]}>{secondary}</Text>}</View>
    <Icon name="i-right" size={theme.isTV ? 24 : 18} color={theme.colors.textTertiary}/>
  </Focusable>;
  // tv-player 4 and playback-info 8: the footer under a list states what this device does, over a hairline.
  const footer = (lines: readonly string[]) => !lines.length ? null : <View style={{marginHorizontal: theme.isTV ? 14 : 18, marginTop: theme.isTV ? 8 : 0,
    paddingTop: theme.isTV ? 12 : 0, borderTopWidth: theme.isTV ? 1 : 0, borderTopColor: 'rgba(237,245,250,.08)'}}>
    {lines.map((line, index) => <Text key={index} style={[shellType(theme.isTV ? 19 : 12.5, theme.isTV ? 700 : 500, theme.isTV ? 28.5 : 18.75, 0, theme.textScale),
      {marginTop: theme.isTV ? (index ? 6 : 0) : (index ? 0 : 10), color: theme.isTV ? theme.colors.textSecondary : theme.colors.textTertiary}]}>{line}</Text>)}
  </View>;
  // tv-player.html #p4: the row names the style by its size and background ("Medium · Translucent");
  // every field is in the style panel itself. Five values never fit beside the label (W-16).
  const summaryFields = style ? style.fields.filter(field => field.key === 'playback.subtitleSize' || field.key === 'playback.subtitleBackground') : [];
  const styleSummary = style ? (summaryFields.length ? summaryFields : style.fields.slice(0, 2))
    .map(field => field.values.find(value => value.value === field.current)?.label).filter(Boolean).join(' · ') : undefined;
  // tv-player 4 draws the chapter in force as the Chapters row's own value.
  const chapterInForce = ready?.chapters.length ? (() => {
    const position = source.playback.positionSeconds * 1000;
    const index = ready.chapters.findIndex((chapter, at) => position >= chapter.startMs && (!ready.chapters[at + 1] || position < ready.chapters[at + 1]!.startMs));
    const chapter = index >= 0 ? ready.chapters[index] : undefined;
    return chapter ? chapter.title || t('player.chapter.numbered', {number: index + 1}) : undefined;
  })() : undefined;
  const retry = () => {void options.load(fence);};
  // Read a live authority without letting a throwing host turn into a default.
  const liveAuthority = () => {try {return context.runtime.getSnapshot().authority;} catch {return undefined;}};
  const liveRoute = () => {try {return context.runtime.navigation?.getSnapshot();} catch {return undefined;}};
  /** The caller lifetime belongs to the original page, not to the output it replaces: it stays
   * current through its own accepted change and retires only when that route or authority does. */
  const upNextLifetime = (opening: NativeUpNextOpening) => {
    const controller = new AbortController(), stops: (() => void)[] = [];
    let released = false;
    const dispose = () => {
      if (released) return;
      released = true;
      for (const stop of stops) {try {stop();} catch {}}
      stops.length = 0;
    };
    const isCurrent = () => {
      if (released || controller.signal.aborted) return false;
      // No output predicate here: the command's own accepted source change must survive.
      const authority = liveAuthority();
      return !!authority && sameAuthority(authority, opening.authority) && liveRoute() === opening.route;
    };
    const check = () => {if (!isCurrent()) {controller.abort(); dispose();}};
    try {
      const offRoute = context.runtime.navigation?.subscribe(check);
      if (offRoute) stops.push(offRoute);
      stops.push(context.runtime.subscribe(check));
    } catch {
      controller.abort(); dispose();
      return undefined;
    }
    return {signal: controller.signal, isCurrent, dispose};
  };
  const selectUpNext = (opening: NativeUpNextOpening) => {
    const select = playback.selectQueuedEntry;
    if (!select || busy.current || !current()) return;
    // Prove the original page before dispatch; never borrow a fresh route or default to true.
    const authority = liveAuthority();
    if (!authority || !sameAuthority(authority, opening.authority) || !sameFence(playback.getSnapshot().fence, opening.fence)
      || liveRoute() !== opening.route) return;
    const view = playback.getSnapshot().queue.view, offeredLive = view?.window;
    if (!view || view.queue.id !== opening.queueId || view.queue.revision !== opening.revision
      || !offeredLive || offeredLive.queueId !== opening.queueId || offeredLive.revision !== opening.revision) return;
    const row = offeredLive.entries.find(entry => entry.entryId === opening.entryId);
    if (row?.available !== true || row.position === undefined) return;
    const lifetime = upNextLifetime(opening);
    if (!lifetime) return;
    // Disposal is outside the command: a run refused at entry still releases its subscriptions.
    void run(() => select(opening.fence, {queueId: opening.queueId, expectedRevision: opening.revision, entryId: opening.entryId},
      {signal: lifetime.signal, isCurrent: lifetime.isCurrent})).finally(lifetime.dispose);
  };
  let body: React.ReactNode = null;
  if (state.panel === 'playback-info') body = playbackInfo?.(context);
  else if (state.panel === 'up-next') body = <View style={theme.isTV ? {padding: 4, borderRadius: 14, backgroundColor: '#edf5fa0d'} : {marginHorizontal: 12, borderRadius: 14, backgroundColor: theme.colors.raisedHigh, overflow: 'hidden'}}>
    {following.map(({item, opening}, index) => {
      const code = item.episode ? t('next.kit.episodeCode', {season: item.episode.seasonNumber, episode: item.episode.number}) : undefined;
      const minutes = item.durationMs && item.durationMs > 0 ? t('next.kit.duration.minutes', {minutes: Math.round(item.durationMs / 60_000)}) : undefined;
      return <Focusable key={item.entryId} ref={index === 0 ? first : undefined} focusId={`watch-up-next-entry-${item.entryId}`} testID={`watch-up-next-entry-${item.entryId}`}
        focusKind="row" focusRadius={theme.isTV ? 12 : 0} accessibilityRole="button" accessibilityLabel={[item.title, code, minutes].filter(Boolean).join(', ')}
        disabled={!current() || pending} onPress={() => {if (current()) selectUpNext(opening);}}
        style={({focused}) => ({minHeight: theme.isTV ? 72 : 64, paddingVertical: 8, paddingHorizontal: theme.isTV ? 14 : 12, flexDirection: 'row', alignItems: 'center', gap: 12,
          borderRadius: theme.isTV ? 12 : 0, backgroundColor: theme.isTV && focused ? '#edf5fa29' : 'transparent', borderBottomWidth: theme.isTV ? 0 : 1, borderBottomColor: '#3b566873'})}>
        <View style={{width: theme.isTV ? 128 : 84, height: theme.isTV ? 72 : 48, borderRadius: 8, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
          <Artwork source={context.runtime.artwork(item.stillUrl ?? item.backdropUrl)} width={theme.isTV ? 128 : 84} height={theme.isTV ? 72 : 48} style={{opacity: .8}}/>
        </View>
        <View style={{flex: 1, minWidth: 0}}>
          <Text {...textLines(1)} style={[shellType(theme.isTV ? 22 : 15, 600, 'normal', 0, theme.textScale), {color: theme.colors.text}]}>{item.title}</Text>
          {(code || minutes) && <Text {...textLines(1)} style={[shellType(theme.isTV ? 19 : 12.5, 500, 'normal', 0, theme.textScale), {marginTop: theme.isTV ? 2 : 1, color: theme.colors.textTertiary}]}>{[code, minutes].filter(Boolean).join(' · ')}</Text>}
        </View>
      </Focusable>;
    })}
  </View>;
  else if (!localTracks && choices.phase === 'failed' && state.panel !== 'audio') body = renderError(choices.error, retry);
  else if (!localTracks && choices.phase === 'failed') body = <View style={{padding: theme.isTV ? 14 : 24, alignItems: 'center', gap: 12}}>
    <Icon name="i-audio" size={theme.isTV ? 36 : 22}/><Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('player.opt.audioLoadFailed')}</Text>
    {!theme.isTV && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{copy.audioContinues}</Text>}
    <Button ref={first} focusId="watch-choices-retry" variant="glass" icon="i-restart" onPress={retry}>{t('action.tryAgain')}</Button>
  </View>;
  else if ((!ready && !localTracks) || !rows) body = <Quiet><ActivityIndicator accessibilityLabel={t('player.opt.loadingAudio')}/></Quiet>;
  else if (state.panel === 'audio') body = <>{list(rows.audio, track => playback.change(fence, {audioTrackId: track.id}))}{footer(names.audioFooter(source))}</>;
  else if (state.panel === 'subtitles') body = <>
    {list([{id: 'subtitle-off', primary: t('player.opt.off'), selected: selected.subtitlesOff, value: null}, ...rows.subtitles], track => playback.change(fence, {subtitleTrackId: track?.id ?? null}))}
    {(localTracks?.subtitleError || source.subtitleUnavailable) && <View style={{marginHorizontal: theme.isTV ? 14 : 18, marginVertical: 10}}>
      <Text testID="watch-subtitle-local-error" accessibilityRole="alert" style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('player.opt.unavailable')}</Text>
    </View>}
    {source.forcedOnly && playback.setForcedOnly && <Focusable focusId="watch-subtitle-forced" testID="watch-subtitle-forced" focusKind="row" accessibilityRole="switch"
      accessibilityLabel={t('player.opt.forced')} accessibilityState={{checked: source.forcedOnly.on}} disabled={!current() || pending}
      onPress={() => {if (!current()) return; playback.setForcedOnly?.(fence, !source.forcedOnly!.on);}}
      style={({focused}) => ({minHeight: theme.isTV ? 54 : tablet ? 48 : 52, paddingHorizontal: theme.isTV ? 14 : 12, flexDirection: 'row', alignItems: 'center', gap: 12,
        borderRadius: theme.isTV ? 12 : 0, backgroundColor: theme.isTV && focused ? '#edf5fa29' : 'transparent'})}>
      <Text style={[shellType(theme.isTV ? 22 : 15, 600, 'normal', 0, theme.textScale), {color: theme.colors.text, flex: 1}]}>{t('player.opt.forced')}</Text>
      <View style={{width: 64, height: 36, borderRadius: 18, backgroundColor: source.forcedOnly.on ? theme.colors.accent : '#1d3848'}}>
        <View style={{position: 'absolute', top: 4, left: source.forcedOnly.on ? 32 : 4, width: 28, height: 28, borderRadius: 14, backgroundColor: source.forcedOnly.on ? '#fff' : theme.colors.textTertiary}}/>
      </View>
    </Focusable>}
    {subtitleDiscovered.map(track => actionRow(`subtitle-add-${track.id}`, track.title || track.language.toUpperCase(), `${track.language.toUpperCase()} · ${t('player.opt.origin', {origin: track.origin})}`,
      () => {const found = source.subtitles?.plan?.resources.find(resource => resource.discoveryId === track.id && resource.sourceId === track.sourceId);
        if (found) void run(() => playback.change(fence, {subtitleTrackId: found.id})); else if (track.reason) setSubtitleReasonText(subtitleReason(track.reason, id => t(id)));},
      !track.enabled))}
    {theme.isTV && subtitleProvider && !subtitleProvider.enabled && actionRow('subtitle-search', t('player.opt.findSubtitles'), subtitleReason(subtitleProvider.reason, id => t(id)), () => {}, true, true)}
    {!theme.isTV && subtitleProvider && !subtitleProvider.enabled && <View style={{marginHorizontal: theme.isTV ? 14 : 18, marginVertical: 10}}>
      <Text style={[shellType(theme.isTV ? 19 : 12.5, 500, theme.isTV ? 28.5 : 18.75, 0, theme.textScale), {color: theme.colors.textTertiary}]}>{subtitleReason(subtitleProvider.reason, id => t(id))}</Text>
    </View>}
    {subtitleProvider?.enabled && !subtitleSearching && actionRow('subtitle-search', t('player.opt.findSubtitles'), t('player.opt.providedByOpenSubtitles'),
      () => {setSubtitleSearching(true); void run(async () => {const controls = subtitleControls?.(fence); if (controls) await controls.search((context.runtime.text.locale ?? 'en').split('-')[0] ?? 'en');});})}
    {subtitleProvider?.enabled && subtitleSearching && <>
      {source.subtitles?.busy && !subtitleCandidates.length && <Text style={[theme.type.body, {marginHorizontal: theme.isTV ? 14 : 18, marginVertical: 10, color: theme.colors.textSecondary}]}>{t('player.opt.lookingForSubtitles')}</Text>}
      {subtitleCandidates.map(candidate => actionRow(`subtitle-candidate-${candidate.id}`, candidate.title, `${candidate.language.toUpperCase()} · ${candidate.attribution}`,
        () => {void run(async () => {const controls = subtitleControls?.(fence); if (controls) {await controls.apply(candidate.id); setSubtitleSearching(false);}});}))}
      {!source.subtitles?.busy && !subtitleCandidates.length && !source.subtitles?.error && <Text style={[theme.type.body, {marginHorizontal: theme.isTV ? 14 : 18, marginVertical: 10, color: theme.colors.textTertiary}]}>{t('player.opt.noSubtitlesFound')}</Text>}
      {source.subtitles?.error && <View style={{marginHorizontal: theme.isTV ? 14 : 18, marginVertical: 10}}>
        <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{source.subtitles.error}</Text>
        <Text style={[theme.type.caption, {marginTop: 6, color: theme.colors.textTertiary}]}>{t('player.opt.providedByOpenSubtitles')}</Text>
      </View>}
    </>}
    {style && style.fields.length > 0 && !theme.isTV && actionRow('subtitle-style', t('player.opt.subtitleStyle'), styleSummary,
      () => {if (current()) context.openPanel('style');})}
    {subtitleReasonText && <Text style={[theme.type.caption, {marginHorizontal: theme.isTV ? 14 : 18, marginTop: 8, color: theme.colors.textTertiary}]}>{subtitleReasonText}</Text>}
    {subtitleOffsetMs !== undefined && <View style={{marginHorizontal: theme.isTV ? 14 : 18, marginTop: 12, flexDirection: 'row', alignItems: 'center', gap: 6}}>
      <Text style={[theme.type.bodyStrong, {color: theme.colors.text, flex: 1}]}>{t('player.opt.timing')}</Text>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('player.opt.offsetSeconds', {seconds: new Intl.NumberFormat(context.runtime.text.locale, {minimumFractionDigits: 1, maximumFractionDigits: 1}).format(subtitleOffsetMs / 1000)})}</Text>
      {[-500, 500].map(delta => <Button key={delta} focusId={`watch-subtitle-offset-${delta}`} variant="glass" size="small" style={tablet ? {height: 44, minWidth: 44} : undefined} disabled={pending || !nativeWatchSubtitleTiming(source, delta)}
        accessibilityLabel={t(delta < 0 ? 'player.opt.earlier' : 'player.opt.later')} onPress={() => {const choice = nativeWatchSubtitleTiming(playback.getSnapshot(), delta); if (choice) void run(() => playback.change(fence, choice));}}>
        {`${delta < 0 ? '−' : '+'}${new Intl.NumberFormat(context.runtime.text.locale, {minimumFractionDigits: 1, maximumFractionDigits: 1}).format(.5)}`}
      </Button>)}
    </View>}
  </>;
  else if (state.panel === 'quality') body = <>{list(rows.qualities, quality => playback.chooseQuality(fence, quality))}{footer(names.qualityFooter(source))}</>;
  else if (state.panel === 'speed') body = list(source.supportedRates.map(rate => ({id: String(rate), primary: rateLabel(rate), selected: source.effectiveRate === rate, value: rate})), async rate => {playback.setRate(fence, rate);});
  else if (state.panel === 'sleep') body = list<number | null>([null, 15, 30, 45, 60, 90].map(minutes => ({
      id: minutes === null ? 'off' : String(minutes), primary: minutes === null ? t('player.opt.off') : t('player.opt.minutes', {count: minutes}),
      selected: minutes === null ? sleep?.minutes == null : sleep?.minutes === minutes, value: minutes})), async minutes => {if (sleep) await sleep.set(fence, minutes);});
  else if (state.panel === 'style' && style) body = <View style={{gap: 18}}>{style.fields.map(field => <View key={field.key} style={{marginHorizontal: theme.isTV ? 0 : 12}}>
      <Text style={[shellType(theme.isTV ? 22 : 13, 700, 'normal', 0, theme.textScale), {color: theme.colors.textSecondary}]}>{field.label}</Text>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 10, marginTop: 8}}>
        {field.values.map(value => <Focusable key={value.value} focusId={`watch-style-${field.key}-${value.value}`} testID={`watch-style-${field.key}-${value.value}`}
          focusKind="row" accessibilityRole="button" accessibilityState={{selected: field.current === value.value}} disabled={!current() || pending}
          onPress={() => {if (!current() || field.current === value.value) return; void run(() => style.apply(field.key, value.value));}}
          style={({focused}) => ({minHeight: 48, paddingHorizontal: 16, borderRadius: 12, alignItems: 'center', justifyContent: 'center',
            backgroundColor: field.current === value.value ? 'rgba(92,179,226,.22)' : focused ? 'rgba(237,245,250,.16)' : 'rgba(237,245,250,.06)'})}>
          <Text style={[shellType(theme.isTV ? 20 : 13, 600, 'normal', 0, 1), {color: field.current === value.value ? theme.colors.accentStrong : theme.colors.text}]}>{value.label}</Text>
        </Focusable>)}
      </View>
    </View>)}</View>;
  else if (state.panel === 'chapters' && ready) body = list(ready.chapters.map((chapter, index) => ({id: `${index}:${chapter.startMs}`, primary: chapter.title || t('player.chapter.numbered', {number: index + 1}), secondary: clockText(chapter.startMs / 1000), selected: source.playback.positionSeconds * 1000 >= chapter.startMs && (!ready.chapters[index + 1] || source.playback.positionSeconds * 1000 < ready.chapters[index + 1]!.startMs), value: chapter})), chapter => playback.seek(fence, chapter.startMs / 1000));
  const renderedOptions: NativeWatchPanel[] = ['subtitles', ...(style?.fields.length && theme.isTV ? ['style' as const] : []), 'audio', ...(fence.kind !== 'local' ? ['quality' as const] : []), 'speed',
    ...(sleep ? ['sleep' as const] : []), ...(following.length ? ['up-next' as const] : []), ...(ready?.chapters.length ? ['chapters' as const] : []), ...(playbackInfo ? ['playback-info' as const] : [])];
  const initialOption = state.panelFocus && renderedOptions.includes(state.panelFocus) ? state.panelFocus : renderedOptions[0];
  const option = (panel: NativeWatchPanel, icon: IconName, value?: string) => {
    const open = state.panel === panel;
    return <Focusable key={panel} ref={state.panel === 'options' && panel === initialOption ? first : undefined}
      focusId={`watch-option-${panel}`} testID={`watch-option-${panel}`} focusKind="row" accessibilityRole="button" accessibilityState={{expanded: open}} onPress={() => {if (current()) context.openPanel(panel);}}
      style={({focused}) => ({minHeight: theme.isTV ? 60 : 44, paddingHorizontal: theme.isTV ? 20 : 12, flexDirection: 'row', alignItems: 'center', gap: 12,
        borderRadius: theme.isTV ? open ? 0 : 14 : 0, ...(theme.isTV && open ? {borderTopLeftRadius: 14, borderTopRightRadius: 14} : {}),
        backgroundColor: theme.isTV && open ? 'rgba(237,245,250,.05)' : focused ? '#edf5fa29' : undefined})}>
      {/* W-16: the label keeps its own width and the value gives way (one line, ellipsis). With
          flex: 1 the label had a zero basis, so a long value squeezed it to nothing and its letters
          wrapped one per line, making the row hundreds of pixels tall. */}
      {!theme.isTV && <Icon name={icon} size={18}/>}<Text {...textLines(1)} style={[shellType(theme.isTV ? 24 : 15, 600, 'normal', 0, theme.textScale), {color: theme.colors.text, flexGrow: 1, flexShrink: 0}]}>{labels[panel as keyof typeof labels]}</Text>
      {value && <Text {...textLines(1)} style={[shellType(theme.isTV ? 22 : 14, theme.isTV && open ? 700 : 500, 'normal', 0, theme.textScale),
        {flexShrink: 1, textAlign: 'right', color: theme.isTV && open ? theme.colors.accentStrong : theme.isTV ? theme.colors.textTertiary : theme.colors.textSecondary}]}>{value}</Text>}
      {theme.isTV && open ? <Icon name="i-chdown" size={24} color={theme.colors.accentStrong}/> : <Icon name="i-right" size={theme.isTV ? 24 : 18}/>}
    </Focusable>;
  };
  // tv-player 4 and playback-info 8: the open row and its list share one well.
  const well = (panel: NativeWatchPanel, content: React.ReactNode) => theme.isTV && state.panel === panel
    ? <View style={{backgroundColor: 'rgba(237,245,250,.05)', borderBottomLeftRadius: 14, borderBottomRightRadius: 14, paddingTop: 4, paddingHorizontal: 10, paddingBottom: 14}}>{content}</View>
    : content;
  const expandedBody = body;
  // Read in the same render as the row: a retained copy of it opens for its own page
  // or for nothing, never for whatever page happens to follow.
  const reportOpening = context.runtime.reportPlaybackProblem && context.runtime.navigation
    ? Object.freeze({fence, navigation: context.runtime.navigation.getSnapshot()}) : undefined;
  if (state.panel !== 'style' && (state.panel === 'options' || theme.isTV && state.panel !== 'playback-info')) body = <View style={{marginHorizontal: theme.isTV ? 0 : 12, borderRadius: 14, backgroundColor: theme.isTV ? undefined : theme.colors.raisedHigh}}>
    {option('subtitles', 'i-cc', selected.subtitlesOff ? t('player.opt.off') : rows?.subtitles.find(row => row.selected)?.primary)}
    {state.panel === 'subtitles' && well('subtitles', expandedBody)}
    {style && style.fields.length > 0 && theme.isTV && option('style', 'i-subs', styleSummary)}
    {option('audio', 'i-audio', rows?.audio.find(row => row.selected)?.primary)}
    {state.panel === 'audio' && well('audio', expandedBody)}
    {fence.kind !== 'local' && option('quality', 'i-quality', rows?.qualities.find(row => row.selected)?.primary)}
    {state.panel === 'quality' && well('quality', expandedBody)}
    {option('speed', 'i-gauge', rateLabel(source.effectiveRate))}
    {state.panel === 'speed' && well('speed', expandedBody)}
    {sleep && option('sleep', 'i-moon', sleep.minutes == null ? t('player.opt.off') : t('player.opt.minutes', {count: sleep.minutes}))}
    {state.panel === 'sleep' && well('sleep', expandedBody)}
    {(!!ready?.chapters.length || playbackInfo || following.length > 0 || state.panel === 'options' && !!reportOpening) && <Text style={[shellType(theme.isTV ? 20 : 12, 700, 'normal', 0, theme.textScale), {marginTop: theme.isTV ? 22 : 10, marginBottom: theme.isTV ? 8 : 4, marginLeft: theme.isTV ? 20 : 18, color: theme.colors.textTertiary}]}>{t('next.player.thisTitle')}</Text>}
    {following.length > 0 && option('up-next', 'i-queue', upNextCount === undefined ? undefined : defaultI18n.number(upNextCount, {useGrouping: false}))}
    {state.panel === 'options' && reportOpening && actionRow('report-problem', t('next.states.reportProblem'), undefined, () => {if (current()) openReport(reportOpening);})}
    {state.panel === 'options' && reportSent && <Text testID="watch-report-sent" accessibilityLiveRegion="polite" style={[shellType(theme.isTV ? 19 : 12.5, 500, 'normal', 0, theme.textScale),
      {marginTop: 6, marginHorizontal: theme.isTV ? 14 : 18, color: theme.colors.textSecondary}]}>{t('about.report.sent', {server: context.runtime.getSnapshot().serverName ?? ''})}</Text>}
    {!!ready?.chapters.length && option('chapters', 'i-queue', chapterInForce)}
    {state.panel === 'up-next' && well('up-next', expandedBody)}
    {state.panel === 'chapters' && well('chapters', expandedBody)}
    {playbackInfo && option('playback-info', 'i-pulse')}
  </View>;
  const upright = !theme.isTV && context.layout.mode === 'watch';
  // foldable3d: a panel on a physical page is square to the crease and uses the
  // frame's own fill; the sheet and the floating phone panel keep the raised card.
  const paged = context.layout.panelPaged === true;
  return <View testID="watch-panels" style={{flex: 1, backgroundColor: paged ? 'rgba(22,44,58,.98)' : theme.colors.raised, borderRadius: theme.isTV || paged ? 0 : 20, overflow: 'hidden',
    paddingTop: theme.isTV ? 60 : paged ? 14 : 8, paddingLeft: theme.isTV ? 56 : 0, paddingRight: theme.isTV ? 96 : 0, paddingBottom: theme.isTV ? 56 : 20}}>
    {theme.isTV && <PanelBackground/>}
    {upright && <View pointerEvents="none" style={{height: 5, width: 36, borderRadius: 3, backgroundColor: '#9bbdce66', alignSelf: 'center', marginBottom: 10}}/>}
    <FocusRegion id="watch-panel" hold orientation="vertical" style={{flex: 1}}>
      <View style={{flexDirection: 'row', alignItems: 'center', paddingHorizontal: theme.isTV ? 0 : 6, paddingBottom: theme.isTV ? 0 : 8}}>
        {!theme.isTV && state.panel !== 'options' && <Button focusId="watch-panel-back" variant="text" iconOnly icon="i-left" accessibilityLabel={t('player.opt.menu')} onPress={back}/>}
        <Text accessibilityRole="header" style={[shellType(theme.isTV ? 44 : upright ? 19 : 18, 800, 'normal', theme.isTV ? -.03 : -.02, theme.textScale), {color: theme.colors.text, flex: 1}]}>{theme.isTV && state.panel !== 'playback-info' ? t('player.opt.menu') : labels[state.panel]}</Text>
        {!theme.isTV && (upright ? <Button focusId="watch-panel-close" variant="text" textStyle={{color: theme.colors.accentStrong}} onPress={context.closePanel}>{t('action.done')}</Button>
          : <Button focusId="watch-panel-close" variant="text" iconOnly icon="i-close" accessibilityLabel={t('action.close')} onPress={context.closePanel}/>)}
      </View>
      {(theme.isTV && state.panel !== 'playback-info' || state.panel === 'options') && <Text style={[shellType(theme.isTV ? 22 : 12.5, 400, theme.isTV ? 31.9 : 18.125, 0, theme.textScale), {marginTop: theme.isTV ? 10 : 2, marginBottom: theme.isTV ? 20 : 10, marginHorizontal: theme.isTV ? 0 : 18, color: theme.colors.textSecondary}]}><Text style={{fontFamily: theme.type.bodyStrong.fontFamily}}>{copy.sessionLead}</Text>{' '}{copy.sessionDefaults}</Text>}
      {/* playback-info 3: the TV route and column heads stay still while its sections scroll. */}
      {theme.isTV && state.panel === 'playback-info' ? <View style={{flex: 1, minHeight: 0, paddingTop: 4}}>{body}</View>
        : <ScrollView ref={scroll} contentContainerStyle={{paddingTop: theme.isTV ? 4 : 0, paddingBottom: theme.isTV ? 72 : 12}} onScroll={event => {if (current()) context.rememberPanel(event.nativeEvent.contentOffset.y);}} scrollEventThrottle={32}>{body}</ScrollView>}
      {theme.isTV && <View style={{flexDirection: 'row', gap: 12, alignItems: 'center', marginTop: 14}}><Text style={[theme.type.caption, {color: theme.colors.textSecondary, borderWidth: 1, borderColor: theme.colors.textTertiary, borderRadius: 6, padding: 4}]}>{t('action.back')}</Text><Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('action.close')}</Text></View>}
    </FocusRegion>
    <Dialog testID="watch-report-consent" open={reportCurrent(reportAsk)} initialFocusRef={reportCancel}
      onOpenChange={next => {if (!next && reportAsk) closeReport(reportAsk);}}
      title={t('about.report.confirmTitle', {server: context.runtime.getSnapshot().serverName ?? ''})} description={t('about.report.confirmBody')}
      footer={<View style={{flexDirection: 'row', gap: 16}}>
        <Button ref={reportCancel} testID="watch-report-cancel" variant="glass" onPress={() => {if (reportAsk) closeReport(reportAsk);}}>{t('action.cancel')}</Button>
        <Button testID="watch-report-send" variant="glass" loading={reportBusy} onPress={sendReport}>{t('about.report.send')}</Button>
      </View>}>
      {reportFailure !== undefined ? renderError(reportFailure, sendReport) : <View/>}
    </Dialog>
  </View>;
}
