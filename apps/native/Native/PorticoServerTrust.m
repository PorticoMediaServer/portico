#import "PorticoServerTrust.h"
#import <CommonCrypto/CommonDigest.h>
#import <Security/Security.h>
#import <React/RCTHTTPRequestHandler.h>

static NSString *const PorticoTrustGroup = @"group.tv.getportico.portico";
static NSString *const PorticoTrustRecord = @"portico.certificates.v1";
static const NSUInteger PorticoTrustMaxPins = 64, PorticoTrustMaxRefused = 32;

static NSUserDefaults *PorticoTrustDefaults(void) {
  return [[NSUserDefaults alloc] initWithSuiteName:PorticoTrustGroup] ?: NSUserDefaults.standardUserDefaults;
}
static BOOL PorticoTrustText(NSString *value, NSString *pattern) {
  return [value isKindOfClass:NSString.class] && [value rangeOfString:pattern options:NSRegularExpressionSearch].location != NSNotFound;
}
static NSString *PorticoTrustKey(NSString *host, NSInteger port) {
  if (![host isKindOfClass:NSString.class] || !host.length || port <= 0 || port > 65535) return nil;
  NSString *bare = host;
  if ([bare hasPrefix:@"["] && [bare hasSuffix:@"]"]) bare = [bare substringWithRange:NSMakeRange(1, bare.length - 2)];
  return [NSString stringWithFormat:@"%@:%ld", bare.lowercaseString, (long)port];
}
/**
 * `host:port` of the Portico Account service (Info.plist's PorticoHostedURL, else production). It is
 * reached over publicly trusted TLS only: no owner's certificate is ever pinned, remembered or
 * accepted there (NS-02), whatever the app asks.
 */
static NSString *PorticoTrustAccountKey(void) {
  static NSString *key; static dispatch_once_t once;
  dispatch_once(&once, ^{
    NSString *named = NSBundle.mainBundle.infoDictionary[@"PorticoHostedURL"];
    NSString *origin = [named isKindOfClass:NSString.class] && named.length && ![named containsString:@"$("] ? named : @"https://web.getportico.tv";
    NSURLComponents *url = [NSURLComponents componentsWithString:origin];
    key = [url.scheme.lowercaseString isEqualToString:@"https"] ? PorticoTrustKey(url.host, url.port ? url.port.integerValue : 443) : nil;
  });
  return key;
}
static BOOL PorticoTrustIsAccount(NSString *key) {
  return key && [key isEqualToString:PorticoTrustAccountKey()];
}
static NSString *PorticoTrustFingerprint(SecTrustRef trust) {
  CFArrayRef chain = SecTrustCopyCertificateChain(trust);
  if (!chain) return nil;
  NSString *result = nil;
  if (CFArrayGetCount(chain) > 0) {
    NSData *der = CFBridgingRelease(SecCertificateCopyData((SecCertificateRef)CFArrayGetValueAtIndex(chain, 0)));
    unsigned char digest[CC_SHA256_DIGEST_LENGTH];
    CC_SHA256(der.bytes, (CC_LONG)der.length, digest);
    NSString *base = [[NSData dataWithBytes:digest length:sizeof digest] base64EncodedStringWithOptions:0];
    result = [[[base stringByReplacingOccurrencesOfString:@"+" withString:@"-"] stringByReplacingOccurrencesOfString:@"/" withString:@"_"] stringByReplacingOccurrencesOfString:@"=" withString:@""];
  }
  CFRelease(chain);
  return result;
}

static NSMutableDictionary<NSString *, NSString *> *PorticoTrustRefused;

@interface PorticoTrustSessionDelegate : NSObject <NSURLSessionDelegate> @end
@implementation PorticoTrustSessionDelegate
- (void)URLSession:(NSURLSession *)session didReceiveChallenge:(NSURLAuthenticationChallenge *)challenge completionHandler:(void (^)(NSURLSessionAuthChallengeDisposition, NSURLCredential *))completionHandler {
  [PorticoServerTrust handleChallenge:challenge completion:completionHandler];
}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task willPerformHTTPRedirection:(NSHTTPURLResponse *)response newRequest:(NSURLRequest *)request completionHandler:(void (^)(NSURLRequest *))completionHandler {
  completionHandler(nil);
}
@end

