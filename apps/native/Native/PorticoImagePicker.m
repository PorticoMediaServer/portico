#import <React/RCTBridgeModule.h>
#import <React/RCTUtils.h>
#import <TargetConditionals.h>
#if !TARGET_OS_TV
#import <PhotosUI/PhotosUI.h>
#endif

/** Picks one photo for a profile picture. The system picker runs out of process, so the app
 * never gains access to the photo library and needs no permission prompt. The chosen image is
 * scaled to fit 1024 points and handed back as JPEG bytes; nothing is written to disk. */
#if TARGET_OS_TV
@interface PorticoImagePicker : NSObject <RCTBridgeModule> @end
#else
@interface PorticoImagePicker : NSObject <RCTBridgeModule, PHPickerViewControllerDelegate>
@property(nonatomic,copy) RCTPromiseResolveBlock resolve;
@end
#endif

@implementation PorticoImagePicker
RCT_EXPORT_MODULE()
+ (BOOL)requiresMainQueueSetup { return NO; }

RCT_REMAP_METHOD(pick, pickWithResolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
#if TARGET_OS_TV
 resolve([NSNull null]);
#else
 dispatch_async(dispatch_get_main_queue(), ^{
  if (self.resolve) { self.resolve([NSNull null]); self.resolve = nil; }
  UIViewController *presenter = RCTPresentedViewController();
  if (!presenter) { resolve([NSNull null]); return; }
  PHPickerConfiguration *configuration = [PHPickerConfiguration new];
  configuration.filter = [PHPickerFilter imagesFilter]; configuration.selectionLimit = 1;
  PHPickerViewController *picker = [[PHPickerViewController alloc] initWithConfiguration:configuration];
  picker.delegate = self; self.resolve = resolve;
  [presenter presentViewController:picker animated:YES completion:nil];
 });
#endif
}

#if !TARGET_OS_TV
- (void)picker:(PHPickerViewController *)picker didFinishPicking:(NSArray<PHPickerResult *> *)results {
 [picker dismissViewControllerAnimated:YES completion:nil];
 RCTPromiseResolveBlock resolve = self.resolve; self.resolve = nil;
 if (!resolve) return;
 NSItemProvider *provider = results.firstObject.itemProvider;
 if (!provider || ![provider canLoadObjectOfClass:UIImage.class]) { resolve([NSNull null]); return; }
 [provider loadObjectOfClass:UIImage.class completionHandler:^(id<NSItemProviderReading> object, NSError *error) {
  UIImage *image = [object isKindOfClass:UIImage.class] ? (UIImage *)object : nil;
  if (!image) { resolve([NSNull null]); return; }
  CGFloat longest = MAX(image.size.width, image.size.height), scale = longest > 1024 ? 1024 / longest : 1;
  CGSize size = CGSizeMake(MAX(1, floor(image.size.width * scale)), MAX(1, floor(image.size.height * scale)));
  UIGraphicsImageRendererFormat *format = [UIGraphicsImageRendererFormat defaultFormat]; format.scale = 1; format.opaque = YES;
  UIImage *scaled = [[[UIGraphicsImageRenderer alloc] initWithSize:size format:format] imageWithActions:^(UIGraphicsImageRendererContext *context) {
   [UIColor.blackColor setFill]; [context fillRect:CGRectMake(0, 0, size.width, size.height)];
   [image drawInRect:CGRectMake(0, 0, size.width, size.height)];
  }];
  NSData *jpeg = UIImageJPEGRepresentation(scaled, 0.9);
  resolve(jpeg ? @{@"base64": [jpeg base64EncodedStringWithOptions:0], @"contentType": @"image/jpeg", @"bytes": @(jpeg.length)} : (id)[NSNull null]);
 }];
}
#endif
@end
