#import "PorticoInstallationStorage.h"
#import "PorticoBootCover.h"
#import <sys/sysctl.h>
#import <React/RCTViewManager.h>
#import <React/RCTBridge.h>
#import <React/RCTBridgeModule.h>
#import <React/UIView+React.h>
#import <AVFoundation/AVFoundation.h>
#import <AVKit/AVKit.h>
#import <MediaAccessibility/MediaAccessibility.h>
#import <CoreText/CoreText.h>
#import <QuartzCore/QuartzCore.h>
#import <Security/Security.h>
#import "PorticoPlaybackController.h"
#import "PorticoSubtitleSelection.h"
#import "PorticoSubtitleOverlay.h"
#import "PorticoSecureStorageRead.h"
#import "PorticoProfileTrust.h"
#import "PorticoServerTrust.h"
#import "PorticoVideoSnapshot.h"
#import "PorticoQueueTransport.h"
#import "PorticoQueuePlaybackStore.h"

// Pull-based event responses share one bounded lifetime and never follow redirects.
@interface PorticoEventStream : NSObject<NSURLSessionDataDelegate>
@property(nonatomic,strong) NSURLSession *session;
@property(nonatomic,strong) NSURLSessionDataTask *task;
@property(nonatomic,strong) NSMutableData *bytes;
@property(nonatomic,weak) id owner;
@property(nonatomic,copy) RCTPromiseResolveBlock opening;
@property(nonatomic,copy) RCTPromiseRejectBlock openingReject;
@property(nonatomic,copy) RCTPromiseResolveBlock reading;
@property(nonatomic,copy) RCTPromiseRejectBlock readingReject;
@property(nonatomic) NSUInteger received;
@property(nonatomic) NSTimeInterval startedAt;
@property(nonatomic) BOOL ended;
@property(nonatomic) BOOL closed;
@property(nonatomic) BOOL failed;
@property(nonatomic,copy) NSString *failureCode;
- (void)cancel;
- (void)deliver;
@end
static NSMutableDictionary<NSString *,PorticoEventStream *> *PorticoEventStreams;
@implementation PorticoEventStream
- (void)deliver {
 if(!self.reading)return;
 if(self.failed){self.readingReject(self.failureCode?:@"event_transport",@"The event connection was interrupted.",nil);}
 else if(self.bytes.length){NSUInteger count=MIN((NSUInteger)16384,self.bytes.length);NSData *chunk=[self.bytes subdataWithRange:NSMakeRange(0,count)];[self.bytes replaceBytesInRange:NSMakeRange(0,count) withBytes:NULL length:0];self.reading(@{@"done":@NO,@"data":[chunk base64EncodedStringWithOptions:0]});}
 else if(self.ended||self.closed){self.reading(@{@"done":@YES});}
 else return;
 self.reading=nil;self.readingReject=nil;
}
- (void)cancel {
 @synchronized(self){
  if(self.closed)return;self.closed=YES;self.ended=YES;
  [self.task cancel];[self.session invalidateAndCancel];self.bytes=nil;
  if(self.openingReject)self.openingReject(self.failureCode?:@"event_cancelled",@"The event stream was cancelled.",nil);
  self.opening=nil;self.openingReject=nil;[self deliver];
 }
}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task willPerformHTTPRedirection:(NSHTTPURLResponse *)response newRequest:(NSURLRequest *)request completionHandler:(void (^)(NSURLRequest *))completionHandler {
 completionHandler(nil);@synchronized(self){self.failed=YES;self.failureCode=@"event_redirect";[self cancel];}
}
- (void)URLSession:(NSURLSession *)session didReceiveChallenge:(NSURLAuthenticationChallenge *)challenge completionHandler:(void (^)(NSURLSessionAuthChallengeDisposition, NSURLCredential *))completionHandler {
 [PorticoServerTrust handleChallenge:challenge completion:completionHandler];
}
- (void)URLSession:(NSURLSession *)session dataTask:(NSURLSessionDataTask *)task didReceiveResponse:(NSURLResponse *)response completionHandler:(void (^)(NSURLSessionResponseDisposition))completionHandler {
 @synchronized(self){
  if(self.closed||![response isKindOfClass:NSHTTPURLResponse.class]){completionHandler(NSURLSessionResponseCancel);[self cancel];return;}
  NSHTTPURLResponse *http=(id)response;
  if(http.statusCode>=300&&http.statusCode<400){completionHandler(NSURLSessionResponseCancel);self.failed=YES;self.failureCode=@"event_redirect";[self cancel];return;}
  NSMutableDictionary *headers=[NSMutableDictionary dictionary];for(id key in http.allHeaderFields)headers[[key description]]=[http.allHeaderFields[key] description];
  if(self.opening)self.opening(@{@"status":@(http.statusCode),@"headers":headers});
  self.opening=nil;self.openingReject=nil;completionHandler(NSURLSessionResponseAllow);
 }
}
- (void)URLSession:(NSURLSession *)session dataTask:(NSURLSessionDataTask *)task didReceiveData:(NSData *)data {
 @synchronized(self){
  if(self.closed)return;
  if(data.length>1048576-self.received){[self cancel];return;}
  self.received+=data.length;[self.bytes appendData:data];[self deliver];
 }
}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task didCompleteWithError:(NSError *)error {
 @synchronized(self){
  if(self.closed)return;self.ended=YES;
  BOOL expired=[error.domain isEqual:NSURLErrorDomain]&&error.code==NSURLErrorTimedOut&&NSProcessInfo.processInfo.systemUptime-self.startedAt>=2099;
  self.failed=error!=nil&&!expired;
  // A time-out before the response has begun is a route that does not answer: route evidence, as
  // it is for an ordinary request. Only an open stream that goes quiet is an idle time-out.
  self.failureCode=[error.domain isEqual:NSURLErrorDomain]&&error.code==NSURLErrorTimedOut?(self.openingReject?@"event_transport":@"event_timeout"):([error.domain isEqual:NSURLErrorDomain]&&error.code==NSURLErrorCancelled?@"event_cancelled":@"event_transport");
  if(self.openingReject)self.openingReject(self.failureCode,@"The event connection could not be opened.",nil);
  self.opening=nil;self.openingReject=nil;[self deliver];[self.session finishTasksAndInvalidate];
 }
}
@end

// Keychain and PIN derivation never hold up API request submission. One queue also keeps
// storage operations ordered across React bridge replacements.
static dispatch_queue_t PorticoStorageQueue(void) {
 static dispatch_queue_t queue;static dispatch_once_t once;
 dispatch_once(&once,^{queue=dispatch_queue_create("tv.getportico.storage",DISPATCH_QUEUE_SERIAL);});
 return queue;
}

