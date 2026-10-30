#import <AppKit/AppKit.h>
#include <stdint.h>
#include <string.h>

// Completion copies the temporary UTF-8 path immediately on the Rust side.
extern void portico_folder_completed(uint64_t request, const unsigned char *path, size_t length, int status);
static NSMutableDictionary<NSString *, NSOpenPanel *> *panels;

void portico_folder_choose(void *window, const char *operation, const char *initial, uint64_t request) {
    NSWindow *parent = (__bridge NSWindow *)window;
    NSString *key = [NSString stringWithUTF8String:operation];
    if (!parent || !key || panels.count) { portico_folder_completed(request, NULL, 0, 2); return; }
    if (!panels) panels = [NSMutableDictionary new];
    NSOpenPanel *panel = [NSOpenPanel openPanel];
    panel.canChooseDirectories = YES;
    panel.canChooseFiles = NO;
    panel.allowsMultipleSelection = NO;
    panel.canCreateDirectories = YES;
    NSString *directory = [NSString stringWithUTF8String:initial];
    if (directory) panel.directoryURL = [NSURL fileURLWithPath:directory isDirectory:YES];
    panels[key] = panel;
    // The system supplies its own localized labels. Never run a nested modal loop.
    [panel beginSheetModalForWindow:parent completionHandler:^(NSModalResponse response) {
        [panel orderOut:nil];
        [panels removeObjectForKey:key];
        if (response != NSModalResponseOK) { portico_folder_completed(request, NULL, 0, 1); return; }
        NSURL *url = panel.URL;
        const char *path = url.isFileURL ? url.path.UTF8String : NULL;
        if (!path) { portico_folder_completed(request, NULL, 0, 2); return; }
        portico_folder_completed(request, (const unsigned char *)path, strlen(path), 0);
    }];
}
void portico_folder_cancel(const char *operation) {
    NSString *key = [NSString stringWithUTF8String:operation];
    [panels[key] cancel:nil];
}
void portico_folder_cancel_all(void) {
    for (NSOpenPanel *panel in panels.allValues) [panel cancel:nil];
}
int portico_folder_open(const char *path) {
    NSString *directory = [NSString stringWithUTF8String:path];
    if (!directory) return 0;
    return [[NSWorkspace sharedWorkspace] openURL:[NSURL fileURLWithPath:directory isDirectory:YES]] ? 1 : 0;
}
