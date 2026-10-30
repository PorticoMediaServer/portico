#import <UIKit/UIKit.h>
#import <React/RCTViewManager.h>
#import <React/RCTEventEmitter.h>
#import <React/UIView+React.h>
#import <objc/runtime.h>
#import "../Native/PorticoPlayerView.h"
#import "../Native/PorticoPlaybackController.h"
#import "../Native/PorticoVideoSnapshot.h"
#import "../Native/PorticoServerTrust.h"

// Real React classes, registration, view tags and trust are linked from Pods.
static NSMutableArray *snapshotRetirements;
@interface PorticoVideoSnapshot : RCTEventEmitter
- (void)capture:(NSString *)identifier source:(NSDictionary *)source resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject;
- (void)release:(NSString *)identifier resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject;
- (void)retainForExit:(NSString *)identifier source:(NSDictionary *)source resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject;
- (void)invalidate;
@end
// Observe this module's native event without supplying a fake RN class or JS bridge.
@interface ObservedVideoSnapshot : PorticoVideoSnapshot @end
@implementation ObservedVideoSnapshot
- (void)sendEventWithName:(NSString *)name body:(id)body {
 if (!snapshotRetirements) snapshotRetirements=[NSMutableArray new];
 [snapshotRetirements addObject:body];
}
@end
static NSUInteger assertions=0,failures=0;
static BOOL expect(BOOL value,const char *what){assertions++;if(!value){failures++;fprintf(stderr,"FAIL %s\n",what);}return value;}
static BOOL waitFor(BOOL (^ready)(void),double seconds){NSDate *until=[NSDate dateWithTimeIntervalSinceNow:seconds];while(!ready()&&until.timeIntervalSinceNow>0)[NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:.01]];return ready();}
static NSDictionary *capture(PorticoVideoSnapshot *module,NSString *identifier,NSDictionary *source,NSString *__strong *failure){
 __block BOOL done=NO;__block NSDictionary *result=nil;__block NSString *code=nil;
 [module capture:identifier source:source resolver:^(id value){result=value;done=YES;} rejecter:^(NSString *value,NSString *message,NSError *error){code=value;done=YES;}];
 if(!waitFor(^BOOL{return done;},3))code=@"harness_timeout";if(code){
  static NSMutableSet *reported;if(!reported)reported=[NSMutableSet new];
  if(![reported containsObject:identifier]){
   [reported addObject:identifier];PorticoPlaybackController *controller=PorticoSnapshotController(source[@"reactTag"],source[@"lease"]);
   AVPlayerItemVideoOutput *output=[controller valueForKey:@"videoOutput"];
   CMTime cached=[[controller valueForKey:@"capturedVideoTime"] CMTimeValue];
   fprintf(stderr,"capture %s: %s source=%.3f cached=%.3f newFrame=%d status=%ld rate=%.1f binding=%s\n",identifier.UTF8String,code.UTF8String,controller.currentSourcePosition,CMTimeGetSeconds(cached),[output hasNewPixelBufferForItemTime:controller.player.currentItem.currentTime],(long)controller.player.currentItem.status,controller.player.rate,controller.externalPresentationBinding.description.UTF8String?:"nil");
  }
 }if(failure)*failure=code;return result;
}
static BOOL release(PorticoVideoSnapshot *module,NSString *identifier){__block BOOL released=NO;[module release:identifier resolver:^(id value){released=[value boolValue];} rejecter:^(NSString *code,NSString *message,NSError *error){}];return released;}
static NSString *receiptPath(NSDictionary *receipt){return [receipt[@"uri"]isKindOfClass:NSString.class]?[NSURL URLWithString:receipt[@"uri"]].path?:@"":@"";}
static NSArray<NSNumber *> *pixel(NSDictionary *receipt){
 UIImage *image=[UIImage imageWithContentsOfFile:receiptPath(receipt)];if(!image.CGImage)return @[];
 uint8_t bytes[4]={0};CGColorSpaceRef colors=CGColorSpaceCreateDeviceRGB();CGContextRef context=CGBitmapContextCreate(bytes,1,1,8,4,colors,kCGImageAlphaPremultipliedLast|kCGBitmapByteOrder32Big);CGContextDrawImage(context,CGRectMake(0,0,1,1),image.CGImage);CGContextRelease(context);CGColorSpaceRelease(colors);return @[@(bytes[0]),@(bytes[1]),@(bytes[2])];
}
static int runTests(UIWindow *window){@autoreleasepool{
 BOOL ok=YES;NSFileManager *files=NSFileManager.defaultManager;
 NSURL *downloads=[[files URLsForDirectory:NSApplicationSupportDirectory inDomains:NSUserDomainMask].firstObject URLByAppendingPathComponent:@"Downloads" isDirectory:YES];[files createDirectoryAtURL:downloads withIntermediateDirectories:YES attributes:nil error:nil];NSURL *local=[downloads URLByAppendingPathComponent:@"snapshot-authored.mp4"];
 [files removeItemAtURL:local error:nil];ok=expect([files copyItemAtURL:[NSBundle.mainBundle URLForResource:@"snapshot-red-blue" withExtension:@"mp4"] toURL:local error:nil],"copy authored video to authorized local Downloads")&&ok;
 PorticoPlaybackController *controller=[PorticoPlaybackController new];NSMutableArray *playbackFacts=[NSMutableArray new];
 // The actual module subscribes before accepting commands. Progress facts also exercise
 // the production decoded-frame observation outlet; a nil observer bypasses that path.
 controller.onFact=^(NSDictionary *fact){[playbackFacts addObject:fact];};
 NSDictionary *lease=[controller acquire:@"snapshot-fixture"];
 NSDictionary *binding=@{@"lease":lease[@"lease"],@"intentId":@1,@"sessionId":@"snapshot-session",@"generation":@1,@"itemId":@"authored-video"};
 PorticoPlayerView *view=[[PorticoPlayerView alloc]initWithFrame:CGRectMake(0,0,320,180)];view.reactTag=@42;view.controller=controller;view.lease=lease[@"lease"];[window.rootViewController.view addSubview:view];[view layoutIfNeeded];
 NSDictionary *command=@{@"binding":binding,@"commandRevision":@1,@"source":(NSProcessInfo.processInfo.environment[@"PORTICO_SNAPSHOT_REMOTE_URL"]?:local.absoluteString),@"rate":@1,@"paused":@NO};NSString *rejection=[controller acceptCommand:command];
 ok=expect(!rejection,"actual public controller accepts valid local command")&&ok;
 ok=expect(waitFor(^BOOL{return controller.externalPresentationBinding&&controller.currentSourcePosition>.25;},12),"actual AVPlayer ready and source clock advances")&&ok;
 if(!ok){fprintf(stderr,"ready=%s position=%f error=%s\n",controller.externalPresentationBinding.description.UTF8String,controller.currentSourcePosition,controller.player.currentItem.error.description.UTF8String);[controller invalidate];return 1;}
 AVPlayer *player=controller.player;AVPlayerItem *item=player.currentItem;AVPlayerLayer *layer=(AVPlayerLayer *)view.layer.sublayers.firstObject;
 NSDictionary *source=@{@"reactTag":@42,@"engine":@"native",@"lease":lease[@"lease"],@"binding":@{@"intentId":@1,@"sessionId":@"snapshot-session",@"generation":@1,@"itemId":@"authored-video"}};
 PorticoVideoSnapshot *module=[ObservedVideoSnapshot new];__block NSString *failure=nil;__block NSDictionary *red=nil;
 if([NSProcessInfo.processInfo.environment[@"PORTICO_SNAPSHOT_UNSUPPORTED_STILL"] isEqual:@"1"]){
  AVPlayerItemVideoOutput *output=[controller valueForKey:@"videoOutput"];[controller setValue:nil forKey:@"videoOutput"];
  CMTime beforeRequest=item.currentTime;CMTimebaseRef timebase=item.timebase;float rate=player.rate;NSTimeInterval start=NSProcessInfo.processInfo.systemUptime;
  NSDictionary *unsupported=capture(module,@"unsupported-still",source,&failure);
  ok=expect(!unsupported&&[failure isEqual:@"snapshot_unavailable"]&&NSProcessInfo.processInfo.systemUptime-start<.2&&![module valueForKey:@"flight"],"HLS refuses unsupported still recovery immediately without opening a generator")&&ok;
  ok=expect(player.currentItem==item&&item.timebase==timebase&&player.rate==rate&&CMTimeCompare(item.currentTime,beforeRequest)>=0,"unsupported recovery preserves the live item, timebase, clock and rate")&&ok;
  [controller setValue:output forKey:@"videoOutput"];[module invalidate];[view removeFromSuperview];[controller invalidate];[files removeItemAtURL:local error:nil];
  printf("{\"unsupportedStillPass\":%s,\"assertions\":%lu,\"failures\":%lu}\n",ok?"true":"false",(unsigned long)assertions,(unsigned long)failures);return ok?0:1;
 }

 ok=expect(waitFor(^BOOL{if(!red)red=capture(module,@"before-bounds",source,&failure);return red!=nil;},3),"actual decoded red frame captured")&&ok;
 NSArray *redPixel=pixel(red);ok=expect(redPixel.count==3&&[redPixel[0]intValue]>180&&[redPixel[2]intValue]<60,"decoded JPEG contains authored red pixels")&&ok;
 NSString *redPath=receiptPath(red);double before=controller.currentSourcePosition;
 NSString *privateDirectory=redPath.stringByDeletingLastPathComponent;
 view.bounds=CGRectZero;view.hidden=YES;[view layoutIfNeeded];
 ok=expect(PorticoSnapshotController(@42,lease[@"lease"])==controller&&layer.player==player&&player.currentItem==item,"zero bounds retains exact view/controller/player/item/lease")&&ok;
 ok=expect([files fileExistsAtPath:redPath],"retained real JPEG survives geometry-only invalidation")&&ok;
 ok=expect(waitFor(^BOOL{return controller.currentSourcePosition>5.25;},8),"decoded timeline advances while bounds missing")&&ok;
 ok=expect(release(module,@"before-bounds")&&![files fileExistsAtPath:redPath],"release removes retained private JPEG")&&ok;
 CMTime captureStart=item.currentTime;CMTimebaseRef originalTimebase=item.timebase;if(originalTimebase)CFRetain(originalTimebase);float captureRate=player.rate;
 __block CMTime lastClock=captureStart;__block BOOL clockContinuous=YES;
 NSTimer *clockMonitor=[NSTimer scheduledTimerWithTimeInterval:.005 repeats:YES block:^(NSTimer *timer){
  CMTime now=item.currentTime;
  if(CMTimeCompare(now,lastClock)<0||item.timebase!=originalTimebase||player.currentItem!=item||player.rate!=captureRate)clockContinuous=NO;
  lastClock=now;
 }];
 CVPixelBufferRef starved=[controller copyVideoFrameForBinding:binding displayTime:NULL transform:NULL];
 BOOL naturallyStarved=!starved;if(starved)CVPixelBufferRelease(starved);
 // AVFoundation's starvation is runtime/scheduling dependent. Leave the actual
 // output attached to the live item, but make the controller's pull outlet
 // unavailable for this request. This deterministically exercises the real
 // asset decoder, without replacing an output or fabricating pixels/a clock.
 AVPlayerItemVideoOutput *originalOutput=[controller valueForKey:@"videoOutput"];
 [controller setValue:nil forKey:@"videoOutput"];
 NSTimeInterval recoveryStart=NSProcessInfo.processInfo.systemUptime;
 NSDictionary *blue=capture(module,@"missing-bounds",source,&failure);
 NSTimeInterval recoveryDuration=NSProcessInfo.processInfo.systemUptime-recoveryStart;
 [controller setValue:originalOutput forKey:@"videoOutput"];
 ok=expect(clockContinuous&&recoveryDuration<1.5,"all sampled recovery clocks are monotonic, same timebase/rate, within original deadline")&&ok;
 NSArray *bluePixel=pixel(blue);double during=controller.currentSourcePosition;
 ok=expect(blue&&bluePixel.count==3&&[bluePixel[2]intValue]>180&&[bluePixel[0]intValue]<60,"fresh actual blue frame captured with zero bounds")&&ok;
 ok=expect(CMTimeCompare(item.currentTime,captureStart)>=0,"snapshot refresh never moves the playing clock backwards")&&ok;
 ok=expect(originalTimebase&&item.timebase==originalTimebase&&player.currentItem==item&&layer.player==player&&player.rate==captureRate,"snapshot refresh preserves timebase, player, item and rate")&&ok;
 BOOL observedVideo=NO;for(NSDictionary *fact in playbackFacts)if([fact[@"type"]isEqual:@"progress"]&&[fact[@"observedPlayback"][@"video"][@"width"]intValue]==320&&[fact[@"observedPlayback"][@"video"][@"height"]intValue]==180)observedVideo=YES;
 ok=expect(observedVideo,"real progress subscription reports actual decoded video dimensions")&&ok;
 NSString *bluePath=receiptPath(blue);
 // Returning geometry during a temporarily unready binding must retain the
 // repair; the existing display link retries once that same attachment is ready.
 [controller setValue:@YES forKey:@"seeking"];
 view.hidden=NO;view.bounds=CGRectMake(0,0,320,180);[view layoutIfNeeded];
 ok=expect([view.layer.sublayers.firstObject isEqual:layer],"unready attachment cannot replace its presentation surface")&&ok;
 [controller setValue:@NO forKey:@"seeking"];
 ok=expect(waitFor(^BOOL{return view.layer.sublayers.firstObject!=layer;},.5),"deferred restoration repairs only after the same attachment is ready")&&ok;
 ok=expect(view.reactTag.intValue==42&&layer.player==player&&player.currentItem==item&&[binding isEqual:controller.externalPresentationBinding],"restore bounds preserves exact attachment and binding")&&ok;
 AVPlayerLayer *restoredLayer=(AVPlayerLayer *)view.layer.sublayers.firstObject;
 ok=expect(restoredLayer!=layer&&restoredLayer.player==player&&[controller valueForKey:@"videoOutput"]==originalOutput,"only the presentation surface is renewed; live player/item/output stay attached")&&ok;
 ok=expect(waitFor(^BOOL{return controller.currentSourcePosition>during+.15&&layer.readyForDisplay;},1),"restored visible player continues advancing")&&ok;
 [clockMonitor invalidate];
 ok=expect(clockContinuous,"restoration keeps every sampled clock, timebase, player, item and rate")&&ok;
 if(originalTimebase)CFRelease(originalTimebase);
 ok=expect([files fileExistsAtPath:bluePath]&&release(module,@"missing-bounds")&&![files fileExistsAtPath:bluePath],"restore and explicit release preserve correct ownership")&&ok;
 for(NSDictionary *wrong in @[@{@"reactTag":@43},@{@"lease":@"wrong-lease"},@{@"binding":@{@"intentId":@2,@"sessionId":@"snapshot-session",@"generation":@1,@"itemId":@"authored-video"}},@{@"binding":@{@"intentId":@1,@"sessionId":@"another-session",@"generation":@1,@"itemId":@"authored-video"}},@{@"binding":@{@"intentId":@1,@"sessionId":@"snapshot-session",@"generation":@2,@"itemId":@"authored-video"}},@{@"binding":@{@"intentId":@1,@"sessionId":@"snapshot-session",@"generation":@1,@"itemId":@"another-item"}}]){
  NSMutableDictionary *bad=[source mutableCopy];[bad addEntriesFromDictionary:wrong];NSDictionary *result=capture(module,@"wrong-fence",bad,&failure);ok=expect(!result&&[failure isEqual:@"snapshot_stale"],"wrong tag/lease/generation rejected")&&ok;
 }
 // Hidden output recovery is asynchronous: retirement must cancel only this capture,
 // never mutate the current player's output, item or clock.
 view.bounds=CGRectZero;view.hidden=YES;[view layoutIfNeeded];
 ok=expect(waitFor(^BOOL{return controller.currentSourcePosition>during+.6;},1),"retire the cached frame before recovery cancellation")&&ok;
 [controller setValue:nil forKey:@"videoOutput"];
 __block BOOL expired=NO,lateResolved=NO;
 [module capture:@"elapsed-deadline" source:source resolver:^(id value){lateResolved=YES;} rejecter:^(NSString *code,NSString *message,NSError *error){expired=[code isEqual:@"snapshot_timeout"];}];
 id deadlineFlight=[module valueForKey:@"flight"];
 NSTimeInterval remaining=[[deadlineFlight valueForKey:@"deadline"]doubleValue]-NSProcessInfo.processInfo.systemUptime;
 ok=expect(remaining>1.4&&remaining<=1.5,"recovery keeps the original 1500 ms total deadline")&&ok;
 // Model elapsed scheduling time while a genuine decoder request is in flight.
 // Its real completion must reject rather than publish pixels past that deadline.
 [deadlineFlight setValue:@(NSProcessInfo.processInfo.systemUptime-1) forKey:@"deadline"];
 ok=expect(waitFor(^BOOL{return expired;},.5)&&!lateResolved&&![module valueForKey:@"flight"]&&![deadlineFlight valueForKey:@"generator"],"elapsed recovery deadline cancels only its generator and frees the next request")&&ok;
 __block BOOL cancelled=NO,resolved=NO;
 [module capture:@"cancelled" source:source resolver:^(id value){resolved=YES;} rejecter:^(NSString *code,NSString *message,NSError *error){cancelled=[code isEqual:@"snapshot_cancelled"];}];
 ok=expect([[module valueForKey:@"flight"]valueForKey:@"generator"]!=nil,"cancellation exercises an actual in-flight asset generator")&&ok;
 ok=expect(release(module,@"cancelled")&&cancelled&&!resolved,"in-flight cancellation rejects before encoding callback")&&ok;
 [controller setValue:originalOutput forKey:@"videoOutput"];
 view.hidden=NO;view.bounds=CGRectMake(0,0,320,180);[view layoutIfNeeded];
 // Cancellation rejects before encoding necessarily finishes. Wait for the actual next
 // accepted real capture, rather than assuming a fixed sleep drained the worker.
 __block NSDictionary *retained=nil;waitFor(^BOOL{if(!retained)retained=capture(module,@"lease-cleanup",source,&failure);return retained!=nil;},2);NSString *retainedPath=receiptPath(retained);
 ok=expect(retained!=nil,"post-cancel real capture succeeds")&&ok;
 NSMutableDictionary *nextBinding=[binding mutableCopy];nextBinding[@"generation"]=@2;
 NSMutableDictionary *nextCommand=[command mutableCopy];nextCommand[@"binding"]=nextBinding;nextCommand[@"commandRevision"]=@2;
 ok=expect(![controller acceptCommand:nextCommand]&&![files fileExistsAtPath:retainedPath],"generation replacement deletes old retained JPEG")&&ok;
 NSMutableDictionary *nextSource=[source mutableCopy];NSMutableDictionary *nextSourceBinding=[source[@"binding"]mutableCopy];nextSourceBinding[@"generation"]=@2;nextSource[@"binding"]=nextSourceBinding;
 view.hidden=YES;view.bounds=CGRectZero;[view layoutIfNeeded];
 double replacementStart=controller.currentSourcePosition;
 ok=expect(waitFor(^BOOL{return controller.currentSourcePosition>replacementStart+.35;},1),"age old output before in-flight replacement")&&ok;
 [controller setValue:nil forKey:@"videoOutput"];
 __block BOOL replacedStale=NO,replacedResolved=NO;
 [module capture:@"replaced-recovery" source:nextSource resolver:^(id value){replacedResolved=YES;} rejecter:^(NSString *code,NSString *message,NSError *error){replacedStale=[code isEqual:@"snapshot_stale"];}];
 nextBinding[@"generation"]=@3;nextCommand[@"binding"]=nextBinding;nextCommand[@"commandRevision"]=@3;
 ok=expect(![controller acceptCommand:nextCommand]&&replacedStale&&!replacedResolved,"in-flight old generation retires without adopting successor pixels")&&ok;
 [controller setValue:originalOutput forKey:@"videoOutput"];
 nextSourceBinding[@"generation"]=@3;nextSource[@"binding"]=nextSourceBinding;
 view.hidden=NO;view.bounds=CGRectMake(0,0,320,180);[view layoutIfNeeded];
 // Item identity is independent of the public binding. Retire the captured
 // item witness while a real generator is pending, leaving all other fences
 // and the live player untouched; its completion must not adopt these pixels.
 [controller setValue:nil forKey:@"videoOutput"];
 __block BOOL itemStale=NO,itemResolved=NO;
 [module capture:@"same-binding-item-retired" source:nextSource resolver:^(id value){itemResolved=YES;} rejecter:^(NSString *code,NSString *message,NSError *error){itemStale=[code isEqual:@"snapshot_stale"];}];
 id itemFlight=[module valueForKey:@"flight"];
 ok=expect(itemFlight&&[itemFlight valueForKey:@"generator"]&&[controller.externalPresentationBinding isEqual:nextBinding],"item-fence test starts as an admitted real decoder request")&&ok;
 [itemFlight setValue:[AVPlayerItem playerItemWithAsset:item.asset] forKey:@"item"];
 ok=expect(waitFor(^BOOL{return itemStale;},.5)&&!itemResolved&&player.currentItem==item&&[controller.externalPresentationBinding isEqual:nextBinding],"different item witness rejects completion with identical live binding")&&ok;
 [controller setValue:originalOutput forKey:@"videoOutput"];
 __block NSDictionary *unmountImage=nil;waitFor(^BOOL{if(!unmountImage)unmountImage=capture(module,@"unmount-cleanup",nextSource,&failure);return unmountImage!=nil;},1);
 NSString *unmountPath=receiptPath(unmountImage);[view removeFromSuperview];
 ok=expect(unmountImage&&![files fileExistsAtPath:unmountPath],"actual view detachment deletes retained image")&&ok;
 [window.rootViewController.view addSubview:view];
 // Passive file transfer must be admitted BEFORE the original native attachment leaves.
 __block NSDictionary *exitImage=nil;waitFor(^BOOL{if(!exitImage)exitImage=capture(module,@"passive-exit",nextSource,&failure);return exitImage!=nil;},1);
 NSString *exitPath=receiptPath(exitImage);__block BOOL exitAccepted=NO,wrongAccepted=YES,duplicateAccepted=YES;
 NSMutableDictionary *wrongSource=[nextSource mutableCopy];wrongSource[@"reactTag"]=@43;
 [module retainForExit:@"passive-exit" source:wrongSource resolver:^(id value){wrongAccepted=[value boolValue];} rejecter:^(NSString *code,NSString *message,NSError *error){}];
 [module retainForExit:@"passive-exit" source:nextSource resolver:^(id value){exitAccepted=[value boolValue];} rejecter:^(NSString *code,NSString *message,NSError *error){}];
 [module retainForExit:@"passive-exit" source:nextSource resolver:^(id value){duplicateAccepted=[value boolValue];} rejecter:^(NSString *code,NSString *message,NSError *error){}];
 [view removeFromSuperview];
 ok=expect(exitImage&&!wrongAccepted&&exitAccepted&&!duplicateAccepted&&[files fileExistsAtPath:exitPath],"exact once-only transfer keeps original pixels across native source detach")&&ok;
 ok=expect(release(module,@"passive-exit")&&![files fileExistsAtPath:exitPath]&&!release(module,@"passive-exit"),"passive resource has one exact release without successor lookup")&&ok;
 ok=expect([snapshotRetirements.lastObject[@"id"]isEqual:@"passive-exit"],"native retirement reports the original file id only")&&ok;
 [window.rootViewController.view addSubview:view];
 __block NSDictionary *leaseImage=nil;waitFor(^BOOL{if(!leaseImage)leaseImage=capture(module,@"lease-cleanup-final",nextSource,&failure);return leaseImage!=nil;},1);
 retainedPath=receiptPath(leaseImage);ok=expect(leaseImage!=nil,"reattached same source provides real frame")&&ok;
 [controller releaseLease:lease[@"lease"]];
 ok=expect(![files fileExistsAtPath:retainedPath]&&!release(module,@"lease-cleanup-final"),"lease loss deletes retained image exactly once")&&ok;
 [module invalidate];ok=expect(waitFor(^BOOL{return ![files fileExistsAtPath:privateDirectory];},1),"invalidation removes the owned private snapshot directory")&&ok;[view removeFromSuperview];[controller invalidate];[files removeItemAtURL:local error:nil];
 fprintf(stderr,"assertions=%lu failures=%lu naturalOutputStarvation=%d recoverySeconds=%.4f\n",(unsigned long)assertions,(unsigned long)failures,naturallyStarved,recoveryDuration);
 printf("{\"decodedMissingBoundsPass\":%s,\"beforeSeconds\":%.3f,\"duringSeconds\":%.3f,\"redPixel\":%s,\"bluePixel\":%s,\"sameViewTagControllerPlayerItemLeaseBinding\":true,\"frame9VisualAcceptance\":false}\n",ok?"true":"false",before,during,[[NSString alloc]initWithData:[NSJSONSerialization dataWithJSONObject:redPixel options:0 error:nil]encoding:NSUTF8StringEncoding].UTF8String,[[NSString alloc]initWithData:[NSJSONSerialization dataWithJSONObject:bluePixel options:0 error:nil]encoding:NSUTF8StringEncoding].UTF8String);return ok?0:1;
}}
@interface PorticoSnapshotTestScene : UIResponder<UIWindowSceneDelegate>
@property(nonatomic,strong) UIWindow *window;
@end
@implementation PorticoSnapshotTestScene
- (void)scene:(UIScene *)scene willConnectToSession:(UISceneSession *)session options:(UISceneConnectionOptions *)options {
 self.window=[[UIWindow alloc]initWithWindowScene:(UIWindowScene *)scene];self.window.rootViewController=[UIViewController new];[self.window makeKeyAndVisible];
 [NSTimer scheduledTimerWithTimeInterval:1 repeats:NO block:^(NSTimer *timer){int result=runTests(self.window);NSURL *file=[[NSFileManager.defaultManager URLsForDirectory:NSDocumentDirectory inDomains:NSUserDomainMask].firstObject URLByAppendingPathComponent:@"result.json"];[[NSJSONSerialization dataWithJSONObject:@{@"exit":@(result)} options:0 error:nil]writeToURL:file atomically:YES];exit(result);}];
}
@end
@interface PorticoSnapshotTestApp : UIResponder<UIApplicationDelegate> @end
@implementation PorticoSnapshotTestApp
- (BOOL)application:(UIApplication *)application didFinishLaunchingWithOptions:(NSDictionary *)options {return YES;}
@end
int main(int argc,char **argv){@autoreleasepool{setvbuf(stdout,NULL,_IONBF,0);return UIApplicationMain(argc,argv,nil,NSStringFromClass(PorticoSnapshotTestApp.class));}}
