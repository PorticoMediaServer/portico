import {hasCompleteDeclaration, declaredFontFamily, prepareNativeDeclarations} from './native-declarations.mjs';
/** Shared facts with explicit observed or declared provenance; never a renderer. */
export const contractVersion = 1;
/** Curated scalar styles; scoring declarations require explicit complete provenance. */
export const diagnosticStyleProperties = Object.freeze([
  'fontFamily', 'fontSize', 'fontWeight', 'fontStyle', 'lineHeight', 'letterSpacing', 'textAlign', 'textTransform',
  'color', 'backgroundColor', 'opacity', 'borderRadius', 'borderTopLeftRadius', 'borderTopRightRadius', 'borderBottomLeftRadius', 'borderBottomRightRadius',
  'borderWidth', 'borderTopWidth', 'borderRightWidth', 'borderBottomWidth', 'borderLeftWidth', 'borderStyle', 'borderColor', 'borderTopColor', 'borderRightColor', 'borderBottomColor', 'borderLeftColor',
  'shadowColor', 'shadowOpacity', 'shadowRadius', 'shadowOffsetX', 'shadowOffsetY',
  'textDecorationLine', 'textShadowColor', 'textShadowRadius', 'textShadowOffsetX', 'textShadowOffsetY',
  'elevation', 'overflow', 'display', 'position', 'zIndex',
  'width', 'height', 'minWidth', 'minHeight', 'maxWidth', 'maxHeight', 'top', 'right', 'bottom', 'left',
  'padding', 'paddingHorizontal', 'paddingVertical', 'paddingTop', 'paddingRight', 'paddingBottom', 'paddingLeft',
  'margin', 'marginHorizontal', 'marginVertical', 'marginTop', 'marginRight', 'marginBottom', 'marginLeft',
  'gap', 'rowGap', 'columnGap', 'alignItems', 'alignSelf', 'justifyContent', 'flexDirection', 'flexGrow', 'flexShrink', 'flexBasis',
]);
const finite = value => typeof value === 'number' && Number.isFinite(value);
const rect = value => value && ['x', 'y', 'width', 'height'].every(key => finite(value[key])) && value.width >= 0 && value.height >= 0;
const only = (value, keys) => value && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).every(key => keys.includes(key));
const rawRect = value => only(value, ['x', 'y', 'width', 'height']) && rect(value);
const publicMeasurement = value => only(value, ['api', 'rect']) && ['bounding-client-rect', 'measure-in-window'].includes(value.api) && rawRect(value.rect);
export function usesLogicalCoordinates(screen) {
  const value = screen?.measurement;
  return value?.renderer === 'dom' && value.coordinateUnit === 'css-px' || value?.renderer === 'react-native' &&
    (value.platform === 'android' && value.coordinateUnit === 'dp' || ['ios', 'tvos'].includes(value.platform) && value.coordinateUnit === 'pt');
}

/** Native host sizes may be float32 while Dimensions supplies float64. Accept
 * only an exact representational projection, never a geometric epsilon. */
export function sameLogicalExtent(left, right, measurement) {
  if (!finite(left) || !finite(right)) return false;
  if (left === right) return true;
  return measurement?.renderer === 'react-native' && usesLogicalCoordinates({measurement}) &&
    (left === Math.fround(right) || right === Math.fround(left));
}

/** Both inputs must already be observed in the same attached native window and
 * logical unit. This is an origin translation only; no scaling or rounding. */
export function nativeWindowRectToRoot(windowRect, rootWindow) {
  if (!rawRect(windowRect) || !rawRect(rootWindow)) throw new Error('Observed native window rectangles required');
  return {x: windowRect.x - rootWindow.x, y: windowRect.y - rootWindow.y, width: windowRect.width, height: windowRect.height};
}

export function nativeWindowCoversViewport(rootWindow, viewport, measurement) {
  return measurement?.renderer === 'react-native' && usesLogicalCoordinates({measurement}) && rawRect(rootWindow) &&
    rootWindow.x === 0 && rootWindow.y === 0 && rootWindow.width > 0 && rootWindow.height > 0 &&
    sameLogicalExtent(rootWindow.width, viewport?.width, measurement) && sameLogicalExtent(rootWindow.height, viewport?.height, measurement);
}

const sameRect = (left, right) => rect(left) && rect(right) && ['x', 'y', 'width', 'height'].every(key => left[key] === right[key]);
const nativeCoordinates = value => only(value, ['basis', 'windowAPI', 'rootWindow', 'publicRoot', 'publicRootReadback']) &&
  value.basis === 'attached-native-window' && typeof value.windowAPI === 'string' && /^[A-Za-z][A-Za-z0-9_.+:-]{0,159}$/.test(value.windowAPI) && !/^(?:https?|file|data):/i.test(value.windowAPI) &&
  rawRect(value.rootWindow) && (value.publicRoot === undefined || publicMeasurement(value.publicRoot)) &&
  (value.publicRootReadback === undefined || publicMeasurement(value.publicRootReadback));

