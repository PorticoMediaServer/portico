import React, {type ReactNode} from 'react';
import {Text, View, type ViewStyle} from 'react-native';
import {PersonCircle, WholeCover, type WholeCoverSource} from '../artwork';
import type {AmbientArtSource, ArtworkSource} from '../artwork';
import {Focusable} from '../controls/Focusable';
import {browseType} from '../browse/presentation';
import {useTheme} from '../theme';

/** stack: a phone, the cover over the centred copy (phone-read 3/4, collections 41a, phone-app r5);
 *  row / row-wide: a tablet, the cover beside the copy (tablet-read 2c / 2 and 2b). The layout is the
 *  page's choice, so another platform can add its own here without a second hero. */
export type CoverHeroLayout = 'stack' | 'row' | 'row-wide';
/** portrait: a book (2:3); square: an audiobook or its series; fan: a reading series' first volumes;
 *  person: an author, a circle with their initials over their books' covers. */
export type CoverHeroShape = 'portrait' | 'square' | 'fan' | 'person';
/** One cover: its picture, and what is drawn when there is none (the generated cover). */
export type CoverHeroCover = WholeCoverSource;
/** A line of the copy that may open another page: the series above the title, the author below it. */
export type CoverHeroLink = Readonly<{label: string; detail?: string; onPress?: () => void; focusId?: string}>;
export type CoverHeroProps = Readonly<{
  layout: CoverHeroLayout; shape: CoverHeroShape; title: string;
  /** portrait and square draw the first; fan draws up to three. */
  covers?: readonly CoverHeroCover[];
  person?: Readonly<{covers: readonly ArtworkSource[]; initials: string}>;
  /** A square cover sits in a glow taken from its own art (collections 41a/43). */
  glow?: AmbientArtSource | null;
  eyebrow?: CoverHeroLink;
  /** `initials` draws the name's initials in a small circle before it, as an author is drawn (phone-read 3). */
  byline?: CoverHeroLink & Readonly<{initials?: string}>;
  /** The first fact leads (the reader, the word count); the rest are quieter. */
  facts?: readonly string[];
  /** Small marks under the facts (a comic's reading direction). */
  marks?: ReactNode;
  actions?: ReactNode;
  under?: ReactNode;
  /** The height of the cover and copy, for a page whose bar pins once they scroll away. */
  onHeight?: (height: number) => void;
}>;

/** One table of sizes for every cover page (the pages used to carry their own literals). */
const SIZES = {
  stack: {cover: {portrait: [168, 252], square: [232, 232], fan: [280, 214], person: [150, 150]}, title: {portrait: 40, square: 30, fan: 34, person: 30},
    eyebrow: 13.5, byline: 15, chip: 22, facts: 13, copyGap: 20, actionsGap: 18},
  row: {cover: {portrait: [176, 264], square: [232, 232], fan: [280, 214], person: [176, 176]}, title: {portrait: 66, square: 66, fan: 66, person: 66},
    eyebrow: 14.5, byline: 14, chip: 28, facts: 14, copyGap: 28, actionsGap: 18},
  'row-wide': {cover: {portrait: [206, 309], square: [260, 260], fan: [280, 214], person: [206, 206]}, title: {portrait: 84, square: 84, fan: 84, person: 84},
    eyebrow: 14.5, byline: 14.5, chip: 28, facts: 14.5, copyGap: 36, actionsGap: 18},
} as const;
/** One radius, shadow and glow per shape. */
const SHAPES = {
  portrait: {radius: 7, shadow: {shadowOpacity: .55, shadowRadius: 30, shadowOffset: {width: 0, height: 18}, elevation: 12}},
  square: {radius: 12, shadow: {shadowOpacity: .85, shadowRadius: 28, shadowOffset: {width: 0, height: 24}, elevation: 12}},
  fan: {radius: 7, shadow: {shadowOpacity: .6, shadowRadius: 24, shadowOffset: {width: 0, height: 14}, elevation: 6}},
} as const;
/** phone-read 4: a 128 x 192 volume, fanned by how many there are. */
const FAN_COVER = [128, 192] as const;
const fanPlace = (count: number, index: number) => ({
  left: count === 1 ? 76 : count === 2 ? 36 + index * 80 : [4, 72, 146][index]!,
  top: count === 3 ? [18, 6, 14][index]! : 12,
  rotate: count === 1 ? '0deg' : count === 2 ? (index ? '5deg' : '-5deg') : ['-9deg', '-2deg', '7deg'][index]!,
});

/** The page's cover beside or over its copy, one piece for the book, audiobook, series and author
 *  pages. A cover is drawn whole in its own shape inside its slot, never cropped (owner's rule). */
