import {compareScreens} from './compare.mjs';

/** Fault injection changes one canonical observed fact, never the frame DOM/CSS. */
export function calibrateFrame(frame) {
  const self = compareScreens(frame, structuredClone(frame), {}, {referenceSelf: true});
  const title = frame.elements.find(node => node.frame === 'title') ?? frame.elements.find(node => node.ownText && parseFloat(node.style?.fontSize));
  const caption = frame.elements.find(node => node.frame === 'lede' || node.frame === 'caption') ?? title;
  if (!title || !caption) return {passed: false, reason: 'Calibration needs measured text parts'};
  const changedFont = structuredClone(frame), changedOpacity = structuredClone(frame);
  changedFont.elements.find(node => node.id === title.id).style.fontSize = `${parseFloat(title.style.fontSize) + 1}px`;
  const opacityNode = changedOpacity.elements.find(node => node.id === caption.id);
  opacityNode.effectiveOpacity = Number(caption.effectiveOpacity ?? caption.style.opacity ?? 1) > .12 ? Number(caption.effectiveOpacity ?? caption.style.opacity ?? 1) - .12 : .5;
  const font = compareScreens(frame, changedFont, {}, {referenceSelf: true}), opacity = compareScreens(frame, changedOpacity, {}, {referenceSelf: true});
  const counts = {self: self.differences.length, oneFont: font.differences.length, oneOpacity: opacity.differences.length};
  return {passed: counts.self === 0 && counts.oneFont === 1 && counts.oneOpacity === 1 && font.differences[0]?.property === 'fontSize' && opacity.differences[0]?.property === 'effectiveOpacity',
    counts, fontProperty: font.differences[0]?.property, opacityProperty: opacity.differences[0]?.property};
}