/** One exact observed leaf at one address, for the proof only. No general trust fallback. */
@interface PorticoTrustProofDelegate : NSObject <NSURLSessionDataDelegate>
@property(nonatomic, copy) NSString *key;
@property(nonatomic, copy) NSString *fingerprint;
@property(nonatomic, copy) NSString *tlsLeafFingerprint;
@property(nonatomic, strong) NSURL *url;
@property(nonatomic, strong) NSHTTPURLResponse *response;
@property(nonatomic, strong) NSMutableData *data;
@property(nonatomic, copy) void (^completion)(NSDictionary *);
@property(nonatomic) BOOL completed;
@end
@implementation PorticoTrustProofDelegate
- (void)URLSession:(NSURLSession *)session didReceiveChallenge:(NSURLAuthenticationChallenge *)challenge completionHandler:(void (^)(NSURLSessionAuthChallengeDisposition, NSURLCredential *))completionHandler {
  NSURLProtectionSpace *space = challenge.protectionSpace;
  SecTrustRef trust = space.serverTrust;
  NSString *actualLeaf = trust ? PorticoTrustFingerprint(trust) : nil;
  if ([space.authenticationMethod isEqualToString:NSURLAuthenticationMethodServerTrust] && trust &&
      challenge.previousFailureCount == 0 && [space.protocol.lowercaseString isEqualToString:@"https"] &&
      [PorticoTrustKey(space.host, space.port) isEqualToString:self.key] &&
      [actualLeaf isEqualToString:self.fingerprint]) {
    // The result fact comes from this session's actual challenge, never its input fingerprint.
    self.tlsLeafFingerprint = actualLeaf;
    completionHandler(NSURLSessionAuthChallengeUseCredential, [NSURLCredential credentialForTrust:trust]);
    return;
  }
  completionHandler(NSURLSessionAuthChallengeCancelAuthenticationChallenge, nil);
}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task didReceiveChallenge:(NSURLAuthenticationChallenge *)challenge completionHandler:(void (^)(NSURLSessionAuthChallengeDisposition, NSURLCredential *))completionHandler {
  [self URLSession:session didReceiveChallenge:challenge completionHandler:completionHandler];
}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task willPerformHTTPRedirection:(NSHTTPURLResponse *)response newRequest:(NSURLRequest *)request completionHandler:(void (^)(NSURLRequest *))completionHandler {
  completionHandler(nil);
}
- (void)finish:(NSURLSession *)session result:(NSDictionary *)result {
  if (self.completed) return;
  self.completed = YES;
  void (^completion)(NSDictionary *) = self.completion;
  self.completion = nil;
  [session finishTasksAndInvalidate];
  if (completion) completion(result);
}
- (void)URLSession:(NSURLSession *)session dataTask:(NSURLSessionDataTask *)task didReceiveResponse:(NSURLResponse *)raw completionHandler:(void (^)(NSURLSessionResponseDisposition))completionHandler {
  NSHTTPURLResponse *response = [raw isKindOfClass:NSHTTPURLResponse.class] ? (NSHTTPURLResponse *)raw : nil;
  if (!response || ![response.URL isEqual:self.url] || (response.statusCode >= 300 && response.statusCode < 400) || response.expectedContentLength > 8192) {
    completionHandler(NSURLSessionResponseCancel);[self finish:session result:nil];return;
  }
  self.response = response;self.data = [NSMutableData data];
  completionHandler(NSURLSessionResponseAllow);
}
- (void)URLSession:(NSURLSession *)session dataTask:(NSURLSessionDataTask *)task didReceiveData:(NSData *)data {
  if (self.completed) return;
  // Enforce the limit while receiving, including chunked/unknown-length bodies. A completion
  // handler's NSData would already have accumulated an unbounded response before checking it.
  if (!self.response || data.length > 8192 - self.data.length) {
    [task cancel];[self finish:session result:nil];return;
  }
  [self.data appendData:data];
}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task didCompleteWithError:(NSError *)error {
  if (self.completed) return;
  NSString *text = !error && self.response && self.tlsLeafFingerprint.length ? [[NSString alloc] initWithData:self.data encoding:NSUTF8StringEncoding] : nil;
  if (!text) {[self finish:session result:nil];return;}
  NSMutableDictionary *headers = [NSMutableDictionary dictionary];
  for (id name in self.response.allHeaderFields) headers[[name description]] = [self.response.allHeaderFields[name] description];
  [self finish:session result:@{@"status": @(self.response.statusCode), @"headers": headers, @"body": text, @"tlsLeafFingerprint": self.tlsLeafFingerprint}];
}
@end

