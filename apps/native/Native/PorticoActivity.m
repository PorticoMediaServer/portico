#import <React/RCTBridgeModule.h>
#import <CoreSpotlight/CoreSpotlight.h>
#import <UniformTypeIdentifiers/UniformTypeIdentifiers.h>
#import <UIKit/UIKit.h>

/**
 * Handoff, Spotlight and Siri's title list (OS-08, iOS/iPadOS).
 *
 * - The title on screen is advertised as an `NSUserActivity`: Handoff offers it on a Mac (the
 *   page in Portico on the web, when the app knows its address), Spotlight and Siri Suggestions
 *   can bring it back, and continuing it opens `portico://item/{id}`.
 * - Continue Watching and Saved are indexed in Spotlight (by title only: no artwork, no server
 *   address, nothing that identifies the server), and written to a small local file the App
 *   Intents read so "Play <title> in Portico" knows what's there.
 * - `clear` removes everything on sign-out or a profile switch.
 */
@interface PorticoActivity : NSObject <RCTBridgeModule> @end

static NSString *const PorticoActivityType = @"tv.getportico.portico.title";
static NSString *const PorticoContinueDomain = @"tv.getportico.continue";
static NSString *const PorticoSavedDomain = @"tv.getportico.saved";

static BOOL PorticoActivityText(id value, NSUInteger limit) {
  if (![value isKindOfClass:NSString.class] || [value length] == 0 || [value length] > limit) return NO;
  for (NSUInteger i = 0; i < [value length]; i++) { unichar c = [value characterAtIndex:i]; if (c < 32 || c == 127) return NO; }
  return YES;
}
static BOOL PorticoActivityIdentifier(id value) {
  return PorticoActivityText(value, 128) && [value rangeOfString:@"^[A-Za-z0-9_-]+$" options:NSRegularExpressionSearch].location != NSNotFound;
}
/** A clean `{itemId, title, subtitle?, webUrl?}` or nil. */
static NSDictionary *PorticoActivityTitle(id raw) {
  if (![raw isKindOfClass:NSDictionary.class] || !PorticoActivityIdentifier(raw[@"itemId"]) || !PorticoActivityText(raw[@"title"], 512)) return nil;
  NSMutableDictionary *out = [@{@"itemId": raw[@"itemId"], @"title": raw[@"title"]} mutableCopy];
  if (PorticoActivityText(raw[@"subtitle"], 512)) out[@"subtitle"] = raw[@"subtitle"];
  if (PorticoActivityText(raw[@"webUrl"], 2048)) {
    NSURL *url = [NSURL URLWithString:raw[@"webUrl"]];
    if ([url.scheme isEqual:@"https"] && url.host.length && !url.user && !url.password) out[@"webUrl"] = url.absoluteString;
  }
  return out;
}

@implementation PorticoActivity { NSUserActivity *_current; }
RCT_EXPORT_MODULE()
+ (BOOL)requiresMainQueueSetup { return YES; }
- (dispatch_queue_t)methodQueue { return dispatch_get_main_queue(); }

+ (NSURL *)siriTitlesURL {
  NSURL *support = [NSFileManager.defaultManager URLsForDirectory:NSApplicationSupportDirectory inDomains:NSUserDomainMask].firstObject;
  return [[support URLByAppendingPathComponent:@"PorticoSiri" isDirectory:YES] URLByAppendingPathComponent:@"titles.json"];
}

RCT_EXPORT_METHOD(setCurrent:(id)raw) {
  NSDictionary *title = PorticoActivityTitle(raw);
  if (!title) { [_current resignCurrent]; _current = nil; return; }
  if ([_current.userInfo[@"itemId"] isEqual:title[@"itemId"]] && [_current.title isEqual:title[@"title"]]) { [_current becomeCurrent]; return; }
  [_current invalidate];
  NSUserActivity *activity = [[NSUserActivity alloc] initWithActivityType:PorticoActivityType];
  activity.title = title[@"title"];
  activity.userInfo = @{@"itemId": title[@"itemId"]};
  activity.requiredUserInfoKeys = [NSSet setWithObject:@"itemId"];
  activity.eligibleForHandoff = YES;
  activity.eligibleForSearch = YES;
  activity.eligibleForPublicIndexing = NO;
  if (@available(iOS 12.0, *)) { activity.eligibleForPrediction = YES; activity.persistentIdentifier = title[@"itemId"]; }
  if (title[@"webUrl"]) activity.webpageURL = [NSURL URLWithString:title[@"webUrl"]];
  CSSearchableItemAttributeSet *attributes = [[CSSearchableItemAttributeSet alloc] initWithContentType:UTTypeContent];
  attributes.title = title[@"title"];
  attributes.contentDescription = title[@"subtitle"];
  activity.contentAttributeSet = attributes;
  _current = activity;
  [activity becomeCurrent];
}

