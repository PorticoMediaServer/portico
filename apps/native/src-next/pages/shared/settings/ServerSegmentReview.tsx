import React from 'react';
import {segmentClock, segmentKinds, type SegmentKind} from '../../../../../../packages/client-core/src/segment-review';
import {Button, Select, TextField} from '../../../kit/controls';
import {PanelBlock, SectionCard, StateRow} from '../../../kit/frames';
import {BottomSheet, Dialog} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {SegmentReviewNotice, SegmentReviewTitle} from '../../../kit/settings/SegmentReviewParts';
import {SettingsHelp} from '../../../kit/settings/SettingsParts';
import {SettingsRow} from '../../../kit/shell';
import {SegmentWindows} from '../../../kit/shell/SegmentWindows';
import {StateView} from '../../../kit/states';
import type {NativeSettingsPanelDeps} from '../../../screens/settings-live/settings-panels';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useSegmentPoster, useSegmentReview} from './useSegmentReview';

type Props = NativeSettingsPanelDeps & {runtime: NativeShellRuntime};

/** The settings body for reviewing and editing the server's Intro and Credits markers. */
export function ServerSegmentReview({context, lifetime, t, runtime, ...deps}: Props) {
  const model = useSegmentReview({context, lifetime, t, runtime, ...deps}), {state, review, season} = model;
  const reviewLabel = t('web.segments.review');
  return <>
    {(state.libraryId || state.titleId) && <PanelBlock>
      <Button variant="text" disabled={state.busy} onPress={model.goBack}>{t('action.back')}</Button>
    </PanelBlock>}
    {state.error ? <StateView variant="inline" {...runtime.error(state.error, model.reload)}/> : null}
    {state.loading && <SectionCard><StateRow kind="loading"/></SectionCard>}
    {state.notice && <SegmentReviewNotice text={t(state.notice.kind === 'approved' ? 'web.segments.approvedCount' : 'web.segments.detectAgainQueued', {count: state.notice.count})}/>}
    {!state.libraryId && state.status && <>
      <SectionCard label={t('web.segments.progress')}
        help={t(state.status.mode === 'off' ? 'web.segments.stateOff' : state.status.catchUpComplete ? 'web.segments.stateDone' : 'web.segments.stateWorking')}>
        <SettingsRow first label={t('web.segments.measured')} value={String(state.status.files.complete)}/>
        <SettingsRow label={t('web.segments.waiting')} value={String(state.status.files.pending)}/>
        <SettingsRow label={t('web.segments.unreadable')} value={String(state.status.files.failed + state.status.files.unsupported)}/>
      </SectionCard>
      <SectionCard label={t('web.segments.libraries')}>
        {state.status.libraries.map((library, index) => <SettingsRow key={library.libraryId} first={index === 0} label={library.name}
          description={[t(library.itemKind === 'movie' ? 'web.segments.libraryModeFilms' : 'web.segments.libraryMode', {mode: library.mode}),
            ...(library.choice === 'server' ? [t('web.segments.followsServer')] : [])]}
          control={<Button variant="glass" size="compact" onPress={() => model.open(library.libraryId)}>{reviewLabel}</Button>}/>)}
      </SectionCard>
    </>}
    {state.page && <>
      {!state.page.titles.length && <SectionCard><StateRow kind="empty" icon="i-film" title={t('web.segments.noTitles')}/></SectionCard>}
      {state.page.titles.map(title => <SectionCard key={title.id}>
        <ReviewTitle id={title.id} title={title.title} libraryId={state.page!.libraryId} context={context} lifetime={lifetime} runtime={runtime}/>
        <SettingsRow label={t('web.segments.titleFiles', {measured: title.files.complete, total: title.files.complete + title.files.pending + title.files.failed + title.files.unsupported})}
          description={[
            ...(title.markers.suggested > 0 ? [t('web.segments.titleSuggested', {count: title.markers.suggested})] : []),
            ...(title.markers.approved > 0 ? [t('web.segments.titleApproved', {count: title.markers.approved})] : []),
            ...(title.markers.automatic > 0 ? [t('web.segments.titleAutomatic', {count: title.markers.automatic})] : []),
          ]} control={<Button variant="glass" size="compact" onPress={() => model.open(state.libraryId, title.id)}>{reviewLabel}</Button>}/>
      </SectionCard>)}
      {!!state.page.nextCursor && <PanelBlock>
        <Button variant="text" disabled={state.loading} onPress={model.more}>{t('web.segments.showMore')}</Button>
      </PanelBlock>}
    </>}
    {review && <>
      <SectionCard label={review.title}>
        {review.seasons.length > 1 && <SettingsRow first label={t('web.segments.episodes')}
          control={<Select accessibilityLabel={t('web.segments.episodes')} value={String(season?.number)}
            options={review.seasons.map(value => ({value: String(value.number), label: value.title || t(value.number === 0 ? 'web.segments.specials' : 'web.segments.season', {number: value.number})}))}
            onValueChange={model.selectSeason}/>}/>}
        <PanelBlock><OverlayFootRow layout="halves">
          <Button variant="glass" disabled={state.busy} onPress={model.approveSeason}>{t('web.segments.approveSeason')}</Button>
          <Button variant="glass" disabled={state.busy} onPress={() => model.setConfirm(true)}>{t('web.segments.detectAgain')}</Button>
        </OverlayFootRow></PanelBlock>
      </SectionCard>
      {season?.entries.map(entry => {
        const remaining = entry.markers.filter(marker => !marker.approved && !marker.automaticSafe && marker.viewerVisible).length;
        return <SectionCard key={entry.itemId} label={segmentClock(entry.durationUS)}
          help={remaining ? t('web.segments.toReview', {count: remaining}) : undefined}>
          <SegmentWindows entry={entry} windows={model.windows(entry)} saving={state.busy}
            onKindChange={(marker, kind) => model.changeKind(entry, marker, kind)} onSave={(marker, bounds) => model.changeBounds(entry, marker, bounds)}
            onApprove={marker => model.approve(entry, marker)} onDismiss={marker => model.dismiss(entry, marker)}
            onWatch={(marker, fromUS) => model.watch(entry, marker, fromUS)} onAdd={() => model.add(entry)}/>
          {!entry.markers.length && <SettingsHelp>{t('web.segments.entryState', {state: entry.state})}</SettingsHelp>}
        </SectionCard>;
      })}
    </>}
    <Dialog open={model.confirm} onOpenChange={model.setConfirm} title={t('web.segments.detectAgain')} description={t('web.segments.detectAgainHelp')}
      footer={<OverlayFootRow layout="halves">
        <Button variant="glass" onPress={() => model.setConfirm(false)}>{t('action.cancel')}</Button>
        <Button disabled={state.busy} onPress={model.redetect}>{t('web.segments.detectAgain')}</Button>
      </OverlayFootRow>}>{null}</Dialog>
    <BottomSheet open={!!model.adding} onOpenChange={open => {if (!open) model.closeAdd();}} title={t('web.segments.addTitle')}
      description={t('web.segments.timeHelp')} keyboardAvoiding
      footer={<Button disabled={!model.valid || state.busy} onPress={model.save}>{t('action.save')}</Button>}>
      <Select label={t('web.segments.kindLabel')} value={model.kind}
        options={segmentKinds.map(value => ({value, label: t('web.segments.kind', {kind: value})}))}
        onValueChange={value => model.setKind(value as SegmentKind)}/>
      <TextField label={t('web.segments.start')} value={model.start} onChangeText={model.setStart}/>
      <TextField label={t('web.segments.end')} value={model.end} onChangeText={model.setEnd}/>
    </BottomSheet>
  </>;
}

function ReviewTitle({title, ...props}: Parameters<typeof useSegmentPoster>[0] & {title: string}) {
  return <SegmentReviewTitle title={title} artwork={useSegmentPoster(props)}/>;
}
