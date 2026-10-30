import React, {useEffect, useState, type ComponentType} from 'react';
import {Linking, Pressable, ScrollView, Text, View} from 'react-native';
import {SafeAreaView} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../packages/i18n/src/index';
import {Icon} from '../kit/Icon';
import {useTheme} from '../kit/theme';
import {BlurView, glassRecipe} from '../platform/native-blur';
import {discoverGalleries, galleriesForPlatform, kitSelectionFromURL} from './gallery-registry';
import {accountProofFromURL} from './account-proof-entry';
import {AccountProofHost} from './AccountProofHost';

const galleries = discoverGalleries();

/** This module, including URL handling, is reachable only through App's __DEV__ branch. */
export function KitHost({shell: Shell}: {shell?: ComponentType}) {
  const [selection, setSelection] = useState<{gallery?: string; window?: boolean} | {serverAddress: string}>();
  useEffect(() => {
    let live = true;
    let changed = false;
    const read = (url: string | null) => accountProofFromURL(url) ?? kitSelectionFromURL(url);
    const subscription = Linking.addEventListener('url', event => {changed = true; setSelection(read(event.url));});
    void Linking.getInitialURL().then(url => {if (live && !changed) setSelection(read(url));}, () => {});
    return () => {live = false; subscription.remove();};
  }, []);
  if (selection && 'serverAddress' in selection) return <AccountProofHost key={selection.serverAddress} serverAddress={selection.serverAddress}/>;
  return selection || !Shell ? <Kit key={selection?.gallery ?? ''} initialGallery={selection?.gallery} presentation={selection?.window ? 'window' : 'catalogue'} /> : <Shell />;
}

/** Dev-only host. Families render in the same theme, gesture root and safe-area providers. */
export function Kit({initialGallery, presentation = 'catalogue'}: {initialGallery?: string; presentation?: 'catalogue' | 'window'}) {
  const theme = useTheme();
  const [selected, setSelected] = useState<string | undefined>(initialGallery);
  const [focused, setFocused] = useState<string>();
  const available = galleriesForPlatform(galleries, theme.platform);
  const current = available.find(gallery => gallery.id === selected);
  const Screen = current?.render;
  const t = defaultI18n.t;
  // The private compiled evidence entry injects this Kit through App's normal
  // KitHost shell slot. Families keep the catalogue gutter they were authored in;
  // Canvas's negative gutter therefore cancels exactly once, at the window edge.
  if (presentation === 'window' && Screen) return <SafeAreaView testID="native-kit-window" edges={[]} style={{flex: 1}}><View style={{flex: 1, paddingHorizontal: theme.gutter}}><Screen /></View></SafeAreaView>;
  return <SafeAreaView testID="native-kit" style={{flex: 1}}>
    <ScrollView contentContainerStyle={{padding: theme.gutter, gap: theme.space.md}}>
      <Text accessibilityRole="header" style={[theme.type.heading, {color: theme.colors.text}]}>{t('next.platform.kitTitle')}</Text>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.xs}}>
        {[{id: '', title: 'next.platform.allFamilies' as const}, ...available].map((gallery, index) => <Pressable key={gallery.id} testID={`gallery-${gallery.id || 'all'}`} accessibilityRole="button" accessibilityLabel={t(gallery.title)} accessibilityState={{selected: (selected ?? '') === gallery.id}} hasTVPreferredFocus={theme.isTV && index === 0} onFocus={() => setFocused(gallery.id)} onBlur={() => setFocused(undefined)} onPress={() => setSelected(gallery.id || undefined)} style={{minHeight: theme.isTV ? theme.targets.tenFoot : theme.targets.touch, paddingHorizontal: theme.space.sm, justifyContent: 'center', borderRadius: theme.baseRadii.control, borderWidth: theme.spec.focus.ringWidth[theme.isTV ? 'tv' : 'phone'], borderColor: focused === gallery.id ? theme.colors.focus : theme.colors.line, backgroundColor: theme.colors.raised}}><Text style={[theme.type.nav, {color: theme.colors.text}]}>{t(gallery.title)}</Text></Pressable>)}
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
