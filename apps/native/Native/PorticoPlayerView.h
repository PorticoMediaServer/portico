#import <UIKit/UIKit.h>
#import <AVKit/AVKit.h>
@class PorticoPlaybackController;
@interface PorticoPlayerView : UIView <AVPictureInPictureControllerDelegate>
@property(nonatomic,copy) NSString *lease;
@property(nonatomic) NSInteger pictureInPictureRequest;
@property(nonatomic,copy) NSDictionary *pictureInPictureBinding;
/** Settings › Device › Start Picture in Picture automatically. YES until JavaScript says otherwise. */
@property(nonatomic) BOOL autoPictureInPicture;
@property(nonatomic,strong) AVPictureInPictureController *pictureInPicture;
@property(nonatomic,strong) PorticoPlaybackController *controller;
@end
