package ui

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/lupohirp/brui/pkg/bru"
)

// envVarKeys lists an environment's variables and secrets, sorted.
func envVarKeys(env *bru.BrunoEnvironment) []string {
	seen := map[string]bool{}
	var keys []string
	for k := range env.Variables {
		seen[k] = true
		keys = append(keys, k)
	}
	for _, k := range env.SecretNames {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func maskSecret(s string) string { return strings.Repeat("•", utf8.RuneCountInString(s)) }

// openVarEditor opens the prompt to edit a variable or a secret.
func (m *Model) openVarEditor(env *bru.BrunoEnvironment, key string) {
	if env == nil {
		return
	}
	m.IsEnvPromptOpen = true
	m.EnvPromptTempKey = key
	m.EnvPromptReveal = false
	if env.IsSecret(key) {
		val, _, _ := m.Coll.Secrets.Value(m.Coll.Path, env.Name, key)
		m.EnvPromptTitle = fmt.Sprintf("EDIT SECRET [%s]", key)
		m.EnvPromptText = val
		m.EnvPromptAction = "edit_secret"
	} else {
		m.EnvPromptTitle = fmt.Sprintf("EDIT VARIABLE [%s]", key)
		m.EnvPromptText = env.Variables[key]
		m.EnvPromptAction = "edit_var"
	}
	m.EnvPromptCursor = len(m.EnvPromptText)
}

// storeSecret saves a secret value in brui's store; an empty value removes
// brui's copy (Bruno's value, if any, applies again).
func (m *Model) storeSecret(env *bru.BrunoEnvironment, key, val string) string {
	var err error
	if val == "" {
		err = m.Coll.Secrets.Delete(m.Coll.Path, env.Name, key)
	} else {
		err = m.Coll.Secrets.Set(m.Coll.Path, env.Name, key, val)
	}
	switch {
	case err != nil:
		return fmt.Sprintf("Cannot save secret [%s]: %v", key, err)
	case val != "":
		return fmt.Sprintf("Secret [%s] saved (encrypted, not in the collection)", key)
	}
	if _, from, ok := m.Coll.Secrets.Value(m.Coll.Path, env.Name, key); ok {
		return fmt.Sprintf("Secret [%s]: brui value removed, using the one from %s", key, from)
	}
	return fmt.Sprintf("Secret [%s] cleared", key)
}

// toggleSecret turns a variable into a secret (its value leaves the .bru
// file) or a secret back into a plain variable.
func (m *Model) toggleSecret(env *bru.BrunoEnvironment, key string) {
	if env == nil {
		return
	}
	if env.IsSecret(key) {
		val, _, _ := m.Coll.Secrets.Value(m.Coll.Path, env.Name, key)
		env.SetSecret(key, false)
		env.Variables[key] = val
		_ = m.Coll.Secrets.Delete(m.Coll.Path, env.Name, key)
		if m.saveEnvironment(env) == nil {
			m.StatusMsg = fmt.Sprintf("[%s] is now a plain variable: its value is saved in %s", key, env.Name+".bru")
		}
		return
	}
	val := env.Variables[key]
	if val != "" {
		if err := m.Coll.Secrets.Set(m.Coll.Path, env.Name, key, val); err != nil {
			m.StatusMsg = fmt.Sprintf("Cannot make [%s] secret: %v", key, err)
			return
		}
	}
	delete(env.Variables, key)
	env.SetSecret(key, true)
	if m.saveEnvironment(env) == nil {
		m.StatusMsg = fmt.Sprintf("[%s] is now secret: removed from %s, value stored encrypted", key, env.Name+".bru")
	}
}

// secretVarLine renders a secret in the environment manager (plain text,
// so the selection highlight applies to the whole row).
func (m Model) secretVarLine(env *bru.BrunoEnvironment, key string, width int) string {
	val, from, ok := m.Coll.Secrets.Value(m.Coll.Path, env.Name, key)
	var shown string
	switch {
	case ok && val != "":
		shown = "••••••••  (" + from + ")"
	case m.Coll.Secrets.Problem(m.Coll.Path, env.Name, key) != nil:
		shown = "unreadable — press Enter to set it"
	default:
		shown = "not set — press Enter"
	}
	return ansi.Truncate(" "+key+" [secret]: "+shown, width, "…") // no emoji: widths vary by terminal
}
