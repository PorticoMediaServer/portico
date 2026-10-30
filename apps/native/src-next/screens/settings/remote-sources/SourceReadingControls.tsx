import React, {useState} from 'react';
import {Text, View} from 'react-native';
import Svg, {Path} from 'react-native-svg';
import type {SourceReadSettings, SourceReadValues} from '../../../../../../packages/client-core/src/remote-sources';
import type {SourceChoiceComparison, SourceChoiceDefinition} from '../../../../../../packages/client-core/src/remote-source-choice';
import {Icon} from '../../../kit/Icon';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls/Button';
import {Focusable} from '../../../kit/controls/Focusable';
import {TextField} from '../../../kit/controls/TextField';
import {BottomSheet} from '../../../kit/overlays/BottomSheet';
import {alpha} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import {LibraryGroup, LibraryRow, SectionTitle} from '../../settings-live/owner-libraries/controls';
import {sourceLimitValue, type SourceLimit} from './reading-presentation';

const names = ['list', 'once', 'everything', 'ask'] as const;

const fields: readonly SourceLimit[] = ['connections', 'requestsPerSecond', 'bytesPerSecond'];
const readLabel = 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options';

export function SourceReadingControls({values, profile, comparison, baseline, disabled, choicesDisabled = disabled, onSelect, onLimit, onReset, t}: {
  values: SourceReadValues; profile: SourceReadSettings; comparison?: SourceChoiceComparison; baseline?: SourceReadValues; disabled: boolean; choicesDisabled?: boolean; onSelect: (definition: SourceChoiceDefinition) => void;
  onLimit: (field: SourceLimit, value: number) => void; onReset?: () => void; t: typeof defaultI18n.t;
}) {
  const theme = useTheme();
  const readingChanged = baseline !== undefined && (['scanDepth', ...fields] as const).some(field => comparison?.applicableFields.includes(field) && values[field] !== profile.values[field]);
  const selected = comparison?.changedFields.length === 0 && !readingChanged ? comparison.name : undefined;
  const definitions = profile.configurationGeneration && profile.choiceApplicableFields?.length ? profile.choiceDefinitions ?? [] : [];
  const applicable = new Set(profile.choiceApplicableFields);
  const spec = 'next.settingsSpec.server.remote-sources.panels.source';
  const [editing, setEditing] = useState<SourceLimit>(), [text, setText] = useState('');
  const label = (field: SourceLimit) => t(field === 'connections' ? 'next.remoteSources.connections' : field === 'requestsPerSecond' ? 'next.remoteSources.requestsPerSecond' : 'next.remoteSources.downloadSpeed');
  const valueText = (field: SourceLimit, value: number) => field === 'bytesPerSecond' ? value ? `${value / 1000000} ${t('next.remoteSources.megabytesPerSecond')}` : t('next.remoteSources.noLimit') : String(value);
  const number = editing ? sourceLimitValue(editing, text) : undefined;
  return <>
    {definitions.map(definition => {
      const preset = definition.name, index = names.indexOf(preset), checked = selected === preset;
      const interval = [900, 3600, 21600, 86400, 604800, 0].indexOf(definition.advanced.scanIntervalSeconds);
      const scanning = applicable.has('scanIntervalSeconds') && interval >= 0 ? [t(`${spec}.tabs.3.groups.0.rows.0.control.options.${interval}.label` as never), ...(applicable.has('watchForChanges') && definition.advanced.watchForChanges ? [t(`${spec}.tabs.3.groups.0.rows.1.label` as never)] : [])].join(' · ') : undefined;
      const analysis = applicable.has('scanDepth') ? t(definition.reading.scanDepth === 'file_list_only' ? 'next.remoteSources.factNone' : definition.reading.scanDepth === 'complete' ? 'next.remoteSources.factWholeFiles' : 'next.remoteSources.factNewFilesOnce') : undefined;
      const caching = applicable.has('cacheBytes') ? definition.advanced.cacheBytes ? defaultI18n.bytes(definition.advanced.cacheBytes) : t(`${spec}.tabs.1.groups.0.rows.1.control.options.0.label` as never) : undefined;
      const facts = [[t('next.remoteSources.scanning'), scanning], [t('next.remoteSources.factAnalysis'), analysis], ['Caching', caching]];
      return <Focusable key={preset} testID={`source-reading-${preset}`} accessibilityRole="radio" accessibilityLabel={t(`${readLabel}.${index}.label` as never)} accessibilityState={{checked, disabled: choicesDisabled}} disabled={choicesDisabled} onPress={() => onSelect(definition)} focusKind="card" focusRadius={14}
        style={{padding: 14, borderRadius: 14, borderWidth: 1.5, borderColor: checked ? theme.colors.accentStrong : theme.colors.lineSoft, backgroundColor: checked ? alpha(theme.colors.accent, .12) : theme.colors.raised, flexDirection: 'row', gap: 12}}>
        <View style={{width: 21, height: 21, borderRadius: 11, borderWidth: 2, borderColor: checked ? theme.colors.accentStrong : theme.colors.lineStrong, alignItems: 'center', justifyContent: 'center'}}>{checked && <View style={{width: 9, height: 9, borderRadius: 5, backgroundColor: theme.colors.text}}/>}</View>
        {preset === 'list' || preset === 'once' ? <Icon name={preset === 'list' ? 'i-list' : 'i-eye'} size={20} color={theme.colors.textSecondary}/>
          : <Svg width={20} height={20} viewBox="0 0 24 24" accessibilityElementsHidden importantForAccessibility="no-hide-descendants"><Path fill="none" stroke={theme.colors.textSecondary} strokeWidth={1.7} strokeLinecap="round" strokeLinejoin="round" d={preset === 'everything' ? 'M13 2 4 14h7l-1 8 9-12h-7z' : 'M8 13V5.5a1.5 1.5 0 0 1 3 0V11m0-1.5V4a1.5 1.5 0 0 1 3 0v6m0-4.5a1.5 1.5 0 0 1 3 0V12m0-3a1.5 1.5 0 0 1 3 0v6a7 7 0 0 1-7 7h-1.5a6 6 0 0 1-5-2.7L4 14.5a1.6 1.6 0 0 1 2.6-1.8L8 14'}/></Svg>}
        <View style={{flex: 1, gap: 6}}><Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t(`${readLabel}.${index}.label` as never)}</Text>
          {preset === 'once' && <Text style={[theme.type.caption, {color: theme.colors.accentStrong}]}>{t('next.remoteSources.choiceRecommended')}</Text>}
          {facts.filter(([, value]) => value !== undefined).map(([caption, value]) => <View key={caption} style={{flexDirection: 'row', gap: 8}}><Text style={[theme.type.caption, {color: theme.colors.textSecondary, flex: 1}]}>{caption}</Text><Text style={[theme.type.caption, {color: theme.colors.text}]}>{value}</Text></View>)}
        </View>
      </Focusable>;
    })}
    {definitions.length > 0 && !selected && <Text style={[theme.type.caption, {color: theme.colors.accentStrong}]}>{t(`${readLabel}.4.label` as never)}</Text>}
    <SectionTitle action={onReset && baseline && (readingChanged || !!comparison?.changedFields.length) ? <Button variant="text" size="xs" disabled={choicesDisabled} onPress={onReset}>{t('next.remoteSources.resetAll')}</Button> : undefined}>{t('next.remoteSources.connectionLimits')}</SectionTitle>
    <LibraryGroup>{fields.map((field, index) => {
      const changed = baseline !== undefined && comparison?.applicableFields.includes(field) && values[field] !== baseline[field];
      return <View key={field} style={changed ? {borderWidth: 1, borderColor: theme.colors.accentStrong, borderRadius: 10} : undefined}>
        <LibraryRow first={index === 0} label={label(field)} value={valueText(field, values[field])} help={changed ? t('next.remoteSources.limitChanged', {value: valueText(field, baseline![field])}) : undefined}
          onPress={disabled ? undefined : () => {setEditing(field); setText(String(field === 'bytesPerSecond' ? values[field] / 1000000 : values[field]));}}/>
      </View>;
    })}</LibraryGroup>
    <BottomSheet open={!!editing} onOpenChange={open => {if (!open) setEditing(undefined);}} title={editing ? label(editing) : ''}
      footer={<Button disabled={disabled || number === undefined} onPress={() => {if (editing && number !== undefined) {onLimit(editing, number); setEditing(undefined);}}}>{t('next.remoteSources.save')}</Button>}>
      <View style={{gap: 12}}><TextField label={editing ? label(editing) : ''} value={text} onChangeText={setText} keyboardType="number-pad" editable={!disabled}
        description={editing === 'bytesPerSecond' ? t('next.remoteSources.megabytesPerSecond') : undefined}/>
        {editing && baseline && <><Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{valueText(editing, baseline[editing])}</Text>
          <Button variant="text" disabled={disabled} onPress={() => setText(String(editing === 'bytesPerSecond' ? baseline[editing] / 1000000 : baseline[editing]))}>{t('player.subtitles.positionReset')}</Button></>}
      </View>
    </BottomSheet>
  </>;
}