static OSStatus PorticoPrepareInstallationStorage(void) {
 BOOL available=YES;
#if !TARGET_OS_TV
 __block BOOL unlocked=NO;
 if(NSThread.isMainThread)unlocked=UIApplication.sharedApplication.isProtectedDataAvailable;
 else dispatch_sync(dispatch_get_main_queue(),^{unlocked=UIApplication.sharedApplication.isProtectedDataAvailable;});
 available=unlocked;
#endif
 return PorticoPrepareInstallation(NSUserDefaults.standardUserDefaults, available, ^OSStatus(NSString *service){
  return SecItemDelete((__bridge CFDictionaryRef)@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,(__bridge id)kSecAttrService:service});
 });
}

// HttpLocalApi's avatar upload is JSON only across the JS/native bridge. Decode at this
// fixed endpoint so the existing redirect-refusing transport sends the original image bytes.
static BOOL PorticoAvatarUploadPath(NSString *path, NSString *method) {
 return [method isEqualToString:@"POST"]&&[path rangeOfString:@"^/v1/(direct/profiles/[A-Za-z0-9_-]{1,128}|admin/access/members/[A-Za-z0-9_-]{1,128}/profiles/[A-Za-z0-9_-]{1,128})/avatar$" options:NSRegularExpressionSearch].location!=NSNotFound;
}
static NSData *PorticoAvatarUploadBody(NSString *body) {
 if(![body isKindOfClass:NSString.class]||body.length>5592448)return nil;
 id envelope=[NSJSONSerialization JSONObjectWithData:[body dataUsingEncoding:NSUTF8StringEncoding] options:0 error:nil];
 if(![envelope isKindOfClass:NSDictionary.class]||[envelope count]!=1||![envelope[@"nativeAvatarBase64"] isKindOfClass:NSString.class])return nil;
 NSData *data=[[NSData alloc]initWithBase64EncodedString:envelope[@"nativeAvatarBase64"] options:0];
 return data.length>0&&data.length<=4194304?data:nil;
}
// The upload bridge carries only staged file references. Read and bound bytes natively.
static NSURL *PorticoBoundedTarget(NSString *url,NSString *method,BOOL fullAPI) {
 if(![url isKindOfClass:NSString.class]||![method isKindOfClass:NSString.class]||url.length>8192)return nil;
 NSURL *target=[NSURL URLWithString:url];
 if(!target||![@[@"http",@"https"] containsObject:target.scheme]||target.user.length||target.password.length||target.fragment.length||
    (fullAPI?(![target.path hasPrefix:@"/v1/"]&&![target.path hasPrefix:@"/v2/"]):(![target.path hasPrefix:@"/v2/"]&&[target.path rangeOfString:@"^/v1/items/[A-Za-z0-9_-]+/playback/[A-Za-z0-9_-]+/subtitles$" options:NSRegularExpressionSearch].location==NSNotFound))||
    ![(fullAPI?@[@"GET",@"POST",@"PUT",@"PATCH",@"DELETE",@"HEAD"]:@[@"GET",@"POST",@"PUT"]) containsObject:method])return nil;
 return target;
}
static NSString *PorticoSubtitleUploadName(NSString *name) {
 if(![name isKindOfClass:NSString.class]||!name.length||name.length>1024)return nil;
 NSData *bytes=[name dataUsingEncoding:NSUTF8StringEncoding];if(!bytes||bytes.length>1024)return nil;
 NSMutableCharacterSet *unsafe=[NSCharacterSet.controlCharacterSet mutableCopy];[unsafe addCharactersInString:@"\"\\"];
 return [[name componentsSeparatedByCharactersInSet:unsafe] componentsJoinedByString:@"_"];
}
static NSURL *PorticoSubtitleUploadFile(NSString *uri) {
 NSURL *url=[uri isKindOfClass:NSString.class]&&[uri hasPrefix:@"file://"]?[NSURL URLWithString:uri]:nil;
 if(!url.isFileURL||url.host.length||url.query||url.fragment)return nil;
 NSURL *resolved=url.URLByResolvingSymlinksInPath.URLByStandardizingPath;
 NSString *root=[[[NSURL fileURLWithPath:NSTemporaryDirectory() isDirectory:YES] URLByAppendingPathComponent:@"portico-subtitle-uploads" isDirectory:YES] URLByResolvingSymlinksInPath].URLByStandardizingPath.path;
 return [resolved.path hasPrefix:[root stringByAppendingString:@"/"]]?resolved:nil;
}
static void PorticoSubtitleAppendText(NSMutableData *body,NSString *text) {[body appendData:[text dataUsingEncoding:NSUTF8StringEncoding]];}
static BOOL PorticoSubtitleAppendFile(NSMutableData *body,NSURL *url,NSString *name,NSString *field,NSString *boundary,NSUInteger *total,NSProgress *progress) {
 if(![PorticoSubtitleUploadFile(url.absoluteString) isEqual:url])return NO;
 NSDictionary *attributes=[NSFileManager.defaultManager attributesOfItemAtPath:url.path error:nil];
 unsigned long long size=[attributes[NSFileSize] unsignedLongLongValue];
 if(![attributes[NSFileType] isEqual:NSFileTypeRegular]||!size||size>33554432-*total||progress.cancelled)return NO;
 PorticoSubtitleAppendText(body,[NSString stringWithFormat:@"--%@\r\nContent-Disposition: form-data; name=\"%@\"; filename=\"%@\"\r\nContent-Type: application/octet-stream\r\n\r\n",boundary,field,name]);
 NSError *error=nil;NSFileHandle *file=[NSFileHandle fileHandleForReadingFromURL:url error:&error];if(!file)return NO;
 NSUInteger count=0;
 @try {
  while(!progress.cancelled){@autoreleasepool {
   NSData *chunk=[file readDataUpToLength:MIN((NSUInteger)65536,33554432-*total+1) error:&error];
   if(!chunk||error)return NO;if(!chunk.length)break;
   if(chunk.length>33554432-*total)return NO;
   *total+=chunk.length;count+=chunk.length;[body appendData:chunk];
  }}
 } @finally {[file closeAndReturnError:nil];}
 if(progress.cancelled||count!=size)return NO;
 PorticoSubtitleAppendText(body,@"\r\n");return YES;
}
@interface PorticoSubtitleUploadPreparation : NSObject
@property(nonatomic,strong) NSProgress *progress;
@property(nonatomic,copy) RCTPromiseRejectBlock reject;
@end
@implementation PorticoSubtitleUploadPreparation @end

static UIView *PorticoBootCoverView;
static BOOL PorticoBootHandedOver;
static double PorticoBootCoverInstalledAt;

void PorticoBootCoverInstall(UIWindow *window){
 PorticoBootCoverInstalledAt=NSDate.date.timeIntervalSince1970*1000;
 if(PorticoBootHandedOver||PorticoBootCoverView)return;
 UIView *cover=[[UIStoryboard storyboardWithName:@"LaunchScreen" bundle:nil] instantiateInitialViewController].view;
 if(!cover)return;
 cover.frame=window.bounds;
 cover.autoresizingMask=UIViewAutoresizingFlexibleWidth|UIViewAutoresizingFlexibleHeight;
 // Never a touch target or a focus stop; VoiceOver reads it as the app's name.
 cover.userInteractionEnabled=NO;
 cover.isAccessibilityElement=YES;
 cover.accessibilityLabel=@"Portico";
 [window addSubview:cover];
 PorticoBootCoverView=cover;
}

