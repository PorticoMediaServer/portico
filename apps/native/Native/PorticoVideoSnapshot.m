#import <React/RCTBridgeModule.h>
#import <React/RCTInvalidating.h>
#import <React/RCTEventEmitter.h>
#import <CoreImage/CoreImage.h>
#import <ImageIO/ImageIO.h>
#import "PorticoPlaybackController.h"
#import "PorticoVideoSnapshot.h"

static BOOL SnapshotText(id value,NSUInteger limit) {return [value isKindOfClass:NSString.class]&&[value length]>0&&[value length]<=limit;}
static BOOL SnapshotInteger(id value) {return [value isKindOfClass:NSNumber.class]&&CFGetTypeID((__bridge CFTypeRef)value)!=CFBooleanGetTypeID()&&[value doubleValue]>0&&[value doubleValue]<=9007199254740991.0&&floor([value doubleValue])==[value doubleValue];}
@interface PorticoSnapshotFlight : NSObject
@property(nonatomic,copy) NSString *identifier;
@property(nonatomic) BOOL passive;
@property(nonatomic,copy) NSDictionary *source;
@property(nonatomic,copy) NSDictionary *binding;
@property(nonatomic,weak) PorticoPlaybackController *controller;
@property(nonatomic,strong) AVPlayerItem *item;
@property(nonatomic,strong) AVAssetImageGenerator *generator;
@property(nonatomic) NSTimeInterval deadline;
@property(nonatomic) NSUInteger recoveryAttempts;
@property(nonatomic,copy) RCTPromiseResolveBlock resolve;
@property(nonatomic,copy) RCTPromiseRejectBlock reject;
@end
@implementation PorticoSnapshotFlight @end

