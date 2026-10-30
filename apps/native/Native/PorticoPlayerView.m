#import <React/RCTViewManager.h>
#import <React/RCTBridge.h>
#import <React/UIView+React.h>
#import <MediaAccessibility/MediaAccessibility.h>
#import <QuartzCore/QuartzCore.h>
#import "PorticoPlayerView.h"
#import "PorticoPlaybackController.h"
#import "PorticoSubtitleSelection.h"
#import "PorticoSubtitleOverlay.h"
#import "PorticoVideoSnapshot.h"
static NSMapTable<NSNumber *,UIView *> *PorticoSnapshotViews;
@implementation PorticoPlayerView { AVPlayerLayer *_layer; UILabel *_subtitle; UIImageView *_richSubtitle; CADisplayLink *_subtitleClock; BOOL _presentationMissing; NSDictionary *_activePictureBinding;NSInteger _submittedPictureRequest; }
- (void)setReactTag:(NSNumber *)tag {
 if(self.reactTag&&[PorticoSnapshotViews objectForKey:self.reactTag]==self)[PorticoSnapshotViews removeObjectForKey:self.reactTag];
 [super setReactTag:tag];
 if(!PorticoSnapshotViews)PorticoSnapshotViews=[NSMapTable strongToWeakObjectsMapTable];
 if(tag)[PorticoSnapshotViews setObject:self forKey:tag];
 [NSNotificationCenter.defaultCenter postNotificationName:@"PorticoVideoSnapshotSourceChanged" object:self];
}
- (PorticoPlaybackController *)snapshotControllerForLease:(NSString *)lease {
 return self.window&&[lease isEqual:_lease]&&[lease isEqual:_controller.lease]&&_layer.player==_controller.player?_controller:nil;
}
- (instancetype)initWithFrame:(CGRect)frame {if((self=[super initWithFrame:frame])){self.backgroundColor=UIColor.blackColor;_layer=[AVPlayerLayer playerLayerWithPlayer:nil];_layer.videoGravity=AVLayerVideoGravityResizeAspect;[self.layer addSublayer:_layer];self.clipsToBounds=YES;
 _richSubtitle=[UIImageView new];_richSubtitle.userInteractionEnabled=NO;_richSubtitle.layer.contentsFormat=kCAContentsFormatRGBA8Uint;
 if(@available(iOS 17.0,tvOS 17.0,*))_richSubtitle.preferredImageDynamicRange=UIImageDynamicRangeStandard;
 [self addSubview:_richSubtitle];_subtitle=[UILabel new];_subtitle.textColor=UIColor.whiteColor;_subtitle.backgroundColor=[UIColor colorWithWhite:0 alpha:0.7];_subtitle.textAlignment=NSTextAlignmentCenter;_subtitle.numberOfLines=0;_subtitle.userInteractionEnabled=NO;_subtitle.hidden=YES;_subtitle.isAccessibilityElement=YES;[self addSubview:_subtitle];[[NSNotificationCenter defaultCenter]addObserver:self selector:@selector(updateSubtitle) name:@"PorticoPlaybackSubtitleChanged" object:nil];[_layer addObserver:self forKeyPath:@"readyForDisplay" options:NSKeyValueObservingOptionNew context:NULL];[[NSNotificationCenter defaultCenter]addObserver:self selector:@selector(attachLayer) name:@"PorticoPlaybackAttachmentChanged" object:nil];
 // The viewer's caption style in the device's Accessibility settings is followed as it changes.
 [[NSNotificationCenter defaultCenter]addObserver:self selector:@selector(updateSubtitle) name:(__bridge NSString *)kMACaptionAppearanceSettingsChangedNotification object:nil];}return self;}
