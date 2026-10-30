import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const source = (path: string) => readFileSync(new URL('../' + path, import.meta.url), 'utf8');

// Android renewal/expiry behavior is exercised by PorticoSubtitleSelectionTimelineTest, Apple's by
// apple-subtitle-timeline-tests.m (owned Apple runner). This source guard is not timeline coverage.
test('Apple subtitle transport failures retain cues only under the existing grant', () => {
  const native = source('Native/PorticoSubtitleSelection.m');
  const completion = native.slice(native.indexOf('- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task didCompleteWithError:'));
  assert.match(completion, /if\(_failed\|\|error\)\{_bytes=nil;[^\n]*\[self tick:_time\]/);
  assert.match(native, /http.statusCode>=500&&http.statusCode<=599/);
  assert.match(completion, /if\(_transientFailure\|\|\(!_failed&&error\)\)_lastRequest=\[self now\]-20/);
  assert.match(native, /_cues=nil;_authorizedUntil=0;\[self setText:@""\]/);
  assert.match(native, /\[self now\]>_authorizedUntil/);
});
