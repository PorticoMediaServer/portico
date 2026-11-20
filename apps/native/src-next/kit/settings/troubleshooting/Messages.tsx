import React, {useEffect, useState} from 'react';
import {Text, View} from 'react-native';
import {KEEP_MESSAGE_DAYS, MESSAGE_KINDS, type KeepMessageDays, type MessageKind} from '../../../../../../packages/client-core/src/server-admin/messages-source';
import {LOG_DETAIL_MINUTES, logLevels, type LogDetailMinutes} from '../../../../../../packages/client-core/src/server-administration';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {useMessageControls, type Group} from '../../../pages/shared/settings/useTroubleshootingMessages';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {Button, Chip, FormError, Select, Switch} from '../../controls';
import {PanelBlock, SectionCard} from '../../frames';
import {Menu} from '../../overlays';
import {SettingsRow} from '../../shell';
import {alpha} from '../../shell/visual';
import {useTheme} from '../../theme';
import {SettingsHelp} from '../SettingsParts';

export function Messages({group, runtime, pageScrollY = 0, scrollToY}: {
  group: Group; runtime: NativeShellRuntime; pageScrollY?: number; scrollToY?: (y: number) => void;
}) {
  // Keep the line being read in place when a newer line arrives.
  const [rootY, setRootY] = useState(0), [logY, setLogY] = useState(0);
  useEffect(() => {group.source.setAtTop(pageScrollY - (rootY + logY) < 48);}, [group, pageScrollY, rootY, logY]);
  const {state, failure, source, t, filter, filterRef, openFilter, closeFilter, until, recording, detail, keep} = useMessageControls(group, runtime);
  const theme = useTheme(), gap = theme.space.controlGap;
  const options = filter?.kind === 'category'
    ? [{value: '', label: t('web.logs.everything')}, ...MESSAGE_KINDS.map(value => ({value, label: t(`web.logs.category.${value}`)}))]
    : [{value: '', label: t('web.logs.allLevels')}, ...logLevels.map(value => ({value, label: t(`web.logs.level.${value}`)}))];
  const meta = {fontFamily: 'monospace', color: theme.colors.textTertiary} as const;

  return <View style={{gap: theme.space.md}} testID="server-troubleshooting-messages" onLayout={event => setRootY(event.nativeEvent.layout.y)}>
    <SectionCard label={t('web.logs.messages')} help={t('web.logs.messagesLede')}>
      <SettingsRow first label={recording ? t('next.settingsSpec.server.troubleshooting.groups.0.rows.0.label') : t('settings.server.detailWindow.forMinutes', {minutes: state.detailMinutes})}
        description={recording && until ? t('settings.server.detailWindow.until', {time: defaultI18n.time(Date.parse(until))}) : t('settings.server.detailWindow.help')}/>
      {group.owner && <PanelBlock><Button size="compact" loading={state.changing} disabled={state.changing} onPress={detail}>
        {t(recording ? 'settings.server.detailWindow.stop' : 'settings.server.detailWindow.start')}
      </Button></PanelBlock>}
      <PanelBlock>
        <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap}}>
          <Chip variant="pill" trailingIcon="i-down" onPress={() => openFilter('level')} accessibilityLabel={t('web.logs.level')}>
            {state.level ? t(`web.logs.level.${state.level}`) : t('web.logs.allLevels')}
          </Chip>
          <Chip variant="pill" trailingIcon="i-down" onPress={() => openFilter('category')} accessibilityLabel={t('feedback.category')}>
            {state.kind ? t(`web.logs.category.${state.kind}`) : t('web.logs.everything')}
          </Chip>
          <Switch checked={state.live} onCheckedChange={value => {if (group.current()) group.setLive(value);}}>{t('next.cards.live')}</Switch>
          {state.live && <Text style={[theme.type.rowHelp, {color: theme.colors.healthy}]}>{t('web.logs.following')}</Text>}
          <Button size="small" variant="text" disabled={state.phase === 'loading'} onPress={() => {
            if (group.current()) {group.clearFailure(); void source.load();}
          }}>{t('web.selection.refresh')}</Button>
        </View>
        {(failure || state.phase === 'error') && <FormError placement="inline">{t('error.unavailable')}</FormError>}
        {state.phase === 'loading' && !state.lines.length && <SettingsHelp>{t('web.logs.loadingMessages')}</SettingsHelp>}
        {state.phase === 'ready' && !state.lines.length && <SettingsHelp>{t('web.logs.noMessages')}</SettingsHelp>}
        {state.waiting > 0 && <Button size="small" variant="glass" testID="server-messages-new-lines" onPress={() => {
          if (group.current()) {source.showWaiting(); scrollToY?.(rootY + logY);}
        }}>{t('web.logs.newLines', {count: state.waiting})}</Button>}
      </PanelBlock>
    </SectionCard>
    {state.lines.length > 0 && <View onLayout={event => setLogY(event.nativeEvent.layout.y)}>
      <SectionCard>
        {state.lines.map((row, index) => <View key={row.sequence} style={{gap: theme.space.xs, padding: theme.space.sm,
          backgroundColor: row.level === 'error' ? alpha(theme.colors.danger, .07) : theme.colors.messageLog,
          borderTopWidth: index ? theme.space.hairline : 0, borderColor: theme.colors.lineSoft}}>
          <View style={{flexDirection: 'row', flexWrap: 'wrap', gap}}>
            <Text style={{...theme.type.rowHelp, ...meta}}>{defaultI18n.time(Date.parse(row.at))}</Text>
            <Text style={{...theme.type.rowHelp, ...meta, fontWeight: '700',
              color: row.level === 'error' ? theme.colors.danger : row.level === 'warn' ? theme.colors.amber : theme.colors.textSecondary}}>
              {t(`web.logs.level.${row.level}`)}
            </Text>
            <Text style={{...theme.type.rowHelp, ...meta}}>{t(`server.logCategory.${row.kind}`)}</Text>
          </View>
          <Text selectable style={[theme.type.rowLabel, {color: theme.colors.text}]}>{row.message}</Text>
          {row.detail && <Text selectable style={[theme.type.rowHelp, meta]}>{row.detail}</Text>}
        </View>)}
      </SectionCard>
    </View>}
    {(group.owner && !recording || !!state.keepDays) && <SectionCard><PanelBlock>
      {group.owner && !recording && <Select label={t('next.settingsSpec.server.troubleshooting.advanced.groups.0.rows.1.label')}
        value={String(state.detailMinutes)} options={LOG_DETAIL_MINUTES.map(value => ({value: String(value), label: t('web.dvr.minutes', {count: value})}))}
        disabled={state.changing} onValueChange={value => {
          const chosen = Number(value) as LogDetailMinutes;
          if (group.current() && LOG_DETAIL_MINUTES.includes(chosen)) source.chooseLength(chosen);
        }}/>}
      {!!state.keepDays && <Select label={t('settings.server.keepMessages')} value={String(state.keepDays)}
        options={KEEP_MESSAGE_DAYS.map(days => ({value: String(days), label: t('settings.server.keepMessages.days', {days})}))}
        disabled={state.changing} onValueChange={value => {
          const days = Number(value) as KeepMessageDays;
          if (KEEP_MESSAGE_DAYS.includes(days)) keep(days);
        }}/>}
    </PanelBlock></SectionCard>}
    <Menu open={!!filter && filterRef.current === filter && group.current()} onOpenChange={open => {if (!open) closeFilter();}} presentation="sheet"
      title={t(filter?.kind === 'category' ? 'feedback.category' : 'web.logs.level')}
      items={options.map(option => ({id: option.value || 'all', label: option.label,
        selected: option.value === (filter?.kind === 'category' ? state.kind : state.level),
        onSelect: () => {
          if (!filter || filterRef.current !== filter || !group.current()) return;
          closeFilter();
          if (filter.kind === 'category') void group.select(source.getSnapshot().level, option.value as MessageKind | '');
          else void group.select(option.value as typeof state.level, source.getSnapshot().kind);
        }}))}/>
  </View>;
}
