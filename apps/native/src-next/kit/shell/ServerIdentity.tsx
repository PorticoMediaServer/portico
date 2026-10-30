import React, {useId, type ReactNode} from 'react';
import {StyleSheet, Text, View, type StyleProp, type ViewStyle} from 'react-native';
import Svg, {Defs, LinearGradient, Mask, RadialGradient, Rect, Stop} from 'react-native-svg';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import {Button} from '../controls/Button';
import {Icon} from '../Icon';
import {useTheme} from '../theme';
import {focusTestID} from '../focus/registry';
import {alpha, shellType} from './visual';
import {canSeeServerPage, serverVisibilityKey, type ServerAudience, type ServerRole} from './authority';

export type ServerMarkVariant = 'state' | 'stateCompact' | 'settings' | 'phone' | 'tv' | 'editorDesktop' | 'editorPhone';
export const serverMarkGeometry = {
  state: {size: 26, radius: 7, icon: 15},
  editorDesktop: {size: 34, radius: 9, icon: 20},
  editorPhone: {size: 24, radius: 7, icon: 15},
  stateCompact: {size: 22, radius: 7, icon: 13},
  settings: {size: 34, radius: 9, icon: 18},
  phone: {size: 36, radius: 10, icon: 18},
  tv: {size: 52, radius: 13, icon: 28},
} as const;

/** The actual server glyph is decorative. Its enclosing identity owns the accessible name. */
export function ServerMark({variant = 'state', style, testID}: {variant?: ServerMarkVariant; style?: StyleProp<ViewStyle>; testID?: string}) {
  const {colors} = useTheme();
  const id = useId();
  const geometry = serverMarkGeometry[variant];
  return <View testID={testID} accessible={false} pointerEvents="none" style={[styles.mark, {width: geometry.size, height: geometry.size, borderRadius: geometry.radius}, style]}>
    {variant === 'phone' ? <View style={[StyleSheet.absoluteFill, {backgroundColor: alpha(colors.ink, .5)}]}/> : <Svg width="100%" height="100%" style={StyleSheet.absoluteFill} accessible={false}>
      <Defs><LinearGradient id={id} x1="0%" y1="0%" x2="100%" y2="100%"><Stop offset="0" stopColor={colors.accentDeep}/><Stop offset="1" stopColor={colors.generatedGreen}/></LinearGradient></Defs>
      <Rect width="100%" height="100%" fill={`url(#${id})`}/>
    </Svg>}
    <Icon name="i-server" size={geometry.icon} color={variant === 'phone' ? colors.accentStrong : colors.white}/>
  </View>;
}