@interface PorticoTrustLoaderDelegate : NSObject <AVAssetResourceLoaderDelegate> @end
@implementation PorticoTrustLoaderDelegate
// AVFoundation asks here when a media request meets a certificate the system does not trust.
- (BOOL)resourceLoader:(AVAssetResourceLoader *)resourceLoader shouldWaitForResponseToAuthenticationChallenge:(NSURLAuthenticationChallenge *)challenge {
  if (![challenge.protectionSpace.authenticationMethod isEqualToString:NSURLAuthenticationMethodServerTrust]) return NO;
  __block BOOL answered = NO;
  [PorticoServerTrust handleChallenge:challenge completion:^(NSURLSessionAuthChallengeDisposition disposition, NSURLCredential *credential) {
    if (disposition == NSURLSessionAuthChallengeUseCredential && credential) {
      [challenge.sender useCredential:credential forAuthenticationChallenge:challenge];
      answered = YES;
    }
  }];
  return answered;
}
@end

@implementation PorticoServerTrust

+ (void)handleChallenge:(NSURLAuthenticationChallenge *)challenge completion:(void (^)(NSURLSessionAuthChallengeDisposition, NSURLCredential *))completion {
  NSURLProtectionSpace *space = challenge.protectionSpace;
  SecTrustRef trust = space.serverTrust;
  if (![space.authenticationMethod isEqualToString:NSURLAuthenticationMethodServerTrust] || !trust) { completion(NSURLSessionAuthChallengePerformDefaultHandling, nil); return; }
  NSString *key = PorticoTrustKey(space.host, space.port);
  // Only genuine system/name trust clears this observation. A saved Portico pin is distinct.
  if (SecTrustEvaluateWithError(trust, NULL)) {
    @synchronized(self) { if (key) [PorticoTrustRefused removeObjectForKey:key]; }
    completion(NSURLSessionAuthChallengePerformDefaultHandling, nil); return;
  }
  NSString *fingerprint = PorticoTrustFingerprint(trust);
  if (key && fingerprint && !PorticoTrustIsAccount(key)) {
    @synchronized(self) {
      if (!PorticoTrustRefused) PorticoTrustRefused = [NSMutableDictionary dictionary];
      if (PorticoTrustRefused.count >= PorticoTrustMaxRefused && !PorticoTrustRefused[key]) [PorticoTrustRefused removeAllObjects];
      PorticoTrustRefused[key] = fingerprint;
    }
    NSString *pinned = [self pinnedForKey:key][@"fingerprint"];
    if ([pinned isEqualToString:fingerprint]) { completion(NSURLSessionAuthChallengeUseCredential, [NSURLCredential credentialForTrust:trust]); return; }
  }
  completion(NSURLSessionAuthChallengePerformDefaultHandling, nil);
}

+ (NSURLSession *)session {
  static NSURLSession *session; static dispatch_once_t once;
  dispatch_once(&once, ^{
    NSURLSessionConfiguration *configuration = NSURLSessionConfiguration.ephemeralSessionConfiguration;
    configuration.HTTPCookieStorage = nil; configuration.HTTPShouldSetCookies = NO; configuration.URLCache = nil; configuration.URLCredentialStorage = nil;
    session = [NSURLSession sessionWithConfiguration:configuration delegate:[PorticoTrustSessionDelegate new] delegateQueue:nil];
  });
  return session;
}

