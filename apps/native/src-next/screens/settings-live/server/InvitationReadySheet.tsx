import React, {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {Image, Share, Text, View} from 'react-native';
import QRCode from 'react-native-qrcode-svg';
import Svg, {Defs, Path, RadialGradient, Rect, Stop, Text as SvgText} from 'react-native-svg';
import {invitationCardFacts} from '../../../../../../packages/client-core/src/invitation-card';
import {ShortCodeIssuer} from '../../../../../../packages/client-core/src/invitation-short-code-issuer';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {qrMatrix} from '../../../kit/qr-matrix';
import {BottomSheet} from '../../../kit/overlays';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import {copyText} from '../../../platform/clipboard';
import {invitationPictureSavePort} from '../../../platform/invitation-picture-save';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {InvitationOpening, NativePeopleSource} from './people-source';

type Picture = Readonly<{brand: string; link: string; expiry: string; serverName: string; access: string; why: string}>;
type SvgHandle = {toDataURL: (callback: (base64: string) => void) => void};

/**
 * Invitation ready on the phone (owner-people "Invitations on the phone", c):
 * the share card first, so a screenshot works, then Share, Copy link and Save
 * picture. The long link is fetched for this card only; the short code is its
 * own shown-once capability, issued with a new invitation and on request.
 */
export function InvitationReadySheet({source, opening, runtime, serverName, signIn}: {
  source: NativePeopleSource; opening: InvitationOpening; runtime: NativeShellRuntime; serverName: string; signIn?: 'local' | 'portico';
}) {
  const theme = useTheme(), t = runtime.text.t, invitation = opening.invitation!;
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [link, setLink] = useState<string>(), [failure, setFailure] = useState<unknown>(), [copied, setCopied] = useState(false), [saving, setSaving] = useState(false);
  const [picture, setPicture] = useState<Picture>();
  const issuer = useMemo(() => new ShortCodeIssuer(source.newOperationId), [source]);
  const code = useSyncExternalStore(issuer.subscribe, issuer.getSnapshot, issuer.getSnapshot);
  const life = useRef(new AbortController()), svg = useRef<SvgHandle | null>(null), pending = useRef<((base64: string) => void) | undefined>(undefined);
  const savePicture = useMemo(() => invitationPictureSavePort(), []);
  const current = () => !life.current.signal.aborted && source.current() && source.getSnapshot().invitationOpening === opening;
  const run = (operationId: string, signal: AbortSignal) => source.invitationShortCode(opening, operationId, signal);
  useLayoutEffect(() => {
    const owned = life.current;
    void source.loadLibraries();
    void source.invitationShareLink(opening).then(value => {if (value && !owned.signal.aborted) setLink(value);}, error => {if (!owned.signal.aborted) setFailure(error);});
    if (opening.ready === 'created') void issuer.issue(true, run);
    return () => {owned.abort(); issuer.dispose();};
    // One card, one opening.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  // The picture renders off screen only while it is being saved, then hands its PNG over once.
  useEffect(() => {
    if (!picture || !svg.current || !pending.current) return;
    const deliver = pending.current; pending.current = undefined;
    svg.current.toDataURL(deliver);
  }, [picture]);
  const facts = invitationCardFacts(serverName, invitation, state.libraries ?? []);
  const access = facts.allLibraries ? t('next.settings.allLibraries') : facts.libraryNames.length === facts.libraryCount && facts.libraryCount <= 3
    ? defaultI18n.list(facts.libraryNames) : t('next.settings.invitations.libraries', {count: facts.libraryCount});
  const expiry = t('api.credential.expires', {when: defaultI18n.dateTime(facts.expiresAt)});
  const share = async () => {
    if (!link || !current()) return;
    try {await Share.share({message: link});} catch (error) {if (current()) setFailure(error);}
  };
  const copy = async () => {
    if (!link || !current()) return;
    try {await copyText(link); if (current()) setCopied(true);} catch (error) {if (current()) setFailure(error);}
  };
  const save = async () => {
    if (!link || !savePicture || saving || !current()) return;
    setSaving(true); setFailure(undefined);
    try {
      const png = await new Promise<string>(resolve => {pending.current = resolve; setPicture({brand: t('accounts.brand'), link, expiry, serverName: facts.serverName, access, why: t('accounts.invitation.to')});});
      if (!current()) return;
      await savePicture(png, life.current.signal, current);
    } catch (error) {if (current() && (error as {name?: string})?.name !== 'AbortError') setFailure(error);}
    finally {if (current()) {setSaving(false); setPicture(undefined);}}
  };
  const caption = [{color: theme.colors.textSecondary}, theme.type.caption];
  return <BottomSheet testID="invitation-ready" open title={t('invite.readyTitle')} dismissOnBack={!state.busy} dismissOnGesture={!state.busy} dismissOnOutsidePress={!state.busy}
    onOpenChange={open => {if (!open && source.current()) source.closeInvitation();}}
    footer={<Button testID="invitation-ready-done" variant="glass" onPress={() => {if (source.current()) source.closeInvitation();}}>{t('action.done')}</Button>}>
    <View testID="invitation-card" style={{borderRadius: 20, padding: 20, gap: 6, backgroundColor: theme.colors.ink, borderWidth: 1, borderColor: theme.colors.line}}>
      <View style={{flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between'}}>
        <Image source={require('../../../../assets/wordmark.png')} accessibilityLabel={t('accounts.brand')} resizeMode="contain" style={{width: 15 * 512 / 117, height: 15}}/>
        <Text style={caption}>{expiry}</Text>
      </View>
      <Text style={[theme.type.body, {color: theme.colors.accent, marginTop: 16}]}>{t('accounts.invitation.to')}</Text>
      <Text accessibilityRole="header" style={[theme.type.title, {color: theme.colors.text}]}>{facts.serverName}</Text>
      <Text style={caption}>{access}</Text>
      <View accessible accessibilityRole="image" accessibilityLabel={t('qr.label')} style={{alignSelf: 'center', marginVertical: 14, padding: 12, borderRadius: 16, backgroundColor: '#ffffff'}}>
        {link ? <QRCode value={link} size={176} ecl="M" backgroundColor="#ffffff" color="#000000"/> : <View style={{width: 176, height: 176}}/>}
      </View>
      <Text style={[caption, {textAlign: 'center'}]}>{t('accounts.invitation.scanHelp')}</Text>
      {link && <Text testID="invitation-link" selectable numberOfLines={1} style={[theme.type.caption, {color: theme.colors.text, padding: 10, borderRadius: 12, backgroundColor: theme.colors.raised}]}>{link}</Text>}
      {code.phase === 'ready' && <View style={{alignItems: 'center', gap: 2, marginTop: 6}}>
        <Text style={caption}>{t('accounts.invitation.orEnterCode')}</Text>
        <Text testID="invitation-short-code" selectable style={[theme.type.title, {color: theme.colors.text, letterSpacing: 2}]}>{code.code}</Text>
      </View>}
      <Text style={[caption, {textAlign: 'center', marginTop: 8}]}>{t('accounts.invitation.shareJoinHelp', {method: signIn ?? 'other'})}</Text>
    </View>
    <View style={{gap: 8, marginTop: 14}}>
      <Button testID="invitation-share" icon="i-share" disabled={!link} onPress={() => {void share();}}>{t('invite.share')}</Button>
      <Button testID="invitation-copy-link" variant="glass" icon="i-copy" disabled={!link} onPress={() => {void copy();}}>{t(copied ? 'next.settings.invitations.linkCopied' : 'web.access.copyLink')}</Button>
      {savePicture && <Button testID="invitation-save-picture" variant="glass" icon="i-download" loading={saving} disabled={!link || saving} onPress={() => {void save();}}>{t('accounts.invitation.savePicture')}</Button>}
      <Text style={caption}>{t('accounts.invitation.copyAgainHelp')}</Text>
      {code.phase === 'ready' && <Text style={caption}>{t('web.access.shortCodeShownOnce')}</Text>}
      {code.phase === 'idle' && <Text style={caption}>{t('web.access.shortCodeHelp')}</Text>}
      {code.phase !== 'issuing' && <Button testID="invitation-new-code" variant="glass" icon="i-plus" onPress={() => {if (current()) void issuer.issue(true, run);}}>{t('web.access.newShortCode')}</Button>}
      {code.phase === 'failed' && <StateView variant="inline" {...runtime.error(code.error, () => {void issuer.retry(run);}, 'server-console')}/>}
      {failure !== undefined && <StateView variant="inline" {...runtime.error(failure, () => {setFailure(undefined);}, 'server-console')}/>}
    </View>
    {picture && <View pointerEvents="none" style={{position: 'absolute', left: -10000, top: 0}}><InvitationPicture ref={svg} picture={picture}/></View>}
  </BottomSheet>;
}

/** The card drawn once as a picture: the same facts, never notes or the short code. */
const InvitationPicture = React.forwardRef<SvgHandle, {picture: Picture}>(function InvitationPicture({picture}, ref) {
  const rows = qrMatrix(picture.link, 'M');
  const size = rows.length, cell = Math.max(1, Math.floor(260 / (size + 8))), tile = cell * (size + 8), left = (512 - tile) / 2, top = 218;
  let path = '';
  rows.forEach((row, y) => row.forEach((on, x) => {if (on) path += `M${left + (x + 4) * cell} ${top + (y + 4) * cell}h${cell}v${cell}h-${cell}z`;}));
  const lines: string[] = [];
  for (let rest = picture.link; rest && lines.length < 7; rest = rest.slice(58)) lines.push(rest.slice(0, 58));
  return <Svg ref={ref as never} width={512} height={680} viewBox="0 0 512 680">
    <Defs>
      <RadialGradient id="blue" cx="512" cy="0" r="520" gradientUnits="userSpaceOnUse"><Stop offset="0" stopColor="#2f86bb" stopOpacity={0.5}/><Stop offset="1" stopColor="#2f86bb" stopOpacity={0}/></RadialGradient>
      <RadialGradient id="green" cx="0" cy="680" r="460" gradientUnits="userSpaceOnUse"><Stop offset="0" stopColor="#1e5b52" stopOpacity={0.5}/><Stop offset="1" stopColor="#1e5b52" stopOpacity={0}/></RadialGradient>
    </Defs>
    <Rect width={512} height={680} fill="#0b1822"/><Rect width={512} height={680} fill="url(#blue)"/><Rect width={512} height={680} fill="url(#green)"/>
    <SvgText x={26} y={42} fill="#edf5fa" fontSize={20} fontWeight="800">{picture.brand}</SvgText>
    <SvgText x={26} y={68} fill="#bdccd6" fontSize={12} fontWeight="600">{picture.expiry}</SvgText>
    <SvgText x={26} y={110} fill="#5cb3e2" fontSize={15} fontWeight="600">{picture.why}</SvgText>
    <SvgText x={26} y={152} fill="#edf5fa" fontSize={36} fontWeight="800">{picture.serverName}</SvgText>
    <SvgText x={26} y={184} fill="#bdccd6" fontSize={14} fontWeight="600">{picture.access}</SvgText>
    <Rect x={left} y={top} width={tile} height={tile} fill="#ffffff"/><Path d={path} fill="#000000"/>
    {lines.map((line, index) => <SvgText key={index} x={26} y={518 + index * 18} fill="#edf5fa" fontSize={12} fontFamily="monospace">{line}</SvgText>)}
  </Svg>;
});
