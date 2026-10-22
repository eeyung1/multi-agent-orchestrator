// Package strictjson decodes bounded-depth, unambiguous JSON domain records.
// Callers supply their own byte limits and domain/version validation.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

func Decode(data []byte, value any) error {
	if !utf8.Valid(data) {
		return errors.New("invalid UTF-8 JSON")
	}
	check := json.NewDecoder(bytes.NewReader(data))
	check.UseNumber()
	if err := uniqueValue(check, 0, reflect.TypeOf(value)); err != nil {
		return err
	}
	if _, err := check.Token(); err != io.EOF {
		return errors.New("JSON contains trailing data")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(value)
}

func uniqueValue(d *json.Decoder, depth int, target reflect.Type) error {
	if depth > 32 {
		return errors.New("JSON nesting exceeds limit")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	for target != nil && target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	keys := map[string]bool{}
	for d.More() {
		var child reflect.Type
		if delim == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("invalid JSON object key")
			}
			// Go folds struct field names, but map keys (session/task IDs) are
			// case-sensitive. Do not reject valid distinct map identities.
			if target != nil && target.Kind() == reflect.Map {
				child = target.Elem()
			} else if target != nil && target.Kind() == reflect.Struct {
				for i := 0; i < target.NumField(); i++ {
					field := target.Field(i)
					tag := strings.Split(field.Tag.Get("json"), ",")[0]
					if tag == "" {
						tag = field.Name
					}
					if field.PkgPath == "" && tag != "-" && strings.EqualFold(name, tag) {
						name, child = tag, field.Type
						break
					}
				}
			}
			if keys[name] {
				return errors.New("duplicate JSON object key")
			}
			keys[name] = true
		} else if target != nil && (target.Kind() == reflect.Array || target.Kind() == reflect.Slice) {
			child = target.Elem()
		}
		if err := uniqueValue(d, depth+1, child); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}