/** Boot's t = 0: the process start, or the window's creation when iOS prewarmed the process. */
static double PorticoLaunchStartedAt(void){
 if([NSProcessInfo.processInfo.environment[@"ActivePrewarm"] isEqualToString:@"1"]&&PorticoBootCoverInstalledAt>0)return PorticoBootCoverInstalledAt;
 struct kinfo_proc info;size_t size=sizeof(info);int mib[4]={CTL_KERN,KERN_PROC,KERN_PROC_PID,getpid()};
 if(sysctl(mib,4,&info,&size,NULL,0)==0&&size>0){struct timeval start=info.kp_proc.p_starttime;return start.tv_sec*1000.0+start.tv_usec/1000.0;}
 return PorticoBootCoverInstalledAt>0?PorticoBootCoverInstalledAt:NSDate.date.timeIntervalSince1970*1000;
}

@interface PorticoRuntime : NSObject<RCTBridgeModule,RCTInvalidating> @end
@implementation PorticoRuntime {
 NSMutableDictionary<NSString *,PorticoSubtitleUploadPreparation *> *_subtitleUploads;
 BOOL _invalidated;
}
RCT_EXPORT_MODULE()
/** A JavaScript reload abandons its promises; release their transport slots at once instead of
 * leaving them to time out while the new JavaScript's first requests wait behind them. */