export type ShellTranslate = typeof defaultI18n.t;
/** Admission supplies authority; administrators and editors supply their actual localized audience. */
export type EditorAudience = Readonly<{authority: 'owner'; label?: never} | {authority: 'administrator' | 'editor'; label: string}>;
export type ServerAudienceTagProps = {
  audience: EditorAudience;
  layout?: 'desktop' | 'phone';
  style?: StyleProp<ViewStyle>;
  testID?: string;
  t?: ShellTranslate;
};
/** Owns the one mark and Server pill; the separate OwnerTag remains a restriction badge. */
export function ServerAudienceTag({audience, layout = 'desktop', style, testID, t = defaultI18n.t}: ServerAudienceTagProps) {
  const {colors, textScale} = useTheme();
  const phone = layout === 'phone';
  const label = audience.authority === 'owner' ? t(phone ? 'next.shell.editorAudience.ownerPhone' : 'next.shell.editorAudience.owner') : audience.label;
  return <View testID={testID} style={[{flexDirection: 'row', alignItems: 'center', gap: phone ? 10 : 12}, style]}>
    <ServerMark variant={phone ? 'editorPhone' : 'editorDesktop'} />
    <View style={{flexDirection: 'row', alignItems: 'center', height: phone ? 28 : 30, gap: phone ? 6 : 8, paddingHorizontal: phone ? 10 : 12, borderRadius: 8, backgroundColor: alpha(colors.ink, .55)}}>
      <Text style={[shellType(phone ? 12 : 13, 700, 'normal', 0, textScale), {color: colors.text}]}>{t('next.shell.server')}</Text>
      <Text style={[shellType(phone ? 11.5 : 13, 600, 'normal', 0, textScale), {color: colors.textTertiary}]}>{label}</Text>
    </View>
  </View>;
}
export type EditorHeaderProps = ServerAudienceTagProps & {
  title: string;
  titleId?: string;
  description?: ReactNode;
  onClose: () => void;
  children?: ReactNode;
};
export function EditorHeader({audience, layout = 'desktop', title, titleId, description, onClose, children, style, testID, t = defaultI18n.t}: EditorHeaderProps) {
  const {colors, textScale} = useTheme();
  const phone = layout === 'phone', id = useId();
  return <View testID={testID} style={[{position: 'relative', paddingTop: phone ? 8 : 26, paddingHorizontal: phone ? 20 : 32, paddingBottom: phone ? 12 : 18}, style]}>
    <Svg accessible={false} pointerEvents="none" width="100%" height="100%" style={StyleSheet.absoluteFill}>
      <Defs>
        <RadialGradient id={`${id}blue`} cx="100%" cy="0%" rx="120%" ry="120%"><Stop offset="0" stopColor={colors.accentDeep} stopOpacity={phone ? .28 : .26} /><Stop offset="0.62" stopColor={colors.accentDeep} stopOpacity="0" /></RadialGradient>
        <RadialGradient id={`${id}green`} cx="0%" cy="100%" rx="100%" ry="100%"><Stop offset="0" stopColor={colors.generatedGreen} stopOpacity=".2" /><Stop offset="0.66" stopColor={colors.generatedGreen} stopOpacity="0" /></RadialGradient>
      </Defs>
      <Rect width="100%" height="100%" fill={`url(#${id}blue)`} /><Rect width="100%" height="100%" fill={`url(#${id}green)`} />
    </Svg>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: phone ? 10 : 12, marginTop: phone ? 4 : 0}}>
      <ServerAudienceTag audience={audience} layout={layout} t={t} />
      <Button testID={controlID(testID, 'dismiss')} variant="icon" size="xs" icon="i-x" accessibilityLabel={t('next.shell.close')} onPress={onClose} style={{marginLeft: 'auto', marginRight: phone ? 0 : -8, width: phone ? 34 : 40, height: phone ? 34 : 40, borderRadius: 20, backgroundColor: phone ? alpha(colors.text, .1) : 'transparent'}} />
    </View>
    <Text nativeID={titleId} accessibilityRole="header" style={[shellType(phone ? 24 : 30, 800, 'normal', phone ? -.03 : -.035, textScale), {color: colors.text, marginTop: phone ? 10 : 16, marginBottom: phone ? 4 : 6}]}>{title}</Text>
    {description !== undefined ? <Text style={[shellType(phone ? 13 : 14.5, 400, phone ? 18.85 : 21.75, 0, textScale), {color: colors.textSecondary, maxWidth: 470}]}>{description}</Text> : null}
    {children}
  </View>;
}

export type OwnerTagProps = {
  /** A real server name, section title or other caller-owned identity. Never fixture text. */
  identity?: ReactNode;
  audience?: ServerAudience;
  /** The badge is a policy label. Members may see it on the owner-only route's state. */
  variant?: 'state' | 'badge';
  compact?: boolean;
  style?: StyleProp<ViewStyle>;
  testID?: string;
};
export function OwnerTag({identity, audience = 'owner', variant = 'state', compact = false, style, testID}: OwnerTagProps) {
  const {colors, textScale} = useTheme();
  const blur = glassRecipe();
  const label = defaultI18n.t(audience === 'owner' ? 'next.shell.ownerOnly' : 'next.shell.administrators');
  if (variant === 'badge') return <View testID={testID} style={[styles.badge, {backgroundColor: alpha(colors.ink, .6)}, style]}><Text style={[shellType(12, 700, 'normal', 0, textScale), {color: colors.textSecondary}]}>{label}</Text></View>;
  return <View testID={testID} style={[styles.ownerTag, {backgroundColor: alpha(colors.ink, .55)}, style]}>
    {blur.nativeBlur && <BlurView pointerEvents="none" style={StyleSheet.absoluteFill} blurType="dark" blurAmount={14} reducedTransparencyFallbackColor={colors.ink}/>}
    <ServerMark variant={compact ? 'stateCompact' : 'state'}/>
    {identity != null && <Text numberOfLines={1} style={[shellType(13.5, 700, 'normal', 0, textScale), {color: colors.text, flexShrink: 1}]}>{identity}</Text>}
    <Text style={[shellType(13.5, 600, 'normal', 0, textScale), {color: colors.textTertiary}]}>{label}</Text>
  </View>;
}

