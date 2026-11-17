// Package weblogin provides the native OAuth2 login window used for the
// automatic authorization_code flow (macOS: a WebKit window, like Bruno's
// built-in browser, with nothing to install).
//
// The window runs in a child process (the brui binary re-executed with
// HelperArg) because AppKit must own the main thread, which in the parent
// belongs to the terminal UI. The child prints the redirect URL on stdout.
package weblogin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/lupohirp/brui/pkg/bru"
)

// HelperArg is the hidden first argument that turns brui into the login
// window process: brui HelperArg <authURL> <callbackURL>.
const HelperArg = "__oauth-login-window"

// Exit codes of the helper process.
const (
	exitOK         = 0 // redirect URL on stdout (empty = the user quit)
	exitFailed     = 1 // the window could not be shown; reason on stderr
	exitClosed     = 2 // the user closed the window
	exitParentGone = 3 // brui went away
)

// Window returns the native login window for this platform, or nil if there
// is none (then brui falls back to a Chromium-based browser or paste).
func Window() *bru.LoginWindow {
	if !supported {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	return newWindow("macOS WebKit", exe)
}

// RunHelper is the entry point of the helper process; it returns the exit
// code. args are the arguments after HelperArg.
func RunHelper(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: brui "+HelperArg+" <authorization-url> <callback-url>")
		return exitFailed
	}
	if !supported {
		fmt.Fprintln(os.Stderr, "the native login window is not available on this platform")
		return exitFailed
	}
	return runHelper(args[0], args[1])
}

func newWindow(name, exe string) *bru.LoginWindow {
	return &bru.LoginWindow{Name: name, Run: func(ctx context.Context, authURL, callback string) (string, error) {
		return launch(ctx, exe, authURL, callback)
	}}
}

// launch runs the helper and maps its outcome to the errors bru expects.
func launch(ctx context.Context, exe, authURL, callback string) (string, error) {
	cmd := exec.CommandContext(ctx, exe, HelperArg, authURL, callback)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// The helper exits when this pipe closes, so no window outlives brui.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("%w: %v", bru.ErrBrowserLaunch, err)
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("%w: cannot start the login window: %v", bru.ErrBrowserLaunch, err)
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		return "", fmt.Errorf("%w: %v", bru.ErrBrowserLaunch, err)
	}
	switch code {
	case exitOK:
		u := strings.TrimSpace(stdout.String())
		if u == "" {
			return "", bru.ErrLoginWindowClosed
		}
		return u, nil
	case exitClosed:
		return "", bru.ErrLoginWindowClosed
	default:
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = fmt.Sprintf("login window exited with status %d", code)
		}
		return "", fmt.Errorf("%w: %s", bru.ErrBrowserLaunch, reason)
	}
}
