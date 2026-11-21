import React from 'react';
import {defaultRecordingOptions} from '../../../../../packages/client-core/src/dvr';
import {legacyChannel, legacyProgram, legacyProgramme, rulerTicks, SLOT_MS} from '../../../../../packages/client-core/src/guide';
import {episodeLabel, programmeTimeRange} from '../../../../../packages/client-core/src/presentation/kit-model';
import {channelUnavailableMessage} from '../../../../../packages/client-core/src/presentation/live-availability';
import {Button, Select} from '../controls';
import {FocusRegion} from '../focus';
import type {LiveFrameProps} from '../frames';
import {GuideActionRow, GuideDayPicker, GuideFilterStrip, GuideNotices, GuideOnNow, GuideRecordingsButton, GuideSourcePicker, GuideViewport, GuideViewSwitch} from './GuideControls';
import {NativeLivePhonePicker} from './LivePhonePicker';
import {NativeGuideGrid} from './NativeGuideGrid';
import {LiveRow} from './NativeGuideRow';
import {ProgrammePanel, type LaterProgramme} from './ProgrammePanel';
import {Menu, ToastHost} from '../overlays';
import {OwnerTag} from '../shell/ServerIdentity';
import {StateView} from '../states';
import {visibleGuideRows} from '../../screens/settings-live/guide-pan-model';
import {nativeChannelMenuEntries} from '../../screens/settings-live/guide-presentation';
import {NativeSeriesRuleSheet} from '../../screens/settings-live/LiveSeriesRule';
import {NativeLiveTabs} from './LiveTabs';
import {guideFocusMoved, liveGuideEntry, tvProgrammeGroups} from '../../screens/settings-live/tv-settings-focus';

import type {useLiveGuide, GuidePageProps} from '../../pages/shared/live/useLiveGuide';