+ (id<AVAssetResourceLoaderDelegate>)resourceLoaderDelegate {
  static PorticoTrustLoaderDelegate *delegate; static dispatch_once_t once;
  dispatch_once(&once, ^{ delegate = [PorticoTrustLoaderDelegate new]; });
  return delegate;
}

+ (dispatch_queue_t)resourceLoaderQueue {
  static dispatch_queue_t queue; static dispatch_once_t once;
  dispatch_once(&once, ^{ queue = dispatch_queue_create("tv.getportico.media-trust", DISPATCH_QUEUE_SERIAL); });
  return queue;
}

+ (BOOL)isRefusal:(NSError *)error {
  if (![error.domain isEqualToString:NSURLErrorDomain]) return NO;
  switch (error.code) {
    case NSURLErrorServerCertificateUntrusted: case NSURLErrorServerCertificateHasUnknownRoot:
    case NSURLErrorServerCertificateHasBadDate: case NSURLErrorServerCertificateNotYetValid:
    case NSURLErrorSecureConnectionFailed: return YES;
    default: return NO;
  }
}

+ (NSString *)keyForOrigin:(NSString *)origin {
  if (![origin isKindOfClass:NSString.class]) return nil;
  NSURLComponents *url = [NSURLComponents componentsWithString:origin];
  if (![url.scheme.lowercaseString isEqualToString:@"https"] || !url.host.length) return nil;
  return PorticoTrustKey(url.host, url.port ? url.port.integerValue : 443);
}

+ (NSString *)refusedAt:(NSString *)origin {
  NSString *key = [self keyForOrigin:origin];
  if (!key) return nil;
  @synchronized(self) { return PorticoTrustRefused[key]; }
}

+ (NSDictionary *)pinnedForKey:(NSString *)key {
  if (PorticoTrustIsAccount(key)) return nil;
  NSString *line = [[PorticoTrustDefaults() dictionaryForKey:PorticoTrustRecord][key] isKindOfClass:NSString.class] ? [PorticoTrustDefaults() dictionaryForKey:PorticoTrustRecord][key] : nil;
  NSArray<NSString *> *parts = [line componentsSeparatedByString:@" "];
  if (parts.count != 2 || !PorticoTrustText(parts[0], @"^[A-Za-z0-9_-]{43}$")) return nil;
  return @{@"fingerprint": parts[0], @"serverId": parts[1]};
}

+ (NSDictionary *)pinnedAt:(NSString *)origin {
  NSString *key = [self keyForOrigin:origin];
  return key ? [self pinnedForKey:key] : nil;
}

+ (BOOL)pin:(NSString *)origin fingerprint:(NSString *)fingerprint serverId:(NSString *)serverId {
  NSString *key = [self keyForOrigin:origin];
  if (!key || PorticoTrustIsAccount(key) || !PorticoTrustText(fingerprint, @"^[A-Za-z0-9_-]{43}$") || ![serverId isKindOfClass:NSString.class] || (serverId.length && !PorticoTrustText(serverId, @"^srv_[A-Za-z0-9_-]{43}$"))) return NO;
  @synchronized(self) {
    NSUserDefaults *defaults = PorticoTrustDefaults();
    NSMutableDictionary *pins = [[defaults dictionaryForKey:PorticoTrustRecord] mutableCopy] ?: [NSMutableDictionary dictionary];
    if (!pins[key] && pins.count >= PorticoTrustMaxPins) return NO;
    pins[key] = [NSString stringWithFormat:@"%@ %@", fingerprint, serverId];
    [defaults setObject:pins forKey:PorticoTrustRecord];
  }
  return YES;
}