- (void)invalidate {
 @synchronized(self){_invalidated=YES;for(PorticoSubtitleUploadPreparation *pending in _subtitleUploads.allValues){[pending.progress cancel];pending.reject(@"cancelled",@"The app reloaded.",nil);}[_subtitleUploads removeAllObjects];}
 [PorticoQueueTransport cancelOwnedBy:self];
 NSArray<NSString *> *identifiers;@synchronized(PorticoEventStream.class){identifiers=[PorticoEventStreams allKeys];}
 for(NSString *identifier in identifiers){PorticoEventStream *stream;@synchronized(PorticoEventStream.class){stream=PorticoEventStreams[identifier];if(stream.owner!=self&&stream.owner!=nil)continue;[PorticoEventStreams removeObjectForKey:identifier];}[stream cancel];}
}
+ (BOOL)requiresMainQueueSetup{return NO;}
/** React has drawn its first frame: the launch cover goes, without animation. */
RCT_EXPORT_METHOD(bootDrawn){
 dispatch_async(dispatch_get_main_queue(), ^{PorticoBootHandedOver=YES;[PorticoBootCoverView removeFromSuperview];PorticoBootCoverView=nil;});
}
RCT_REMAP_METHOD(prepareSecureRecords,prepareSecureRecordsWithResolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 dispatch_async(PorticoStorageQueue(), ^{
 if(PorticoPrepareInstallationStorage()!=errSecSuccess){reject(@"secure_storage",@"This installation’s saved records could not be prepared.",nil);return;}

 // Keychain records do not depend on a separate app-held sealing key.
 resolve(@NO);

 });
}
- (NSDictionary *)constantsToExport{
 NSMutableDictionary *values=[@{@"launchId":NSUUID.UUID.UUIDString} mutableCopy];
 values[@"launchStartedAt"]=@(PorticoLaunchStartedAt());
#if !TARGET_OS_TV
 values[@"pictureInPictureSupported"]=@([AVPictureInPictureController isPictureInPictureSupported]);
#endif
 NSString *hosted=NSBundle.mainBundle.infoDictionary[@"PorticoHostedURL"];if(hosted.length && ![hosted containsString:@"$("])values[@"hostedUrl"]=hosted;
#if DEBUG
 NSString *fixture=NSProcessInfo.processInfo.environment[@"PORTICO_TEST_LOGIN_JSON"];
 values[@"testOwnership"]=@([NSProcessInfo.processInfo.environment[@"PORTICO_TEST_PLAYER_OWNERSHIP"] isEqualToString:@"1"]);
 if(fixture){id json=[NSJSONSerialization JSONObjectWithData:[fixture dataUsingEncoding:NSUTF8StringEncoding] options:0 error:nil];if([json isKindOfClass:NSDictionary.class])values[@"testLogin"]=json;}
#endif
 return values;
}
#if DEBUG
RCT_REMAP_METHOD(recordTestEvent,recordTestEvent:(NSString *)json){
 NSString *path=[NSTemporaryDirectory() stringByAppendingPathComponent:@"portico-journey.jsonl"];
 if(![NSFileManager.defaultManager fileExistsAtPath:path])[NSFileManager.defaultManager createFileAtPath:path contents:nil attributes:nil];
 NSFileHandle *file=[NSFileHandle fileHandleForWritingAtPath:path];[file seekToEndOfFile];[file writeData:[[json stringByAppendingString:@"\n"] dataUsingEncoding:NSUTF8StringEncoding]];[file closeFile];
}
#endif
RCT_REMAP_METHOD(userMutationTransaction,userMutationTransaction:(NSString *)operation json:(NSString *)json resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 dispatch_async(PorticoStorageQueue(), ^{
  if(PorticoPrepareInstallationStorage()!=errSecSuccess){reject(@"mutation_store_unavailable",@"User changes could not be saved.",nil);return;}
  NSDictionary *payload=[NSJSONSerialization JSONObjectWithData:[json dataUsingEncoding:NSUTF8StringEncoding] options:0 error:nil];NSError *error=nil;
  id result=[PorticoQueuePlaybackStore userMutationTransaction:operation payload:payload error:&error];
  if(result)resolve([result isKindOfClass:NSDictionary.class]?[[NSString alloc]initWithData:[NSJSONSerialization dataWithJSONObject:result options:0 error:nil] encoding:NSUTF8StringEncoding]:result);
  else reject(@"mutation_store_unavailable",@"User changes could not be saved.",error);
 });
}
RCT_REMAP_METHOD(loadQueueRecord,loadQueueRecord:(NSString *)key resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 dispatch_async(PorticoStorageQueue(), ^{
 if(PorticoPrepareInstallationStorage()!=errSecSuccess){reject(@"secure_storage",@"This installation’s saved records could not be prepared.",nil);return;}
 NSDictionary *query=@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,(__bridge id)kSecAttrService:@"tv.getportico.portico.queues",(__bridge id)kSecAttrAccount:key,(__bridge id)kSecReturnData:@YES};CFTypeRef result=NULL;OSStatus status=SecItemCopyMatching((__bridge CFDictionaryRef)query,&result);
 id stored=CFBridgingRelease(result);
 // The Keychain returned the item, but its bytes are not text: not a record this app wrote, and
 // waiting will not mend it. That is `secure_record_damaged` (the Android runtime's code too).
 // Every Keychain failure, a locked device before first unlock included, stays `secure_storage`.
 if(status==errSecSuccess&&[stored isKindOfClass:NSData.class]&&![[NSString alloc] initWithData:stored encoding:NSUTF8StringEncoding]){reject(@"secure_record_damaged",@"Queue recovery could not be read.",nil);return;}
 PorticoResolveSecureStorageRead(status,stored,NSNull.null,@"Queue recovery could not be read.",resolve,reject);
 });
}
RCT_REMAP_METHOD(saveQueueRecord,saveQueueRecord:(NSString *)key json:(NSString *)json resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 dispatch_async(PorticoStorageQueue(), ^{
 if(PorticoPrepareInstallationStorage()!=errSecSuccess){reject(@"secure_storage",@"This installation’s saved records could not be prepared.",nil);return;}
 NSData *data=[json dataUsingEncoding:NSUTF8StringEncoding];if(!data||data.length>262144){reject(@"queue_journal_size",@"Queue recovery is full.",nil);return;}
 NSDictionary *query=@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,(__bridge id)kSecAttrService:@"tv.getportico.portico.queues",(__bridge id)kSecAttrAccount:key};NSDictionary *attributes=@{(__bridge id)kSecValueData:data,(__bridge id)kSecAttrAccessible:(__bridge id)kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly};OSStatus status=SecItemUpdate((__bridge CFDictionaryRef)query,(__bridge CFDictionaryRef)attributes);
 if(status==errSecItemNotFound){NSMutableDictionary *insert=[query mutableCopy];[insert addEntriesFromDictionary:attributes];status=SecItemAdd((__bridge CFDictionaryRef)insert,NULL);}if(status==errSecSuccess)resolve(@YES);else reject(@"secure_storage",@"Queue recovery could not be saved.",nil);
 });
}
RCT_REMAP_METHOD(queueRequest,queueRequest:(NSString *)identifier url:(NSString *)url method:(NSString *)method headers:(NSDictionary *)headers body:(NSString *)body resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 [self boundedRequest:identifier url:url method:method headers:headers body:body fullAPI:NO resolver:resolve rejecter:reject];
}
RCT_REMAP_METHOD(openNetworkStream,openNetworkStream:(NSString *)identifier url:(NSString *)url headers:(NSDictionary *)headers resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 if(![url isKindOfClass:NSString.class]||!url.length||url.length>8192||![identifier isKindOfClass:NSString.class]||!identifier.length||identifier.length>128){reject(@"event_local",@"Invalid event request.",nil);return;}
 NSURL *target=[NSURL URLWithString:url];
 BOOL pathAllowed=[target.path isEqual:@"/v1/events"]||[target.path rangeOfString:@"^/v1/groups/[A-Za-z0-9_-]{1,128}/events$" options:NSRegularExpressionSearch].location!=NSNotFound;
 if(!target||![@[@"http",@"https"] containsObject:target.scheme]||!target.host.length||target.user||target.password||target.fragment||url.length>8192||!pathAllowed||!identifier.length||identifier.length>128){reject(@"event_local",@"Invalid event request.",nil);return;}
 @synchronized(PorticoEventStream.class){
  if(!PorticoEventStreams)PorticoEventStreams=[NSMutableDictionary dictionary];
  if(PorticoEventStreams.count>=4||PorticoEventStreams[identifier]){reject(@"event_local",@"Too many event streams.",nil);return;}
  PorticoEventStream *stream=[PorticoEventStream new];stream.owner=self;stream.startedAt=NSProcessInfo.processInfo.systemUptime;stream.bytes=[NSMutableData data];stream.opening=resolve;stream.openingReject=reject;
  NSURLSessionConfiguration *configuration=NSURLSessionConfiguration.ephemeralSessionConfiguration;
  configuration.timeoutIntervalForRequest=60;configuration.timeoutIntervalForResource=2100;configuration.HTTPShouldSetCookies=NO;configuration.HTTPCookieStorage=nil;configuration.URLCredentialStorage=nil;configuration.URLCache=nil;configuration.requestCachePolicy=NSURLRequestReloadIgnoringLocalCacheData;
  NSOperationQueue *queue=[NSOperationQueue new];queue.maxConcurrentOperationCount=1;
  stream.session=[NSURLSession sessionWithConfiguration:configuration delegate:stream delegateQueue:queue];
  NSMutableURLRequest *request=[NSMutableURLRequest requestWithURL:target];request.HTTPMethod=@"GET";
  for(id name in headers)if([name isKindOfClass:NSString.class]&&[headers[name] isKindOfClass:NSString.class])[request setValue:headers[name] forHTTPHeaderField:name];
  stream.task=[stream.session dataTaskWithRequest:request];PorticoEventStreams[identifier]=stream;[stream.task resume];
 }
}
RCT_REMAP_METHOD(readNetworkStream,readNetworkStream:(NSString *)identifier resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 PorticoEventStream *stream;@synchronized(PorticoEventStream.class){stream=PorticoEventStreams[identifier];}
 if(!stream){reject(@"event_local",@"The event stream is unavailable.",nil);return;}
 @synchronized(stream){
  if(stream.reading){reject(@"event_local",@"An event read is already pending.",nil);return;}
  stream.reading=resolve;stream.readingReject=reject;[stream deliver];
 }
}
RCT_REMAP_METHOD(cancelNetworkStream,cancelNetworkStream:(NSString *)identifier){
 PorticoEventStream *stream;@synchronized(PorticoEventStream.class){stream=PorticoEventStreams[identifier];[PorticoEventStreams removeObjectForKey:identifier];}
 [stream cancel];
}

