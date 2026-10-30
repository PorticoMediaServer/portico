import React from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Focusable} from '../../kit/controls';
import {FocusRegion} from '../../kit/focus';
import {Icon} from '../../kit/Icon';
import {alpha, browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeDestinationActivation} from '../destinations/focus';
import {useDestinationEntryFocus} from '../destinations/useEntryFocus';
import {applyNativeTVKeyboardKey, nativeTVKeyboardRows} from './keyboard';

export type NativeTVKeyboardProps = Readonly<{
  /** Authority- and route-bound caller identity; Search and a retained sheet are distinct. */
  id: string;
  value: string;
  onChangeText: (value: string) => void;
  activation: NativeDestinationActivation;
  leftRegionId: string;
  rightRegionId?: string;
  width?: number;
  /** Explicit caller character inventory; Search retains its authored default. */
  rows?: readonly (readonly string[])[];
  /** Keys across a row; Search is authored six wide, the TV screens are nine. */
  columns?: number;
  /** A key the frame draws ahead of Space, such as the last digit of its row. */
  lead?: string;
  testID?: string;
  /** The row region holding focus, so a screen can point Left/Back at its remembered key. */
  onRowFocus?: (rowId: string) => void;
  /** Present only when a real caller-owned system recognizer is available. */
  onVoice?: () => void;
  voiceBusy?: boolean;
}>;

/** Shared actual native keyboard for Search, Settings search and typing sheets.
 * Native focus guides choose directional traversal. No remote-key listener or
 * synthetic first child is installed; entry waits for the real key-a host. */
export function NativeTVKeyboard({id, value, onChangeText, activation, leftRegionId, rightRegionId, width = 500, rows = nativeTVKeyboardRows, columns = 6, lead, testID = 'native-tv-keyboard', onRowFocus, onVoice, voiceBusy}: NativeTVKeyboardProps) {
  const theme = useTheme(), keyWidth = (width - 10 * (columns - 1)) / columns;
  const first = {regionId: `${id}.row.0`, childId: rows[0]?.[0] ?? 'a'};
  /** The lead key is drawn on the action row, so the inventory that types it is the rows plus that key. */
  const inventory = lead ? [...rows, [lead]] : rows;
  const actions = lead ? [lead, 'space', 'delete', 'clear'] : ['space', 'delete', 'clear'];
  useDestinationEntryFocus(activation, first);
  const choose = (key: string) => onChangeText(applyNativeTVKeyboardKey(value, key, inventory));
  const keyStyle = (span = 1) => ({width: keyWidth * span + 10 * (span - 1), height: 72, borderRadius: 14,
    backgroundColor: alpha(theme.colors.text, .05), justifyContent: 'center' as const, alignItems: 'center' as const});
  const edges = {left: leftRegionId, ...(rightRegionId ? {right: rightRegionId} : {})};
  const rowFocus = (row: string) => (owns: boolean) => {if (owns) onRowFocus?.(row);};
  return <FocusRegion id={id} enabled={activation.visible} testID={testID} style={{width, gap: 10}}>
    {rows.map((row, index) => <FocusRegion key={index} id={`${id}.row.${index}`} entry={row[0]} order={row} orientation="horizontal"
      enabled={activation.visible} screenStart={first} edges={edges} onFocusChange={rowFocus(`${id}.row.${index}`)} style={{flexDirection: 'row', gap: 10}}>
      {row.map(key => <Focusable key={key} focusId={key} testID={`${testID}-key-${key}`} disabled={!activation.visible} accessibilityLabel={key}
        onPress={() => choose(key)} focusRadius={14} style={keyStyle()}>
        <Text style={[browseType(theme, 28, 700), {color: /\d/.test(key) ? theme.colors.textTertiary : theme.colors.textSecondary}]}>{key}</Text>
      </Focusable>)}
    </FocusRegion>)}
    <FocusRegion id={`${id}.row.${rows.length}`} entry={lead ?? 'space'} order={actions} orientation="horizontal" enabled={activation.visible}
      screenStart={first} edges={edges} onFocusChange={rowFocus(`${id}.row.${rows.length}`)} style={{flexDirection: 'row', gap: 10}}>
      {lead && <Focusable focusId={lead} testID={`${testID}-key-${lead}`} disabled={!activation.visible} accessibilityLabel={lead}
        onPress={() => choose(lead)} focusRadius={14} style={keyStyle()}>
        <Text style={[browseType(theme, 28, 700), {color: /\d/.test(lead) ? theme.colors.textTertiary : theme.colors.textSecondary}]}>{lead}</Text>
      </Focusable>}
      <Focusable focusId="space" testID={`${testID}-key-space`} disabled={!activation.visible} accessibilityLabel={defaultI18n.t('next.states.key.Space')} onPress={() => choose('space')} focusRadius={14} style={keyStyle(3)}>
        <Text style={[browseType(theme, 22, 700), {color: theme.colors.textSecondary}]}>{defaultI18n.t('next.states.key.Space')}</Text>
      </Focusable>
      <Focusable focusId="delete" testID={`${testID}-key-delete`} disabled={!activation.visible} accessibilityLabel={defaultI18n.t('action.delete')} onPress={() => choose('delete')} focusRadius={14} style={keyStyle()}>
        <Icon name="i-backspace" size={32} color={theme.colors.textSecondary}/>
      </Focusable>
      <Focusable focusId="clear" testID={`${testID}-key-clear`} disabled={!activation.visible} accessibilityLabel={defaultI18n.t('filter.clear')} onPress={() => choose('clear')} focusRadius={14} style={keyStyle(2)}>
        <Text style={[browseType(theme, 22, 700), {color: theme.colors.textSecondary}]}>{defaultI18n.t('filter.clear')}</Text>
      </Focusable>
    </FocusRegion>
    {onVoice && <FocusRegion id={`${id}.voice`} entry="voice" order={['voice']} orientation="horizontal" enabled={activation.visible} edges={edges} onFocusChange={rowFocus(`${id}.voice`)}>
      <Focusable focusId="voice" testID={`${testID}-voice`} disabled={!activation.visible || voiceBusy} accessibilityLabel={defaultI18n.t('web.search.title')} onPress={onVoice} focusRadius={14} style={{...keyStyle(6), flexDirection: 'row', gap: 14}}>
        <Icon name="i-mic" size={28} color={theme.colors.textSecondary}/><Text style={[browseType(theme, 22, 700), {color: theme.colors.textSecondary}]}>{defaultI18n.t('web.search.title')}</Text>
      </Focusable>
    </FocusRegion>}
  </FocusRegion>;
}
