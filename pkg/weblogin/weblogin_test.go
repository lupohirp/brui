package weblogin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lupohirp/brui/pkg/bru"
)

// The test binary doubles as the helper process; WEBLOGIN_FAKE picks the
// outcome it simulates.
func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == HelperArg {
		callback := os.Args[3]
		switch os.Getenv("WEBLOGIN_FAKE") {
		case "ok":
			fmt.Println(callback + "?code=abc&state=xyz")
			os.Exit(exitOK)
		case "quit":
			os.Exit(exitOK)
		case "closed":
			os.Exit(exitClosed)
		case "fail":
			fmt.Fprintln(os.Stderr, "WebKit unavailable")
			os.Exit(exitFailed)
		case "hang":
			time.Sleep(time.Minute)
		}
		os.Exit(99)
	}
	os.Exit(m.Run())
}

func runFake(t *testing.T, mode string, ctx context.Context) (string, error) {
	t.Helper()
	t.Setenv("WEBLOGIN_FAKE", mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return newWindow("test", exe).Run(ctx, "https://idp/authorize", "https://app/cb")
}

func TestLauncherOutcomes(t *testing.T) {
	u, err := runFake(t, "ok", context.Background())
	if err != nil || u != "https://app/cb?code=abc&state=xyz" {
		t.Fatalf("ok: %q %v", u, err)
	}
	for _, mode := range []string{"closed", "quit"} {
		if _, err := runFake(t, mode, context.Background()); !errors.Is(err, bru.ErrLoginWindowClosed) {
			t.Fatalf("%s: want ErrLoginWindowClosed, got %v", mode, err)
		}
	}
	_, err = runFake(t, "fail", context.Background())
	if !errors.Is(err, bru.ErrBrowserLaunch) || !strings.Contains(err.Error(), "WebKit unavailable") {
		t.Fatalf("fail: want ErrBrowserLaunch with reason, got %v", err)
	}
}

func TestLauncherCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := runFake(t, "hang", ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context error, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancel did not kill the login window")
	}
}

func TestStartFailureIsLaunchError(t *testing.T) {
	_, err := newWindow("x", "/nonexistent/brui").Run(context.Background(), "https://idp", "https://app/cb")
	if !errors.Is(err, bru.ErrBrowserLaunch) {
		t.Fatalf("want ErrBrowserLaunch, got %v", err)
	}
}

func TestWindowAvailability(t *testing.T) {
	if (Window() != nil) != supported {
		t.Fatalf("Window() availability must match the platform (supported=%v)", supported)
	}
}
