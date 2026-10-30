#import <Foundation/Foundation.h>
#import <AVFoundation/AVFoundation.h>
#import "../Native/PorticoSubtitleSelection.h"
#import "../Native/PorticoServerTrust.h"

// N06: the Apple subtitle grant and recheck timeline on a deterministic clock, with the HTTP boundary
// under the test's control (an NSURLProtocol on the selection's own session) and the once-a-second
// check driven by hand. The same cases as Android's PorticoSubtitleSelectionTimelineTest.
// Build: PorticoSubtitleSelection.m + this file, iOS simulator SDK (UIKit, AVFoundation, CoreMedia,
// MediaAccessibility, CoreText). Prints {"assertions":N,"failures":M}.

// The real trust owner needs React and is not linked; nothing here is TLS, so the one method the
// selection calls answers with the system's default handling.
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wincomplete-implementation"
@implementation PorticoServerTrust
+ (void)handleChallenge:(NSURLAuthenticationChallenge *)challenge completion:(void (^)(NSURLSessionAuthChallengeDisposition, NSURLCredential *))completion {
  completion(NSURLSessionAuthChallengePerformDefaultHandling, nil);
}
@end
#pragma clang diagnostic pop

static int assertions = 0, failures = 0;
static void check(BOOL ok, const char *what) {
  assertions++;
  if (!ok) {failures++; fprintf(stderr, "FAIL %s\n", what);}
}

#pragma mark - The HTTP boundary

typedef struct {NSInteger status; const char *etag; const char *body; BOOL transportError;} Answer;
static NSMutableArray<NSValue *> *answers;
static NSMutableArray<NSURLRequest *> *requests;

@interface ScriptedProtocol : NSURLProtocol @end
@implementation ScriptedProtocol
+ (BOOL)canInitWithRequest:(NSURLRequest *)request {return [request.URL.host isEqual:@"subtitles.test"];}
+ (NSURLRequest *)canonicalRequestForRequest:(NSURLRequest *)request {return request;}
- (void)startLoading {
  [requests addObject:self.request];
  if (answers.count == 0) {[self.client URLProtocol:self didFailWithError:[NSError errorWithDomain:NSURLErrorDomain code:NSURLErrorTimedOut userInfo:nil]]; return;}
  Answer answer; [answers.firstObject getValue:&answer]; [answers removeObjectAtIndex:0];
  if (answer.transportError) {[self.client URLProtocol:self didFailWithError:[NSError errorWithDomain:NSURLErrorDomain code:NSURLErrorNetworkConnectionLost userInfo:nil]]; return;}
  NSMutableDictionary *headers = [@{@"Content-Type": @"application/json"} mutableCopy];
  if (answer.etag) headers[@"ETag"] = @(answer.etag);
  NSHTTPURLResponse *response = [[NSHTTPURLResponse alloc] initWithURL:self.request.URL statusCode:answer.status HTTPVersion:@"HTTP/1.1" headerFields:headers];
  [self.client URLProtocol:self didReceiveResponse:response cacheStoragePolicy:NSURLCacheStorageNotAllowed];
  if (answer.body) [self.client URLProtocol:self didLoadData:[NSData dataWithBytes:answer.body length:strlen(answer.body)]];
  [self.client URLProtocolDidFinishLoading:self];
}
- (void)stopLoading {}
@end
static void answer(NSInteger status, const char *etag, const char *body) {Answer value = {status, etag, body, NO}; [answers addObject:[NSValue valueWithBytes:&value objCType:@encode(Answer)]];}
static void dropConnection(void) {Answer value = {0, NULL, NULL, YES}; [answers addObject:[NSValue valueWithBytes:&value objCType:@encode(Answer)]];}
static BOOL waitFor(BOOL (^condition)(void)) {
  NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:5];
  while (!condition() && deadline.timeIntervalSinceNow > 0) [NSRunLoop.mainRunLoop runMode:NSDefaultRunLoopMode beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.01]];
  return condition();
}
static void drain(void) {[NSRunLoop.mainRunLoop runMode:NSDefaultRunLoopMode beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.2]];}

#pragma mark - The selection

static const char *document = "{\"version\":1,\"timeDomain\":\"source-relative\",\"cues\":[{\"startUs\":\"0\",\"endUs\":\"100000000\",\"text\":\"Hello\"}]}";
static const char *otherDocument = "{\"version\":1,\"timeDomain\":\"source-relative\",\"cues\":[{\"startUs\":\"0\",\"endUs\":\"100000000\",\"text\":\"Changed\"}]}";

