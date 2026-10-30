import React, {useState} from 'react';
import {Text, View} from 'react-native';
import {profileArtColors} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {PROFILE_ART, type ProfileArt} from '../../../../../packages/client-core/src/profile-management';
import type {ProfileRestrictions, RatingSystem} from '../../../../../packages/client-core/src/identity-restrictions';
import {Button, Focusable, Select, Switch, TextField} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {SettingsRow} from '../../kit/shell';
import {useTheme} from '../../kit/theme';
import {addHiddenLabel} from '../accounts/account-form-values';

const t = defaultI18n.t;

const profileArtLabels = {
  blue: 'next.settingsSpec.account.profile.groups.1.rows.1.control.options.0.label',
  violet: 'next.settingsSpec.account.profile.groups.1.rows.1.control.options.1.label',
  mint: 'next.settingsSpec.account.profile.groups.1.rows.1.control.options.2.label',
  coral: 'next.settingsSpec.account.profile.groups.1.rows.1.control.options.3.label',
  gold: 'next.settingsSpec.account.profile.groups.1.rows.1.control.options.4.label',
  slate: 'next.settingsSpec.account.profile.groups.1.rows.1.control.options.5.label',
  rose: 'next.settingsSpec.account.profile.groups.1.rows.1.control.options.6.label',
  sky: 'next.settingsSpec.account.profile.groups.1.rows.1.control.options.7.label',
} as const;

export function ProfileColorPicker({value, onChange, disabled, testID}: {value: ProfileArt; onChange: (value: ProfileArt) => void; disabled: boolean; testID: string}) {
  const theme = useTheme();
  return <View testID={testID} style={{gap: 10, marginTop: 16}}>
    <Text style={[theme.type.captionStrong, {color: theme.colors.text}]}>{t('next.settingsSpec.account.profile.groups.1.rows.1.label')}</Text>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 6}}>{PROFILE_ART.map(art => <Focusable key={art} testID={`${testID}-${art}`}
      accessibilityRole="radio" accessibilityLabel={t(profileArtLabels[art])} accessibilityState={{selected: art === value}}
      disabled={disabled} onPress={() => onChange(art)} focusRadius={24} style={{width: 46, height: 46, alignItems: 'center', justifyContent: 'center'}}>
      <View style={{width: 44, height: 44, borderRadius: 22, alignItems: 'center', justifyContent: 'center', borderWidth: art === value ? 2 : 0, borderColor: theme.colors.text}}>
        <View style={{width: 34, height: 34, borderRadius: 17, backgroundColor: profileArtColors[art]}}/>
      </View>
    </Focusable>)}</View>
  </View>;
}

/** The same server-owned scale, label limits and feature switches for one's
 * own profile and People's editor. No picture or rating-system chooser. */
/** `recordingLocked`: Recordings is off and the caller is not the server owner, who alone may
 * switch it on (Who can record). Switching it off is open to anyone who manages the profile. */
