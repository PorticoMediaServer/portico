import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const source = (path: string) => readFileSync(new URL('../' + path, import.meta.url), 'utf8');

// Structural guards only; Android emitted classification is executed by PorticoPlaybackFailuresTest.
// Apple classification still requires an executable native-owner test (AUD-test-quality-N03).
test('generic engine classes are not used to learn native incompatibilities', () => {
  const android = source('android/app/src/main/java/tv/getportico/portico/native/PorticoPlaybackController.kt') +
    source('android/app/src/main/java/tv/getportico/portico/native/PorticoPlaybackFailures.kt');
  const apple = source('Native/PorticoPlaybackController.m') + source('Native/PorticoPlaybackFailures.h');
  assert.doesNotMatch(android, /else -> "engine_error"/);
  assert.doesNotMatch(apple, /NSString \*engine=@"engine_error"/);
  assert.doesNotMatch(source('src-next/platform/playback/runtime.ts'), /\['decode_error', 'source_not_supported', 'engine_error'\]/);
});

test('transport failures take precedence over decoder evidence on both platforms', () => {
  const android = source('android/app/src/main/java/tv/getportico/portico/native/PorticoPlaybackController.kt') +
    source('android/app/src/main/java/tv/getportico/portico/native/PorticoPlaybackFailures.kt');
  for (const code of ['IO_UNSPECIFIED', 'IO_BAD_HTTP_STATUS', 'IO_INVALID_HTTP_CONTENT_TYPE', 'IO_FILE_NOT_FOUND', 'IO_NO_PERMISSION', 'IO_CLEARTEXT_NOT_PERMITTED', 'IO_READ_POSITION_OUT_OF_RANGE']) {
    assert.match(android, new RegExp('PlaybackException.ERROR_CODE_' + code));
  }
  const apple = source('Native/PorticoPlaybackController.m') + source('Native/PorticoPlaybackFailures.h');
  assert.match(apple, /\[error.domain isEqual:NSURLErrorDomain\]\)\{/);
  const classification = source('Native/PorticoPlaybackFailures.h');
  assert.match(classification, /AVErrorContentIsUnavailable\)delivery=YES/);
  assert.match(android, /fact\["engineCode"\] = if \(network \|\| delivery\) "" else engine/);
  assert.match(apple, /@"engineCode":\(network\|\|delivery\)\?@"":engine/);
});

test('native startup and wrapped HTTP evidence reaches the shared failure policy', () => {
  const android = source('android/app/src/main/java/tv/getportico/portico/native/PorticoPlaybackController.kt') +
    source('android/app/src/main/java/tv/getportico/portico/native/PorticoPlaybackFailures.kt');
  const apple = source('Native/PorticoPlaybackController.m') + source('Native/PorticoPlaybackFailures.h');
  assert.match(android, /HttpDataSource.InvalidResponseCodeException/);
  assert.match(android, /fact\["hasPlayed"\] = hasPlayed/);
  assert.match(android, /onRenderedFirstFrame\(\) \{ hasPlayed = true/);
  assert.match(apple, /error.code==-12938\)return 404/);
  assert.match(apple, /error.code==-12660\)return 403/);
  assert.match(apple, /PorticoHTTPStatusInComment\(logComment\)/);
  assert.match(apple, /@"hasPlayed":@\(_hasPlayed\)/);
  assert.match(apple, /displayReadyForLease:[^\n]+_hasPlayed=YES/);
  assert.match(source('src-next/platform/playback/runtime.ts'), /const failure = classifyPlaybackFailure\(fact\)/);
});