- (void)setLease:(NSString *)lease {
 if(_lease==lease||[_lease isEqualToString:lease])return;
 _lease=[lease copy];_richSubtitle.image=nil;[self attachLayer];[NSNotificationCenter.defaultCenter postNotificationName:@"PorticoVideoSnapshotSourceChanged" object:self];
}
- (void)setController:(PorticoPlaybackController *)controller {
 if(_controller==controller)return;
 _controller.subtitleOverlay.onImage=nil;_controller=controller;_richSubtitle.image=nil;
 __weak PorticoPlayerView *weakSelf=self;controller.subtitleOverlay.onImage=^(UIImage *image){PorticoPlayerView *v=weakSelf;if(v)v->_richSubtitle.image=(v->_lease.length&&[v->_lease isEqual:v->_controller.lease])?image:nil;};[self attachLayer];[NSNotificationCenter.defaultCenter postNotificationName:@"PorticoVideoSnapshotSourceChanged" object:self];
}
- (void)subtitleFrame {
 if(_presentationMissing&&[self recoverPresentation])_presentationMissing=NO;
 if(!_lease.length||![_lease isEqual:_controller.lease]||!self.window||!_controller.subtitleOverlay.active){_richSubtitle.image=nil;return;}
 CGRect video=_layer.videoRect;if(CGRectIsEmpty(video))return;CGFloat scale=self.window.screen.scale;
 [_controller.subtitleOverlay renderAtSourceTime:_controller.currentSourcePosition size:CGSizeMake(video.size.width*scale,video.size.height*scale)];
}
- (void)attachLayer {
 BOOL valid=_lease.length&&[_lease isEqual:_controller.lease];
 if(!valid){[self.pictureInPicture stopPictureInPicture];self.pictureInPicture=nil;_richSubtitle.image=nil;}
#if TARGET_OS_TV
 if(!valid)self.window.avDisplayManager.preferredDisplayCriteria=nil;
 else if(self.window)_controller.displayWindow=self.window;
#endif
 AVPlayer *player=valid?_controller.player:nil;
 if(_layer.player!=player){_richSubtitle.image=nil;_layer.player=player;}
 [self updateSubtitle];[self preparePictureInPicture];
}
/** OS-04: the controller exists as soon as a video is attached, so leaving the app (Home swipe)
 * can continue the film in Picture in Picture, as the system's own players do. The system's
 * "Start PiP Automatically" setting still decides. This view stays the controller's only owner. */
- (void)preparePictureInPicture {
 if(![AVPictureInPictureController isPictureInPictureSupported]||!_lease.length||![_lease isEqual:_controller.lease]||!_layer.player)return;
 if(!self.pictureInPicture){self.pictureInPicture=[[AVPictureInPictureController alloc]initWithPlayerLayer:_layer];self.pictureInPicture.delegate=self;}
#if !TARGET_OS_TV
 if(@available(iOS 14.2, *))self.pictureInPicture.canStartPictureInPictureAutomaticallyFromInline=!_controller.subtitleOverlayActive;
#endif
 if(_controller.subtitleOverlayActive&&self.pictureInPicture.isPictureInPictureActive)[self.pictureInPicture stopPictureInPicture];
}
// Reported through the playback module's event channel, not a view event prop: old-style view
// events reach JavaScript through RCTEventEmitter, which a New Architecture production bundle
// never registers, so the first one (PiP "unavailable" on the simulator) crashed the app.
- (void)pictureInPictureChanged:(NSDictionary *)change {NSMutableDictionary *body=[change mutableCopy];body[@"lease"]=_lease?:@"";body[@"binding"]=_activePictureBinding?:_pictureInPictureBinding?:NSNull.null;body[@"requestId"]=@(_submittedPictureRequest);if(_controller.onPictureInPicture)_controller.onPictureInPicture(body);}
- (void)setPictureInPictureRequest:(NSInteger)value {if(value==_pictureInPictureRequest)return;_pictureInPictureRequest=value;
 // React may assign the request counter before its companion binding prop. Settle both
 // on the same main loop, then check the actual source-ready native binding again.
 __weak PorticoPlayerView *weakSelf=self;
 dispatch_async(dispatch_get_main_queue(),^{PorticoPlayerView *view=weakSelf;if(!view||view->_pictureInPictureRequest!=value)return;[view requestPictureInPicture:value];});
}
- (void)requestPictureInPicture:(NSInteger)value {
 _submittedPictureRequest=value;
 NSDictionary *current=_controller.externalPresentationBinding;
 if(![_pictureInPictureBinding isKindOfClass:NSDictionary.class]||!current||![_pictureInPictureBinding isEqual:current]){_activePictureBinding=nil;[self pictureInPictureChanged:@{@"state":@"unavailable"}];return;}
 _activePictureBinding=[current copy];
 // tvOS 14+ supports Picture in Picture from an AVPlayerLayer too (OS-04); the TV player decides
 // whether to offer it. Unsupported hardware answers "unavailable" rather than doing nothing.
 if(![AVPictureInPictureController isPictureInPictureSupported]||!_lease.length||![_lease isEqual:_controller.lease]){[self pictureInPictureChanged:@{@"state":@"unavailable"}];return;}
 if(self.pictureInPicture.isPictureInPictureActive){[self.pictureInPicture stopPictureInPicture];return;}
 // The coupled runtime command must finish the same-session burn/WebVTT replan first.
 // UIKit PiP has no composition path for an app-drawn caption or ASS/bitmap overlay.
 if(_controller.subtitleOverlayActive){[self pictureInPictureChanged:@{@"state":@"unavailable",@"subtitlesCarried":@NO}];return;}
 [self preparePictureInPicture];
 if(self.pictureInPicture.isPictureInPicturePossible)[self.pictureInPicture startPictureInPicture];else [self pictureInPictureChanged:@{@"state":@"unavailable"}];
}
// OS-02: `subtitlesCarried` is NO when the chosen subtitles are drawn by the app, which PiP can't
// show; the player can say so instead of letting them vanish silently.
- (void)pictureInPictureControllerDidStartPictureInPicture:(AVPictureInPictureController *)controller {
 if(_controller.subtitleOverlayActive||![_activePictureBinding isEqual:_controller.externalPresentationBinding]){[controller stopPictureInPicture];[self pictureInPictureChanged:@{@"state":@"unavailable",@"subtitlesCarried":@NO}];return;}
 [self pictureInPictureChanged:@{@"state":@"started",@"subtitlesCarried":@YES}];
}
- (void)pictureInPictureControllerWillStartPictureInPicture:(AVPictureInPictureController *)controller {
 if(!_activePictureBinding){_activePictureBinding=[_controller.externalPresentationBinding copy];_submittedPictureRequest=0;}
}
- (void)pictureInPictureControllerDidStopPictureInPicture:(AVPictureInPictureController *)controller {[self pictureInPictureChanged:@{@"state":@"stopped"}];_activePictureBinding=nil;}
- (void)pictureInPictureController:(AVPictureInPictureController *)controller failedToStartPictureInPictureWithError:(NSError *)error {[self pictureInPictureChanged:@{@"state":@"unavailable"}];_activePictureBinding=nil;}
- (void)pictureInPictureController:(AVPictureInPictureController *)controller restoreUserInterfaceForPictureInPictureStopWithCompletionHandler:(void (^)(BOOL restored))completionHandler {[self pictureInPictureChanged:@{@"state":@"restore"}];completionHandler(YES);}
- (void)updateSubtitle {_subtitle.text=(_lease.length&&[_lease isEqual:_controller.lease])?_controller.subtitleText:@"";_subtitle.hidden=!_subtitle.text.length;[self preparePictureInPicture];[self setNeedsLayout];}
/**
 * The look of the line the app draws: Portico's caption style (the profile's size and background,
 * sent with the subtitle command) on top of the system's. The size the viewer set for captions in
 * Accessibility always multiplies. A colour, background or typeface is taken from the system only
 * where the viewer's style there says it overrides what a video provides; otherwise Portico's.
 */
