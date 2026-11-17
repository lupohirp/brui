package bru

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
)

var varRegex = regexp.MustCompile(`\{\{([a-zA-Z0-9_$-]+)\}\}`)

func GetVariable(varName string, coll *BrunoCollection) (string, bool) {
	val, _, ok := LookupVariable(varName, coll)
	return val, ok
}

// Variable sources reported by LookupVariable.
const (
	SourceRuntime = "runtime"
	SourceEnv     = "env"
	SourceProcess = "process env"
)

// LookupVariable resolves a variable like GetVariable and also reports where
// the value comes from (SourceRuntime, SourceEnv or SourceProcess).
func LookupVariable(varName string, coll *BrunoCollection) (value, source string, ok bool) {
	// 1. Runtime variables (set by scripts)
	if val, ok := coll.RuntimeVariables[varName]; ok {
		return fmt.Sprintf("%v", val), SourceRuntime, true
	}

	// 2. Active environment
	if coll.ActiveEnvironment != "" {
		for _, env := range coll.Environments {
			if env.Name == coll.ActiveEnvironment {
				if val, ok := env.Variables[varName]; ok {
					return val, SourceEnv, true
				}
			}
		}
	}

	// 3. Process environment
	if val, ok := os.LookupEnv(varName); ok {
		return val, SourceProcess, true
	}

	return "", "", false
}

// IsDynamicVariable reports whether name is generated at send time
// ({{$uuid}}, {{$timestamp}}, ...).
func IsDynamicVariable(name string) bool {
	switch name {
	case "$uuid", "uuid", "$guid", "guid", "$timestamp", "timestamp",
		"$isoTimestamp", "isoTimestamp", "$randomInt", "randomInt":
		return true
	}
	return false
}

func Interpolate(text string, coll *BrunoCollection) string {
	if text == "" {
		return ""
	}

	return varRegex.ReplaceAllStringFunc(text, func(m string) string {
		match := varRegex.FindStringSubmatch(m)
		if len(match) < 2 {
			return m
		}
		varName := match[1]

		// Dynamic variables
		switch varName {
		case "$uuid", "uuid", "$guid", "guid":
			return uuid.NewString()
		case "$timestamp", "timestamp":
			return strconv.FormatInt(time.Now().Unix(), 10)
		case "$isoTimestamp", "isoTimestamp":
			return time.Now().UTC().Format(time.RFC3339)
		case "$randomInt", "randomInt":
			n, _ := rand.Int(rand.Reader, big.NewInt(1000))
			return strconv.FormatInt(n.Int64(), 10)
		}

		if val, ok := GetVariable(varName, coll); ok {
			return val
		}

		return m
	})
}
