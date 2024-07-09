package helm

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Foundation/Foundation.h>
#import <ApplicationServices/ApplicationServices.h>

extern CGEventRef handleEvent(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *refcon);

static inline CFMachPortRef CreateEventTap() {
	CGEventMask eventMask = {
		CGEventMaskBit(kCGEventKeyDown)
	};

	CFMachPortRef eventTap = CGEventTapCreate(
		kCGHIDEventTap, kCGHeadInsertEventTap, kCGEventTapOptionListenOnly, eventMask, handleEvent, NULL
	);

	// Exit the program if unable to create the event tap.
	if(!eventTap) {
		fprintf(stderr, "ERROR: Unable to create event tap.\n");
		exit(1);
	}

	CGEventTapEnable(eventTap, true);

	return eventTap;
}
*/
import "C"
import (
	"unsafe"

	"fmt"

	"github.com/charmbracelet/log"
	"github.com/progrium/macdriver/macos/appkit"
	"github.com/progrium/macdriver/macos/foundation"
	"github.com/progrium/macdriver/objc"
)

var (
	tap C.CFMachPortRef
)

func Intercept() {
	app := appkit.Application_SharedApplication()

	ad := &appkit.ApplicationDelegate{}
	ad.SetApplicationDidFinishLaunching(func(foundation.Notification) {
		log.Info("App started.")
		setupEventTap()
		setSystemBar(app)
	})

	ad.SetApplicationShouldTerminateAfterLastWindowClosed(func(appkit.Application) bool {
		return false
	})
	app.SetDelegate(ad)

	// appkit.Event_AddGlobalMonitorForEventsMatchingMaskHandler(appkit.EventMaskAny, KeyDown)

	app.SetActivationPolicy(appkit.ApplicationActivationPolicyRegular)
	app.ActivateIgnoringOtherApps(true)
	app.Run()
}

func setSystemBar(app appkit.Application) {
	item := appkit.StatusBar_SystemStatusBar().StatusItemWithLength(appkit.VariableStatusItemLength)
	objc.Retain(&item)
	img := appkit.Image_ImageWithSystemSymbolNameAccessibilityDescription("multiply.circle.fill", "A multiply symbol inside a filled circle.")
	item.Button().SetImage(img)

	menu := appkit.NewMenuWithTitle("main")
	menu.AddItem(appkit.NewMenuItemWithAction("Hide", "h", func(sender objc.Object) { app.Hide(nil) }))
	menu.AddItem(appkit.NewMenuItemWithAction("Quit", "q", func(sender objc.Object) { app.Terminate(nil) }))
	item.SetMenu(menu)
}

//export handleEvent
func handleEvent(proxy C.CGEventTapProxy, et C.CGEventType, event C.CGEventRef, refcon *C.void) C.CGEventRef {
	log.Infof("%v, %v, %v, %v", proxy, et, event, refcon)
	return event
}

func setupEventTap() error {
	tap = C.CreateEventTap()

	if unsafe.Pointer(&tap) == nil {
		return fmt.Errorf("unable to create event tap")
	}

	return nil
}
