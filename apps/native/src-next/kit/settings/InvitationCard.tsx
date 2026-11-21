import React from 'react';
import {Image, Text, View} from 'react-native';
import QRCode from 'react-native-qrcode-svg';
import Svg, {Defs, Path, RadialGradient, Rect, Stop, Text as SvgText} from 'react-native-svg';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {useFrameMetrics} from '../frames/metrics';
import {qrMatrix} from '../qr-matrix';
import {useTheme} from '../theme';

export type InvitationPictureData = Readonly<{brand: string; link: string; expiry: string; serverName: string; access: string; why: string}>;
export type InvitationPictureHandle = {toDataURL: (callback: (base64: string) => void) => void};

export function InvitationCard({expiry, serverName, access, link, code, signIn, t}: {
  expiry: string; serverName: string; access: string; link?: string; code?: string; signIn?: 'local' | 'portico'; t: typeof defaultI18n.t;
}) {
  const theme = useTheme(), frame = useFrameMetrics();
  const caption = [theme.type.caption, {color: theme.colors.textSecondary}];
  const qrSize = frame.rowTwoLine * 3;
  const logoHeight = theme.space.md;
  return <View testID="invitation-card" style={{borderRadius: frame.cardRadius, padding: frame.cardPadding, gap: theme.space.controlGap,
    backgroundColor: theme.colors.ink, borderWidth: 1, borderColor: theme.colors.line}}>
    <View style={{flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: theme.space.xs}}>
      <Image source={require('../../../assets/wordmark.png')} accessibilityLabel={t('accounts.brand')} resizeMode="contain"
        style={{width: logoHeight * 512 / 117, height: logoHeight}}/>
      <Text style={caption}>{expiry}</Text>
    </View>
    <Text style={[theme.type.body, {color: theme.colors.accent}]}>{t('accounts.invitation.to')}</Text>
    <Text accessibilityRole="header" style={[theme.type.title, {color: theme.colors.text}]}>{serverName}</Text>
    <Text style={caption}>{access}</Text>
    <View accessible accessibilityRole="image" accessibilityLabel={t('qr.label')}
      style={{alignSelf: 'center', padding: theme.space.sm, borderRadius: theme.baseRadii.control, backgroundColor: '#ffffff'}}>
      {link ? <QRCode value={link} size={qrSize} ecl="M" backgroundColor="#ffffff" color="#000000"/> : <View style={{width: qrSize, height: qrSize}}/>}
    </View>
    <Text style={[caption, {textAlign: 'center'}]}>{t('accounts.invitation.scanHelp')}</Text>
    {link && <Text testID="invitation-link" selectable style={[theme.type.caption, {color: theme.colors.text, padding: theme.space.sm,
      borderRadius: theme.baseRadii.control, backgroundColor: theme.colors.raised}]}>{link}</Text>}
    {code && <View style={{alignItems: 'center', gap: theme.space.xs}}>
      <Text style={caption}>{t('accounts.invitation.orEnterCode')}</Text>
      <Text testID="invitation-short-code" selectable style={[theme.type.title, {color: theme.colors.text}]}>{code}</Text>
    </View>}
    <Text style={[caption, {textAlign: 'center'}]}>{t('accounts.invitation.shareJoinHelp', {method: signIn ?? 'other'})}</Text>
  </View>;
}

export function InvitationPictureCapture({picture, pictureRef}: {picture: InvitationPictureData; pictureRef: React.Ref<InvitationPictureHandle>}) {
  return <View pointerEvents="none" accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{position: 'absolute', left: -10000, top: 0}}>
    <InvitationPicture ref={pictureRef} picture={picture}/>
  </View>;
}

function pictureLines(value: string, max: number) {
  const lines: string[] = [];
  let line = '';
  for (const word of value.trim().split(/\s+/)) {
    const next = line ? line + ' ' + word : word;
    if (Array.from(next).length <= max) {
      line = next;
      continue;
    }
    if (line) lines.push(line);
    const letters = Array.from(word);
    while (letters.length > max) lines.push(letters.splice(0, max).join(''));
    line = letters.join('');
  }
  if (line) lines.push(line);
  return lines;
}

/** The card drawn once as a picture: the same facts, never notes or the short code. */
export const InvitationPicture = React.forwardRef<InvitationPictureHandle, {picture: InvitationPictureData}>(function InvitationPicture({picture}, ref) {
  const {type} = useTheme();
  const width = 512, margin = 26, contentWidth = width - 2 * margin;
  const rows = qrMatrix(picture.link, 'M');
  const size = rows.length, cell = Math.max(1, Math.floor(260 / (size + 8))), tile = cell * (size + 8), left = (width - tile) / 2;
  let baseline = margin;
  const draw = (value: string, role: typeof type.caption, fill: string) => {
    const {fontFamily, fontSize, lineHeight} = role;
    // Leave one em per glyph and spare room for wide fallback glyphs in the exported image.
    const max = Math.max(1, Math.floor(contentWidth / (1.12 * (fontSize ?? 16))));
    const height = lineHeight ?? Math.ceil((fontSize ?? 16) * 1.4);
    return pictureLines(value, max).map((line, index) => {
      baseline += height;
      return <SvgText key={index} x={margin} y={baseline} fill={fill} {...{fontFamily, fontSize}}>{line}</SvgText>;
    });
  };
  const brand = draw(picture.brand, type.rowHeading, '#edf5fa');
  const expiry = draw(picture.expiry, type.caption, '#bdccd6');
  baseline += margin;
  const why = draw(picture.why, type.rowLabel, '#5cb3e2');
  const server = draw(picture.serverName, type.pageTitle, '#edf5fa');
  const access = draw(picture.access, type.rowHelp, '#bdccd6');
  const top = baseline + margin;
  baseline = top + tile + margin;
  const link = draw(picture.link, type.caption, '#edf5fa');
  const height = baseline + margin;
  let path = '';
  rows.forEach((row, y) => row.forEach((on, x) => {if (on) path += `M${left + (x + 4) * cell} ${top + (y + 4) * cell}h${cell}v${cell}h-${cell}z`;}));
  return <Svg ref={ref as never} width={width} height={height} viewBox={`0 0 ${width} ${height}`}>
    <Defs>
      <RadialGradient id="blue" cx={width} cy="0" r="520" gradientUnits="userSpaceOnUse"><Stop offset="0" stopColor="#2f86bb" stopOpacity={0.5}/><Stop offset="1" stopColor="#2f86bb" stopOpacity={0}/></RadialGradient>
      <RadialGradient id="green" cx="0" cy={height} r="460" gradientUnits="userSpaceOnUse"><Stop offset="0" stopColor="#1e5b52" stopOpacity={0.5}/><Stop offset="1" stopColor="#1e5b52" stopOpacity={0}/></RadialGradient>
    </Defs>
    <Rect width={width} height={height} fill="#0b1822"/><Rect width={width} height={height} fill="url(#blue)"/><Rect width={width} height={height} fill="url(#green)"/>
    {brand}{expiry}{why}{server}{access}
    <Rect x={left} y={top} width={tile} height={tile} fill="#ffffff"/><Path d={path} fill="#000000"/>
    {link}
  </Svg>;
});