export function componentFitsViewport(screen) {
  const box = screen?.componentPresentation?.windowBounds, viewport = screen?.viewport;
  return rect(box) && box.x >= 0 && box.y >= 0 && finite(viewport?.width) && finite(viewport?.height) &&
    box.x + box.width <= viewport.width && box.y + box.height <= viewport.height;
}

export function validateMeasurement(screen, {reference = false} = {}) {
  screen = prepareNativeDeclarations(screen);
  const errors = [];
  const need = (condition, path) => {if (!condition) errors.push(path);};
  need(screen?.version === contractVersion, 'version');
  need(['screen', 'component'].includes(screen?.scope), 'scope');
  need(rect(screen?.root), 'root');
  const referenceFrame = screen?.measurement?.sourceRole === 'reference-frame';
  const component = screen?.scope === 'component', referenceComponent = screen?.measurement?.sourceRole === 'reference-component';
  const physicalWindow = screen?.measurement?.geometrySource === 'native-window';
  const coordinates = screen?.diagnostics?.nativeCoordinates;
  need(screen?.measurement?.geometrySource === undefined || physicalWindow, 'measurement.geometrySource');
  if (physicalWindow) {
    need(!reference && !component && screen?.measurement?.renderer === 'react-native' && usesLogicalCoordinates(screen), 'measurement.geometrySource');
    need(nativeCoordinates(coordinates), 'diagnostics.nativeCoordinates');
    need(nativeWindowCoversViewport(coordinates?.rootWindow, screen?.viewport, screen?.measurement), 'nativeWindow.fullViewport');
    need(screen?.root?.width === coordinates?.rootWindow?.width && screen?.root?.height === coordinates?.rootWindow?.height, 'nativeWindow.root');
  } else need(coordinates === undefined, 'diagnostics.nativeCoordinates.geometrySource');
  need(screen?.measurement?.sourceRole === undefined || referenceFrame || referenceComponent, 'measurement.sourceRole');
  need(screen?.fullViewport === true || reference && referenceFrame && screen?.fullViewport === false || component && screen?.fullViewport === false, 'fullViewport');
  if (referenceComponent) need(reference && component && screen.viewport?.deviceScaleFactor === 1, 'measurement.sourceRole');
  if (referenceFrame) {
    need(reference, 'measurement.sourceRole');
    const origin = screen.referencePresentation;
    need(rect(origin?.windowBounds) && origin.windowBounds.width === screen.root?.width && origin.windowBounds.height === screen.root?.height && origin.windowViewport?.deviceScaleFactor === 1 && screen.viewport?.deviceScaleFactor === 1 && typeof origin.fullWindowViewport === 'boolean', 'referencePresentation');
  }
  need(finite(screen?.viewport?.width) && screen.viewport.width > 0 && finite(screen?.viewport?.height) && screen.viewport.height > 0 && finite(screen?.viewport?.deviceScaleFactor) && screen.viewport.deviceScaleFactor > 0, 'viewport');
  if (component) {
    const presentation = screen.componentPresentation, bounds = presentation?.windowBounds;
    need(screen.measurement?.renderer === 'dom' && screen.measurement.coordinateUnit === 'css-px' && !referenceFrame, 'component.coordinateMapping');
    need(rect(bounds) && rect(presentation?.sourceRoot) && bounds.width === screen.root?.width && bounds.height === screen.root?.height, 'component.presentation');
    need(screen.root?.x === 0 && screen.root?.y === 0, 'component.origin');
    if (!referenceComponent) need(componentFitsViewport(screen), 'component.visibility');
  } else {
    need(screen?.componentPresentation === undefined, 'component.scope');
    need(screen?.root?.x === 0 && screen?.root?.y === 0 && sameLogicalExtent(screen?.root?.width, screen?.viewport?.width, screen?.measurement) && sameLogicalExtent(screen?.root?.height, screen?.viewport?.height, screen?.measurement), 'root.viewport');
  }
  if (screen?.measurement) {
    need(['dom', 'react-native'].includes(screen.measurement.renderer), 'measurement.renderer');
    need(['css-px', 'dp', 'pt'].includes(screen.measurement.coordinateUnit), 'measurement.coordinateUnit');
    need(screen.measurement.renderer !== 'react-native' || ['android', 'ios', 'tvos'].includes(screen.measurement.platform), 'measurement.platform');
    need(usesLogicalCoordinates(screen), 'measurement.coordinateMapping');
  }
  need(Array.isArray(screen?.elements), 'elements');
  need(Array.isArray(screen?.strings), 'strings');
  need(Array.isArray(screen?.anchorIssues), 'anchorIssues');
  need(Array.isArray(screen?.repeatedLists), 'repeatedLists');
  if (screen?.diagnostics !== undefined) need(only(screen.diagnostics, ['fontScale', 'nativeCoordinates']) && (screen.diagnostics.fontScale === undefined || finite(screen.diagnostics.fontScale) && screen.diagnostics.fontScale > 0), 'diagnostics.fontScale');
  if (screen?.unavailable !== undefined) need(Array.isArray(screen.unavailable) && screen.unavailable.every(fact => typeof fact?.property === 'string' && typeof fact.reason === 'string' && fact.reason.length > 0 && (fact.element === undefined || typeof fact.element === 'string')), 'unavailable');
  const ids = new Set();
  for (const [index, node] of (Array.isArray(screen?.elements) ? screen.elements : []).entries()) {
    const path = `elements.${index}`;
    const declared = property => Array.isArray(screen.unavailable) && screen.unavailable.some(fact => fact.element === node.id && fact.property === property && typeof fact.reason === 'string' && fact.reason.length > 0);
    need(typeof node.id === 'string' && !ids.has(node.id), path + '.id'); ids.add(node.id);
    if (node.diagnostics !== undefined) {
      need(only(node.diagnostics, physicalWindow ? ['flattenedStyle', 'declaredStyle', 'nativeWindowRect', 'nativeWindowTextRects', 'publicMeasurement'] : ['flattenedStyle', 'declaredStyle']), path + '.diagnostics');
      if (node.diagnostics.declaredStyle !== undefined) need(!reference && screen.measurement?.renderer === 'react-native' &&
        only(node.diagnostics.declaredStyle, ['source', 'complete']) && hasCompleteDeclaration(node), path + '.diagnostics.declaredStyle');
      const style = node.diagnostics?.flattenedStyle;
      if (style !== undefined) need(style && typeof style === 'object' && !Array.isArray(style) && Object.entries(style).every(([key, value]) => diagnosticStyleProperties.includes(key) && (finite(value) || typeof value === 'string' && !/url\s*\(|(?:https?|file|data):/i.test(value))), path + '.diagnostics.flattenedStyle');
    }
    // Pseudo-elements have no separate box or semantic focus target.
    if (node.tag?.startsWith('::')) continue;
    need(rect(node.box) || physicalWindow && node.box == null && declared('box'), path + '.box');
    if (physicalWindow) {
      const raw = node.diagnostics?.nativeWindowRect;
      if (node.box != null) need(rawRect(raw) && rawRect(coordinates?.rootWindow) && sameRect(node.box, nativeWindowRectToRoot(raw, coordinates.rootWindow)), path + '.nativeWindow.box');
      else need(raw === undefined, path + '.nativeWindow.box');
      if (node.diagnostics?.publicMeasurement !== undefined) need(publicMeasurement(node.diagnostics.publicMeasurement), path + '.diagnostics.publicMeasurement');
      const runs = node.diagnostics?.nativeWindowTextRects;
      if (node.textRects?.length || runs !== undefined) need(!node.input && Array.isArray(runs) && runs.length > 0 && runs.every(rawRect) && rawRect(coordinates?.rootWindow) &&
        Array.isArray(node.textRects) && runs.length === node.textRects.length && runs.every((run, index) => sameRect(node.textRects[index], nativeWindowRectToRoot(run, coordinates.rootWindow))), path + '.nativeWindow.textRects');
    }
    need((typeof node.ownText === 'string' || node.ownText == null && declared('ownText')) && (typeof node.normalizedText === 'string' || node.normalizedText == null && declared('normalizedText')), path + '.text');
    need(typeof node.focused === 'boolean' || node.focused == null && declared('focused'), path + '.focused');
    need(node.style && typeof node.style === 'object', path + '.style');
    need(finite(node.effectiveOpacity) && node.effectiveOpacity >= 0 && node.effectiveOpacity <= 1 || node.effectiveOpacity == null && declared('effectiveOpacity'), path + '.effectiveOpacity');
    if (screen?.measurement?.renderer === 'react-native') {
      need(typeof node.effectiveBackgroundColor === 'string' || declared('effectiveBackgroundColor'), path + '.effectiveBackgroundColor');
      if (node.ownText && !node.input) {
        need(Array.isArray(node.textRects) && node.textRects.length > 0 && node.textRects.every(rect) || declared('textRects'), path + '.textRects');
        for (const property of ['resolvedFontFamily', 'fontSize', 'fontWeight', 'color']) need(typeof node.style?.[property] === 'string' || declared(property) ||
          property === 'resolvedFontFamily' && declaredFontFamily(node) !== undefined, path + '.style.' + property);
      }
    }
  }
  return errors;
}
