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
	// 1. Check runtime variables
	if val, ok := coll.RuntimeVariables[varName]; ok {
		return fmt.Sprintf("%v", val), true
	}

	// 2. Check active environment
	if coll.ActiveEnvironment != "" {
		for _, env := range coll.Environments {
			if env.Name == coll.ActiveEnvironment {
				if val, ok := env.Variables[varName]; ok {
					return val, true
				}
			}
		}
	}

	// 3. Fallback to process env
	if val, ok := os.LookupEnv(varName); ok {
		return val, true
	}

	return "", false
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
