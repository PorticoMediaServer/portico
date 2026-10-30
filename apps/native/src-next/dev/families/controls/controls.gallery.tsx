import React, {useRef, useState} from 'react';
import {Text, TextInput, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import type {NextMessageId} from '../../../../../../packages/i18n/src/catalog/en-US/next/index';
import {nativeControlsFixtureMessages} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/controls-native-fixtures';
import type {Gallery} from '../../gallery-registry';
import {Button, Chip, Focusable, ResumeButton, Segmented, Switch, Tabs, TextField, Select, RichSelect, type ButtonSize, type ButtonVariant, type ChipVariant} from '../../../kit/controls';
import {fontFamilies, nativeType, useTheme} from '../../../kit/theme';
import {OverlaysProvider} from '../../../kit/overlays/OverlayHost';

const t = defaultI18n.t;
const longTitle = nativeControlsFixtureMessages['next.controls.native.fixture.longTitle'];

function Frame({title, children}: {title: NextMessageId; children: React.ReactNode}) {
  const theme = useTheme();
  return <OverlaysProvider><View style={{gap: 24, padding: 8}}><Text accessibilityRole="header" style={[theme.type.heading, {color: theme.colors.text}]}>{t(title)}</Text>{children}</View></OverlaysProvider>;
}
function Count({value}: {value: number}) {
  const theme = useTheme();
  return <Text testID="controls-value" accessibilityLiveRegion="polite" style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('next.kit.items', {count: value})}</Text>;
}

export function ButtonsGallery() {
  const theme = useTheme();
  const [count, setCount] = useState(0);
  const [loading, setLoading] = useState(true);
  const press = () => setCount(value => value + 1);
  const variants: readonly ButtonVariant[] = ['primary', 'glass', 'text', 'icon'];
  const sizes: readonly ButtonSize[] = ['hero', 'compact', 'small', 'xs', 'episode', 'dock'];
  return <Frame title="next.controls.native.buttonsTitle">
    {sizes.map(size => <View key={size} style={{flexDirection: 'row', flexWrap: 'wrap', gap: 16}}>
      {variants.map(variant => <Button key={variant} variant={variant} size={size} icon={variant === 'icon' ? 'i-more' : 'i-play'}
        testID={`controls-button-${size}-${variant}`} accessibilityLabel={variant === 'icon' ? t('title.more') : undefined}
        onPress={press} onLongPressAction={() => setCount(value => value + 10)}>{t('entry.play')}</Button>)}
    </View>)}
    <Button testID="controls-button-disabled" disabled onPress={press}>{t('entry.play')}</Button>
    <Button testID="controls-button-loading" loading={loading} icon="i-play" onPress={press}>{t('entry.play')}</Button>
    <Button testID="controls-button-loading-text" loading={loading} onPress={press}>{t('action.tryAgain')}</Button>
    <Button testID="controls-button-loading-toggle" variant="glass" size="small" onPress={() => setLoading(value => !value)}>{t('action.tryAgain')}</Button>
    <Button testID="controls-button-long" variant="glass" onPress={press} style={{maxWidth: 250}}>{longTitle}</Button>
    <Button testID="controls-button-full50" onPress={press} style={{height: 50, alignSelf: 'stretch'}}>{t('action.tryAgain')}</Button>
    <Button testID="controls-button-trailing" trailingIcon="i-ext" iconSize={18} onPress={press}>{t('action.view')}</Button>
    <Button testID="controls-button-player40" icon="i-retry" iconSize={18} onPress={press}
      style={{height: 40, paddingHorizontal: 16, borderRadius: 10, gap: 7}} textStyle={{fontSize: 14.5 * theme.textScale}}>{t('action.tryAgain')}</Button>
    <Count value={count} />
  </Frame>;
}

