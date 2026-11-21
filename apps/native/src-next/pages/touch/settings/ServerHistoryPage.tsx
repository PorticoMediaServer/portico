import React from 'react';
import {Button, Select, TextField} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame, StateRow} from '../../../kit/frames';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {SettingsHistoryFilter, SettingsHistoryList, SettingsHistoryToolbar} from '../../../kit/settings/SettingsHistoryList';
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
  const filters = <SettingsHistoryToolbar search={<TextField testID="server-history-search" accessibilityLabel={history.searchLabel} placeholder={history.searchLabel} variant="search" value={history.search}
    onChangeText={history.setSearch} maxLength={160} returnKeyType="search" onSubmitEditing={submitSearch}/>}
    actions={<OverlayFootRow layout="halves">
      <Button variant="glass" size="compact" onPress={submitSearch}>{history.searchLabel}</Button>
      <Button variant="glass" size="compact" disabled={state.phase === 'loading'} onPress={() => source.refresh()}>{t('action.refresh')}</Button>
    </OverlayFootRow>}
    filters={[
      <SettingsHistoryFilter key="media" testID="server-history-media" rowLabel={labels.media} accessibilityLabel={labels.media}
        value={state.query.media ?? 'all'} options={history.media} onValueChange={value => change({media: value === 'all' ? undefined : value})}/>,
      <SettingsHistoryFilter key="person" testID="server-history-person" rowLabel={labels.person} accessibilityLabel={labels.person}
        value={state.query.accountId ?? ''} options={history.people} onValueChange={value => change({accountId: value || undefined})}/>,
      <SettingsHistoryFilter key="period" testID="server-history-period" rowLabel={labels.period} accessibilityLabel={labels.period}
        value={state.query.period} options={history.period} onValueChange={period => {
          if (period === 'today' || period === '7d' || period === '30d' || period === 'all') change({period});
        }}/>,
    ]} sortLabel={t('web.filters.sortBy')} doneLabel={t('action.done')} sort={[
      <Select key="sort" testID="server-history-sort" rowLabel={t('web.filters.sortBy')} accessibilityLabel={t('web.filters.sortBy')}
        value={state.query.sort} options={history.sort} onValueChange={sort => change({sort})}/>,
      <Select key="direction" testID="server-history-direction" rowLabel={t('builder.preset.order')} accessibilityLabel={t('builder.preset.order')}
        value={state.query.direction} options={history.direction} onValueChange={direction => {
          if (direction === 'asc' || direction === 'desc') change({direction});
        }}/>,
    ]}>
    {retry}
    {state.peopleCursor && <Button variant="glass" size="compact" align="stretch" loading={state.peopleLoading}
      onPress={() => source.morePeople()}>{t('web.people.morePeople')}</Button>}
    {state.peopleError !== undefined && <StateView variant="inline" {...runtime.error(state.peopleError, () => source.retryPeople(), 'server-console')}/>}
    {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, () => source.retry(), 'server-console')}/>}
  </SettingsHistoryToolbar>;
  const empty = state.phase === 'ready' ? <SectionCard><StateRow kind="empty" icon="i-clock" title={history.empty}/></SectionCard>
    : state.phase === 'loading' || state.phase === 'idle' ? <SectionCard><StateRow kind="loading"/></SectionCard> : null;
  return <SettingsPageFrame {...frame} title={title} panel={<SettingsHistoryList entries={state.entries} row={history.row} columns={history.columns} header={filters} empty={empty}
    footer={state.nextCursor && <PanelBlock><Button variant="glass" align="stretch" loading={state.phase === 'loading'}
      disabled={state.phase !== 'ready'} onPress={() => source.more()}>{t('action.showMore')}</Button></PanelBlock>}/>} />;
}
