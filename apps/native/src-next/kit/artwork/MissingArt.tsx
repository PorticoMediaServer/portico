import React, {type ReactNode} from 'react';
import {generatedCoverTint} from '@portico/design';
import {GeneratedArt, GeneratedCover} from './Generated';

export type MissingArtShape = 'poster' | 'square' | 'wide' | 'still' | 'book' | 'periodical' | 'newspaper';

/** mock.html "No artwork": a picture that was never served is drawn as the generated cover of its title, never an empty
 * frame. Portrait and square art are the typographic cover; 16:9 art (wide cards, episode stills) is the tinted wash with
 * the title's initial, as the "No artwork" hero is. The one place every card and hero takes its missing-art look from.
 * A nameless slot (a waiting card) draws nothing. */
export function missingArt({title, shape, width, height, author}: {title: string; shape: MissingArtShape; width: number; height: number; author?: string}): ReactNode {
  if (!title.trim()) return undefined;
  const glyph = Array.from(title.trim())[0] ?? '', tint = generatedCoverTint(title);
  return shape === 'wide' || shape === 'still'
    ? <GeneratedArt glyph={glyph} palette={tint} width={width} height={height}/>
    : <GeneratedCover title={title} author={author} glyph={glyph} tint={tint} width={width} height={height}/>;
}