export function ResumeGallery() {
  const [count, setCount] = useState(0);
  return <Frame title="next.controls.native.resumeTitle">
    {[0, .09, .67, 1].map(progress => <ResumeButton key={progress} progress={progress} testID={`controls-resume-${progress}`}
      remaining={t('next.kit.remaining', {time: t('next.kit.duration.hoursMinutes', {hours: 1, minutes: 26})})}
      onPress={() => setCount(value => value + 1)}>{t('entry.resume')}</ResumeButton>)}
    <ResumeButton variant="glass" size="small" progress={.67} remaining={t('next.kit.remaining', {time: t('next.kit.duration.minutes', {minutes: 27})})}
      testID="controls-resume-glass" onPress={() => setCount(value => value + 1)}>{t('entry.resume')}</ResumeButton>
    <Count value={count} />
  </Frame>;
}

export function ChipsGallery() {
  const [selected, setSelected] = useState<Record<string, boolean>>({facet: true, applied: true});
  const [count, setCount] = useState(0);
  const variants: readonly ChipVariant[] = ['default', 'quick', 'facet', 'applied', 'pill'];
  return <Frame title="next.controls.native.chipsTitle">
    {variants.map(variant => <View key={variant} style={{flexDirection: 'row', flexWrap: 'wrap', gap: 12}}>
      <Chip variant={variant} selected={!!selected[variant]} count={variant === 'default' ? 0 : 24}
        prefix={variant === 'applied' ? t('filter.field.genre') : undefined} icon={variant === 'quick' ? 'i-check' : undefined}
        trailingIcon={variant === 'applied' ? 'i-close' : undefined} testID={`controls-chip-${variant}`}
        onPress={() => setSelected(value => ({...value, [variant]: !value[variant]}))}>{t('search.chip.movies')}</Chip>
      <Chip variant={variant} dim count={2} testID={`controls-chip-${variant}-dim`} onPress={() => setCount(value => value + 1)}>{t('search.chip.shows')}</Chip>
      <Chip variant={variant} retry count={4} testID={`controls-chip-${variant}-retry`} onPress={() => setCount(value => value + 1)}>{t('action.tryAgain')}</Chip>
      <Chip variant={variant} disabled testID={`controls-chip-${variant}-disabled`} onPress={() => setCount(value => value + 1)}>{t('search.chip.all')}</Chip>
    </View>)}
    <Chip style={{maxWidth: 240}} onPress={() => setCount(value => value + 1)} testID="controls-chip-long">{longTitle}</Chip>
    <Chip defaultSelected count={2} countStyle="badge" onSelectedChange={() => setCount(value => value + 1)} testID="controls-chip-uncontrolled">{t('filter.quick.unwatched')}</Chip>
    <Count value={count} />
  </Frame>;
}

export function TabsGallery() {
  const [value, setValue] = useState('episodes');
  const theme = useTheme();
  const items = [
    {value: 'episodes', label: t('title.episodes'), panel: <Text style={[theme.type.body, {color: theme.colors.text}]}>{t('title.episodes')}</Text>},
    {value: 'details', label: t('title.details'), panel: <Text style={[theme.type.body, {color: theme.colors.text}]}>{t('title.details')}</Text>},
    {value: 'long', label: longTitle, disabled: true},
  ];
  return <Frame title="next.controls.native.tabsTitle">
    <Tabs items={items} value={value} onValueChange={setValue} testID="controls-tabs-controlled" />
    <Tabs items={items} defaultValue="details" testID="controls-tabs-uncontrolled" />
    {theme.isTV ? <Tabs items={items} value={value} onValueChange={setValue}
      contentContainerStyle={{paddingHorizontal: 0, borderBottomWidth: 0}} testID="controls-tabs-inline" /> : null}
  </Frame>;
}