+ (NSArray<NSDictionary<NSString *, NSString *> *> *)allPinned {
  NSDictionary *pins = [PorticoTrustDefaults() dictionaryForKey:PorticoTrustRecord];
  NSMutableArray<NSDictionary<NSString *, NSString *> *> *out = [NSMutableArray array];
  NSArray *keys = [[pins.allKeys filteredArrayUsingPredicate:[NSPredicate predicateWithBlock:^BOOL(id key, NSDictionary *bindings) { return [key isKindOfClass:NSString.class]; }]] sortedArrayUsingSelector:@selector(compare:)];
  for (NSString *key in keys) {
    NSDictionary *pin = [self pinnedForKey:key];
    if (pin) [out addObject:@{@"address": key, @"fingerprint": pin[@"fingerprint"], @"serverId": pin[@"serverId"]}];
  }
  return out;
}

+ (void)forget:(NSString *)origin {
  NSString *key = [self keyForOrigin:origin];
  if (!key) return;
  @synchronized(self) {
    NSUserDefaults *defaults = PorticoTrustDefaults();
    NSMutableDictionary *pins = [[defaults dictionaryForKey:PorticoTrustRecord] mutableCopy];
    if (!pins[key]) return;
    [pins removeObjectForKey:key];
    [defaults setObject:pins forKey:PorticoTrustRecord];
  }
}

+ (BOOL)forgetCaptured:(NSString *)origin fingerprint:(NSString *)fingerprint serverId:(NSString *)serverId {
  NSString *key = [self keyForOrigin:origin];
  if (!key || PorticoTrustIsAccount(key) || !PorticoTrustText(fingerprint, @"^[A-Za-z0-9_-]{43}$") || !PorticoTrustText(serverId, @"^srv_[A-Za-z0-9_-]{43}$")) return NO;
  @synchronized(self) {
    NSDictionary *current = [self pinnedForKey:key];
    if (![current[@"fingerprint"] isEqualToString:fingerprint] || ![current[@"serverId"] isEqualToString:serverId]) return NO;
    NSUserDefaults *defaults = PorticoTrustDefaults();
    NSMutableDictionary *pins = [[defaults dictionaryForKey:PorticoTrustRecord] mutableCopy];
    [pins removeObjectForKey:key];
    [defaults setObject:pins forKey:PorticoTrustRecord];
    [PorticoTrustRefused removeObjectForKey:key];
    return YES;
  }
}

+ (BOOL)forgetServerCertificates:(NSString *)serverId origins:(NSArray<NSString *> *)origins {
  if (!PorticoTrustText(serverId, @"^srv_[A-Za-z0-9_-]{43}$") || ![origins isKindOfClass:NSArray.class] || origins.count > 128) return NO;
  NSMutableSet<NSString *> *keys = [NSMutableSet set];
  // Validate the entire set before changing anything. HTTP origins have no TLS pin, but can
  // share an explicit host/port with a stored route, so their named authority is also removed.
  for (id raw in origins) {
    if (![raw isKindOfClass:NSString.class] || [raw length] > 2048 || [raw rangeOfCharacterFromSet:NSCharacterSet.whitespaceAndNewlineCharacterSet].location != NSNotFound || [raw containsString:@"\\"]) return NO;
    NSURLComponents *origin = [NSURLComponents componentsWithString:raw];
    BOOL tls = [origin.scheme isEqualToString:@"https"], plain = [origin.scheme isEqualToString:@"http"];
    if ((!tls && !plain) || !origin.host.length || origin.user || origin.password || origin.percentEncodedPath.length || origin.query || origin.fragment || [origin.host containsString:@"%"] || !origin.URL) return NO;
    NSString *key = PorticoTrustKey(origin.host, origin.port ? origin.port.integerValue : (tls ? 443 : 80));
    if (!key) return NO;
    [keys addObject:key];
  }
  @synchronized(self) {
    NSUserDefaults *defaults = PorticoTrustDefaults();
    NSMutableDictionary *pins = [[defaults dictionaryForKey:PorticoTrustRecord] mutableCopy] ?: [NSMutableDictionary dictionary];
    for (id key in pins.allKeys) {
      id value = pins[key];
      NSArray *parts = [value isKindOfClass:NSString.class] ? [value componentsSeparatedByString:@" "] : nil;
      if ([keys containsObject:key] || (parts.count == 2 && [parts[1] isEqualToString:serverId])) [pins removeObjectForKey:key];
    }
    // One dictionary replacement keeps the ID/origin deletion indivisible for readers. Do not
    // resolve the JS promise until the app-group persistent domain acknowledges the write.
    [defaults setObject:pins forKey:PorticoTrustRecord];
    if (![defaults synchronize]) return NO;
    return [[defaults dictionaryForKey:PorticoTrustRecord] isEqualToDictionary:pins];
  }
}

