#import <React/RCTViewManager.h>
#import <AVKit/AVKit.h>
#import <TargetConditionals.h>
/** The system picker changes AVAudioSession's route, not the playback URL/player. */
@interface PorticoAudioRoutePicker : RCTViewManager @end
@implementation PorticoAudioRoutePicker
RCT_EXPORT_MODULE()
+ (BOOL)requiresMainQueueSetup { return YES; }
- (UIView *)view {
#if TARGET_OS_TV
 // tvOS exposes output choice through system Control Center, not AVRoutePickerView.
 return [UIView new];
#else
 AVRoutePickerView *picker=[AVRoutePickerView new];picker.prioritizesVideoDevices=NO;
 picker.tintColor=UIColor.whiteColor;picker.activeTintColor=UIColor.systemTealColor;
 return picker;
#endif
}
#if !TARGET_OS_TV
/** A video player asks for televisions first; a music player for speakers. */
RCT_CUSTOM_VIEW_PROPERTY(video, BOOL, AVRoutePickerView) {
 BOOL video=json?[RCTConvert BOOL:json]:NO;view.prioritizesVideoDevices=video;
}
#endif
@end

#if !TARGET_OS_TV
#import <MediaPlayer/MediaPlayer.h>
#endif
/** APL-PLAYER-03: the system volume slider (iPhone/iPad), so the player can change the device
 *  volume the way the hardware buttons do. Television has no in-app volume. */
@interface PorticoVolumeView : RCTViewManager @end
@implementation PorticoVolumeView
RCT_EXPORT_MODULE()
+ (BOOL)requiresMainQueueSetup { return YES; }
- (UIView *)view {
#if TARGET_OS_TV
 return [UIView new];
#else
 MPVolumeView *volume=[MPVolumeView new];
 volume.tintColor=UIColor.whiteColor;
 // The route button already lives in Play on; the slider is only the slider.
 if ([volume respondsToSelector:@selector(setShowsRouteButton:)]) {
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
  volume.showsRouteButton=NO;
#pragma clang diagnostic pop
 }
 return volume;
#endif
}
@end
