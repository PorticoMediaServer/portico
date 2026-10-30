#import <Foundation/Foundation.h>
#import <AVFoundation/AVFoundation.h>
#import "../Native/PorticoPlaybackFailures.h"

// N03: the Apple failure classification as a table, matching Android's PorticoPlaybackFailuresTest.
// Runs the same header PorticoPlaybackController.m reports from. Prints {"assertions":N,...}.

static int assertions = 0, failures = 0;
static void check(BOOL ok, NSString *what) {
  assertions++;
  if (!ok) {failures++; fprintf(stderr, "FAIL %s\n", what.UTF8String);}
}
static NSError *failure(NSErrorDomain domain, NSInteger code, NSDictionary *info) {return [NSError errorWithDomain:domain code:code userInfo:info];}
static NSError *wrapped(NSErrorDomain domain, NSInteger code, NSError *under) {return failure(domain, code, @{NSUnderlyingErrorKey: under});}
static NSDictionary *classify(NSError *top) {return PorticoPlaybackFailureFacts(top, NO, nil, 0, nil);}
static void expect(NSString *name, NSDictionary *facts, BOOL network, BOOL delivery, NSInteger status, NSString *engine, BOOL uncertain) {
  check([facts[@"networkError"] boolValue] == network, [name stringByAppendingString:@" networkError"]);
  check([facts[@"deliveryError"] boolValue] == delivery, [name stringByAppendingString:@" deliveryError"]);
  check([facts[@"httpStatus"] integerValue] == status, [name stringByAppendingString:@" httpStatus"]);
  check([facts[@"engineCode"] isEqualToString:engine], [name stringByAppendingString:@" engineCode"]);
  check([facts[@"uncertain"] boolValue] == uncertain, [name stringByAppendingString:@" uncertain"]);
  check([facts[@"error"] isEqualToString:network ? @"playback_connection_interrupted" : @"playback_failed"], [name stringByAppendingString:@" error"]);
}