export function guideFrame(props: GuidePageProps, model: ReturnType<typeof useLiveGuide>): LiveFrameProps {
  const {controller, recordings, ports, runtime} = props;
  const {theme, t, dockTop, serverName, state, guide, data, ids, compact, screenReader, phone, phoneChrome, expandedTouch,
    insetLeft, insetRight, now, start, view, setView, selected, setSelected, channelMenu, setChannelMenu, channelMenuBusy,
    channelMenuError, setChannelMenuError, channelMenuAnchor, content, setMeasuredWidth, measureContent, contentWidth,
    recording, setRecording, recordError, setRecordError, series, setSeries, toast, setToast, reminders, later,
    laterRecordings, laterRecording, guideReentry, guideReentryRef, tvRegistry, reentryBaseline, scopeKey, reentry,
    setRecordingsOpen, recordingsReturn, managementReturn, opener, selectedOpener,
    list, visible, guideOffset, pull, channelWidth, paneLeft, guideEnd, twoPane, width, rowHeight, rowGap, px, end,
    tvRange, revealTVCell, pan, panPosition, panActive, rulerShift, nowShift, nowPillShift, pastShade, earlierNow, laterNow,
    overscan, clock, goNow, nowX, inNowSpan, open, openChannelMenu, changeChannel, remind, recordLater, askSeries, favorite,
    watch, select, chooseSource, sourceOptions, chooseDay, shortDate, fullDate, dayName, dayTrailing, dayNote, days, daysCount,
    today, currentDay, sortOptions, chooseSort, controls, grid, filters, empty, filterEmpty, filterChips,
    managesServer, hasRecordings, conflicts, recordingsLabel, managementUrl, openManagement, serverWide, stale} = model;
  const guideNotices = <GuideNotices reason={serverWide && {title: t(serverWide.title), body: t(serverWide.body)}} stale={stale ? t('guide.stale') : undefined}/>;

  // A later programme leads with Record and offers Remind me; Watch live appears once it is on.
  const onNowProgramme = selected?.program !== undefined && selected.program.start <= now && selected.program.end > now;
  const showTVWatch = !!selected?.channel.tuneAvailable && (onNowProgramme || !selected.program);
  const tvCanRecord = !!selected && selected.channel.kind === 'live' && selected.channel.recordAvailable && !!selected.program && selected.program.end > now;
  const tvCanRemind = !!selected && selected.channel.kind === 'live' && !!selected.program && selected.program.start > now;
  const sourcePicker = <GuideSourcePicker phone={phoneChrome} label={t('channels.source')} value={state.options.sourceId} options={sourceOptions}
    current={state.sources.length === 1 ? state.sources[0].name : undefined} onChange={chooseSource}/>;
  const dayPicker = phoneChrome ? <GuideDayPicker testID="live-day" accessibilityLabel={t('guide.day')} value={String(currentDay)} disabled={daysCount < 1}
    onChange={chooseDay} options={days.map(day => ({id: String(day), label: dayName(day), trailing: dayTrailing(day)}))} note={dayNote}
    name={dayName(currentDay)} date={shortDate(currentDay)}/>
    : <Select testID="live-day" chevron="down" accessibilityLabel={t('guide.day')} disabled={daysCount < 1} value={String(currentDay)}
        options={days.map(day => ({value: String(day), label: day === today ? t('guide.today') : fullDate(day)}))} onValueChange={chooseDay}/>;
  const sortPicker = phoneChrome ? <NativeLivePhonePicker testID="live-sort" accessibilityLabel={t('channels.sort')} value={state.options.sort ?? 'number'}
    options={sortOptions.map(option => ({id: option.value, label: option.label}))} onChange={chooseSort} placement="bottom-end" secondary>
      {sortOptions.find(option => option.value === state.options.sort)?.label}
    </NativeLivePhonePicker> : <Select testID="live-sort" chevron="down" accessibilityLabel={t('channels.sort')} value={state.options.sort ?? 'number'}
      options={sortOptions} onValueChange={chooseSort}/>;
  const viewSwitch = <GuideViewSwitch value={view} onChange={setView} list={t('channels.viewList')} guide={t('channels.viewGuide')} compact={phone}/>;
  const recordingsIcon = <GuideRecordingsButton label={recordingsLabel} count={conflicts} onPress={() => setRecordingsOpen(true)}/>;
  const filterEmptyView = <StateView testID="live-filter-empty" title={t('guide.filterEmpty', {filter: state.options.group ?? t('channels.favorites')})}
    primaryAction={{id: 'show-all', label: t('channels.showAll'), onPress: () => {if (theme.isTV) reentry({kind: 'press', revision: state.revision}); select({favorites: false, group: undefined});}}}/>;
  const programmeRegion = ids.surface('live.programme');
  const programmeGroups = tvProgrammeGroups({watch: showTVWatch, record: tvCanRecord, remind: !!(tvCanRemind && selected && legacyChannel(selected.channel)),
    later: later.map(airing => airing.programme.id), recordError: recordError !== undefined, preferenceError: state.mutationError !== undefined, laterFirst: true});
  const laterOn: LaterProgramme[] = !selected ? [] : later.map(airing => {
    const episode = airing.programme.episode?.number !== undefined
      ? episodeLabel({mode: 'seasonal', season: airing.programme.episode.season, episode: airing.programme.episode.number}, 'player', runtime.text)?.text
      : undefined;
    const time = clock(Date.parse(airing.programme.start));
    const range = programmeTimeRange(Date.parse(airing.programme.start), Date.parse(airing.programme.end), {...runtime.text, timeZone: state.options.timezone})?.text;
    const recordingOf = laterRecordings.get(airing.programme.id) ?? (airing.programme.recordingId ? {id: airing.programme.recordingId, state: airing.programme.recordingState ?? ''} : undefined);
    const existing = reminders.get(airing.programme.id);
    const mayRecord = !theme.isTV && selected.channel.kind === 'live' && selected.channel.recordAvailable && Date.parse(airing.programme.end) > now;
    return {id: airing.programme.id, clock: time, episode, title: airing.programme.title, range, actions: [
      {id: `live-later-remind-${airing.programme.id}`, label: t(existing ? 'guide.stopRemind' : 'guide.remind'), icon: existing ? 'i-bell-f' : 'i-bell',
        selected: !!existing, onPress: () => remind(airing, existing)},
      ...(mayRecord ? [{id: `live-later-record-${airing.programme.id}`,
        label: recordingOf ? t(recordLabelKey(recordingOf)) : t('guide.recordAiring', {title: airing.programme.title, time: time ?? ''}),
        icon: 'i-rec' as const, disabled: laterRecording.has(airing.programme.id) || !!recordingOf, onPress: () => recordLater(airing)}] : []),
      {id: `live-later-open-${airing.programme.id}`, label: airing.programme.title, icon: 'i-info',
        onPress: () => setSelected({channel: selected.channel, program: {...legacyProgram(airing.programme), ...(recordingOf ? {recording: recordingOf} : {})}})},
    ]};
  });
  const programmeActions: React.ComponentProps<typeof ProgrammePanel>['actions'][number][] = [];
  if (selected) {
    const {channel, program} = selected;
    const watchAction = channel.tuneAvailable ? {id: 'watch', buttons: [{id: 'live-watch', label: t(onNowProgramme ? 'guide.watchLive' : 'guide.watchChannel'),
      icon: 'i-play' as const, primary: true, opener, onPress: () => watch(channel)}]} : undefined;
    if (watchAction && (theme.isTV ? showTVWatch : onNowProgramme)) programmeActions.push(watchAction);
    if (tvCanRecord && program) programmeActions.push({id: 'record', buttons: [
      {id: 'live-record', label: t(recordLabelKey(program.recording)), icon: 'i-rec', loading: recording, disabled: recording || !!program.recording,
        opener: (theme.isTV ? !showTVWatch : !channel.tuneAvailable) ? opener : undefined,
        onPress: () => {
          const legacy = legacyChannel(channel), programme = legacyProgramme(program);
          if (!legacy || !programme) return;
          setRecording(true);
          void recordings.schedule({channel: legacy, programme, series: false}, defaultRecordingOptions).then(result => {
            if (!controller.lifetime.current() || !result) {if (controller.lifetime.current()) setRecordError(recordings.getSnapshot().error); return;}
            const receipt = result.value;
            if (!theme.isTV) void recordings.loadSummary();
            setSelected(current => current?.channel.id === channel.id && current.program?.id === program.id ? {...current, program: {...current.program, recording: {id: receipt.id, state: receipt.state}}} : current);
            guide.invalidate();
          }, error => {if (controller.lifetime.current()) setRecordError(error);}).finally(() => {if (controller.lifetime.current()) setRecording(false);});
        },
      },
      {id: 'live-record-series', label: t('guide.recordSeries'), onPress: () => {void askSeries(channel, program);}},
    ]});
    if (theme.isTV) programmeActions.push({id: 'favorite', buttons: [{id: 'live-channel-favorite',
      label: t(channel.favorite ? 'channels.removeFavorite' : 'channels.addFavorite'), icon: channel.favorite ? 'i-star-f' : 'i-star',
      opener: !showTVWatch && !tvCanRecord ? opener : undefined, selected: channel.favorite, disabled: state.mutating, onPress: favorite}]});
    if (tvCanRemind && program) {
      const legacy = legacyChannel(channel), programme = legacyProgramme(program), existing = reminders.get(program.id);
      if (legacy) programmeActions.push({id: 'remind', buttons: [{id: 'live-remind', label: t(existing ? 'guide.stopRemind' : 'guide.remind'),
        icon: existing ? 'i-bell-f' : 'i-bell', disabled: !programme, onPress: () => {if (programme) {
          const {id, sourceId, provenance, generation, name, number, group} = legacy;
          remind({channel: {id, sourceId, provenance, generation, name, number, group}, programme}, existing);
        }}}]});
    }
    if (watchAction && !onNowProgramme && !theme.isTV) programmeActions.push(watchAction);
  }
  const sheetReason = selected && !selected.channel.tuneAvailable ? channelUnavailableMessage(selected.channel.tuneUnavailableReason ?? '') : undefined;
  // tablet-play6: at expanded touch widths the title row carries the Guide / Recordings pair and the view switch;
  // the day, the 2-hour steps with Now between them, and Sort sit under the chips. TV keeps its one controls row.
  const ownerButton = managesServer && <Button testID="live-owner-open" variant="glass" size="small" icon="i-sliders" disabled={theme.isTV && !managementUrl} onPress={() => openManagement()}>{t('settings.server.live')}</Button>;
  // tablet-play6 draws the view choice in the title row as an icon-only Grid / List pair, and no owner button there.
  const controlsRow = <GuideActionRow region={{id: controls, entry: 'live-source', orientation: 'horizontal', edges: {left: ids.rail, down: filters}, stops: {up: true}}}>
    {sourcePicker}
    {dayPicker}
    <Button testID="live-now" variant="glass" size="small" onPress={goNow}>{t('guide.now')}</Button>
    {hasRecordings && <Button focusId="live-recordings" testID="live-recordings" variant="glass" size="small" icon="i-repeatrule" onPress={() => setRecordingsOpen(true)}>{t('recordings.title')}</Button>}
    {ownerButton}
  </GuideActionRow>;
  const timeRow = <GuideActionRow testID="live-time">
    {dayPicker}
    {view === 'guide' && <Button testID="live-earlier" variant="glass" size="small" icon="i-left" accessibilityLabel={t('guide.earlier')} disabled={!panActive} onPress={() => pan.adjust(-4 * SLOT_MS)}>2 h</Button>}
    <Button testID="live-now" variant="glass" size="small" onPress={goNow}>{t('guide.now')}</Button>
    {view === 'guide' && <Button testID="live-later" variant="glass" size="small" trailingIcon="i-right" accessibilityLabel={t('guide.later')} disabled={!panActive} onPress={() => pan.adjust(4 * SLOT_MS)}>2 h</Button>}
    {sortPicker}
  </GuideActionRow>;
  const phoneOwner = managesServer && <Button testID="live-owner-open" variant="icon" size="small" icon="i-sliders"
    accessibilityLabel={t('settings.server.live')} onPress={() => openManagement()}/>;
  const phoneActions = <>{viewSwitch}{hasRecordings && recordingsIcon}</>;
  const channelFilters = <GuideFilterStrip choices={filterChips} leading={phoneChrome && sourcePicker}
    region={{id: filters, orientation: 'horizontal', entry: state.options.favorites ? 'favorites' : state.options.group ? `group:${state.options.group}` : 'all',
      edges: {left: ids.rail, up: controls, down: grid}}}/>;
  const phoneToolbar = <>{view === 'guide' ? dayPicker : <GuideOnNow label={t('guide.onNow')} clock={clock(now)}/>}{phoneOwner}{sortPicker}</>;
  const ticks = rulerTicks(start - overscan, end + overscan, px, state.options.timezone).map(tick => ({
    at: tick.atMs, left: tick.x - overscan * px, label: clock(tick.atMs) ?? '',
  }));
  const guideView = <GuideViewport viewport={content} onLayout={event => {setMeasuredWidth(event.nativeEvent.layout.width); measureContent();}}
      leading={insetLeft} trailing={twoPane ? Math.max(insetRight, contentWidth - guideEnd) : insetRight}>
      {state.error !== undefined ? <StateView {...runtime.error(state.error, () => {void controller.loadSources();}, 'live')}/> : empty ? <StateView testID="live-no-sources" variant={compact ? 'centered' : 'inline'} stackActions={compact}
        // states 17c: the owner's state names the server it sets up, with the Owner only tag over the heading.
        kicker={managesServer && !theme.isTV ? <OwnerTag testID="live-no-sources-owner-tag" identity={serverName}/> : undefined} title={t(managesServer ? 'web.live.empty.noSourcesOwnerTitle' : 'web.live.empty.noSourcesTitle')}
        body={t(managesServer ? 'next.states.setupLiveBody' : 'web.live.empty.noSourcesMember')}
        primaryAction={managesServer ? {id: 'add-source', label: t('next.states.addTVSource'), icon: 'i-plus', disabled: theme.isTV && !managementUrl, onPress: () => openManagement('add')}
          : {id: 'home', label: t('next.states.goHome'), onPress: () => runtime.onDestination({kind: 'home'})}}
        secondaryAction={managesServer ? {id: 'discover-tuners', label: t('next.states.findTuners'), icon: 'i-search', disabled: theme.isTV && !managementUrl, onPress: () => openManagement('discover')} : undefined}/> : <>
        {guideNotices}
        {!theme.isTV && screenReader && theme.width < 1024 && view === 'guide' && <GuideActionRow>
          <Button testID="live-earlier" variant="glass" size="small" icon="i-left" disabled={!panActive} onPress={() => pan.adjust(-4 * SLOT_MS)}>{t('guide.earlier')}</Button>
          <Button testID="live-later" variant="glass" size="small" trailingIcon="i-right" disabled={!panActive} onPress={() => pan.adjust(4 * SLOT_MS)}>{t('guide.later')}</Button>
        </GuideActionRow>}
        {filterEmpty ? theme.isTV ? <FocusRegion id={grid} edges={{up: filters, left: ids.rail}}>{filterEmptyView}</FocusRegion> : filterEmptyView : <NativeGuideGrid key={`${state.revision}:${view}`} gesture={pan.gesture} listRef={list}
          region={{id: grid, edges: theme.isTV ? {up: filters, left: ids.rail} : undefined, stops: theme.isTV ? {right: true, down: true} : undefined,
            timeGrid: theme.isTV ? {initialTime: now, start, end} : undefined}}
          ruler={view === 'guide' ? {marks: ticks, nowAt: nowX, nowVisible: inNowSpan || panActive,
            paneWidth: width - channelWidth, channelWidth, shift: rulerShift} : undefined}
          marker={view === 'guide' && (inNowSpan || panActive) ? {channelWidth, nowAt: nowX, clock: clock(now), shift: nowShift, pill: nowPillShift,
            past: phone ? pastShade : undefined} : undefined}
          docked={phoneChrome && view === 'guide' ? {earlier: earlierNow, later: laterNow, label: t('guide.now'),
            earlierLabel: t('guide.nowEarlier'), laterLabel: t('guide.nowLater'), onPress: goNow} : undefined}
          rows={{refreshControl: pull, data: null, getItem: (_, index) => index, getItemCount: () => data.total ?? 12, keyExtractor: index => String(index),
            initialNumToRender: 12, maxToRenderPerBatch: 12, windowSize: 5, removeClippedSubviews: false,
            getItemLayout: view === 'list' && !theme.isTV ? undefined : (_, index) => ({index, length: rowHeight + rowGap, offset: index * (rowHeight + rowGap)}),
            onLayout: () => list.current?.scrollToOffset({offset: guideOffset.current, animated: false}),
            onScroll: event => {guideOffset.current = event.nativeEvent.contentOffset.y;}, scrollEventThrottle: 16,
            onViewableItemsChanged: ({viewableItems}) => {
              const rows = visibleGuideRows(viewableItems.map(value => value.index).filter((value): value is number => value !== null));
              if (!rows) return;
              visible.current = rows;
              controller.setViewport({firstRow: rows.first, lastRow: rows.last, start, end});
            },
            renderItem: ({item: index}) => <LiveRow tvRange={tvRange} onTVFocus={revealTVCell} panPosition={panPosition} panActive={panActive} laneWidth={width - channelWidth}
              clock={clock} entryEnabled={liveGuideEntry(state.revision, recordingsReturn, managementReturn, guideReentry)}
              entryOpen={() => guideReentry === undefined || guideReentryRef.current !== undefined && !guideFocusMoved(reentryBaseline.current, tvRegistry.attachedFocus())}
              index={index} controller={controller} runtime={runtime} start={start} end={end} now={now} channelWidth={channelWidth} rowHeight={rowHeight}
              px={px} list={view === 'list'} onOpen={open} onChannelMenu={openChannelMenu} selected={twoPane ? selected : undefined} grid={grid} watch={watch}/>,
          }}/>}
        {data.channelsError && <StateView {...runtime.error(new Error('guide_channels_unavailable'), () => guide.reset(), 'live')}/>}
      </>}
    </GuideViewport>;
  const overlays = <>
    {selected && <ProgrammePanel channel={selected.channel} programme={selected.program} now={now} runtime={runtime}
      favorite={favorite} busy={state.mutating} paneLeft={twoPane ? paneLeft : undefined}
      region={programmeRegion} groups={programmeGroups} later={laterOn} actions={programmeActions} reason={sheetReason && t(sheetReason)}
      onClose={() => setSelected(undefined)} opener={theme.isTV || selected.channel.tuneAvailable || selected.channel.recordAvailable && selected.program ? opener : undefined}
      returnFocus={selectedOpener}
      recordError={recordError !== undefined ? <StateView testID="live-record-error" {...runtime.error(recordError, () => setRecordError(undefined), 'live')}/> : undefined}
      preferenceError={state.mutationError !== undefined ? <StateView testID="live-channel-preference-error" {...runtime.error(state.mutationError, favorite, 'live')}/> : undefined}/>}
    <Menu testID="live-channel-menu" open={!!channelMenu && channelMenu.scope === scopeKey} onOpenChange={open => {if (!open) setChannelMenu(undefined);}} title={channelMenu?.channel.name} anchorRef={channelMenuAnchor} returnFocusRef={channelMenuAnchor} touchContext
      footer={channelMenu && channelMenuError !== undefined ? <StateView {...runtime.error(channelMenuError, () => setChannelMenuError(undefined), 'live')}/> : undefined}
      items={channelMenu ? [
        ...nativeChannelMenuEntries(channelMenu.channel, legacyChannel(channelMenu.channel)?.serverHidden === true, ports.context.account.account.role === 'owner', t, theme.isTV)
          .map(entry => ({...entry, disabled: channelMenuBusy || state.mutating, closeOnSelect: false, onSelect: () => changeChannel(entry.id)})),
      ] : []}/>
    <NativeSeriesRuleSheet controller={recordings} draft={series?.draft} defaults={series?.defaults} runtime={runtime} open={series !== undefined}
      onOpenChange={value => {if (!value) setSeries(undefined);}}
      onCreated={() => {setSelected(undefined); setSeries(undefined); guide.invalidate(); if (!theme.isTV) void recordings.loadSummary(); setToast({id: `series-${Date.now()}`, message: t('guide.seriesSet'), icon: 'i-rec'});}}/>
    <ToastHost toast={toast} onDismiss={() => setToast(null)} bottomOffset={phone ? 96 : 0} obstructionTop={dockTop}/>
  </>;
  return {title: t('search.chip.liveTV'), switcher: expandedTouch ? sourcePicker : undefined,
    // A tablet's Guide / Recordings pair and the view switch sit on the title's line, inside the gutter (tablet-play 6), as on
    // the Recordings page; the row under the title is the channel filters.
    actions: theme.isTV ? controlsRow : expandedTouch ? <NativeLiveTabs value="guide" t={t} conflicts={conflicts}
      onRecordings={hasRecordings ? () => setRecordingsOpen(true) : undefined}>{viewSwitch}</NativeLiveTabs> : phoneActions,
    tabs: channelFilters,
    toolbar: theme.isTV ? undefined : phoneChrome ? phoneToolbar : timeRow,
    list: guideView, overlay: overlays, flushList: theme.isTV, testID: theme.isTV ? 'tv-live-guide' : 'phone-live-guide'};
}

/** A scheduled airing says its state where Record was; an unscheduled one says Record. */
const recordLabelKey = (recording?: Readonly<{state: string}>) => recording?.state === 'recording' ? 'web.dvr.state.recording' : recording?.state === 'conflicted' ? 'web.dvr.state.conflicted' : recording ? 'guide.recordingScheduled' : 'guide.record';
