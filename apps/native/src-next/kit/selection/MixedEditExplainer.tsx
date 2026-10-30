import React, {useRef} from 'react';
import {StyleSheet, View} from 'react-native';
import {Button} from '../controls/Button';
import {Focusable} from '../controls/Focusable';
import {Icon} from '../Icon';
import {SurfaceHeader} from '../overlays/surface';
import {useTheme} from '../theme';
import {selectionText} from './copy';
import {selectionControlID as controlID} from './focus-ids';
import {selectionFooterProps, SelectionSurface, SelectionText} from './surface';
import type {MixedEditExplainerProps} from './types';

export function MixedEditExplainer(props: MixedEditExplainerProps) {
  const theme = useTheme();
  const {isTV} = theme;
  const footerButton = selectionFooterProps(theme);
  const firstRef = useRef<View>(null);
  const closeRef = useRef<View>(null);
  const mixed = props.groups.length > 1;
  const phoneMixed = theme.platform === 'phone' && mixed;
  const title = selectionText(mixed ? 'next.selection.native.mixedTitle' : 'next.selection.native.noBulkFields');
  return <SelectionSurface {...props} title={title} description={props.description}
    headerContent={phoneMixed ? <SurfaceHeader title={title} description={props.description} titleType={{size: 20, weight: 800, tracking: -.02, lineHeight: 24}} titleTextStyle={{includeFontPadding: false}}
      descriptionTextStyle={{fontSize: 13.5 * theme.textScale, lineHeight: 19.575 * theme.textScale, marginTop: 4, color: theme.colors.textTertiary, includeFontPadding: false}} style={{paddingTop: 4, paddingBottom: 10}} /> : undefined}
    footerPadding={phoneMixed ? {top: 12, bottom: 34, keyboardBottom: 12} : undefined} contentFooterGap={phoneMixed ? 0 : undefined} showFooterSeparator={phoneMixed ? false : undefined}
    contentStyle={{paddingHorizontal: isTV || phoneMixed ? 0 : 20}} initialFocusRef={props.initialFocusRef ?? (mixed ? firstRef : closeRef)}
    footer={<Button {...footerButton} testID={controlID(props.testID, 'dismiss')} ref={closeRef} variant="glass" size="compact" style={[footerButton.style, {alignSelf: 'stretch'}]} onPress={() => props.onOpenChange(false, 'action')}>{selectionText('next.selection.native.close')}</Button>}>
    {mixed ? phoneMixed ? <View style={{minHeight: 48, paddingTop: 8, paddingHorizontal: 20, justifyContent: 'center'}}>
      <SelectionText size={12.5} weight={700} secondary style={{lineHeight: 16.875 * theme.textScale, includeFontPadding: false}}>{selectionText('next.selection.native.keepOnly')}</SelectionText>
    </View> : <SelectionText size={isTV ? 20 : 12.5} weight={700} secondary style={{marginTop: isTV ? 22 : 4, marginBottom: isTV ? 22 : 8}}>{selectionText('next.selection.native.keepOnly')}</SelectionText> : null}
    {mixed ? props.groups.map((group, index) => <Focusable key={group.kind} testID={controlID(props.testID, 'keep-only', group.kind)} ref={index === 0 ? firstRef : undefined} accessibilityRole="button" accessibilityLabel={group.label}
      onPress={() => {props.onKeepOnly(group.kind); props.onOpenChange(false, 'action');}} focusKind="row" focusRadius={isTV ? 14 : 10} style={[styles.row, phoneMixed ? {paddingVertical: 8, paddingHorizontal: 20} : isTV ? {minHeight: 64, paddingHorizontal: 20, gap: 16, marginBottom: 12} : undefined]}>
      <Icon name={group.icon} size={isTV ? 30 : 24} color={phoneMixed ? theme.colors.textSecondary : undefined} />
      <View style={{flex: 1, gap: 2}}><SelectionText size={isTV ? 24 : 16} weight={600} style={phoneMixed ? {includeFontPadding: false} : undefined}>{group.label}</SelectionText><SelectionText size={isTV ? 20 : 12.5} secondary style={phoneMixed ? {lineHeight: 16.875 * theme.textScale, includeFontPadding: false} : undefined}>{selectionText('next.selection.native.ofCount', {count: group.count, total: props.total})}</SelectionText></View>
    </Focusable>) : null}
  </SelectionSurface>;
}
const styles = StyleSheet.create({row: {minHeight: 56, paddingVertical: 6, flexDirection: 'row', alignItems: 'center', gap: 14}});
