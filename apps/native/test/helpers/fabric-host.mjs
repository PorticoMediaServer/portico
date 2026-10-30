import {readFile} from 'node:fs/promises';
import {createRequire} from 'node:module';
import {compileFunction} from 'node:vm';
import {dirname, join} from 'node:path';
const require = createRequire(import.meta.url);
export const React = require(join(dirname(require.resolve('react')), 'cjs/react.production.js'));

// The installed production Fabric reconciler, as in fabric-playback-harness.
// Only the native host boundary is controlled. Node tags identify committed
// native-host fibers; they are not claims about an OS View, Modal, IME or focus.
export async function fabricHost({publicInstance = tag => ({tag})} = {}) {
  const errors = [], commits = [];
  let root = [], mounted = false;
  const attributes = props => Object.fromEntries(Object.entries(props).filter(([key]) => key !== 'children' && key !== 'ref'));
  const privateInterface = {
    ReactNativeViewConfigRegistry: {get: name => ({uiViewClassName: name, validAttributes: {}})},
    createAttributePayload: attributes,
    diffAttributePayloads: (oldProps, props) => Object.keys({...oldProps, ...props}).some(key => !Object.is(oldProps[key], props[key])) ? attributes(props) : null,
    createPublicRootInstance: () => ({}), createPublicInstance: publicInstance, createPublicTextInstance: tag => ({tag}),
    RawEventEmitter: {emit() {}}, ReactFiberErrorDialog: {showErrorDialog: () => true},
  };
  const fabric = {
    createNode: (tag, type, _root, props) => ({tag, type, props, children: []}),
    cloneNodeWithNewChildren: node => ({...node, children: []}),
    cloneNodeWithNewChildrenAndProps: (node, props) => ({...node, props: {...node.props, ...props}, children: []}),
    cloneNodeWithNewProps: (node, props) => ({...node, props: {...node.props, ...props}}),
    appendChild: (node, child) => node.children.push(child), createChildSet: () => [], appendChildToSet: (set, node) => set.push(node),
    completeRoot: (_tag, nodes) => {root = nodes; commits.push(nodes);}, unstable_getCurrentEventPriority: () => 32,
  };
  const renderer = {};
  const path = require.resolve('react-native/Libraries/Renderer/implementations/ReactFabric-prod.js');
  compileFunction(await readFile(path, 'utf8'), ['require', 'exports', 'nativeFabricUIManager'])(name =>
    name.endsWith('ReactNativePrivateInitializeCore') ? {} : name.endsWith('ReactNativePrivateInterface') ? privateInterface : name === 'react' ? React : require(name), renderer, fabric);
  const settle = async () => {for (let n = 0; n < 12; n++) await new Promise(resolve => setImmediate(resolve)); if (errors.length) throw errors[0];};
  const options = {onUncaughtError: error => errors.push(error), onCaughtError: error => errors.push(error), onRecoverableError: error => errors.push(error)};
  return {
    commits, get root() {return root;}, settle,
    async render(element) {mounted = true; renderer.render(element, 11, undefined, true, options); await settle();},
    async unmount() {if (!mounted) return; mounted = false; renderer.stopSurface(11); await settle();},
  };
}