export function CoverHero({layout, shape, title, covers = [], person, glow, eyebrow, byline, facts = [], marks, actions, under, onHeight}: CoverHeroProps) {
  const theme = useTheme(), size = SIZES[layout], stack = layout === 'stack';
  const [width, height] = size.cover[shape];
  const align = stack ? 'center' as const : 'flex-start' as const, textAlign = stack ? 'center' as const : 'left' as const;
  const art = shape === 'person' && person
    ? <PersonCircle covers={person.covers} initials={person.initials} alt={title} size={width} visible/>
    : shape === 'fan' ? <View style={{width, height}}>{covers.slice(0, 3).map((cover, index, shown) => {
      const place = fanPlace(shown.length, index);
      return <View key={cover.key} style={{position: 'absolute', left: place.left, top: place.top, transform: [{rotate: place.rotate}]}}>
        <WholeCover cover={cover} width={FAN_COVER[0]} height={FAN_COVER[1]} look={SHAPES.fan} elevation={index + 4}/>
      </View>;
    })}</View>
      : covers[0] ? <WholeCover cover={covers[0]} width={width} height={height} look={shape === 'square' ? SHAPES.square : SHAPES.portrait} priority
        glow={shape === 'square' ? glow : undefined}/> : null;
  const titleSize = size.title[shape];
  const copy = <>
    {eyebrow ? <Line link={eyebrow} align={align} style={{marginTop: stack ? size.copyGap : 0}}>
      <Text numberOfLines={2} style={[browseType(theme, size.eyebrow, 600), {color: theme.colors.accentStrong, textAlign}]}>{eyebrow.label}</Text>
      {eyebrow.detail ? <Text style={[browseType(theme, size.eyebrow, 600), {color: theme.colors.textSecondary}]}>{eyebrow.detail}</Text> : null}
    </Line> : null}
    {/* A long title shrinks to fit its three lines rather than being cut (cut text is a fault). */}
    <Text accessibilityRole="header" numberOfLines={3} adjustsFontSizeToFit minimumFontScale={.6} style={[browseType(theme, titleSize, 800, stack ? -.03 : -.045, Math.round(titleSize * 1.06)),
      {color: theme.colors.text, textAlign, marginTop: eyebrow ? 6 : stack ? size.copyGap : 0}]}>{title}</Text>
    {byline ? <Line link={byline} align={align} wrap={false} style={{marginTop: 8}}>
      {byline.initials ? <View style={{width: size.chip, height: size.chip, borderRadius: size.chip / 2, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.raisedHigh}}>
        <Text style={[browseType(theme, size.chip * .43, 800), {color: theme.colors.text}]}>{byline.initials}</Text>
      </View> : null}
      <Text numberOfLines={2} style={[browseType(theme, size.byline, 700), {color: byline.onPress ? theme.colors.accentStrong : theme.colors.text, textAlign, flexShrink: 1}]}>{byline.label}</Text>
    </Line> : null}
    {facts.length ? <View style={{flexDirection: 'row', flexWrap: 'wrap', justifyContent: align, columnGap: 12, rowGap: 6, marginTop: 8}}>
      {facts.map((fact, index) => <Text key={`${fact}-${index}`} style={[browseType(theme, size.facts, stack ? (index === 0 ? 700 : 600) : 400),
        {color: index === 0 ? theme.colors.text : theme.colors.textSecondary, textAlign}]}>{fact}</Text>)}
    </View> : null}
    {marks ? <View style={{flexDirection: 'row', justifyContent: align, marginTop: 10}}>{marks}</View> : null}
  </>;
  const tail = <>{actions ? <View style={{marginTop: size.actionsGap, alignSelf: 'stretch'}}>{actions}</View> : null}{under}</>;
  const measure = onHeight ? (event: {nativeEvent: {layout: {height: number}}}) => {const next = Math.round(event.nativeEvent.layout.height); if (next > 0) onHeight(next);} : undefined;
  if (stack) return <View style={{width: '100%', alignItems: 'center'}}>
    <View style={{width: '100%', alignItems: 'center'}} onLayout={measure}>{art}{copy}</View>
    {tail}
  </View>;
  return <View style={{flexDirection: 'row', alignItems: 'flex-end', gap: size.copyGap}} onLayout={measure}>
    {art}
    <View style={{flex: 1, minWidth: 0}}>{copy}{tail}</View>
  </View>;
}

/** A line of the copy, pressable when it opens another page. */
function Line({link, align, wrap = true, style, children}: {link: CoverHeroLink; align: 'center' | 'flex-start'; wrap?: boolean; style?: ViewStyle; children: ReactNode}) {
  // The byline keeps its initials beside the name (the name wraps), the eyebrow may wrap its detail.
  const row = <View style={{flexDirection: 'row', flexWrap: wrap ? 'wrap' : 'nowrap', alignItems: 'center', justifyContent: align, columnGap: 8, rowGap: 4}}>{children}</View>;
  return link.onPress
    ? <Focusable focusId={link.focusId} accessibilityRole="link" accessibilityLabel={link.label} onPress={link.onPress} style={[{paddingVertical: 3, maxWidth: '100%', alignSelf: align}, style]}>{row}</Focusable>
    : <View style={[{maxWidth: '100%', alignSelf: align}, style]}>{row}</View>;
}
