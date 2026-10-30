import React, {useSyncExternalStore} from 'react';
import {Text, View, type TextStyle, type ViewStyle} from '@amazon-devices/react-native-kepler';
import {subtitleDisplay} from './subtitles';
import type {CaptionBackground, CaptionColor, CaptionFont, CaptionPosition} from './subtitle-cues';

/**
 * The subtitle line, drawn over the picture by the app itself (ENGINE-CONTRACT.md §7).
 * This is the renderer and nothing else: it reads the shared `subtitleDisplay` and writes
 * nothing, so the engine, its lease and the surface stay where they are, in `playback.ts`.
 *
 * Where the look comes from: Portico's caption style arrives with the subtitle command
 * (`subtitle-cues.ts`), and the other two engines draw the same fields — tvOS in
 * `PorticoSubtitleSelection.applyCaptionAppearanceToLabel`, Android in
 * `PorticoMpvSupport` — so the band, the size, the colours and the three placements here
 * are theirs. `style.scale` is the only multiplier: `allowFontScaling` is off on the line
 * and on its copies, because this SDK cannot read the viewer's system caption size or the
 * system caption style, and the platform's default text scaling is not that setting. So
 * neither is applied and the app does not pretend otherwise.
 *
 * What the SDK cannot do, and what that costs:
 * - The platform's own caption renderer is not usable: `KeplerCaptionsView` takes only
 *   `show`, and the handle a player is given carries no style at all, so it could not
 *   draw this style even if the engine attached one.
 * - There is no text stroke. `outline` is four black copies of the line, one small step
 *   out in each of the four directions and painted behind it: an explicitly limited
 *   approximation, not parity with the 3px stroke the Apple and Android engines draw.
 * - The face is the platform's own for every parsed choice: see `faces`.
 */
export function CaptionLabel({width, height}: {width: number; height: number}): React.ReactElement | null {
  const {display: cue, style} = useSyncExternalStore(subtitleDisplay.subscribe, subtitleDisplay.current);
  if (!cue.text || !width || !height) return null;
  const fontSize = Math.max(12, Math.min(40, width * 0.035)) * style.scale;
  const label: TextStyle = {
    color: colors[style.color], textAlign: 'center', fontFamily: faces[style.font], fontSize,
    lineHeight: Math.round(fontSize * 1.25), fontWeight: '500', fontStyle: cue.italic ? 'italic' : 'normal', ...edgeOf(style.background, fontSize),
  };
  const step = style.background === 'outline' ? outlineStep(fontSize) : 0;
  return (
    <View pointerEvents="none" accessible style={[band, {left: width * 0.05, width: width * 0.9, maxHeight: height * 0.6, backgroundColor: boxOf(style.background)}, placementOf(cue.top, style.position, height)]}>
      {/* The line's own box, so the outline's copies line up with it exactly. */}
      <View>
        {step ? OUTLINE_DIRECTIONS.map(direction => (
          <Text key={`${direction.width}:${direction.height}`} accessible={false} aria-hidden allowFontScaling={false} style={[label, outlineCopy, {transform: [{translateX: direction.width * step}, {translateY: direction.height * step}]}]}>{cue.text}</Text>
        )) : null}
        <Text allowFontScaling={false} style={label}>{cue.text}</Text>
      </View>
    </View>
  );
}

/** The five colours the profile offers, as the Apple and Android engines draw them. */
const colors: Readonly<Record<CaptionColor, string>> = {white: '#ffffff', yellow: '#ffff00', cyan: '#00ffff', green: '#00ff00', pink: '#ff80bf'};

/**
 * The face each parsed choice is drawn in. All four are the platform's own face today.
 *
 * `default` and `rounded` are the platform's face by design — the Android engine keeps its
 * ordinary face for `rounded` rather than inventing a rounded one. `serif` and `mono` keep
 * their parsed intent in `CaptionStyle` but are also drawn in the platform's face, because
 * a generic family name has not been shown to resolve to an actual face on this platform.
 * Recording the gap here is deliberate: the setting does what it can and the difference is
 * stated, rather than an unproven family name or a bundled asset being passed off as the
 * viewer's choice. Adding a face is one entry in this table, once it is proven on device.
 */
const faces: Readonly<Record<CaptionFont, string | undefined>> = Object.freeze({default: undefined, rounded: undefined, serif: undefined, mono: undefined});

/** The band the line is drawn in, as the other engines draw it. */
const band: ViewStyle = {position: 'absolute', paddingVertical: 4};

/** The box behind the line: the soft black box, the solid one, or none of it. */
const boxOf = (background: CaptionBackground): string => background === 'opaque' ? '#000000' : background === 'translucent' ? 'rgba(0,0,0,0.7)' : 'transparent';

/** The edge a line with no box keeps so it stays readable over a bright picture. */
const edgeOf = (background: CaptionBackground, fontSize: number): TextStyle | undefined =>
  background === 'none' ? {textShadowColor: '#000000', textShadowOffset: {width: 0, height: 0}, textShadowRadius: Math.max(2, fontSize * 0.08)}
    : background === 'shadow' ? {textShadowColor: '#000000', textShadowOffset: {width: 0, height: 2}, textShadowRadius: Math.max(2, fontSize * 0.08)}
      : undefined;

/** One step of the outline, as a fraction of the line's size, never below a whole pixel. */
const outlineStep = (fontSize: number): number => Math.max(1, Math.round(fontSize * 0.045));

/** The four directions the outline's copies sit in. */
const OUTLINE_DIRECTIONS = [{width: 1, height: 0}, {width: -1, height: 0}, {width: 0, height: 1}, {width: 0, height: -1}] as const;

/**
 * An outline copy: the same line, in the same place and the same box, drawn in black and
 * then moved one step by `transform`, so the measured width and line box stay the
 * foreground's alone. Painted before the line, so the line covers all but the step that
 * shows; out of the accessibility tree and never a target, so the line is read and
 * pointed at once. Real glyphs at real offsets: no reliance on how a shadow is drawn.
 */
const outlineCopy: TextStyle = {position: 'absolute', left: 0, right: 0, top: 0, pointerEvents: 'none', color: '#000000'};

/** Where the line sits: the cue's own `top` wins, then the viewer's choice (0.20 / 0.08 as the other engines use). */
const placementOf = (top: boolean, position: CaptionPosition, height: number): ViewStyle =>
  top || position === 'top' ? {top: 8} : {bottom: Math.max(8, height * (position === 'raised' ? 0.20 : 0.08))};