+ (void)prove:(NSString *)origin fingerprint:(NSString *)fingerprint body:(NSString *)body completion:(void (^)(NSDictionary *))completion {
  NSURLComponents *parts = [origin isKindOfClass:NSString.class] ? [NSURLComponents componentsWithString:origin] : nil;
  NSMutableCharacterSet *invalid = [NSCharacterSet.whitespaceAndNewlineCharacterSet mutableCopy];
  [invalid formUnionWithCharacterSet:NSCharacterSet.controlCharacterSet];
  [invalid addCharactersInString:@"\\"];
  NSString *key = [self keyForOrigin:origin];
  if (!key || PorticoTrustIsAccount(key) || origin.length > 2048 ||
      [origin rangeOfCharacterFromSet:invalid].location != NSNotFound ||
      ![parts.scheme isEqualToString:@"https"] || parts.user || parts.password ||
      parts.percentEncodedPath.length || parts.query || parts.fragment ||
      [parts.host containsString:@"%"] || !parts.URL ||
      !PorticoTrustText(fingerprint, @"^[A-Za-z0-9_-]{43}$") || ![[self refusedAt:origin] isEqualToString:fingerprint] ||
      ![body isKindOfClass:NSString.class] || [body lengthOfBytesUsingEncoding:NSUTF8StringEncoding] > 4096) { completion(nil); return; }
  parts.path = @"/v1/networking/identity-proof";
  NSURL *url = parts.URL;
  if (!url) { completion(nil); return; }
  PorticoTrustProofDelegate *delegate = [PorticoTrustProofDelegate new];
  delegate.key = key; delegate.fingerprint = fingerprint;delegate.url = url;delegate.completion = completion;
  NSURLSessionConfiguration *configuration = NSURLSessionConfiguration.ephemeralSessionConfiguration;
  configuration.HTTPCookieStorage = nil; configuration.HTTPShouldSetCookies = NO; configuration.URLCache = nil; configuration.URLCredentialStorage = nil;
  configuration.timeoutIntervalForRequest = 5; configuration.timeoutIntervalForResource = 5;
  NSURLSession *session = [NSURLSession sessionWithConfiguration:configuration delegate:delegate delegateQueue:nil];
  NSMutableURLRequest *request = [NSMutableURLRequest requestWithURL:url];
  request.HTTPMethod = @"POST"; request.HTTPBody = [body dataUsingEncoding:NSUTF8StringEncoding]; request.HTTPShouldHandleCookies = NO;
  [request setValue:@"application/json" forHTTPHeaderField:@"Content-Type"]; [request setValue:@"application/json; portico-route-proof=2" forHTTPHeaderField:@"Accept"];
  [[session dataTaskWithRequest:request] resume];
}

@end

/// React Native's own HTTP handler (images, and any request made through `fetch`) has no answer
/// for a trust challenge of its own; it gets this one, so a poster loads from a server whose
/// certificate the app pinned exactly as an API request does.
@interface RCTHTTPRequestHandler (PorticoServerTrust) <NSURLSessionDelegate> @end
@implementation RCTHTTPRequestHandler (PorticoServerTrust)
- (void)URLSession:(NSURLSession *)session didReceiveChallenge:(NSURLAuthenticationChallenge *)challenge completionHandler:(void (^)(NSURLSessionAuthChallengeDisposition, NSURLCredential *))completionHandler {
  [PorticoServerTrust handleChallenge:challenge completion:completionHandler];
}
@end
