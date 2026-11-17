package bru

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/dop251/goja"
)

// installShims adds the small subset of browser/Node globals that Bruno
// scripts commonly rely on: atob, btoa and Buffer.from(...).toString(...).
func (sr *ScriptRunner) installShims() {
	vm := sr.vm
	_ = vm.Set("atob", func(s string) (string, error) {
		b, err := decodeBase64Lenient(s)
		if err != nil {
			return "", err
		}
		return latin1String(b), nil
	})
	_ = vm.Set("btoa", func(s string) (string, error) {
		b, err := latin1Bytes(s)
		if err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(b), nil
	})

	buffer := vm.NewObject()
	_ = buffer.Set("from", func(call goja.FunctionCall) goja.Value {
		input := call.Argument(0).String()
		enc := "utf8"
		if a := call.Argument(1); !goja.IsUndefined(a) {
			enc = strings.ToLower(a.String())
		}
		b, err := decodeBytes(input, enc)
		if err != nil {
			panic(vm.NewTypeError(err.Error()))
		}
		return sr.newBuffer(b)
	})
	_ = buffer.Set("isBuffer", func(call goja.FunctionCall) goja.Value {
		obj, ok := call.Argument(0).(*goja.Object)
		return vm.ToValue(ok && obj.Get("__brui_buffer") != nil)
	})
	_ = vm.Set("Buffer", buffer)
}

func (sr *ScriptRunner) newBuffer(b []byte) goja.Value {
	vm := sr.vm
	obj := vm.NewObject()
	_ = obj.Set("__brui_buffer", true)
	_ = obj.Set("length", len(b))
	_ = obj.Set("toString", func(call goja.FunctionCall) goja.Value {
		enc := "utf8"
		if a := call.Argument(0); !goja.IsUndefined(a) {
			enc = strings.ToLower(a.String())
		}
		s, err := encodeBytes(b, enc)
		if err != nil {
			panic(vm.NewTypeError(err.Error()))
		}
		return vm.ToValue(s)
	})
	return obj
}

func decodeBytes(s, enc string) ([]byte, error) {
	switch enc {
	case "utf8", "utf-8":
		return []byte(s), nil
	case "base64", "base64url":
		return decodeBase64Lenient(s)
	case "hex":
		return hex.DecodeString(s)
	case "latin1", "binary", "ascii":
		return latin1Bytes(s)
	}
	return nil, errors.New("unsupported encoding: " + enc)
}

func encodeBytes(b []byte, enc string) (string, error) {
	switch enc {
	case "utf8", "utf-8":
		return strings.ToValidUTF8(string(b), string(utf8.RuneError)), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(b), nil
	case "base64url":
		return base64.RawURLEncoding.EncodeToString(b), nil
	case "hex":
		return hex.EncodeToString(b), nil
	case "latin1", "binary", "ascii":
		return latin1String(b), nil
	}
	return "", errors.New("unsupported encoding: " + enc)
}

// decodeBase64Lenient accepts standard and URL-safe alphabets, with or without
// padding or whitespace, like Node's Buffer.from(s, "base64").
func decodeBase64Lenient(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '-':
			return '+'
		case '_':
			return '/'
		case '=', ' ', '\n', '\r', '\t':
			return -1
		}
		return r
	}, s)
	return base64.RawStdEncoding.DecodeString(s)
}

func latin1String(b []byte) string {
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

func latin1Bytes(s string) ([]byte, error) {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 255 {
			return nil, errors.New("string contains characters outside the Latin1 range")
		}
		b = append(b, byte(r))
	}
	return b, nil
}