int main(void) {@autoreleasepool {
  // The connection: transient URL and socket failures are weather.
  for (NSNumber *code in @[@(NSURLErrorTimedOut), @(NSURLErrorNotConnectedToInternet), @(NSURLErrorNetworkConnectionLost), @(NSURLErrorBadServerResponse)])
    expect([NSString stringWithFormat:@"URL %@", code], classify(failure(NSURLErrorDomain, code.integerValue, nil)), YES, NO, 0, @"", NO);
  expect(@"POSIX ECONNRESET", classify(failure(NSPOSIXErrorDomain, ECONNRESET, nil)), YES, NO, 0, @"", NO);
  // Delivery: failures that waiting will not change.
  for (NSNumber *code in @[@(NSURLErrorCancelled), @(NSURLErrorFileDoesNotExist), @(NSURLErrorNoPermissionsToReadFile), @(NSURLErrorAppTransportSecurityRequiresSecureConnection), @(NSURLErrorUserAuthenticationRequired)])
    expect([NSString stringWithFormat:@"URL %@", code], classify(failure(NSURLErrorDomain, code.integerValue, nil)), NO, YES, 0, @"", NO);
  for (NSNumber *code in @[@(ENOENT), @(EACCES), @(EPERM), @(EISDIR)])
    expect([NSString stringWithFormat:@"POSIX %@", code], classify(failure(NSPOSIXErrorDomain, code.integerValue, nil)), NO, YES, 0, @"", NO);
  expect(@"Cocoa no such file", classify(failure(NSCocoaErrorDomain, NSFileReadNoSuchFileError, nil)), NO, YES, 0, @"", NO);
  expect(@"AV content unavailable", classify(failure(AVFoundationErrorDomain, AVErrorContentIsUnavailable, nil)), NO, YES, 0, @"", NO);
  // The engine: a decoder or a parser refused the media.
  for (NSNumber *code in @[@(AVErrorDecodeFailed), @(AVErrorDecoderNotFound), @(AVErrorDecoderTemporarilyUnavailable)])
    expect([NSString stringWithFormat:@"AV decode %@", code], classify(failure(AVFoundationErrorDomain, code.integerValue, nil)), NO, NO, 0, @"decode_error", NO);
  for (NSNumber *code in @[@(AVErrorFileFormatNotRecognized), @(AVErrorFailedToParse), @(AVErrorFormatUnsupported)])
    expect([NSString stringWithFormat:@"AV source %@", code], classify(failure(AVFoundationErrorDomain, code.integerValue, nil)), NO, NO, 0, @"source_not_supported", NO);
  // The system interrupted playback: not evidence about the media.
  for (NSNumber *code in @[@(-11818), @(-11819), @(-11847), @(-11801)])
    expect([NSString stringWithFormat:@"AV interrupted %@", code], classify(failure(AVFoundationErrorDomain, code.integerValue, nil)), NO, NO, 0, @"", YES);
  expect(@"AV unknown", classify(failure(AVFoundationErrorDomain, AVErrorUnknown, nil)), NO, NO, 0, @"", NO);
  // HTTP evidence overrides decoder and transport classes (Android httpEvidenceOverrides...).
  for (NSNumber *status in @[@400, @401, @403, @404, @410, @499]) {
    NSHTTPURLResponse *response = [[NSHTTPURLResponse alloc] initWithURL:[NSURL URLWithString:@"https://server.invalid/a"] statusCode:status.integerValue HTTPVersion:@"HTTP/1.1" headerFields:nil];
    for (NSError *top in @[failure(AVFoundationErrorDomain, AVErrorDecodeFailed, @{@"NSHTTPURLResponse": response}), failure(NSURLErrorDomain, NSURLErrorTimedOut, @{@"NSHTTPURLResponse": response})])
      expect([NSString stringWithFormat:@"HTTP %@ on %@", status, top.domain], classify(top), NO, YES, status.integerValue, @"", NO);
  }
  for (NSNumber *status in @[@408, @429, @500, @502, @503, @599]) {
    NSHTTPURLResponse *response = [[NSHTTPURLResponse alloc] initWithURL:[NSURL URLWithString:@"https://server.invalid/a"] statusCode:status.integerValue HTTPVersion:@"HTTP/1.1" headerFields:nil];
    expect([NSString stringWithFormat:@"HTTP %@", status], classify(failure(AVFoundationErrorDomain, AVErrorDecodeFailed, @{@"NSHTTPURLResponse": response})), YES, YES, status.integerValue, @"", NO);
  }
  expect(@"HTTP in description", classify(failure(AVFoundationErrorDomain, AVErrorUnknown, @{NSLocalizedDescriptionKey: @"The server returned HTTP error 403"})), NO, YES, 403, @"", NO);
  expect(@"CoreMedia 404", classify(failure(@"CoreMediaErrorDomain", -12938, nil)), NO, YES, 404, @"", NO);
  expect(@"CoreMedia 403", classify(failure(@"CoreMediaErrorDomain", -12660, nil)), NO, YES, 403, @"", NO);
  // The outermost error that says anything decides; a deeper code never turns weather into the media.
  expect(@"decode over lost connection", classify(wrapped(AVFoundationErrorDomain, AVErrorDecodeFailed, failure(NSURLErrorDomain, NSURLErrorNetworkConnectionLost, nil))), YES, NO, 0, @"", NO);
  expect(@"lost connection over missing file", classify(wrapped(NSURLErrorDomain, NSURLErrorNetworkConnectionLost, failure(NSPOSIXErrorDomain, ENOENT, nil))), YES, NO, 0, @"", NO);
  expect(@"five levels deep is not read", classify(wrapped(AVFoundationErrorDomain, AVErrorUnknown, wrapped(AVFoundationErrorDomain, AVErrorUnknown, wrapped(AVFoundationErrorDomain, AVErrorUnknown, wrapped(AVFoundationErrorDomain, AVErrorUnknown, failure(NSURLErrorDomain, NSURLErrorTimedOut, nil)))))), NO, NO, 0, @"", NO);
  // The error log counts only when recent, and never over an engine verdict.
  NSError *unknown = failure(AVFoundationErrorDomain, AVErrorUnknown, nil);
  expect(@"recent log URL failure", PorticoPlaybackFailureFacts(unknown, YES, NSURLErrorDomain, NSURLErrorTimedOut, nil), YES, NO, 0, @"", NO);
  expect(@"recent log permanent URL failure", PorticoPlaybackFailureFacts(unknown, YES, NSURLErrorDomain, NSURLErrorFileDoesNotExist, nil), NO, YES, 0, @"", NO);
  expect(@"recent log HTTP comment", PorticoPlaybackFailureFacts(unknown, YES, @"CoreMediaErrorDomain", -12938, @"HTTP 404: File Not Found"), NO, YES, 404, @"", NO);
  expect(@"recent log HTTP status", PorticoPlaybackFailureFacts(unknown, YES, @"CoreMediaErrorDomain", 503, nil), YES, YES, 503, @"", NO);
  expect(@"recent log without a cause is uncertain", PorticoPlaybackFailureFacts(unknown, YES, @"CoreMediaErrorDomain", -1, @"segment"), NO, NO, 0, @"", YES);
  expect(@"an old log is not read", PorticoPlaybackFailureFacts(unknown, NO, NSURLErrorDomain, NSURLErrorTimedOut, @"HTTP 503"), NO, NO, 0, @"", NO);
  expect(@"the engine verdict wins over a recent log", PorticoPlaybackFailureFacts(failure(AVFoundationErrorDomain, AVErrorDecodeFailed, nil), YES, NSURLErrorDomain, 503, @"HTTP 503"), NO, NO, 0, @"decode_error", NO);
  // The detail names the top error and its cause.
  check([classify(wrapped(AVFoundationErrorDomain, AVErrorDecodeFailed, failure(NSOSStatusErrorDomain, -12909, nil)))[@"errorDetail"] isEqualToString:[NSString stringWithFormat:@"%@ %ld / %@ -12909", AVFoundationErrorDomain, (long)AVErrorDecodeFailed, NSOSStatusErrorDomain]], @"errorDetail");
  printf("{\"assertions\":%d,\"failures\":%d}\n", assertions, failures);
  return failures == 0 ? 0 : 1;
}}
