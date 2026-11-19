import React from 'react';
import {ScrollView, Text, View, type TextStyle} from 'react-native';
import {formScale, type ScaleForm, type TypeRole} from '@portico/design';
import {formatMessage} from '../../../../../../packages/i18n/src';
import {layoutNativeFixtureMessages} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/layout-native-fixtures';
import {fontFamilies, useTheme} from '../../../kit/theme';
import type {Gallery, GalleryPlatform} from '../../gallery-registry';

/** G2 (native rebuild): every type role of the running form, each set in one line and in two, on a tint that is
 * exactly its line box. A glyph cut at the tint's top or bottom edge is a line box set too tight on this system; the
 * measured correction goes in `nativeType` (kit/theme.ts). The roles are split into pages that fit the form's window,
 * so every case is one screenshot with nothing to scroll (a TV page cannot scroll without focus). */
type FixtureName = keyof typeof layoutNativeFixtureMessages extends `next.layout.native.fixture.${infer Name}` ? Name : never;
const fixture = (name: FixtureName) => formatMessage(layoutNativeFixtureMessages[`next.layout.native.fixture.${name}`]);
// A tint no role's own colour uses, so the line box reads against both the text and the page.
const tint = 'rgba(255, 64, 160, 0.42)';

/** The window each platform's cases are paged for (the TV canvas is 1920 x 1080 at scale 1). */
const canvases: Readonly<Record<'phone' | 'tablet' | 'tv', {form: ScaleForm; width: number; height: number; gutter: number}>> = {
  phone: {form: 'phone', width: 360, height: 700, gutter: 20},
  tablet: {form: 'medium', width: 760, height: 760, gutter: 28},
  tv: {form: 'tv', width: 1920, height: 960, gutter: 200},
};
// Manrope's natural line is about 1.37 of its size; a wide bold glyph about 0.7 of it.
const lineBox = (size: number, line: number | 'normal') => line === 'normal' ? size * 1.37 : Math.max(line, size * 1.2);
const short = (size: number, width: number) => size * 0.7 * 13 * 2 > width;

function paged(form: ScaleForm, width: number, height: number, gutter: number): TypeRole[][] {
  const roles = (Object.keys(formScale.type[form]) as TypeRole[]).sort((a, b) => formScale.type[form][a].size - formScale.type[form][b].size);
  const room = width - 2 * gutter, label = form === 'tv' ? 40 : 24, gap = form === 'tv' ? 32 : 14;
  const pages: TypeRole[][] = [];
  let page: TypeRole[] = [], used = 0, rowWidth = 0, rowHeight = 0;
  for (const role of roles) {
    const {size, lineHeight} = formScale.type[form][role], line = lineBox(size, lineHeight);
    const w = Math.min(room, Math.max(label * 8, (short(size, room) ? 4 : 13) * size * 0.7 * 2 + gap)), h = label + 2 * line + gap;
    if (rowWidth + w > room) {used += rowHeight; rowWidth = 0; rowHeight = 0;}
    if (page.length && used + Math.max(rowHeight, h) > height) {pages.push(page); page = []; used = 0; rowWidth = 0; rowHeight = 0;}
    page.push(role); rowWidth += w + gap; rowHeight = Math.max(rowHeight, h);
  }
  if (page.length) pages.push(page);
  return pages;
}

/** One specimen: its label, then the text in one line and in two (the last line with descenders), each on the tint. */
function Specimen({label, style, tiny}: {label: string; style: TextStyle; tiny: boolean}) {
  const theme = useTheme();
  const box: TextStyle = {backgroundColor: tint, alignSelf: 'flex-start'};
  return <View style={{marginRight: theme.isTV ? 32 : 14, marginBottom: theme.isTV ? 32 : 14}}>
    <Text style={[theme.type.micro, {color: theme.colors.textSecondary}]}>{label}</Text>
    <View style={{flexDirection: 'row', alignItems: 'flex-start', gap: theme.isTV ? 24 : 10}}>
      <Text style={[{color: theme.colors.text}, style, box]}>{fixture(tiny ? 'typeSpecimenShort' : 'typeSpecimen')}</Text>
      <Text style={[{color: theme.colors.text}, style, box]}>{fixture(tiny ? 'typeSpecimenShortTwo' : 'typeSpecimenTwo')}</Text>
    </View>
  </View>;
}

function TypePage({roles}: {roles: readonly TypeRole[]}) {
  const theme = useTheme();
  return <ScrollView contentContainerStyle={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'flex-start', paddingVertical: 20}}>
    {roles.map(role => {
      const style = theme.type[role] as TextStyle, design = formScale.type[theme.form][role];
      return <Specimen key={role} label={`${role}  ${design.size}/${style.lineHeight ?? 'normal'}  ${style.fontFamily}`} style={style} tiny={short(design.size, theme.width - 2 * theme.gutter)} />;
    })}
  </ScrollView>;
}

/** The ladder: one large size set at line boxes from 1.2 to 1.38 of it, regular and extra bold, and once with the
 * font's own line ('normal'). The smallest box with no glyph cut is this system's floor for the font. */
const ladder = [1.2, 1.25, 1.28, 1.3, 1.32, 1.34, 1.36, 1.38] as const;
function LadderPage({large}: {large: boolean}) {
  const theme = useTheme(), size = theme.isTV ? large ? 88 : 32 : large ? 40 : 15;
  return <ScrollView contentContainerStyle={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'flex-start', paddingVertical: 20}}>
    {([800, 400] as const).flatMap(weight => [...ladder, 'normal' as const].map(ratio =>
      <Specimen key={`${weight}-${ratio}`} label={`${weight}  ${ratio}`} tiny
        style={{fontFamily: fontFamilies[weight], fontSize: size, ...(ratio === 'normal' ? {} : {lineHeight: Math.round(size * ratio)})}} />))}
  </ScrollView>;
}

const galleries: readonly Gallery[] = (Object.entries(canvases) as [GalleryPlatform & keyof typeof canvases, typeof canvases.tv][]).flatMap(([platform, {form, width, height, gutter}]) =>
  paged(form, width, height, gutter).map((roles, index): Gallery => ({
    id: `type-${platform}-${index + 1}`, title: 'next.layout.native.typeTitle', platforms: [platform], render: () => <TypePage roles={roles} />,
  }))).concat({id: 'type-ladder', title: 'next.layout.native.typeTitle', render: () => <LadderPage large />},
    {id: 'type-ladder-small', title: 'next.layout.native.typeTitle', render: () => <LadderPage large={false} />});
export default galleries;