/** Same capture/release envelope as Android. This module never pauses, seeks or replaces the live item/output. */
@interface PorticoVideoSnapshot : RCTEventEmitter <RCTBridgeModule,RCTInvalidating> @end
@implementation PorticoVideoSnapshot {
 dispatch_queue_t _encoder;
 NSURL *_directory,*_retainedURL;
 PorticoSnapshotFlight *_flight,*_retained;
 BOOL _encoding,_invalidated;
 CIContext *_imageContext;
}
RCT_EXPORT_MODULE()
+ (BOOL)requiresMainQueueSetup {return YES;}
- (dispatch_queue_t)methodQueue {return dispatch_get_main_queue();}
- (NSArray<NSString *> *)supportedEvents {return @[@"PorticoVideoSnapshotRetired"];}
- (instancetype)init {
 if((self=[super init])){
  _encoder=dispatch_queue_create("tv.getportico.video-snapshot",DISPATCH_QUEUE_SERIAL);
  NSURL *cache=[NSFileManager.defaultManager URLsForDirectory:NSCachesDirectory inDomains:NSUserDomainMask].firstObject;
  if(cache)_directory=[[cache URLByAppendingPathComponent:@"PorticoVideoSnapshots" isDirectory:YES]URLByAppendingPathComponent:NSUUID.UUID.UUIDString isDirectory:YES];
  [NSNotificationCenter.defaultCenter addObserver:self selector:@selector(sourceChanged) name:@"PorticoPlaybackAttachmentChanged" object:nil];
  [NSNotificationCenter.defaultCenter addObserver:self selector:@selector(sourceChanged) name:@"PorticoVideoSnapshotSourceChanged" object:nil];
  [NSNotificationCenter.defaultCenter addObserver:self selector:@selector(suspend) name:UIApplicationWillResignActiveNotification object:nil];
 }
 return self;
}
- (BOOL)current:(PorticoSnapshotFlight *)flight {
 return !_invalidated&&UIApplication.sharedApplication.applicationState==UIApplicationStateActive&&flight.controller&&PorticoSnapshotController(flight.source[@"reactTag"],flight.source[@"lease"])==flight.controller&&flight.item==flight.controller.player.currentItem&&[flight.binding isEqual:flight.controller.externalPresentationBinding];
}
- (void)discardRetained {
 NSString *retired=_retained.passive?_retained.identifier:nil;
 if(_retainedURL)[NSFileManager.defaultManager removeItemAtURL:_retainedURL error:nil];
 _retainedURL=nil;_retained=nil;
 if(retired){@try{[self sendEventWithName:@"PorticoVideoSnapshotRetired" body:@{@"id":retired}];}@catch(NSException *exception){ /* Cleanup never depends on a live JS bridge. */ }}
}
- (void)fail:(PorticoSnapshotFlight *)flight code:(NSString *)code {
 if(_flight!=flight)return;
 _flight=nil;[flight.generator cancelAllCGImageGeneration];flight.generator=nil;RCTPromiseRejectBlock reject=flight.reject;flight.resolve=nil;flight.reject=nil;
 if(reject)reject(code,code,nil);
}
- (void)sourceChanged {
 if(!NSThread.isMainThread){dispatch_async(dispatch_get_main_queue(),^{[self sourceChanged];});return;}
 if(_flight&&![self current:_flight])[self fail:_flight code:@"snapshot_stale"];
 if(_retained&&!_retained.passive&&![self current:_retained])[self discardRetained];
}
- (void)suspend {
 if(_flight)[self fail:_flight code:@"snapshot_cancelled"];[self discardRetained];
}
RCT_REMAP_METHOD(capture,capture:(NSString *)identifier source:(NSDictionary *)source resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
#if TARGET_OS_TV
 reject(@"snapshot_unavailable",@"snapshot_unavailable",nil);return;
#endif
 if(_invalidated||!_directory||!SnapshotText(identifier,128)||![source isKindOfClass:NSDictionary.class]||![source[@"engine"] isEqual:@"native"]||!SnapshotInteger(source[@"reactTag"])||!SnapshotText(source[@"lease"],512)) {reject(@"snapshot_unavailable",@"snapshot_unavailable",nil);return;}
 if(_encoding||_flight||_retained){reject(@"snapshot_busy",@"snapshot_busy",nil);return;}
 NSDictionary *binding=[source[@"binding"] isKindOfClass:NSDictionary.class]?source[@"binding"]:nil;
 if(!binding||binding.count!=4||!SnapshotInteger(binding[@"intentId"])||!SnapshotInteger(binding[@"generation"])||!SnapshotText(binding[@"sessionId"],512)||!SnapshotText(binding[@"itemId"],512)){reject(@"snapshot_unavailable",@"snapshot_unavailable",nil);return;}
 PorticoSnapshotFlight *flight=[PorticoSnapshotFlight new];flight.identifier=identifier;flight.source=@{@"reactTag":source[@"reactTag"],@"engine":@"native",@"lease":[source[@"lease"]copy],@"binding":[binding copy]};flight.resolve=resolve;flight.reject=reject;
 NSMutableDictionary *full=[binding mutableCopy];full[@"lease"]=source[@"lease"];flight.binding=full;flight.controller=PorticoSnapshotController(source[@"reactTag"],source[@"lease"]);flight.item=flight.controller.player.currentItem;flight.deadline=NSProcessInfo.processInfo.systemUptime+1.5;
 if(![self current:flight]){reject(@"snapshot_stale",@"snapshot_stale",nil);return;}
 _flight=flight;
 // One total deadline includes frame recovery, encoding and main-queue delivery.
 dispatch_after(dispatch_time(DISPATCH_TIME_NOW,1500*NSEC_PER_MSEC),dispatch_get_main_queue(),^{[self fail:flight code:@"snapshot_timeout"];});
 CGAffineTransform transform=CGAffineTransformIdentity;CMTime decodedTime=kCMTimeInvalid;
 CVPixelBufferRef frame=[flight.controller copyVideoFrameForBinding:flight.binding displayTime:&decodedTime transform:&transform];
 if(frame){
  CIImage *image=[[CIImage imageWithCVPixelBuffer:frame]imageByApplyingTransform:transform];CVPixelBufferRelease(frame);
  [self encodeImage:image flight:flight];
 }else{
  // A hidden AVPlayerLayer can starve its item's output. Decode a bounded still from
  // that exact asset instead of replacing the live output (which can reset its clock).
  AVAsset *asset=flight.item.asset;
  // Do not open a playlist/segment reader for HLS. Unsupported still assets
  // remain unavailable; recovery is a bounded direct-file range read only.
  if(![asset isKindOfClass:AVURLAsset.class]||[[(AVURLAsset *)asset URL].pathExtension.lowercaseString isEqual:@"m3u8"]){[self fail:flight code:@"snapshot_unavailable"];return;}
  flight.generator=[[AVAssetImageGenerator alloc]initWithAsset:asset];
  flight.generator.appliesPreferredTrackTransform=YES;
  flight.generator.maximumSize=CGSizeMake(1280,1280);
  flight.generator.requestedTimeToleranceBefore=kCMTimeZero;
  flight.generator.requestedTimeToleranceAfter=kCMTimeZero;
  [self recoverFrame:flight];
 }
}
- (void)recoverFrame:(PorticoSnapshotFlight *)flight {
 if(_flight!=flight)return;
 if(![self current:flight]){[self fail:flight code:@"snapshot_stale"];return;}
 if(NSProcessInfo.processInfo.systemUptime>=flight.deadline){[self fail:flight code:@"snapshot_timeout"];return;}
 if(flight.recoveryAttempts++>=3){[self fail:flight code:@"snapshot_unavailable"];return;}
 CMTime requested=flight.item.currentTime;
 if(!CMTIME_IS_NUMERIC(requested)){[self fail:flight code:@"snapshot_unavailable"];return;}
 [flight.generator generateCGImagesAsynchronouslyForTimes:@[[NSValue valueWithCMTime:requested]] completionHandler:^(CMTime request,CGImageRef bitmap,CMTime actual,AVAssetImageGeneratorResult result,NSError *error){
  // AVFoundation owns the callback image; retain only the bounded pixels before
  // returning to the main queue. Cancellation never touches an item or successor.
  CIImage *image=bitmap?[CIImage imageWithCGImage:bitmap]:nil;
  dispatch_async(dispatch_get_main_queue(),^{
   if(self->_flight!=flight)return;
   if(![self current:flight]){[self fail:flight code:@"snapshot_stale"];return;}
   if(NSProcessInfo.processInfo.systemUptime>=flight.deadline){[self fail:flight code:@"snapshot_timeout"];return;}
   if(result!=AVAssetImageGeneratorSucceeded||!image||!CMTIME_IS_NUMERIC(actual)){[self fail:flight code:@"snapshot_unavailable"];return;}
   CMTime now=flight.item.currentTime;
   if(!CMTIME_IS_NUMERIC(now)){[self fail:flight code:@"snapshot_unavailable"];return;}
   if(fabs(CMTimeGetSeconds(CMTimeSubtract(now,actual)))>.25){[self recoverFrame:flight];return;}
   [flight.generator cancelAllCGImageGeneration];flight.generator=nil;
   [self encodeImage:image flight:flight];
  });
 }];
}
- (void)encodeImage:(CIImage *)input flight:(PorticoSnapshotFlight *)flight {
 if(_flight!=flight)return;
 _encoding=YES;NSURL *directory=_directory;
 dispatch_async(_encoder,^{@autoreleasepool{
  CIImage *image=input;CGRect extent=image.extent;CGFloat scale=MIN(1,MIN(1280/MAX(extent.size.width,extent.size.height),sqrt(921600/(extent.size.width*extent.size.height))));
  NSURL *url=nil;NSInteger encodedWidth=0,encodedHeight=0;
  if(!CGRectIsInfinite(extent)&&!CGRectIsNull(extent)&&extent.size.width>0&&extent.size.height>0&&isfinite(scale)&&scale>0){
   image=[image imageByApplyingTransform:CGAffineTransformMakeTranslation(-extent.origin.x,-extent.origin.y)];
   image=[image imageByApplyingTransform:CGAffineTransformMakeScale(scale,scale)];
   encodedWidth=(NSInteger)floor(image.extent.size.width);encodedHeight=(NSInteger)floor(image.extent.size.height);
   if(encodedWidth>0&&encodedHeight>0&&encodedWidth<=1280&&encodedHeight<=1280&&encodedWidth*encodedHeight<=921600){
    CGColorSpaceRef colors=CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
    // A bounded still does not need a second GPU command queue. Reuse one CPU context;
    // this also avoids cold Metal pipeline stalls while the live video keeps rendering.
    if(!self->_imageContext)self->_imageContext=[CIContext contextWithOptions:@{kCIContextUseSoftwareRenderer:@YES,kCIContextWorkingColorSpace:(__bridge id)colors,kCIContextOutputColorSpace:(__bridge id)colors}];
    CGImageRef bitmap=[self->_imageContext createCGImage:image fromRect:CGRectMake(0,0,encodedWidth,encodedHeight) format:kCIFormatRGBA8 colorSpace:colors];CGColorSpaceRelease(colors);
    NSMutableData *data=[NSMutableData data];CGImageDestinationRef destination=bitmap?CGImageDestinationCreateWithData((__bridge CFMutableDataRef)data,CFSTR("public.jpeg"),1,NULL):NULL;
    if(destination){CGImageDestinationAddImage(destination,bitmap,(__bridge CFDictionaryRef)@{(id)kCGImageDestinationLossyCompressionQuality:@.85});BOOL done=CGImageDestinationFinalize(destination);CFRelease(destination);
     if(done&&data.length&&data.length<=4194304&&[NSFileManager.defaultManager createDirectoryAtURL:directory withIntermediateDirectories:YES attributes:@{NSFileProtectionKey:NSFileProtectionComplete} error:nil]){
      NSURL *candidate=[directory URLByAppendingPathComponent:[NSUUID.UUID.UUIDString stringByAppendingString:@".jpg"]];
      if([data writeToURL:candidate options:NSDataWritingAtomic|NSDataWritingFileProtectionComplete error:nil])url=candidate;
     }
    }
    if(bitmap)CGImageRelease(bitmap);
   }
  }
  dispatch_async(dispatch_get_main_queue(),^{
   self->_encoding=NO;
   if(self->_flight!=flight||![self current:flight]){if(url)[NSFileManager.defaultManager removeItemAtURL:url error:nil];[self fail:flight code:@"snapshot_stale"];return;}
   if(NSProcessInfo.processInfo.systemUptime>=flight.deadline){if(url)[NSFileManager.defaultManager removeItemAtURL:url error:nil];[self fail:flight code:@"snapshot_timeout"];return;}
   if(!url){[self fail:flight code:@"snapshot_unavailable"];return;}
   self->_flight=nil;self->_retained=flight;self->_retainedURL=url;
   // File lifetime starts at capture completion; retainForExit never renews it.
   dispatch_after(dispatch_time(DISPATCH_TIME_NOW,15*NSEC_PER_SEC),dispatch_get_main_queue(),^{if(self->_retained==flight)[self discardRetained];});
   RCTPromiseResolveBlock completion=flight.resolve;flight.resolve=nil;flight.reject=nil;
   if(completion)completion(@{@"id":flight.identifier,@"uri":url.absoluteString,@"width":@(encodedWidth),@"height":@(encodedHeight)});
  });
 }});
}
/** Main-queue transfer of one exact settled image; no old-source capture path. */
RCT_REMAP_METHOD(retainForExit,retainForExit:(NSString *)identifier source:(NSDictionary *)source resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
 PorticoSnapshotFlight *image=_retained;
 if(!image||image.passive||![image.identifier isEqual:identifier]||![source isKindOfClass:NSDictionary.class]||![image.source isEqual:source]||![self current:image]||!_retainedURL){resolve(@NO);return;}
 image.passive=YES;
 // Drop even weak engine access and original lookup data; retain only immutable file pixels.
 image.controller=nil;image.item=nil;image.source=nil;image.binding=nil;
 resolve(@YES);
}
RCT_REMAP_METHOD(release,release:(NSString *)identifier resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
 BOOL owned=NO;
 if([_flight.identifier isEqual:identifier]){owned=YES;[self fail:_flight code:@"snapshot_cancelled"];}
 if([_retained.identifier isEqual:identifier]){owned=YES;[self discardRetained];}
 resolve(@(owned));
}
- (void)invalidate {
 if(!NSThread.isMainThread){dispatch_async(dispatch_get_main_queue(),^{[self invalidate];});return;}
 if(_invalidated)return;_invalidated=YES;[NSNotificationCenter.defaultCenter removeObserver:self];
 if(_flight)[self fail:_flight code:@"snapshot_cancelled"];[self discardRetained];
 NSURL *directory=_directory;dispatch_async(_encoder,^{if(directory)[NSFileManager.defaultManager removeItemAtURL:directory error:nil];});
 [super invalidate];
}
- (void)dealloc {
 [NSNotificationCenter.defaultCenter removeObserver:self];NSURL *directory=_directory;
 if(directory)dispatch_async(_encoder,^{[NSFileManager.defaultManager removeItemAtURL:directory error:nil];});
}
@end
