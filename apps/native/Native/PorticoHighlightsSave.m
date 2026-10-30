#import <React/RCTBridgeModule.h>
#import <React/RCTUtils.h>
#import <UIKit/UIKit.h>
#import <TargetConditionals.h>

#if !TARGET_OS_TV
@interface PorticoHighlightsOperation : NSObject
@property(nonatomic,copy) NSString *identifier;
@property(nonatomic,copy) NSString *kind;
@property(nonatomic,strong) NSURL *directory;
@property(nonatomic,strong) UIDocumentPickerViewController *picker;
@property(nonatomic,copy) RCTPromiseResolveBlock resolve;
@property(nonatomic,copy) RCTPromiseRejectBlock reject;
@property(nonatomic) BOOL finished;
@end
@implementation PorticoHighlightsOperation @end
@interface PorticoHighlightsSave : NSObject <RCTBridgeModule, UIDocumentPickerDelegate>
@property(nonatomic) BOOL closed;
@property(nonatomic,strong) PorticoHighlightsOperation *operation;
@end
@implementation PorticoHighlightsSave
RCT_EXPORT_MODULE()
+ (BOOL)requiresMainQueueSetup { return YES; }
- (dispatch_queue_t)methodQueue { return dispatch_get_main_queue(); }
// All admission, presentation, terminal and result decisions use the same lock.
- (void)finish:(PorticoHighlightsOperation *)original result:(NSString *)result error:(BOOL)failed {
  @synchronized(self) {
    if(!original||original.finished||self.operation!=original) return;
    original.finished=YES;
    RCTPromiseResolveBlock resolve=original.resolve; RCTPromiseRejectBlock reject=original.reject;
    original.resolve=nil; original.reject=nil;
    original.picker.delegate=nil;
    self.operation=nil;
    // Only the original app-created private directory is a cleanup target.
    if(original.directory) [[NSFileManager defaultManager] removeItemAtURL:original.directory error:nil];
    original.directory=nil; original.picker=nil;
    if(failed) { if(reject) reject(@"highlights_save_failed",@"Could not save highlights",nil); }
    else if(resolve) resolve(result);
  }
}
RCT_REMAP_METHOD(saveHighlights, saveHighlights:(NSString *)operation name:(NSString *)name markdown:(NSString *)markdown resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
  @synchronized(self) {
  if(self.closed) { reject(@"highlights_unavailable",@"Save unavailable",nil); return; }
  NSRegularExpression *ids=[NSRegularExpression regularExpressionWithPattern:@"^[A-Za-z0-9_-]{32}$" options:0 error:nil];
  NSRegularExpression *names=[NSRegularExpression regularExpressionWithPattern:@"^[A-Za-z0-9 -]{1,64}-highlights\\.md$" options:0 error:nil];
  if(![operation isKindOfClass:NSString.class]||![name isKindOfClass:NSString.class]||![markdown isKindOfClass:NSString.class]||operation.length!=32||name.length>78||markdown.length>4194304||![ids numberOfMatchesInString:operation options:0 range:NSMakeRange(0,operation.length)]||![names numberOfMatchesInString:name options:0 range:NSMakeRange(0,name.length)]||![markdown hasPrefix:@"# Highlights\n"]) { reject(@"invalid_highlights",@"Invalid highlights export",nil); return; }
  NSData *bytes=[markdown dataUsingEncoding:NSUTF8StringEncoding];
  UIViewController *presenter=RCTPresentedViewController();
  if(self.operation||!presenter||!bytes||bytes.length>4194304) { reject(@"highlights_unavailable",@"Save unavailable",nil); return; }
  NSURL *directory=[[NSURL fileURLWithPath:NSTemporaryDirectory() isDirectory:YES] URLByAppendingPathComponent:[@"portico-highlights-" stringByAppendingString:NSUUID.UUID.UUIDString] isDirectory:YES];
  NSError *error=nil;
  if(![[NSFileManager defaultManager] createDirectoryAtURL:directory withIntermediateDirectories:NO attributes:@{NSFileProtectionKey:NSFileProtectionComplete} error:&error]) { reject(@"highlights_save_failed",@"Could not prepare highlights",nil); return; }
  PorticoHighlightsOperation *original=[PorticoHighlightsOperation new];
  original.identifier=operation; original.directory=directory; original.resolve=resolve; original.reject=reject; self.operation=original;
  [directory setResourceValue:@YES forKey:NSURLIsExcludedFromBackupKey error:nil];
  NSURL *file=[directory URLByAppendingPathComponent:name isDirectory:NO];
  if(![bytes writeToURL:file options:NSDataWritingAtomic|NSDataWritingFileProtectionComplete error:&error]) { [self finish:original result:nil error:YES]; return; }
  UIDocumentPickerViewController *picker=[[UIDocumentPickerViewController alloc] initForExportingURLs:@[file] asCopy:YES];
  picker.delegate=self; original.picker=picker;
  if(self.closed||self.operation!=original||original.finished) { [self finish:original result:@"cancelled" error:NO]; return; }
  [presenter presentViewController:picker animated:YES completion:nil];
  }
}
// The diagnostics entry accepts only bounded typed JSON and fixed filenames.
RCT_REMAP_METHOD(saveDiagnostics, saveDiagnostics:(NSString *)operation kind:(NSString *)kind json:(NSString *)json resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
  @synchronized(self) {
    if(self.closed||self.operation) { reject(@"diagnostics_unavailable",@"Save unavailable",nil); return; }
    if(![operation isKindOfClass:NSString.class]||operation.length!=32||[operation rangeOfString:@"^[A-Za-z0-9_-]{32}$" options:NSRegularExpressionSearch].location==NSNotFound||![kind isKindOfClass:NSString.class]||![@[@"report",@"support"] containsObject:kind]||![json isKindOfClass:NSString.class]||json.length>8388608) { reject(@"invalid_diagnostics",@"Invalid diagnostics export",nil); return; }
    NSData *bytes=[json dataUsingEncoding:NSUTF8StringEncoding];
    if(!bytes||bytes.length>8388608) { reject(@"invalid_diagnostics",@"Invalid diagnostics export",nil); return; }
    id document=[NSJSONSerialization JSONObjectWithData:bytes options:0 error:nil];
    if(![document isKindOfClass:NSDictionary.class]||![document[@"id"] isKindOfClass:NSString.class]||([kind isEqual:@"report"]&&![document[@"body"] isKindOfClass:NSString.class])||([kind isEqual:@"support"]&&![document[@"manifest"] isKindOfClass:NSDictionary.class])) { reject(@"invalid_diagnostics",@"Invalid diagnostics export",nil); return; }
    UIViewController *presenter=RCTPresentedViewController();
    if(!presenter) { reject(@"diagnostics_unavailable",@"Save unavailable",nil); return; }
    static NSMutableSet<NSString *> *issued; static dispatch_once_t once;
    dispatch_once(&once, ^{issued=[NSMutableSet new];});
    @synchronized(issued) { if(issued.count>=4096||[issued containsObject:operation]) { reject(@"diagnostics_unavailable",@"Save unavailable",nil); return; } [issued addObject:operation]; }
    NSURL *directory=[[NSURL fileURLWithPath:NSTemporaryDirectory() isDirectory:YES] URLByAppendingPathComponent:[@"portico-diagnostics-" stringByAppendingString:NSUUID.UUID.UUIDString] isDirectory:YES];
    NSError *error=nil;
    if(![[NSFileManager defaultManager] createDirectoryAtURL:directory withIntermediateDirectories:NO attributes:@{NSFileProtectionKey:NSFileProtectionComplete} error:&error]) { reject(@"diagnostics_save_failed",@"Could not prepare diagnostics",nil); return; }
    PorticoHighlightsOperation *original=[PorticoHighlightsOperation new];original.identifier=operation;original.kind=@"diagnostics";original.directory=directory;original.resolve=resolve;original.reject=reject;self.operation=original;
    [directory setResourceValue:@YES forKey:NSURLIsExcludedFromBackupKey error:nil];
    NSURL *file=[directory URLByAppendingPathComponent:[NSString stringWithFormat:@"portico-%@.json",kind] isDirectory:NO];
    if(![bytes writeToURL:file options:NSDataWritingAtomic|NSDataWritingFileProtectionComplete error:&error]) { [self finish:original result:nil error:YES]; return; }
    UIDocumentPickerViewController *picker=[[UIDocumentPickerViewController alloc] initForExportingURLs:@[file] asCopy:YES];picker.delegate=self;original.picker=picker;
    if(self.closed||self.operation!=original||original.finished) { [self finish:original result:@"cancelled" error:NO]; return; }
    [presenter presentViewController:picker animated:YES completion:nil];
  }
}
// The invitation card as a PNG, at most 4 MB, under one fixed name.
RCT_REMAP_METHOD(saveInvitationPicture, saveInvitationPicture:(NSString *)operation png:(NSString *)png resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
  @synchronized(self) {
    if(self.closed||self.operation) { reject(@"invitation_picture_unavailable",@"Save unavailable",nil); return; }
    if(![operation isKindOfClass:NSString.class]||operation.length!=32||[operation rangeOfString:@"^[A-Za-z0-9_-]{32}$" options:NSRegularExpressionSearch].location==NSNotFound||![png isKindOfClass:NSString.class]||png.length>5600000) { reject(@"invalid_invitation_picture",@"Invalid invitation picture",nil); return; }
    NSData *bytes=[[NSData alloc] initWithBase64EncodedString:png options:0];
    static const unsigned char signature[8]={0x89,0x50,0x4E,0x47,0x0D,0x0A,0x1A,0x0A};
    if(!bytes||bytes.length<8||bytes.length>4194304||memcmp(bytes.bytes,signature,8)!=0) { reject(@"invalid_invitation_picture",@"Invalid invitation picture",nil); return; }
    UIViewController *presenter=RCTPresentedViewController();
    if(!presenter) { reject(@"invitation_picture_unavailable",@"Save unavailable",nil); return; }
    NSURL *directory=[[NSURL fileURLWithPath:NSTemporaryDirectory() isDirectory:YES] URLByAppendingPathComponent:[@"portico-invitation-" stringByAppendingString:NSUUID.UUID.UUIDString] isDirectory:YES];
    NSError *error=nil;
    if(![[NSFileManager defaultManager] createDirectoryAtURL:directory withIntermediateDirectories:NO attributes:@{NSFileProtectionKey:NSFileProtectionComplete} error:&error]) { reject(@"invitation_picture_save_failed",@"Could not prepare the picture",nil); return; }
    PorticoHighlightsOperation *original=[PorticoHighlightsOperation new];original.identifier=operation;original.kind=@"invitation";original.directory=directory;original.resolve=resolve;original.reject=reject;self.operation=original;
    [directory setResourceValue:@YES forKey:NSURLIsExcludedFromBackupKey error:nil];
    NSURL *file=[directory URLByAppendingPathComponent:@"portico-invitation.png" isDirectory:NO];
    if(![bytes writeToURL:file options:NSDataWritingAtomic|NSDataWritingFileProtectionComplete error:&error]) { [self finish:original result:nil error:YES]; return; }
    UIDocumentPickerViewController *picker=[[UIDocumentPickerViewController alloc] initForExportingURLs:@[file] asCopy:YES];picker.delegate=self;original.picker=picker;
    if(self.closed||self.operation!=original||original.finished) { [self finish:original result:@"cancelled" error:NO]; return; }
    [presenter presentViewController:picker animated:YES completion:nil];
  }
}
RCT_REMAP_METHOD(cancelInvitationPicture, cancelInvitationPicture:(NSString *)identifier) {
  @synchronized(self) {
    PorticoHighlightsOperation *original=self.operation;
    if(!original||![original.kind isEqual:@"invitation"]||![identifier isEqual:original.identifier]) return;
    [original.picker dismissViewControllerAnimated:YES completion:nil];[self finish:original result:@"cancelled" error:NO];
  }
}
RCT_REMAP_METHOD(cancelDiagnostics, cancelDiagnostics:(NSString *)identifier) {
  @synchronized(self) {
    PorticoHighlightsOperation *original=self.operation;
    if(!original||![original.kind isEqual:@"diagnostics"]||![identifier isEqual:original.identifier]) return;
    [original.picker dismissViewControllerAnimated:YES completion:nil];[self finish:original result:@"cancelled" error:NO];
  }
}
RCT_REMAP_METHOD(cancelHighlights, cancelHighlights:(NSString *)identifier) {
  @synchronized(self) {
    PorticoHighlightsOperation *original=self.operation;
    if(!original||original.kind||![identifier isEqual:original.identifier]) return;
    [original.picker dismissViewControllerAnimated:YES completion:nil];
    [self finish:original result:@"cancelled" error:NO];
  }
}
- (void)documentPickerWasCancelled:(UIDocumentPickerViewController *)controller {
  @synchronized(self) {
    PorticoHighlightsOperation *original=self.operation;
    if(controller!=original.picker) return;
    [self finish:original result:@"cancelled" error:NO];
  }
}
- (void)documentPicker:(UIDocumentPickerViewController *)controller didPickDocumentsAtURLs:(NSArray<NSURL *> *)urls {
  @synchronized(self) {
    PorticoHighlightsOperation *original=self.operation;
    if(!original||controller!=original.picker) return;
    // The OS-owned destination is never retained or deleted.
    [self finish:original result:!self.closed&&urls.count ? @"saved" : @"cancelled" error:NO];
  }
}
- (void)invalidate {
  // Close admission synchronously even when React invalidates off the main queue.
  PorticoHighlightsOperation *original;
  @synchronized(self) { self.closed=YES; original=self.operation; }
  dispatch_async(dispatch_get_main_queue(), ^{
    @synchronized(self) {
      if(!original||self.operation!=original||original.finished) return;
      [original.picker dismissViewControllerAnimated:NO completion:nil];
      [self finish:original result:@"cancelled" error:NO];
    }
  });
}
@end
#endif
