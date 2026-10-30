import React, {useEffect, useRef} from 'react';
import {FlatList, StyleSheet, TextInput, View} from 'react-native';
import {Button} from '../controls/Button';
import {Focusable} from '../controls/Focusable';
import {TextField} from '../controls/TextField';
import {Icon} from '../Icon';
import {focusTarget} from '../overlays/OverlayHost';
import {ToastHost} from '../overlays/Feedback';
import {useTheme} from '../theme';
import {pickerResultSucceeded} from './presentation';
import {selectionText} from './copy';
import {selectionControlID as controlID} from './focus-ids';
import {selectionFooterProps, SelectionSurface, SelectionText} from './surface';
import type {PickerEntry, PickerProps, PickerResult} from './types';

export type DestinationPickerProps = PickerProps & {onViewDestination: (id: string) => void; onResultDismiss: () => void};
function DestinationPicker({kind, ...props}: DestinationPickerProps & {kind: 'playlist' | 'collection'}) {
  const theme = useTheme();
  const {isTV, colors} = theme;
  const footerButton = selectionFooterProps(theme);
  const nameRef = useRef<TextInput>(null);
  const retryRef = useRef<View>(null), newRef = useRef<View>(null), dismissRef = useRef<View>(null);
  const retryRecovery = props.onRetryError ?? props.onRetryOperation, recovering = !!retryRecovery;
  const entryRef = useRef<View>(null);
  const listRef = useRef<FlatList<PickerEntry>>(null);
  const windowKey = JSON.stringify([props.query, props.entries.map(entry => entry.id)]);
  const page = useRef<{direction: 'next' | 'previous'; key: string; loading: boolean; focus: boolean} | null>(null);
  const lastQuery = useRef(props.query), scroll = useRef(0), focused = useRef(false);
  const direction = useRef<'next' | 'previous' | null>(null);
  const pendingPage = page.current;
  const available = props.entries.filter(entry => entry.canAdd);
  const preferredId = props.focusedEntryId ?? (pendingPage?.direction === 'previous' ? available.at(-1)?.id : available[0]?.id);
  useEffect(() => {
    const pending = page.current;
    if (pending && props.loading) pending.loading = true;
    if (pending && !props.loading && (pending.key !== windowKey || pending.loading)) {
      direction.current = null;
      scroll.current = pending.direction === 'previous' ? Math.max(0, props.entries.length * (isTV ? 116 : 64) - (isTV ? 580 : 320)) : 0;
      if (pending.direction === 'previous') listRef.current?.scrollToEnd({animated: false});
      else listRef.current?.scrollToOffset({offset: 0, animated: false});
      if (pending.focus) focusTarget(entryRef.current);
      page.current = null;
    }
    if (lastQuery.current !== props.query) {listRef.current?.scrollToOffset({offset: 0, animated: false}); scroll.current = 0; page.current = null; lastQuery.current = props.query;}
    if (props.error) page.current = null;
  }, [windowKey, props.loading, props.query, props.error, props.entries.length, isTV]);
  const turnPage = (direction: 'next' | 'previous') => {
    const callback = direction === 'next' ? props.hasNext && props.onNext : props.hasPrevious && props.onPrevious;
    if (!callback || props.loading || props.pending || page.current) return;
    page.current = {direction, key: windowKey, loading: false, focus: focused.current};
    callback();
  };
  const failures = props.result?.failures ?? [];
  const failed = [...new Set(failures.map(row => row.id))];
  const retryFailed = failed.length > 0 && !!props.result?.complete && !recovering && !props.loading;
  const retryAvailable = recovering || retryFailed;
  useEffect(() => {if (props.open && retryAvailable) focusTarget(retryRef.current);}, [props.open, retryAvailable]);
  const complete = !!props.result && !recovering && !props.loading && pickerResultSucceeded(props.result);
  const newLabel = selectionText(`next.selection.native.new.${kind}`);
  const retry = () => {if (!props.pending && !props.loading && !recovering && props.result?.complete && failed.length) props.onRetry(failed);};
  const close = () => props.onOpenChange(false, 'action');
  const title = props.creating ? newLabel : selectionText(`next.selection.native.picker.${kind}`, {items: props.selectionLabel});
  const rowContent = (item: PickerResult['destination'] & Partial<Pick<PickerEntry, 'size' | 'alreadyIn'>>, active: boolean, added?: number) => <View style={[styles.entryContent, {minHeight: isTV ? 104 : 64, paddingHorizontal: isTV ? 16 : 20, gap: isTV ? 24 : 14, borderRadius: isTV ? 16 : 10, backgroundColor: isTV ? active ? colors.text : 'rgba(237,245,250,.07)' : active ? 'rgba(237,245,250,.06)' : 'transparent'}]}>
    <View pointerEvents="none" accessible={false} importantForAccessibility="no-hide-descendants" style={{width: isTV ? 76 : 48, height: isTV ? 76 : 48, overflow: 'hidden', borderRadius: isTV ? 12 : 10}}>{item.artwork}</View>
    <View style={{flex: 1, minWidth: 0}}>
      <SelectionText numberOfLines={1} size={isTV ? 28 : 16} weight={700} style={{color: isTV && active ? colors.ink : colors.text}}>{item.title}</SelectionText>
      {item.size !== undefined ? <SelectionText size={isTV ? 20 : 12.5} secondary style={{marginTop: 3, color: isTV && active ? 'rgba(7,16,24,.66)' : colors.textTertiary}}>{selectionText('next.selection.native.titles', {count: item.size})}</SelectionText> : null}
    </View>
    {added !== undefined ? <SelectionText size={isTV ? 20 : 12.5} weight={700} style={{color: colors.accentStrong}}>{selectionText('next.selection.native.addedIndicator', {count: added})}</SelectionText> : item.alreadyIn !== undefined && item.alreadyIn > 0 ? <SelectionText size={isTV ? 20 : 12.5} weight={700} style={{color: isTV && active ? 'rgba(7,16,24,.66)' : colors.accentStrong}}>{selectionText('next.selection.native.alreadyIn', {count: item.alreadyIn})}</SelectionText> : null}
  </View>;
  const row = (item: PickerEntry) => <Focusable testID={controlID(props.testID, 'destination', kind, item.id)} ref={item.id === preferredId ? entryRef : undefined} hasTVPreferredFocus={isTV && !failed.length && item.id === preferredId} accessibilityRole="button" accessibilityLabel={item.title} focusRadius={isTV ? 16 : 10} focusKind="row"
    disabled={props.pending || props.loading || !item.canAdd} onPress={() => {if (item.canAdd && !props.pending && !props.loading) props.onAdd(item.id);}} onFocus={() => {focused.current = true;}} onBlur={() => {focused.current = false;}} style={styles.entry}>
    {state => rowContent(item, state.focused || state.hovered || state.pressed)}
  </Focusable>;
  const buttons = <View style={styles.buttons}>
    <Button {...footerButton} testID={controlID(props.testID, 'dismiss')} ref={dismissRef} size="compact" variant="glass" onPress={props.creating && !props.result && !recovering ? () => props.onCreatingChange(false) : close}>{selectionText(props.creating && !props.result && !recovering ? 'next.selection.native.cancel' : 'next.selection.native.done')}</Button>
    {retryRecovery ? <Button {...footerButton} testID={controlID(props.testID, 'retry')} ref={retryRef} size="compact" loading={props.pending} style={[footerButton.style, {flex: 1, alignSelf: 'stretch'}]} onPress={() => {if (!props.pending) retryRecovery();}}>{selectionText('next.selection.native.retry')}</Button> : props.creating && !props.result ? <Button {...footerButton} testID={controlID(props.testID, 'create')} size="compact" loading={props.pending} disabled={props.loading} style={[footerButton.style, {flex: 1, alignSelf: 'stretch'}]} onPress={() => {if (!props.pending && !props.loading) props.onCreateAndAdd(props.name.trim() || newLabel);}}>{selectionText('next.selection.native.createAdd', {count: props.count})}</Button>
      : retryFailed ? <Button {...footerButton} testID={controlID(props.testID, 'retry')} ref={retryRef} size="compact" loading={props.pending} style={[footerButton.style, {flex: 1, alignSelf: 'stretch'}]} onPress={retry}>{failed.length === 1 ? selectionText('next.selection.native.retryFor', {title: failures[0]!.title}) : selectionText('next.selection.native.retry')}</Button> : null}
  </View>;
  return <>
    <SelectionSurface compactHeader scrollable={false} open={props.open} onOpenChange={props.onOpenChange} title={title}
      contentFooterGap={theme.platform === 'phone' ? 0 : undefined} showFooterSeparator={theme.platform === 'phone' ? false : undefined} footerPadding={theme.platform === 'phone' ? {top: 12, bottom: 34, keyboardBottom: 12} : undefined}
      initialFocusRef={retryAvailable ? retryRef : props.creating ? props.initialFocusRef ?? nameRef : props.initialFocusRef ?? (props.result ? dismissRef : available.length ? entryRef : newRef)} returnFocusRef={props.returnFocusRef}
      testID={props.testID} footer={props.creating || props.result || recovering ? buttons : undefined} contentStyle={{paddingHorizontal: 0}}>
      {props.error ? <View style={styles.notice}><SelectionText>{props.error}</SelectionText></View> : null}
      {props.result ? <View style={[styles.notice, failures.length ? {backgroundColor: 'rgba(241,121,121,.08)', borderColor: 'rgba(241,121,121,.35)'} : undefined]} accessibilityLiveRegion="polite" accessibilityState={{busy: !props.result.complete}}>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}><Icon name={failures.length ? 'i-warn' : 'i-check'} size={20} color={failures.length ? colors.record : colors.healthy} />
        {props.result.inserted !== undefined ? <SelectionText size={isTV ? 24 : 14.5} weight={700} style={{flex: 1}}>{props.result.total !== undefined ? selectionText('next.selection.native.addedPartial', {added: props.result.inserted, count: props.result.total, title: props.result.destination.title}) : selectionText('next.selection.native.added', {count: props.result.inserted, title: props.result.destination.title})}</SelectionText> : null}</View>
        {!props.result.complete && props.result.processed !== undefined && props.result.total !== undefined ? <SelectionText secondary>{selectionText('next.selection.native.ofCount', {count: props.result.processed, total: props.result.total})}</SelectionText> : null}
        {props.result.jobFacts?.map(fact => <SelectionText key={fact.id} size={isTV ? 22 : 13.5} secondary>{fact.text}</SelectionText>)}
        {failures.map(item => <SelectionText key={item.id} size={isTV ? 22 : 13.5} secondary style={{lineHeight: isTV ? 33 : 19.575}}>{selectionText('next.selection.native.couldNotAdd', {title: item.title, reason: item.reason ?? selectionText('next.selection.native.addFailed')})}</SelectionText>)}
      </View> : null}
      {recovering && !props.result ? null : !props.result && props.creating ? <View style={{paddingHorizontal: 20, paddingTop: theme.platform === 'phone' ? 4 : 0, paddingBottom: theme.platform === 'phone' ? 0 : 8}}>{props.nameInput ?? <TextField appearance="selection" testID={controlID(props.testID, 'name')} ref={nameRef} autoFocus={!isTV} value={props.name} onChangeText={props.onNameChange} label={selectionText('next.selection.native.name')} accessibilityLabel={selectionText('next.selection.native.name')} placeholder={newLabel} editable={!props.pending} fieldStyle={theme.platform === 'phone' ? {height: 46} : undefined} style={theme.platform === 'phone' ? {fontSize: 16 * theme.textScale} : undefined} returnKeyType="done" onSubmitEditing={() => {if (!props.pending && !props.loading) props.onCreateAndAdd(props.name.trim() || newLabel);}} />}</View> : props.result ? rowContent({...props.result.destination, alreadyIn: props.result.alreadyIn}, true, props.result.inserted) : <>
        {props.showFind ? <View style={{paddingHorizontal: 20, paddingBottom: 6}}><TextField testID={controlID(props.testID, 'find')} variant="search" value={props.query} onChangeText={props.onQueryChange} accessibilityLabel={selectionText(`next.selection.native.find.${kind}`)} placeholder={selectionText(`next.selection.native.find.${kind}`)} /></View> : null}
        <Focusable testID={controlID(props.testID, 'new')} ref={newRef} accessibilityRole="button" accessibilityLabel={newLabel} disabled={props.pending || props.loading} onPress={() => {if (!props.pending && !props.loading) props.onCreatingChange(true);}} focusRadius={isTV ? 16 : 10} style={{paddingHorizontal: isTV ? 16 : 20, minHeight: isTV ? 104 : 64, flexDirection: 'row', alignItems: 'center', gap: isTV ? 24 : 14}}>
          <View style={{width: isTV ? 76 : 48, height: isTV ? 76 : 48, borderRadius: isTV ? 12 : 10, backgroundColor: 'rgba(237,245,250,.1)', alignItems: 'center', justifyContent: 'center'}}><Icon name="i-plus" size={isTV ? 36 : 24} /></View><SelectionText size={isTV ? 28 : 16} weight={700}>{newLabel}</SelectionText>
        </Focusable>
        {!isTV ? <View style={{height: 1, marginHorizontal: 20, marginVertical: 6, backgroundColor: colors.line}} /> : null}
        <FlatList ref={listRef} data={props.entries} keyExtractor={item => item.id} renderItem={({item}) => row(item)} scrollEnabled nestedScrollEnabled
          initialNumToRender={8} maxToRenderPerBatch={8} windowSize={5} keyboardShouldPersistTaps="handled" onScroll={event => {
            const {contentOffset, contentSize, layoutMeasurement} = event.nativeEvent;
            const previous = scroll.current; scroll.current = contentOffset.y;
            direction.current = contentOffset.y > previous ? 'next' : contentOffset.y < previous ? 'previous' : null;
            if (contentOffset.y > previous && contentOffset.y + layoutMeasurement.height >= contentSize.height - 1) turnPage('next');
            else if (contentOffset.y < previous && contentOffset.y <= 1) turnPage('previous');
          }} scrollEventThrottle={16}
          onStartReached={() => {if (direction.current === 'previous') turnPage('previous');}}
          onEndReached={() => {if (direction.current === 'next') turnPage('next');}}
          style={{height: Math.min(Math.max(1, props.entries.length) * (isTV ? 116 : 64), isTV ? 580 : 320)}}
          ItemSeparatorComponent={isTV ? () => <View style={{height: 12}} /> : undefined}
          ListFooterComponent={props.loading ? <SelectionText secondary>{selectionText('next.selection.native.loading')}</SelectionText> : props.entries.length === 0 ? <SelectionText secondary>{selectionText(`next.selection.native.empty.${kind}`)}</SelectionText> : null} />
      </>}
    </SelectionSurface>
    {complete ? <ToastHost bottomOffset={84} toast={{id: `${kind}-${props.result!.destination.id}`, icon: 'i-check', message: selectionText('next.selection.native.added', {count: props.result!.inserted!, title: props.result!.destination.title}), action: {label: selectionText('next.selection.native.view'), onPress: () => props.onViewDestination(props.result!.destination.id)}}} onDismiss={props.onResultDismiss} /> : null}
  </>;
}
export function PlaylistPicker(props: DestinationPickerProps) {return <DestinationPicker {...props} kind="playlist" />;}
export function CollectionPicker(props: DestinationPickerProps) {return <DestinationPicker {...props} kind="collection" />;}
const styles = StyleSheet.create({entry: {alignSelf: 'stretch'}, entryContent: {flexDirection: 'row', alignItems: 'center', paddingVertical: 8}, buttons: {flexDirection: 'row', gap: 10}, notice: {marginHorizontal: 16, marginTop: 4, marginBottom: 10, paddingHorizontal: 14, paddingVertical: 12, gap: 3, borderRadius: 12, backgroundColor: 'rgba(232,188,106,.1)', borderWidth: 1, borderColor: 'rgba(232,188,106,.35)'}});
