import React from 'react';
import {generatedCoverTint} from '@portico/design';
import {GeneratedCover} from '../../kit/artwork';

/** mock.html "No artwork": a title without a poster draws its generated cover (ink, two tinted
 * glows, the oversized initial and the title), as on web, never a blank card. */
export function generatedPoster(title: string, width: number) {
  return <GeneratedCover title={title} glyph={Array.from(title.trim())[0] ?? ''} tint={generatedCoverTint(title)} width={width} height={Math.round(width * 3 / 2)}/>;
}
