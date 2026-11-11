#import <Cocoa/Cocoa.h>
#import <ServiceManagement/ServiceManagement.h>
#include "macos_darwin.h"

static int gLaunchedAtLogin = 0;

// The open-application Apple event is only current while the app finishes
// launching, so look at it from a notification observer registered at load.
__attribute__((constructor)) static void lolad_watch_launch(void) {
	[[NSNotificationCenter defaultCenter]
		addObserverForName:NSApplicationWillFinishLaunchingNotification
					object:nil
					 queue:nil
				usingBlock:^(NSNotification *n) {
		NSAppleEventDescriptor *ev = [[NSAppleEventManager sharedAppleEventManager] currentAppleEvent];
		NSAppleEventDescriptor *prop = [ev paramDescriptorForKeyword:keyAEPropData];
		if (prop && [prop enumCodeValue] == keyAELaunchedAsLogInItem) {
			gLaunchedAtLogin = 1;
		}
	}];
}

int lolad_launched_at_login(void) { return gLaunchedAtLogin; }

static char *dup_err(NSError *e, const char *fallback) {
	NSString *s = e ? e.localizedDescription : [NSString stringWithUTF8String:fallback];
	return strdup(s.UTF8String);
}

int lolad_login_status(void) {
	if (@available(macOS 13.0, *)) {
		switch ([SMAppService mainAppService].status) {
		case SMAppServiceStatusNotRegistered: return 0;
		case SMAppServiceStatusEnabled: return 1;
		case SMAppServiceStatusRequiresApproval: return 2;
		case SMAppServiceStatusNotFound: return 3;
		}
		return 0;
	}
	return -1;
}

int lolad_login_set(int on, char **err) {
	if (@available(macOS 13.0, *)) {
		NSError *e = nil;
		BOOL ok = on ? [[SMAppService mainAppService] registerAndReturnError:&e]
					 : [[SMAppService mainAppService] unregisterAndReturnError:&e];
		if (!ok) {
			// Unregistering something that is not registered is not an error here.
			if (!on && e && e.code == kSMErrorJobNotFound) return 0;
			*err = dup_err(e, "login item change failed");
			return 1;
		}
		return 0;
	}
	*err = strdup("Login items need macOS 13 or newer");
	return 1;
}

void lolad_open_login_settings(void) {
	if (@available(macOS 13.0, *)) {
		[SMAppService openSystemSettingsLoginItems];
	}
}

void lolad_set_dock_visible(int visible) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[NSApp setActivationPolicy:visible ? NSApplicationActivationPolicyRegular : NSApplicationActivationPolicyAccessory];
		if (visible) [NSApp activateIgnoringOtherApps:YES];
	});
}

int lolad_trash(const char *path, char **err) {
	NSError *e = nil;
	NSURL *u = [NSURL fileURLWithPath:[NSString stringWithUTF8String:path]];
	if (![[NSFileManager defaultManager] trashItemAtURL:u resultingItemURL:nil error:&e]) {
		*err = dup_err(e, "could not move to the Trash");
		return 1;
	}
	return 0;
}
