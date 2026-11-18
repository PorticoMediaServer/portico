import React, {useEffect, useState, type ComponentType} from 'react';
import {Dimensions, Linking, LogBox, Platform, Pressable, ScrollView, Settings, Text, View} from 'react-native';
import {OverlaysProvider} from '../kit/overlays';
import {SafeAreaView} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../packages/i18n/src/index';
import {Icon} from '../kit/Icon';
import {useTheme} from '../kit/theme';
import {BlurView, glassRecipe} from '../platform/native-blur';
import {discoverGalleries, galleriesForPlatform, kitSelectionFromURL} from './gallery-registry';
import {accountProofFromURL} from './account-proof-entry';
import {AccountProofHost} from './AccountProofHost';

const galleries = discoverGalleries();

/** Apple capture runs launch the gallery app with `-PorticoKitGallery <id>` (a launch argument reaches
 * NSUserDefaults), so each case opens full-window with no link prompt and no development toast over it. */
function launchedGallery(): {gallery: string; window: true} | undefined {
  if (Platform.OS !== 'ios') return;
  const value: unknown = Settings.get('PorticoKitGallery');
  if (typeof value !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(value)) return;
  LogBox.ignoreAllLogs();
  // `-PorticoKitGallery list` publishes this platform's gallery ids for the capture script to read back.
  if (value === 'list') {
    const {width, height} = Dimensions.get('window'), short = Math.min(width, height);
    Settings.set({PorticoKitGalleryIds: galleriesForPlatform(galleries, Platform.isTV ? 'tv' : short < 600 ? 'phone' : 'tablet').map(gallery => gallery.id).join(',')});
  }
  return {gallery: value, window: true};
}

/** This module, including URL handling, is reachable only through App's __DEV__ branch. */
export function KitHost({shell: Shell}: {shell?: ComponentType}) {
  const [launched] = useState(launchedGallery);
  const [selection, setSelection] = useState<{gallery?: string; window?: boolean} | {serverAddress: string} | undefined>(launched);
  useEffect(() => {
    let live = true;
    let changed = !!launched;
    const read = (url: string | null) => accountProofFromURL(url) ?? kitSelectionFromURL(url);
    const subscription = Linking.addEventListener('url', event => {changed = true; setSelection(read(event.url));});
    void Linking.getInitialURL().then(url => {if (live && !changed) setSelection(read(url));}, () => {});
    return () => {live = false; subscription.remove();};
  }, [launched]);
  if (selection && 'serverAddress' in selection) return <AccountProofHost key={selection.serverAddress} serverAddress={selection.serverAddress}/>;
  return selection || !Shell ? <Kit key={selection?.gallery ?? ''} initialGallery={selection?.gallery} presentation={selection?.window ? 'window' : 'catalogue'} captured={!!launched && selection === launched} /> : <Shell />;
}

