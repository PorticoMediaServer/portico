import React, {useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../packages/i18n/src';
import {Banner} from '../kit/overlays';
import {fontFamilies, useTheme} from '../kit/theme';
import {phoneHeaderGeometry, type ShellLayout} from './model';
import type {NativeShellRuntime} from './runtime';

const t = defaultI18n.t.bind(defaultI18n);
const noReachability = () => () => {};
const reachable = () => 'reachable' as const;

/**
 * states.html 4b: the server stopped answering after this viewer was connected. The tabs and the
 * pages stay (each page shows its own error); one quiet line says "Can't reach {server}. Trying
 * again." once the shell's reads have failed for a while, and goes as soon as one succeeds.
 * Phone: under the top bar. Tablet: top centre. TV: top centre in the 1920 canvas, never focusable.
 */
export function ShellUnreachableLine({reachability, serverName, layout, hidden}: {
  reachability: NativeShellRuntime['reachability']; serverName?: string; layout: ShellLayout; hidden: boolean;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets();
  const state = useSyncExternalStore(reachability?.subscribe ?? noReachability, reachability?.getSnapshot ?? reachable, reachability?.getSnapshot ?? reachable);
  const visible = state === 'unreachable' && !hidden;
  const top = layout.kind === 'tv' ? 44 : layout.kind === 'phone' ? phoneHeaderGeometry(insets.top, false).height + 2 : Math.max(insets.top, 20) + 16;
  const name = serverName?.trim();
  const spoken = name ? `${t('next.overlays.unreachableStart')}${name}${t('next.overlays.unreachableEnd')}` : `${t('next.states.cantReachServer')}${t('next.overlays.unreachableEnd')}`;
  const line = <Banner testID="native-shell-unreachable" variant="quiet" tone="warning" visible={visible} topInset={top} accessibilityLabel={spoken}
    message={name ? <Text>{t('next.overlays.unreachableStart')}<Text style={{fontFamily: fontFamilies[700], color: theme.colors.text}}>{name}</Text>{t('next.overlays.unreachableEnd')}</Text> : spoken}/>;
  // Nothing here takes a touch or focus: the page under it stays usable.
  return <View pointerEvents="none" style={layout.kind === 'tv' ? {position: 'absolute', left: layout.left, top: layout.top, width: layout.width, height: layout.height,
    transformOrigin: 'top left', transform: [{scale: layout.scale}], zIndex: theme.layers.notice} : {position: 'absolute', inset: 0, zIndex: theme.layers.notice}}>{line}</View>;
}