export function SegmentedGallery() {
  const [value, setValue] = useState('any');
  const items = [{value: 'any', label: t('lib.any')}, {value: 'yes', label: t('next.kit.yes')}, {value: 'no', label: t('next.kit.no')}];
  return <Frame title="next.controls.native.segmentedTitle">
    {(['glass', 'browse', 'phone'] as const).map(variant => <Segmented key={variant} variant={variant} items={items} value={value} onValueChange={setValue} testID={`controls-segmented-${variant}`} />)}
    <Segmented items={items.map(item => ({...item, disabled: item.value === 'yes'}))} defaultValue="no" testID="controls-segmented-uncontrolled" />
  </Frame>;
}

export function SwitchesGallery() {
  const [checked, setChecked] = useState(true);
  return <Frame title="next.controls.native.switchesTitle">
    {(['compact', 'phone', 'episode', 'selection'] as const).map(variant => <View key={variant} style={{gap: 20}}>
      <Switch variant={variant} checked={checked} onCheckedChange={setChecked} testID={`controls-switch-${variant}-controlled`}>{t('settings.row.autoplayNext')}</Switch>
      <Switch variant={variant} defaultChecked={false} testID={`controls-switch-${variant}-uncontrolled`}>{t('settings.row.startNextDuringCredits')}</Switch>
      <Switch variant={variant} checked disabled testID={`controls-switch-${variant}-disabled`}>{t('settings.row.autoplayNext')}</Switch>
    </View>)}
  </Frame>;
}

export function FieldsGallery() {
  const [value, setValue] = useState('');
  const field = useRef<TextInput>(null);
  const theme = useTheme();
  return <Frame title="next.controls.native.fieldsTitle">
    <TextField ref={field} label={t('browse.field.search')} value={value} onChangeText={setValue} variant="search" leadingIcon="i-search"
      placeholder={t('search.placeholder')} clearButtonMode="while-editing" returnKeyType="search" testID="controls-field-search" />
    <TextField label={t('settings.row.autoplayNext')} description={t('settings.help.qualityHome')} defaultValue={t('entry.play')} testID="controls-field-default" />
    <TextField label={t('browse.field.search')} error={t('player.quality.tooSlowAny')} value={value} onChangeText={setValue} testID="controls-field-error" />
    <TextField label={t('browse.field.search')} editable={false} value={longTitle} testID="controls-field-disabled" />
    <TextField label={t('filter.field.genre')} variant="search" leadingIcon="i-search" iconSize={17} value={value} onChangeText={setValue}
      fieldStyle={{height: 38, borderRadius: 10, paddingHorizontal: 10, gap: 8, backgroundColor: theme.colors.raisedBright}}
      style={{fontSize: 14 * theme.textScale, fontFamily: fontFamilies[400]}} hitSlop={{top: 3, bottom: 3}}
      testID="controls-field-filter38" />
    <Button variant="text" size="small" onPress={() => {setValue(''); field.current?.focus();}} testID="controls-field-clear">{t('filter.clear')}</Button>
  </Frame>;
}

export function FocusGallery() {
  const [count, setCount] = useState(0);
  const theme = useTheme();
  return <Frame title="next.controls.native.galleryTitle">
    {(['button', 'row', 'chip', 'card'] as const).map(focusKind => <Focusable key={focusKind} testID={`controls-focus-${focusKind}`}
      focusKind={focusKind} hasTVPreferredFocus={theme.isTV && focusKind === 'button'}
      accessibilityRole="button" accessibilityLabel={t('entry.play')} onPress={() => setCount(value => value + 1)}
      onLongPressAction={() => setCount(value => value + 10)} style={{padding: 20, backgroundColor: theme.colors.raisedHigh, borderRadius: 10}}>
      {({focused}) => <Text style={[theme.type.body, {color: focused ? theme.colors.text : theme.colors.textSecondary}]}>{t('entry.play')}</Text>}
    </Focusable>)}
    <Count value={count} />
  </Frame>;
}

