package render

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

func goLiteral(value any) (string, error) {
	var b strings.Builder
	err := writeLiteral(&b, reflect.ValueOf(value))
	return b.String(), err
}

func writeLiteral(b *strings.Builder, value reflect.Value) error {
	if !value.IsValid() {
		b.WriteString("nil")
		return nil
	}
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			b.WriteString("nil")
			return nil
		}
		b.WriteByte('&')
		return writeLiteral(b, value.Elem())
	case reflect.Struct:
		fmt.Fprintf(b, "%s{\n", value.Type())
		for i := 0; i < value.NumField(); i++ {
			field := value.Field(i)
			if field.IsZero() || (field.Kind() == reflect.Slice || field.Kind() == reflect.Map) && field.Len() == 0 {
				continue
			}
			fmt.Fprintf(b, "%s: ", value.Type().Field(i).Name)
			if err := writeLiteral(b, field); err != nil {
				return err
			}
			b.WriteString(",\n")
		}
		b.WriteByte('}')
	case reflect.Slice, reflect.Map:
		if value.IsNil() {
			b.WriteString("nil")
			return nil
		}
		if value.Kind() == reflect.Slice && value.Type().Elem().Kind() == reflect.Uint8 {
			fmt.Fprintf(b, "[]byte(%q)", value.Bytes())
			return nil
		}
		fmt.Fprintf(b, "%s{", value.Type())
		if value.Kind() == reflect.Map {
			keys := value.MapKeys()
			sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
			for _, key := range keys {
				fmt.Fprintf(b, "%q: ", key.String())
				if err := writeLiteral(b, value.MapIndex(key)); err != nil {
					return err
				}
				b.WriteByte(',')
			}
		} else {
			for i := 0; i < value.Len(); i++ {
				if err := writeLiteral(b, value.Index(i)); err != nil {
					return err
				}
				b.WriteByte(',')
			}
		}
		b.WriteByte('}')
	case reflect.String, reflect.Bool, reflect.Int:
		fmt.Fprintf(b, "%#v", value.Interface())
	default:
		return fmt.Errorf("unsupported generated literal type %s", value.Type())
	}
	return nil
}