RCT_REMAP_METHOD(networkRequest,networkRequest:(NSString *)identifier url:(NSString *)url method:(NSString *)method headers:(NSDictionary *)headers body:(NSString *)body resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 [self boundedRequest:identifier url:url method:method headers:headers body:body fullAPI:YES resolver:resolve rejecter:reject];
}
RCT_REMAP_METHOD(networkUploadRequest,networkUploadRequest:(NSString *)identifier url:(NSString *)url method:(NSString *)method headers:(NSDictionary *)headers metadata:(NSString *)metadata fileUri:(NSString *)fileUri fileName:(NSString *)fileName companionUri:(NSString *)companionUri companionName:(NSString *)companionName resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 NSURL *target=PorticoBoundedTarget(url,method,YES);
 BOOL allowed=([method isEqual:@"POST"]&&[target.path rangeOfString:@"^/v1/items/[A-Za-z0-9_-]+/subtitles$" options:NSRegularExpressionSearch].location!=NSNotFound)||([method isEqual:@"PUT"]&&[target.path rangeOfString:@"^/v1/items/[A-Za-z0-9_-]+/subtitles/[A-Za-z0-9_-]+$" options:NSRegularExpressionSearch].location!=NSNotFound);
 NSData *metadataBytes=[metadata isKindOfClass:NSString.class]&&metadata.length<=16384?[metadata dataUsingEncoding:NSUTF8StringEncoding]:nil;
 NSString *name=PorticoSubtitleUploadName(fileName),*otherName=PorticoSubtitleUploadName(companionName);
 NSURL *file=PorticoSubtitleUploadFile(fileUri),*other=PorticoSubtitleUploadFile(companionUri);
 BOOL hasCompanion=companionUri!=nil||companionName!=nil;
 if(!target||!target.host.length||!allowed||![identifier isKindOfClass:NSString.class]||!identifier.length||identifier.length>128||!metadataBytes||metadataBytes.length>16384||!file||!name||(hasCompanion&&(!other||!otherName))){reject(@"queue_transport",@"Invalid subtitle upload request.",nil);return;}
 PorticoSubtitleUploadPreparation *pending=[PorticoSubtitleUploadPreparation new];pending.progress=[NSProgress progressWithTotalUnitCount:1];pending.reject=reject;
 @synchronized(self){
  if(_invalidated){reject(@"cancelled",@"The app reloaded.",nil);return;}
  if(!_subtitleUploads)_subtitleUploads=[NSMutableDictionary new];
  if(_subtitleUploads[identifier]||_subtitleUploads.count>=4){reject(@"queue_transport",@"Too many subtitle uploads.",nil);return;}
  _subtitleUploads[identifier]=pending;
 }
 // Assembly is never on the main queue. Cancellation covers preparation as well as transport.
 dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0),^{@autoreleasepool {
  NSString *boundary=[@"Portico-" stringByAppendingString:NSUUID.UUID.UUIDString];NSMutableData *body=[NSMutableData new];NSUInteger total=0;
  PorticoSubtitleAppendText(body,[NSString stringWithFormat:@"--%@\r\nContent-Disposition: form-data; name=\"metadata\"\r\n\r\n",boundary]);[body appendData:metadataBytes];PorticoSubtitleAppendText(body,@"\r\n");
  BOOL valid=PorticoSubtitleAppendFile(body,file,name,@"file",boundary,&total,pending.progress)&&(!hasCompanion||PorticoSubtitleAppendFile(body,other,otherName,@"companion",boundary,&total,pending.progress));
  PorticoSubtitleAppendText(body,[NSString stringWithFormat:@"--%@--\r\n",boundary]);
  @synchronized(self){
   if(_subtitleUploads[identifier]!=pending)return;
   [_subtitleUploads removeObjectForKey:identifier];
   if(!valid){reject(@"queue_transport",@"Invalid staged subtitle file.",nil);return;}
   NSMutableDictionary *wireHeaders=[NSMutableDictionary new];
   for(id key in headers)if([key isKindOfClass:NSString.class]&&[headers[key] isKindOfClass:NSString.class]&&[key caseInsensitiveCompare:@"Content-Type"]!=NSOrderedSame&&[key caseInsensitiveCompare:@"Content-Length"]!=NSOrderedSame)wireHeaders[key]=headers[key];
   wireHeaders[@"Content-Type"]=[@"multipart/form-data; boundary=" stringByAppendingString:boundary];
   [self submitBoundedBytes:identifier target:target method:method headers:wireHeaders data:body fullAPI:YES subtitleUpload:YES resolver:resolve rejecter:reject];
  }
 }});
}
RCT_REMAP_METHOD(networkBytesRequest,networkBytesRequest:(NSString *)identifier url:(NSString *)url method:(NSString *)method headers:(NSDictionary *)headers byteLimit:(NSNumber *)byteLimit resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 PorticoQueueRequest *request=PorticoReadingBytesRequest(identifier,url,method,headers,byteLimit,self,resolve,reject);
 if(request)[PorticoQueueTransport submit:request domain:@"queue_transport" busy:@"network_bytes_busy"];
}
#if !TARGET_OS_TV
RCT_REMAP_METHOD(networkStreamOpen,networkStreamOpen:(NSString *)identifier url:(NSString *)url headers:(NSDictionary *)headers resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 PorticoQueueRequest *request=PorticoReadingStreamRequest(identifier,url,headers,self,resolve,reject);
 if(request)[PorticoQueueTransport submit:request domain:@"queue_transport" busy:@"network_stream_busy"];
}
RCT_REMAP_METHOD(networkStreamRead,networkStreamRead:(NSString *)identifier resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 PorticoReadingStreamRead(identifier,self,resolve,reject);
}
#endif
- (void)boundedRequest:(NSString *)identifier url:(NSString *)url method:(NSString *)method headers:(NSDictionary *)headers body:(NSString *)body fullAPI:(BOOL)fullAPI resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject {

 NSURL *target=PorticoBoundedTarget(url,method,fullAPI);BOOL avatar=PorticoAvatarUploadPath(target.path,method);NSData *data=avatar?PorticoAvatarUploadBody(body):[body dataUsingEncoding:NSUTF8StringEncoding];
 if(!target||(avatar?!data:data.length>1048576)||identifier.length>128){reject(@"queue_transport",@"Invalid queue request.",nil);return;}
 [self submitBoundedBytes:identifier target:target method:method headers:headers data:data fullAPI:fullAPI subtitleUpload:NO resolver:resolve rejecter:reject];
}
- (void)submitBoundedBytes:(NSString *)identifier target:(NSURL *)target method:(NSString *)method headers:(NSDictionary *)headers data:(NSData *)data fullAPI:(BOOL)fullAPI subtitleUpload:(BOOL)subtitleUpload resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject {
 PorticoQueueRequest *request=[PorticoQueueRequest new];request.identifier=identifier;request.resolve=resolve;request.reject=reject;request.data=[NSMutableData data];request.owner=self;
 request.subtitleUpload=subtitleUpload;
 // PERF-S18: API reads that grow with the library (Home rows, queue views, admin pages) get 8 MB;
 // writes and the narrow queue-control surface keep the 1 MB bound.
 request.responseLimit=(fullAPI&&[method isEqualToString:@"GET"])?8388608:1048576;
 NSMutableURLRequest *wire=[NSMutableURLRequest requestWithURL:target];wire.HTTPMethod=method;wire.HTTPBody=data;wire.cachePolicy=NSURLRequestReloadIgnoringLocalCacheData;
 if(subtitleUpload)wire.timeoutInterval=PorticoSubtitleUploadIdleTimeout;
 for(id name in headers){if([name isKindOfClass:NSString.class]&&[headers[name] isKindOfClass:NSString.class])[wire setValue:headers[name] forHTTPHeaderField:name];}
 request.wire=wire;[PorticoQueueTransport submit:request domain:@"queue_transport" busy:@"Too many queue requests."];
}
RCT_REMAP_METHOD(profileRequest,profileRequest:(NSString *)identifier url:(NSString *)url method:(NSString *)method headers:(NSDictionary *)headers body:(NSString *)body resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 NSURL *target=[NSURL URLWithString:url];BOOL avatar=PorticoAvatarUploadPath(target.path,method);NSData *data=avatar?PorticoAvatarUploadBody(body):([body isKindOfClass:NSString.class]?[body dataUsingEncoding:NSUTF8StringEncoding]:nil);
 if(!target||![@[@"https",@"http"] containsObject:target.scheme]||!target.host.length||target.user||target.password||target.fragment||url.length>8192||!identifier.length||![target.path hasPrefix:@"/v1/"]||![@[@"GET",@"POST",@"PUT",@"PATCH",@"DELETE"]containsObject:method]||(avatar?!data:data.length>65536)||identifier.length>128){reject(@"profile_transport",@"A bounded profile request is required.",nil);return;}
 PorticoQueueRequest *request=[PorticoQueueRequest new];request.identifier=identifier;request.resolve=resolve;request.reject=reject;request.data=[NSMutableData data];request.owner=self;
 NSMutableURLRequest *wire=[NSMutableURLRequest requestWithURL:target];wire.HTTPMethod=method;wire.HTTPBody=data;wire.cachePolicy=NSURLRequestReloadIgnoringLocalCacheData;
 for(id name in headers){if([name isKindOfClass:NSString.class]&&[headers[name] isKindOfClass:NSString.class])[wire setValue:headers[name] forHTTPHeaderField:name];}
 request.wire=wire;[PorticoQueueTransport submit:request domain:@"profile_transport" busy:@"Too many requests are in progress."];
}

