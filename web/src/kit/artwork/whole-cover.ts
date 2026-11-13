import type {CSSProperties} from 'react';

/** The style that draws a cover's own box (its picture, radius, shadow, badges and focus ring)
 * whole, in its own shape, inside its slot: `size` wide and `size / slot` tall. The box is never
 * larger than the slot, and the margins keep the slot's size, so a grid or a row stays even. A
 * narrower cover is centred; a wider one sits at the slot's foot (`end`) or its middle. */
export function wholeCoverBox(aspect: number | undefined, size: string, {slot = 1, align = 'center'}: Readonly<{slot?: number; align?: 'center' | 'end'}> = {}): CSSProperties | undefined {
  if (!aspect || Math.abs(aspect - slot) < .01) return undefined;
  if (aspect < slot) {
    const share = aspect / slot;
    return {width: `calc(${size} * ${share})`, height: 'auto', aspectRatio: String(aspect), marginInline: `calc(${size} * ${(1 - share) / 2})`};
  }
  // Margins in percent are of the slot's width, as `size` is, so the spare height is exact.
  const spare = 1 / slot - 1 / aspect;
  return {width: size, height: 'auto', aspectRatio: String(aspect), ...(align === 'end' ? {marginTop: `calc(${size} * ${spare})`} : {marginBlock: `calc(${size} * ${spare / 2})`})};
}
