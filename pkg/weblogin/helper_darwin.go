//go:build darwin

package weblogin

import (
	"fmt"
	"html"
	"io"
	"os"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/lupohirp/brui/pkg/bru"
)

// The window is built with AppKit + WebKit through the Objective-C runtime
// (purego), so brui still builds with CGO_ENABLED=0 for every platform.

const supported = true

func init() {
	// AppKit must run on the main thread. Locking it in an init function makes
	// main() run on the main thread; harmless for the terminal UI.
	runtime.LockOSThread()
}

// WKNavigationActionPolicy
const (
	policyCancel = 0
	policyAllow  = 1
)

type cgSize struct{ Width, Height float64 }

func sel(name string) objc.SEL                          { return objc.RegisterName(name) }
func class(name string) objc.ID                         { return objc.ID(objc.GetClass(name)) }
func send(id objc.ID, name string, args ...any) objc.ID { return id.Send(sel(name), args...) }
func alloc(name string) objc.ID                         { return send(class(name), "alloc") }
func nsString(s string) objc.ID                         { return send(class("NSString"), "stringWithUTF8String:", s) }

func goString(ns objc.ID) string {
	if ns == 0 {
		return ""
	}
	return objc.Send[string](ns, sel("UTF8String"))
}

// callBlock invokes an Objective-C block taking one NSInteger, such as the
// decisionHandler WebKit passes to the navigation delegate. A block literal
// is {isa, flags, reserved, invoke, ...}; invoke takes the block itself first.
func callBlock(block objc.ID, arg int) {
	p := *(*unsafe.Pointer)(unsafe.Pointer(&block))
	invoke := *(*uintptr)(unsafe.Add(p, 2*unsafe.Sizeof(uintptr(0))))
	purego.SyscallN(invoke, uintptr(block), uintptr(arg))
}

func runHelper(authURL, callback string) int {
	for _, fw := range []string{
		"/System/Library/Frameworks/AppKit.framework/AppKit",
		"/System/Library/Frameworks/WebKit.framework/WebKit",
	} {
		if _, err := purego.Dlopen(fw, purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
			fmt.Fprintf(os.Stderr, "cannot load %s: %v\n", fw, err)
			return exitFailed
		}
	}
	// brui keeps our stdin open; EOF means it is gone (quit, crash, cancel).
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(exitParentGone)
	}()

	var protocols []*objc.Protocol
	for _, name := range []string{"WKNavigationDelegate", "WKUIDelegate", "NSWindowDelegate"} {
		if p := objc.GetProtocol(name); p != nil {
			protocols = append(protocols, p)
		}
	}
	delegateClass, err := objc.RegisterClass("BruiLoginDelegate", objc.GetClass("NSObject"), protocols, nil, []objc.MethodDef{
		{
			// Every navigation is checked before it starts: the one to the
			// callback is cancelled (the real page never loads) and returned.
			Cmd: sel("webView:decidePolicyForNavigationAction:decisionHandler:"),
			Fn: func(_ objc.ID, _ objc.SEL, _, action, handler objc.ID) {
				u := goString(send(send(send(action, "request"), "URL"), "absoluteString"))
				if u != "" && bru.SameEndpoint(u, callback) {
					callBlock(handler, policyCancel)
					fmt.Fprintln(os.Stdout, u)
					os.Exit(exitOK)
				}
				callBlock(handler, policyAllow)
			},
		},
		{
			// Links/popups that want a new window open in the same one.
			Cmd: sel("webView:createWebViewWithConfiguration:forNavigationAction:windowFeatures:"),
			Fn: func(_ objc.ID, _ objc.SEL, webView, _, action, _ objc.ID) objc.ID {
				send(webView, "loadRequest:", send(action, "request"))
				return 0
			},
		},
		{
			Cmd: sel("webView:didFailProvisionalNavigation:withError:"),
			Fn: func(_ objc.ID, _ objc.SEL, webView, _, nsErr objc.ID) {
				code := objc.Send[int](nsErr, sel("code"))
				if code == -999 || code == 102 { // cancelled / interrupted by policy
					return
				}
				msg := html.EscapeString(goString(send(nsErr, "localizedDescription")))
				page := `<!doctype html><body style="font-family:-apple-system,sans-serif;padding:2em">` +
					`<h3>Cannot load the login page</h3><p>` + msg + `</p><p>Close this window to cancel.</p></body>`
				send(webView, "loadHTMLString:baseURL:", nsString(page), objc.ID(0))
			},
		},
		{
			Cmd: sel("windowWillClose:"),
			Fn:  func(_ objc.ID, _ objc.SEL, _ objc.ID) { os.Exit(exitClosed) },
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot create the window delegate: %v\n", err)
		return exitFailed
	}

	target := send(class("NSURL"), "URLWithString:", nsString(authURL))
	if target == 0 {
		fmt.Fprintf(os.Stderr, "invalid authorization URL: %s\n", authURL)
		return exitFailed
	}

	app := send(class("NSApplication"), "sharedApplication")
	send(app, "setActivationPolicy:", 0) // regular app: window in front, Dock icon
	setupMenu(app)

	delegate := send(objc.ID(delegateClass), "alloc")
	delegate = send(delegate, "init")
	web := send(alloc("WKWebView"), "init")
	send(web, "setNavigationDelegate:", delegate)
	send(web, "setUIDelegate:", delegate)

	vc := send(alloc("NSViewController"), "init")
	send(vc, "setView:", web)
	win := send(class("NSWindow"), "windowWithContentViewController:", vc)
	if win == 0 {
		fmt.Fprintln(os.Stderr, "cannot create the login window")
		return exitFailed
	}
	send(win, "setTitle:", nsString("brui — OAuth2 login"))
	send(win, "setContentSize:", cgSize{Width: 960, Height: 760})
	send(win, "center")
	send(win, "setReleasedWhenClosed:", false)
	send(win, "setDelegate:", delegate)

	send(web, "loadRequest:", send(class("NSURLRequest"), "requestWithURL:", target))
	send(win, "makeKeyAndOrderFront:", objc.ID(0))
	send(app, "activateIgnoringOtherApps:", true)
	send(app, "run")
	return exitClosed // only reached if the app loop stops without a result
}

// setupMenu adds the standard menus so Cmd+C/V/X/A/Z (needed to paste
// credentials or use a password manager), Cmd+W and Cmd+Q work.
func setupMenu(app objc.ID) {
	menu := func(title string) objc.ID { return send(alloc("NSMenu"), "initWithTitle:", nsString(title)) }
	item := func(m objc.ID, title, action, key string) {
		send(m, "addItemWithTitle:action:keyEquivalent:", nsString(title), sel(action), nsString(key))
	}
	appMenu := menu("brui")
	item(appMenu, "Close Window", "performClose:", "w")
	item(appMenu, "Quit", "terminate:", "q")
	edit := menu("Edit")
	item(edit, "Undo", "undo:", "z")
	item(edit, "Redo", "redo:", "Z")
	item(edit, "Cut", "cut:", "x")
	item(edit, "Copy", "copy:", "c")
	item(edit, "Paste", "paste:", "v")
	item(edit, "Select All", "selectAll:", "a")

	bar := menu("")
	for _, sub := range []objc.ID{appMenu, edit} {
		holder := send(alloc("NSMenuItem"), "init")
		send(holder, "setSubmenu:", sub)
		send(bar, "addItem:", holder)
	}
	send(app, "setMainMenu:", bar)
}