- (void)styleSubtitle:(CGFloat)base {
 [PorticoSubtitleSelection applyCaptionAppearanceToLabel:_subtitle baseSize:base style:_controller.subtitleStyle italic:[_controller.subtitleCue[@"italic"]boolValue]];
}
// A zero-size/hidden presentation can remain stuck on its first frame even
// while the live item clock advances. Replace only that presentation surface;
// keep the exact view, controller, player, item, lease and video output intact.
- (BOOL)recoverPresentation {
 if(self.hidden||CGRectIsEmpty(self.bounds)||!self.window||![_lease isEqual:_controller.lease]||_layer.player!=_controller.player||!_controller.externalPresentationBinding||_controller.player.externalPlaybackActive||self.pictureInPicture.isPictureInPictureActive)return NO;
 AVPlayerLayer *previous=_layer;
 AVPlayerLayer *next=[AVPlayerLayer playerLayerWithPlayer:previous.player];next.videoGravity=previous.videoGravity;next.frame=self.bounds;
 [CATransaction begin];[CATransaction setDisableActions:YES];
 [previous removeObserver:self forKeyPath:@"readyForDisplay"];
 _layer=next;[self.layer insertSublayer:next atIndex:0];[previous removeFromSuperlayer];
 self.pictureInPicture=nil;
 [next addObserver:self forKeyPath:@"readyForDisplay" options:NSKeyValueObservingOptionNew context:NULL];
 [CATransaction commit];[self preparePictureInPicture];return YES;
}
- (void)setHidden:(BOOL)hidden {
 BOOL restoring=self.hidden&&!hidden;[super setHidden:hidden];
 if(hidden)_presentationMissing=YES;
 if(restoring&&[self recoverPresentation])_presentationMissing=NO;
}
- (void)layoutSubviews {
 [super layoutSubviews];
 if(CGRectIsEmpty(self.bounds))_presentationMissing=YES;
 else if(_presentationMissing&&[self recoverPresentation])_presentationMissing=NO;
 if(!CGRectEqualToRect(_layer.frame,self.bounds)){
  // The persistent shell owns the single presentation cross-fade. Resize the same
  // aspect-fit layer immediately, without a second implicit Core Animation transition.
  [CATransaction begin];[CATransaction setDisableActions:YES];_layer.frame=self.bounds;[CATransaction commit];
 }
 CGRect video=_layer.videoRect;if(CGRectIsEmpty(video))video=self.bounds;
 _richSubtitle.frame=video;CGFloat width=video.size.width*0.9;
 [self styleSubtitle:MAX(12,MIN(40,video.size.width*0.035))];
 CGSize size=[_subtitle sizeThatFits:CGSizeMake(width,video.size.height*0.6)];CGFloat height=MIN(size.height+8,video.size.height*0.6);
 _subtitle.frame=CGRectMake(CGRectGetMidX(video)-width/2,(([_controller.subtitleCue[@"top"]boolValue]||[_controller.subtitleStyle[@"position"] isEqual:@"top"])?MAX(CGRectGetMinY(video)+8,self.safeAreaInsets.top):CGRectGetMaxY(video)-MAX(8,video.size.height*([_controller.subtitleStyle[@"position"] isEqual:@"raised"]?.20:.08))-height),width,height);
}
// The display is matched to the title by the controller before the title is announced ready
// (it used to be asked for here, at the first frame, when the title was already playing).
- (void)observeValueForKeyPath:(NSString *)keyPath ofObject:(id)object change:(NSDictionary *)change context:(void *)context {if(object==_layer&&_layer.readyForDisplay)[_controller displayReadyForLease:_lease];}
- (void)didMoveToWindow {[super didMoveToWindow];
 [NSNotificationCenter.defaultCenter postNotificationName:@"PorticoVideoSnapshotSourceChanged" object:self];
 [_subtitleClock invalidate];_subtitleClock=nil;
 if(self.window){_subtitleClock=[CADisplayLink displayLinkWithTarget:self selector:@selector(subtitleFrame)];[_subtitleClock addToRunLoop:NSRunLoop.mainRunLoop forMode:NSRunLoopCommonModes];}
#if TARGET_OS_TV
 if(self.window&&_lease.length&&[_lease isEqual:_controller.lease])_controller.displayWindow=self.window;
#endif
}
- (void)willMoveToWindow:(UIWindow *)newWindow {
#if TARGET_OS_TV
 if(self.window&&self.window!=newWindow)self.window.avDisplayManager.preferredDisplayCriteria=nil;
#endif
 [super willMoveToWindow:newWindow];
}
- (void)dealloc{
 if(self.reactTag&&[PorticoSnapshotViews objectForKey:self.reactTag]==self)[PorticoSnapshotViews removeObjectForKey:self.reactTag];
 [NSNotificationCenter.defaultCenter postNotificationName:@"PorticoVideoSnapshotSourceChanged" object:self];
#if TARGET_OS_TV
 self.window.avDisplayManager.preferredDisplayCriteria=nil;
#endif
 // This view is the app's strong owner of the PiP controller and its delegate is
 // weak, so teardown ends PiP here rather than leaving a controller running
 // against a layer whose player is about to go away. Exactly once: the property
 // is cleared immediately after.
 if(_pictureInPicture){[_pictureInPicture stopPictureInPicture];_pictureInPicture.delegate=nil;_pictureInPicture=nil;}
 [_subtitleClock invalidate];_controller.subtitleOverlay.onImage=nil;[_layer removeObserver:self forKeyPath:@"readyForDisplay"];[[NSNotificationCenter defaultCenter]removeObserver:self];_layer.player=nil;}
@end
PorticoPlaybackController *PorticoSnapshotController(NSNumber *reactTag,NSString *lease) {
 if(!NSThread.isMainThread)return nil;
 PorticoPlayerView *view=(PorticoPlayerView *)[PorticoSnapshotViews objectForKey:reactTag];
 return [view snapshotControllerForLease:lease];
}
@interface PorticoPlayerManager : RCTViewManager @end
@implementation PorticoPlayerManager
RCT_EXPORT_MODULE(PorticoPlayer)
// After a JavaScript reload the new playback module registers itself as the shared controller;
// looking it up through the bridge could hand a new player view the previous, invalidated one.
- (UIView *)view {PorticoPlayerView *view=[PorticoPlayerView new];PorticoPlaybackController *controller=PorticoPlaybackController.shared;if(!controller){id module=[self.bridge moduleForClass:NSClassFromString(@"PorticoPlaybackModule")];controller=[module valueForKey:@"controller"];}view.controller=controller;return view;}
RCT_EXPORT_VIEW_PROPERTY(lease,NSString)
RCT_EXPORT_VIEW_PROPERTY(pictureInPictureRequest,NSInteger)
RCT_EXPORT_VIEW_PROPERTY(pictureInPictureBinding,NSDictionary)
@end