export function ProfileRestrictionsFields({draft, systems, disabled, recordingLocked = false, current, onChange}: {draft: ProfileRestrictions; systems: readonly RatingSystem[]; disabled: boolean; recordingLocked?: boolean; current: () => boolean; onChange: (change: Partial<ProfileRestrictions>) => void}) {
  const theme = useTheme();
  const [labelsOpen, setLabelsOpen] = useState(false), [labelsDraft, setLabelsDraft] = useState<readonly string[]>([]), [labelDraft, setLabelDraft] = useState('');
  const system = systems.find(value => value.id === draft.ratingSystem) ?? (draft.ratingSystem === '' ? systems[0] : undefined);
  const change = (patch: Partial<ProfileRestrictions>) => {if (current() && !disabled) onChange(patch);};
  const ratingOptions = [{value: '', label: t('limits.noLimit')}, ...(system?.values.map(value => ({value: value.code, label: value.label})) ?? [])];
  // Keep an unknown saved rating visible, without treating it as a new grant.
  if (draft.maximumAgeRating && !ratingOptions.some(value => value.value === draft.maximumAgeRating)) ratingOptions.push({value: draft.maximumAgeRating, label: draft.maximumAgeRating});
  return <View style={{gap: 14}}>
    <Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary}]}>{t('limits.content')}</Text>
    <Select focusId="maximum-age-rating" testID="profile-limits-maximum-age-rating" label={t('limits.highestRating')} description={t('limits.highestRatingHelp')} value={draft.maximumAgeRating} options={ratingOptions} disabled={disabled || !system}
      onValueChange={value => {if (system && (value === '' || system.values.some(rating => rating.code === value))) change({ratingSystem: system.id, maximumAgeRating: value});}}/>
    <SettingsRow first label={t('limits.allowUnrated')} description={t('limits.allowUnratedHelp')} control={<Switch focusId="allow-unrated" accessibilityLabel={t('limits.allowUnrated')} checked={draft.allowUnrated} disabled={disabled} onCheckedChange={allowUnrated => change({allowUnrated})}/>}/>
    {theme.isTV ? draft.blockedLabels.length ? <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('limits.hiddenLabels', {labels: draft.blockedLabels.join(', ')})}</Text> : null : <SettingsRow testID="profile-hidden-labels" label={t('web.limits.hiddenLabels')} description={draft.blockedLabels.join(', ') || t('web.dvr.none')} onPress={() => {
      if (!current() || disabled) return;
      setLabelsDraft([...draft.blockedLabels]); setLabelDraft(''); setLabelsOpen(true);
    }}/>}
    <Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary}]}>{t('limits.features')}</Text>
    {([
      ['allowDownloads', 'limits.feature.downloads', 'web.limits.downloadsHelp'],
      ['allowLiveTv', 'limits.feature.liveTv', 'web.limits.liveTvHelp'],
      ['allowDvr', 'limits.feature.dvr', 'web.limits.dvrHelp'],
      ['allowWatchTogether', 'limits.feature.watchTogether', 'web.limits.togetherHelp'],
    ] as const).map(([key, label, help]) => <SettingsRow key={key} label={t(label)} description={t(help)} control={<Switch focusId={`feature:${key}`} testID={`profile-feature-${key}`} accessibilityLabel={t(label)} checked={draft[key]} disabled={disabled || key === 'allowDvr' && recordingLocked} onCheckedChange={value => change({[key]: value})}/>}/>)}
    <Dialog testID="profile-hidden-labels-picker" open={labelsOpen} onOpenChange={open => {if (current() && !disabled) setLabelsOpen(open);}} title={t('web.limits.hiddenLabels')} description={t('next.settingsSpec.account.profile.groups.3.rows.2.help')}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={disabled} onPress={() => {if (current()) setLabelsOpen(false);}}>{t('action.cancel')}</Button>
        <Button disabled={disabled} onPress={() => {if (!current() || disabled) return; change({blockedLabels: [...labelsDraft]}); setLabelsOpen(false);}}>{t('action.done')}</Button></View>}>
      <View style={{gap: 10}}>
        {labelsDraft.length ? labelsDraft.map(label => <View key={label.toLowerCase()} style={{flexDirection: 'row', alignItems: 'center', gap: 10}}>
          <Text style={[theme.type.body, {color: theme.colors.text, flex: 1}]}>{label}</Text>
          <Button variant="text" disabled={disabled} accessibilityLabel={t('web.metadata.removeLabel', {label})} onPress={() => {if (current() && !disabled) setLabelsDraft(values => values.filter(value => value !== label));}}>{t('action.remove')}</Button>
        </View>) : <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('web.dvr.none')}</Text>}
        <TextField label={t('web.metadata.addLabelChip')} value={labelDraft} editable={!disabled} onChangeText={value => {if (current() && !disabled) setLabelDraft(value);}} maxLength={120} autoCorrect={false}/>
        <Button variant="glass" disabled={disabled || !addHiddenLabel(labelsDraft, labelDraft)} onPress={() => {if (!current() || disabled) return; const next = addHiddenLabel(labelsDraft, labelDraft); if (next) {setLabelsDraft(next); setLabelDraft('');}}}>{t('web.metadata.addLabelChip')}</Button>
      </View>
    </Dialog>
  </View>;
}
