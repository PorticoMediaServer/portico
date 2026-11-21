import React, {createContext, useCallback, useContext, useEffect, useId, useMemo, useState} from 'react';
import {StyleSheet, Text, View, type LayoutChangeEvent} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {rowSpec, tvAccountSpec} from '@portico/design';
import {Focusable, type FocusableProps} from './Focusable';
import {Icon, type IconName} from '../Icon';
import {useTheme} from '../theme';

type TVAccountRowProps = Pick<FocusableProps,
  'focusId' | 'preferredFocus' | 'disabled' | 'accessibilityLabel' | 'accessibilityState' | 'onPress' | 'nextFocusDown'> & {
  title: string; detail: string; icon: IconName; badge?: string; children?: React.ReactNode;
  /** The server this device is on: its tile takes the brand gradient, as connect24 draws it. */
  active?: boolean;
};

type Column = {width: number; report: (key: string, width: number | undefined) => void};
const TrailingColumn = createContext<Column | null>(null);

/** Rows inside share one trailing column, so every face ends at the same x and every chevron lines up,
 * whatever actions each row carries. The column is as wide as the widest row's actions (labels translate). */
export function TVAccountRowColumn({children}: {children: React.ReactNode}) {
  const [widths, setWidths] = useState<Record<string, number>>({});
  const report = useCallback((key: string, width: number | undefined) => setWidths(previous => {
    if (previous[key] === width) return previous;
    const next = {...previous};
    if (width === undefined) delete next[key]; else next[key] = width;
    return next;
  }), []);
  const value = useMemo(() => ({width: Math.max(0, ...Object.values(widths)), report}), [widths, report]);
  return <TrailingColumn.Provider value={value}>{children}</TrailingColumn.Provider>;
}

/** connect24's server/certificate row: the icon inset belongs to the row's own focus face. */
export function TVAccountRow({title, detail, icon, badge, children, onPress, active = false, ...props}: TVAccountRowProps) {
  const theme = useTheme(), tile = tvAccountSpec.rowIcon, column = useContext(TrailingColumn), key = useId();
  const hasActions = React.Children.toArray(children).length > 0;
  const report = column?.report;
  useEffect(() => () => report?.(key, undefined), [report, key]);
  const measure = column ? (event: LayoutChangeEvent) => column.report(key, event.nativeEvent.layout.width) : undefined;
  const trailing = hasActions || (column?.width ?? 0) > 0;
  const style = {flex: 1, minHeight: rowSpec.minHeight.withSubtitle.tv, paddingHorizontal: rowSpec.paddingX.tv,
    paddingVertical: rowSpec.paddingY.tv, flexDirection: 'row' as const, alignItems: 'center' as const, gap: rowSpec.leadingGap.tv};
  const content = <>
    <View style={{width: tile.box, height: tile.box, borderRadius: tile.radius, overflow: 'hidden', alignItems: 'center', justifyContent: 'center',
      backgroundColor: theme.colors.raisedBright}}>
      {active && <Svg pointerEvents="none" style={StyleSheet.absoluteFill} width="100%" height="100%"><Defs><LinearGradient id="tv-account-row-tile" x1="0" y1="0" x2="1" y2="1">
        <Stop offset="0" stopColor={theme.colors.accentDeep}/><Stop offset="1" stopColor={theme.colors.generatedGreen}/></LinearGradient></Defs>
        <Rect width="100%" height="100%" fill="url(#tv-account-row-tile)"/></Svg>}
      <Icon name={icon} size={tile.glyph} color={active ? theme.colors.text : theme.colors.textSecondary}/>
    </View>
    <View style={{flex: 1, minWidth: 0, gap: 6}}>
      <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{title}</Text>
      <Text style={[theme.type[rowSpec.subtitle.role], {color: theme.colors[rowSpec.subtitle.color]}]}>{detail}</Text>
    </View>
    {badge && <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{badge}</Text>}
    {onPress && !props.disabled && <Icon name="i-right" size={26} color={theme.colors.textTertiary}/>}
  </>;
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: rowSpec.leadingGap.tv,
    backgroundColor: theme.colors.raised, borderRadius: rowSpec.group.radius.tv,
    borderBottomWidth: 1, borderBottomColor: theme.colors.lineSoft}}>
    {onPress ? <Focusable {...props} accessibilityRole="button" onPress={onPress} focusKind="row" focusRadius={rowSpec.group.radius.tv} style={style}>{content}</Focusable>
      : <View style={style}>{content}</View>}
    {trailing && <View style={{minWidth: column?.width, flexDirection: 'row', justifyContent: 'flex-end'}}>
      {hasActions && <View onLayout={measure} style={{flexDirection: 'row', alignItems: 'center', gap: rowSpec.leadingGap.tv, paddingRight: rowSpec.paddingX.tv}}>{children}</View>}
    </View>}
  </View>;
}