/** The same band on tablet and TV: intersecting left/down fades, never a decorative letter. */
export function ServerBand({viewerRole, audience, height, style, testID}: {viewerRole: ServerRole; audience: ServerAudience; height?: number; style?: StyleProp<ViewStyle>; testID?: string}) {
  const {colors, isTV} = useTheme();
  const id = useId();
  if (!canSeeServerPage(viewerRole, audience)) return null;
  return <View testID={testID} pointerEvents="none" accessible={false} style={[styles.band, {height: height ?? (isTV ? 520 : 420)}, style]}>
    <Svg width="100%" height="100%" viewBox="0 0 1000 1000" preserveAspectRatio="none" accessible={false}>
      <Defs>
        <RadialGradient id={`${id}blue`} cx="84%" cy="-12%" rx="50%" ry="120%" gradientUnits="objectBoundingBox"><Stop offset="0" stopColor={colors.accentDeep}/><Stop offset="0.62" stopColor={colors.accentDeep} stopOpacity="0"/></RadialGradient>
        <RadialGradient id={`${id}green`} cx="56%" cy="96%" rx="40%" ry="90%"><Stop offset="0" stopColor={colors.generatedGreen}/><Stop offset="0.66" stopColor={colors.generatedGreen} stopOpacity="0"/></RadialGradient>
        <LinearGradient id={`${id}left`} x1="0" x2="1" y1="0" y2="0"><Stop offset="0.14" stopColor={colors.white} stopOpacity="0"/><Stop offset={isTV ? '0.56' : '0.5'} stopColor={colors.white}/></LinearGradient>
        <LinearGradient id={`${id}down`} x1="0" x2="0" y1="0" y2="1"><Stop offset="0.3" stopColor={colors.ink} stopOpacity="0"/><Stop offset="1" stopColor={colors.ink}/></LinearGradient>
        <Mask id={`${id}mask`} x="0" y="0" width="1000" height="1000"><Rect width="1000" height="1000" fill={`url(#${id}left)`}/></Mask>
      </Defs>
      <Rect width="1000" height="1000" fill={colors.raisedSoft} mask={`url(#${id}mask)`}/>
      <Rect width="1000" height="1000" fill={`url(#${id}green)`} mask={`url(#${id}mask)`}/>
      <Rect width="1000" height="1000" fill={`url(#${id}blue)`} mask={`url(#${id}mask)`}/>
      <Rect width="1000" height="1000" fill={`url(#${id}down)`}/>
    </Svg>
  </View>;
}