@interface Fixture : NSObject
@property(nonatomic) PorticoSubtitleSelection *selection;
@property(nonatomic) NSTimeInterval now;
@property(nonatomic, copy) NSString *text;
@property(nonatomic) NSMutableArray<NSString *> *statuses;
@end
@implementation Fixture
- (instancetype)init {
  if ((self = [super init])) {
    answers = [NSMutableArray new]; requests = [NSMutableArray new];
    _now = 1000; _text = @""; _statuses = [NSMutableArray new];
    _selection = [PorticoSubtitleSelection new];
    __weak Fixture *weak = self;
    _selection.clock = ^NSTimeInterval {return weak.now;};
    _selection.manualTimer = YES;
    _selection.configureSession = ^(NSURLSessionConfiguration *configuration) {configuration.protocolClasses = @[ScriptedProtocol.class];};
    _selection.onText = ^(NSDictionary *cue) {weak.text = cue[@"text"];};
    _selection.onStatus = ^(NSString *error) {[weak.statuses addObject:error];};
  }
  return self;
}
- (void)select {
  [self.selection apply:@{@"mode": @"track", @"revision": @1, @"resourceRevision": @1, @"offsetUs": @"0", @"binding": @"b1", @"resourceId": @"res1",
                          @"documentUrl": @"https://subtitles.test/v1/media/subtitles/GRANT/res1/1"}
                   item:nil source:@"https://subtitles.test/v1/media/GRANT/file.m3u8"];
}
- (void)show {[self.selection tick:CMTimeMake(1000000, 1000000)];}
- (BOOL)settle:(NSUInteger)count {
  // NSURLProtocol consuming an answer does not mean URLSession has delivered it. Wait for
  // the owner's completion too, before showing a cue or replacing the scripted HTTP fixture.
  return waitFor(^BOOL {return requests.count >= count && answers.count == 0 && !self.selection.requestPending;});
}
@end

int main(void) {@autoreleasepool {
  {
    // The first document is accepted and shown; a recheck sends its validator, and "unchanged"
    // renews the grant from when the answer arrived, not from when the request left.
    Fixture *f = [Fixture new];
    answer(200, "\"v1\"", document);
    [f select]; check([f settle:1], "initial request answered");
    [f show]; check([f.text isEqual:@"Hello"], "the accepted document is shown");
    check([requests[0] valueForHTTPHeaderField:@"If-None-Match"] == nil, "the first request has no validator");
    f.now += 24; [f.selection timerTick]; drain();
    check(requests.count == 1, "no recheck before 25 seconds");
    f.now += 1; answer(304, NULL, NULL); [f.selection timerTick]; check([f settle:2], "recheck at 25 seconds");
    check([[requests[1] valueForHTTPHeaderField:@"If-None-Match"] isEqual:@"\"v1\""], "the recheck sends the held validator");
    f.now += 39; [f show]; check([f.text isEqual:@"Hello"], "unchanged renews the grant for 40 seconds from the answer");
    // The same tick finds the grant expired and the next recheck due (41 seconds since the last).
    answer(200, "\"v2\"", otherDocument);
    f.now += 2; [f.selection timerTick]; check([f.text isEqual:@""], "an expired grant clears the cue on the timer, with no position tick");
    check([f settle:3], "the next recheck runs");
    [f show]; check([f.text isEqual:@"Changed"], "a changed document replaces the held cues");
    [f.selection invalidate];
  }
  {
    // Transport failures keep the cues only under the existing grant, retry after five seconds,
    // and never renew it.
    Fixture *f = [Fixture new];
    answer(200, "\"v1\"", document); [f select]; [f settle:1]; [f show];
    f.now += 25; answer(503, NULL, NULL); [f.selection timerTick]; check([f settle:2], "a 503 recheck");
    [f show]; check([f.text isEqual:@"Hello"], "a 5xx keeps the cues under the grant");
    check([f.statuses.lastObject isEqual:@"subtitle_connection_failed"], "a 5xx reports the connection");
    f.now += 4; [f.selection timerTick]; drain(); check(requests.count == 2, "no retry before five seconds");
    f.now += 1; dropConnection(); [f.selection timerTick]; check([f settle:3], "retry after five seconds");
    [f show]; check([f.text isEqual:@"Hello"], "a dropped connection keeps the cues under the grant");
    f.now += 5; dropConnection(); [f.selection timerTick]; check([f settle:4], "and again five seconds later");
    f.now = 1000 + 41; [f show]; check([f.text isEqual:@""], "failures never renew the grant: the cues expire 40 seconds after acceptance");
    [f.selection invalidate];
  }
  {
    // A permanent refusal or an invalid document clears the held cues at once.
    Fixture *f = [Fixture new];
    answer(200, "\"v1\"", document); [f select]; [f settle:1]; [f show];
    f.now += 25; answer(404, NULL, NULL); [f.selection timerTick]; check([f settle:2], "a 404 recheck");
    check([f.text isEqual:@""] && [f.statuses.lastObject isEqual:@"subtitle_unavailable"], "a 404 clears the cues immediately");
    [f show]; check([f.text isEqual:@""], "and they stay cleared");
    [f.selection invalidate];
    Fixture *g = [Fixture new];
    answer(200, "\"v1\"", document); [g select]; [g settle:1]; [g show];
    g.now += 25; answer(200, "\"v2\"", "{\"version\":2}"); [g.selection timerTick]; check([g settle:2], "an invalid document");
    check([g.text isEqual:@""] && [g.statuses.lastObject isEqual:@"subtitle_document_invalid"], "an invalid document clears the cues immediately");
    [g.selection invalidate];
  }
  {
    // Turning subtitles off cancels the work; nothing late can bring the cues back.
    Fixture *f = [Fixture new];
    answer(200, "\"v1\"", document); [f select]; [f settle:1]; [f show];
    [f.selection invalidate]; check([f.text isEqual:@""], "off clears the cue");
    f.now += 30; [f.selection timerTick]; [f show]; drain();
    check(requests.count == 1 && [f.text isEqual:@""], "after off, no recheck and no cue");
  }
  printf("{\"assertions\":%d,\"failures\":%d}\n", assertions, failures);
  return failures == 0 ? 0 : 1;
}}
