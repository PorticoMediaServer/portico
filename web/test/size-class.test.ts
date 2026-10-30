import test from 'node:test';
import assert from 'node:assert/strict';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {useSizeClass, useCoarsePointer} from '../src/kit/theme/index.ts';

test('real React server rendering uses documented compact startup before live measurement', () => {
  function Probe() {return createElement('output', {'data-size': useSizeClass(), 'data-coarse': useCoarsePointer()});}
  assert.equal(renderToStaticMarkup(createElement(Probe)), '<output data-size="compact" data-coarse="false"></output>');
});
