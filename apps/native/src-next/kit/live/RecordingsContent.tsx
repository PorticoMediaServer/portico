import React from 'react';
import {activeRecording, playableRecording, type DVRView} from '../../../../../packages/client-core/src/dvr';
import {programmeTimeRange} from '../../../../../packages/client-core/src/presentation/kit-model';
import {serverNow} from '../../../../../packages/client-core/src/server-clock';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Chip} from '../controls';
import type {LiveFrameProps} from '../frames';
import {ConflictCard, RecordingRow as KitRecordingRow, RecordingCard, recordingGridColumns, RecordingFacts, RecordingList, RecordingResults, RecordingSection, RecordingSpaceFacts, RecordingStorageSummary, RecordingViewTabs, RuleCard} from './Recordings';
import {Menu} from '../overlays';
import {Quiet, StateView} from '../states';
import {NewRuleSheet} from './NewRuleSheet';
import {NativeLiveTabs} from './LiveTabs';
import {keepLabel, ruleFacts, ruleKeep, ruleNext, ruleScope} from '../../screens/settings-live/recording-rule-presentation';

import {RecordingConfirm, RecordingDetails, RuleEditor} from './RecordingDialogs';
import {keptOrWatched, reasonLabels, sortLabels, stateLabels, storageFacts, viewLabels, type useRecordings, type RecordingsProps} from '../../pages/shared/live/useRecordings';

