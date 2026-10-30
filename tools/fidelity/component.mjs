/** Root-relative projection of an actually measured DOM subtree. No scaling,
 * style replacement, new rendering or full-screen acceptance is involved. */
export function componentMeasurement(measured, {elementId, frames = {}, reference = false} = {}) {
  if (measured?.scope !== 'screen' || measured.measurement?.renderer !== 'dom' || measured.measurement.coordinateUnit !== 'css-px' ||
    measured.measurement.sourceRole && !reference) throw new Error('A genuine DOM source measurement is required');
  if (measured.anchorIssues?.length) throw new Error('Ambiguous source anchors cannot establish a component');
  const nodes = new Map(measured.elements.map(node => [node.id, node]));
  const root = elementId ? nodes.get(elementId) : measured.elements.find(node => node.parent == null);
  if (!root?.box || !(root.box.width > 0 && root.box.height > 0)) throw new Error('A positive measured component root is required');
  if (Object.values(frames).some(frame => typeof frame !== 'string' || !/^[a-z][a-z0-9-]{0,63}$/.test(frame)) || frames[root.id] && frames[root.id] !== 'component-root') throw new Error('Public component part names required');
  const included = new Set();
  for (const node of measured.elements) {
    const seen = new Set();
    for (let id = node.id; id && !seen.has(id); id = nodes.get(id)?.parent) {
      if (id === root.id) {included.add(node.id); break;}
      seen.add(id);
    }
  }
  for (const id of Object.keys(frames)) if (!included.has(id)) throw new Error('Component part is outside its measured root');
  const sourceRoot = measured.referencePresentation?.windowBounds ?? measured.root;
  const viewport = measured.referencePresentation?.windowViewport ?? measured.viewport;
  if (reference && viewport.deviceScaleFactor !== 1) throw new Error('Authored component references require actual DPR1');
  const windowBounds = {x: sourceRoot.x + root.box.x, y: sourceRoot.y + root.box.y, width: root.box.width, height: root.box.height};
  const move = box => box && ({...box, x: box.x - root.box.x, y: box.y - root.box.y});
  const elements = measured.elements.filter(node => included.has(node.id)).map(node => {
    const result = structuredClone(node);
    if (node.id === root.id) {result.parent = null; result.frame = 'component-root';}
    if (frames[node.id]) result.frame = frames[node.id];
    if (result.box) result.box = move(result.box);
    for (const property of ['textRects', 'descendantTextRects']) if (result[property]) result[property] = result[property].map(move);
    return result;
  });
  const strings = measured.strings.filter(value => included.has(value.owner)).map(value => ({...structuredClone(value),
    ...(value.owner === root.id ? {frame: 'component-root'} : {}), ...(frames[value.owner] ? {frame: frames[value.owner]} : {}),
    ...(value.box ? {box: move(value.box)} : {}), ...(value.rects ? {rects: value.rects.map(move)} : {})}));
  const groups = new Map();
  for (const node of elements) if (node.frame) groups.set(node.frame, [...(groups.get(node.frame) ?? []), node.id]);
  const anchorIssues = [...groups].filter(([, ids]) => ids.length !== 1).map(([name, ids]) => ({name, matches: ids.length, elements: ids}));
  const result = {version: measured.version, scope: 'component', measurement: {...measured.measurement}, viewport: structuredClone(viewport),
    root: {x: 0, y: 0, width: root.box.width, height: root.box.height},
    fullViewport: measured.fullViewport === true && windowBounds.x === 0 && windowBounds.y === 0 && windowBounds.width === viewport.width && windowBounds.height === viewport.height,
    componentPresentation: {windowBounds, sourceRoot: structuredClone(sourceRoot)}, elements, strings, anchorIssues, repeatedLists: [],
    ...(measured.fontStatus === undefined ? {} : {fontStatus: measured.fontStatus}), ...(measured.fontFaces ? {fontFaces: structuredClone(measured.fontFaces)} : {}),
    ...(measured.documentFocused === undefined ? {} : {documentFocused: measured.documentFocused})};
  if (reference) result.measurement.sourceRole = 'reference-component';
  const unavailable = (measured.unavailable ?? []).filter(fact => fact.element === undefined || included.has(fact.element));
  if (unavailable.length) result.unavailable = structuredClone(unavailable);
  if (measured.diagnostics) result.diagnostics = structuredClone(measured.diagnostics);
  return result;
}