test('the Apple classifier lets the outermost evidence decide and treats a socket error as the connection', () => {
  const apple = source('Native/PorticoPlaybackController.m') + source('Native/PorticoPlaybackFailures.h');
  const classification = source('Native/PorticoPlaybackFailures.h');
  // The walk down the chain stops at the first error that says anything; nothing deeper overrides it.
  assert.match(classification, /i<4&&!network&&!delivery;/);
  assert.doesNotMatch(classification, /else if\(delivery\)network=NO/);
  // Only a missing or forbidden file is delivery; every other socket-level code is the connection.
  assert.match(classification, /error\.code==ENOENT\|\|error\.code==EACCES\|\|error\.code==EPERM\|\|error\.code==EISDIR\)delivery=YES;else network=YES;/);
  // A bad server response with no status is a failing server, not a permanent refusal.
  const permanent = apple.slice(apple.indexOf('static inline BOOL PorticoURLErrorIsPermanent'), apple.indexOf('static inline NSInteger PorticoErrorHTTPStatus'));
  assert.ok(permanent.length > 0);
  assert.doesNotMatch(permanent, /NSURLErrorBadServerResponse\)/);
  assert.match(permanent, /NSURLErrorFileDoesNotExist/);
});

test('the Apple error log is evidence only when fresh and when the engine named no fault', () => {
  const apple = source('Native/PorticoPlaybackController.m') + source('Native/PorticoPlaybackFailures.h');
  const classification = source('Native/PorticoPlaybackFailures.h');
  assert.match(apple, /BOOL recent=log!=nil&&log\.date!=nil&&fabs\(log\.date\.timeIntervalSinceNow\)<=15;/);
  assert.match(classification, /BOOL fresh=logRecent&&!engine\.length;/);
  assert.match(classification, /if\(!httpStatus&&fresh\)httpStatus=PorticoHTTPStatusInComment\(logComment\)/);
  assert.match(classification, /fresh&&\[logDomain isEqual:NSURLErrorDomain\]/);
  assert.ok(classification.indexOf('NSString *engine=@""') < classification.indexOf('BOOL fresh='), 'the engine verdict is known before the log is consulted');
});

test('both runtimes mark a failure that is not about the media, so start-up does not report it', () => {
  const android = source('android/app/src/main/java/tv/getportico/portico/native/PorticoPlaybackController.kt') +
    source('android/app/src/main/java/tv/getportico/portico/native/PorticoPlaybackFailures.kt');
  assert.match(android, /fact\["uncertain"\] = !network && !delivery && engine\.isEmpty\(\) && errorCode in UNCERTAIN_ERRORS/);
  const uncertain = android.match(/UNCERTAIN_ERRORS = setOf\(([\s\S]*?)\)/)?.[1];
  assert.ok(uncertain);
  assert.deepEqual([...uncertain.matchAll(/PlaybackException\.ERROR_CODE_([A-Z_]+)/g)].map(m => m[1]), [
    'TIMEOUT', 'REMOTE_ERROR', 'DECODING_RESOURCES_RECLAIMED', 'AUDIO_TRACK_INIT_FAILED', 'AUDIO_TRACK_WRITE_FAILED',
  ]);
  const apple = source('Native/PorticoPlaybackController.m') + source('Native/PorticoPlaybackFailures.h');
  const classification = source('Native/PorticoPlaybackFailures.h');
  assert.match(classification, /BOOL uncertain=!network&&!delivery&&!httpStatus&&!engine\.length&&\(interrupted\|\|fresh\);/);
  assert.match(classification, /@"uncertain":@\(uncertain\)/);
  assert.match(source('src-next/platform/playback/playback-failure.ts'), /fact\.uncertain !== true/);
});

// E-mobile-27: the last read of a short clip failed after its final frame and the viewer saw
// "Playback stopped / Try again". A failure within a second of the known duration is its end.
test('E-mobile-27: a failure at the natural end is the end, anything earlier is still a failure', async () => {
  const {atNaturalEnd} = await import('../src-next/platform/playback/playback-failure.ts');
  assert.equal(atNaturalEnd(29.4, 30), true);
  assert.equal(atNaturalEnd(30, 30), true);
  assert.equal(atNaturalEnd(28.5, 30), false);
  assert.equal(atNaturalEnd(10, 0), false, 'an unknown duration is never an end');
  assert.equal(atNaturalEnd(Number.NaN, 30), false);
  assert.equal(atNaturalEnd(undefined, 30), false);
});