RCT_REMAP_METHOD(cancelQueueRequest,cancelQueueRequest:(NSString *)identifier){
 @synchronized(self){PorticoSubtitleUploadPreparation *pending=_subtitleUploads[identifier];if(pending){[pending.progress cancel];[_subtitleUploads removeObjectForKey:identifier];pending.reject(@"cancelled",@"Queue request cancelled.",nil);}}
 PorticoQueueRequest *request;@synchronized(PorticoQueueRequest.class){request=PorticoQueueRequests[identifier];}@synchronized(request){if(request.streamingResponse&&request.owner!=self)return;if(!request.completed){request.failureCode=@"cancelled";[request finish:@"Queue request cancelled."];}}
}

RCT_EXPORT_BLOCKING_SYNCHRONOUS_METHOD(playbackRequestId){return NSUUID.UUID.UUIDString;}
RCT_REMAP_METHOD(newRequestId,newRequestIdResolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){resolve(NSUUID.UUID.UUIDString);}
// Each PIN attempt, including a failed decrypt, is durably charged before the
// expensive operation. A restart or second React bridge cannot reset the delay.
RCT_REMAP_METHOD(profileInstallation,profileInstallationResolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 dispatch_async(PorticoStorageQueue(), ^{
 if(PorticoPrepareInstallationStorage()!=errSecSuccess){reject(@"secure_storage",@"This installation’s saved records could not be prepared.",nil);return;}
 @synchronized(PorticoRuntime.class){OSStatus e;NSString *value=PorticoProfileInstallation(&e);if(value)resolve(value);else reject(@"secure_storage",@"This device's profile installation could not be read.",nil);}
 });
}
RCT_REMAP_METHOD(saveProfileTrust,saveProfileTrust:(NSString *)scope metadata:(NSString *)metadataJSON payload:(NSString *)payloadJSON pin:(NSString *)pin resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 dispatch_async(PorticoStorageQueue(), ^{
 if(PorticoPrepareInstallationStorage()!=errSecSuccess){reject(@"secure_storage",@"This installation’s saved records could not be prepared.",nil);return;}
 @synchronized(PorticoRuntime.class){OSStatus e;NSString *key=PorticoProfileRecordKey(scope),*installation=PorticoProfileInstallation(&e);id metadata=PorticoProfileParse([metadataJSON dataUsingEncoding:NSUTF8StringEncoding]);
  if(!key||!installation||!PorticoProfileMetadata(scope,metadata,installation)||!PorticoProfilePIN(pin,[metadata[@"pinRequired"]boolValue])){reject(@"profile_trust_invalid",@"A current, fully scoped selection and its PIN are required to trust this profile.",nil);return;}
  NSArray *index=PorticoProfileActiveIndex(&e);if(!index){reject(@"secure_storage",@"Trusted profiles could not be read.",nil);return;}if(index.count>=64&&![index containsObject:key]){reject(@"profile_trust_full",@"Remove an unused trusted profile before remembering another.",nil);return;}
  NSDictionary *record=PorticoProfileEnvelope(scope,metadataJSON,payloadJSON,pin,&e);if(!record){reject(@"secure_storage",@"The profile proof could not be protected on this device.",nil);return;}
  NSData *old=PorticoProfileRead(key,&e);if(e!=errSecSuccess&&e!=errSecItemNotFound){reject(@"secure_storage",@"The existing profile proof could not be read.",nil);return;}
  e=PorticoProfileWrite(key,PorticoProfileJSON(record));if(e==errSecSuccess&&![index containsObject:key]){NSMutableArray *next=[index mutableCopy];[next addObject:key];e=PorticoProfileWrite(@"index.v1",PorticoProfileJSON(next));}
  if(e!=errSecSuccess){PorticoProfileWrite(key,old);PorticoProfileWrite(@"index.v1",PorticoProfileJSON(index));}
  if(e==errSecSuccess)resolve(@YES);else reject(@"secure_storage",@"This profile selection could not be remembered.",nil);
 }
 });
}
RCT_REMAP_METHOD(listProfileTrust,listProfileTrustResolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 dispatch_async(PorticoStorageQueue(), ^{
 if(PorticoPrepareInstallationStorage()!=errSecSuccess){reject(@"secure_storage",@"This installation’s saved records could not be prepared.",nil);return;}
 @synchronized(PorticoRuntime.class){OSStatus e;NSArray *index=PorticoProfileActiveIndex(&e);if(!index){reject(@"secure_storage",@"Trusted profiles could not be read.",nil);return;}NSMutableArray *out=[NSMutableArray array];double now=NSDate.date.timeIntervalSince1970*1000;
  for(NSString *key in index){NSData *raw=PorticoProfileRead(key,&e);if(e==errSecItemNotFound)continue;if(e!=errSecSuccess){reject(@"secure_storage",@"A trusted profile could not be read.",nil);return;}id record=PorticoProfileParse(raw);if(![record isKindOfClass:NSDictionary.class]||!PorticoProfileText(record[@"metadata"],8192)){reject(@"secure_storage",@"A trusted profile record is invalid.",nil);return;}id metadata=PorticoProfileParse([record[@"metadata"]dataUsingEncoding:NSUTF8StringEncoding]);if(![metadata isKindOfClass:NSDictionary.class]){reject(@"secure_storage",@"A trusted profile record is invalid.",nil);return;}if([metadata[@"expiresAtMs"]isKindOfClass:NSNumber.class]&&[metadata[@"expiresAtMs"]doubleValue]>now)[out addObject:metadata];}
  resolve([[NSString alloc]initWithData:PorticoProfileJSON(out) encoding:NSUTF8StringEncoding]);
 }
 });
}
RCT_REMAP_METHOD(unlockProfileTrust,unlockProfileTrust:(NSString *)scope pin:(NSString *)pin resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 dispatch_async(PorticoStorageQueue(), ^{
 if(PorticoPrepareInstallationStorage()!=errSecSuccess){reject(@"secure_storage",@"This installation’s saved records could not be prepared.",nil);return;}
 @synchronized(PorticoRuntime.class){OSStatus e;NSString *key=PorticoProfileRecordKey(scope);if(!key){reject(@"profile_trust_invalid",@"Choose a previously trusted profile.",nil);return;}id value=PorticoProfileParse(PorticoProfileRead(key,&e));if(e!=errSecSuccess||![value isKindOfClass:NSDictionary.class]){reject(@"profile_trust_missing",@"This profile is not available for offline selection on this device.",nil);return;}NSMutableDictionary *record=[value mutableCopy];if(!PorticoProfileText(record[@"metadata"],8192)||!PorticoProfileAttempts(record)){reject(@"secure_storage",@"The trusted profile record is invalid. Select it online again.",nil);return;}id metadata=PorticoProfileParse([record[@"metadata"]dataUsingEncoding:NSUTF8StringEncoding]);NSString *installation=PorticoProfileInstallation(&e);
  if(!installation||!PorticoProfileMetadata(scope,metadata,installation)){reject(@"profile_trust_expired",@"This trusted profile expired or belongs to another installation. Select it online again.",nil);return;}
  double now=NSDate.date.timeIntervalSince1970;if([record[@"lockedUntil"]doubleValue]>now){reject(@"profile_pin_locked",@"Profile PIN attempts are temporarily locked. Try again later.",nil);return;}
  if(!PorticoProfilePIN(pin,[metadata[@"pinRequired"]boolValue])){reject(@"profile_pin_required",@"Enter this profile's four-digit PIN.",nil);return;}
  NSInteger attempts=MIN(1000,[record[@"attempts"]integerValue]+1);double delay=attempts>=10?900:attempts>=5?300:attempts>=3?30:2;
  record[@"attempts"]=@(attempts);record[@"lockedUntil"]=@(now+delay);e=PorticoProfileWrite(key,PorticoProfileJSON(record));if(e!=errSecSuccess){reject(@"secure_storage",@"The profile unlock attempt could not be saved.",nil);return;}
  NSString *payload=PorticoProfileDecrypt(record,scope,pin,&e);if(!payload){reject(e==errSecAuthFailed?@"profile_pin_required":@"secure_storage",e==errSecAuthFailed?@"That profile PIN is not correct. Try again after the delay.":@"The protected profile proof could not be read.",nil);return;}
  record[@"attempts"]=@0;record[@"lockedUntil"]=@0;e=PorticoProfileWrite(key,PorticoProfileJSON(record));if(e!=errSecSuccess){reject(@"secure_storage",@"The profile unlock could not be saved.",nil);return;}resolve(payload);
 }
 });
}
RCT_REMAP_METHOD(removeProfileTrust,removeProfileTrust:(NSString *)scope resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 dispatch_async(PorticoStorageQueue(), ^{
 if(PorticoPrepareInstallationStorage()!=errSecSuccess){reject(@"secure_storage",@"This installation’s saved records could not be prepared.",nil);return;}
 @synchronized(PorticoRuntime.class){NSString *key=PorticoProfileRecordKey(scope);if(!key){reject(@"profile_trust_invalid",@"A complete profile scope is required.",nil);return;}OSStatus e=PorticoProfileWrite(key,nil);if(e==errSecSuccess){NSArray *index=PorticoProfileIndex(&e);if(index){NSMutableArray *next=[index mutableCopy];[next removeObject:key];e=PorticoProfileWrite(@"index.v1",PorticoProfileJSON(next));}}if(e==errSecSuccess)resolve(@YES);else reject(@"secure_storage",@"The trusted profile could not be removed.",nil);}
 });
}