export function recordingsFrame(props: RecordingsProps, model: ReturnType<typeof useRecordings>): LiveFrameProps {
  const {controller, ports, runtime, onGuide, owner} = props;
  const {theme, t, state, query, ids, tabs, body, bodyFocused, group, setGroup, menu, detail, rule, confirm, setConfirm,
    newRule, setNewRule, sortOpen, setSortOpen, opener, cancel, list, alive, viewerOpening, overlay,
    drawnCurrent, closeConfirm, retire, groups, rules, first, emptyGuide, entryRegion, enter, select, sort,
    selectSort, leaveGuide, back, open, close, askReady, run, play, askDelete, rowData, restartViewer, isEmpty, emptyTitle,
    emptyBody, unavailable, window, watchFocus, refRow, scroll, openRule, closeRule, ruleCurrent, availableViews} = model;
  // Local open-sheet/group state must not outlive an authoritative DVR access denial.
  const expandedTouch = theme.form === 'expanded';
  if (state.accessDenied) return {title: t('recordings.title'), list: <StateView {...runtime.error(state.error ?? {status: 403}, onGuide, 'live')} title={t('recordings.title')}/>};
  const groupHeader = group && <RecordingFacts items={[group.recording.channel?.name,
    t('recordings.showCountUnwatched', {count: group.recordings, unwatched: group.unwatched})]}/>;
  const headerStorage = !group && state.storage && query.view !== 'storage' ? state.storage : undefined;
  const usage = headerStorage && storageFacts(headerStorage);
  const storageHeader = usage && theme.isTV ? <RecordingSpaceFacts used={usage.used} free={usage.free} fraction={usage.fraction}/> : undefined;
  const viewsHeader = groupHeader ?? <RecordingViewTabs
    region={{id: tabs, entry: query.view, orientation: 'horizontal', edges: {left: ids.rail, down: body}, onLayout: entryRegion === tabs ? enter : undefined}}
    choices={availableViews.map(view => ({value: view, label: t(viewLabels[view]),
      count: view === 'upcoming' || view === 'recorded' || view === 'rules' ? state.summary?.[view] : undefined}))}
    value={query.view} onChange={view => select(view as DVRView)}/>;
  const columns = recordingGridColumns(theme);
  const isGroupGrid = query.view === 'recorded' && !query.group;
  const entries = isGroupGrid ? <RecordingList key={`groups:${columns}`} {...scroll} columns={columns} fade={theme.isTV} data={groups}
    keyExtractor={value => value.key}
    lead={theme.isTV && state.summary && <RecordingSection
      title={t('next.live.recordedHead', {shows: state.summary.groups, recordings: state.summary.recorded, unwatched: state.summary.unwatched})}
      action={<Chip focusId="recordings-sort" testID="recordings-sort" onPress={() => setSortOpen(true)}>{`${t('next.browse.sort')} · ${t(sortLabels[sort])}`}</Chip>}/>}
    renderItem={({item, index}, width) => <RecordingCard value={item} runtime={runtime} width={width} {...watchFocus(item.key, index, groups.length)}
      onOpen={() => {
        if (!drawnCurrent() || !window || controller.getSnapshot().viewerWindow !== window || !window.items.some(row => row.kind === 'group' && row.value.key === item.key)) return;
        setGroup(item); retire();
        void controller.load({view: 'recorded', group: item.key, sort});
      }}/>}
  /> : query.view === 'rules' ? <RecordingList key="rules" {...scroll} data={rules} keyExtractor={value => value.id}
    ListHeaderComponent={<RecordingSection title={[t('recordings.rulesLedeLead'), t('recordings.rulesLedeAction'), t('recordings.rulesLedeTail')].join(' ')}/>}
    renderItem={({item, index}) => {
      const paused = !item.config.enabled, facts = ruleFacts(item.summary);
      const next = ruleNext(item.summary, defaultI18n, serverNow());
      const summary = [t(item.config.episodes === 'new' ? 'recordings.newEpisodes' : 'recordings.allEpisodes'),
        ruleScope(item, item.summary?.channel.name, runtime.text), keepLabel(runtime.text, ruleKeep(item.config.options))].filter(Boolean).join(' · ');
      return <RuleCard title={item.config.name} summary={summary} warning={!paused && !!next?.conflict}
        next={paused && facts.tonight > 0 ? t('recordings.rulePausedTonight', {count: facts.tonight}) : next?.text}
        state={{label: t(paused ? 'recordings.paused' : 'recordings.on'), on: !paused}}
        upcoming={paused ? undefined : t('recordings.ruleUpcoming', {count: facts.upcoming})}
        focus={{focusId: item.id, testID: `recordings-rule-${item.id}`, node: refRow(item.id), ...watchFocus(item.id, index, rules.length)}}
        onPress={() => openRule(item)}/>;
    }}
  /> : <RecordingList key={`rows:${query.view}:${query.group ?? ''}`} {...scroll} fade={theme.isTV && query.view === 'history'} listRef={list}
    data={rowData} keyExtractor={item => item.kind === 'heading' ? item.id : item.recording.id}
    renderItem={({item, index}) => {
      if (item.kind === 'heading') return <RecordingSection title={item.title}/>;
      const recording = item.recording, history = query.view === 'history', title = recording.programme.title;
      const reason = reasonLabels[recording.reason], status = t(stateLabels[recording.state]);
      const openDetails = () => open(recording, 'details', window), openMenu = () => open(recording, 'menu', window);
      const focus = {focusId: recording.id, testID: `recordings-row-${recording.id}`, node: refRow(recording.id),
        onLongPressAction: openMenu, ...watchFocus(recording.id, index, rowData.length)};
      const range = programmeTimeRange(Date.parse(recording.programme.start ?? recording.start), Date.parse(recording.programme.end ?? recording.end), runtime.text)?.text;
      const time = history ? defaultI18n.formatDate(recording.programme.start, {month: 'short', day: 'numeric'}) : defaultI18n.time(recording.programme.start ?? recording.start);
      if (recording.state === 'conflicted') return <ConflictCard title={title} body={reason ? t(reason) : status}
        action={{label: t('web.dvr.seeConflict'), onPress: openDetails, focus: {...focus, focusId: `${recording.id}.resolve`}}}/>;
      return <KitRecordingRow time={time} range={range} title={title}
        source={recording.channel?.name} detail={history && reason ? t(reason) : query.group ? keptOrWatched(recording) : undefined}
        artwork={runtime.artwork(recording.programme.image)}
        status={history || recording.state !== 'scheduled' ? {text: status, tone: activeRecording(recording) ? 'record' : undefined} : undefined}
        focus={focus} onPress={openDetails}/>;
    }}
  />;
  const recordingsBody = <RecordingResults region={{id: body, entry: first, orientation: 'free', edges: {left: ids.rail, up: tabs},
    onFocusChange: value => {bodyFocused.current = value;}, onLayout: entryRegion === body ? enter : undefined}}>
      {state.error !== undefined ? <StateView testID="recordings-error" {...runtime.error(state.error, () => {void controller.load(); void controller.loadFacts();}, 'live')}/>
        : query.view === 'storage' ? state.storage && <RecordingSpaceFacts {...storageFacts(state.storage)}
          remaining={t('recordings.hoursLeft', {count: state.storage.hoursLeft})}
          policy={ports.context.account.account.role === 'owner' && ports.context.account.account.primaryProfileId === controller.lifetime.authority.profileId && state.storage.keepRule
            ? t('next.live.keepRule', {rule: state.storage.keepRule}) : undefined}
          warning={state.storage.writeHealthy ? undefined : t('recordings.writeUnhealthy')}/>
        : state.loading && !state.page && !state.groups ? <Quiet><StateView title={t('recordings.loading')}/></Quiet>
        : unavailable ? <StateView title={t('recordings.unavailableTitle')} body={t('recordings.unavailableBody')}/>
        : isEmpty ? <StateView testID="recordings-empty" title={t(emptyTitle)} body={emptyBody && t(emptyBody)} primaryAction={emptyGuide ? {id: 'guide', testID: 'recordings-open-guide', label: t('web.dvr.openGuide'), onPress: leaveGuide} : undefined}/>
        : entries}
      {state.viewerWindow && <>
        {state.viewerWindow.error !== undefined && <StateView {...runtime.error(state.viewerWindow.error, () => {if (drawnCurrent() && state.viewerWindow) void controller.retryViewerPage(state.viewerWindow, viewerOpening);}, 'live')} testID="recordings-page-error"/>}
        {state.viewerWindow.refreshRequired && <StateView {...runtime.error({code: 'stale_continuation'}, () => {if (state.viewerWindow) restartViewer(state.viewerWindow);}, 'live')} testID="recordings-page-refresh"/>}
        {state.viewerWindow.chunks[0]?.cursor && <Button focusId="recordings-page-previous" testID="recordings-page-previous" onPress={() => {if (drawnCurrent() && state.viewerWindow) void controller.previousViewerPage(state.viewerWindow, viewerOpening);}}>{t('web.pager.previous')}</Button>}
        {(state.viewerWindow.nextCursor || state.viewerWindow.pending) && <Button key="recordings-page-next" variant="glass" focusId="recordings-page-next" testID="recordings-page-next" disabled={state.viewerWindow.pending} focusableWhenDisabled onFocus={() => {if (drawnCurrent() && state.viewerWindow) controller.focusViewerHost(state.viewerWindow);}} onPress={() => {if (drawnCurrent() && state.viewerWindow) void (state.viewerWindow.refreshRequired ? (restartViewer(state.viewerWindow), Promise.resolve()) : state.viewerWindow.error !== undefined ? controller.retryViewerPage(state.viewerWindow, viewerOpening) : controller.nextViewerPage(state.viewerWindow, viewerOpening));}}><>{t(state.viewerWindow.error !== undefined || state.viewerWindow.refreshRequired ? 'next.states.tryAgain' : 'next.settings.loadMore')}</></Button>}
      </>}
    </RecordingResults>;
  const overlays = <>
    <Menu key={menu ? `${menu.recording.id}:${menu.recording.revision}` : 'recordings-menu'} open={menu !== undefined} onOpenChange={(value, reason) => {if (!value && menu) close(menu, reason === 'action' ? 'action' : 'user');}} title={menu?.recording.programme.title} returnFocusRef={opener} testID="recordings-menu" items={menu ? [
      ...(playableRecording(menu.recording) ? [{id: 'play', icon: 'i-play' as const, label: t('action.play'), disabled: state.busy, onSelect: () => {if (overlay.current === menu && askReady(menu)) run(() => play(menu.recording, menu));}}] : []),
      ...(!activeRecording(menu.recording) && playableRecording(menu.recording) ? [{id: 'keep', icon: 'i-bookmark' as const, label: t(menu.recording.keep ? 'web.dvr.stopKeeping' : 'web.dvr.keep'), disabled: state.busy, onSelect: () => {if (overlay.current === menu && askReady(menu)) run(() => controller.keep(menu.recording));}}] : []),
      ...(activeRecording(menu.recording) ? [{id: 'cancel', label: t('recordings.cancelAction'), disabled: state.busy, onSelect: () => run(() => askDelete(menu))}] : state.page?.deletionAvailable ? [{id: 'separator', separator: true as const}, {id: 'delete', icon: 'i-trash' as const, label: t('web.dvr.deleteRecording'), destructive: true, disabled: state.busy, onSelect: () => run(() => askDelete(menu))}] : []),
    ] : []}/>
    <Menu testID="recordings-sort" presentation="sideSheet" open={sortOpen} onOpenChange={value => {if (!value) setSortOpen(false);}} title={t('next.browse.sort')}
      items={(['recent', 'title', 'oldest'] as const).map(value => ({id: value, label: t(sortLabels[value]), selected: value === sort, onSelect: () => selectSort(value)}))}/>
    {detail && <RecordingDetails key={`${detail.recording.id}:${detail.recording.revision}:${detail.query}`} recording={detail.recording} controller={controller} runtime={runtime} parent={detail} isCurrent={() => overlay.current === detail && askReady(detail)} onClose={() => close(detail, 'user')} opener={opener} onPlay={() => {if (overlay.current === detail && askReady(detail)) run(() => play(detail.recording, detail));}} onRemove={() => run(() => askDelete(detail))} deletionAvailable={state.page?.deletionAvailable === true}/>}
    <RecordingConfirm key={confirm ? `${confirm.recording.id}:${confirm.recording.revision}` : 'recordings-confirm'} open={confirm !== undefined}
      onClose={() => {if (!controller.getSnapshot().busy) closeConfirm();}} cancel={cancel} returnFocus={opener} busy={state.busy}
      title={confirm ? t(confirm.preview ? 'web.dvr.deleteTitle' : 'web.dvr.cancelTitle', {title: confirm.recording.programme.title}) : ''}
      description={confirm ? confirm.preview ? t(confirm.preview.activeReaders ? 'web.dvr.deleteBodyInUse' : confirm.preview.keep ? 'web.dvr.deleteBodyKept' : 'web.dvr.deleteBody', {size: defaultI18n.bytes(confirm.preview.bytes)}) : t('web.dvr.cancelBody') : undefined}
      cancelLabel={t('web.dvr.keepIt')} confirmLabel={t(confirm?.preview ? 'web.dvr.deleteRecording' : 'recordings.cancelAction')}
      onConfirm={() => {
        if (!confirm || !askReady(confirm.opening)) return;
        const opening = confirm;
        run(async () => {
          const result = opening.preview ? await controller.remove(opening.recording, opening.preview) : await controller.cancel(opening.recording);
          if (result && alive.current && controller.lifetime.current()) {
            controller.unpinViewerRow(opening.opening.pin, opening.opening.scope);
            setConfirm(current => current === opening ? undefined : current);
          }
        });
      }}>
      {state.error !== undefined && <StateView testID="recordings-confirm-error" {...runtime.error(state.error, closeConfirm, 'live')}/>}
    </RecordingConfirm>
    {rule && <RuleEditor key={`${rule.id}:${rule.revision}`} rule={rule} controller={controller} runtime={runtime} opener={opener} isCurrent={ruleCurrent} onClose={closeRule}/>}
    {/* TV viewers keep programme/series recording and rule editing; the administrative
        new-rule sheet is not a TV surface and never mounts here. */}
    {owner && !theme.isTV && <NewRuleSheet context={ports.context} runtime={runtime} open={newRule} onOpenChange={setNewRule} onSaved={() => {setNewRule(false); void controller.load({view: 'rules'});}}/>}
  </>;
  return {title: group?.title ?? t(expandedTouch ? 'search.chip.liveTV' : 'recordings.title'),
    back: theme.isTV || expandedTouch && !group ? undefined : {label: t(group ? 'recordings.title' : 'search.chip.liveTV'), onPress: group ? back : leaveGuide, testID: 'recordings-back'},
    actions: expandedTouch && !group ? <NativeLiveTabs value="recordings" t={t} onGuide={leaveGuide}/> : undefined,
    facts: storageHeader, tabs: viewsHeader, list: recordingsBody, overlay: overlays,
    footer: !theme.isTV && usage && headerStorage && <RecordingStorageSummary title={t('recordings.storage')} {...usage}
      remaining={t('recordings.hoursLeft', {count: headerStorage.hoursLeft})} onPress={() => select('storage')}/>,
    testID: theme.isTV ? 'tv-live-recordings' : 'phone-live-recordings'};
}