/** Dev-only host. Families render in the same theme, gesture root and safe-area providers. */
/** `captured`: a launched capture; its page keeps clear of the phone's status bar (measurement windows stay edge to edge). */
export function Kit({initialGallery, presentation = 'catalogue', captured = false}: {initialGallery?: string; presentation?: 'catalogue' | 'window'; captured?: boolean}) {
  const theme = useTheme();
  // A case opened as a window is being looked at or shot: the development warning box must not cover it (Android draws it over the bottom of the screen).
  useEffect(() => {if (presentation === 'window') LogBox.ignoreAllLogs();}, [presentation]);
  const [selected, setSelected] = useState<string | undefined>(initialGallery);
  const [focused, setFocused] = useState<string>();
  // A wide tablet's theme platform is `web` (the desktop layout); it is still a tablet for which sections it has, as in portrait.
  const available = galleriesForPlatform(galleries, theme.platform === 'web' ? 'tablet' : theme.platform);
  const current = available.find(gallery => gallery.id === selected);
  const Screen = current?.render;
  const t = defaultI18n.t;
  // The private compiled evidence entry injects this Kit through App's normal
  // KitHost shell slot. Families keep the catalogue gutter they were authored in;
  // Canvas's negative gutter therefore cancels exactly once, at the window edge.
  // The overlay host at the window's root, as the app's shell has it (NativeShell): an anchored popover is placed in window
  // coordinates, so without it a case's popover rendered inline inside a page that sits beside the rail and landed a rail's
  // width too far right.
  if (presentation === 'window' && Screen) return <OverlaysProvider><SafeAreaView testID="native-kit-window" edges={(captured || Platform.OS === 'android') && !theme.isTV && !current?.screen ? ['top'] : []} style={{flex: 1}}><View style={{flex: 1, paddingHorizontal: theme.gutter}}><Screen /></View></SafeAreaView></OverlaysProvider>;
  // A section asked for as a window that this kind of device does not have (a phone-only sheet on a TV) says so. Falling through
  // to the catalogue drew every family at once, and a shot of that was read as the section's own fault.
  if (presentation === 'window' && initialGallery && !Screen) return <SafeAreaView testID="native-kit-absent" style={{flex: 1, alignItems: 'center', justifyContent: 'center', padding: theme.gutter}}>
    <Text style={[theme.type.heading, {color: theme.colors.textSecondary, textAlign: 'center'}]}>{t('next.platform.kitAbsent', {id: initialGallery})}</Text></SafeAreaView>;
  return <SafeAreaView testID="native-kit" style={{flex: 1}}>
    <ScrollView contentContainerStyle={{padding: theme.gutter, gap: theme.space.md}}>
      <Text accessibilityRole="header" style={[theme.type.heading, {color: theme.colors.text}]}>{t('next.platform.kitTitle')}</Text>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.xs}}>
        {[{id: '', title: 'next.platform.allFamilies' as const}, ...available].map((gallery, index) => <Pressable key={gallery.id} testID={`gallery-${gallery.id || 'all'}`} accessibilityRole="button" accessibilityLabel={t(gallery.title)} accessibilityState={{selected: (selected ?? '') === gallery.id}} onFocus={() => setFocused(gallery.id)} onBlur={() => setFocused(undefined)} onPress={() => setSelected(gallery.id || undefined)} style={{minHeight: theme.isTV ? theme.targets.tenFoot : theme.targets.touch, paddingHorizontal: theme.space.sm, justifyContent: 'center', borderRadius: theme.baseRadii.control, borderWidth: theme.spec.focus.ringWidth[theme.isTV ? 'tv' : 'phone'], borderColor: focused === gallery.id ? theme.colors.focus : theme.colors.line, backgroundColor: theme.colors.raised}}><Text style={[theme.type.nav, {color: theme.colors.text}]}>{t(gallery.title)}</Text></Pressable>)}
      </View>
      {Screen ? <Screen /> : available.length ? available.map(gallery => {const Family = gallery.render; return <View key={gallery.id}><Family /></View>;}) : <View testID="native-theme-specimen" style={{gap: theme.space.md}}>
        <Text style={[theme.type.lockup, {color: theme.colors.text}]}>{t('next.platform.kitTitle')}</Text>
        <View style={{flexDirection: 'row', gap: theme.space.sm}}><Icon name="i-home" /><Icon name="i-search" /><Icon name="i-act" /></View>
        <View testID="native-blur-specimen" style={{height: theme.spec.navigation.phone.tabBar.height, overflow: 'hidden'}}>
          <View style={{flex: 1, flexDirection: 'row'}}><View style={{flex: 1, backgroundColor: theme.colors.accent}} /><View style={{flex: 1, backgroundColor: theme.colors.generatedGreen}} /></View>
          {glassRecipe().nativeBlur ? <BlurView style={{position: 'absolute', inset: 0}} blurType="dark" blurAmount={glassRecipe().blurAmount} reducedTransparencyFallbackColor={theme.colors.raised} /> : <View style={{position: 'absolute', inset: 0, backgroundColor: glassRecipe().backgroundColor, opacity: glassRecipe().backgroundAlpha}} />}
        </View>
      </View>}
    </ScrollView>
  </SafeAreaView>;
}
