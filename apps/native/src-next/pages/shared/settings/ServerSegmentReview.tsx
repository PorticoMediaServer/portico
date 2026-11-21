import React from 'react';
import {segmentClock, segmentKinds, type SegmentKind} from '../../../../../../packages/client-core/src/segment-review';
import {Button, Select, TextField} from '../../../kit/controls';
import {PanelBlock, SectionCard, StateRow} from '../../../kit/frames';
import {BottomSheet, Dialog} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {SegmentReviewInset, SegmentReviewNotice, SegmentReviewTitle} from '../../../kit/settings/SegmentReviewParts';
import {SettingsHelp} from '../../../kit/settings/SettingsParts';
import {SettingsRow} from '../../../kit/shell';
import {SegmentWindows} from '../../../kit/shell/SegmentWindows';
import {StateView} from '../../../kit/states';
import type {NativeSettingsPanelDeps} from '../../../screens/settings-live/settings-panels';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useSegmentPoster, type useSegmentReview} from './useSegmentReview';

type Props = Pick<NativeSettingsPanelDeps, 'context' | 'lifetime' | 't'> & {runtime: NativeShellRuntime; model: ReturnType<typeof useSegmentReview>};

/** The settings body for reviewing and editing the server's Intro and Credits markers. */
export function ServerSegmentReview({context, lifetime, t, runtime, model}: Props) {
  const {state, review, season} = model;
  const reviewLabel = t('web.segments.review');
  return <>
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
        {!state.status.libraries.length && <StateRow kind="empty" icon="i-film" title={t('web.segments.noLibraries')}/>}
        {state.status.libraries.map((library, index) => <SettingsRow key={library.libraryId} first={index === 0} label={library.name}
          description={[t(library.itemKind === 'movie' ? 'web.segments.libraryModeFilms' : 'web.segments.libraryMode', {mode: library.mode}),
            ...(library.choice === 'server' ? [t('web.segments.followsServer')] : [])]}
          control={<Button variant="glass" size="compact" onPress={() => model.open(library.libraryId)}>{reviewLabel}</Button>}/>)}
      </SectionCard>
    </>}
    {state.page && <>
      <SectionCard>
        {!state.page.titles.length && <StateRow kind="empty" icon="i-film" title={t('web.segments.noTitles')}/>}
        {state.page.titles.map((title, index) => <ReviewTitle key={title.id} id={title.id} title={title.title} first={index === 0}
          libraryId={state.page!.libraryId} context={context} lifetime={lifetime} runtime={runtime}
          facts={[t('web.segments.titleFiles', {measured: title.files.complete, total: title.files.complete + title.files.pending + title.files.failed + title.files.unsupported}),
            ...(title.markers.suggested > 0 ? [t('web.segments.titleSuggested', {count: title.markers.suggested})] : []),
            ...(title.markers.approved > 0 ? [t('web.segments.titleApproved', {count: title.markers.approved})] : []),
            ...(title.markers.automatic > 0 ? [t('web.segments.titleAutomatic', {count: title.markers.automatic})] : [])]}
          onPress={() => model.open(state.libraryId, title.id)}/>)}
      </SectionCard>
      {!!state.page.nextCursor && <PanelBlock>
        <Button variant="text" disabled={state.loading} onPress={model.more}>{t('web.segments.showMore')}</Button>
      </PanelBlock>}
    </>}
    {review && <>
      <SectionCard>
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
          <SegmentReviewInset><SegmentWindows entry={entry} windows={model.windows(entry)} saving={state.busy}
            onKindChange={(marker, kind) => model.changeKind(entry, marker, kind)} onSave={(marker, bounds) => model.changeBounds(entry, marker, bounds)}
            onApprove={marker => model.approve(entry, marker)} onDismiss={marker => model.dismiss(entry, marker)}
            onWatch={(marker, fromUS) => model.watch(entry, marker, fromUS)} onAdd={() => model.add(entry)}/></SegmentReviewInset>
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

function ReviewTitle({title, first, facts, onPress, ...props}: Parameters<typeof useSegmentPoster>[0] & {title: string; first: boolean; facts: readonly string[]; onPress: () => void}) {
  return <SegmentReviewTitle title={title} first={first} facts={facts} onPress={onPress} artwork={useSegmentPoster(props)}/>;
}
