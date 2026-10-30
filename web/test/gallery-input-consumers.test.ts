import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {parseSync} from 'rolldown/utils';

for (const family of ['layout', 'browse']) {
  test(`${family} obtains its own external DEV inputs without importing private HTML or raw responses`, () => {
    const url = new URL(`../src/dev/families/${family}.tsx`, import.meta.url);
    const parsed = parseSync(fileURLToPath(url), readFileSync(url, 'utf8'));
    assert.deepEqual(parsed.errors, []);
    const imports = parsed.program.body.filter(node => node.type === 'ImportDeclaration');
    for (const declaration of imports) {
      const specifier = declaration.source.value;
      assert.equal(typeof specifier, 'string');
      assert.doesNotMatch(specifier, /\.html(?:\?|$)|\/raw\//, 'Private reference documents/responses must stay outside the application import graph.');
      if (specifier.includes('design-ui-v1/')) assert.match(specifier, /\/assets\//, 'Only the existing DEV artwork boundary may refer to external assets.');
    }
    const inputImport = imports.find(node => node.source.value === 'virtual:portico-kit-families');
    assert.ok(inputImport?.specifiers.some(node => node.type === 'ImportSpecifier' && node.local.name === 'loadGalleryInputs'));
    const calls: {node: Record<string, unknown>; parent?: Record<string, unknown>}[] = [];
    const visit = (value: unknown, parent?: Record<string, unknown>) => {
      if (!value || typeof value !== 'object' || Array.isArray(value)) return;
      const node = value as Record<string, unknown>, callee = node.callee as Record<string, unknown> | undefined;
      if (node.type === 'CallExpression' && callee?.type === 'Identifier' && callee.name === 'loadGalleryInputs') calls.push({node, parent});
      for (const [key, child] of Object.entries(node)) if (key !== 'parent') {
        if (Array.isArray(child)) child.forEach(value => visit(value, node));
        else visit(child, node);
      }
    };
    visit(parsed.program);
    assert.equal(calls.length, 1, 'A family must not read another family or introduce a second input mechanism.');
    const call = calls[0]!;
    assert.equal(call.parent?.type, 'AwaitExpression', 'Input loading must finish before the genuine model/parser consumes it.');
    const args = call.node.arguments;
    assert.ok(Array.isArray(args));
    assert.deepEqual(args.map(argument => ({type: argument.type, value: argument.value})), [{type: 'Literal', value: family}]);
  });
}
