import React from 'react';
import {Button, Select, TextField} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame, StateRow} from '../../../kit/frames';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {SettingsHistoryList} from '../../../kit/settings/SettingsHistoryList';
import {SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useServerHistory} from '../../shared/settings/useServerHistory';

type Frame = Pick<React.ComponentProps<typeof SettingsPageFrame>, 'back' | 'server'>;

export function ServerHistoryPage({ports, runtime, retry, ...frame}: Frame & {
  ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; retry?: React.ReactNode;
}) {
  const history = useServerHistory({ports, runtime}), {t} = runtime.text;
  const title = t('settings.server.playHistory');
  if (!history) return <SettingsPageFrame {...frame} title={title}><SectionCard><StateRow kind="loading"/></SectionCard></SettingsPageFrame>;
  const {state, source, change, labels, submitSearch} = history;
  const filters = <>
    {retry}
    <PanelBlock><TextField testID="server-history-search" label={history.searchLabel} value={history.search}
      onChangeText={history.setSearch} maxLength={160} returnKeyType="search" onSubmitEditing={submitSearch}/></PanelBlock>
    <PanelBlock><OverlayFootRow layout="halves">
      <Button variant="glass" size="compact" onPress={submitSearch}>{history.searchLabel}</Button>
      <Button variant="glass" size="compact" disabled={state.phase === 'loading'} onPress={() => source.refresh()}>{t('action.tryAgain')}</Button>
    </OverlayFootRow></PanelBlock>
    <SectionCard>
      <SettingsRow first label={labels.media} control={<Select testID="server-history-media" accessibilityLabel={labels.media}
        value={state.query.media ?? 'all'} options={history.media} onValueChange={value => change({media: value === 'all' ? undefined : value})}/>}/>
      <SettingsRow label={labels.person} control={<Select testID="server-history-person" accessibilityLabel={labels.person}
        value={state.query.accountId ?? ''} options={history.people} onValueChange={value => change({accountId: value || undefined})}/>}/>
      {state.peopleCursor && <Button variant="glass" size="compact" align="stretch" loading={state.peopleLoading}
        onPress={() => source.morePeople()}>{t('web.people.morePeople')}</Button>}
      {state.peopleError !== undefined && <StateView variant="inline" {...runtime.error(state.peopleError, () => source.retryPeople(), 'server-console')}/>}
      <SettingsRow label={labels.period} control={<Select testID="server-history-period" accessibilityLabel={labels.period}
        value={state.query.period} options={history.period} onValueChange={period => {
          if (period === 'today' || period === '7d' || period === '30d' || period === 'all') change({period});
        }}/>}/>
      <SettingsRow label={t('web.filters.sortBy')} control={<Select testID="server-history-sort" accessibilityLabel={t('web.filters.sortBy')}
        value={state.query.sort} options={history.sort} onValueChange={sort => change({sort})}/>}/>
      <SettingsRow label={t('builder.preset.order')} control={<Select testID="server-history-direction" accessibilityLabel={t('builder.preset.order')}
        value={state.query.direction} options={history.direction} onValueChange={direction => {
          if (direction === 'asc' || direction === 'desc') change({direction});
        }}/>}/>
    </SectionCard>
    {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, () => source.retry(), 'server-console')}/>}
  </>;
  const empty = state.phase === 'ready' ? <SectionCard><StateRow kind="empty" icon="i-clock" title={history.empty}/></SectionCard>
    : state.phase === 'loading' || state.phase === 'idle' ? <SectionCard><StateRow kind="loading"/></SectionCard> : null;
  return <SettingsPageFrame {...frame} title={title} panel={<SettingsHistoryList entries={state.entries} row={history.row} header={filters} empty={empty}
    footer={state.nextCursor && <PanelBlock><Button variant="glass" align="stretch" loading={state.phase === 'loading'}
      disabled={state.phase !== 'ready'} onPress={() => source.more()}>{t('action.showMore')}</Button></PanelBlock>}/>} />;
}
