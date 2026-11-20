import React, {type RefObject} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {importSkipped, liveSourceState} from '../../../../../packages/client-core/src/server-admin/live-words';
import type {useLiveOwnerPage} from '../../pages/shared/settings/useLiveOwnerPage';
import type {NativeLiveOwnerController} from '../../screens/settings-live/live-owner-controller';
import type {LiveSource} from '../../screens/settings-live/live-owner-model';
import type {NativeShellRuntime} from '../../shell/runtime';
import {Button} from '../controls';
import {PanelBlock, SectionCard} from '../frames';
import {SettingsRow} from '../shell';
import {StateView} from '../states';
import {useTheme} from '../theme';
import {SettingsHelp} from './SettingsParts';

type Model = ReturnType<typeof useLiveOwnerPage>;

export function LiveSourcesSection({model, controller, runtime}: {model: Model; controller: NativeLiveOwnerController; runtime: NativeShellRuntime}) {
  const {t, state, sources} = model, theme = useTheme();
  const openAdd = () => model.setAddMenu(true), empty = state.list && !sources.length;
  const checked = state.checkedAt === undefined ? undefined : t('web.servers.checked', {
    when: defaultI18n.relativeTime(state.checkedAt, undefined, {inSentence: true}),
  });
  return <>
    <PanelBlock><SettingsHelp>{t('web.liveTv.sourcesLede')}</SettingsHelp></PanelBlock>
    {!empty && <PanelBlock><Button ref={model.addButton} testID="live-owner-add" align="stretch" icon="i-plus" onPress={openAdd}>{t('web.liveTv.addSource')}</Button></PanelBlock>}
    {state.error !== undefined && <StateView testID="live-owner-error" variant="inline" {...runtime.error(state.error, () => {void controller.load();})}/>}
    <SectionCard label={t('web.liveTv.sources')} help={checked}>
      {state.loading && !state.list && <SettingsRow first label={t('web.librarySettings.loading')}/>}
      {empty && <View testID="live-owner-empty">
        <SettingsRow first label={t('web.live.empty.noSourcesTitle')} description={t('web.liveTv.noSourcesHelp')}/>
        <PanelBlock><Button ref={model.addButton} testID="live-owner-empty-add" align="stretch" icon="i-plus" onPress={openAdd}>{t('web.liveTv.addSource')}</Button></PanelBlock>
      </View>}
      {state.notice && <PanelBlock><Text accessibilityLiveRegion="polite" style={[theme.type.rowHelp, {color: theme.colors.healthy}]}>{state.notice}</Text></PanelBlock>}
      {sources.map((value, index) => <LiveSourceRow key={value.id} source={value} first={index === 0}
        model={model} opener={model.menuOpener}/>)}
    </SectionCard>
  </>;
}

function LiveSourceRow({source, first, model, opener}: {source: LiveSource; first: boolean; model: Model; opener: RefObject<View | null>}) {
  const {t} = model, theme = useTheme();
  const status = liveSourceState(source, model.refresh(source.id));
  const skipped = importSkipped(t, id => defaultI18n.has(id), source.diagnostics);
  const description = [t('server.live.channelCount', {count: source.channels}), t('server.live.programCount', {count: source.programmes})];
  if (source.tunerCount > 0) description.push(t(source.tunerCountMode === 'discovered' ? 'web.liveTv.tunersDetected' : 'server.live.tunerCount', {count: source.tunerCount}));
  if (source.publishedAt) description.push(t('server.live.guideUpdated', {when: defaultI18n.dateTime(source.publishedAt)}));
  const color = theme.colors[status.tone === 'healthy' ? 'healthy' : status.tone === 'warning' ? 'amber' : status.tone === 'danger' ? 'record' : 'textTertiary'];
  return <View style={{paddingBottom: theme.space.xs}}>
    <View style={{flexDirection: 'row', alignItems: 'center', borderColor: theme.colors.lineSoft, borderTopWidth: first ? 0 : theme.space.hairline}}>
      <View style={{flex: 1}}>
        <SettingsRow testID={`live-owner-source-${source.id}`} first icon="i-live" label={source.name} description={description}
          navigates onPress={() => model.setConfigure(source)}/>
      </View>
      <Button ref={node => {if (node) model.rowOpeners.current.set(source.id, node); else model.rowOpeners.current.delete(source.id);}}
        testID={`live-owner-more-${source.id}`} variant="icon" size="small" icon="i-more"
        accessibilityLabel={t('server.row.actions', {name: source.name})}
        onPress={() => {opener.current = model.rowOpeners.current.get(source.id) ?? null; model.setMenu(source);}}/>
    </View>
    <PanelBlock>
      <Text style={[theme.type.rowHelp, {color}]}>{t(status.id)}</Text>
      {!!skipped && <SettingsHelp testID={`live-owner-skipped-${source.id}`}>{[skipped.summary, ...skipped.reasons].join(' ')}</SettingsHelp>}
    </PanelBlock>
  </View>;
}
