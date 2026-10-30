#import <UIKit/UIKit.h>
#import <AVKit/AVKit.h>
@class PorticoPlaybackController;
@interface PorticoPlayerView : UIView <AVPictureInPictureControllerDelegate>
@property(nonatomic,copy) NSString *lease;
@property(nonatomic) NSInteger pictureInPictureRequest;
@property(nonatomic,copy) NSDictionary *pictureInPictureBinding;
@property(nonatomic,strong) AVPictureInPictureController *pictureInPicture;
@property(nonatomic,strong) PorticoPlaybackController *controller;
@end
