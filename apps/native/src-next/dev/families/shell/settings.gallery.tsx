import React, {useState, type ReactNode} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {shellNativeCases as fixture} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/shell-native-fixtures';
import type {Gallery} from '../../gallery-registry';
import {OverlaysProvider} from '../../../kit/overlays/OverlayHost';
import {Select} from '../../../kit/controls/Select';
import {Switch} from '../../../kit/controls/Switch';
import {Button} from '../../../kit/controls/Button';
import {useTheme} from '../../../kit/theme';
import {ChoiceCards, ChoiceMatrix, DefaultHint, LanguageList, LargeTitleHeader, PushedNavBar, SettingsNav, SettingsRow, SettingsSheet, ShortcutTiles, SubtitlePreview, ProfileAvatar} from '../../../kit/shell';
import type {SettingsNavPage, LanguageOption} from '../../../kit/shell';

function Frame({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <ScrollView contentContainerStyle={{padding: theme.isTV ? 96 : 16, gap: 20}}>{children}</ScrollView>;
}
function SettingsControlsGallery() {
  const theme = useTheme();
  const [skip, setSkip] = useState<string>(fixture.defaultSkip);
  const [method, setMethod] = useState<string>(fixture.defaultMethod);
  const [autoplay, setAutoplay] = useState(true);
  const [credits, setCredits] = useState(false);
  const [countdown, setCountdown] = useState('10000');
  const skipOptions = [{value: 'ask', label: defaultI18n.t('pref.value.playback.introSkip.ask')}, {value: 'auto', label: defaultI18n.t('pref.value.playback.introSkip.auto')}, {value: 'off', label: defaultI18n.t('pref.value.playback.introSkip.off')}];
  const methodOptions = [{value: 'automatic', label: defaultI18n.t('settings.playbackMethod.automatic')}, {value: 'original', label: defaultI18n.t('settings.playbackMethod.original')}, {value: 'convert', label: defaultI18n.t('settings.playbackMethod.convert')}];
  return <Frame><LargeTitleHeader title={defaultI18n.t('settings.page.playback')}/><View style={{borderRadius: theme.isTV ? 18 : 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
    <SettingsRow label={defaultI18n.t('settings.row.autoplayNext')} first control={<Switch testID="dev.shell.settings.autoplay" accessibilityLabel={defaultI18n.t('settings.row.autoplayNext')} checked={autoplay} onCheckedChange={setAutoplay}/>}/>
    {autoplay && <SettingsRow label={defaultI18n.t('settings.row.countdown')} dependent continuesBranch control={<Select testID="dev.shell.settings.countdown" accessibilityLabel={defaultI18n.t('settings.row.countdown')} options={[0, 5, 10, 15].map(seconds => ({value: String(seconds * 1000), label: seconds === 0 ? defaultI18n.t('pref.value.playback.upNextCountdownMs.0') : defaultI18n.t('pref.unit.seconds', {count: seconds})}))} value={countdown} onValueChange={setCountdown}/>}/>}
    {autoplay && <SettingsRow label={defaultI18n.t('settings.row.startNextDuringCredits')} dependent control={<Switch testID="dev.shell.settings.credits" accessibilityLabel={defaultI18n.t('settings.row.startNextDuringCredits')} checked={credits} onCheckedChange={setCredits}/>}/>}
  </View>
    <ChoiceMatrix testID="dev.shell.settings.skip" rows={[{id: 'intro', label: defaultI18n.t('web.segments.kind', {kind: 'intro'}), value: skip, defaultHint: <DefaultHint testID="dev.shell.settings.skip-default" value={skipOptions[0].label} changed={skip !== fixture.defaultSkip} onReset={() => setSkip(fixture.defaultSkip)}/>}, {id: 'credits', label: defaultI18n.t('web.segments.kind', {kind: 'credits'}), value: skip}]} options={skipOptions} onChange={(_id, value) => setSkip(value)}/>
    <ChoiceCards testID="dev.shell.settings.quality" label={defaultI18n.t('settings.page.quality')} value={method} options={methodOptions} onChange={setMethod}/>
  </Frame>;
}
function LanguageGallery() {
  const [languages, setLanguages] = useState<readonly LanguageOption[]>(fixture.languages);
  return <Frame><LanguageList testID="dev.shell.settings.audio-languages" label={defaultI18n.t('settings.section.audio')} value={languages} available={fixture.availableLanguages} limit={8} onChange={setLanguages}/></Frame>;
}
function SubtitleGallery() {
  const [size, setSize] = useState<'small' | 'medium' | 'large' | 'extra-large'>(fixture.subtitleSize);
  const [background, setBackground] = useState<'none' | 'translucent' | 'opaque'>(fixture.subtitleBackground);
  return <Frame><SubtitlePreview source={fixture.subtitleSource} size={size} background={background}/><Select testID="dev.shell.settings.size" accessibilityLabel={defaultI18n.t('settings.row.subtitleSize')} value={size} options={(['small', 'medium', 'large', 'extra-large'] as const).map(value => ({value, label: defaultI18n.t(`settings.subtitleSize.${value}`)}))} onValueChange={value => setSize(value as typeof size)}/><Select testID="dev.shell.settings.background" accessibilityLabel={defaultI18n.t('settings.row.subtitleBackground')} value={background} options={(['none', 'translucent', 'opaque'] as const).map(value => ({value, label: defaultI18n.t(`settings.subtitleBackground.${value}`)}))} onValueChange={value => setBackground(value as typeof background)}/></Frame>;
}
function SettingsSheetGallery() {
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState('playback');
  const [message, setMessage] = useState('');
  const {height} = useWindowDimensions();
  const theme = useTheme();
  const accountPages: SettingsNavPage[] = [
    {id: 'profile', label: defaultI18n.t('settings.page.profile'), icon: 'i-person'},
    {id: 'playback', label: defaultI18n.t('settings.page.playback'), icon: 'i-play'},
    {id: 'audio', label: defaultI18n.t('settings.page.audioSubtitles'), icon: 'i-subs'},
    {id: 'quality', label: defaultI18n.t('settings.page.quality'), icon: 'i-sliders'},
  ];
  const actions = [
    {id: 'profiles', label: defaultI18n.t('next.shell.switchProfile'), icon: 'i-person' as const, onPress: () => setMessage(defaultI18n.t('next.shell.switchProfile'))},
    {id: 'servers', label: defaultI18n.t('next.shell.switchServer'), icon: 'i-server' as const, onPress: () => setMessage(defaultI18n.t('next.shell.switchServer'))},
    {id: 'notifications', label: defaultI18n.t('next.shell.notifications'), icon: 'i-bell' as const, onPress: () => setMessage(defaultI18n.t('next.shell.notifications'))},
    {id: 'together', label: defaultI18n.t('next.shell.watchTogether'), icon: 'i-people' as const, onPress: () => setMessage(defaultI18n.t('next.shell.watchTogether'))},
  ];
  return <Frame><Button size="small" onPress={() => setOpen(true)}>{defaultI18n.t('next.shell.settings')}</Button><PushedNavBar title={defaultI18n.t('next.shell.settings')} backLabel={defaultI18n.t('action.back')} onBack={() => setOpen(false)}/><Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{message}</Text><SettingsSheet open={open} onOpenChange={setOpen} detents={[height - 62]} profile={<View style={{padding: 16}}><ProfileAvatar profile={fixture.profiles[0]} size={54} onPress={() => setMessage(fixture.profiles[0].name)}/></View>} shortcuts={<ShortcutTiles actions={actions}/>}><SettingsNav accountPages={accountPages} serverPages={[]} viewerRole="owner" serverName={fixture.profiles[0].name} selectedId={selected} onNavigate={id => {setSelected(id); setMessage(accountPages.find(page => page.id === id)?.label ?? '');}}/></SettingsSheet></Frame>;
}
function wrap(Render: () => ReactNode) {return function GalleryRender() {return <OverlaysProvider><Render/></OverlaysProvider>;};}
const galleries: readonly Gallery[] = [
  {id: 'shell-settings-rows', title: 'next.shell.native.rowGallery', render: wrap(SettingsControlsGallery), platforms: ['phone', 'tablet', 'tv']},
  {id: 'shell-languages', title: 'next.shell.native.languageGallery', render: wrap(LanguageGallery), platforms: ['phone', 'tablet', 'tv']},
  {id: 'shell-subtitles', title: 'next.shell.native.subtitleGallery', render: wrap(SubtitleGallery), platforms: ['tablet', 'tv']},
  {id: 'shell-settings-sheet', title: 'next.shell.native.settingsGallery', render: wrap(SettingsSheetGallery), platforms: ['phone', 'tablet']},
];
export default galleries;
