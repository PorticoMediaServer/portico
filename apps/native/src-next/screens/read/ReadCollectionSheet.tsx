import React, {useCallback, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Focusable} from '../../kit/controls';
import {BottomSheet} from '../../kit/overlays';
import {overlayPaintPermit, RetainedOverlay} from '../../kit/overlays/SurfacePaintHost';
import {LoadingBoundary, SkeletonBlock, StateView} from '../../kit/states';
import {Icon} from '../../kit/Icon';
import {useTheme} from '../../kit/theme';
import {linkNativeAbortSignals} from '../../platform/abort-signals';
import {nativeScreenServices} from '../native-screen-services';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeReadOwnerEditLease} from './read-book-model';
import {NativeReadingCollectionPicker} from './read-collection-picker';
import type {NativeReadingCollectionKind} from './read-collection-capability';

const {t} = defaultI18n;

/** phone-read: add the exact Reading title to one existing collection. The
 * picker reads the item's own enabled Detail action, then writes the existing
 * bounded entries operation with the collection's revision. Presentation only;
 * eligibility, ownership and write validation stay in the picker model. */
export function ReadCollectionSheet({context, lease, owner, itemId, libraryId, kind, title, onClose}: Readonly<{
  context: NativeShellBootstrapContext; lease: NativeReadOwnerEditLease; owner: object;
  itemId: string; libraryId: string; kind: NativeReadingCollectionKind; title: string; onClose: (owner: object) => void;
}>) {
  const theme = useTheme();
  const sheetLife = useRef<AbortController>(new AbortController()).current;
  const linked = useMemo(() => linkNativeAbortSignals([lease.signal, sheetLife.signal]), [lease, sheetLife]);
  const owned = useMemo(() => () => !linked.signal.aborted && lease.isCurrent(), [lease, linked]);
  const retired = useRef<boolean>(false);
  // Each opening is its own keyed instance: its owner ticket and close callback
  // are captured once and never follow a successor.
  const opening = useRef<Readonly<{owner: object; onClose: (owner: object) => void}> | undefined>(undefined);
  if (!opening.current) opening.current = Object.freeze({owner, onClose});
  const close = () => {if (!retired.current) {retired.current = true; sheetLife.abort(); opening.current!.onClose(opening.current!.owner);}};
  const [model, setModel] = useState<NativeReadingCollectionPicker>();
  const subscribe = useCallback((listener: () => void) => model ? model.subscribe(listener) : () => {}, [model]);
  const revision = useCallback(() => model ? model.getSnapshot() : 0, [model]);
  useSyncExternalStore(subscribe, revision);
  useLayoutEffect(() => {
    if (!owned() || !context.current()) {close(); return;}
    const picker = (() => {
      try {
        return new NativeReadingCollectionPicker({
          api: context.api,
          scope: {serverId: lease.viewer.serverId, viewerId: JSON.stringify([lease.viewer.authority, lease.viewer.accountId, lease.viewer.profileId])},
          requestId: nativeScreenServices(context).requestId,
          signal: linked.signal, isCurrent: owned, itemId, libraryId, kind,
        });
      } catch {return undefined;}
    })();
    if (!picker) {close(); return;}
    setModel(picker);
    void picker.load();
    const retire = () => {if (!retired.current && !lease.isCurrent()) close();};
    const offNav = context.navigation.subscribe(retire), offAuthority = context.subscribeAuthority(retire), offModel = lease.subscribe(retire);
    return () => {offNav(); offAuthority(); offModel(); retired.current = true; sheetLife.abort(); linked.dispose(); picker.dispose();};
  }, [context, lease, itemId, libraryId, kind, owned, sheetLife, linked]); // eslint-disable-line react-hooks/exhaustive-deps
  const state = model?.snapshot();
  const added = state?.added ? state.collections.find(resource => resource.id === state.added) : undefined;
  const surface = {open: true as const, title: t('next.selection.pickerTitle', {items: t('next.selection.book', {count: 1}), destination: t('next.selection.collectionWord')}),
    description: title, testID: 'native-reading-collection', onOpenChange: (open: boolean) => {if (!open) close();}};
  const paintPermit = overlayPaintPermit(() => !lease.signal.aborted && lease.isCurrent(), [listener => context.navigation.subscribe(listener), listener => context.subscribeAuthority(listener), listener => lease.subscribe(listener)], [lease.signal]);
  return <RetainedOverlay permit={paintPermit} allowedTypes={[BottomSheet, React.Fragment, View, Text, Focusable, Icon, LoadingBoundary, SkeletonBlock, StateView]}><BottomSheet {...surface} fitContent>
    <View style={{gap: 10}}>
      {state?.phase === 'error' ? <StateView variant="inline" title={t('next.states.pageFailed')} body={t('next.states.pageFailedBody')}
        primaryAction={{id: 'retry', label: t('next.states.tryAgain'), onPress: () => {if (model) void model.retryLoad().catch(() => {});}}}/> : <LoadingBoundary ready={state?.phase === 'ready'} skeleton={<SkeletonBlock width="100%" height={180}/>}>
      {state?.phase === 'ready' ? <>
        {added ? <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('next.selection.addedToast', {items: title, name: added.name})}</Text> : null}
        {!added && state.unchanged ? <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('next.selection.duplicatesToast', {count: 1})}</Text> : null}
        {!state.collections.length ? <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('next.selection.collection', {count: 0})}</Text> : null}
        {state.collections.map(resource => {
          const result = state.added === resource.id || state.unchanged === resource.id;
          const busy = state.busy === resource.id;
          return <Focusable key={resource.id} testID={`reading-collection-${resource.id}`} accessibilityRole="button"
            disabled={!!state.busy || result} focusKind="row" focusRadius={12}
            onPress={() => {if (model && !state.busy && !result) void model.choose(resource).catch(() => {});}}
            style={{minHeight: 60, borderRadius: 12, paddingHorizontal: 16, paddingVertical: 12, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: 10,
              backgroundColor: theme.colors.raised, opacity: state.busy && !busy ? .6 : 1}}>
            <View style={{flex: 1}}>
              <Text numberOfLines={2} style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{resource.name}</Text>
              <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.selection.titles', {count: resource.entryCount})}</Text>
            </View>
            {result ? <Icon name="i-check" size={20} color={theme.colors.text}/> : busy ? <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('status.loading')}</Text> : null}
          </Focusable>;
        })}
        {state.nextCursor ? <Focusable testID="reading-collection-more" accessibilityRole="button" disabled={state.loadingMore}
          onPress={() => {if (model && !state.loadingMore) void model.more().catch(() => {});}}
          style={{minHeight: 44, borderRadius: 12, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.raisedBright, opacity: state.loadingMore ? .6 : 1}}>
          <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('next.selection.showMore')}</Text>
        </Focusable> : null}
        {state.failure !== undefined ? <View style={{gap: 8}}>
          <Text style={[theme.type.body, {color: theme.colors.record}]}>{t('next.selection.pickerFailed')}</Text>
          {state.retryPending ? <Focusable testID="reading-collection-retry" accessibilityRole="button" disabled={!!state.busy}
            onPress={() => {if (model && !state.busy) void model.retry().catch(() => {});}}
            style={{minHeight: 44, borderRadius: 12, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.raisedBright}}>
            <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{t('next.states.tryAgain')}</Text>
          </Focusable> : null}
        </View> : null}
      </> : null}
      </LoadingBoundary>}
    </View>
  </BottomSheet></RetainedOverlay>;
}