RCT_REMAP_METHOD(loadSelectedViewer,loadSelectedViewerResolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 dispatch_async(PorticoStorageQueue(), ^{
 if(PorticoPrepareInstallationStorage()!=errSecSuccess){reject(@"secure_storage",@"This installation’s saved records could not be prepared.",nil);return;}
 @synchronized(PorticoRuntime.class){if([NSUserDefaults.standardUserDefaults boolForKey:@"PorticoSelectedViewerQuarantined"]){resolve(NSNull.null);return;}
  NSDictionary *query=@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,(__bridge id)kSecAttrService:@"tv.getportico.portico.credentials",(__bridge id)kSecAttrAccount:@"portico:selected-viewer:v1",(__bridge id)kSecReturnData:@YES};CFTypeRef value=NULL;OSStatus e=SecItemCopyMatching((__bridge CFDictionaryRef)query,&value);PorticoResolveSecureStorageRead(e,CFBridgingRelease(value),NSNull.null,@"The saved server selection could not be read.",resolve,reject);
 }
 });
}
RCT_REMAP_METHOD(saveSelectedViewer,saveSelectedViewer:(NSString *)json resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 dispatch_async(PorticoStorageQueue(), ^{
 if(PorticoPrepareInstallationStorage()!=errSecSuccess){reject(@"secure_storage",@"This installation’s saved records could not be prepared.",nil);return;}
 @synchronized(PorticoRuntime.class){BOOL removing=[json isEqual:@"null"];if(removing){[NSUserDefaults.standardUserDefaults setBool:YES forKey:@"PorticoSelectedViewerQuarantined"];[NSUserDefaults.standardUserDefaults synchronize];}
  NSData *data=[json dataUsingEncoding:NSUTF8StringEncoding];if(!data||data.length>65536){reject(@"secure_storage",@"The server credential record is invalid.",nil);return;}
  NSDictionary *query=@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,(__bridge id)kSecAttrService:@"tv.getportico.portico.credentials",(__bridge id)kSecAttrAccount:@"portico:selected-viewer:v1"};OSStatus e;
  if(removing){e=SecItemDelete((__bridge CFDictionaryRef)query);if(e==errSecItemNotFound)e=errSecSuccess;}else{NSDictionary *attributes=@{(__bridge id)kSecValueData:data,(__bridge id)kSecAttrAccessible:(__bridge id)kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly};e=SecItemUpdate((__bridge CFDictionaryRef)query,(__bridge CFDictionaryRef)attributes);if(e==errSecItemNotFound){NSMutableDictionary *insert=[query mutableCopy];[insert addEntriesFromDictionary:attributes];e=SecItemAdd((__bridge CFDictionaryRef)insert,NULL);}}
  if(e!=errSecSuccess){reject(@"secure_storage",@"The server credential could not be saved. The account credential is unchanged.",nil);return;}
  NSMutableDictionary *read=[query mutableCopy];read[(__bridge id)kSecReturnData]=@YES;CFTypeRef value=NULL;e=SecItemCopyMatching((__bridge CFDictionaryRef)read,&value);NSData *confirmed=CFBridgingRelease(value);
  if((removing&&e!=errSecItemNotFound)||(!removing&&(e!=errSecSuccess||![confirmed isKindOfClass:NSData.class]||![confirmed isEqualToData:data]))){[NSUserDefaults.standardUserDefaults setBool:YES forKey:@"PorticoSelectedViewerQuarantined"];[NSUserDefaults.standardUserDefaults synchronize];reject(@"secure_storage",@"Server credential storage could not be verified. Sign in to the server again.",nil);return;}
  if(!removing){[NSUserDefaults.standardUserDefaults setBool:NO forKey:@"PorticoSelectedViewerQuarantined"];[NSUserDefaults.standardUserDefaults synchronize];}resolve(@YES);
 }
 });
}