export function SelectGallery() {
  const [value, setValue] = useState('forced');
  const [open, setOpen] = useState(false);
  const options = [
    {value: 'off', label: t('settings.subtitleMode.off')},
    {value: 'forced', label: t('settings.subtitleMode.forced'), isDefault: true},
    {value: 'always', label: t('settings.subtitleMode.always')},
    {value: 'long', label: longTitle, disabled: true},
  ];
  return <Frame title="next.controls.native.selectTitle">
    <Select label={t('settings.row.subtitleMode')} options={options} value={value} onValueChange={setValue} open={open} onOpenChange={setOpen} testID="controls-select-controlled" />
    <Select label={t('settings.row.subtitleMode')} options={options} defaultValue="off" testID="controls-select-uncontrolled" />
    <Select label={t('settings.row.subtitleMode')} options={options} value="always" disabled testID="controls-select-disabled" />
    <Select label={t('settings.row.subtitleMode')} options={options} value="long" testID="controls-select-long" />
  </Frame>;
}

export function RichSelectGallery() {
  const [value, setValue] = useState('auto');
  const options = [
    {value: 'original', label: t('next.kit.quality.original'), description: t('next.kit.quality.originalDetail')},
    {value: 'auto', label: t('next.kit.quality.automatic'), description: t('settings.help.qualityAway'), isDefault: true},
    {value: 'long', label: longTitle, description: t('settings.help.qualityHome')},
    {value: 'disabled', label: t('settings.playbackMethod.convert'), description: t('player.quality.conversionOff'), disabled: true},
  ];
  return <Frame title="next.controls.native.richSelectTitle">
    <RichSelect label={t('settings.row.qualityAway')} options={options} value={value} onValueChange={setValue} testID="controls-rich-select-controlled" />
    <RichSelect label={t('settings.row.qualityHome')} options={options} defaultValue="original" testID="controls-rich-select-uncontrolled" />
    <RichSelect label={t('settings.row.qualityAway')} options={options} disabled testID="controls-rich-select-disabled" />
  </Frame>;
}

export function TVChoiceGallery() {
  const theme = useTheme();
  const [value, setValue] = useState('ask');
  const options = [
    {value: 'ask', label: t('pref.value.playback.introSkip.ask')},
    {value: 'auto', label: t('pref.value.playback.introSkip.auto')},
    {value: 'off', label: t('pref.value.playback.introSkip.off')},
  ];
  return <Frame title="next.controls.native.selectTitle">
    <Select label={t('pref.label.playback.introSkip')} options={options} value={value} onValueChange={setValue}
      pickerContext={<Text style={[nativeType({size: 22, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{t('settings.section.skipping')}</Text>}
      pickerDescription={t('pref.help.playback.introSkip')} testID="controls-select-tv-choice" />
  </Frame>;
}

const galleries: readonly Gallery[] = [
  {id: 'controls', title: 'next.controls.native.galleryTitle', render: FocusGallery},
  {id: 'controls-buttons', title: 'next.controls.native.buttonsTitle', render: ButtonsGallery},
  {id: 'controls-resume', title: 'next.controls.native.resumeTitle', render: ResumeGallery},
  {id: 'controls-chips', title: 'next.controls.native.chipsTitle', render: ChipsGallery},
  {id: 'controls-tabs', title: 'next.controls.native.tabsTitle', render: TabsGallery},
  {id: 'controls-segmented', title: 'next.controls.native.segmentedTitle', render: SegmentedGallery},
  {id: 'controls-switches', title: 'next.controls.native.switchesTitle', render: SwitchesGallery},
  {id: 'controls-fields', title: 'next.controls.native.fieldsTitle', render: FieldsGallery},
  {id: 'controls-select', title: 'next.controls.native.selectTitle', render: SelectGallery},
  {id: 'controls-rich-select', title: 'next.controls.native.richSelectTitle', render: RichSelectGallery},
  {id: 'controls-select-tv-choice', title: 'next.controls.native.selectTitle', render: TVChoiceGallery, platforms: ['tv']},
];
export default galleries;