RCT_EXPORT_METHOD(indexTitles:(NSDictionary *)raw) {
  if (![raw isKindOfClass:NSDictionary.class]) return;
  NSDictionary *labels=[raw[@"labels"] isKindOfClass:NSDictionary.class]?raw[@"labels"]:@{};
  NSMutableArray<CSSearchableItem *> *items = [NSMutableArray new];
  NSMutableDictionary *cache = [NSMutableDictionary new];
  NSDictionary *lists = @{@"continueWatching": PorticoContinueDomain, @"saved": PorticoSavedDomain};
  for (NSString *key in lists) {
    NSMutableArray *clean = [NSMutableArray new];
    NSArray *given = [raw[key] isKindOfClass:NSArray.class] ? raw[key] : @[];
    for (id entry in [given subarrayWithRange:NSMakeRange(0, MIN((NSUInteger)50, given.count))]) {
      NSDictionary *title = PorticoActivityTitle(entry);
      if (!title) continue;
      [clean addObject:@{@"itemId": title[@"itemId"], @"title": title[@"title"], @"subtitle": title[@"subtitle"] ?: @""}];
      CSSearchableItemAttributeSet *attributes = [[CSSearchableItemAttributeSet alloc] initWithContentType:UTTypeContent];
      attributes.title = title[@"title"];
      attributes.contentDescription = PorticoActivityText(labels[key],512)?labels[key]:nil;
      CSSearchableItem *item = [[CSSearchableItem alloc] initWithUniqueIdentifier:[NSString stringWithFormat:@"%@:%@", key, title[@"itemId"]] domainIdentifier:lists[key] attributeSet:attributes];
      item.expirationDate = [NSDate dateWithTimeIntervalSinceNow:30 * 86400];
      [items addObject:item];
    }
    cache[key] = clean;
  }
  CSSearchableIndex *index = CSSearchableIndex.defaultSearchableIndex;
  [index deleteSearchableItemsWithDomainIdentifiers:lists.allValues completionHandler:^(NSError *error) {
    if (items.count) [index indexSearchableItems:items completionHandler:nil];
  }];
  NSURL *file = [PorticoActivity siriTitlesURL];
  [NSFileManager.defaultManager createDirectoryAtURL:file.URLByDeletingLastPathComponent withIntermediateDirectories:YES attributes:nil error:nil];
  NSData *json = [NSJSONSerialization dataWithJSONObject:cache options:0 error:nil];
  if (json) [json writeToURL:file options:NSDataWritingAtomic | NSDataWritingFileProtectionCompleteUntilFirstUserAuthentication error:nil];
  // Refresh the titles Siri offers in "Play … in Portico" (implemented in PorticoIntents.swift).
  Class bridge = NSClassFromString(@"PorticoIntentsBridge");
  if ([bridge respondsToSelector:NSSelectorFromString(@"titlesChanged")]) [bridge performSelector:NSSelectorFromString(@"titlesChanged")];
}

RCT_EXPORT_METHOD(clear) {
  [_current invalidate]; _current = nil;
  [CSSearchableIndex.defaultSearchableIndex deleteSearchableItemsWithDomainIdentifiers:@[PorticoContinueDomain, PorticoSavedDomain] completionHandler:nil];
  if (@available(iOS 12.0, *)) [NSUserActivity deleteAllSavedUserActivitiesWithCompletionHandler:^{}];
  [NSFileManager.defaultManager removeItemAtURL:[PorticoActivity siriTitlesURL] error:nil];
  Class bridge = NSClassFromString(@"PorticoIntentsBridge");
  if ([bridge respondsToSelector:NSSelectorFromString(@"titlesChanged")]) [bridge performSelector:NSSelectorFromString(@"titlesChanged")];
}
@end
