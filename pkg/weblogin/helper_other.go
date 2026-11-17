//go:build !darwin

package weblogin

const supported = false

func runHelper(authURL, callback string) int { return exitFailed }