RCT_REMAP_METHOD(loadCredentials,loadCredentials:(NSString *)origin resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 dispatch_async(PorticoStorageQueue(), ^{
 if(PorticoPrepareInstallationStorage()!=errSecSuccess){reject(@"secure_storage",@"This installation’s saved records could not be prepared.",nil);return;}
 NSDictionary *query=@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,(__bridge id)kSecAttrService:@"tv.getportico.portico.credentials",(__bridge id)kSecAttrAccount:origin,(__bridge id)kSecReturnData:@YES};CFTypeRef result=NULL;OSStatus status=SecItemCopyMatching((__bridge CFDictionaryRef)query,&result);
 PorticoResolveSecureStorageRead(status,CFBridgingRelease(result),NSNull.null,@"Account credentials could not be read.",resolve,reject);
 });
}
RCT_REMAP_METHOD(saveCredentials,saveCredentials:(NSString *)origin json:(NSString *)json resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 dispatch_async(PorticoStorageQueue(), ^{
 if(PorticoPrepareInstallationStorage()!=errSecSuccess){reject(@"secure_storage",@"This installation’s saved records could not be prepared.",nil);return;}
 NSData *data=[json dataUsingEncoding:NSUTF8StringEncoding];if(!data||data.length>262144){reject(@"credential_size",@"Account credential storage is full.",nil);return;}
 NSDictionary *query=@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,(__bridge id)kSecAttrService:@"tv.getportico.portico.credentials",(__bridge id)kSecAttrAccount:origin};NSDictionary *attributes=@{(__bridge id)kSecValueData:data,(__bridge id)kSecAttrAccessible:(__bridge id)kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly};OSStatus status=SecItemUpdate((__bridge CFDictionaryRef)query,(__bridge CFDictionaryRef)attributes);if(status==errSecItemNotFound){NSMutableDictionary *insert=[query mutableCopy];[insert addEntriesFromDictionary:attributes];status=SecItemAdd((__bridge CFDictionaryRef)insert,NULL);}if(status==errSecSuccess)resolve(@YES);else reject(@"secure_storage",@"Account credentials could not be saved.",nil);
 });
}
RCT_REMAP_METHOD(loadJournal,loadJournal:(NSString *)scope resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 dispatch_async(PorticoStorageQueue(), ^{
 if(PorticoPrepareInstallationStorage()!=errSecSuccess){reject(@"secure_storage",@"This installation’s saved records could not be prepared.",nil);return;}
 NSDictionary *query=@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,(__bridge id)kSecAttrService:@"tv.getportico.portico.playback",(__bridge id)kSecAttrAccount:scope,(__bridge id)kSecReturnData:@YES};CFTypeRef result=NULL;OSStatus status=SecItemCopyMatching((__bridge CFDictionaryRef)query,&result);
 PorticoResolveSecureStorageRead(status,CFBridgingRelease(result),@"[]",@"Playback recovery could not be read.",resolve,reject);
 });
}
RCT_REMAP_METHOD(saveJournal,saveJournal:(NSString *)scope json:(NSString *)json resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject){
 dispatch_async(PorticoStorageQueue(), ^{
 if(PorticoPrepareInstallationStorage()!=errSecSuccess){reject(@"secure_storage",@"This installation’s saved records could not be prepared.",nil);return;}
 NSData *data=[json dataUsingEncoding:NSUTF8StringEncoding];if(data.length>262144){reject(@"journal_size",@"Playback recovery is full.",nil);return;}
 NSDictionary *query=@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,(__bridge id)kSecAttrService:@"tv.getportico.portico.playback",(__bridge id)kSecAttrAccount:scope};NSDictionary *attributes=@{(__bridge id)kSecValueData:data,(__bridge id)kSecAttrAccessible:(__bridge id)kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly};OSStatus status=SecItemUpdate((__bridge CFDictionaryRef)query,(__bridge CFDictionaryRef)attributes);if(status==errSecItemNotFound){NSMutableDictionary *insert=[query mutableCopy];[insert addEntriesFromDictionary:attributes];status=SecItemAdd((__bridge CFDictionaryRef)insert,NULL);}if(status==errSecSuccess)resolve(@YES);else reject(@"secure_storage",@"Playback recovery could not be saved.",nil);
 });
}

RCT_EXPORT_BLOCKING_SYNCHRONOUS_METHOD(playbackRandomBytes:(NSNumber *)count){
 NSInteger n=[count integerValue];if(n<1||n>64)return NSNull.null;uint8_t bytes[64];if(SecRandomCopyBytes(kSecRandomDefault,(size_t)n,bytes)!=errSecSuccess)return NSNull.null;
 NSMutableArray *result=[NSMutableArray new];for(NSInteger i=0;i<n;i++)[result addObject:@(bytes[i])];return result;
}

@end

@interface PorticoBackdropView : UIView @end
@implementation PorticoBackdropView
+ (Class)layerClass{return CAGradientLayer.class;}
- (instancetype)initWithFrame:(CGRect)frame{if((self=[super initWithFrame:frame])){CAGradientLayer *gradient=(CAGradientLayer *)self.layer;gradient.type=kCAGradientLayerRadial;gradient.colors=@[(id)[UIColor colorWithRed:0.075 green:0.18 blue:0.24 alpha:1].CGColor,(id)[UIColor colorWithRed:0.027 green:0.063 blue:0.094 alpha:1].CGColor];gradient.locations=@[@0,@0.74,@1];gradient.colors=@[(id)[UIColor colorWithRed:0.09 green:0.24 blue:0.33 alpha:1].CGColor,(id)[UIColor colorWithRed:0.063 green:0.129 blue:0.176 alpha:1].CGColor,(id)[UIColor colorWithRed:0.027 green:0.063 blue:0.094 alpha:1].CGColor];gradient.startPoint=CGPointMake(.5,-.08);gradient.endPoint=CGPointMake(.92,.44);self.userInteractionEnabled=NO;}return self;}
@end
@interface PorticoBackdropManager : RCTViewManager @end
@implementation PorticoBackdropManager
RCT_EXPORT_MODULE(PorticoBackdrop)
- (UIView *)view{return [PorticoBackdropView new];}
@end