export type ServerFact = Readonly<{id: string; text: string; tone?: 'normal' | 'warning' | 'muted'}>;
export type ServerHeaderProps = {
  serverName: string;
  viewerRole: ServerRole;
  audience: ServerAudience;
  facts?: readonly ServerFact[];
  variant?: 'phone' | 'page' | 'group';
  /** TV pages a viewer cannot change carry the View only tag (tv-more 35). */
  viewOnly?: boolean;
  style?: StyleProp<ViewStyle>;
  testID?: string;
};
export function ServerHeader({serverName, viewerRole, audience, facts = [], variant = 'page', viewOnly = true, style, testID}: ServerHeaderProps) {
  const {colors, isTV, textScale} = useTheme();
  const visibility = serverVisibilityKey(viewerRole, audience);
  if (!visibility) return null;
  const text = (size: number, weight: 600 | 700 | 800 = 600, tracking = 0) => shellType(size, weight, 'normal', tracking, textScale);
  if (variant !== 'page') return <View testID={testID} style={[styles.groupHeader, {gap: isTV ? 16 : 12, padding: variant === 'phone' ? 14 : 0}, style]}>
    {variant === 'phone' && <Svg width="100%" height="100%" style={StyleSheet.absoluteFill} accessible={false} pointerEvents="none"><Defs><LinearGradient id="server-phone-header" x1="0%" y1="0%" x2="100%" y2="57.735%"><Stop offset="0" stopColor={colors.accent} stopOpacity=".3"/><Stop offset="0.7" stopColor={colors.accent} stopOpacity=".06"/></LinearGradient></Defs><Rect width="100%" height="100%" fill="url(#server-phone-header)"/></Svg>}
    <ServerMark variant={isTV ? 'tv' : variant === 'phone' ? 'phone' : 'settings'}/>
    <View style={{flex: 1, minWidth: 0}}>
      {variant === 'phone' ? <><Text style={[text(12, 700), {color: colors.accentStrong}]}>{defaultI18n.t('next.shell.server')}</Text><Text numberOfLines={1} style={[text(16.5, 800, -.015), {color: colors.text}]}>{serverName}</Text></> : <><Text style={[text(isTV ? 24 : 15, 800), {color: colors.text}]}>{defaultI18n.t('next.shell.server')}</Text><Text numberOfLines={1} style={[text(isTV ? 18 : 13), {color: colors.textTertiary, marginTop: isTV ? 2 : 1}]}>{serverName}</Text></>}
    </View>
    <View style={[styles.badge, {backgroundColor: alpha(colors.ink, variant === 'phone' ? .5 : .6), paddingVertical: isTV || variant === 'phone' ? 4 : 3, paddingHorizontal: isTV ? 12 : 8, borderRadius: isTV ? 8 : 6}]}><Text style={[text(isTV ? 18 : variant === 'phone' ? 11.5 : 12, 700), {color: colors.textSecondary}]}>{defaultI18n.t(viewerRole === 'administrator' ? 'next.shell.administrators' : 'next.shell.ownerOnly')}</Text></View>
  </View>;
  return <View testID={testID} style={style}>
    <View style={[styles.pill, {height: isTV ? 46 : 30, borderRadius: isTV ? 12 : 8, paddingLeft: isTV ? 14 : 9, paddingRight: isTV ? 18 : 12, gap: isTV ? 12 : 8, backgroundColor: alpha(colors.ink, isTV ? .6 : .55)}]}>
      <Icon name="i-server" size={isTV ? 22 : 16}/><Text style={[text(isTV ? 20 : 13, 700), {color: colors.text}]}>{defaultI18n.t('next.shell.server')}</Text>
      <Text style={[text(isTV ? 20 : 13), {color: colors.textTertiary}]}>{defaultI18n.t(visibility)}</Text>
      {isTV && viewOnly && <Text style={[text(20, 700), {color: colors.text, backgroundColor: alpha(colors.text, .14), paddingVertical: 2, paddingHorizontal: 10, borderRadius: 7}]}>{defaultI18n.t('next.shell.viewOnly')}</Text>}
    </View>
    <View style={[styles.facts, {marginTop: isTV ? 0 : 16, marginBottom: isTV ? 22 : 40, gap: isTV ? 26 : 22}]}>
      {!isTV && <Text numberOfLines={1} style={[shellType(26, 800, 26, -.03, textScale), {color: colors.text, flexShrink: 1}]}>{serverName}</Text>}
      {facts.map(fact => <View key={fact.id} style={{flexDirection: 'row', alignItems: 'center', gap: isTV ? 10 : 8}}>
        {fact.tone && fact.tone !== 'muted' && <View style={{width: isTV ? 12 : 9, height: isTV ? 12 : 9, borderRadius: 6, backgroundColor: fact.tone === 'warning' ? colors.amber : colors.healthy, boxShadow: `0 0 0 4px ${alpha(fact.tone === 'warning' ? colors.amber : colors.healthy, .16)}`}}/>}
        <Text style={[text(isTV ? 22 : 15, fact.tone && fact.tone !== 'muted' ? 700 : 600), {color: fact.tone === 'normal' ? colors.text : fact.tone === 'warning' ? colors.amber : colors.textSecondary}]}>{fact.text}</Text>
      </View>)}
    </View>
  </View>;
}
const styles = StyleSheet.create({
  mark: {alignItems: 'center', justifyContent: 'center', overflow: 'hidden', flexShrink: 0},
  ownerTag: {flexDirection: 'row', alignItems: 'center', alignSelf: 'flex-start', gap: 10, paddingTop: 6, paddingBottom: 6, paddingLeft: 6, paddingRight: 12, borderRadius: 10, overflow: 'hidden'},
  badge: {alignSelf: 'center', paddingVertical: 3, paddingHorizontal: 8, borderRadius: 6},
  band: {position: 'absolute', top: 0, left: 0, right: 0, overflow: 'hidden'},
  groupHeader: {flexDirection: 'row', alignItems: 'center'},
  pill: {alignSelf: 'flex-start', flexDirection: 'row', alignItems: 'center', marginBottom: 14},
  facts: {flexDirection: 'row', alignItems: 'baseline', flexWrap: 'wrap'},
});

const controlID = (prefix: string | undefined, ...parts: readonly string[]) => prefix ? focusTestID(prefix, JSON.stringify(parts)) : undefined;
